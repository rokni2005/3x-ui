# راهنمای کامل بازوی بله‌ی مهرمان: نصب، ساخت دوباره‌ی کانتینر، به‌روزرسانی

این سند برای صاحب بازو و هر هوش مصنوعی (مثل Gemini یا Claude) است که بعداً روی برنامه کار می‌کند.
**اگر هوش مصنوعی هستید:** بعد از هر تغییر در کد، مراحل بخش «۴. بعد از تغییر کد» را دقیق انجام دهید و در پایان «بسته‌ی کانتینر + دستور نصب» تحویل بدهید.

---

## ۱. محتوای بسته (زیپ)

| مسیر | چیست |
|---|---|
| `README-FIRST-fa.md` | همین راهنما |
| `source/mehraman-bot/` | سورس کامل برنامه (پایتون ۳.۱۲) |
| `container/mehraman-bot.tar` | بسته‌ی کانتینر آماده (ایمیج amd64) برای میکروتیک RouterOS یا Docker |
| `container/SHA256.txt` | چکسام بسته برای اطمینان از سالم بودن فایل |

داخل سورس:

| مسیر | نقش |
|---|---|
| `mehraman_bot/main.py` | شروع برنامه (`python -m mehraman_bot.main`، با `--check` فقط بررسی) |
| `mehraman_bot/bale.py` | ارتباط با API بله (`tapi.bale.ai`)، ارسال متن/عکس/فایل |
| `mehraman_bot/handlers.py` | منو، پروفایل، تمرین و کاربرگ، پرسش‌وپاسخ، ارجاع به مشاور، ارسال روزانه |
| `mehraman_bot/admin_panel.py` | پنل مدیریت دکمه‌ای: افزودن محتوا و کاربرگ، ارسال پیام به مخاطبان، فهرست کاربران |
| `mehraman_bot/knowledge.py` | جستجو در محتوای پشتیبان (BM25 فارسی) |
| `mehraman_bot/llm.py` / `persona.py` | ارتباط با مدل (Gemini/Anthropic/OpenAI) و قواعد لحن و ایمنی |
| `mehraman_bot/db.py` | پایگاه داده‌ی SQLite (کاربران، پیشرفت، ارجاع‌ها، ارسال‌های گروهی) |
| `mehraman_bot/content.py` | خواندن دوره‌ها، کاربرگ‌ها، پیام‌ها از `content/` |
| `mehraman_bot/scheduler.py` | ارسال خودکار روزانه‌ی کاربرگ |
| `content/` | متن پیام‌ها، دوره، کاربرگ‌ها، لحن (`persona.md`)، قواعد ایمنی |
| `knowledge/` | محتوای پشتیبان برای پاسخ به پرسش‌ها |
| `tests/` | ۲۹ تست خودکار |
| `deploy/routeros/` | ساخت و نصب روی میکروتیک (پایین توضیح داده شده) |
| `Dockerfile`, `docker-compose.yml`, `deploy/deploy-serverB.sh` | نصب روی سرور لینوکسی با Docker (جایگزین) |
| `docs/HANDOFF.md` | معماری و تصمیم‌های طراحی |
| `docs/UPDATING_WITH_AI.md` | به‌روزرسانی محتوا بدون برنامه‌نویسی |

---

## ۲. محل اجرای فعلی

- **سرور:** میکروتیک CHR (x86_64، RouterOS 7.24) در ایران. آدرس و دسترسی SSH سرور را صاحب سرور می‌دهد. در این راهنما به‌جای آن `<ROUTER>` و `<PORT>` نوشته شده.
- **نام کانتینر:** `mehraman-bot`. روی بریج کانتینرها با IP `172.31.0.23` است و درگاهش `172.31.0.1` است.
- **پوشه‌ی ماندگار روی روتر:** `/mehraman-state`. داخل کانتینر `/srv/mehraman` دیده می‌شود.
  - `data/mehraman.db`: پایگاه داده‌ی کاربران. **مهم‌ترین چیز برای بکاپ همین است.**
  - `content/` و `knowledge/`: محتوای فعلی بازو، همراه هر چه ادمین‌ها از پنل اضافه کرده‌اند.
- **تنظیمات و اسرار:** در `/container envs` روتر، لیست `mehraman-env`، ذخیره شده‌اند.
  - `BALE_BOT_TOKEN`, `LLM_PROVIDER=gemini`, `LLM_MODEL=gemini-3.1-flash-lite`, `LLM_API_KEY`
  - `ADMIN_IDS` (شناسه‌های عددی ادمین‌ها با کاما)
  - `REFERRAL_CHAT_ID` (گیرنده‌های فرم ارجاع با کاما؛ اولی مسئول اصلی است)
  - `TIMEZONE`, `DAILY_QUESTION_LIMIT`, `MIN_RETRIEVAL_SCORE`
