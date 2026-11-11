"""تنظیمات از متغیرهای محیطی (و فایل .env) خوانده می‌شود."""
from __future__ import annotations

import os
from dataclasses import dataclass, field
from pathlib import Path


def load_dotenv(path: str | Path = ".env") -> None:
    p = Path(path)
    if not p.exists():
        return
    for line in p.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, v = line.split("=", 1)
        os.environ.setdefault(k.strip(), v.strip().strip('"').strip("'"))


def _ids(raw: str) -> list[int]:
    out = []
    for part in raw.replace("،", ",").split(","):
        part = part.strip()
        if part.lstrip("-").isdigit():
            out.append(int(part))
    return out


@dataclass
class Config:
    bale_token: str = ""
    bale_api_base: str = "https://tapi.bale.ai"
    llm_provider: str = "gemini"
    llm_api_key: str = ""
    llm_model: str = "gemini-2.5-flash"
    llm_base_url: str = ""
    llm_proxy: str = ""
    admin_ids: list[int] = field(default_factory=list)
    referral_chat_ids: list[int] = field(default_factory=list)  # REFERRAL_CHAT_ID با کاما: اول مسئول اصلی

    @property
    def referral_chat_id(self) -> int | None:  # سازگاری با کد قدیمی
        return self.referral_chat_ids[0] if self.referral_chat_ids else None
    timezone: str = "Asia/Tehran"
    daily_question_limit: int = 30
    min_retrieval_score: float = 1.2
    data_dir: Path = Path("./data")
    content_dir: Path = Path("./content")
    knowledge_dir: Path = Path("./knowledge")

    @classmethod
    def from_env(cls) -> "Config":
        load_dotenv()
        g = os.environ.get
        ref = g("REFERRAL_CHAT_ID", "").strip()
        return cls(
            bale_token=g("BALE_BOT_TOKEN", "").strip(),
            bale_api_base=g("BALE_API_BASE", "https://tapi.bale.ai").rstrip("/"),
            llm_provider=g("LLM_PROVIDER", "gemini").strip().lower(),
            llm_api_key=g("LLM_API_KEY", "").strip(),
            llm_model=g("LLM_MODEL", "gemini-2.5-flash").strip(),
            llm_base_url=g("LLM_BASE_URL", "").strip().rstrip("/"),
            llm_proxy=g("LLM_PROXY", "").strip(),
            admin_ids=_ids(g("ADMIN_IDS", "")),
            referral_chat_ids=_ids(ref),
            timezone=g("TIMEZONE", "Asia/Tehran").strip(),
            daily_question_limit=int(g("DAILY_QUESTION_LIMIT", "30")),
            min_retrieval_score=float(g("MIN_RETRIEVAL_SCORE", "1.2")),
            data_dir=Path(g("DATA_DIR", "./data")),
            content_dir=Path(g("CONTENT_DIR", "./content")),
            knowledge_dir=Path(g("KNOWLEDGE_DIR", "./knowledge")),
        )

    def validate(self) -> list[str]:
        errs = []
        if not self.bale_token:
            errs.append("BALE_BOT_TOKEN تنظیم نشده است.")
        if self.llm_provider not in ("anthropic", "gemini", "openai"):
            errs.append("LLM_PROVIDER باید anthropic یا gemini یا openai باشد.")
        if not self.llm_api_key:
            errs.append("LLM_API_KEY تنظیم نشده است.")
        return errs
