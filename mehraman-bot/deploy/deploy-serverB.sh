#!/usr/bin/env bash
# اجرا روی «سرور B»:   bash deploy-serverB.sh
# توکن‌ها فقط روی همان سرور و در فایل .env با دسترسی ۶۰۰ ذخیره می‌شوند.
set -euo pipefail

REPO_URL="${REPO_URL:-https://github.com/rokni2005/3x-ui}"
BRANCH="${BRANCH:-ccr-49cdcaf2-vrb055}"
DIR="${DIR:-/opt/mehraman}"

command -v docker >/dev/null || { echo "❌ Docker نصب نیست."; exit 1; }
docker compose version >/dev/null 2>&1 || { echo "❌ docker compose نصب نیست."; exit 1; }

if [ -d "$DIR/.git" ]; then
  git -C "$DIR" fetch origin "$BRANCH" && git -C "$DIR" checkout "$BRANCH" && git -C "$DIR" pull --ff-only origin "$BRANCH"
else
  git clone --branch "$BRANCH" --depth 1 "$REPO_URL" "$DIR"
fi
cd "$DIR/mehraman-bot"

if [ ! -f .env ]; then
  cp .env.example .env
  read -rsp "توکن بازوی بله: " T; echo
  read -rp  "ارائه‌دهنده مدل (anthropic/gemini/openai) [gemini]: " P; P="${P:-gemini}"
  read -rp  "نام مدل [gemini-3.1-flash-lite]: " M; M="${M:-gemini-3.1-flash-lite}"
  read -rsp "کلید API مدل: " K; echo
  read -rp  "شناسه عددی ادمین(ها)، با کاما: " A
  read -rp  "شناسه عددی مسئول ارجاع به مشاور: " R
  read -rp  "آدرس پروکسی مدل (اختیاری): " X
  python3 - "$T" "$P" "$M" "$K" "$A" "$R" "$X" <<'PY'
import sys, re
t,p,m,k,a,r,x = sys.argv[1:8]
s = open(".env", encoding="utf-8").read()
for key, val in [("BALE_BOT_TOKEN",t),("LLM_PROVIDER",p),("LLM_MODEL",m),("LLM_API_KEY",k),
                 ("ADMIN_IDS",a),("REFERRAL_CHAT_ID",r),("LLM_PROXY",x)]:
    s = re.sub(rf"^{key}=.*$", lambda _m, key=key, val=val: f"{key}={val}", s, flags=re.M)
open(".env", "w", encoding="utf-8").write(s)
PY
  chmod 600 .env
fi

mkdir -p data knowledge && chmod 777 data knowledge content 2>/dev/null || true
docker compose up -d --build
docker compose run --rm mehraman-bot python -m mehraman_bot.main --check || echo "⚠️ --check خطا داد؛ لاگ: docker compose logs -f"
echo "✅ بازو اجرا شد. لاگ: docker compose -f $DIR/mehraman-bot/docker-compose.yml logs -f"
