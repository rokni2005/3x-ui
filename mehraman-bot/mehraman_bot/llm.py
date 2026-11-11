"""لایه‌ی یکپارچه برای مدل‌های زبانی: Anthropic، Gemini و هر API سازگار با OpenAI.

عمداً فقط با httpx کار می‌کند تا با تعویض مدل/سرویس، کدی تغییر نکند.
"""
from __future__ import annotations

import logging
import time
from dataclasses import dataclass
from typing import Protocol

import httpx

from .config import Config

log = logging.getLogger(__name__)


@dataclass
class Msg:
    role: str  # "user" | "assistant"
    content: str


class LLM(Protocol):
    def generate(self, system: str, messages: list[Msg], max_tokens: int = 700) -> str: ...


class LLMError(RuntimeError):
    pass


class _Base:
    def __init__(self, cfg: Config):
        self.cfg = cfg
        kw = {"timeout": 60.0}
        if cfg.llm_proxy:
            kw["proxy"] = cfg.llm_proxy
        self.http = httpx.Client(**kw)

    def _post(self, url: str, headers: dict, body: dict) -> dict:
        last = None
        for attempt in range(3):
            try:
                r = self.http.post(url, headers=headers, json=body)
                if r.status_code == 429 or r.status_code >= 500:
                    last = f"HTTP {r.status_code}"
                    time.sleep(2 * (attempt + 1))
                    continue
                if r.status_code >= 400:
                    raise LLMError(f"HTTP {r.status_code}: {r.text[:300]}")
                return r.json()
            except httpx.HTTPError as e:
                last = type(e).__name__
                time.sleep(2 * (attempt + 1))
        raise LLMError(f"LLM request failed: {last}")


class AnthropicLLM(_Base):
    def generate(self, system, messages, max_tokens=700):
        base = self.cfg.llm_base_url or "https://api.anthropic.com"
        data = self._post(
            f"{base}/v1/messages",
            {"x-api-key": self.cfg.llm_api_key, "anthropic-version": "2023-06-01"},
            {"model": self.cfg.llm_model, "max_tokens": max_tokens, "system": system,
             "messages": [{"role": m.role, "content": m.content} for m in messages]},
        )
        return "".join(b.get("text", "") for b in data.get("content", [])).strip()


class GeminiLLM(_Base):
    def generate(self, system, messages, max_tokens=700):
        base = self.cfg.llm_base_url or "https://generativelanguage.googleapis.com"
        data = self._post(
            f"{base}/v1beta/models/{self.cfg.llm_model}:generateContent",
            {"x-goog-api-key": self.cfg.llm_api_key},
            {"systemInstruction": {"parts": [{"text": system}]},
             "contents": [{"role": "user" if m.role == "user" else "model",
                           "parts": [{"text": m.content}]} for m in messages],
             "generationConfig": {"maxOutputTokens": max_tokens, "temperature": 0.6}},
        )
        try:
            parts = data["candidates"][0]["content"]["parts"]
            return "".join(p.get("text", "") for p in parts).strip()
        except (KeyError, IndexError):
            raise LLMError(f"Unexpected Gemini response: {str(data)[:200]}")


class OpenAICompatLLM(_Base):
    def generate(self, system, messages, max_tokens=700):
        base = self.cfg.llm_base_url or "https://api.openai.com/v1"
        data = self._post(
            f"{base}/chat/completions",
            {"Authorization": f"Bearer {self.cfg.llm_api_key}"},
            {"model": self.cfg.llm_model, "max_tokens": max_tokens, "temperature": 0.6,
             "messages": [{"role": "system", "content": system}]
             + [{"role": m.role, "content": m.content} for m in messages]},
        )
        try:
            return data["choices"][0]["message"]["content"].strip()
        except (KeyError, IndexError):
            raise LLMError(f"Unexpected response: {str(data)[:200]}")


def make_llm(cfg: Config) -> LLM:
    return {"anthropic": AnthropicLLM, "gemini": GeminiLLM, "openai": OpenAICompatLLM}[cfg.llm_provider](cfg)
