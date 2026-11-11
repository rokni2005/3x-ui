"""نقطه‌ی ورود:  python -m mehraman_bot.main  [--check]"""
from __future__ import annotations

import argparse
import logging
import sys
import time

from .bale import BaleClient, BaleError
from .config import Config
from .content import Content
from .db import DB
from .handlers import Bot
from .knowledge import KnowledgeBase
from .llm import make_llm
from .scheduler import start_scheduler

log = logging.getLogger("mehraman")


def build(cfg: Config) -> Bot:
    return Bot(cfg, BaleClient(cfg.bale_token, cfg.bale_api_base), DB(cfg.data_dir / "mehraman.db"),
               Content(cfg.content_dir), KnowledgeBase(cfg.knowledge_dir), make_llm(cfg))


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--check", action="store_true", help="فقط تنظیمات و محتوا را بررسی کن")
    args = ap.parse_args()
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s: %(message)s")
    logging.getLogger("httpx").setLevel(logging.WARNING)  # URL شامل توکن است؛ لاگ نشود

    cfg = Config.from_env()
    errs = cfg.validate()
    if errs:
        print("\n".join("❌ " + e for e in errs))
        return 2
    bot = build(cfg)
    print(f"محتوا: {len(bot.kb.chunks)} تکه از {len(bot.kb.sources)} منبع | "
          f"{len(bot.content.courses)} دوره | {len(bot.content.worksheets)} کاربرگ")
    for p in bot.content.problems:
        print("⚠️", p)
    if not bot.kb.chunks:
        print("⚠️ پوشه knowledge خالی است؛ بازو بدون محتوای پشتیبان فقط پاسخ‌های «خارج از دامنه» می‌دهد.")
    if args.check:
        try:
            print("✅ اتصال به بله:", bot.client.get_me().get("username"))
        except BaleError as e:
            print("❌", e)
            return 3
        return 0

    start_scheduler(bot)
    offset = None
    log.info("bot started")
    while True:
        try:
            for upd in bot.client.get_updates(offset):
                offset = upd["update_id"] + 1
                bot.handle_update(upd)
        except BaleError:
            time.sleep(5)
        except KeyboardInterrupt:
            return 0


if __name__ == "__main__":
    sys.exit(main())
