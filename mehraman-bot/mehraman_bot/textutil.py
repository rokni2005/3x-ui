"""نرمال‌سازی متن فارسی و توکن‌سازی برای جست‌وجو و تشخیص کلیدواژه."""
from __future__ import annotations

import re

_AR2FA = str.maketrans({"ي": "ی", "ك": "ک", "ۀ": "ه", "ة": "ه", "أ": "ا", "إ": "ا", "ؤ": "و"})
_DIGITS = str.maketrans("۰۱۲۳۴۵۶۷۸۹٠١٢٣٤٥٦٧٨٩", "01234567890123456789")
_DIACRITICS = re.compile("[ً-ٰٟۖ-ۭ‌‍‎‏‪-‮ـ]")
_WORD = re.compile(r"[\w]+", re.UNICODE)
_SUFFIXES = ("هایی", "های", "ها", "ات", "ان", "ین", "تان", "شان", "مان", "تر", "ترین", "ی", "ام", "ات", "ش")

STOPWORDS = set("""و در به از که این آن با برای را است بود شد می یک تا هم یا اما اگر چه چرا چطور
چگونه من تو ما شما او آنها هر همه بر پس دیگر خیلی باید نیز هست نیست کرد کنم کنید کند""".split())


def normalize(text: str) -> str:
    text = text.translate(_AR2FA).translate(_DIGITS)
    text = _DIACRITICS.sub(lambda m: " " if m.group() in "‌‍" else "", text)
    return text.lower()


def _stem(tok: str) -> str:
    for suf in sorted(_SUFFIXES, key=len, reverse=True):
        if tok.endswith(suf) and len(tok) - len(suf) >= 3:
            return tok[: -len(suf)]
    return tok


def tokens(text: str) -> list[str]:
    out = []
    for w in _WORD.findall(normalize(text)):
        if w in STOPWORDS or len(w) < 2:
            continue
        out.append(_stem(w))
    return out
