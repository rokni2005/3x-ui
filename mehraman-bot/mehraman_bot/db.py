"""ذخیره‌سازی SQLite: پروفایل/نقشه‌ی رشد کاربر، پیشرفت تمرین‌ها، تاریخچه، ارجاع‌ها، پرسش‌های بی‌پاسخ."""
from __future__ import annotations

import json
import sqlite3
import threading
import time
from pathlib import Path

SCHEMA = """
CREATE TABLE IF NOT EXISTS users(
  chat_id INTEGER PRIMARY KEY, name TEXT DEFAULT '', username TEXT DEFAULT '',
  created_at INTEGER, consent INTEGER DEFAULT 0,
  course_id TEXT DEFAULT '', station_order INTEGER DEFAULT 1,
  reminder_time TEXT DEFAULT '', last_reminder_day TEXT DEFAULT '',
  state TEXT DEFAULT '', state_data TEXT DEFAULT '{}');
CREATE TABLE IF NOT EXISTS progress(
  chat_id INTEGER, station_id TEXT, worksheet_id TEXT, done_at INTEGER,
  status TEXT DEFAULT 'done', obstacle TEXT DEFAULT '',
  PRIMARY KEY(chat_id, station_id, worksheet_id));
CREATE TABLE IF NOT EXISTS history(
  id INTEGER PRIMARY KEY AUTOINCREMENT, chat_id INTEGER, role TEXT, content TEXT, ts INTEGER);
CREATE TABLE IF NOT EXISTS usage(chat_id INTEGER, day TEXT, n INTEGER, PRIMARY KEY(chat_id, day));
CREATE TABLE IF NOT EXISTS referrals(
  id INTEGER PRIMARY KEY AUTOINCREMENT, chat_id INTEGER, payload TEXT, created_at INTEGER);
CREATE TABLE IF NOT EXISTS unanswered(
  id INTEGER PRIMARY KEY AUTOINCREMENT, question TEXT, ts INTEGER);
CREATE TABLE IF NOT EXISTS questions(
  id INTEGER PRIMARY KEY AUTOINCREMENT, station_order INTEGER, ts INTEGER);
CREATE TABLE IF NOT EXISTS broadcasts(
  id INTEGER PRIMARY KEY AUTOINCREMENT, admin_id INTEGER, audience TEXT, payload TEXT,
  total INTEGER, sent INTEGER DEFAULT 0, failed INTEGER DEFAULT 0, ts INTEGER);
"""


