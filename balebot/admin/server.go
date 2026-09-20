// Package admin serves a small authenticated web panel for editing each
// fruit's price and minimum order weight, and for reviewing recent orders.
package admin

import (
	"crypto/subtle"
	"html/template"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

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
	mux.HandleFunc("/photo", s.auth(s.handlePhoto))
	mux.HandleFunc("/orders", s.auth(s.handleOrders))
	mux.HandleFunc("/orders/confirm", s.auth(s.handleConfirmOrder))
	mux.HandleFunc("/orders/ship", s.auth(s.handleShipOrder))
	mux.HandleFunc("/stats", s.auth(s.handleStats))
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
		grid-template-columns: 56px 1.6fr 1fr 1fr 1.4fr 90px;
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
</style>
</head>
<body>
	<nav><a href="/fruits">میوه‌ها</a><a href="/orders">سفارش‌ها</a><a href="/stats">آمار</a></nav>
	<h1>🍉 مدیریت قیمت، حداقل وزن و عکس میوه‌ها</h1>
	{{if .Message}}<div class="msg {{.MessageClass}}">{{.Message}}</div>{{end}}
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
			<div><button type="submit">ذخیره</button></div>
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
	table { width:100%; border-collapse:collapse; background:#fff; border-radius:10px; overflow:hidden; }
	th, td { padding:10px 12px; border-bottom:1px solid #e0e0e0; text-align:right; vertical-align:top; }
	th { background:#f0f0f0; }
	.empty { padding:16px; color:#777; }
	.status { display:inline-block; padding:3px 8px; border-radius:6px; font-size:12px; white-space:nowrap; }
	.status.pending { background:#fff4e0; color:#8a5a00; }
	.status.confirmed { background:#e6f4ea; color:#1e7e34; }
	.status.shipped { background:#e3f0fd; color:#0b5ed7; }
	.actions form { display:inline; }
	.actions button { padding:6px 10px; margin:2px 0; border:none; border-radius:6px; background:#2e7d32; color:#fff; cursor:pointer; font-size:12px; display:block; width:100%; box-sizing:border-box; }
	.actions button.ship { background:#0b5ed7; }
	.loc a { display:block; font-size:12px; margin-top:4px; }
	.loc .snapp { color:#888; }
	.loc .none { color:#999; font-size:12px; }
</style>
</head>
<body>
	<nav><a href="/fruits">میوه‌ها</a><a href="/orders">سفارش‌ها</a><a href="/stats">آمار</a></nav>
	<h1>📦 سفارش‌های اخیر</h1>
	{{if not .Orders}}
		<div class="empty">هنوز سفارشی ثبت نشده است.</div>
	{{else}}
	<table>
		<tr><th>#</th><th>زمان</th><th>وضعیت</th><th>اقلام</th><th>جمع کل</th><th>ودیعه</th><th>آدرس</th><th>تماس</th><th>لوکیشن پیک</th><th>عملیات</th></tr>
		{{range .Orders}}
		<tr>
			<td>{{.ID}}</td>
			<td>{{.CreatedAt.Format "2006-01-02 15:04"}}</td>
			<td><span class="status {{.Status}}">{{.StatusLabel}}</span></td>
			<td>{{range .Items}}{{.Emoji}} {{.Name}} ({{weight .WeightKg}} کیلو)<br>{{end}}</td>
			<td>{{toman .Total}} تومان</td>
			<td>{{toman .Deposit}} تومان</td>
			<td>{{.Address}}</td>
			<td>{{.Phone}}</td>
			<td class="loc">
				{{if .HasLocation}}
					<a target="_blank" href="https://www.google.com/maps?q={{.CustomerLat}},{{.CustomerLng}}">📍 روی نقشه</a>
					<a class="snapp" target="_blank" href="snapp://origin?lat={{.CustomerLat}}&lng={{.CustomerLng}}">🛵 تلاش برای باز کردن اسنپ‌باکس*</a>
				{{else}}
					<span class="none">—</span>
				{{end}}
			</td>
			<td class="actions">
				{{if eq .Status "pending"}}
				<form method="post" action="/orders/confirm"><input type="hidden" name="id" value="{{.ID}}"><button type="submit">✅ تایید سفارش</button></form>
				{{else if eq .Status "confirmed"}}
				<form method="post" action="/orders/ship"><input type="hidden" name="id" value="{{.ID}}"><button class="ship" type="submit">🚚 ارسال شد</button></form>
				{{else}}
				&mdash;
				{{end}}
			</td>
		</tr>
		{{end}}
	</table>
	<p style="color:#888; font-size:12px; margin-top:12px;">
		* لینک اسنپ‌باکس آزمایشی است؛ چون Snapp مستندات عمومی رسمی برای این deep link منتشر نکرده، مطمئن نیستیم روی گوشی شما باز می‌شه یا نه — اگه کار نکرد، از لینک «روی نقشه» برای دیدن مختصات و باز کردن دستی اسنپ‌باکس استفاده کنید.
	</p>
	{{end}}
</body>
</html>
`))

type ordersPageData struct {
	Orders []db.Order
}

func (s *Server) handleOrders(w http.ResponseWriter, r *http.Request) {
	orders, err := s.data.ListRecentOrders(100)
	if err != nil {
		http.Error(w, "خطا در خواندن سفارش‌ها", http.StatusInternalServerError)
		log.Printf("admin: ListRecentOrders: %v", err)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := ordersTemplate.Execute(w, ordersPageData{Orders: orders}); err != nil {
		log.Printf("admin: render orders: %v", err)
	}
}

func (s *Server) parseOrderIDForm(r *http.Request) (int64, *db.Order, error) {
	if err := r.ParseForm(); err != nil {
		return 0, nil, err
	}
	id, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("id")), 10, 64)
	if err != nil {
		return 0, nil, err
	}
	order, err := s.data.GetOrder(id)
	if err != nil {
		return id, nil, err
	}
	return id, order, nil
}

func (s *Server) handleConfirmOrder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id, order, err := s.parseOrderIDForm(r)
	if err != nil || order == nil {
		log.Printf("admin: confirm order %d: %v", id, err)
		http.Redirect(w, r, "/orders", http.StatusFound)
		return
	}
	if err := s.data.UpdateOrderStatus(id, db.StatusConfirmed); err != nil {
		log.Printf("admin: UpdateOrderStatus(%d, confirmed): %v", id, err)
	} else if s.bot != nil {
		s.bot.NotifyOrderConfirmed(order.ChatID, id)
	}
	http.Redirect(w, r, "/orders", http.StatusFound)
}

func (s *Server) handleShipOrder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id, order, err := s.parseOrderIDForm(r)
	if err != nil || order == nil {
		log.Printf("admin: ship order %d: %v", id, err)
		http.Redirect(w, r, "/orders", http.StatusFound)
		return
	}
	if err := s.data.UpdateOrderStatus(id, db.StatusShipped); err != nil {
		log.Printf("admin: UpdateOrderStatus(%d, shipped): %v", id, err)
	} else if s.bot != nil {
		s.bot.RequestShipmentLocation(order.ChatID, id)
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
	<nav><a href="/fruits">میوه‌ها</a><a href="/orders">سفارش‌ها</a><a href="/stats">آمار</a></nav>
	<h1>📊 آمار سفارش‌ها</h1>
	<div class="cards">
		<div class="card"><div class="n">{{.TotalOrders}}</div><div class="l">کل سفارش‌ها</div></div>
		<div class="card"><div class="n">{{toman .TotalRevenue}}</div><div class="l">جمع کل فروش (تومان)</div></div>
		<div class="card"><div class="n">{{toman .TotalDeposits}}</div><div class="l">جمع ودیعه‌های دریافتی (تومان)</div></div>
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
