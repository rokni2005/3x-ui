// Package admin serves a small authenticated web panel for editing each
// fruit's price and minimum order weight, and for reviewing recent orders.
package admin

import (
	"crypto/subtle"
	"html/template"
	"log"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"balebot/bot"
	"balebot/db"
)

// Server is the admin HTTP handler. It requires HTTP Basic Auth.
type Server struct {
	data     *db.Store
	bot      *bot.Bot
	username string
	password string
}

// New builds an admin server. username/password must both be non-empty;
// the caller decides whether to start the server at all. bot is used to
// message customers when the admin confirms or ships an order; it may be
// nil in tests that don't exercise those actions.
func New(data *db.Store, b *bot.Bot, username, password string) *Server {
	return &Server{data: data, bot: b, username: username, password: password}
}

// Handler returns the configured http.Handler, ready to be served.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.auth(s.handleRoot))
	mux.HandleFunc("/fruits", s.auth(s.handleFruits))
	mux.HandleFunc("/fruits/update", s.auth(s.handleUpdateFruit))
	mux.HandleFunc("/fruits/add", s.auth(s.handleAddFruit))
	mux.HandleFunc("/fruits/delete", s.auth(s.handleDeleteFruit))
	mux.HandleFunc("/photo", s.auth(s.handlePhoto))
	mux.HandleFunc("/orders", s.auth(s.handleOrders))
	mux.HandleFunc("/orders/confirm", s.auth(s.handleConfirmOrder))
	mux.HandleFunc("/orders/ship", s.auth(s.handleShipOrder))
	mux.HandleFunc("/stats", s.auth(s.handleStats))
	mux.HandleFunc("/wallets", s.auth(s.handleWallets))
	mux.HandleFunc("/wallets/update", s.auth(s.handleUpdateWallet))
	mux.HandleFunc("/wallets/zero", s.auth(s.handleZeroWallet))
	return mux
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		validUser := subtle.ConstantTimeCompare([]byte(user), []byte(s.username)) == 1
		validPass := subtle.ConstantTimeCompare([]byte(pass), []byte(s.password)) == 1
		if !ok || !validUser || !validPass {
			w.Header().Set("WWW-Authenticate", `Basic realm="fruit bot admin"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/fruits", http.StatusFound)
}

var funcMap = template.FuncMap{
	"toman":  bot.FormatToman,
	"weight": bot.FormatWeight,
}

var fruitsTemplate = template.Must(template.New("fruits").Funcs(funcMap).Parse(`
<!doctype html>
<html lang="fa" dir="rtl">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>مدیریت میوه‌ها</title>
<style>
	body { font-family: Tahoma, sans-serif; background:#f6f7f4; margin:0; padding:24px; color:#222; }
	h1 { font-size:20px; }
	nav a { margin-inline-end:16px; color:#2e7d32; text-decoration:none; font-weight:bold; }
	.msg { padding:10px 14px; border-radius:8px; margin-bottom:16px; }
	.msg.ok { background:#e6f4ea; color:#1e7e34; }
	.msg.err { background:#fdecea; color:#b3261e; }
	.header-row, .fruit-row {
		display:grid;
		grid-template-columns: 56px 1.6fr 1fr 1fr 1.4fr 130px;
		gap:12px;
		align-items:center;
		padding:10px 12px;
		border-bottom:1px solid #e0e0e0;
	}
	.header-row { font-weight:bold; color:#555; }
	.fruit-row { background:#fff; }
	.fruit-row input {
		width:100%; box-sizing:border-box; padding:6px 8px; border:1px solid #ccc; border-radius:6px;
	}
	.fruit-row input[type=file] { padding:2px 0; border:none; font-size:12px; }
	.fruit-row button {
		padding:7px 10px; border:none; border-radius:6px; background:#2e7d32; color:#fff; cursor:pointer;
	}
	.thumb {
		width:48px; height:48px; border-radius:8px; object-fit:cover; background:#f0f0f0; display:block;
	}
	.thumb.empty {
		display:flex; align-items:center; justify-content:center; font-size:20px; color:#bbb;
	}
	.list { border:1px solid #e0e0e0; border-radius:10px; overflow:hidden; }
	.fruit-row .del { background:#b3261e; margin-inline-start:6px; }
	.add-box {
		background:#fff; border:1px solid #e0e0e0; border-radius:10px; padding:16px;
		margin-bottom:20px;
	}
	.add-box h2 { font-size:15px; margin:0 0 12px; }
	.add-grid { display:grid; grid-template-columns: 90px 70px 1.4fr 1fr 1fr auto; gap:10px; align-items:end; }
	.add-grid label { display:block; font-size:12px; color:#666; margin-bottom:4px; }
	.add-grid input { width:100%; box-sizing:border-box; padding:7px 8px; border:1px solid #ccc; border-radius:6px; }
	.add-grid button { padding:8px 14px; border:none; border-radius:6px; background:#2e7d32; color:#fff; cursor:pointer; }
</style>
</head>
<body>
	<nav><a href="/fruits">میوه‌ها</a><a href="/orders">سفارش‌ها</a><a href="/stats">آمار</a><a href="/wallets">کیف‌پول</a></nav>
	<h1>🍉 مدیریت قیمت، حداقل وزن و عکس میوه‌ها</h1>
	{{if .Message}}<div class="msg {{.MessageClass}}">{{.Message}}</div>{{end}}

	<div class="add-box">
		<h2>➕ افزودن میوه جدید</h2>
		<form class="add-grid" method="post" action="/fruits/add">
			<div><label>شناسه (لاتین)</label><input type="text" name="id" placeholder="e.g. lemon" pattern="[a-z0-9_]+" required></div>
			<div><label>اموجی</label><input type="text" name="emoji" placeholder="🍋" required></div>
			<div><label>نام</label><input type="text" name="name" placeholder="لیمو" required></div>
			<div><label>قیمت (تومان/کیلو)</label><input type="number" name="price" min="0" step="1000" required></div>
			<div><label>حداقل سفارش (کیلوگرم)</label><input type="number" name="min_weight" min="0.1" step="0.1" value="0.5" required></div>
			<div><button type="submit">افزودن</button></div>
		</form>
	</div>

	<div class="list">
		<div class="header-row">
			<div></div><div>میوه</div><div>قیمت (تومان/کیلو)</div><div>حداقل سفارش (کیلوگرم)</div><div>عکس</div><div></div>
		</div>
		{{range .Fruits}}
		<form class="fruit-row" method="post" action="/fruits/update" enctype="multipart/form-data">
			{{if .PhotoPath}}
				<img class="thumb" src="/photo?id={{.ID}}" alt="{{.Name}}">
			{{else}}
				<div class="thumb empty">{{.Emoji}}</div>
			{{end}}
			<div>{{.Emoji}} {{.Name}}</div>
			<input type="hidden" name="id" value="{{.ID}}">
			<div><input type="number" name="price" value="{{.Price}}" min="0" step="1000" required></div>
			<div><input type="number" name="min_weight" value="{{weight .MinWeightKg}}" min="0.1" step="0.1" required></div>
			<div><input type="file" name="photo" accept="image/jpeg,image/png,image/webp"></div>
			<div style="display:flex; gap:6px;">
				<button type="submit">ذخیره</button>
				<button class="del" type="submit" formaction="/fruits/delete" formnovalidate
					onclick="return confirm('میوه «{{.Name}}» حذف بشه؟');">🗑</button>
			</div>
		</form>
		{{end}}
	</div>
</body>
</html>
`))

type fruitsPageData struct {
	Fruits       []db.Fruit
	Message      string
	MessageClass string
}

func (s *Server) handleFruits(w http.ResponseWriter, r *http.Request) {
	fruits, err := s.data.ListFruits()
	if err != nil {
		http.Error(w, "خطا در خواندن لیست میوه‌ها", http.StatusInternalServerError)
		log.Printf("admin: ListFruits: %v", err)
		return
	}

	data := fruitsPageData{Fruits: fruits}
	switch r.URL.Query().Get("status") {
	case "ok":
		data.Message = "✅ ذخیره شد."
		data.MessageClass = "ok"
	case "err":
		data.Message = "❌ مقدار وارد شده نامعتبر است."
		data.MessageClass = "err"
	case "photo_err":
		data.Message = "❌ قیمت و حداقل وزن ذخیره شد، اما آپلود عکس با خطا مواجه شد (فرمت باید jpg، png یا webp باشد)."
		data.MessageClass = "err"
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := fruitsTemplate.Execute(w, data); err != nil {
		log.Printf("admin: render fruits: %v", err)
	}
}

const maxPhotoUploadBytes = 10 << 20 // 10 MiB

func (s *Server) handleUpdateFruit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(maxPhotoUploadBytes); err != nil {
		http.Redirect(w, r, "/fruits?status=err", http.StatusFound)
		return
	}

	id := strings.TrimSpace(r.FormValue("id"))
	price, priceErr := strconv.Atoi(strings.TrimSpace(r.FormValue("price")))
	minWeight, weightErr := strconv.ParseFloat(strings.TrimSpace(r.FormValue("min_weight")), 64)

	if id == "" || priceErr != nil || weightErr != nil || price < 0 || minWeight <= 0 || minWeight > 50 {
		http.Redirect(w, r, "/fruits?status=err", http.StatusFound)
		return
	}

	if err := s.data.UpdateFruit(id, price, minWeight); err != nil {
		log.Printf("admin: UpdateFruit(%s): %v", id, err)
		http.Redirect(w, r, "/fruits?status=err", http.StatusFound)
		return
	}

	if err := s.maybeSavePhoto(r, id); err != nil {
		log.Printf("admin: save photo for %s: %v", id, err)
		http.Redirect(w, r, "/fruits?status=photo_err", http.StatusFound)
		return
	}

	http.Redirect(w, r, "/fruits?status=ok", http.StatusFound)
}

var fruitIDPattern = regexp.MustCompile(`^[a-z0-9_]+$`)

func (s *Server) handleAddFruit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/fruits?status=err", http.StatusFound)
		return
	}

	id := strings.TrimSpace(r.FormValue("id"))
	emoji := strings.TrimSpace(r.FormValue("emoji"))
	name := strings.TrimSpace(r.FormValue("name"))
	price, priceErr := strconv.Atoi(strings.TrimSpace(r.FormValue("price")))
	minWeight, weightErr := strconv.ParseFloat(strings.TrimSpace(r.FormValue("min_weight")), 64)

	if !fruitIDPattern.MatchString(id) || emoji == "" || name == "" ||
		priceErr != nil || weightErr != nil || price < 0 || minWeight <= 0 || minWeight > 50 {
		http.Redirect(w, r, "/fruits?status=err", http.StatusFound)
		return
	}

	if existing, err := s.data.GetFruit(id); err != nil {
		log.Printf("admin: GetFruit(%s) before add: %v", id, err)
		http.Redirect(w, r, "/fruits?status=err", http.StatusFound)
		return
	} else if existing != nil {
		http.Redirect(w, r, "/fruits?status=err", http.StatusFound)
		return
	}

	if err := s.data.AddFruit(id, emoji, name, price, minWeight); err != nil {
		log.Printf("admin: AddFruit(%s): %v", id, err)
		http.Redirect(w, r, "/fruits?status=err", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/fruits?status=ok", http.StatusFound)
}

func (s *Server) handleDeleteFruit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/fruits?status=err", http.StatusFound)
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	if id == "" {
		http.Redirect(w, r, "/fruits?status=err", http.StatusFound)
		return
	}
	if err := s.data.DeleteFruit(id); err != nil {
		log.Printf("admin: DeleteFruit(%s): %v", id, err)
		http.Redirect(w, r, "/fruits?status=err", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/fruits?status=ok", http.StatusFound)
}

// maybeSavePhoto stores an uploaded "photo" file, if one was submitted.
// A missing file is not an error: the photo field is optional on every save.
func (s *Server) maybeSavePhoto(r *http.Request, fruitID string) error {
	file, header, err := r.FormFile("photo")
	if err == http.ErrMissingFile {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext == ".jpeg" {
		ext = ".jpg"
	}

	filename, err := s.data.SavePhoto(fruitID, ext, file)
	if err != nil {
		return err
	}
	return s.data.UpdateFruitPhoto(fruitID, filename)
}

func (s *Server) handlePhoto(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	fruit, err := s.data.GetFruit(id)
	if err != nil {
		log.Printf("admin: GetFruit(%s): %v", id, err)
		http.Error(w, "خطا", http.StatusInternalServerError)
		return
	}
	if fruit == nil || fruit.PhotoPath == "" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, s.data.PhotoFullPath(fruit.PhotoPath))
}

var ordersTemplate = template.Must(template.New("orders").Funcs(funcMap).Parse(`
<!doctype html>
<html lang="fa" dir="rtl">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>سفارش‌های اخیر</title>
<style>
	body { font-family: Tahoma, sans-serif; background:#f6f7f4; margin:0; padding:24px; color:#222; }
	h1 { font-size:20px; }
	nav a { margin-inline-end:16px; color:#2e7d32; text-decoration:none; font-weight:bold; }
	.empty { padding:16px; color:#777; }
	.range-tabs { display:flex; gap:8px; margin-bottom:18px; flex-wrap:wrap; }
	.range-tabs a {
		padding:7px 14px; border-radius:20px; background:#fff; border:1px solid #ddd;
		color:#444; text-decoration:none; font-size:13px;
	}
	.range-tabs a.active { background:#2e7d32; border-color:#2e7d32; color:#fff; font-weight:bold; }
	.cards { display:flex; flex-direction:column; gap:14px; }
	.card {
		background:#fff; border-radius:12px; box-shadow:0 1px 2px rgba(0,0,0,.06);
		overflow:hidden; display:flex; flex-direction:column;
	}
	.card .head {
		display:flex; justify-content:space-between; align-items:center;
		padding:12px 14px; border-bottom:1px solid #f0f0f0;
	}
	.card .head .id { font-weight:bold; }
	.card .head .time { font-size:12px; color:#888; }
	.stepper { display:flex; align-items:center; padding:10px 14px; gap:4px; font-size:12px; }
	.stepper .step { padding:3px 8px; border-radius:12px; background:#eee; color:#888; white-space:nowrap; }
	.stepper .step.done { background:#e6f4ea; color:#1e7e34; }
	.stepper .step.current { background:#2e7d32; color:#fff; font-weight:bold; }
	.stepper .sep { flex:1; height:2px; background:#eee; margin:0 2px; min-width:8px; }
	.body { padding:0 14px 12px; font-size:13px; }
	.body .items { color:#333; margin-bottom:6px; }
	.body .totals { display:flex; gap:16px; color:#444; margin-bottom:6px; }
	.body .meta { color:#666; }
	.loc { padding:0 14px 12px; font-size:12px; }
	.loc a { color:#0b5ed7; margin-inline-end:14px; }
	.loc .snapp { color:#888; }
	.loc .none { color:#999; }
	.card-actions { margin-top:auto; padding:12px 14px; background:#fafafa; border-top:1px solid #f0f0f0; }
	.card-actions form { margin:0; }
	.card-actions button {
		width:100%; padding:12px; border:none; border-radius:8px; background:#2e7d32; color:#fff;
		font-size:14px; font-weight:bold; cursor:pointer;
	}
	.card-actions button.ship { background:#0b5ed7; }
	.card-actions .done-note { color:#888; font-size:12px; text-align:center; }
</style>
</head>
<body>
	<nav><a href="/fruits">میوه‌ها</a><a href="/orders">سفارش‌ها</a><a href="/stats">آمار</a><a href="/wallets">کیف‌پول</a></nav>
	<h1>📦 سفارش‌ها</h1>
	<div class="range-tabs">
		<a href="/orders?range=today" class="{{if eq .Range "today"}}active{{end}}">امروز</a>
		<a href="/orders?range=week" class="{{if eq .Range "week"}}active{{end}}">این هفته</a>
		<a href="/orders?range=month" class="{{if eq .Range "month"}}active{{end}}">این ماه</a>
		<a href="/orders?range=all" class="{{if eq .Range "all"}}active{{end}}">همه</a>
	</div>
	{{if not .Orders}}
		<div class="empty">سفارشی در این بازه ثبت نشده است.</div>
	{{else}}
	<div class="cards">
		{{range .Orders}}
		<div class="card">
			<div class="head">
				<span class="id">سفارش #{{.ID}}{{if .CustomerName}} — {{.CustomerName}}{{end}}</span>
				<span class="time">{{.CreatedAt.Format "2006-01-02 15:04"}}</span>
			</div>
			<div class="stepper">
				<span class="step {{if or (eq .Status "confirmed") (eq .Status "shipped")}}done{{else}}current{{end}}">⏳ در انتظار تایید</span>
				<span class="sep"></span>
				<span class="step {{if eq .Status "shipped"}}done{{else if eq .Status "confirmed"}}current{{end}}">✅ تایید شده</span>
				<span class="sep"></span>
				<span class="step {{if eq .Status "shipped"}}current{{end}}">🚚 ارسال شده</span>
			</div>
			<div class="body">
				<div class="items">{{range .Items}}{{.Emoji}} {{.Name}} ({{weight .WeightKg}} کیلو) &nbsp;{{end}}</div>
				<div class="totals">
					<span>جمع کل: {{toman .Total}} تومان</span>
					<span>پرداخت‌شده: {{toman .Deposit}} تومان</span>
					{{if gt .Remaining 0}}<span style="color:#b3261e;">باقی‌مانده: {{toman .Remaining}} تومان</span>{{end}}
				</div>
				<div class="meta">📍 {{.Address}} &nbsp;|&nbsp; 📞 {{.Phone}}</div>
			</div>
			<div class="loc">
				{{if .HasLocation}}
					<a target="_blank" href="https://www.google.com/maps?q={{.CustomerLat}},{{.CustomerLng}}">📍 لوکیشن مشتری روی نقشه</a>
					<a class="snapp" target="_blank" href="snapp://origin?lat={{.CustomerLat}}&lng={{.CustomerLng}}">🛵 تلاش برای باز کردن اسنپ‌باکس*</a>
				{{else if eq .Status "shipped"}}
					<span class="none">در انتظار دریافت لوکیشن از مشتری…</span>
				{{end}}
			</div>
			<div class="card-actions">
				{{if eq .Status "pending"}}
				<form method="post" action="/orders/confirm"><input type="hidden" name="id" value="{{.ID}}"><button type="submit">✅ تایید سفارش</button></form>
				{{else if eq .Status "confirmed"}}
				<form method="post" action="/orders/ship"><input type="hidden" name="id" value="{{.ID}}"><button class="ship" type="submit">🚚 ارسال شد (درخواست لوکیشن از مشتری)</button></form>
				{{else}}
				<div class="done-note">✅ فرآیند این سفارش کامل شده</div>
				{{end}}
			</div>
		</div>
		{{end}}
	</div>
	<p style="color:#888; font-size:12px; margin-top:14px;">
		* لینک اسنپ‌باکس آزمایشی است؛ چون Snapp مستندات عمومی رسمی برای این deep link منتشر نکرده، مطمئن نیستیم روی گوشی شما باز می‌شه یا نه — اگه کار نکرد، از لینک «روی نقشه» برای دیدن مختصات و باز کردن دستی اسنپ‌باکس استفاده کنید.
	</p>
	{{end}}
</body>
</html>
`))

type ordersPageData struct {
	Orders []db.Order
	Range  string
}

// tehran is used to compute "today"/"this week"/"this month" boundaries in
// the shop's own timezone rather than UTC (orders.created_at is stored in
// UTC, but "today" should mean today in Iran).
var tehran = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Tehran")
	if err != nil {
		log.Printf("admin: loading Asia/Tehran timezone: %v (falling back to UTC+3:30)", err)
		return time.FixedZone("Asia/Tehran", 3*60*60+30*60)
	}
	return loc
}()

// rangeSince returns the start of the requested range in the shop's
// timezone, or a zero time for "all" (no filtering).
func rangeSince(rangeKey string) time.Time {
	now := time.Now().In(tehran)
	y, m, d := now.Date()
	startOfDay := time.Date(y, m, d, 0, 0, 0, 0, tehran)
	switch rangeKey {
	case "today":
		return startOfDay
	case "week":
		// Iran's week starts Saturday; time.Weekday Saturday = 6.
		daysSinceSaturday := (int(now.Weekday()) - int(time.Saturday) + 7) % 7
		return startOfDay.AddDate(0, 0, -daysSinceSaturday)
	case "month":
		return time.Date(y, m, 1, 0, 0, 0, 0, tehran)
	default:
		return time.Time{}
	}
}

func (s *Server) handleOrders(w http.ResponseWriter, r *http.Request) {
	rangeKey := r.URL.Query().Get("range")
	switch rangeKey {
	case "today", "week", "month":
	default:
		rangeKey = "all"
	}

	orders, err := s.data.ListOrdersSince(rangeSince(rangeKey), 200)
	if err != nil {
		http.Error(w, "خطا در خواندن سفارش‌ها", http.StatusInternalServerError)
		log.Printf("admin: ListOrdersSince: %v", err)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := ordersTemplate.Execute(w, ordersPageData{Orders: orders, Range: rangeKey}); err != nil {
		log.Printf("admin: render orders: %v", err)
	}
}

func (s *Server) parseOrderIDForm(r *http.Request) (int64, error) {
	if err := r.ParseForm(); err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(r.FormValue("id")), 10, 64)
}

// handleConfirmOrder and handleShipOrder delegate the actual status change
// and customer notification to *bot.Bot (bot.ConfirmOrder/ShipOrder), the
// same methods the admin's own Bale-chat buttons use, so both surfaces stay
// in sync.
func (s *Server) handleConfirmOrder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id, err := s.parseOrderIDForm(r)
	if err != nil {
		log.Printf("admin: confirm order: %v", err)
		http.Redirect(w, r, "/orders", http.StatusFound)
		return
	}
	if s.bot != nil {
		if _, err := s.bot.ConfirmOrder(id); err != nil {
			log.Printf("admin: ConfirmOrder(%d): %v", id, err)
		}
	}
	http.Redirect(w, r, "/orders", http.StatusFound)
}

func (s *Server) handleShipOrder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id, err := s.parseOrderIDForm(r)
	if err != nil {
		log.Printf("admin: ship order: %v", err)
		http.Redirect(w, r, "/orders", http.StatusFound)
		return
	}
	if s.bot != nil {
		if _, err := s.bot.ShipOrder(id); err != nil {
			log.Printf("admin: ShipOrder(%d): %v", id, err)
		}
	}
	http.Redirect(w, r, "/orders", http.StatusFound)
}

var statsTemplate = template.Must(template.New("stats").Funcs(funcMap).Parse(`
<!doctype html>
<html lang="fa" dir="rtl">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>آمار سفارش‌ها</title>
<style>
	body { font-family: Tahoma, sans-serif; background:#f6f7f4; margin:0; padding:24px; color:#222; }
	h1 { font-size:20px; }
	nav a { margin-inline-end:16px; color:#2e7d32; text-decoration:none; font-weight:bold; }
	.cards { display:flex; flex-wrap:wrap; gap:16px; }
	.card { background:#fff; border-radius:10px; padding:18px 22px; min-width:160px; box-shadow:0 1px 2px rgba(0,0,0,.06); }
	.card .n { font-size:26px; font-weight:bold; color:#2e7d32; }
	.card .l { font-size:13px; color:#666; margin-top:4px; }
</style>
</head>
<body>
	<nav><a href="/fruits">میوه‌ها</a><a href="/orders">سفارش‌ها</a><a href="/stats">آمار</a><a href="/wallets">کیف‌پول</a></nav>
	<h1>📊 آمار سفارش‌ها</h1>
	<div class="cards">
		<div class="card"><div class="n">{{.TotalOrders}}</div><div class="l">کل سفارش‌ها</div></div>
		<div class="card"><div class="n">{{toman .TotalRevenue}}</div><div class="l">جمع کل فروش (تومان)</div></div>
		<div class="card"><div class="n">{{toman .TotalDeposits}}</div><div class="l">جمع مبالغ دریافتی (تومان)</div></div>
		<div class="card"><div class="n">{{.PendingCount}}</div><div class="l">⏳ در انتظار تایید</div></div>
		<div class="card"><div class="n">{{.ConfirmedCount}}</div><div class="l">✅ تایید شده</div></div>
		<div class="card"><div class="n">{{.ShippedCount}}</div><div class="l">🚚 ارسال شده</div></div>
	</div>
</body>
</html>
`))

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.data.Stats()
	if err != nil {
		http.Error(w, "خطا در خواندن آمار", http.StatusInternalServerError)
		log.Printf("admin: Stats: %v", err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := statsTemplate.Execute(w, stats); err != nil {
		log.Printf("admin: render stats: %v", err)
	}
}

var walletsTemplate = template.Must(template.New("wallets").Funcs(funcMap).Parse(`
<!doctype html>
<html lang="fa" dir="rtl">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>کیف‌پول مشتری‌ها</title>
<style>
	body { font-family: Tahoma, sans-serif; background:#f6f7f4; margin:0; padding:24px; color:#222; }
	h1 { font-size:20px; }
	nav a { margin-inline-end:16px; color:#2e7d32; text-decoration:none; font-weight:bold; }
	.empty { padding:16px; color:#777; }
	.list { border:1px solid #e0e0e0; border-radius:10px; overflow:hidden; background:#fff; }
	.row {
		display:grid; grid-template-columns: 1fr 130px 140px 100px; gap:12px; align-items:center;
		padding:12px 14px; border-bottom:1px solid #e0e0e0;
	}
	.row .name { font-weight:bold; }
	.row .chatid { font-size:12px; color:#888; }
	.row .debt { color:#b3261e; font-weight:bold; }
	.row input {
		width:100%; box-sizing:border-box; padding:6px 8px; border:1px solid #ccc; border-radius:6px;
	}
	.row button {
		padding:7px 10px; border:none; border-radius:6px; background:#2e7d32; color:#fff; cursor:pointer; font-size:12px;
	}
	.row .zero { background:#666; margin-inline-start:4px; }
	.note { color:#888; font-size:12px; margin-top:12px; }
</style>
</head>
<body>
	<nav><a href="/fruits">میوه‌ها</a><a href="/orders">سفارش‌ها</a><a href="/stats">آمار</a><a href="/wallets">کیف‌پول</a></nav>
	<h1>👛 کیف‌پول مشتری‌ها (مبلغ باقی‌مانده بدهکاری)</h1>
	{{if not .Customers}}
		<div class="empty">هیچ مشتری‌ای در حال حاضر بدهی باقی‌مانده ندارد.</div>
	{{else}}
	<div class="list">
		{{range .Customers}}
		<div class="row">
			<div>
				<div class="name">{{.FullName}}</div>
				<div class="chatid">chat id: {{.ChatID}}</div>
			</div>
			<div class="debt">{{toman .WalletDebt}} تومان</div>
			<form method="post" action="/wallets/update" style="display:flex; gap:6px;">
				<input type="hidden" name="chat_id" value="{{.ChatID}}">
				<input type="number" name="amount" value="{{.WalletDebt}}" step="1000">
				<button type="submit">ذخیره</button>
			</form>
			<form method="post" action="/wallets/zero" onsubmit="return confirm('بدهی {{.FullName}} صفر بشه؟');">
				<input type="hidden" name="chat_id" value="{{.ChatID}}">
				<button class="zero" type="submit">🔄 صفر کردن</button>
			</form>
		</div>
		{{end}}
	</div>
	{{end}}
	<p class="note">این مبلغ همون باقی‌مونده‌ی سفارش‌هاییه که مشتری فقط ودیعه پرداخت کرده. وقتی هنگام تحویل بقیه پول رو نقدی/کارتی گرفتید، بزنید «صفر کردن».</p>
</body>
</html>
`))

type walletsPageData struct {
	Customers []db.Customer
}

func (s *Server) handleWallets(w http.ResponseWriter, r *http.Request) {
	customers, err := s.data.ListCustomersWithDebt()
	if err != nil {
		http.Error(w, "خطا در خواندن کیف‌پول‌ها", http.StatusInternalServerError)
		log.Printf("admin: ListCustomersWithDebt: %v", err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := walletsTemplate.Execute(w, walletsPageData{Customers: customers}); err != nil {
		log.Printf("admin: render wallets: %v", err)
	}
}

func (s *Server) parseWalletForm(r *http.Request) (chatID int64, amount int, err error) {
	if err = r.ParseForm(); err != nil {
		return 0, 0, err
	}
	chatID, err = strconv.ParseInt(strings.TrimSpace(r.FormValue("chat_id")), 10, 64)
	if err != nil {
		return 0, 0, err
	}
	amountStr := strings.TrimSpace(r.FormValue("amount"))
	if amountStr == "" {
		return chatID, 0, nil
	}
	amount, err = strconv.Atoi(amountStr)
	return chatID, amount, err
}

func (s *Server) handleUpdateWallet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	chatID, amount, err := s.parseWalletForm(r)
	if err != nil {
		log.Printf("admin: update wallet: %v", err)
		http.Redirect(w, r, "/wallets", http.StatusFound)
		return
	}
	if err := s.data.SetWalletDebt(chatID, amount); err != nil {
		log.Printf("admin: SetWalletDebt(%d, %d): %v", chatID, amount, err)
	}
	http.Redirect(w, r, "/wallets", http.StatusFound)
}

func (s *Server) handleZeroWallet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/wallets", http.StatusFound)
		return
	}
	chatID, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("chat_id")), 10, 64)
	if err != nil {
		log.Printf("admin: zero wallet: %v", err)
		http.Redirect(w, r, "/wallets", http.StatusFound)
		return
	}
	if err := s.data.SetWalletDebt(chatID, 0); err != nil {
		log.Printf("admin: SetWalletDebt(%d, 0): %v", chatID, err)
	}
	http.Redirect(w, r, "/wallets", http.StatusFound)
}
