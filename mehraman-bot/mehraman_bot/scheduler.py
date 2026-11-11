"""رشته‌ی پس‌زمینه برای ارسال یادآوری‌های روزانه."""
from __future__ import annotations

import logging
import threading

from .handlers import Bot

log = logging.getLogger(__name__)


def start_scheduler(bot: Bot, interval: int = 30) -> threading.Event:
    stop = threading.Event()

    def loop():
        while not stop.wait(interval):
            try:
                bot.send_due_reminders()
            except Exception:
                log.exception("reminder loop error")

    threading.Thread(target=loop, daemon=True, name="reminders").start()
    return stop