- **مسیر اینترنت:** بله مستقیم وصل می‌شود. Gemini آی‌پی ایران را رد می‌کند، برای همین روی روتر یک قانون mangle ترافیک خارجی همین کانتینر را از سرور خارج (هلند) عبور می‌دهد. اگر بازو به سرور دیگری منتقل شود، باید دسترسی به Gemini از طریق یک آی‌پی غیرایرانی فراهم شود. راه دیگر: متغیر `LLM_PROXY` (پروکسی HTTP/SOCKS).

> ⚠️ توکن بله و کلید Gemini را هرگز در گیت، چت یا فایل‌های این بسته نگذارید. این بسته هیچ رمزی ندارد.

---

## ۳. پیش‌نیاز کامپیوتری که بسته را می‌سازد

- **پایتون ۳.۱۰ یا بالاتر** با `pip`. Docker لازم **نیست**.
- **اینترنت با دسترسی به Docker Hub و PyPI.** از ایران ممکن است VPN لازم باشد.
- برای نصب روی روتر: `ssh` و `scp` (در ویندوز Git Bash دارد) و کلید SSH روتر.

---

## ۴. بعد از تغییر کد: ساخت دوباره‌ی بسته‌ی کانتینر (برای هوش مصنوعی)

همه‌ی دستورها از داخل پوشه‌ی `source/mehraman-bot` اجرا می‌شوند.

**۱) تست‌ها باید همه سبز باشند:**
```bash
python -m pip install -r requirements-dev.txt
python -m pytest -q
```

**۲) ساخت بسته‌ی کانتینر.** خروجی یک فایل tar است، بدون نیاز به Docker:
```bash
python deploy/routeros/build_image_tar.py ../../container/mehraman-bot.tar
```
- برای روترهای ARM64 به آخر دستور `--arch arm64` اضافه کنید.
- اسکریپت `python:3.12-slim` را از Docker Hub می‌گیرد و چکسامش را بررسی می‌کند. کتابخانه‌های `requirements.txt` را برای لینوکس دانلود می‌کند. کد را در `/app` و نسخه‌ی اولیه‌ی `content/` و `knowledge/` را در `/app/_seed` می‌گذارد. نقطه‌ی شروع کانتینر `deploy/routeros/entrypoint.sh` است.
- فایل `.env` و پوشه‌ی `data/` وارد بسته **نمی‌شوند**.

**۳) چکسام:**
```bash
python -c "import hashlib;print(hashlib.sha256(open('../../container/mehraman-bot.tar','rb').read()).hexdigest())" > ../../container/SHA256.txt
```

**۴) اگر پیام یا کلید جدیدی به `content/messages.yaml` اضافه شده:** نسخه‌ی روی سرور خودکار عوض نمی‌شود، چون محتوا فقط بار اول کپی می‌شود. فایل جدید را هم به سرور بفرستید (دستور در بخش ۵-ب). در کد هم برای هر پیام جدید مقدار پیش‌فرض بگذارید: `self.content.msg("key", "متن پیش‌فرض")`.

**۵) تحویل:**
- پوشه‌ی سورس به‌روزشده و `container/mehraman-bot.tar` با `SHA256.txt`.
- دستور نصب دوباره (بخش ۵) را هم بدهید.
- همه را زیپ کنید.
- در `docs/HANDOFF.md` بنویسید چه چیزی تغییر کرد.

---

## ۵. نصب دوباره / به‌روزرسانی روی میکروتیک

### ۵-الف) خودکار، با اسکریپت (پیشنهادی)

از داخل `source/mehraman-bot`، در Git Bash یا لینوکس:
```bash
# فقط ارتقای برنامه (دیتابیس، محتوا و تنظیمات می‌مانند):
ROUTER=<user>@<ROUTER> PORT=<PORT> KEY=~/.ssh/<router-key> bash deploy/routeros/deploy-routeros.sh update

# تعویض توکن یا کلید یا شناسه‌ها (می‌پرسد، چیزی نشان نمی‌دهد):
ROUTER=<user>@<ROUTER> PORT=<PORT> KEY=~/.ssh/<router-key> bash deploy/routeros/deploy-routeros.sh secrets

# نصب از صفر روی روتری که هنوز بازو ندارد:
ROUTER=<user>@<ROUTER> PORT=<PORT> KEY=~/.ssh/<router-key> bash deploy/routeros/deploy-routeros.sh install

# دیدن لاگ‌های اخیر بازو:
ROUTER=<user>@<ROUTER> PORT=<PORT> KEY=~/.ssh/<router-key> bash deploy/routeros/deploy-routeros.sh check
```
- `update` خودش بسته را می‌سازد، آپلود می‌کند و کانتینر را از نو می‌سازد.
- اگر بسته را قبلاً ساخته‌اید، بخش ۵-ب را انجام دهید.

