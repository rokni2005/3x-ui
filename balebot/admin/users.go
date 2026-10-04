package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"html/template"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"balebot/db"
)

// ---- authentication ----
//
// Basic Auth against the admin_users table. Verifying a PBKDF2 hash on every
// request would make each page load slow (a page is several requests), so a
// successful login is remembered for a few minutes keyed by a hash of the
// credentials; the cache is flushed whenever accounts change. Failed logins
// are throttled per client IP, since nothing in front of the panel does
// rate limiting any more.

const (
	authCacheTTL    = 10 * time.Minute
	maxFailures     = 10
	failureWindow   = 10 * time.Minute
	lockoutDuration = 10 * time.Minute
)

type ctxKey int

const userCtxKey ctxKey = 1

type failRecord struct {
	count        int
	windowStart  time.Time
	blockedUntil time.Time
}

type authState struct {
	mu    sync.Mutex
	ok    map[string]cachedLogin
	fails map[string]*failRecord
}

type cachedLogin struct {
	username string
	expires  time.Time
}

func newAuthState() *authState {
	return &authState{ok: map[string]cachedLogin{}, fails: map[string]*failRecord{}}
}

func credKey(user, pass string) string {
	sum := sha256.Sum256([]byte(user + "\x00" + pass))
	return hex.EncodeToString(sum[:])
}

func (a *authState) cached(user, pass string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	e, ok := a.ok[credKey(user, pass)]
	if !ok || time.Now().After(e.expires) || e.username != user {
		delete(a.ok, credKey(user, pass))
		return false
	}
	return true
}

func (a *authState) remember(user, pass string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ok[credKey(user, pass)] = cachedLogin{username: user, expires: time.Now().Add(authCacheTTL)}
}

// flush forgets every remembered login (called after any account change so
// a changed/removed password stops working immediately).
func (a *authState) flush() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ok = map[string]cachedLogin{}
}

func (a *authState) blocked(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	f := a.fails[ip]
	return f != nil && time.Now().Before(f.blockedUntil)
}

func (a *authState) recordFailure(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	f := a.fails[ip]
	if f == nil || now.Sub(f.windowStart) > failureWindow {
		f = &failRecord{windowStart: now}
		a.fails[ip] = f
	}
	f.count++
	if f.count >= maxFailures {
		f.blockedUntil = now.Add(lockoutDuration)
	}
}

func (a *authState) clearFailures(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.fails, ip)
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func currentUser(r *http.Request) string {
	u, _ := r.Context().Value(userCtxKey).(string)
	return u
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if s.authz.blocked(ip) {
			http.Error(w, "Too many failed attempts, try again later", http.StatusTooManyRequests)
			return
		}

		user, pass, ok := r.BasicAuth()
		if ok {
			valid := s.authz.cached(user, pass)
			if !valid {
				var err error
				valid, err = s.data.VerifyAdminUser(user, pass)
				if err != nil {
					log.Printf("admin: VerifyAdminUser: %v", err)
				}
				if valid {
					s.authz.remember(user, pass)
				}
			}
			if valid {
				s.authz.clearFailures(ip)
				next(w, r.WithContext(context.WithValue(r.Context(), userCtxKey, user)))
				return
			}
			s.authz.recordFailure(ip)
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="fruit bot admin"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}
}

// ---- /users page ----

var usersTemplate = template.Must(template.New("users").Funcs(funcMap).Parse(`
<!doctype html>
<html lang="fa" dir="rtl">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>کاربران پنل</title>
<style>
	body { font-family: Tahoma, sans-serif; background:#f6f7f4; margin:0; padding:24px; color:#222; }
	h1 { font-size:20px; }
	nav a { margin-inline-end:16px; color:#2e7d32; text-decoration:none; font-weight:bold; }
	.msg { padding:10px 14px; border-radius:8px; margin-bottom:16px; }
	.msg.ok { background:#e6f4ea; color:#1e7e34; }
	.msg.err { background:#fdecea; color:#b3261e; }
	.box { background:#fff; border:1px solid #e0e0e0; border-radius:10px; padding:16px; margin-bottom:18px; }
	.box h2 { font-size:15px; margin:0 0 12px; }
	.row { display:grid; grid-template-columns: 1.2fr 1.2fr 1.2fr auto auto; gap:10px; align-items:center; padding:10px 0; border-top:1px solid #eee; }
	.row:first-of-type { border-top:none; }
	.addgrid { display:grid; grid-template-columns: 1fr 1fr auto; gap:10px; align-items:end; }
	label { display:block; font-size:12px; color:#666; margin-bottom:4px; }
	input[type=text], input[type=password] { width:100%; box-sizing:border-box; padding:7px 8px; border:1px solid #ccc; border-radius:6px; }
	button { padding:8px 14px; border:none; border-radius:6px; background:#2e7d32; color:#fff; cursor:pointer; }
	button.del { background:#b3261e; }
	.me { color:#888; font-size:12px; }
	.note { color:#888; font-size:12px; }
	@media (max-width: 700px) { .row, .addgrid { grid-template-columns: 1fr; } }
</style>
</head>
<body>
	<nav><a href="/fruits">میوه‌ها</a><a href="/orders">سفارش‌ها</a><a href="/stats">آمار</a><a href="/wallets">کیف‌پول</a><a href="/users">کاربران</a></nav>
	<h1>👥 کاربران پنل</h1>
	{{if .Message}}<div class="msg {{.MessageClass}}">{{.Message}}</div>{{end}}

	<div class="box">
		<h2>➕ افزودن کاربر جدید (مثلاً شریک)</h2>
		<form class="addgrid" method="post" action="/users/add">
			<div><label>نام کاربری (۳ تا ۳۲ حرف انگلیسی/عدد)</label><input type="text" name="username" required autocomplete="off"></div>
			<div><label>رمز عبور (حداقل ۸ کاراکتر)</label><input type="password" name="password" required minlength="8" autocomplete="new-password"></div>
			<div><button type="submit">افزودن</button></div>
		</form>
		<p class="note">همه‌ی کاربران دسترسی کامل دارند (میوه‌ها، سفارش‌ها، کیف‌پول، کاربران).</p>
	</div>

	<div class="box">
		<h2>کاربران موجود</h2>
		{{range .Users}}
		<form class="row" method="post" action="/users/update">
			<input type="hidden" name="username" value="{{.Username}}">
			<div><strong>{{.Username}}</strong> {{if eq .Username $.Current}}<span class="me">(شما)</span>{{end}}<div class="note">{{.CreatedAt}}</div></div>
			<div><input type="text" name="new_username" placeholder="نام کاربری جدید (اختیاری)" autocomplete="off"></div>
			<div><input type="password" name="new_password" placeholder="رمز جدید (اختیاری)" minlength="8" autocomplete="new-password"></div>
			<div><button type="submit">ذخیره</button></div>
			<div><button class="del" type="submit" formaction="/users/delete" formnovalidate onclick="return confirm('کاربر «{{.Username}}» حذف بشه؟');">🗑</button></div>
		</form>
		{{end}}
		<p class="note">بعد از تغییر نام کاربری یا رمز خودتان، مرورگر دوباره ورود می‌خواهد.</p>
	</div>
</body>
</html>
`))

