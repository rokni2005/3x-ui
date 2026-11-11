# بازوی بله‌ی مهرمان روی RouterOS (معادل docker-compose.yml) — یک‌بار اجرا می‌شود.
# مقصد فعلی: روتر میکروتیک ایران (CHR x86_64، RouterOS 7.24.x). این پروژه جزو VPN نیست؛ فقط روی همین روتر اجرا می‌شود.
# پیش‌نیاز: پکیج container، device-mode container=yes، بریج کانتینرها BR-Container (172.31.0.1/24) با NAT خروجی.
# اسرار (توکن بله، کلید مدل) اینجا نیستند: با secrets.rsc جدا وارد می‌شوند (deploy-routeros.sh آن را می‌سازد).

# شبکه: یک veth روی بریج کانتینرها
/interface veth add name=veth-mehraman address=172.31.0.23/24 gateway=172.31.0.1 comment="Mehraman Bale bot"
/interface bridge port add bridge=BR-Container interface=veth-mehraman comment="Mehraman Bale bot"

# پوشه‌ی ماندگار: data/ (SQLite) ، content/ ، knowledge/  ← /srv/mehraman داخل کانتینر
/container mounts add list=mehraman-state src=/mehraman-state dst=/srv/mehraman comment="Mehraman bot persistent state"

# تنظیمات غیرمحرمانه (معادل environment در compose)
/container envs add list=mehraman-env key=TIMEZONE value="Asia/Tehran"
/container envs add list=mehraman-env key=DAILY_QUESTION_LIMIT value="30"
/container envs add list=mehraman-env key=MIN_RETRIEVAL_SCORE value="1.2"

# مسیر: بله داخل ایران است و باید **مستقیم** برود (اگر از خارج برود پاسخ‌ها چند دقیقه دیر می‌رسد)؛
# API مدل (Gemini/Anthropic/OpenAI) آی‌پی ایران را رد می‌کند، پس بقیه‌ی ترافیک خارجیِ کانتینر از جدول EXT-DE
# (خروجی سرور خارج، با پشتیبان‌های خودکار) می‌رود.
# ⚠️ ترتیب مهم است: این دو قانون باید **بالاتر** از هر قانون عمومی‌ای باشند که ترافیک کانتینرها را جای دیگر
# می‌فرستد (روی روتر فعلی: قانون با comment «containers -> abroad via A»). اگر چنین قانونی نیست، place-before را بردارید.
/ip firewall address-list add list=Mehraman-Bale address=tapi.bale.ai comment="Mehraman bot: Bale API (direct)"
/ip firewall mangle add chain=prerouting action=accept src-address=172.31.0.23 dst-address-list=Mehraman-Bale comment="Mehraman bot: Bale direct (Iran)" place-before=[find comment~"containers -> abroad via A"]
/ip firewall mangle add chain=prerouting action=mark-routing new-routing-mark=EXT-DE passthrough=no src-address=172.31.0.23 dst-address-list=!IranSupernets comment="Mehraman bot: model API abroad via foreign exit" place-before=[find comment~"containers -> abroad via A"]

# خود کانتینر در container.rsc است (برای ارتقا جدا اجرا می‌شود)
