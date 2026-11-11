"""منطق اصلی بازو: منو، پروفایل/نقشه‌ی رشد، تمرین روزانه، پرسش‌وپاسخ، ارجاع به مشاور، ابزار ادمین."""
from __future__ import annotations

import logging
import re
import time
from datetime import datetime
from zoneinfo import ZoneInfo

import yaml

from . import safety
from .bale import BaleClient, inline_keyboard, reply_keyboard
from .config import Config
from .admin_panel import BTN_ADMIN, AdminMixin
from .content import Content, Station, validate_data
from .db import DB
from .ingest import to_markdown
from .knowledge import KnowledgeBase
from .llm import LLM, LLMError, Msg
from .persona import build_system_prompt, build_user_prompt
from .textutil import tokens

log = logging.getLogger(__name__)

BTN_PRACTICE = "📝 تمرین امروز"
BTN_MAP = "🧭 نقشه‌ی من"
BTN_ASK = "❓ پرسشی دارم"
BTN_COUNSELOR = "🤝 ارتباط با مشاور"
BTN_REMINDER = "⏰ یادآوری"
BTN_ABOUT = "ℹ️ درباره‌ی مهرمان"
MENU = reply_keyboard([[BTN_PRACTICE, BTN_MAP], [BTN_ASK, BTN_COUNSELOR], [BTN_REMINDER, BTN_ABOUT]])
REFER_MARK = "[[REFER]]"
OBSTACLE_LABELS = {"time": "وقت نشد", "unclear": "نفهمیدم چه کنم", "partner": "همسرم همراه نبود",
                   "forgot": "یادم رفت", "other": "دلیل دیگر"}
OBSTACLES = {"time": "وقت", "unclear": "ابهام در تمرین", "partner": "همراهی همسر", "forgot": "فراموشی", "other": "سایر"}
ADMIN_MENU = reply_keyboard([[BTN_PRACTICE, BTN_MAP], [BTN_ASK, BTN_COUNSELOR],
                             [BTN_REMINDER, BTN_ABOUT], [BTN_ADMIN]])
ADMIN_CMDS = {"/stats", "/reload", "/sources", "/delknowledge", "/adminhelp"}
HHMM = re.compile(r"^([01]?\d|2[0-3]):([0-5]\d)$")


