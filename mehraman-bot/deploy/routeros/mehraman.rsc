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

# مسیر: بله داخل ایران است و مستقیم می‌رود؛ API مدل (Gemini/Anthropic/OpenAI) آی‌پی ایران را رد می‌کند،
# پس ترافیک خارجیِ کانتینر از جدول EXT-DE (خروجی سرور خارج (هلند) از تونل، با پشتیبان‌های خودکار) می‌رود.
/ip firewall mangle add chain=prerouting action=mark-routing new-routing-mark=EXT-DE passthrough=no src-address=172.31.0.23 dst-address-list=!IranSupernets comment="Mehraman bot: model API abroad via foreign exit"

# خود کانتینر در container.rsc است (برای ارتقا جدا اجرا می‌شود)
