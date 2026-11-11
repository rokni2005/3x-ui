"""بارگذاری و اعتبارسنجی محتوای قابل‌ویرایش: دوره‌ها، کاربرگ‌ها، پیام‌ها، قواعد ایمنی."""
from __future__ import annotations

import random
from dataclasses import dataclass, field
from pathlib import Path

import yaml


@dataclass
class Worksheet:
    id: str
    station: str
    title: str
    purpose: str = ""
    minutes: int = 10
    steps: list[str] = field(default_factory=list)
    reflection: str = ""


@dataclass
class Station:
    id: str
    order: int
    name: str
    subtitle: str = ""
    level: str = ""
    summary: str = ""
    keywords: list[str] = field(default_factory=list)
    weekly_tasks: list[str] = field(default_factory=list)
    worksheet_ids: list[str] = field(default_factory=list)


@dataclass
class Course:
    id: str
    title: str
    stations: list[Station]

    def by_order(self, order: int) -> Station | None:
        return next((s for s in self.stations if s.order == order), None)


def _load_yaml(path: Path):
    return yaml.safe_load(path.read_text(encoding="utf-8"))


def validate_data(data, kind: str | None = None) -> tuple[str | None, list[str]]:
    """نوع (course|worksheet) را تشخیص می‌دهد و خطاهای قابل‌فهم فارسی برمی‌گرداند."""
    errs: list[str] = []
    if not isinstance(data, dict):
        return None, ["ساختار فایل باید یک YAML از نوع «کلید: مقدار» باشد."]
    kind = kind or ("course" if "stations" in data else "worksheet" if "steps" in data else None)
    if kind is None:
        return None, ["نوع فایل مشخص نیست: دوره باید کلید stations و کاربرگ باید کلید steps داشته باشد."]
    if kind == "course":
        for k in ("id", "title", "stations"):
            if not data.get(k):
                errs.append(f"کلید «{k}» لازم است.")
        seen = set()
        for i, s in enumerate(data.get("stations") or [], 1):
            if not isinstance(s, dict):
                errs.append(f"ایستگاه شماره {i} باید یک مجموعه کلید/مقدار باشد.")
                continue
            for k in ("id", "order", "name"):
                if s.get(k) in (None, ""):
                    errs.append(f"ایستگاه {i}: کلید «{k}» لازم است.")
            if s.get("order") in seen:
                errs.append(f"ایستگاه {i}: شماره ترتیب (order) تکراری است.")
            seen.add(s.get("order"))
            if s.get("worksheets") is not None and not isinstance(s["worksheets"], list):
                errs.append(f"ایستگاه {i}: worksheets باید فهرست باشد.")
    else:
        for k in ("id", "station", "title", "steps"):
            if not data.get(k):
                errs.append(f"کلید «{k}» لازم است.")
        if not isinstance(data.get("steps"), list) or not all(isinstance(x, str) for x in data.get("steps") or []):
            errs.append("steps باید فهرستی از متن‌ها (هر گام یک خط) باشد.")
    return kind, errs


class Content:
    def __init__(self, directory: Path):
        self.dir = Path(directory)
        self.courses: dict[str, Course] = {}
        self.worksheets: dict[str, Worksheet] = {}
        self.persona: str = ""
        self.messages: dict = {}
        self.safety: dict = {}
        self.referral_form: list[dict] = []
        self.faq: list[dict] = []
        self.problems: list[str] = []
        self.reload()

    def _yaml_dir(self, sub: str):
        d = self.dir / sub
        if d.exists():
            for p in sorted(d.glob("*.y*ml")):
                if not p.name.startswith("_"):
                    yield p

    def reload(self) -> list[str]:
        self.problems = []
        self.courses, self.worksheets = {}, {}
        for p in self._yaml_dir("courses"):
            try:
                data = _load_yaml(p)
            except yaml.YAMLError as e:
                self.problems.append(f"{p.name}: خطای YAML ({e})")
                continue
            _, errs = validate_data(data, "course")
            if errs:
                self.problems += [f"{p.name}: {e}" for e in errs]
                continue
            stations = [
                Station(id=str(s["id"]), order=int(s["order"]), name=s["name"],
                        subtitle=s.get("subtitle", ""), level=s.get("level", ""),
                        summary=s.get("summary", ""), keywords=s.get("keywords") or [],
                        weekly_tasks=s.get("weekly_tasks") or [],
                        worksheet_ids=[str(w) for w in (s.get("worksheets") or [])])
                for s in sorted(data["stations"], key=lambda x: int(x["order"]))
            ]
            self.courses[str(data["id"])] = Course(str(data["id"]), data["title"], stations)
        for p in self._yaml_dir("worksheets"):
            try:
                data = _load_yaml(p)
            except yaml.YAMLError as e:
                self.problems.append(f"{p.name}: خطای YAML ({e})")
                continue
            _, errs = validate_data(data, "worksheet")
            if errs:
                self.problems += [f"{p.name}: {e}" for e in errs]
                continue
            self.worksheets[str(data["id"])] = Worksheet(
                id=str(data["id"]), station=str(data["station"]), title=data["title"],
                purpose=data.get("purpose", ""), minutes=int(data.get("minutes", 10)),
                steps=data["steps"], reflection=data.get("reflection", ""))
        pf = self.dir / "persona.md"
        self.persona = pf.read_text(encoding="utf-8") if pf.exists() else ""
        for attr, name in (("messages", "messages.yaml"), ("safety", "safety.yaml")):
            f = self.dir / name
            setattr(self, attr, (_load_yaml(f) or {}) if f.exists() else {})
        ff = self.dir / "referral_form.yaml"
        self.referral_form = (_load_yaml(ff) or {}).get("questions", []) if ff.exists() else []
        fq = self.dir / "faq.yaml"
        self.faq = (_load_yaml(fq) or {}).get("items", []) if fq.exists() else []
        for ws in self.worksheets.values():
            if not any(ws.station == s.id for c in self.courses.values() for s in c.stations):
                self.problems.append(f"کاربرگ {ws.id}: ایستگاه «{ws.station}» در هیچ دوره‌ای نیست.")
        return self.problems

    # ── دسترسی‌ها ──
    @property
    def default_course(self) -> Course | None:
        return next(iter(self.courses.values()), None)

    def station(self, course_id: str, order: int) -> Station | None:
        c = self.courses.get(course_id)
        return c.by_order(order) if c else None

    def worksheets_for(self, station: Station) -> list[Worksheet]:
        ids = list(station.worksheet_ids)
        ids += [w.id for w in self.worksheets.values() if w.station == station.id and w.id not in ids]
        return [self.worksheets[i] for i in ids if i in self.worksheets]

    def msg(self, key: str, default: str = "", **fmt) -> str:
        v = self.messages.get(key, default)
        if isinstance(v, list):
            v = random.choice(v)
        try:
            return str(v).format(**fmt)
        except (KeyError, IndexError):
            return str(v)
