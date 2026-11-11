"""تبدیل فایل‌های docx/pdf/txt به Markdown قابل‌استفاده برای محتوای پشتیبان.

    python -m mehraman_bot.ingest فایل.docx [--title "عنوان"] [--audience internal]

docx با کتابخانه‌ی استاندارد پایتون خوانده می‌شود. برای pdf باید `pdftotext` نصب باشد؛
توجه: استخراج PDF فارسی گاهی ترتیب/نیم‌فاصله را خراب می‌کند و بهتر است منبع docx باشد.
"""
from __future__ import annotations

import argparse
import re
import shutil
import subprocess
import sys
import zipfile
from pathlib import Path
from xml.etree import ElementTree as ET

W = "{http://schemas.openxmlformats.org/wordprocessingml/2006/main}"


def _para_text(p) -> str:
    out = []
    for node in p.iter():
        if node.tag == W + "t" and node.text:
            out.append(node.text)
        elif node.tag in (W + "br", W + "tab"):
            out.append(" ")
    return "".join(out).strip()


def docx_to_markdown(data: bytes | Path) -> str:
    import io
    src = io.BytesIO(data) if isinstance(data, (bytes, bytearray)) else data
    with zipfile.ZipFile(src) as z:
        root = ET.fromstring(z.read("word/document.xml"))
    body = root.find(W + "body")
    lines: list[str] = []
    for el in body:
        if el.tag == W + "p":
            text = _para_text(el)
            if not text:
                lines.append("")
                continue
            style = el.find(f"{W}pPr/{W}pStyle")
            val = style.get(W + "val", "") if style is not None else ""
            m = re.search(r"(?:Heading|heading|عنوان)\s*(\d)", val)
            lines.append(f"{'#' * min(int(m.group(1)), 4)} {text}" if m else text)
            lines.append("")
        elif el.tag == W + "tbl":
            for tr in el.iter(W + "tr"):
                cells = [" ".join(_para_text(p) for p in tc.iter(W + "p")).strip()
                         for tc in tr.findall(W + "tc")]
                lines.append(" | ".join(c for c in cells if c))
            lines.append("")
    return re.sub(r"\n{3,}", "\n\n", "\n".join(lines)).strip() + "\n"


def pdf_to_text(path: Path) -> str:
    if not shutil.which("pdftotext"):
        raise RuntimeError("برای pdf باید pdftotext نصب باشد (بسته poppler-utils)؛ یا فایل را به docx تبدیل کنید.")
    return subprocess.run(["pdftotext", str(path), "-"], capture_output=True, text=True, check=True).stdout


def to_markdown(path: Path, title: str | None = None, audience: str = "internal") -> str:
    ext = path.suffix.lower()
    if ext == ".docx":
        body = docx_to_markdown(path)
    elif ext == ".pdf":
        body = pdf_to_text(path)
    elif ext in (".md", ".txt"):
        body = path.read_text(encoding="utf-8")
    else:
        raise ValueError(f"فرمت {ext} پشتیبانی نمی‌شود (docx, pdf, md, txt).")
    if body.lstrip().startswith("---"):
        return body
    t = (title or path.stem).replace('"', "'")
    return f'---\ntitle: "{t}"\naudience: {audience}\n---\n\n{body}'


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("file", type=Path)
    ap.add_argument("--title")
    ap.add_argument("--audience", default="internal", choices=["internal", "public"])
    ap.add_argument("--out", type=Path, default=Path("knowledge"))
    a = ap.parse_args()
    md = to_markdown(a.file, a.title, a.audience)
    a.out.mkdir(parents=True, exist_ok=True)
    dest = a.out / (re.sub(r"[^\w\-]+", "-", a.file.stem, flags=re.UNICODE).strip("-") + ".md")
    dest.write_text(md, encoding="utf-8")
    print(f"ذخیره شد: {dest}  ({len(md)} نویسه)")


if __name__ == "__main__":
    sys.exit(main())
