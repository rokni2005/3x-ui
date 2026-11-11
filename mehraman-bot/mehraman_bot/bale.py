"""کلاینت ساده API بازوهای بله (سازگار با ساختار Bot API)."""
from __future__ import annotations

import logging
import time
from typing import Any

import httpx

log = logging.getLogger(__name__)
MAX_LEN = 4000  # سقف پیام بله ۴۰۹۶ نویسه است؛ کمی حاشیه می‌گذاریم


def split_text(text: str, limit: int = MAX_LEN) -> list[str]:
    """متن بلند را روی مرز پاراگراف/خط/جمله می‌شکند."""
    text = text.strip()
    if len(text) <= limit:
        return [text] if text else []
    parts, cur = [], ""
    for para in text.split("\n\n"):
        if len(para) > limit:
            if cur:
                parts.append(cur.strip())
                cur = ""
            chunk = ""
            for line in para.replace("\n", "\n§").split("§"):
                if len(chunk) + len(line) > limit:
                    parts.append(chunk.strip())
                    chunk = ""
                chunk += line
            cur = chunk
            continue
        if len(cur) + len(para) + 2 > limit:
            parts.append(cur.strip())
            cur = para
        else:
            cur = f"{cur}\n\n{para}" if cur else para
    if cur.strip():
        parts.append(cur.strip())
    return [p for p in parts if p]


# نوع رسانه ← (متد ارسال، نام فیلد)
MEDIA_KINDS = {"photo": ("sendPhoto", "photo"), "document": ("sendDocument", "document"),
               "video": ("sendVideo", "video"), "audio": ("sendAudio", "audio"),
               "voice": ("sendVoice", "voice"), "animation": ("sendAnimation", "animation")}
CAPTION_MAX = 1000


def extract_media(msg: dict) -> dict | None:
    """پیام دریافتی ← {kind, file_id?, text}. برای ارسال دوباره با file_id (بدون دانلود)."""
    for kind in MEDIA_KINDS:
        v = msg.get(kind)
        if v:
            fid = v[-1]["file_id"] if isinstance(v, list) else v.get("file_id")  # photo: بزرگ‌ترین اندازه
            if fid:
                item = {"kind": kind, "file_id": fid, "text": (msg.get("caption") or "").strip()}
                if kind == "document" and v.get("file_name"):
                    item["name"] = v["file_name"]
                return item
    text = (msg.get("text") or "").strip()
    return {"kind": "text", "text": text} if text else None


def reply_keyboard(rows: list[list[str]], resize: bool = True) -> dict:
    return {"keyboard": [[{"text": t} for t in row] for row in rows], "resize_keyboard": resize}


def inline_keyboard(rows: list[list[tuple[str, str]]]) -> dict:
    """rows: [[(متن دکمه, callback_data), ...], ...]"""
    return {"inline_keyboard": [[{"text": t, "callback_data": d} for t, d in row] for row in rows]}


class BaleError(RuntimeError):
    pass


class BaleClient:
    def __init__(self, token: str, base: str = "https://tapi.bale.ai", timeout: float = 70.0):
        self.token = token
        self.base = base.rstrip("/")
        self.http = httpx.Client(timeout=timeout)

    def _url(self, method: str) -> str:
        return f"{self.base}/bot{self.token}/{method}"

    def call(self, method: str, **params: Any) -> Any:
        params = {k: v for k, v in params.items() if v is not None}
        for attempt in range(3):
            try:
                r = self.http.post(self._url(method), json=params)
                data = r.json()
                if data.get("ok"):
                    return data.get("result")
                raise BaleError(f"{method}: {data.get('description') or data}")
            except (httpx.HTTPError, ValueError) as e:
                log.warning("Bale %s attempt %d failed: %s", method, attempt + 1, type(e).__name__)
                time.sleep(1.5 * (attempt + 1))
        raise BaleError(f"{method}: network failure")

    # ── متدهای رایج ──
    def get_me(self) -> dict:
        return self.call("getMe")

    def get_updates(self, offset: int | None, timeout: int = 50) -> list[dict]:
        return self.call("getUpdates", offset=offset, timeout=timeout) or []

    def send_message(self, chat_id: int, text: str, reply_markup: dict | None = None) -> None:
        chunks = split_text(text)
        for i, chunk in enumerate(chunks):
            last = i == len(chunks) - 1
            self.call("sendMessage", chat_id=chat_id, text=chunk,
                      reply_markup=reply_markup if last else None)

    def send_media(self, chat_id: int, item: dict, reply_markup: dict | None = None) -> None:
        """ارسال متن/عکس/فایل/ویدیو/صدا با file_id ذخیره‌شده. کپشن بلند جدا فرستاده می‌شود."""
        if item.get("kind", "text") == "text":
            return self.send_message(chat_id, item.get("text", ""), reply_markup)
        method, fld = MEDIA_KINDS[item["kind"]]
        cap = item.get("text") or ""
        long_cap = len(cap) > CAPTION_MAX
        self.call(method, chat_id=chat_id, **{fld: item["file_id"]},
                  caption=None if long_cap or not cap else cap,
                  reply_markup=None if long_cap else reply_markup)
        if long_cap:
            self.send_message(chat_id, cap, reply_markup)

    def answer_callback(self, callback_id: str, text: str | None = None) -> None:
        try:
            self.call("answerCallbackQuery", callback_query_id=callback_id, text=text)
        except BaleError:
            pass

    def typing(self, chat_id: int) -> None:
        try:
            self.call("sendChatAction", chat_id=chat_id, action="typing")
        except BaleError:
            pass

    def download_file(self, file_id: str) -> bytes:
        info = self.call("getFile", file_id=file_id)
        path = info["file_path"]
        r = self.http.get(f"{self.base}/file/bot{self.token}/{path}")
        r.raise_for_status()
        return r.content
