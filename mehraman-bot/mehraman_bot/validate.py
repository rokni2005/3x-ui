"""بررسی آفلاین فایل‌های محتوا:  python -m mehraman_bot.validate [فایل.yaml ...]"""
from __future__ import annotations

import sys
from pathlib import Path

import yaml

from .config import Config
from .content import Content, validate_data
from .knowledge import KnowledgeBase


def main() -> int:
    bad = 0
    for f in sys.argv[1:]:
        try:
            data = yaml.safe_load(Path(f).read_text(encoding="utf-8"))
        except (yaml.YAMLError, OSError) as e:
            print(f"❌ {f}: {e}")
            bad += 1
            continue
        kind, errs = validate_data(data)
        print(("❌" if errs else "✅"), f, f"({kind})" if kind else "")
        for e in errs:
            print("   •", e)
        bad += bool(errs)
    if not sys.argv[1:]:
        cfg = Config.from_env()
        c, kb = Content(cfg.content_dir), KnowledgeBase(cfg.knowledge_dir)
        print(f"{len(c.courses)} دوره، {len(c.worksheets)} کاربرگ، {len(kb.chunks)} تکه‌ی محتوایی")
        for p in c.problems:
            print("⚠️", p)
        bad = len(c.problems)
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
