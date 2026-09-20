package bot

import (
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	"balebot/bale"
	"balebot/db"
)

const (
	weightStepKg = 0.5
	maxWeightKg  = 30

	// depositInvoicePayload identifies our one and only invoice type; kept
	// short and constant since the deposit amount is a fixed shop-wide
	// setting, not per-order.
	depositInvoicePayload = "deposit"

	// Persistent reply-keyboard button labels. Tapping one of these sends
	// its exact text back to the bot as an ordinary message, so neither
	// customers nor the admin ever need to type a command like /start.
	customerMenuButton = "🍉 منو و سفارش میوه"
	adminOrdersButton  = "📦 سفارش‌های اخیر"
)

// Config holds the deposit/payment details shown to customers.
type Config struct {
	AdminChatID   int64
	DepositAmount int
	CardNumber    string
	CardHolder    string

	// PaymentProviderToken is a Bale wallet/payment provider token (e.g.
	// "WALLET-..."). When set, the deposit is collected via a native
	// sendInvoice payment instead of manual card-to-card transfer.
	PaymentProviderToken string
	// PaymentCurrency is the currency code passed to sendInvoice.
	PaymentCurrency string
	// PaymentAmountMultiplier converts DepositAmount (Toman) into the
	// currency's smallest unit expected by sendInvoice. Bale's payment API
	// isn't fully documented here, so this defaults to 10 (Toman -> Rial)
	// but is meant to be corrected via config after a real test payment.
	PaymentAmountMultiplier int
}

// Bot wires together the Bale API client, the persistent catalog/orders
// store and per-chat session state for the fruit-ordering conversation.
type Bot struct {
	api      *bale.Client
	sessions *Store
	data     *db.Store
	cfg      Config
}

// New builds a Bot ready to Run.
func New(api *bale.Client, data *db.Store, cfg Config) *Bot {
	return &Bot{api: api, sessions: NewStore(), data: data, cfg: cfg}
}

// Run starts the long-polling loop. It blocks until an unrecoverable error occurs.
func (b *Bot) Run() error {
	var offset int64
	for {
		updates, err := b.api.GetUpdates(offset)
		if err != nil {
			log.Printf("getUpdates error: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}
		for _, u := range updates {
			offset = u.UpdateID + 1
			b.handleUpdate(u)
		}
	}
}

func (b *Bot) handleUpdate(u bale.Update) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic while handling update %d: %v", u.UpdateID, r)
		}
	}()

	if u.PreCheckoutQuery != nil {
		b.handlePreCheckoutQuery(*u.PreCheckoutQuery)
		return
	}
	if u.CallbackQuery != nil {
		b.handleCallback(*u.CallbackQuery)
		return
	}
	if u.Message != nil {
		b.handleMessage(*u.Message)
	}
}

// handlePreCheckoutQuery approves or rejects a charge just before Bale
// processes it. We reject anything that doesn't match our one known invoice
// (payload and amount), since accepting a mismatched charge would collect
// the wrong amount from the customer.
func (b *Bot) handlePreCheckoutQuery(q bale.PreCheckoutQuery) {
	expected := b.cfg.DepositAmount * b.cfg.PaymentAmountMultiplier
	if q.InvoicePayload != depositInvoicePayload || q.TotalAmount != expected {
		if err := b.api.AnswerPreCheckoutQuery(q.ID, false, "مبلغ پرداخت با سفارش مطابقت ندارد. لطفا دوباره تلاش کنید."); err != nil {
			log.Printf("AnswerPreCheckoutQuery (reject): %v", err)
		}
		return
	}
	if err := b.api.AnswerPreCheckoutQuery(q.ID, true, ""); err != nil {
		log.Printf("AnswerPreCheckoutQuery (accept): %v", err)
	}
}

// ---- messages ----

