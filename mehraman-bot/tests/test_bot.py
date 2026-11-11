import shutil
from datetime import datetime
from pathlib import Path

import pytest

from mehraman_bot.bale import split_text
from mehraman_bot.config import Config
from mehraman_bot.content import Content, validate_data
from mehraman_bot.db import DB
from mehraman_bot.handlers import Bot
from mehraman_bot.knowledge import KnowledgeBase
from mehraman_bot.llm import LLMError
from mehraman_bot.safety import check
from mehraman_bot.textutil import normalize, tokens

ROOT = Path(__file__).resolve().parents[1]


class FakeClient:
    def __init__(self):
        self.sent = []  # (chat_id, text, markup)
        self.media = []  # (chat_id, item) — عکس/فایل/متنِ ارسال‌شده با send_media
        self.files = {}

    def send_message(self, chat_id, text, markup=None):
        self.sent.append((chat_id, text, markup))

    def send_media(self, chat_id, item, markup=None):
        if chat_id == 404:
            raise RuntimeError("blocked")
        self.media.append((chat_id, item))

    def answer_callback(self, *a, **k): pass
    def typing(self, *a): pass
    def download_file(self, fid): return self.files[fid]

    def last(self, chat=None):
        return [s for s in self.sent if chat is None or s[0] == chat][-1]


class FakeLLM:
    def __init__(self, reply="پاسخ آزمایشی"):
        self.reply, self.calls, self.fail = reply, [], False

    def generate(self, system, messages, max_tokens=700):
        if self.fail:
            raise LLMError("x")
        self.calls.append((system, messages))
        return self.reply


@pytest.fixture
def env(tmp_path):
    content = tmp_path / "content"
    shutil.copytree(ROOT / "content", content)
    (content / "worksheets" / "s03-ws1.yaml").write_text(
        (ROOT / "content/worksheets/_example.yaml").read_text(encoding="utf-8"), encoding="utf-8")
    kn = tmp_path / "knowledge"
    shutil.copytree(ROOT / "knowledge", kn)
    (kn / "listen.md").write_text((ROOT / "knowledge/_example.md").read_text(encoding="utf-8"), encoding="utf-8")
    cfg = Config(bale_token="t", admin_ids=[1], referral_chat_ids=[999], data_dir=tmp_path / "d",
                 content_dir=content, knowledge_dir=kn, min_retrieval_score=0.5, daily_question_limit=3)
    client, llm = FakeClient(), FakeLLM()
    bot = Bot(cfg, client, DB(":memory:"), Content(content), KnowledgeBase(kn), llm)
    return bot, client, llm


def msg(chat, text, **extra):
    return {"update_id": 1, "message": {"chat": {"id": chat}, "from": {"id": chat, "first_name": "سارا"},
                                         "text": text, **extra}}


def cb(chat, data):
    return {"update_id": 1, "callback_query": {"id": "c", "data": data, "from": {"id": chat, "first_name": "سارا"},
                                                "message": {"chat": {"id": chat}}}}


def onboard(bot, chat=5, station=3):
    bot.handle_update(msg(chat, "/start"))
    bot.handle_update(cb(chat, "consent:1"))
    bot.handle_update(cb(chat, f"st:{station}"))
    bot.handle_update(cb(chat, "rm:20:30"))


# ── ابزارهای متنی ──
def test_normalize_and_tokens():
    assert normalize("كيف ۱۲٣") == "کیف 123"
    assert "گوش" in tokens("گوش‌دادن‌های فعال")[0] or tokens("گوش دادن")


def test_split_text_respects_limit():
    t = "\n\n".join(["الف" * 500] * 20)
    parts = split_text(t, 1500)
    assert all(len(p) <= 1500 for p in parts) and len(parts) > 3


def test_safety():
    cfg = Content(ROOT / "content").safety
    assert check("می‌خوام بمیرم", cfg).level == "emergency"
    assert check("به فکر طلاق افتادم", cfg).level == "refer"
    assert check("چطور بهتر گوش بدهم", cfg).level == "ok"


# ── محتوا ──
def test_content_loads_and_validates():
    c = Content(ROOT / "content")
    assert len(c.default_course.stations) == 15 and not c.problems
    assert validate_data({"id": "a", "title": "t", "stations": [{"id": "s", "order": 1}]})[1]
    kind, errs = validate_data({"id": "w", "station": "s1", "title": "t", "steps": "not list"})
    assert kind == "worksheet" and errs


