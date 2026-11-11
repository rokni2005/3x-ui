"""ساخت ایمیج کانتینر بازوی مهرمان برای RouterOS بدون نیاز به Docker.

خروجی یک فایل tar با قالب `docker save` است که RouterOS با `/container add file=...` می‌شناسد.
ایمیج = python:3.12-slim (amd64 از Docker Hub) + یک لایه‌ی اضافه شامل:
  - کتابخانه‌های requirements.txt (چرخ‌های manylinux برای cp312/x86_64، با pip download)
  - کد بازو در /app ، نسخه‌ی اولیه‌ی content/ و knowledge/ در /app/_seed
  - /app/deploy/routeros/entrypoint.sh  (پوشه‌ی ماندگار را آماده می‌کند و بازو را اجرا می‌کند)

اجرا (از پوشه‌ی mehraman-bot):
    python deploy/routeros/build_image_tar.py mehraman-bot.tar [--arch amd64|arm64]
نیاز: پایتون ۳.۱۰+ و pip، دسترسی اینترنت به Docker Hub و PyPI.
"""
from __future__ import annotations

import argparse
import gzip
import hashlib
import io
import json
import os
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.request
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]          # پوشه‌ی mehraman-bot
BASE_REPO, BASE_TAG = "library/python", "3.12-slim"
SITE = "usr/local/lib/python3.12/site-packages"
PIP_PLAT = {"amd64": "manylinux2014_x86_64", "arm64": "manylinux2014_aarch64"}
SKIP_DIRS = {"__pycache__", ".pytest_cache", "tests", ".git", "data"}
SKIP_FILES = {".env"}


def hub_get(url: str, token: str, accept: str | None = None) -> urllib.request.addinfourl:
    h = {"Authorization": "Bearer " + token}
    if accept:
        h["Accept"] = accept
    return urllib.request.urlopen(urllib.request.Request(url, headers=h), timeout=300)


def pull_base(arch: str, work: Path) -> tuple[dict, list[Path]]:
    tok = json.loads(urllib.request.urlopen(
        f"https://auth.docker.io/token?service=registry.docker.io&scope=repository:{BASE_REPO}:pull",
        timeout=60).read())["token"]
    reg = f"https://registry-1.docker.io/v2/{BASE_REPO}"
    idx = json.loads(hub_get(f"{reg}/manifests/{BASE_TAG}", tok,
                             "application/vnd.oci.image.index.v1+json,"
                             "application/vnd.docker.distribution.manifest.list.v2+json").read())
    dig = next(m["digest"] for m in idx["manifests"]
               if m["platform"].get("architecture") == arch and m["platform"].get("os") == "linux")
    man = json.loads(hub_get(f"{reg}/manifests/{dig}", tok,
                             "application/vnd.oci.image.manifest.v1+json,"
                             "application/vnd.docker.distribution.manifest.v2+json").read())
    cfg_raw = hub_get(f"{reg}/blobs/{man['config']['digest']}", tok).read()
    assert "sha256:" + hashlib.sha256(cfg_raw).hexdigest() == man["config"]["digest"], "config digest"
    layers = []
    for i, l in enumerate(man["layers"]):
        gz = work / f"base{i}.gz"
        if not (gz.exists() and gz.stat().st_size == l["size"]):
            with hub_get(f"{reg}/blobs/{l['digest']}", tok) as r, open(gz, "wb") as f:
                shutil.copyfileobj(r, f, 1 << 20)
        h = hashlib.sha256(gz.read_bytes()).hexdigest()
        assert "sha256:" + h == l["digest"], f"base layer {i} digest"
        raw = work / f"base{i}.tar"
        with gzip.open(gz, "rb") as s, open(raw, "wb") as d:
            shutil.copyfileobj(s, d, 1 << 20)
        layers.append(raw)
        print(f"base layer {i}: {l['size'] // 1024} KiB")
    print("base", BASE_REPO + ":" + BASE_TAG, arch, dig)
    return json.loads(cfg_raw), layers


def download_wheels(arch: str, dest: Path) -> list[Path]:
    subprocess.check_call([sys.executable, "-m", "pip", "download", "-q", "--only-binary=:all:",
                           "--platform", PIP_PLAT[arch], "--python-version", "3.12",
                           "--implementation", "cp", "--abi", "cp312",
                           "-r", str(ROOT / "requirements.txt"), "-d", str(dest)])
    wheels = sorted(dest.glob("*.whl"))
    print("wheels:", ", ".join(w.name for w in wheels))
    return wheels