func (b *Bot) handleMessage(msg bale.Message) {
	chatID := msg.Chat.ID
	text := strings.TrimSpace(msg.Text)

	if b.cfg.AdminChatID != 0 && chatID == b.cfg.AdminChatID {
		b.handleAdminMessage(chatID, text)
		return
	}

	sess := b.sessions.Get(chatID)

	if msg.SuccessfulPayment != nil {
		b.finalizeOrder(chatID, sess, &msg)
		return
	}

	if text == "/start" || text == customerMenuButton {
		sess.resetOrder()
		b.api.SendMessage(chatID, "🍇 سلام! به سفارش آنلاین میوه خوش آمدید.\nهر وقت خواستید به این منو برگردید، کافیه دکمه پایین صفحه رو بزنید 👇", customerMenuKeyboard())
		b.sendCatalog(chatID, sess)
		return
	}

	switch sess.Stage {
	case StageAwaitingAddress:
		if len([]rune(text)) < 10 {
			b.api.SendMessage(chatID, "لطفا آدرس کامل و دقیق خود را وارد کنید (حداقل شامل شهر، خیابان و پلاک):", nil)
			return
		}
		sess.Address = text
		sess.Stage = StageAwaitingPhone
		b.api.SendMessage(chatID, "ممنون 🙏\nلطفا شماره تماس خود را وارد کنید:", nil)

	case StageAwaitingPhone:
		if !isValidPhone(text) {
			b.api.SendMessage(chatID, "شماره تماس معتبر نیست. لطفا یک شماره موبایل صحیح وارد کنید (مثال: 09121234567):", nil)
			return
		}
		sess.Phone = text
		sess.Stage = StageInvoice
		b.sendInvoice(chatID, sess)

	case StageAwaitingReceipt:
		if len(msg.Photo) > 0 {
			b.finalizeOrder(chatID, sess, &msg)
			return
		}
		b.api.SendMessage(chatID, "لطفا تصویر رسید واریزی ودیعه را همینجا ارسال کنید 🧾", nil)

	default:
		b.sendCatalog(chatID, sess)
	}
}

var phonePattern = regexp.MustCompile(`^(\+?98|0)?9\d{9}$`)

func isValidPhone(text string) bool {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' || r == '+' {
			return r
		}
		return -1
	}, text)
	return phonePattern.MatchString(digits)
}

// ---- callback queries ----

func (b *Bot) handleCallback(cq bale.CallbackQuery) {
	if cq.Message == nil {
		b.api.AnswerCallbackQuery(cq.ID, "", false)
		return
	}
	chatID := cq.Message.Chat.ID
	messageID := cq.Message.MessageID
	sess := b.sessions.Get(chatID)
	data := cq.Data

	switch {
	case data == "noop":
		b.api.AnswerCallbackQuery(cq.ID, "", false)

	case data == "back:menu":
		sess.Stage = StageBrowsing
		b.api.AnswerCallbackQuery(cq.ID, "", false)
		b.editCatalog(chatID, messageID, sess)

	case strings.HasPrefix(data, "fruit:"):
		id := strings.TrimPrefix(data, "fruit:")
		fruit, err := b.data.GetFruit(id)
		if err != nil {
			log.Printf("GetFruit(%s): %v", id, err)
			b.api.AnswerCallbackQuery(cq.ID, "خطایی رخ داد، دوباره تلاش کنید", true)
			return
		}
		if fruit == nil {
			b.api.AnswerCallbackQuery(cq.ID, "این میوه یافت نشد", true)
			return
		}
		sess.CurrentFruit = id
		sess.CurrentWeight = fruit.MinWeightKg
		sess.Stage = StageViewingFruit
		b.api.AnswerCallbackQuery(cq.ID, "", false)
		if fruit.PhotoPath != "" {
			// A photo message can't be turned into a text message by
			// editing, so this always opens as a fresh message.
			b.sendFruitPhoto(chatID, sess, fruit)
		} else {
			b.editFruitDetail(chatID, messageID, sess, fruit)
		}

	case data == "w:inc" || data == "w:dec":
		fruit, err := b.data.GetFruit(sess.CurrentFruit)
		if err != nil || fruit == nil {
			b.api.AnswerCallbackQuery(cq.ID, "", false)
			return
		}
		if data == "w:inc" {
			sess.CurrentWeight += weightStepKg
			if sess.CurrentWeight > maxWeightKg {
				sess.CurrentWeight = maxWeightKg
			}
		} else {
			sess.CurrentWeight -= weightStepKg
			if sess.CurrentWeight < fruit.MinWeightKg {
				sess.CurrentWeight = fruit.MinWeightKg
			}
		}
		b.api.AnswerCallbackQuery(cq.ID, "", false)
		if len(cq.Message.Photo) > 0 {
			b.editFruitPhotoCaption(chatID, messageID, sess, fruit)
		} else {
			b.editFruitDetail(chatID, messageID, sess, fruit)
		}

	case strings.HasPrefix(data, "add:"):
		id := strings.TrimPrefix(data, "add:")
		fruit, err := b.data.GetFruit(id)
		if err != nil {
			log.Printf("GetFruit(%s): %v", id, err)
			b.api.AnswerCallbackQuery(cq.ID, "خطایی رخ داد، دوباره تلاش کنید", true)
			return
		}
		if fruit == nil {
			b.api.AnswerCallbackQuery(cq.ID, "این میوه یافت نشد", true)
			return
		}
		sess.AddToCart(CartItem{
			FruitID:    fruit.ID,
			Emoji:      fruit.Emoji,
			Name:       fruit.Name,
			WeightKg:   sess.CurrentWeight,
			PricePerKg: fruit.Price,
		})
		sess.Stage = StageBrowsing
		b.api.AnswerCallbackQuery(cq.ID, fmt.Sprintf("%s %s به سبد خرید اضافه شد ✅", fruit.Emoji, fruit.Name), false)
		b.editCatalog(chatID, messageID, sess)

	case data == "cart:view":
		b.api.AnswerCallbackQuery(cq.ID, "", false)
		b.editCart(chatID, messageID, sess)

	case data == "cart:checkout":
		if len(sess.Cart) == 0 {
			b.api.AnswerCallbackQuery(cq.ID, "سبد خرید شما خالی است", true)
			return
		}
		sess.Stage = StageAwaitingAddress
		b.api.AnswerCallbackQuery(cq.ID, "", false)
		b.api.SendMessage(chatID, "لطفا آدرس کامل خود را برای ارسال سفارش وارد کنید:", nil)

	case data == "pay:deposit":
		b.api.AnswerCallbackQuery(cq.ID, "", false)
		if b.cfg.PaymentProviderToken != "" {
			b.sendDepositInvoice(chatID)
			return
		}
		sess.Stage = StageAwaitingReceipt
		text := fmt.Sprintf(
			"لطفا مبلغ %s تومان بابت ودیعه سفارش را به شماره کارت زیر واریز کنید:\n\n💳 %s\nبه نام: %s\n\nپس از واریز، لطفا تصویر رسید پرداخت را همینجا ارسال کنید تا سفارش شما نهایی شود.",
			FormatToman(b.cfg.DepositAmount), b.cfg.CardNumber, b.cfg.CardHolder,
		)
		b.api.SendMessage(chatID, text, nil)

	default:
		b.api.AnswerCallbackQuery(cq.ID, "", false)
	}
}