def test_retrieval_finds_relevant_chunk(env):
    bot, _, _ = env
    hits = bot.kb.search("چطور گوش دادن فعال یاد بگیرم؟")
    assert hits and "گوش" in hits[0].chunk.text
    assert not bot.kb.search("قیمت بیت‌کوین")


# ── جریان‌ها ──
def test_onboarding_and_profile(env):
    bot, client, _ = env
    onboard(bot)
    u = bot.db.user(5)
    assert u["consent"] == 1 and u["station_order"] == 3 and u["reminder_time"] == "20:30"
    bot.handle_update(msg(5, "🧭 نقشه‌ی من"))
    assert "چراغ تشخیص" in client.last(5)[1]


def test_no_consent_blocks_features(env):
    bot, client, llm = env
    bot.handle_update(msg(7, "سلام گوش دادن"))
    assert not llm.calls and "موافقم" in str(client.last(7)[2])


def test_practice_checkin_flow(env):
    bot, client, _ = env
    onboard(bot)
    bot.handle_update(msg(5, "📝 تمرین امروز"))
    assert "چهار جمله‌ی روشن" in client.last(5)[1]
    bot.handle_update(cb(5, "done:s03-ws1"))
    assert "تمرین چطور" in client.last(5)[1]
    bot.handle_update(cb(5, "ci:p:s03-ws1"))
    bot.handle_update(cb(5, "ob:time:p:s03-ws1"))
    row = bot.db.conn.execute("select status,obstacle from progress").fetchone()
    assert (row[0], row[1]) == ("partial", "وقت")
    assert "تمام شد" in client.last(5)[1]  # همه‌ی کاربرگ‌های ایستگاه انجام شد
    bot.handle_update(cb(5, "nextst:1"))
    assert bot.db.user(5)["station_order"] == 4


def test_not_yet_does_not_complete(env):
    bot, _, _ = env
    onboard(bot)
    bot.handle_update(cb(5, "ob:forgot:n:s03-ws1"))
    assert bot.db.total_done(5) == 0


def test_question_answered_with_context(env):
    bot, client, llm = env
    onboard(bot)
    bot.handle_update(msg(5, "چطور گوش دادن فعال یاد بگیرم؟"))
    system, messages = llm.calls[-1]
    assert "دستیار هوشمند مهرمان" in system and "چراغ تشخیص" in system
    assert "<مواد_مرجع>" in messages[-1].content and "گوش" in messages[-1].content
    assert client.last(5)[1] == "پاسخ آزمایشی"


def test_out_of_scope_skips_llm_and_logs(env):
    bot, client, llm = env
    onboard(bot)
    bot.handle_update(msg(5, "قیمت بیت کوین و اتریوم چقدر است"))
    assert not llm.calls
    assert bot.db.stats(0)["unanswered"]


def test_refer_marker_adds_button(env):
    bot, client, llm = env
    onboard(bot)
    llm.reply = "همدلی کوتاه.\n[[REFER]]"
    bot.handle_update(msg(5, "گوش دادن همسرم کم است"))
    text, markup = client.last(5)[1], client.last(5)[2]
    assert "[[REFER]]" not in text and "refer:start" in str(markup)


def test_emergency_bypasses_llm(env):
    bot, client, llm = env
    onboard(bot)
    bot.handle_update(msg(5, "می‌خوام بمیرم"))
    assert not llm.calls and "۱۲۳" in client.last(5)[1]


def test_llm_failure_is_graceful(env):
    bot, client, llm = env
    onboard(bot)
    llm.fail = True
    bot.handle_update(msg(5, "چطور گوش دادن فعال یاد بگیرم؟"))
    assert "نتوانستم" in client.last(5)[1]


def test_daily_limit(env):
    bot, client, llm = env
    onboard(bot)
    for _ in range(5):
        bot.handle_update(msg(5, "چطور گوش دادن فعال یاد بگیرم؟"))
    assert len(llm.calls) == 3 and "سقف" in client.last(5)[1]


def test_referral_flow_forwards_to_counselor(env):
    bot, client, _ = env
    onboard(bot)
    bot.handle_update(msg(5, "🤝 ارتباط با مشاور"))
    bot.handle_update(cb(5, "refer:go"))
    for ans in ["اختلاف زیاد", "۵ سال، یک فرزند", "متوسط", "شب‌ها"]:
        bot.handle_update(msg(5, ans))
    to_counselor = [s for s in client.sent if s[0] == 999]
    assert to_counselor and "اختلاف زیاد" in to_counselor[0][1] and "#1" in to_counselor[0][1]
    assert bot.db.state(5)[0] == ""