def _ti(name: str, size: int = 0, mode: int = 0o644, kind: bytes = tarfile.REGTYPE) -> tarfile.TarInfo:
    ti = tarfile.TarInfo(name)
    ti.size, ti.mode, ti.type, ti.mtime, ti.uid, ti.gid = size, mode, kind, int(time.time()), 0, 0
    return ti


def build_app_layer(wheels: list[Path], out: Path) -> None:
    dirs: set[str] = set()

    def add_dirs(t: tarfile.TarFile, path: str) -> None:
        parts = path.split("/")[:-1]
        for i in range(1, len(parts) + 1):
            d = "/".join(parts[:i])
            if d and d not in dirs:
                dirs.add(d)
                t.addfile(_ti(d, mode=0o755, kind=tarfile.DIRTYPE))

    def add_bytes(t: tarfile.TarFile, path: str, data: bytes, mode: int = 0o644) -> None:
        add_dirs(t, path)
        t.addfile(_ti(path, len(data), mode), io.BytesIO(data))

    with tarfile.open(out, "w") as t:
        for w in wheels:                                  # نصب چرخ‌ها = باز کردن zip در site-packages
            with zipfile.ZipFile(w) as z:
                for n in z.namelist():
                    if n.endswith("/"):
                        continue
                    if ".data/" in n.split("/", 1)[0] + "/":   # purelib/platlib داخل .data
                        rest = n.split("/", 2)
                        if len(rest) < 3 or rest[1] not in ("purelib", "platlib"):
                            continue
                        n_out = rest[2]
                    else:
                        n_out = n
                    add_bytes(t, f"{SITE}/{n_out}", z.read(n))
        for p in sorted(ROOT.rglob("*")):                 # کد و محتوای اولیه
            rel = p.relative_to(ROOT)
            if p.is_dir() or set(rel.parts) & SKIP_DIRS or p.name in SKIP_FILES or p.suffix == ".tar":
                continue
            data = p.read_bytes()
            mode = 0o755 if p.suffix == ".sh" else 0o644
            add_bytes(t, f"app/{rel.as_posix()}", data, mode)
            if rel.parts[0] in ("content", "knowledge"):
                add_bytes(t, f"app/_seed/{rel.as_posix()}", data, mode)
        add_bytes(t, "app/_seed/data/.keep", b"")


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("out")
    ap.add_argument("--arch", default="amd64", choices=sorted(PIP_PLAT))
    a = ap.parse_args()
    out = Path(a.out).resolve()
    work = Path(tempfile.mkdtemp(prefix="mehraman-img-"))
    try:
        cfg, base_layers = pull_base(a.arch, work)
        wheels = download_wheels(a.arch, work / "wheels")
        app_layer = work / "app.tar"
        build_app_layer(wheels, app_layer)
        diff = "sha256:" + hashlib.sha256(app_layer.read_bytes()).hexdigest()

        c = cfg.setdefault("config", {})
        env = [e for e in c.get("Env", []) if not e.startswith(("PYTHONUNBUFFERED=", "PYTHONDONTWRITEBYTECODE="))]
        c["Env"] = env + ["PYTHONUNBUFFERED=1", "PYTHONDONTWRITEBYTECODE=1", "STATE_DIR=/srv/mehraman"]
        c["WorkingDir"] = "/app"
        c["Entrypoint"] = ["sh", "/app/deploy/routeros/entrypoint.sh"]
        c["Cmd"] = []
        c.pop("User", None)
        cfg["rootfs"]["diff_ids"].append(diff)
        cfg.setdefault("history", []).append({"created": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                                              "created_by": "mehraman-bot build_image_tar.py"})
        cfg_raw = json.dumps(cfg, separators=(",", ":")).encode()
        cname = hashlib.sha256(cfg_raw).hexdigest() + ".json"

        with tarfile.open(out, "w") as t:
            t.addfile(_ti(cname, len(cfg_raw)), io.BytesIO(cfg_raw))
            names = []
            for i, raw in enumerate(base_layers + [app_layer]):
                n = f"layer{i}/layer.tar"
                t.add(str(raw), n)
                names.append(n)
            m = json.dumps([{"Config": cname, "RepoTags": ["mehraman-bot:latest"], "Layers": names}]).encode()
            t.addfile(_ti("manifest.json", len(m)), io.BytesIO(m))
        print(f"written {out} ({out.stat().st_size // 1048576} MiB), app layer {diff[:19]}")
    finally:
        shutil.rmtree(work, ignore_errors=True)


if __name__ == "__main__":
    main()