### ۵-ب) دستی، با بسته‌ی آماده‌ی همین زیپ

```bash
# ۱) آپلود بسته به روتر
scp -P <PORT> -i ~/.ssh/<router-key> container/mehraman-bot.tar <user>@<ROUTER>:images/mehraman-bot.tar
# (اختیاری) اگر messages.yaml تغییر کرده:
scp -P <PORT> -i ~/.ssh/<router-key> source/mehraman-bot/content/messages.yaml <user>@<ROUTER>:mehraman-state/content/messages.yaml
# ۲) ساخت دوباره‌ی کانتینر (دیتابیس و تنظیمات می‌مانند)
scp -P <PORT> -i ~/.ssh/<router-key> source/mehraman-bot/deploy/routeros/container.rsc <user>@<ROUTER>:mehraman-container.rsc
ssh -p <PORT> -i ~/.ssh/<router-key> <user>@<ROUTER> '/import file-name=mehraman-container.rsc; /file remove [find name="mehraman-container.rsc"]'
```

همان دستورها، اگر در ترمینال میکروتیک (WinBox ← New Terminal) وارد می‌کنید، بعد از آپلود فایل در Files:
```
/container stop [find name=mehraman-bot]
/container remove [find name=mehraman-bot]
/container add name=mehraman-bot file=images/mehraman-bot.tar interface=veth-mehraman root-dir=/mehraman-rootdir mountlists=mehraman-state envlists=mehraman-env dns=172.31.0.1 logging=yes start-on-boot=yes restart-policy=on-failure
# صبر کنید تا استخراج تمام شود (در /container print وضعیت S شود)، بعد:
/container start [find name=mehraman-bot]
```

**بررسی سلامت:**
```
/container print where name=mehraman-bot
/log print where message~"mehraman-bot"
```
- در لاگ باید عبارت `bot started` دیده شود.
- خطای «… تنظیم نشده است» یعنی یکی از envها خالی است.

### ۵-ج) نصب از صفر روی یک میکروتیک دیگر

1. پکیج container نصب و `/system/device-mode/update container=yes` فعال باشد. این مرحله روشن و خاموش کردن فیزیکی/کنسول روتر را لازم دارد.
2. یک بریج برای کانتینرها با NAT خروجی بسازید. در فایل `deploy/routeros/mehraman.rsc` بریج `BR-Container` با شبکه‌ی `172.31.0.0/24` فرض شده. اگر روتر دیگری است، IP و نام بریج را در این فایل و در `container.rsc` تطبیق دهید.
3. قانون mangle آخر `mehraman.rsc` برای عبور Gemini از سرور خارج است و جدول مسیر `EXT-DE` را لازم دارد. روی روتر دیگر یا مسیر خارجی خودتان را بگذارید، یا این خط را حذف کنید و `LLM_PROXY` تنظیم کنید.
4. سپس: `deploy-routeros.sh install`.

### ۵-د) جایگزین: سرور لینوکسی با Docker
```bash
docker load -i container/mehraman-bot.tar          # ایمیج: mehraman-bot:latest
docker run -d --name mehraman-bot --restart unless-stopped \
  -v /opt/mehraman-state:/srv/mehraman \
  -e BALE_BOT_TOKEN=... -e LLM_PROVIDER=gemini -e LLM_MODEL=gemini-3.1-flash-lite -e LLM_API_KEY=... \
  -e ADMIN_IDS=... -e REFERRAL_CHAT_ID=... mehraman-bot:latest
```
یا از سورس: `docker compose up -d --build`. فایل `.env` از روی `.env.example` ساخته می‌شود.

---

## ۶. بکاپ و بازگردانی

