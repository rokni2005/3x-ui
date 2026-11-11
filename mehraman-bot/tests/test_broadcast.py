"""ارسال پیام/عکس/فایل ادمین به مخاطبان، کاربرگ با پیوست، و ارسال خودکار روزانه."""
from datetime import datetime

from mehraman_bot.bale import extract_media
from mehraman_bot.content import validate_data

from test_bot import cb, env, msg, onboard  # noqa: F401  (fixture)

PHOTO = {"photo": [{"file_id": "small"}, {"file_id": "big"}], "caption": "کاربرگ جلسه‌ی ۳"}


def media_msg(chat, **extra):
    return {"update_id": 1, "message": {"chat": {"id": chat}, "from": {"id": chat, "first_name": "ادمین"}, **extra}}


def _sync(bot):
    bot._spawn = lambda fn: fn()


def test_extract_media():
    assert extract_media(PHOTO) == {"kind": "photo", "file_id": "big", "text": "کاربرگ جلسه‌ی ۳"}
    d = extract_media({"document": {"file_id": "d1", "file_name": "ws.pdf"}})
    assert d["kind"] == "document" and d["name"] == "ws.pdf"
    assert extract_media({"text": " سلام "}) == {"kind": "text", "text": "سلام"}
    assert extract_media({}) is None


def test_broadcast_photo_to_all(env):
    bot, client, _ = env
    _sync(bot)
    onboard(bot, chat=5, station=3)
    onboard(bot, chat=6, station=1)
    bot.handle_update(cb(1, "adm:bc"))
    bot.handle_update(media_msg(1, **PHOTO))
    assert client.media[-1] == (1, {"kind": "photo", "file_id": "big", "text": "کاربرگ جلسه‌ی ۳"})  # پیش‌نمایش
    bot.handle_update(cb(1, "adm:bca:all"))
    assert "2 نفر" in client.last(1)[1]
    bot.handle_update(cb(1, "adm:bcgo"))
    got = sorted(c for c, it in client.media if it["file_id"] == "big" and c != 1)
    assert got == [5, 6]
    assert "2 موفق" in client.last(1)[1]
    row = bot.db._q("SELECT total, sent, failed FROM broadcasts").fetchone()
    assert tuple(row) == (2, 2, 0)


def test_broadcast_by_station_and_failure_count(env):
    bot, client, _ = env
    _sync(bot)
    onboard(bot, chat=5, station=3)
    onboard(bot, chat=6, station=1)
    onboard(bot, chat=404, station=3)  # کاربری که بازو را بسته
    bot.handle_update(cb(1, "adm:bc"))
    bot.handle_update(msg(1, "جلسه‌ی امشب ساعت ۲۱ است"))
    bot.handle_update(cb(1, "adm:bca:st"))
    bot.handle_update(cb(1, "adm:bcs:3"))
    bot.handle_update(cb(1, "adm:bcgo"))
    sent_to = [c for c, it in client.media if it["kind"] == "text" and c != 1]
    assert sent_to == [5]
    assert "1 موفق" in client.last(1)[1] and "1 ناموفق" in client.last(1)[1]


def test_broadcast_selected_users(env):
    bot, client, _ = env
    _sync(bot)
    onboard(bot, chat=5)
    onboard(bot, chat=6)
    bot.db.update_user(6, username="reza")
    bot.handle_update(cb(1, "adm:bc"))
    bot.handle_update(media_msg(1, document={"file_id": "pdf1", "file_name": "ws.pdf"}))
    bot.handle_update(cb(1, "adm:bca:sel"))
    bot.handle_update(msg(1, "@reza، 999999"))
    assert any("پیدا نشد: 999999" in s[1] for s in client.sent if s[0] == 1)
    bot.handle_update(cb(1, "adm:bcgo"))
    assert [c for c, it in client.media if it["file_id"] == "pdf1" and c != 1] == [6]


def test_non_admin_cannot_broadcast(env):
    bot, client, _ = env
    _sync(bot)
    onboard(bot, chat=5)
    bot.handle_update(cb(5, "adm:bc"))
    bot.handle_update(cb(5, "adm:bcgo"))
    assert client.media == []


def test_worksheet_with_file_and_daily_send(env):
    bot, client, _ = env
    onboard(bot, chat=1, station=5)
    bot.handle_update(cb(1, "adm:addw"))
    bot.handle_update(cb(1, "adm:ws:5"))
    bot.handle_update(msg(1, "کاربرگ جلسه‌ی پنجم"))
    bot.handle_update(media_msg(1, document={"file_id": "ws5pdf", "file_name": "s5.pdf"}, caption="فرم را پر کنید"))
    bot.handle_update(media_msg(1, **PHOTO))
    bot.handle_update(cb(1, "adm:wsave"))
    ws = bot.content.worksheets["s05-ws1"]
    assert ws.steps == [] and [a["file_id"] for a in ws.attachments] == ["ws5pdf", "big"]
    # کاربرِ ایستگاه ۵ هر روز خودکار همین کاربرگ و فایل‌هایش را می‌گیرد
    onboard(bot, chat=7, station=5)
    client.media.clear()
    n = bot.send_due_reminders(datetime(2026, 11, 12, 21, 0, tzinfo=bot.tz))
    assert n >= 1
    assert [it["file_id"] for c, it in client.media if c == 7] == ["ws5pdf", "big"]
    assert "done:s05-ws1" in str(client.last(7)[2])


def test_validate_worksheet_attachments_only():
    ok = {"id": "w", "station": "s1", "title": "t", "attachments": [{"kind": "photo", "file_id": "x"}]}
    assert validate_data(ok) == ("worksheet", [])
    _, errs = validate_data({"id": "w", "station": "s1", "title": "t", "steps": []}, "worksheet")
    assert errs