// ---- view builders ----

func (b *Bot) sendCatalog(chatID int64, sess *Session) {
	fruits, err := b.data.ListFruits()
	if err != nil {
		log.Printf("ListFruits: %v", err)
		b.api.SendMessage(chatID, "متاسفانه در حال حاضر امکان نمایش لیست میوه‌ها نیست. لطفا بعدا دوباره تلاش کنید.", nil)
		return
	}
	b.api.SendMessage(chatID, catalogText(sess), catalogKeyboard(sess, fruits))
}

func (b *Bot) editCatalog(chatID, messageID int64, sess *Session) {
	fruits, err := b.data.ListFruits()
	if err != nil {
		log.Printf("ListFruits: %v", err)
		return
	}
	if err := b.api.EditMessageText(chatID, messageID, catalogText(sess), catalogKeyboard(sess, fruits)); err != nil {
		b.api.SendMessage(chatID, catalogText(sess), catalogKeyboard(sess, fruits))
	}
}

func catalogText(sess *Session) string {
	var sb strings.Builder
	sb.WriteString("🍇 سلام! به سفارش آنلاین میوه خوش آمدید 🍉\nلطفا میوه مورد نظر خود را انتخاب کنید:")
	if len(sess.Cart) > 0 {
		sb.WriteString(fmt.Sprintf("\n\n🛒 سبد خرید شما: %d قلم — جمع: %s تومان", len(sess.Cart), FormatToman(sess.Total())))
	}
	return sb.String()
}

