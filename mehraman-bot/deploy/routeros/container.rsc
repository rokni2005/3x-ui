# ساخت/بازسازی کانتینر از images/mehraman-bot.tar (هم نصب اول، هم ارتقا).
# پوشه‌ی ماندگار /mehraman-state دست نمی‌خورد؛ فقط root-dir ایمیج نو می‌شود.
:if ([:len [/container find name=mehraman-bot]] > 0) do={
  /container stop [find name=mehraman-bot]
  :local i 0
  :while ((![/container get [find name=mehraman-bot] stopped]) && ($i < 30)) do={ :delay 1s; :set i ($i + 1) }
  /container remove [find name=mehraman-bot]
  :delay 3s
}
/container add name=mehraman-bot file=images/mehraman-bot.tar interface=veth-mehraman root-dir=/mehraman-rootdir mountlists=mehraman-state envlists=mehraman-env dns=172.31.0.1 logging=yes start-on-boot=yes restart-policy=on-failure comment="Mehraman Bale bot"
# RouterOS 7.24: ویژگی status ندارد؛ پس از استخراج ایمیج، stopped=true می‌شود
:local j 0
:delay 5s
:while ((![/container get [find name=mehraman-bot] stopped]) && ($j < 120)) do={ :delay 2s; :set j ($j + 1) }
/container start [find name=mehraman-bot]
