"""پنل مدیریت دکمه‌ای (بدون دستور و بدون YAML دستی) برای ادمین‌ها."""
from __future__ import annotations

import logging
import re
import threading
import time
from pathlib import Path

import yaml

from .bale import inline_keyboard, reply_keyboard

log = logging.getLogger(__name__)
BTN_ADMIN = "🛠 پنل مدیریت"
BACK = [("⬅️ پنل مدیریت", "adm:home")]
MEDIA_FA = {"text": "متن", "photo": "عکس", "document": "فایل", "video": "ویدیو", "audio": "صوت",
            "voice": "پیام صوتی", "animation": "گیف"}
SEND_GAP = 0.06  # فاصله‌ی ارسال‌ها (حدود ۱۵ پیام در ثانیه) تا بله محدود نکند


class AdminMixin:
    # ── ورودی‌ها ──
    def admin_home(self, chat_id: int) -> None:
        self.db.set_state(chat_id)
        self.say(chat_id, "🛠 پنل مدیریت مهرمان", inline_keyboard([
            [("📊 گزارش هفته", "adm:stats:7"), ("📊 گزارش ماه", "adm:stats:30")],
            [("➕ افزودن محتوای پشتیبان", "adm:addk"), ("➕ افزودن کاربرگ", "adm:addw")],
            [("📤 ارسال پیام به مخاطبان", "adm:bc"), ("👥 فهرست کاربران", "adm:users")],
            [("📚 منابع / حذف", "adm:src"), ("🔄 بارگذاری مجدد", "adm:reload")],
            [("📁 افزودن دوره (فایل)", "adm:addc")]]))

    def _station_rows(self, prefix: str) -> list:
        c = self.content.default_course
        rows, row = [], []
        for s in (c.stations if c else []):
            row.append((f"{s.order}. {s.name}", f"{prefix}{s.order}"))
            if len(row) == 2:
                rows.append(row)
                row = []
        if row:
            rows.append(row)
        return rows

    def _spawn(self, fn) -> None:
        """کار طولانی (ارسال گروهی) در پس‌زمینه تا بازو به بقیه جواب بدهد. تست‌ها این را همگام می‌کنند."""
        threading.Thread(target=fn, daemon=True, name="broadcast").start()

    def admin_callback(self, chat_id: int, val: str) -> None:
        if not self.is_admin(chat_id):
            return
        act, _, rest = val.partition(":")
        if act == "home":
            return self.admin_home(chat_id)
        if act == "stats":
            return self._admin_cmd(chat_id, "/stats", rest or "7")
        if act == "reload":
            return self.say(chat_id, self._reload_text(), inline_keyboard([BACK]))
        if act == "addk":
            self.db.set_state(chat_id, "adm_k")
            return self.say(chat_id, "محتوای پشتیبان را بفرستید: یا متن را همین‌جا بنویسید/پیست کنید، "
                            "یا یک فایل Word/‏md/‏txt بفرستید. (برای انصراف: /cancel)")
        if act == "addc":
            self.db.set_state(chat_id, "adm_c")
            return self.say(chat_id, "فایل دوره (yaml) را بفرستید. برای کاربرگ ساده‌تر است از «افزودن کاربرگ» استفاده کنید.")
        if act == "src":
            names = list(self.kb.sources)
            if not names:
                return self.say(chat_id, "هنوز منبعی نیست.", inline_keyboard([BACK]))
            rows = [[(f"🗑 {self.kb.sources[n]['title'][:28]}", f"adm:del:{i}")] for i, n in enumerate(names)]
            return self.say(chat_id, "منابع بارگذاری‌شده (برای حذف، روی منبع بزنید):", inline_keyboard(rows + [BACK]))
        if act == "del":
            n = list(self.kb.sources)[int(rest)]
            return self.say(chat_id, f"«{self.kb.sources[n]['title']}» حذف شود؟",
                            inline_keyboard([[("بله، حذف", f"adm:delok:{rest}"), ("انصراف", "adm:src")]]))
        if act == "delok":
            n = list(self.kb.sources)[int(rest)]
            (self.cfg.knowledge_dir / n).unlink(missing_ok=True)
            return self.say(chat_id, "حذف شد.\n" + self._reload_text(), inline_keyboard([BACK]))
        if act == "addw":
            return self.say(chat_id, "کاربرگ برای کدام ایستگاه (جلسه) است؟",
                            inline_keyboard(self._station_rows("adm:ws:") + [BACK]))
        if act == "ws":
            self.db.set_state(chat_id, "adm_w_title", {"order": int(rest)})
            return self.say(chat_id, "عنوان کاربرگ را بنویسید:")
        if act == "wsave":
            return self._admin_ws_save(chat_id)
        if act == "users":
            return self._admin_users(chat_id)
        # ── ارسال پیام به مخاطبان ──
        if act == "bc":
            self.db.set_state(chat_id, "adm_b_msg")
            return self.say(chat_id, "📤 پیامی را که می‌خواهید برای مخاطبان برود بفرستید: متن، عکس، فایل (PDF/Word)، "
                            "ویدیو یا صوت؛ برای عکس و فایل می‌توانید توضیح (کپشن) هم بنویسید.\nبرای انصراف: /cancel")
        state, data = self.db.state(chat_id)
        if act.startswith("bc") and not data.get("item"):
            return self.admin_home(chat_id)
        if act == "bca":
            if rest == "all":
                data["aud"] = {"type": "all"}
                return self._bc_confirm(chat_id, data)
            if rest == "st":
                self.db.set_state(chat_id, "adm_b_aud", data)
                return self.say(chat_id, "برای مخاطبان کدام ایستگاه (جلسه) فرستاده شود؟",
                                inline_keyboard(self._station_rows("adm:bcs:") + [BACK]))
            if rest == "sel":
                self.db.set_state(chat_id, "adm_b_sel", data)
                return self.say(chat_id, "نام‌کاربری (مثل @ali) یا شناسه‌ی عددی گیرنده‌ها را بفرستید؛ "
                                "هر کدام در یک خط یا با کاما جدا.\n(شناسه‌ها را در «👥 فهرست کاربران» ببینید.)")
        if act == "bcs":
            data["aud"] = {"type": "station", "order": int(rest)}
            return self._bc_confirm(chat_id, data)
        if act == "bcgo":
            return self._bc_start(chat_id, data)

    # ── حالت‌های چندمرحله‌ای (متن آزاد) ──
    def admin_state_text(self, chat_id: int, text: str, state: str, data: dict) -> bool:
        if not self.is_admin(chat_id):
            return False
        if state == "adm_k":
            self._save_knowledge(chat_id, text, title=None)
            return True
        if state == "adm_w_title":
            data["title"] = text[:80]
            self.db.set_state(chat_id, "adm_w_steps", data)
            self.say(chat_id, "حالا محتوای کاربرگ را بفرستید:\n• گام‌های تمرین به‌صورت متن (هر گام در یک خط)، و/یا\n"
                     "• فایل کاربرگ (PDF/Word) یا عکس آن — چند فایل هم می‌شود.\nبعد «✅ ذخیره» را بزنید.")
            return True
        if state == "adm_w_steps":
            steps = [re.sub(r"^\s*(\d+[.)\-]|[•\-*])\s*", "", l).strip() for l in text.splitlines()]
            data["steps"] = data.get("steps", []) + [s for s in steps if s]
            self.db.set_state(chat_id, "adm_w_steps", data)
            self._ws_progress(chat_id, data)
            return True
        if state == "adm_b_msg":
            return self.admin_state_media(chat_id, {"kind": "text", "text": text}, state, data)
        if state == "adm_b_sel":
            refs = [r for r in re.split(r"[\s,،]+", text) if r.strip()]
            found, missing = self.db.find_users(refs)
            if not found:
                self.say(chat_id, "هیچ کاربری پیدا نشد. نام‌کاربری یا شناسه را دوباره بفرستید (کاربر باید قبلاً "
                         "بازو را استارت کرده باشد).")
                return True
            data["aud"] = {"type": "ids", "ids": [u["chat_id"] for u in found]}
            names = "، ".join((u["name"] or str(u["chat_id"])) + (f" (@{u['username']})" if u["username"] else "")
                             for u in found)
            note = f"\n⚠️ پیدا نشد: {'، '.join(missing)}" if missing else ""
            self.say(chat_id, f"گیرنده‌ها: {names}{note}")
            self._bc_confirm(chat_id, data)
            return True
        return False

    def admin_state_media(self, chat_id: int, item: dict, state: str, data: dict) -> bool:
        """عکس/فایل/متنِ ادمین در حالت ارسال گروهی یا افزودن کاربرگ."""
        if not self.is_admin(chat_id):
            return False
        if state == "adm_b_msg":
            data = {"item": item}
            self.db.set_state(chat_id, "adm_b_aud", data)
            self.say(chat_id, "👇 پیش‌نمایش پیامی که فرستاده می‌شود:")
            self.client.send_media(chat_id, item)
            self.say(chat_id, "برای چه کسانی فرستاده شود؟", inline_keyboard([
                [("👥 همه‌ی مخاطبان", "adm:bca:all")],
                [("📍 مخاطبان یک ایستگاه (جلسه)", "adm:bca:st")],
                [("👤 کاربران انتخابی", "adm:bca:sel")], BACK]))
            return True
        if state == "adm_w_steps" and item["kind"] != "text":
            data.setdefault("attachments", []).append(item)
            self.db.set_state(chat_id, "adm_w_steps", data)
            self._ws_progress(chat_id, data)
            return True
        return False

    def _ws_progress(self, chat_id: int, data: dict) -> None:
        steps, atts = data.get("steps", []), data.get("attachments", [])
        body = "\n".join(f"{i}. {s}" for i, s in enumerate(steps, 1))
        files = "، ".join(MEDIA_FA.get(a["kind"], a["kind"]) for a in atts)
        self.say(chat_id, f"📝 {data['title']}\n" + (f"\n{body}\n" if body else "")
                 + (f"\n📎 پیوست: {files}\n" if files else "")
                 + "\nمی‌توانید گام یا فایل/عکس دیگری هم بفرستید؛ وگرنه ذخیره کنید.",
                 inline_keyboard([[("✅ ذخیره", "adm:wsave"), ("انصراف", "adm:home")]]))

    def _bc_recipients(self, aud: dict) -> list[int]:
        if aud["type"] == "all":
            return self.db.audience()
        if aud["type"] == "station":
            return self.db.audience(aud["order"])
        return list(aud.get("ids", []))

    def _bc_label(self, aud: dict) -> str:
        if aud["type"] == "all":
            return "همه‌ی مخاطبان"
        if aud["type"] == "station":
            c = self.content.default_course
            st = c.by_order(aud["order"]) if c else None
            return f"مخاطبان ایستگاه {aud['order']}" + (f" ({st.name})" if st else "")
        return f"{len(aud.get('ids', []))} کاربر انتخابی"

    def _bc_confirm(self, chat_id: int, data: dict) -> None:
        n = len(self._bc_recipients(data["aud"]))
        self.db.set_state(chat_id, "adm_b_ok", data)
        if not n:
            return self.say(chat_id, f"در «{self._bc_label(data['aud'])}» هنوز کسی نیست.", inline_keyboard([BACK]))
        self.say(chat_id, f"ارسال به {self._bc_label(data['aud'])}: {n} نفر. فرستاده شود؟",
                 inline_keyboard([[("✅ ارسال", "adm:bcgo"), ("انصراف", "adm:home")]]))

    def _bc_start(self, chat_id: int, data: dict) -> None:
        state, _ = self.db.state(chat_id)
        if state != "adm_b_ok" or not data.get("aud"):
            return self.admin_home(chat_id)
        ids, item, label = self._bc_recipients(data["aud"]), data["item"], self._bc_label(data["aud"])
        self.db.set_state(chat_id)
        bid = self.db.add_broadcast(chat_id, label, item, len(ids))
        self.say(chat_id, f"⏳ ارسال به {len(ids)} نفر شروع شد؛ پایانش را خبر می‌دهم.")

        def run():
            sent = failed = 0
            for uid in ids:
                try:
                    self.client.send_media(uid, item)
                    sent += 1
                except Exception:
                    failed += 1
                    log.warning("broadcast %s to %s failed", bid, uid)
                time.sleep(SEND_GAP)
            self.db.finish_broadcast(bid, sent, failed)
            self.say(chat_id, f"✅ ارسال #{bid} ({label}) تمام شد: {sent} موفق"
                     + (f"، {failed} ناموفق (احتمالاً بازو را بسته‌اند)" if failed else "") + ".",
                     inline_keyboard([BACK]))

        self._spawn(run)

    def _admin_users(self, chat_id: int) -> None:
        users = [u for u in self.db.all_users() if u["consent"]]
        if not users:
            return self.say(chat_id, "هنوز کاربری نیست.", inline_keyboard([BACK]))
        lines = [f"👥 کاربران ({len(users)} نفر) — نام | نام‌کاربری | شناسه | ایستگاه"]
        for u in sorted(users, key=lambda r: -(r["created_at"] or 0))[:200]:
            lines.append(f"• {u['name'] or '-'} | " + (f"@{u['username']}" if u["username"] else "-")
                         + f" | {u['chat_id']} | {u['station_order'] if u['course_id'] else '-'}")
        self.say(chat_id, "\n".join(lines), inline_keyboard([BACK]))

    def _admin_ws_save(self, chat_id: int) -> None:
        state, data = self.db.state(chat_id)
        if state not in ("adm_w_steps", "adm_w_confirm") or not (data.get("steps") or data.get("attachments")):
            return self.admin_home(chat_id)
        c = self.content.default_course
        st = c.by_order(data["order"])
        n = len([w for w in self.content.worksheets.values() if w.station == st.id]) + 1
        wid = f"{st.id}-ws{n}"
        doc = {"id": wid, "station": st.id, "title": data["title"], "minutes": 10, "steps": data.get("steps", [])}
        if data.get("attachments"):
            doc["attachments"] = data["attachments"]
        d = self.cfg.content_dir / "worksheets"
        d.mkdir(parents=True, exist_ok=True)
        (d / f"{wid}.yaml").write_text(yaml.safe_dump(doc, allow_unicode=True, sort_keys=False), encoding="utf-8")
        self.db.set_state(chat_id)
        self.say(chat_id, f"✅ کاربرگ برای «{st.name}» ذخیره شد.\n" + self._reload_text(), inline_keyboard([BACK]))

    def _save_knowledge(self, chat_id: int, body: str, title: str | None) -> None:
        first = body.strip().splitlines()[0][:40] if body.strip() else "منبع"
        title = title or first
        slug = re.sub(r"[^\w\-]+", "-", title, flags=re.UNICODE).strip("-") or "source"
        name = f"{slug}-{int(time.time())}.md"
        self.cfg.knowledge_dir.mkdir(parents=True, exist_ok=True)
        (self.cfg.knowledge_dir / name).write_text(
            f'---\ntitle: "{title.replace(chr(34), chr(39))}"\naudience: internal\n---\n\n{body}\n', encoding="utf-8")
        self.db.set_state(chat_id)
        self.say(chat_id, f"✅ ذخیره شد: «{title}»\n" + self._reload_text(), inline_keyboard([BACK]))