type usersPageData struct {
	Users        []db.AdminUser
	Current      string
	Message      string
	MessageClass string
}

var userStatusMessages = map[string]string{
	"ok":       "✅ انجام شد.",
	"exists":   "❌ این نام کاربری قبلاً وجود دارد.",
	"badname":  "❌ نام کاربری نامعتبر است (۳ تا ۳۲ حرف انگلیسی، عدد، _ . -).",
	"weak":     "❌ رمز عبور باید حداقل ۸ کاراکتر باشد.",
	"last":     "❌ آخرین کاربر پنل را نمی‌شود حذف کرد.",
	"self":     "❌ حذف کاربر خودتان ممکن نیست (با کاربر دیگری وارد شوید و بعد حذفش کنید).",
	"notfound": "❌ کاربر پیدا نشد.",
	"err":      "❌ خطا در انجام عملیات.",
}

func userErrStatus(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, db.ErrUserExists):
		return "exists"
	case errors.Is(err, db.ErrInvalidUsername):
		return "badname"
	case errors.Is(err, db.ErrWeakPassword):
		return "weak"
	case errors.Is(err, db.ErrLastUser):
		return "last"
	case errors.Is(err, db.ErrUserNotFound):
		return "notfound"
	default:
		return "err"
	}
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.data.ListAdminUsers()
	if err != nil {
		http.Error(w, "خطا در خواندن کاربران", http.StatusInternalServerError)
		log.Printf("admin: ListAdminUsers: %v", err)
		return
	}
	data := usersPageData{Users: users, Current: currentUser(r)}
	if st := r.URL.Query().Get("status"); st != "" {
		data.Message = userStatusMessages[st]
		if st == "ok" {
			data.MessageClass = "ok"
		} else {
			data.MessageClass = "err"
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := usersTemplate.Execute(w, data); err != nil {
		log.Printf("admin: render users: %v", err)
	}
}

func redirectUsers(w http.ResponseWriter, r *http.Request, status string) {
	http.Redirect(w, r, "/users?status="+status, http.StatusFound)
}

func (s *Server) handleAddUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		redirectUsers(w, r, "err")
		return
	}
	err := s.data.CreateAdminUser(strings.TrimSpace(r.FormValue("username")), r.FormValue("password"))
	if err != nil && userErrStatus(err) == "err" {
		log.Printf("admin: CreateAdminUser: %v", err)
	}
	redirectUsers(w, r, userErrStatus(err))
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		redirectUsers(w, r, "err")
		return
	}
	err := s.data.UpdateAdminUser(
		strings.TrimSpace(r.FormValue("username")),
		strings.TrimSpace(r.FormValue("new_username")),
		r.FormValue("new_password"),
	)
	if err != nil && userErrStatus(err) == "err" {
		log.Printf("admin: UpdateAdminUser: %v", err)
	}
	s.authz.flush()
	redirectUsers(w, r, userErrStatus(err))
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		redirectUsers(w, r, "err")
		return
	}
	target := strings.TrimSpace(r.FormValue("username"))
	if target == currentUser(r) {
		redirectUsers(w, r, "self")
		return
	}
	err := s.data.DeleteAdminUser(target)
	if err != nil && userErrStatus(err) == "err" {
		log.Printf("admin: DeleteAdminUser: %v", err)
	}
	s.authz.flush()
	redirectUsers(w, r, userErrStatus(err))
}
