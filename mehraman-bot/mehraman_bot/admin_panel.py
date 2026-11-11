"""پنل مدیریت دکمه‌ای (بدون دستور و بدون YAML دستی) برای ادمین‌ها."""
from __future__ import annotations

import re
import time
from pathlib import Path

import yaml

from .bale import inline_keyboard, reply_keyboard

BTN_ADMIN = "🛠 پنل مدیریت"
BACK = [("⬅️ پنل مدیریت", "adm:home")]


class AdminMixin:
    # ── ورودی‌ها ──
    def admin_home(self, chat_id: int) -> None:
        self.db.set_state(chat_id)
        self.say(chat_id, "🛠 پنل مدیریت مهرمان", inline_keyboard([
            [("📊 گزارش هفته", "adm:stats:7"), ("📊 گزارش ماه", "adm:stats:30")],
            [("➕ افزودن محتوای پشتیبان", "adm:addk"), ("➕ افزودن کاربرگ", "adm:addw")],
            [("📚 منابع / حذف", "adm:src"), ("🔄 بارگذاری مجدد", "adm:reload")],
            [("📁 افزودن دوره (فایل)", "adm:addc")]]))

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
            c = self.content.default_course
            rows, row = [], []
            for s in (c.stations if c else []):
                row.append((f"{s.order}. {s.name}", f"adm:ws:{s.order}"))
                if len(row) == 2:
                    rows.append(row)
                    row = []
            if row:
                rows.append(row)
            return self.say(chat_id, "کاربرگ برای کدام ایستگاه است؟", inline_keyboard(rows + [BACK]))
        if act == "ws":
            self.db.set_state(chat_id, "adm_w_title", {"order": int(rest)})
            return self.say(chat_id, "عنوان کاربرگ را بنویسید:")
        if act == "wsave":
            return self._admin_ws_save(chat_id)

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
            self.say(chat_id, "گام‌های تمرین را بفرستید؛ **هر گام در یک خط**:")
            return True
        if state == "adm_w_steps":
            steps = [re.sub(r"^\s*(\d+[.)\-]|[•\-*])\s*", "", l).strip() for l in text.splitlines()]
            data["steps"] = [s for s in steps if s]
            if not data["steps"]:
                self.say(chat_id, "حداقل یک گام لازم است.")
                return True
            self.db.set_state(chat_id, "adm_w_confirm", data)
            body = "\n".join(f"{i}. {s}" for i, s in enumerate(data["steps"], 1))
            self.say(chat_id, f"📝 {data['title']}\n\n{body}\n\nذخیره شود؟",
                     inline_keyboard([[("✅ ذخیره", "adm:wsave"), ("انصراف", "adm:home")]]))
            return True
        return False

    def _admin_ws_save(self, chat_id: int) -> None:
        state, data = self.db.state(chat_id)
        if state != "adm_w_confirm":
            return self.admin_home(chat_id)
        c = self.content.default_course
        st = c.by_order(data["order"])
        n = len([w for w in self.content.worksheets.values() if w.station == st.id]) + 1
        wid = f"{st.id}-ws{n}"
        doc = {"id": wid, "station": st.id, "title": data["title"], "minutes": 10, "steps": data["steps"]}
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