class DB:
    def __init__(self, path: Path | str):
        if str(path) != ":memory:":
            Path(path).parent.mkdir(parents=True, exist_ok=True)
        self.conn = sqlite3.connect(str(path), check_same_thread=False)
        self.conn.row_factory = sqlite3.Row
        self.lock = threading.RLock()
        with self.lock:
            self.conn.executescript(SCHEMA)

    def _q(self, sql, args=()):
        with self.lock:
            cur = self.conn.execute(sql, args)
            self.conn.commit()
            return cur

    # ── کاربران ──
    def user(self, chat_id: int) -> sqlite3.Row | None:
        return self._q("SELECT * FROM users WHERE chat_id=?", (chat_id,)).fetchone()

    def ensure_user(self, chat_id: int, name: str = "", username: str = "") -> sqlite3.Row:
        if not self.user(chat_id):
            self._q("INSERT INTO users(chat_id,name,username,created_at) VALUES(?,?,?,?)",
                    (chat_id, name, username, int(time.time())))
        return self.user(chat_id)

    def update_user(self, chat_id: int, **fields) -> None:
        if not fields:
            return
        sets = ",".join(f"{k}=?" for k in fields)
        self._q(f"UPDATE users SET {sets} WHERE chat_id=?", (*fields.values(), chat_id))

    def set_state(self, chat_id: int, state: str = "", data: dict | None = None) -> None:
        self.update_user(chat_id, state=state, state_data=json.dumps(data or {}, ensure_ascii=False))

    def state(self, chat_id: int) -> tuple[str, dict]:
        u = self.user(chat_id)
        return (u["state"], json.loads(u["state_data"] or "{}")) if u else ("", {})

    def all_users(self) -> list[sqlite3.Row]:
        return self._q("SELECT * FROM users").fetchall()

    def audience(self, station_order: int | None = None) -> list[int]:
        """مخاطبان (رضایت داده‌اند)؛ با station_order فقط کسانی که در آن ایستگاه دوره هستند."""
        if station_order is None:
            rows = self._q("SELECT chat_id FROM users WHERE consent=1 ORDER BY chat_id").fetchall()
        else:
            rows = self._q("SELECT chat_id FROM users WHERE consent=1 AND course_id!='' AND station_order=? "
                           "ORDER BY chat_id", (station_order,)).fetchall()
        return [r[0] for r in rows]

    def find_users(self, refs: list[str]) -> tuple[list[sqlite3.Row], list[str]]:
        """refs: @نام‌کاربری یا شناسه‌ی عددی ← (کاربران پیداشده، مواردی که پیدا نشد)"""
        found, missing, seen = [], [], set()
        for ref in refs:
            r = ref.strip().lstrip("@")
            if not r:
                continue
            if r.lstrip("-").isdigit():
                u = self.user(int(r))
            else:
                u = self._q("SELECT * FROM users WHERE lower(username)=lower(?)", (r,)).fetchone()
            if u and u["chat_id"] not in seen:
                seen.add(u["chat_id"])
                found.append(u)
            elif not u:
                missing.append(ref.strip())
        return found, missing

    # ── ارسال گروهی ──
    def add_broadcast(self, admin_id: int, audience: str, payload: dict, total: int) -> int:
        return self._q("INSERT INTO broadcasts(admin_id,audience,payload,total,ts) VALUES(?,?,?,?,?)",
                       (admin_id, audience, json.dumps(payload, ensure_ascii=False), total,
                        int(time.time()))).lastrowid

    def finish_broadcast(self, bid: int, sent: int, failed: int) -> None:
        self._q("UPDATE broadcasts SET sent=?, failed=? WHERE id=?", (sent, failed, bid))

    # ── پیشرفت ──
    def mark_done(self, chat_id: int, station_id: str, worksheet_id: str,
                  status: str = "done", obstacle: str = "") -> None:
        """status: done | partial | not  (چک‌این سه‌گزینه‌ای)"""
        self._q("INSERT OR REPLACE INTO progress VALUES(?,?,?,?,?,?)",
                (chat_id, station_id, worksheet_id, int(time.time()), status, obstacle))

    def done_ids(self, chat_id: int, station_id: str) -> set[str]:
        """تمرین‌هایی که کامل یا تا حدی انجام شده‌اند (نه «هنوز نه»)."""
        rows = self._q("SELECT worksheet_id FROM progress WHERE chat_id=? AND station_id=? AND status!='not'",
                       (chat_id, station_id)).fetchall()
        return {r[0] for r in rows}

    def total_done(self, chat_id: int) -> int:
        return self._q("SELECT COUNT(*) FROM progress WHERE chat_id=? AND status!='not'", (chat_id,)).fetchone()[0]

    # ── گفتگو ──
    def add_history(self, chat_id: int, role: str, content: str, keep: int = 12) -> None:
        self._q("INSERT INTO history(chat_id,role,content,ts) VALUES(?,?,?,?)",
                (chat_id, role, content, int(time.time())))
        self._q("DELETE FROM history WHERE chat_id=? AND id NOT IN "
                "(SELECT id FROM history WHERE chat_id=? ORDER BY id DESC LIMIT ?)", (chat_id, chat_id, keep))

    def history(self, chat_id: int, n: int = 6) -> list[tuple[str, str]]:
        rows = self._q("SELECT role,content FROM history WHERE chat_id=? ORDER BY id DESC LIMIT ?",
                       (chat_id, n)).fetchall()
        return [(r[0], r[1]) for r in reversed(rows)]

    def clear_history(self, chat_id: int) -> None:
        self._q("DELETE FROM history WHERE chat_id=?", (chat_id,))

    def bump_usage(self, chat_id: int, day: str) -> int:
        self._q("INSERT INTO usage VALUES(?,?,1) ON CONFLICT(chat_id,day) DO UPDATE SET n=n+1", (chat_id, day))
        return self._q("SELECT n FROM usage WHERE chat_id=? AND day=?", (chat_id, day)).fetchone()[0]

    def usage(self, chat_id: int, day: str) -> int:
        r = self._q("SELECT n FROM usage WHERE chat_id=? AND day=?", (chat_id, day)).fetchone()
        return r[0] if r else 0

    # ── ارجاع و گزارش ──
    def add_referral(self, chat_id: int, payload: dict) -> int:
        cur = self._q("INSERT INTO referrals(chat_id,payload,created_at) VALUES(?,?,?)",
                      (chat_id, json.dumps(payload, ensure_ascii=False), int(time.time())))
        return cur.lastrowid

    def add_unanswered(self, question: str) -> None:
        self._q("INSERT INTO unanswered(question,ts) VALUES(?,?)", (question[:500], int(time.time())))

    def add_question_stat(self, station_order: int) -> None:
        self._q("INSERT INTO questions(station_order,ts) VALUES(?,?)", (station_order, int(time.time())))

    def stats(self, since_ts: int) -> dict:
        q = lambda s, a=(): self._q(s, a).fetchone()[0]
        return {
            "users": q("SELECT COUNT(*) FROM users"),
            "new_users": q("SELECT COUNT(*) FROM users WHERE created_at>=?", (since_ts,)),
            "questions": q("SELECT COUNT(*) FROM questions WHERE ts>=?", (since_ts,)),
            "worksheets_done": q("SELECT COUNT(*) FROM progress WHERE done_at>=? AND status!='not'", (since_ts,)),
            "checkins": [(r[0], r[1]) for r in self._q(
                "SELECT status, COUNT(*) FROM progress WHERE done_at>=? GROUP BY status", (since_ts,))],
            "obstacles": [(r[0], r[1]) for r in self._q(
                "SELECT obstacle, COUNT(*) FROM progress WHERE done_at>=? AND obstacle!='' "
                "GROUP BY obstacle ORDER BY 2 DESC", (since_ts,))],
            "referrals": q("SELECT COUNT(*) FROM referrals WHERE created_at>=?", (since_ts,)),
            "by_station": [(r[0], r[1]) for r in self._q(
                "SELECT station_order, COUNT(*) FROM users GROUP BY station_order ORDER BY station_order")],
            "unanswered": [r[0] for r in self._q(
                "SELECT question FROM unanswered WHERE ts>=? ORDER BY id DESC LIMIT 20", (since_ts,))],
        }