func catalogKeyboard(sess *Session, fruits []db.Fruit) *bale.InlineKeyboardMarkup {
	var rows [][]bale.InlineKeyboardButton
	for i := 0; i < len(fruits); i += 2 {
		row := []bale.InlineKeyboardButton{
			{Text: fruits[i].Emoji + " " + fruits[i].Name, CallbackData: "fruit:" + fruits[i].ID},
		}
		if i+1 < len(fruits) {
			row = append(row, bale.InlineKeyboardButton{
				Text: fruits[i+1].Emoji + " " + fruits[i+1].Name, CallbackData: "fruit:" + fruits[i+1].ID,
			})
		}
		rows = append(rows, row)
	}
	if len(sess.Cart) > 0 {
		rows = append(rows, []bale.InlineKeyboardButton{
			{Text: fmt.Sprintf("🛒 مشاهده سبد خرید (%d) و ثبت سفارش", len(sess.Cart)), CallbackData: "cart:view"},
		})
	}
	return &bale.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func fruitDetailText(sess *Session, fruit *db.Fruit) string {
	amount := int(sess.CurrentWeight * float64(fruit.Price))
	return fmt.Sprintf(
		"%s %s درجه یک\nقیمت: %s تومان به ازای هر کیلو\nحداقل سفارش: %s کیلوگرم\n\nمقدار انتخابی: %s کیلوگرم\nمبلغ این قلم: %s تومان",
		fruit.Emoji, fruit.Name, FormatToman(fruit.Price), FormatWeight(fruit.MinWeightKg), FormatWeight(sess.CurrentWeight), FormatToman(amount),
	)
}

func fruitDetailKeyboard(sess *Session, fruit *db.Fruit) *bale.InlineKeyboardMarkup {
	return &bale.InlineKeyboardMarkup{InlineKeyboard: [][]bale.InlineKeyboardButton{
		{
			{Text: "➖", CallbackData: "w:dec"},
			{Text: FormatWeight(sess.CurrentWeight) + " کیلوگرم", CallbackData: "noop"},
			{Text: "➕", CallbackData: "w:inc"},
		},
		{{Text: "✅ افزودن به سبد خرید", CallbackData: "add:" + fruit.ID}},
		{{Text: "🔙 بازگشت به لیست میوه‌ها", CallbackData: "back:menu"}},
	}}
}

func (b *Bot) editFruitDetail(chatID, messageID int64, sess *Session, fruit *db.Fruit) {
	if err := b.api.EditMessageText(chatID, messageID, fruitDetailText(sess, fruit), fruitDetailKeyboard(sess, fruit)); err != nil {
		b.api.SendMessage(chatID, fruitDetailText(sess, fruit), fruitDetailKeyboard(sess, fruit))
	}
}

// sendFruitPhoto opens a fruit's detail view as a photo message with the
// usual weight-picker caption/keyboard. If the photo file is missing or the
// upload fails, it falls back to the plain text detail view so a bad/missing
// photo never blocks ordering.
func (b *Bot) sendFruitPhoto(chatID int64, sess *Session, fruit *db.Fruit) {
	f, err := os.Open(b.data.PhotoFullPath(fruit.PhotoPath))
	if err != nil {
		log.Printf("open photo for %s: %v", fruit.ID, err)
		b.api.SendMessage(chatID, fruitDetailText(sess, fruit), fruitDetailKeyboard(sess, fruit))
		return
	}
	defer f.Close()

	if _, err := b.api.SendPhoto(chatID, fruit.PhotoPath, f, fruitDetailText(sess, fruit), fruitDetailKeyboard(sess, fruit)); err != nil {
		log.Printf("SendPhoto for %s: %v", fruit.ID, err)
		b.api.SendMessage(chatID, fruitDetailText(sess, fruit), fruitDetailKeyboard(sess, fruit))
	}
}

// editFruitPhotoCaption updates the weight/amount shown on an already-open
// photo detail message, falling back to a fresh photo message if the edit
// itself fails for some reason.
func (b *Bot) editFruitPhotoCaption(chatID, messageID int64, sess *Session, fruit *db.Fruit) {
	if err := b.api.EditMessageCaption(chatID, messageID, fruitDetailText(sess, fruit), fruitDetailKeyboard(sess, fruit)); err != nil {
		log.Printf("EditMessageCaption for %s: %v", fruit.ID, err)
		b.sendFruitPhoto(chatID, sess, fruit)
	}
}

func cartText(sess *Session) string {
	var sb strings.Builder
	sb.WriteString("🛒 سبد خرید شما:\n\n")
	for _, item := range sess.Cart {
		sb.WriteString(fmt.Sprintf("%s %s — %s کیلوگرم — %s تومان\n", item.Emoji, item.Name, FormatWeight(item.WeightKg), FormatToman(item.LineTotal())))
	}
	sb.WriteString(fmt.Sprintf("\nجمع کل: %s تومان", FormatToman(sess.Total())))
	return sb.String()
}

func cartKeyboard() *bale.InlineKeyboardMarkup {
	return &bale.InlineKeyboardMarkup{InlineKeyboard: [][]bale.InlineKeyboardButton{
		{{Text: "➕ افزودن میوه دیگر", CallbackData: "back:menu"}},
		{{Text: "✅ ثبت آدرس و ادامه سفارش", CallbackData: "cart:checkout"}},
	}}
}

func (b *Bot) editCart(chatID, messageID int64, sess *Session) {
	if err := b.api.EditMessageText(chatID, messageID, cartText(sess), cartKeyboard()); err != nil {
		b.api.SendMessage(chatID, cartText(sess), cartKeyboard())
	}
}

func (b *Bot) sendInvoice(chatID int64, sess *Session) {
	total := sess.Total()
	deposit := b.cfg.DepositAmount
	remaining := total - deposit
	if remaining < 0 {
		remaining = 0
	}

	var sb strings.Builder
	sb.WriteString("🧾 فاکتور سفارش شما\n\n")
	for _, item := range sess.Cart {
		sb.WriteString(fmt.Sprintf("%s %s — %s کیلوگرم — %s تومان\n", item.Emoji, item.Name, FormatWeight(item.WeightKg), FormatToman(item.LineTotal())))
	}
	sb.WriteString(fmt.Sprintf("\nجمع کل: %s تومان\n", FormatToman(total)))
	sb.WriteString("🚚 ارسال ما رایگانه!\n\n")
	sb.WriteString(fmt.Sprintf("برای ثبت نهایی سفارش، مبلغ %s تومان بابت ودیعه پرداخت می‌شود و مابقی مبلغ (%s تومان) پس از تحویل سفارش دریافت خواهد شد.\n\n", FormatToman(deposit), FormatToman(remaining)))
	sb.WriteString(fmt.Sprintf("📍 آدرس: %s\n📞 شماره تماس: %s", sess.Address, sess.Phone))

	keyboard := &bale.InlineKeyboardMarkup{InlineKeyboard: [][]bale.InlineKeyboardButton{
		{{Text: fmt.Sprintf("💳 پرداخت ودیعه (%s تومان)", FormatToman(deposit)), CallbackData: "pay:deposit"}},
	}}
	b.api.SendMessage(chatID, sb.String(), keyboard)
}

// sendDepositInvoice asks Bale to charge the customer's wallet for the
// deposit via a native invoice, instead of the manual card-transfer flow.
func (b *Bot) sendDepositInvoice(chatID int64) {
	amount := b.cfg.DepositAmount * b.cfg.PaymentAmountMultiplier
	_, err := b.api.SendInvoice(
		chatID,
		"پرداخت ودیعه سفارش میوه",
		fmt.Sprintf("ودیعه سفارش شما (%s تومان). مابقی مبلغ پس از تحویل سفارش دریافت می‌شود.", FormatToman(b.cfg.DepositAmount)),
		depositInvoicePayload,
		b.cfg.PaymentProviderToken,
		b.cfg.PaymentCurrency,
		[]bale.LabeledPrice{{Label: "ودیعه سفارش", Amount: amount}},
	)
	if err != nil {
		log.Printf("SendInvoice: %v", err)
		b.api.SendMessage(chatID, "متاسفانه در حال حاضر امکان پرداخت آنلاین وجود ندارد. لطفا کمی بعد دوباره تلاش کنید یا با پشتیبانی تماس بگیرید.", nil)
	}
}

func (b *Bot) finalizeOrder(chatID int64, sess *Session, trigger *bale.Message) {
	if len(sess.Cart) == 0 {
		// Nothing to finalize: most likely a duplicate successful_payment
		// or receipt-photo delivery for an order already completed.
		return
	}

	items := make([]db.OrderItem, 0, len(sess.Cart))
	for _, item := range sess.Cart {
		items = append(items, db.OrderItem{
			FruitID:    item.FruitID,
			Emoji:      item.Emoji,
			Name:       item.Name,
			WeightKg:   item.WeightKg,
			PricePerKg: item.PricePerKg,
		})
	}
	order := db.Order{
		ChatID:  chatID,
		Address: sess.Address,
		Phone:   sess.Phone,
		Items:   items,
		Total:   sess.Total(),
		Deposit: b.cfg.DepositAmount,
	}
	if _, err := b.data.SaveOrder(order); err != nil {
		log.Printf("SaveOrder: %v", err)
	}

	confirmation := "✅ رسید شما دریافت شد. سفارش شما ثبت شد و همکاران ما به زودی جهت هماهنگی نهایی با شما تماس خواهند گرفت.\n\nبا تشکر از خرید شما 🌿"
	if trigger != nil && trigger.SuccessfulPayment != nil {
		confirmation = "✅ پرداخت ودیعه با موفقیت انجام شد و سفارش شما ثبت شد. همکاران ما به زودی جهت هماهنگی نهایی با شما تماس خواهند گرفت.\n\nبا تشکر از خرید شما 🌿"
	}
	b.api.SendMessage(chatID, confirmation, nil)

	if b.cfg.AdminChatID != 0 {
		summary := fmt.Sprintf("📦 سفارش جدید\n\n%s\n\nجمع کل: %s تومان\nودیعه: %s تومان\n📍 آدرس: %s\n📞 تماس: %s",
			cartLinesOnly(sess), FormatToman(sess.Total()), FormatToman(b.cfg.DepositAmount), sess.Address, sess.Phone)
		b.api.SendMessage(b.cfg.AdminChatID, summary, adminMenuKeyboard())
		if trigger != nil {
			b.api.ForwardMessage(b.cfg.AdminChatID, chatID, trigger.MessageID)
		}
	}

	sess.resetOrder()
}

// ---- reply keyboards (persistent, chat-wide "buttons instead of commands") ----

func customerMenuKeyboard() *bale.ReplyKeyboardMarkup {
	return &bale.ReplyKeyboardMarkup{
		Keyboard:       [][]bale.KeyboardButton{{{Text: customerMenuButton}}},
		ResizeKeyboard: true,
	}
}

func adminMenuKeyboard() *bale.ReplyKeyboardMarkup {
	return &bale.ReplyKeyboardMarkup{
		Keyboard:       [][]bale.KeyboardButton{{{Text: adminOrdersButton}}},
		ResizeKeyboard: true,
	}
}

// ---- admin chat ----
//
// Price and minimum-weight changes go through the web admin panel (already
// forms/buttons, not typed commands). This is just a quick, tap-only way for
// the admin to check recent orders from inside the bot chat itself.

func (b *Bot) handleAdminMessage(chatID int64, text string) {
	switch text {
	case adminOrdersButton:
		b.sendRecentOrdersToAdmin(chatID)
	default:
		b.api.SendMessage(chatID, "👋 پنل مدیریت ربات میوه.\nقیمت‌ها و حداقل وزن هر میوه از پنل وب ادمین قابل تغییرند. برای مشاهده سفارش‌های اخیر از دکمه پایین صفحه استفاده کنید.", adminMenuKeyboard())
	}
}

func (b *Bot) sendRecentOrdersToAdmin(chatID int64) {
	orders, err := b.data.ListRecentOrders(10)
	if err != nil {
		log.Printf("ListRecentOrders: %v", err)
		b.api.SendMessage(chatID, "خطا در خواندن سفارش‌ها.", adminMenuKeyboard())
		return
	}
	if len(orders) == 0 {
		b.api.SendMessage(chatID, "هنوز سفارشی ثبت نشده است.", adminMenuKeyboard())
		return
	}

	var sb strings.Builder
	sb.WriteString("📦 آخرین سفارش‌ها:\n\n")
	for _, o := range orders {
		sb.WriteString(fmt.Sprintf("#%d — %s\n", o.ID, o.CreatedAt.Format("2006-01-02 15:04")))
		for _, item := range o.Items {
			sb.WriteString(fmt.Sprintf("  %s %s (%s کیلو)\n", item.Emoji, item.Name, FormatWeight(item.WeightKg)))
		}
		sb.WriteString(fmt.Sprintf("  جمع: %s تومان — ودیعه: %s تومان\n  📍 %s | 📞 %s\n\n", FormatToman(o.Total), FormatToman(o.Deposit), o.Address, o.Phone))
	}
	b.api.SendMessage(chatID, sb.String(), adminMenuKeyboard())
}

func cartLinesOnly(sess *Session) string {
	var sb strings.Builder
	for _, item := range sess.Cart {
		sb.WriteString(fmt.Sprintf("%s %s — %s کیلوگرم — %s تومان\n", item.Emoji, item.Name, FormatWeight(item.WeightKg), FormatToman(item.LineTotal())))
	}
	return strings.TrimRight(sb.String(), "\n")
}
