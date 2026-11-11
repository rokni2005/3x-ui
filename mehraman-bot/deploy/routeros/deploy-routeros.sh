#!/usr/bin/env bash
# استقرار بازوی مهرمان روی میکروتیک (معادل deploy-serverB.sh برای RouterOS).
# روی کامپیوتر ادمین اجرا می‌شود (Git Bash/لینوکس) با دسترسی SSH کلیدی به روتر:
#   ROUTER=<user>@<ROUTER> PORT=<PORT> KEY=~/.ssh/<key> bash deploy/routeros/deploy-routeros.sh install
#   ... deploy-routeros.sh update     # فقط ایمیج نو (محتوا و دیتابیس می‌مانند)
#   ... deploy-routeros.sh secrets    # فقط تنظیم/تعویض توکن و کلید
#   ... deploy-routeros.sh check      # نمایش لاگ‌های اخیر بازو از لاگ روتر
# اسرار فقط در /container envs روتر (لیست mehraman-env) ذخیره می‌شوند؛ در گیت/لاگ نیستند.
set -euo pipefail
cd "$(dirname "$0")/../.."
: "${ROUTER:?ROUTER=user@host}" "${PORT:=22}" "${KEY:?KEY=path to ssh key}"
SSH=(ssh -i "$KEY" -p "$PORT" -o BatchMode=yes "$ROUTER")
SCP=(scp -i "$KEY" -P "$PORT" -o BatchMode=yes)
MODE="${1:-update}"

push_image() {
  python deploy/routeros/build_image_tar.py mehraman-bot.tar
  "${SCP[@]}" mehraman-bot.tar "$ROUTER:images/mehraman-bot.tar"
}
run_rsc() {  # $1 = local .rsc
  local n; n="mehraman-$(basename "$1")"
  "${SCP[@]}" "$1" "$ROUTER:$n"
  "${SSH[@]}" "/import file-name=$n verbose=no; /file remove [find name=\"$n\"]"
}
rsc_quote() { printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' -e 's/\$/\\$/g'; }

secrets() {
  local T P M K A R tmp
  read -rsp "توکن بازوی بله: " T; echo
  read -rp  "ارائه‌دهنده مدل (anthropic/gemini/openai) [gemini]: " P; P="${P:-gemini}"
  read -rp  "نام مدل [gemini-2.5-flash]: " M; M="${M:-gemini-2.5-flash}"
  read -rsp "کلید API مدل: " K; echo
  read -rp  "شناسه عددی ادمین(ها)، با کاما: " A
  read -rp  "شناسه عددی مسئول ارجاع به مشاور: " R
  tmp="$(mktemp)"; trap 'rm -f "$tmp"' RETURN
  {
    echo '/container envs remove [find list=mehraman-env and (key="BALE_BOT_TOKEN" or key="LLM_PROVIDER" or key="LLM_MODEL" or key="LLM_API_KEY" or key="ADMIN_IDS" or key="REFERRAL_CHAT_ID")]'
    for kv in "BALE_BOT_TOKEN=$T" "LLM_PROVIDER=$P" "LLM_MODEL=$M" "LLM_API_KEY=$K" "ADMIN_IDS=$A" "REFERRAL_CHAT_ID=$R"; do
      echo "/container envs add list=mehraman-env key=${kv%%=*} value=\"$(rsc_quote "${kv#*=}")\""
    done
  } > "$tmp"
  run_rsc "$tmp"
}

case "$MODE" in
  install) run_rsc deploy/routeros/mehraman.rsc; secrets; push_image; run_rsc deploy/routeros/container.rsc ;;
  update)  push_image; run_rsc deploy/routeros/container.rsc ;;
  secrets) secrets; "${SSH[@]}" '/container stop [find name=mehraman-bot]; :delay 8s; /container start [find name=mehraman-bot]' ;;
  check)   "${SSH[@]}" '/log print without-paging where message~"mehraman|محتوا|✅|❌|⚠️"' | tail -30 ;;
  *) echo "usage: $0 install|update|secrets|check"; exit 1 ;;
esac
echo "✅ $MODE done. لاگ: ${SSH[*]} '/log print where topics~\"container\"'"
