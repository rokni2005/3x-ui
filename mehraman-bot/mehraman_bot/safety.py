"""تشخیص قطعی (بدون مدل) موارد بحرانی و نیازمند ارجاع؛ کلیدواژه‌ها در content/safety.yaml ویرایش می‌شوند."""
from __future__ import annotations

from dataclasses import dataclass

from .textutil import normalize


@dataclass
class SafetyResult:
    level: str  # "emergency" | "refer" | "ok"
    matched: str = ""


def check(text: str, cfg: dict) -> SafetyResult:
    t = normalize(text)
    for kw in cfg.get("emergency_keywords", []):
        if normalize(kw) in t:
            return SafetyResult("emergency", kw)
    for kw in cfg.get("refer_keywords", []):
        if normalize(kw) in t:
            return SafetyResult("refer", kw)
    return SafetyResult("ok")
