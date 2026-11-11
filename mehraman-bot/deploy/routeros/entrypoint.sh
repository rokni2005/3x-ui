#!/bin/sh
# نقطه‌ی ورود کانتینر روی RouterOS.
# RouterOS فقط «پوشه» mount می‌کند و پوشه‌ی خالی را با محتوای ایمیج پر نمی‌کند؛
# پس یک پوشه‌ی ماندگار ($STATE_DIR) mount می‌شود و این اسکریپت بار اول
# content/ و knowledge/ اولیه را از /app/_seed در آن کپی می‌کند. بعد از آن
# تغییرات ادمین (پنل دکمه‌ای) در همین پوشه می‌ماند و با ارتقای ایمیج پاک نمی‌شود.
set -e
S="${STATE_DIR:-/srv/mehraman}"
mkdir -p "$S/data" "$S/content" "$S/knowledge"
[ -n "$(ls -A "$S/content" 2>/dev/null)" ]   || cp -a /app/_seed/content/.   "$S/content/"
[ -n "$(ls -A "$S/knowledge" 2>/dev/null)" ] || cp -a /app/_seed/knowledge/. "$S/knowledge/"
export DATA_DIR="${DATA_DIR:-$S/data}" CONTENT_DIR="${CONTENT_DIR:-$S/content}" KNOWLEDGE_DIR="${KNOWLEDGE_DIR:-$S/knowledge}"
cd /app
exec python -m mehraman_bot.main "$@"