def test_reminders_fire_once_per_day(env):
    bot, client, _ = env
    onboard(bot)
    at = lambda h, m: datetime(2026, 11, 11, h, m, tzinfo=bot.tz)
    assert bot.send_due_reminders(at(20, 0)) == 0
    assert bot.send_due_reminders(at(20, 31)) == 1
    assert bot.send_due_reminders(at(21, 0)) == 0
    assert "چراغ تشخیص" in client.sent[-2][1]          # پیام روزانه
    assert client.last(5)[1].startswith("📝")             # و خودِ کاربرگ امروز، خودکار


def test_custom_and_off_reminder(env):
    bot, _, _ = env
    onboard(bot)
    bot.handle_update(msg(5, "/reminder ۲۱:۱۵"))
    assert bot.db.user(5)["reminder_time"] == "21:15"
    bot.handle_update(msg(5, "/reminder off"))
    assert bot.db.user(5)["reminder_time"] == ""


def test_admin_stats_and_upload(env):
    bot, client, _ = env
    bot.handle_update(msg(1, "/start"))
    bot.handle_update(msg(1, "/stats"))
    assert "گزارش" in client.last(1)[1]
    # کاربر عادی ادمین نیست
    onboard(bot)
    bot.handle_update(msg(5, "/stats"))
    assert "گزارش" not in client.last(5)[1]
    # آپلود کاربرگ معتبر و نامعتبر
    client.files["f1"] = (ROOT / "content/worksheets/_example.yaml").read_bytes().replace(b"s03-ws1", b"s04-ws1").replace(b"station: s03", b"station: s04")
    bot.handle_update(msg(1, "", caption="/addcontent", document={"file_id": "f1", "file_name": "a.yaml"}))
    assert "s04-ws1" in bot.content.worksheets
    client.files["f2"] = b"id: x\ntitle: y\n"
    bot.handle_update(msg(1, "", caption="/addcontent", document={"file_id": "f2", "file_name": "b.yaml"}))
    assert "❌" in client.last(1)[1]
    # آپلود دانش md
    client.files["f3"] = "---\ntitle: تست\n---\n# مرز\nاحترام به حریم خصوصی همسر مهم است.".encode()
    bot.handle_update(msg(1, "", caption="/addknowledge", document={"file_id": "f3", "file_name": "t.md"}))
    assert "t.md" in bot.kb.sources


def test_non_admin_upload_ignored(env):
    bot, client, _ = env
    onboard(bot)
    bot.handle_update(msg(5, "", caption="/addknowledge", document={"file_id": "x", "file_name": "t.md"}))
    assert not (bot.cfg.knowledge_dir / "t.md").exists()


def test_admin_panel_worksheet_wizard_and_knowledge(env):
    bot, client, llm = env
    onboard(bot, chat=1)
    bot.handle_update(msg(1, "🛠 پنل مدیریت"))
    assert "adm:addw" in str(client.last(1)[2])
    bot.handle_update(cb(1, "adm:addw"))
    bot.handle_update(cb(1, "adm:ws:5"))
    bot.handle_update(msg(1, "بازتاب یک جمله"))
    bot.handle_update(msg(1, "1. یک جمله از همسر بشنوید\n2. با زبان خودتان بازگو کنید"))
    bot.handle_update(cb(1, "adm:wsave"))
    ws = bot.content.worksheets["s05-ws1"]
    assert ws.title == "بازتاب یک جمله" and len(ws.steps) == 2
    # افزودن متن به‌عنوان محتوای پشتیبان
    bot.handle_update(cb(1, "adm:addk"))
    bot.handle_update(msg(1, "حریم خصوصی\nاحترام به حریم همسر مهم است."))
    assert any("حریم" in v["title"] for v in bot.kb.sources.values())
    # حذف با دکمه
    bot.handle_update(cb(1, "adm:del:0"))
    bot.handle_update(cb(1, "adm:delok:0"))
    assert "حذف شد" in client.last(1)[1]


def test_non_admin_cannot_use_panel(env):
    bot, client, _ = env
    onboard(bot)
    bot.handle_update(cb(5, "adm:addk"))
    bot.handle_update(msg(5, "🛠 پنل مدیریت"))
    assert bot.db.state(5)[0] == ""
