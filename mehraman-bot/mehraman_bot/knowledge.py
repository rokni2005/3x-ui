"""بارگذاری «محتوای پشتیبان» و بازیابی تکه‌های مرتبط (BM25 سبک، بدون وابستگی خارجی).

هر فایل .md/.txt در پوشه knowledge/ یک منبع است. سربرگ YAML اختیاری:

    ---
    title: عنوان منبع
    audience: internal      # internal = مبنای فکری پشت‌صحنه؛ هرگز نقل مستقیم نشود
    topics: [ارتباط, خشم]
    version: 0.1
    ---
"""
from __future__ import annotations

import math
import re
from collections import Counter
from dataclasses import dataclass, field
from pathlib import Path

import yaml

from .textutil import tokens

CHUNK_CHARS = 1100


@dataclass
class Chunk:
    source: str
    title: str
    heading: str
    text: str
    audience: str = "internal"
    toks: list[str] = field(default_factory=list, repr=False)


def parse_front_matter(raw: str) -> tuple[dict, str]:
    if raw.startswith("---"):
        m = re.match(r"---\s*\n(.*?)\n---\s*\n?", raw, re.S)
        if m:
            try:
                meta = yaml.safe_load(m.group(1)) or {}
                return (meta if isinstance(meta, dict) else {}), raw[m.end():]
            except yaml.YAMLError:
                pass
    return {}, raw


def chunk_document(body: str, max_chars: int = CHUNK_CHARS) -> list[tuple[str, str]]:
    """متن را بر اساس عنوان‌ها (#) و سپس پاراگراف‌ها به تکه‌های حداکثر max_chars می‌شکند."""
    sections: list[tuple[str, list[str]]] = [("", [])]
    for line in body.splitlines():
        if re.match(r"^#{1,4}\s+", line):
            sections.append((re.sub(r"^#+\s*", "", line).strip(), []))
        else:
            sections[-1][1].append(line)
    out: list[tuple[str, str]] = []
    for heading, lines in sections:
        paras = [p.strip() for p in "\n".join(lines).split("\n\n") if p.strip()]
        cur = ""
        for p in paras:
            if cur and len(cur) + len(p) > max_chars:
                out.append((heading, cur))
                cur = p
            else:
                cur = f"{cur}\n\n{p}" if cur else p
            while len(cur) > max_chars * 1.5:  # پاراگراف خیلی بلند
                out.append((heading, cur[:max_chars]))
                cur = cur[max_chars:]
        if cur:
            out.append((heading, cur))
    return out


@dataclass
class Hit:
    chunk: Chunk
    score: float


class KnowledgeBase:
    def __init__(self, directory: Path):
        self.dir = Path(directory)
        self.chunks: list[Chunk] = []
        self.sources: dict[str, dict] = {}
        self._df: Counter = Counter()
        self._avg = 1.0
        self.reload()

    def reload(self) -> int:
        self.chunks, self.sources = [], {}
        if self.dir.exists():
            for path in sorted(self.dir.iterdir()):
                if path.suffix.lower() not in (".md", ".txt") or path.name.startswith(("_", "README")):
                    continue
                raw = path.read_text(encoding="utf-8", errors="ignore")
                meta, body = parse_front_matter(raw)
                title = str(meta.get("title") or path.stem)
                aud = str(meta.get("audience") or "internal")
                n = 0
                for heading, text in chunk_document(body):
                    c = Chunk(path.name, title, heading, text, aud)
                    c.toks = tokens(f"{title} {heading} {text}")
                    self.chunks.append(c)
                    n += 1
                self.sources[path.name] = {"title": title, "audience": aud, "chunks": n,
                                           "version": meta.get("version")}
        self._df = Counter()
        for c in self.chunks:
            self._df.update(set(c.toks))
        self._avg = (sum(len(c.toks) for c in self.chunks) / len(self.chunks)) if self.chunks else 1.0
        return len(self.chunks)

    def search(self, query: str, k: int = 5, boost_terms: list[str] | None = None) -> list[Hit]:
        q = tokens(query) + tokens(" ".join(boost_terms or []))
        if not q or not self.chunks:
            return []
        N, k1, b = len(self.chunks), 1.4, 0.75
        hits = []
        for c in self.chunks:
            tf = Counter(c.toks)
            score = 0.0
            for t in set(q):
                if t not in tf:
                    continue
                idf = math.log(1 + (N - self._df[t] + 0.5) / (self._df[t] + 0.5))
                f = tf[t]
                score += idf * f * (k1 + 1) / (f + k1 * (1 - b + b * len(c.toks) / self._avg))
            if score > 0:
                hits.append(Hit(c, score))
        hits.sort(key=lambda h: -h.score)
        return hits[:k]