- **بکاپ:** فایل `/mehraman-state/data/mehraman.db` و پوشه‌های `content` و `knowledge` را از Files روتر (WinBox) یا با sftp دانلود کنید.
- **بازگردانی:** همان‌ها را در همان مسیر بگذارید و کانتینر را restart کنید.
- **حذف کامل بازو از روتر:**
  ```
  /container stop [find name=mehraman-bot]
  /container remove [find name=mehraman-bot]
  /ip firewall mangle remove [find comment~"Mehraman"]
  /interface bridge port remove [find interface=veth-mehraman]
  /interface veth remove veth-mehraman
  /container envs remove [find list=mehraman-env]
  /container mounts remove [find list=mehraman-state]
  ```
  پوشه‌ی `/mehraman-state` فقط بعد از بکاپ پاک شود.

---

## ۷. کار با بازو (برای ادمین‌ها، بدون کد)

در بله: «🛠 پنل مدیریت».
- **📤 ارسال پیام به مخاطبان:**
  - متن، عکس، فایل، ویدیو یا صوت بفرستید.
  - گیرنده را انتخاب کنید: همه، یک ایستگاه یا جلسه، یا کاربران انتخابی با `@نام‌کاربری` یا شناسه.
  - «✅ ارسال» را بزنید. در پایان گزارش موفق و ناموفق می‌آید.
- **➕ افزودن کاربرگ:** ایستگاه ← عنوان ← گام‌های متنی و/یا فایل و عکس ← «✅ ذخیره».
  - هر کاربر هر روز در ساعت انتخابی‌اش، کاربرگ بعدی جلسه‌ی خودش را خودکار می‌گیرد.
- **➕ افزودن محتوای پشتیبان:** متن یا فایل Word، md یا txt. پاسخ پرسش‌ها فقط از روی همین محتواست.
- **👥 فهرست کاربران**، **📊 گزارش هفته/ماه**، **📚 منابع / حذف**.
- جزئیات بیشتر در `docs/UPDATING_WITH_AI.md`.

---

## ۸. نکته‌های شناخته‌شده

- **کندی شدید پاسخ (چند دقیقه):** تقریباً همیشه یعنی ترافیک بله از خارج رد می‌شود. بله باید مستقیم از اینترنت ایران برود.
  - روی روتر بررسی کنید که دو قانون `Mehraman bot: Bale direct` و `Mehraman bot: model API` در `/ip firewall mangle` **بالاتر** از قوانین عمومی کانتینرها باشند.
  - شمارنده‌ی قانون Bale direct باید بالا برود. ببینید: `/ip firewall mangle print stats where comment~"Mehraman"`.
  - (این مشکل ۲۰۲۶-۱۱-۱۱ رخ داد و رفع شد.)
- **یکی‌یکی جواب دادن:** بازو پیام‌ها را یکی‌یکی جواب می‌دهد. اگر Gemini کند باشد یا جواب ندهد، هر پرسش تا حدود ۳ دقیقه منتظر می‌ماند و پیام‌های بعدی پشت آن صف می‌شوند. اول مسیر Gemini را بررسی کنید.

- **Gemini:**
  - کلید Gemini فقط از آی‌پی غیرایرانی کار می‌کند؛ از ایران خطای 403 می‌دهد.
  - مدل فعلی `gemini-3.1-flash-lite` است: سریع (حدود ۱ ثانیه) و در سهمیه‌ی رایگان. مدل‌های «فکرکننده» مثل `gemini-3.8-flash` در سهمیه‌ی رایگان این کلید نیستند و خطای 429 می‌دهند. اگر بازو مدام «الان نتوانستم پاسخ بدهم» گفت، اول سهمیه و مدل را در aistudio.google.com بررسی کنید. اگر گوگل مدل را عوض کرد، فقط `LLM_MODEL` را در envها عوض کنید.
- **اگر Gemini در دسترس نباشد:**
  - منو، کاربرگ‌ها، ارسال روزانه، ارسال پیام ادمین و فرم ارجاع کار می‌کنند.
  - فقط پاسخ پرسش‌های آزاد پیام «الان نتوانستم پاسخ بدهم» می‌دهد.
- **فایل‌ها و توکن:** فایل‌ها و عکس‌های کاربرگ با شناسه‌ی فایل همین بازو ذخیره می‌شوند. اگر بازوی جدیدی با توکن دیگر ساخته شود، باید دوباره فرستاده شوند.
- **رفتن به جلسه‌ی بعد:** فعلاً با خود کاربر است، با دکمه‌ی «➡️ ایستگاه بعد». پیشروی خودکار بر اساس تاریخ پیاده نشده.
- **کارهای باز:**
  - اضافه کردن محتوای پشتیبان واقعی.
  - تطبیق شماره‌های اضطراری در `content/safety.yaml`.