class Bot(AdminMixin):
    def __init__(self, cfg: Config, client: BaleClient, db: DB, content: Content,
                 kb: KnowledgeBase, llm: LLM):
        self.cfg, self.client, self.db, self.content, self.kb, self.llm = cfg, client, db, content, kb, llm
        self.tz = ZoneInfo(cfg.timezone)

    # ───────────── کمکی‌ها ─────────────
    def say(self, chat_id: int, text: str, markup: dict | None = None) -> None:
        if markup is MENU and self.is_admin(chat_id):
            markup = ADMIN_MENU
        self.client.send_message(chat_id, text, markup)

    def is_admin(self, chat_id: int) -> bool:
        return chat_id in self.cfg.admin_ids

    def today(self) -> str:
        return datetime.now(self.tz).strftime("%Y-%m-%d")

    def m(self, key: str, **kw) -> str:
        return self.content.msg(key, **kw)

    def _course(self, user):
        return self.content.courses.get(user["course_id"]) or self.content.default_course

    def _station(self, user) -> Station | None:
        c = self._course(user)
        if not c or not c.stations:
            return None
        return c.by_order(user["station_order"]) or c.stations[0]

    # ───────────── ورودی اصلی ─────────────
    def handle_update(self, upd: dict) -> None:
        try:
            if "callback_query" in upd:
                self._on_callback(upd["callback_query"])
            elif "message" in upd:
                self._on_message(upd["message"])
        except Exception:
            log.exception("update handling failed")
            chat = (upd.get("message") or upd.get("callback_query", {}).get("message") or {}).get("chat", {}).get("id")
            if chat:
                self.say(chat, self.m("error"))

    def _on_message(self, msg: dict) -> None:
        chat_id = msg["chat"]["id"]
        frm = msg.get("from") or {}
        user = self.db.ensure_user(chat_id, frm.get("first_name", ""), frm.get("username", "") or "")
        if msg.get("document"):
            return self._on_document(chat_id, msg)
        text = (msg.get("text") or "").strip()
        if not text:
            return self.say(chat_id, self.m("text_only"))

        cmd, _, arg = text.partition(" ")
        cmd = cmd.split("@")[0].lower() if cmd.startswith("/") else ""
        if cmd == "/start":
            return self.cmd_start(chat_id)
        if cmd in ("/cancel", "/menu"):
            self.db.set_state(chat_id)
            return self.say(chat_id, self.m("menu_hint"), MENU)
        admin_cmd = self.is_admin(chat_id) and cmd in ADMIN_CMDS
        if admin_cmd:
            return self._admin_cmd(chat_id, cmd, arg.strip())
        if not user["consent"]:
            return self.cmd_start(chat_id)

        routes = {"/profile": self.cmd_profile, "/map": self.cmd_profile, "/today": self.cmd_practice,
                  "/practice": self.cmd_practice, "/stations": self.cmd_stations,
                  "/counselor": self.cmd_counselor, "/about": self.cmd_about, "/help": self.cmd_help,
                  "/reminder": lambda c: self.cmd_reminder(c, arg.strip()),
                  "/done": self.cmd_done_latest, "/reset": self.cmd_reset}
        by_btn = {BTN_PRACTICE: self.cmd_practice, BTN_MAP: self.cmd_profile, BTN_COUNSELOR: self.cmd_counselor,
                  BTN_REMINDER: lambda c: self.cmd_reminder(c, ""), BTN_ABOUT: self.cmd_about,
                  BTN_ASK: lambda c: self.say(c, self.m("ask_prompt"), MENU)}
        if text == BTN_ADMIN and self.is_admin(chat_id):
            return self.admin_home(chat_id)
        if text in by_btn:
            self.db.set_state(chat_id)
            return by_btn[text](chat_id)
        if cmd in routes:
            return routes[cmd](chat_id)
        state, data = self.db.state(chat_id)
        if state.startswith("adm_") and self.admin_state_text(chat_id, text, state, data):
            return
        if state == "refer":
            return self._referral_answer(chat_id, text, data)
        if state == "reminder_custom":
            self.db.set_state(chat_id)
            return self.cmd_reminder(chat_id, text)
        return self.answer_question(chat_id, text)

    def _on_callback(self, cb: dict) -> None:
        chat_id = cb["message"]["chat"]["id"]
        data = cb.get("data", "")
        self.client.answer_callback(cb["id"])
        self.db.ensure_user(chat_id, (cb.get("from") or {}).get("first_name", ""), (cb.get("from") or {}).get("username", "") or "")
        kind, _, val = data.partition(":")
        if kind == "consent":
            self.db.update_user(chat_id, consent=1)
            return self.ask_station(chat_id, onboarding=True)
        if kind == "st":
            return self.ask_station(chat_id) if val == "0" else self.set_station(chat_id, int(val))
        if kind == "rm":
            return self.cmd_reminder(chat_id, val if val != "off" else "off")
        if kind == "rmc":
            self.db.set_state(chat_id, "reminder_custom")
            return self.say(chat_id, self.m("reminder_custom_ask"))
        if kind == "done":
            return self.ask_checkin(chat_id, val)
        if kind == "ci":  # ci:<d|p|n>:<ws>
            code, _, ws = val.partition(":")
            if code == "d":
                return self.mark_done(chat_id, ws, "done")
            return self.ask_obstacle(chat_id, code, ws)
        if kind == "ob":  # ob:<code>:<p|n>:<ws>
            ob, _, rest = val.partition(":")
            code, _, ws = rest.partition(":")
            return self.mark_done(chat_id, ws, "partial" if code == "p" else "not", OBSTACLES.get(ob, ""))
        if kind == "more":
            return self.cmd_practice(chat_id, skip=val)
        if kind == "nextst":
            u = self.db.user(chat_id)
            return self.set_station(chat_id, u["station_order"] + 1)
        if kind == "refer":
            return self.cmd_counselor(chat_id) if val == "start" else self._referral_begin(chat_id)
        if kind == "adm":
            return self.admin_callback(chat_id, val)
        if kind == "profile":
            return self.cmd_profile(chat_id)
        if kind == "practice":
            return self.cmd_practice(chat_id)

    # ───────────── شروع و پروفایل ─────────────
    def cmd_start(self, chat_id: int) -> None:
        u = self.db.user(chat_id)
        name = u["name"] or ""
        if u["consent"]:
            return self.say(chat_id, self.m("welcome_back", name=name), MENU)
        self.say(chat_id, self.m("welcome", name=name), inline_keyboard([[("🌿 موافقم، شروع کنیم", "consent:1")]]))

    def ask_station(self, chat_id: int, onboarding: bool = False) -> None:
        c = self.content.default_course
        if not c:
            return self.say(chat_id, self.m("no_course"), MENU)
        rows, row = [], []
        for s in c.stations:
            row.append((f"{s.order}. {s.name}", f"st:{s.order}"))
            if len(row) == 2:
                rows.append(row)
                row = []
        if row:
            rows.append(row)
        self.say(chat_id, self.m("ask_station" if onboarding else "change_station"), inline_keyboard(rows))

    def set_station(self, chat_id: int, order: int) -> None:
        u = self.db.user(chat_id)
        c = self._course(u)
        if not c:
            return self.say(chat_id, self.m("no_course"), MENU)
        st = c.by_order(order)
        if not st:
            return self.say(chat_id, self.m("course_finished"), MENU)
        first_time = not u["course_id"]
        self.db.update_user(chat_id, course_id=c.id, station_order=order)
        self.say(chat_id, self.m("station_set", station=st.name, subtitle=st.subtitle))
        if first_time:
            return self._ask_reminder(chat_id)
        self.cmd_profile(chat_id)

    def _ask_reminder(self, chat_id: int) -> None:
        self.say(chat_id, self.m("ask_reminder"), inline_keyboard([
            [("🌅 ۸ صبح", "rm:08:00"), ("🌤 ۱۸ عصر", "rm:18:00")],
            [("🌙 ۲۰:۳۰", "rm:20:30"), ("🌌 ۲۲ شب", "rm:22:00")],
            [("⏱ ساعت دلخواه", "rmc:1"), ("نمی‌خواهم", "rm:off")]]))

    def cmd_profile(self, chat_id: int) -> None:
        u = self.db.user(chat_id)
        c, st = self._course(u), self._station(u)
        if not c or not st:
            return self.say(chat_id, self.m("no_course"), MENU)
        ws = self.content.worksheets_for(st)
        done = self.db.done_ids(chat_id, st.id)
        bar_n = len(c.stations)
        bar = "".join("🟩" if s.order < st.order else "🟨" if s.order == st.order else "⬜" for s in c.stations)
        lines = [f"🏠 نقشه‌ی خانه‌ی شما", f"مسیر: {c.title}", bar,
                 f"ایستگاه {st.order} از {bar_n}: {st.name}"]
        if st.subtitle:
            lines.append(f"«{st.subtitle}»")
        if ws:
            lines.append(f"تمرین‌های این ایستگاه: {len(done & {w.id for w in ws})} از {len(ws)} انجام شده")
        lines.append(f"کل تمرین‌های انجام‌شده: {self.db.total_done(chat_id)}")
        lines.append("یادآوری روزانه: " + (u["reminder_time"] if u["reminder_time"] else "خاموش"))
        tasks = st.weekly_tasks
        if tasks:
            lines.append("\n📌 کارهای این هفته:\n" + "\n".join(f"• {t}" for t in tasks))
        kb = inline_keyboard([[("📝 تمرین امروز", "practice:1"), ("🔄 تغییر ایستگاه", "st:0")],
                              [("➡️ ایستگاه بعد", "nextst:1")]])
        self.say(chat_id, "\n".join(lines), kb)

    def cmd_stations(self, chat_id: int) -> None:
        u = self.db.user(chat_id)
        c = self._course(u)
        if not c:
            return self.say(chat_id, self.m("no_course"), MENU)
        lines = [f"🧭 ایستگاه‌های «{c.title}»\n"]
        for s in c.stations:
            mark = "✅" if s.order < u["station_order"] else "📍" if s.order == u["station_order"] else "▫️"
            lines.append(f"{mark} {s.order}. {s.name}" + (f" — {s.subtitle}" if s.subtitle else ""))
        self.say(chat_id, "\n".join(lines), inline_keyboard([[("🔄 تغییر ایستگاه من", "st:0")]]))

    def cmd_about(self, chat_id: int) -> None:
        self.say(chat_id, self.m("about"), MENU)

    def cmd_help(self, chat_id: int) -> None:
        self.say(chat_id, self.m("help"), MENU)

    def cmd_reset(self, chat_id: int) -> None:
        self.db.clear_history(chat_id)
        self.say(chat_id, self.m("history_cleared"), MENU)

    # ───────────── یادآوری ─────────────
    def cmd_reminder(self, chat_id: int, arg: str) -> None:
        arg = arg.strip()
        if not arg:
            return self._ask_reminder(chat_id)
        if arg in ("off", "خاموش"):
            self.db.update_user(chat_id, reminder_time="")
            return self.say(chat_id, self.m("reminder_off"), MENU)
        from .textutil import normalize
        arg = normalize(arg).replace("٫", ":")
        mt = HHMM.match(arg)
        if not mt:
            return self.say(chat_id, self.m("reminder_bad"))
        hhmm = f"{int(mt.group(1)):02d}:{mt.group(2)}"
        self.db.update_user(chat_id, reminder_time=hhmm, last_reminder_day="")
        self.say(chat_id, self.m("reminder_on", time=hhmm), MENU)

    def send_due_reminders(self, now: datetime | None = None) -> int:
        now = now or datetime.now(self.tz)
        hhmm, day, sent = now.strftime("%H:%M"), now.strftime("%Y-%m-%d"), 0
        for u in self.db.all_users():
            if not u["consent"] or not u["reminder_time"] or u["last_reminder_day"] == day:
                continue
            if u["reminder_time"] <= hhmm:  # اگر بازو لحظه‌ای خاموش بوده، همان روز جبران می‌شود
                st = self._station(u)
                text = self.m("reminder", name=u["name"] or "", station=st.name if st else "")
                try:
                    self.say(u["chat_id"], text, inline_keyboard([[("📝 تمرین امروز", "practice:1")]]))
                    sent += 1
                except Exception:
                    log.warning("reminder failed for %s", u["chat_id"])
                self.db.update_user(u["chat_id"], last_reminder_day=day)
        return sent

    # ───────────── تمرین ─────────────
    def cmd_practice(self, chat_id: int, skip: str = "") -> None:
        u = self.db.user(chat_id)
        st = self._station(u)
        if not st:
            return self.say(chat_id, self.m("no_course"), MENU)
        done = self.db.done_ids(chat_id, st.id)
        todo = [w for w in self.content.worksheets_for(st) if w.id not in done and w.id != skip]
        if todo:
            w = todo[0]
            steps = "\n".join(f"{i}. {s}" for i, s in enumerate(w.steps, 1))
            text = f"📝 {w.title}\n⏱ حدود {w.minutes} دقیقه\n"
            if w.purpose:
                text += f"\n🎯 {w.purpose}\n"
            text += f"\n{steps}"
            if w.reflection:
                text += f"\n\n💭 {w.reflection}"
            return self.say(chat_id, text, inline_keyboard([
                [("✅ انجام دادم", f"done:{w.id}")],
                [("🔁 تمرین دیگر", f"more:{w.id}"), ("❓ سؤال دارم", "profile:1")]]))
        if st.weekly_tasks:
            task = st.weekly_tasks[int(time.time() // 86400) % len(st.weekly_tasks)]
            return self.say(chat_id, f"📌 تمرین امروز در ایستگاه «{st.name}»:\n\n{task}",
                            inline_keyboard([[("✅ انجام دادم", "done:gen")]]))
        self.say(chat_id, self._generate_practice(u, st),
                 inline_keyboard([[("✅ انجام دادم", "done:gen")], [("🔁 یک تمرین دیگر", "more:gen")]]))

    def _generate_practice(self, user, st: Station) -> str:
        hits = self.kb.search(f"{st.name} {st.subtitle} {st.summary}", k=4, boost_terms=st.keywords)
        prompt = build_user_prompt(
            f"برای ایستگاه «{st.name}» ({st.subtitle}) یک تمرین کوتاه، عملی و ۵ تا ۱۰ دقیقه‌ای برای امروز بساز. "
            "در سه تا پنج گام شماره‌دار، با زبان ساده و خانه‌مانند، بدون نقل مستقیم از مواد مرجع.", hits)
        try:
            out = self.llm.generate(build_system_prompt(self.content.persona, st, user["name"]),
                                    [Msg("user", prompt)], 600)
        except LLMError:
            return self.m("llm_error")
        return "📝 تمرین پیشنهادی امروز\n\n" + out.replace(REFER_MARK, "").strip()

    def ask_checkin(self, chat_id: int, ws_id: str) -> None:
        """چک‌این ۳۰ ثانیه‌ای: انجام شد / تا حدی / هنوز نه."""
        self.say(chat_id, self.m("checkin_ask"), inline_keyboard([
            [("✅ انجام شد", f"ci:d:{ws_id}")], [("🌓 تا حدی", f"ci:p:{ws_id}")],
            [("⏳ هنوز نه", f"ci:n:{ws_id}")]]))

    def ask_obstacle(self, chat_id: int, code: str, ws_id: str) -> None:
        rows = [[(label, f"ob:{key}:{code}:{ws_id}")] for key, label in OBSTACLE_LABELS.items()]
        self.say(chat_id, self.m("obstacle_ask"), inline_keyboard(rows))

    def mark_done(self, chat_id: int, ws_id: str, status: str = "done", obstacle: str = "") -> None:
        u = self.db.user(chat_id)
        st = self._station(u)
        if not st:
            return
        wid = f"gen-{self.today()}" if ws_id == "gen" else ws_id
        self.db.mark_done(chat_id, st.id, wid, status, obstacle)
        if status == "not":
            return self.say(chat_id, self.m("checkin_not"), inline_keyboard([[("🧭 نقشه‌ی من", "profile:1")]]))
        ws = self.content.worksheets_for(st)
        remaining = [w for w in ws if w.id not in self.db.done_ids(chat_id, st.id)]
        if ws and not remaining:
            return self.say(chat_id, self.m("station_complete", station=st.name),
                            inline_keyboard([[("➡️ ایستگاه بعد", "nextst:1")], [("🧭 نقشه‌ی من", "profile:1")]]))
        self.say(chat_id, self.m("practice_done"), inline_keyboard([[("📝 تمرین بعدی", "practice:1"), ("🧭 نقشه‌ی من", "profile:1")]]))

    def cmd_done_latest(self, chat_id: int) -> None:
        u = self.db.user(chat_id)
        st = self._station(u)
        todo = [w for w in self.content.worksheets_for(st) if w.id not in self.db.done_ids(chat_id, st.id)] if st else []
        self.ask_checkin(chat_id, todo[0].id if todo else "gen")

    # ───────────── پرسش‌وپاسخ ─────────────
    def answer_question(self, chat_id: int, text: str) -> None:
        u = self.db.user(chat_id)
        sres = safety.check(text, self.content.safety)
        if sres.level == "emergency":
            return self.say(chat_id, self.content.safety.get("emergency_message", self.m("error")),
                            inline_keyboard([[("🤝 ارتباط با مشاور", "refer:start")]]))
        n = self.db.bump_usage(chat_id, self.today())
        if n > self.cfg.daily_question_limit:
            return self.say(chat_id, self.m("limit_reached"), MENU)
        st = self._station(u)
        self.db.add_question_stat(u["station_order"])
        hits = self.kb.search(text, k=5, boost_terms=(st.keywords if st else []))
        smalltalk = len(tokens(text)) < 2
        weak = (not hits) or hits[0].score < self.cfg.min_retrieval_score
        if weak and not smalltalk and self.cfg.min_retrieval_score > 0 and sres.level != "refer":
            self.db.add_unanswered(text)
            return self.say(chat_id, self.m("out_of_scope"),
                            inline_keyboard([[("🤝 ارتباط با مشاور", "refer:start")]]))
        self.client.typing(chat_id)
        history = [Msg(r, c) for r, c in self.db.history(chat_id, 6)]
        history.append(Msg("user", build_user_prompt(text, [] if smalltalk else hits)))
        try:
            out = self.llm.generate(build_system_prompt(self.content.persona, st, u["name"]), history, 700)
        except LLMError:
            log.exception("LLM failed")
            return self.say(chat_id, self.m("llm_error"), MENU)
        need_refer = REFER_MARK in out or sres.level == "refer"
        out = out.replace(REFER_MARK, "").strip() or self.m("error")
        self.db.add_history(chat_id, "user", text)
        self.db.add_history(chat_id, "assistant", out)
        markup = inline_keyboard([[("🤝 ارتباط با مشاور", "refer:start")]]) if need_refer else None
        self.say(chat_id, out, markup)

    # ───────────── ارجاع به مشاور ─────────────
    def cmd_counselor(self, chat_id: int) -> None:
        self.say(chat_id, self.m("refer_intro"), inline_keyboard([[("✍️ تکمیل فرم کوتاه", "refer:go")],
                                                                  [("فعلاً نه", "profile:1")]]))

    def _referral_begin(self, chat_id: int) -> None:
        if not self.content.referral_form:
            return self.say(chat_id, self.m("error"), MENU)
        self.db.set_state(chat_id, "refer", {"i": 0, "answers": {}})
        self.say(chat_id, self.content.referral_form[0]["ask"])

    def _referral_answer(self, chat_id: int, text: str, data: dict) -> None:
        form = self.content.referral_form
        i = data.get("i", 0)
        data["answers"][form[i]["key"]] = text
        if i + 1 < len(form):
            data["i"] = i + 1
            self.db.set_state(chat_id, "refer", data)
            return self.say(chat_id, form[i + 1]["ask"])
        self.db.set_state(chat_id)
        u = self.db.user(chat_id)
        payload = {"answers": data["answers"], "chat_id": chat_id, "name": u["name"], "username": u["username"]}
        rid = self.db.add_referral(chat_id, payload)
        st = self._station(u)
        lines = [f"📨 درخواست ارجاع #{rid}", f"کاربر: {u['name'] or '-'}"
                 + (f" (@{u['username']})" if u["username"] else "") + f" | شناسه: {chat_id}",
                 f"ایستگاه: {st.name if st else '-'}", ""]
        for q in form:
            lines.append(f"▪️ {q.get('label', q['key'])}: {data['answers'].get(q['key'], '-')}")
        dest = self.cfg.referral_chat_id or (self.cfg.admin_ids[0] if self.cfg.admin_ids else None)
        if dest:
            try:
                self.client.send_message(dest, "\n".join(lines))
            except Exception:
                log.exception("referral delivery failed")
        self.say(chat_id, self.m("refer_done"), MENU)

    # ───────────── ابزار ادمین ─────────────
    def _admin_cmd(self, chat_id: int, cmd: str, arg: str) -> None:
        if cmd == "/stats":
            days = int(arg) if arg.isdigit() else 7
            s = self.db.stats(int(time.time()) - days * 86400)
            st_lines = "\n".join(f"  ایستگاه {o}: {n} نفر" for o, n in s["by_station"]) or "  -"
            un = "\n".join(f"• {q}" for q in s["unanswered"]) or "• -"
            names = {"done": "انجام شد", "partial": "تا حدی", "not": "هنوز نه"}
            ci = "، ".join(f"{names.get(k, k)}: {n}" for k, n in s["checkins"]) or "-"
            ob = "، ".join(f"{k}: {n}" for k, n in s["obstacles"]) or "-"
            return self.say(chat_id, f"📊 گزارش {days} روز اخیر\nکاربران: {s['users']} (جدید: {s['new_users']})\n"
                            f"پرسش‌ها: {s['questions']}\nتمرین‌های انجام‌شده: {s['worksheets_done']}\n"
                            f"چک‌این‌ها: {ci}\nموانع: {ob}\n"
                            f"ارجاع‌ها: {s['referrals']}\n\nتوزیع ایستگاه‌ها:\n{st_lines}\n\n"
                            f"پرسش‌های بی‌پاسخ (خلأ محتوایی):\n{un}")
        if cmd == "/reload":
            return self.say(chat_id, self._reload_text())
        if cmd == "/sources":
            lines = [f"• {n}: «{i['title']}» — {i['chunks']} تکه" for n, i in self.kb.sources.items()]
            return self.say(chat_id, "منابع بارگذاری‌شده:\n" + ("\n".join(lines) or "هیچ"))
        if cmd == "/delknowledge":
            p = self.cfg.knowledge_dir / arg
            if arg and p.parent == self.cfg.knowledge_dir and p.exists():
                p.unlink()
                return self.say(chat_id, "حذف شد.\n" + self._reload_text())
            return self.say(chat_id, "نام فایل را دقیق بنویسید (از /sources).")
        if cmd == "/adminhelp":
            return self.say(chat_id, self.m("admin_help"))
        self.say(chat_id, self.m("admin_help"))

    def _reload_text(self) -> str:
        n = self.kb.reload()
        probs = self.content.reload()
        out = f"✅ بارگذاری شد: {n} تکه‌ی محتوایی از {len(self.kb.sources)} منبع؛ {len(self.content.courses)} دوره، {len(self.content.worksheets)} کاربرگ."
        if probs:
            out += "\n⚠️ مشکلات:\n" + "\n".join(f"• {p}" for p in probs[:10])
        return out

    def _on_document(self, chat_id: int, msg: dict) -> None:
        if not self.is_admin(chat_id):
            return self.say(chat_id, self.m("text_only"))
        doc, caption = msg["document"], (msg.get("caption") or "").strip()
        name = re.sub(r"[^\w.\-]+", "_", doc.get("file_name") or "file", flags=re.UNICODE)
        ext = name.rsplit(".", 1)[-1].lower() if "." in name else ""
        state = self.db.state(chat_id)[0]
        if not caption and state == "adm_k":
            caption = "/addknowledge"
        elif not caption and state == "adm_c":
            caption = "/addcontent"
        if not caption.startswith(("/addknowledge", "/addcontent")):
            return self.say(chat_id, "برای افزودن، فایل را با کپشن /addknowledge (محتوای پشتیبان: docx/md/txt) "
                            "یا /addcontent (دوره/کاربرگ: yaml) بفرستید.")
        data = self.client.download_file(doc["file_id"])
        if caption.startswith("/addknowledge"):
            if ext not in ("md", "txt", "docx"):
                return self.say(chat_id, "برای محتوای پشتیبان فقط md، txt یا docx بفرستید.")
            self.cfg.knowledge_dir.mkdir(parents=True, exist_ok=True)
            tmp = self.cfg.knowledge_dir / f".upload.{ext}"
            tmp.write_bytes(data)
            try:
                md = to_markdown(tmp, title=caption.partition(" ")[2].strip() or None)
            finally:
                tmp.unlink(missing_ok=True)
            dest = self.cfg.knowledge_dir / (name.rsplit(".", 1)[0] + ".md")
            dest.write_text(md, encoding="utf-8")
            return self.say(chat_id, f"ذخیره شد: {dest.name}\n" + self._reload_text())
        # /addcontent
        try:
            parsed = yaml.safe_load(data.decode("utf-8"))
        except (yaml.YAMLError, UnicodeDecodeError) as e:
            return self.say(chat_id, f"❌ فایل YAML معتبر نیست: {str(e)[:200]}")
        kind, errs = validate_data(parsed)
        if errs:
            return self.say(chat_id, "❌ فایل اعتبارسنجی نشد:\n" + "\n".join(f"• {e}" for e in errs))
        sub = "courses" if kind == "course" else "worksheets"
        d = self.cfg.content_dir / sub
        d.mkdir(parents=True, exist_ok=True)
        (d / (re.sub(r"[^\w\-]+", "_", str(parsed["id"])) + ".yaml")).write_bytes(data)
        self.say(chat_id, f"✅ {('دوره' if kind == 'course' else 'کاربرگ')} «{parsed.get('title')}» ذخیره شد.\n" + self._reload_text())
