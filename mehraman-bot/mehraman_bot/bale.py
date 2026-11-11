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
