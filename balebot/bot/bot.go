package bot

import (
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"balebot/bale"
	"balebot/db"
)

const (
	weightStepKg = 0.5
	maxWeightKg  = 30

	// depositInvoicePayload/fullInvoicePayload identify which of the two
	// checkout options a payment is for. The deposit amount is a fixed
	// shop-wide setting; the full-payment amount varies per order, so it's
	// looked up from the customer's session (by chat ID) when a
	// pre_checkout_query for it arrives.
	depositInvoicePayload = "deposit"
	fullInvoicePayload    = "full"

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
	var expectedToman int
	switch q.InvoicePayload {
	case depositInvoicePayload:
		expectedToman = b.cfg.DepositAmount
	case fullInvoicePayload:
		expectedToman = b.sessions.Get(q.From.ID).Total()
	default:
		expectedToman = -1 // unknown payload: never matches, always rejected below
	}

	expected := expectedToman * b.cfg.PaymentAmountMultiplier
	if q.TotalAmount != expected {
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

	if msg.Location != nil && sess.AwaitingLocationForOrder != 0 {
		b.handleShipmentLocation(chatID, sess, *msg.Location)
		return
	}

	if msg.SuccessfulPayment != nil {
		b.finalizeOrder(chatID, sess, &msg)
		return
	}

	if text == "/start" || text == customerMenuButton {
		sess.resetOrder()
		customer, err := b.data.GetCustomer(chatID)
		if err != nil {
			log.Printf("GetCustomer(%d): %v", chatID, err)
		}
		if customer == nil || customer.FirstName == "" {
			sess.Stage = StageAwaitingName
			b.api.SendMessage(chatID, "🍇 سلام! به سفارش آنلاین میوه خوش آمدید.\nبرای شروع، لطفا نام و نام خانوادگی خود را وارد کنید:", nil)
			return
		}
		b.api.SendMessage(chatID, "🍇 سلام! به سفارش آنلاین میوه خوش آمدید.\nهر وقت خواستید به این منو برگردید، کافیه دکمه پایین صفحه رو بزنید 👇", customerMenuKeyboard())
		b.sendCatalog(chatID, sess)
		return
	}

	switch sess.Stage {
	case StageAwaitingName:
		if len([]rune(text)) < 3 {
			b.api.SendMessage(chatID, "لطفا نام و نام خانوادگی خود را کامل وارد کنید:", nil)
			return
		}
		parts := strings.SplitN(text, " ", 2)
		firstName := parts[0]
		lastName := ""
		if len(parts) > 1 {
			lastName = strings.TrimSpace(parts[1])
		}
		if err := b.data.UpsertCustomer(chatID, firstName, lastName); err != nil {
			log.Printf("UpsertCustomer(%d): %v", chatID, err)
		}
		sess.Stage = StageBrowsing
		b.api.SendMessage(chatID, fmt.Sprintf("خوش اومدید %s 🌿\nهر وقت خواستید به این منو برگردید، کافیه دکمه پایین صفحه رو بزنید 👇", firstName), customerMenuKeyboard())
		b.sendCatalog(chatID, sess)

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

	case strings.HasPrefix(data, "cart:dec:"):
		id := strings.TrimPrefix(data, "cart:dec:")
		sess.DecreaseInCart(id, weightStepKg)
		b.api.AnswerCallbackQuery(cq.ID, "", false)
		b.editCart(chatID, messageID, sess)

	case strings.HasPrefix(data, "cart:rm:"):
		id := strings.TrimPrefix(data, "cart:rm:")
		sess.RemoveFromCart(id)
		b.api.AnswerCallbackQuery(cq.ID, "حذف شد", false)
		b.editCart(chatID, messageID, sess)

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

	case data == "pay:deposit" || data == "pay:full":
		full := data == "pay:full"
		sess.PayingFull = full
		b.api.AnswerCallbackQuery(cq.ID, "", false)

		amount := b.cfg.DepositAmount
		payload := depositInvoicePayload
		label := "ودیعه سفارش"
		amountDesc := "بابت ودیعه سفارش"
		if full {
			amount = sess.Total()
			payload = fullInvoicePayload
			label = "مبلغ کامل سفارش"
			amountDesc = "بابت کل سفارش"
		}

		if b.cfg.PaymentProviderToken != "" {
			b.sendPaymentInvoice(chatID, payload, amount, label)
			return
		}
		sess.Stage = StageAwaitingReceipt
		text := fmt.Sprintf(
			"لطفا مبلغ %s تومان %s را به شماره کارت زیر واریز کنید:\n\n💳 %s\nبه نام: %s\n\nپس از واریز، لطفا تصویر رسید پرداخت را همینجا ارسال کنید تا سفارش شما نهایی شود.",
			FormatToman(amount), amountDesc, b.cfg.CardNumber, b.cfg.CardHolder,
		)
		b.api.SendMessage(chatID, text, nil)

	case chatID == b.cfg.AdminChatID && strings.HasPrefix(data, "admin:confirm:"):
		b.api.AnswerCallbackQuery(cq.ID, "", false)
		order, err := b.ConfirmOrder(parseOrderID(strings.TrimPrefix(data, "admin:confirm:")))
		if err != nil || order == nil {
			log.Printf("ConfirmOrder: %v", err)
			return
		}
		if err := b.api.EditMessageText(chatID, messageID, adminOrderText(*order), adminOrderKeyboard(*order)); err != nil {
			log.Printf("EditMessageText (confirm order #%d): %v", order.ID, err)
		}

	case chatID == b.cfg.AdminChatID && strings.HasPrefix(data, "admin:ship:"):
		b.api.AnswerCallbackQuery(cq.ID, "", false)
		order, err := b.ShipOrder(parseOrderID(strings.TrimPrefix(data, "admin:ship:")))
		if err != nil || order == nil {
			log.Printf("ShipOrder: %v", err)
			return
		}
		if err := b.api.EditMessageText(chatID, messageID, adminOrderText(*order), adminOrderKeyboard(*order)); err != nil {
			log.Printf("EditMessageText (ship order #%d): %v", order.ID, err)
		}

	default:
		b.api.AnswerCallbackQuery(cq.ID, "", false)
	}
}

func parseOrderID(s string) int64 {
	id, _ := strconv.ParseInt(s, 10, 64)
	return id
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

// cartKeyboard shows a ➖/🗑 pair per cart line (decrease by one step, or
// drop the line entirely) above the usual add-more/checkout buttons.
func cartKeyboard(sess *Session) *bale.InlineKeyboardMarkup {
	var rows [][]bale.InlineKeyboardButton
	for _, item := range sess.Cart {
		rows = append(rows, []bale.InlineKeyboardButton{
			{Text: fmt.Sprintf("➖ کم کردن %s %s", item.Emoji, item.Name), CallbackData: "cart:dec:" + item.FruitID},
			{Text: "🗑 حذف", CallbackData: "cart:rm:" + item.FruitID},
		})
	}
	rows = append(rows,
		[]bale.InlineKeyboardButton{{Text: "➕ افزودن میوه دیگر", CallbackData: "back:menu"}},
		[]bale.InlineKeyboardButton{{Text: "✅ ثبت آدرس و ادامه سفارش", CallbackData: "cart:checkout"}},
	)
	return &bale.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func (b *Bot) editCart(chatID, messageID int64, sess *Session) {
	if err := b.api.EditMessageText(chatID, messageID, cartText(sess), cartKeyboard(sess)); err != nil {
		b.api.SendMessage(chatID, cartText(sess), cartKeyboard(sess))
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
		{{Text: fmt.Sprintf("💰 پرداخت کامل مبلغ (%s تومان)", FormatToman(total)), CallbackData: "pay:full"}},
	}}
	b.api.SendMessage(chatID, sb.String(), keyboard)
}

// sendPaymentInvoice asks Bale to charge the customer's wallet via a native
// invoice, instead of the manual card-transfer flow. payload is either
// depositInvoicePayload or fullInvoicePayload, and amountToman is in Toman
// (converted to the currency's smallest unit via PaymentAmountMultiplier).
func (b *Bot) sendPaymentInvoice(chatID int64, payload string, amountToman int, label string) {
	title := "پرداخت ودیعه سفارش میوه"
	description := fmt.Sprintf("ودیعه سفارش شما (%s تومان). مابقی مبلغ پس از تحویل سفارش دریافت می‌شود.", FormatToman(amountToman))
	if payload == fullInvoicePayload {
		title = "پرداخت سفارش میوه"
		description = fmt.Sprintf("مبلغ کامل سفارش شما (%s تومان).", FormatToman(amountToman))
	}

	amount := amountToman * b.cfg.PaymentAmountMultiplier
	_, err := b.api.SendInvoice(
		chatID, title, description, payload,
		b.cfg.PaymentProviderToken, b.cfg.PaymentCurrency,
		[]bale.LabeledPrice{{Label: label, Amount: amount}},
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
	total := sess.Total()
	paid := b.cfg.DepositAmount
	if sess.PayingFull {
		paid = total
	}
	if paid > total {
		paid = total
	}
	remaining := total - paid

	order := db.Order{
		ChatID:  chatID,
		Address: sess.Address,
		Phone:   sess.Phone,
		Items:   items,
		Total:   total,
		Deposit: paid,
	}
	orderID, err := b.data.SaveOrder(order)
	if err != nil {
		log.Printf("SaveOrder: %v", err)
	}
	order.ID = orderID
	order.Status = db.StatusPending

	if remaining > 0 {
		if err := b.data.AddToWalletDebt(chatID, remaining); err != nil {
			log.Printf("AddToWalletDebt(%d): %v", chatID, err)
		}
	}

	confirmation := "✅ رسید شما دریافت شد. سفارش شما ثبت شد و همکاران ما به زودی جهت هماهنگی نهایی با شما تماس خواهند گرفت.\n\nبا تشکر از خرید شما 🌿"
	if trigger != nil && trigger.SuccessfulPayment != nil {
		confirmation = "✅ پرداخت با موفقیت انجام شد و سفارش شما ثبت شد. همکاران ما به زودی جهت هماهنگی نهایی با شما تماس خواهند گرفت.\n\nبا تشکر از خرید شما 🌿"
	}
	if remaining > 0 {
		confirmation += fmt.Sprintf("\n\nمبلغ باقی‌مانده (%s تومان) پس از تحویل سفارش دریافت می‌شود.", FormatToman(remaining))
	}
	b.api.SendMessage(chatID, confirmation, nil)

	if b.cfg.AdminChatID != 0 {
		if customer, err := b.data.GetCustomer(chatID); err != nil {
			log.Printf("GetCustomer(%d): %v", chatID, err)
		} else if customer != nil {
			order.CustomerName = customer.FullName()
		}
		b.api.SendMessage(b.cfg.AdminChatID, adminOrderText(order), adminOrderKeyboard(order))
		// Only the manual card-receipt flow has anything worth forwarding
		// (the photo itself); a successful_payment update carries no
		// forwardable message of its own.
		if trigger != nil && len(trigger.Photo) > 0 {
			b.api.ForwardMessage(b.cfg.AdminChatID, chatID, trigger.MessageID)
		}
	}

	sess.resetOrder()
}

// adminOrderText renders one order as a message for the admin, in the bot
// chat itself (used both for the new-order notification and the
// "recent orders" listing) so the admin can act on it without needing the
// web panel.
func adminOrderText(o db.Order) string {
	name := o.CustomerName
	if name == "" {
		name = "—"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📦 سفارش #%d — %s\n👤 %s\n\n", o.ID, o.StatusLabel(), name))
	for _, item := range o.Items {
		sb.WriteString(fmt.Sprintf("%s %s — %s کیلوگرم\n", item.Emoji, item.Name, FormatWeight(item.WeightKg)))
	}
	sb.WriteString(fmt.Sprintf("\nجمع کل: %s تومان\nپرداخت‌شده: %s تومان", FormatToman(o.Total), FormatToman(o.Deposit)))
	if o.Remaining() > 0 {
		sb.WriteString(fmt.Sprintf("\nباقی‌مانده (دریافت هنگام تحویل): %s تومان", FormatToman(o.Remaining())))
	}
	sb.WriteString(fmt.Sprintf("\n📍 %s\n📞 %s", o.Address, o.Phone))
	if o.HasLocation() {
		sb.WriteString(fmt.Sprintf("\n📍 لوکیشن پیک: https://www.google.com/maps?q=%f,%f", *o.CustomerLat, *o.CustomerLng))
	}
	return sb.String()
}

// adminOrderKeyboard returns the one action button relevant to an order's
// current status (nil once it's shipped — nothing left to do from chat).
func adminOrderKeyboard(o db.Order) *bale.InlineKeyboardMarkup {
	switch o.Status {
	case db.StatusPending:
		return &bale.InlineKeyboardMarkup{InlineKeyboard: [][]bale.InlineKeyboardButton{
			{{Text: "✅ تایید سفارش", CallbackData: fmt.Sprintf("admin:confirm:%d", o.ID)}},
		}}
	case db.StatusConfirmed:
		return &bale.InlineKeyboardMarkup{InlineKeyboard: [][]bale.InlineKeyboardButton{
			{{Text: "🚚 ارسال شد (درخواست لوکیشن از مشتری)", CallbackData: fmt.Sprintf("admin:ship:%d", o.ID)}},
		}}
	default:
		return nil
	}
}

// ConfirmOrder marks an order confirmed and notifies the customer. Called
// both from the admin web panel and from the admin's own bot-chat button.
func (b *Bot) ConfirmOrder(orderID int64) (*db.Order, error) {
	order, err := b.data.GetOrder(orderID)
	if err != nil || order == nil {
		return order, err
	}
	if err := b.data.UpdateOrderStatus(orderID, db.StatusConfirmed); err != nil {
		return order, err
	}
	order.Status = db.StatusConfirmed
	b.NotifyOrderConfirmed(order.ChatID, orderID)
	return order, nil
}

// ShipOrder marks an order shipped and asks the customer for their live
// location. Called both from the admin web panel and from the admin's own
// bot-chat button.
func (b *Bot) ShipOrder(orderID int64) (*db.Order, error) {
	order, err := b.data.GetOrder(orderID)
	if err != nil || order == nil {
		return order, err
	}
	if err := b.data.UpdateOrderStatus(orderID, db.StatusShipped); err != nil {
		return order, err
	}
	order.Status = db.StatusShipped
	b.RequestShipmentLocation(order.ChatID, orderID)
	return order, nil
}

// ---- admin-panel-triggered notifications ----
//
// The admin web panel calls these when the admin confirms an order or marks
// it shipped, so the customer hears about it inside the same Bale chat.

// NotifyOrderConfirmed tells a customer their order has been checked and confirmed.
func (b *Bot) NotifyOrderConfirmed(chatID, orderID int64) {
	b.api.SendMessage(chatID, fmt.Sprintf("✅ سفارش شما (#%d) بررسی و تایید شد. به‌زودی برای ارسال آماده می‌شود.", orderID), nil)
}

// RequestShipmentLocation tells a customer their order is out for delivery
// and asks them to share their live location, so the admin can hand it to
// a courier with the right destination. The customer's next location
// message is picked up in handleMessage via Session.AwaitingLocationForOrder.
func (b *Bot) RequestShipmentLocation(chatID, orderID int64) {
	sess := b.sessions.Get(chatID)
	sess.AwaitingLocationForOrder = orderID
	b.api.SendMessage(chatID,
		fmt.Sprintf("🚚 سفارش شما (#%d) برای ارسال آماده شد!\nلطفا برای هماهنگی دقیق پیک، روی دکمه پایین بزنید تا لوکیشن فعلی‌تان برای ما ارسال شود:", orderID),
		locationRequestKeyboard(),
	)
}

func locationRequestKeyboard() *bale.ReplyKeyboardMarkup {
	return &bale.ReplyKeyboardMarkup{
		Keyboard:       [][]bale.KeyboardButton{{{Text: "📍 ارسال لوکیشن من", RequestLocation: true}}},
		ResizeKeyboard: true,
	}
}

// handleShipmentLocation saves the customer's shared location against the
// order the admin marked shipped, and lets both sides know it arrived.
func (b *Bot) handleShipmentLocation(chatID int64, sess *Session, loc bale.Location) {
	orderID := sess.AwaitingLocationForOrder
	sess.AwaitingLocationForOrder = 0

	if err := b.data.SaveOrderLocation(orderID, loc.Latitude, loc.Longitude); err != nil {
		log.Printf("SaveOrderLocation(%d): %v", orderID, err)
	}
	b.api.SendMessage(chatID, "📍 لوکیشن شما دریافت شد. سفارش شما به‌زودی توسط پیک تحویل داده می‌شود 🙏", customerMenuKeyboard())

	if b.cfg.AdminChatID != 0 {
		mapLink := fmt.Sprintf("https://www.google.com/maps?q=%f,%f", loc.Latitude, loc.Longitude)
		b.api.SendMessage(b.cfg.AdminChatID,
			fmt.Sprintf("📍 لوکیشن مشتری برای سفارش #%d رسید:\n%s\n\nاین مختصات رو می‌تونید توی پنل ادمین (بخش سفارش‌ها) هم ببینید و از اونجا برای باز کردن اسنپ‌باکس استفاده کنید.", orderID, mapLink),
			adminMenuKeyboard(),
		)
	}
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
	case adminOrdersButton, "/orders":
		b.sendRecentOrdersToAdmin(chatID)
	case "/stats":
		b.sendStatsToAdmin(chatID)
	default:
		b.api.SendMessage(chatID, "👋 پنل مدیریت ربات میوه.\nبرای دیدن و تایید/ارسال سفارش‌های اخیر از دکمه پایین صفحه استفاده کنید. قیمت‌ها، حداقل وزن و کیف‌پول مشتری‌ها از پنل وب ادمین قابل تغییرند.", adminMenuKeyboard())
	}
}

// sendRecentOrdersToAdmin sends each recent order as its own message with
// an inline "✅ تایید سفارش" / "🚚 ارسال شد" button matching its current
// status, so the admin can act on orders directly from the Bale chat
// without needing the web panel.
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
	for _, o := range orders {
		b.api.SendMessage(chatID, adminOrderText(o), adminOrderKeyboard(o))
	}
}

func (b *Bot) sendStatsToAdmin(chatID int64) {
	st, err := b.data.Stats()
	if err != nil {
		log.Printf("Stats: %v", err)
		b.api.SendMessage(chatID, "خطا در خواندن آمار.", adminMenuKeyboard())
		return
	}
	text := fmt.Sprintf(
		"📊 آمار سفارش‌ها\n\nکل سفارش‌ها: %d\nجمع کل فروش: %s تومان\nجمع مبالغ دریافتی: %s تومان\n\n⏳ در انتظار تایید: %d\n✅ تایید شده: %d\n🚚 ارسال شده: %d",
		st.TotalOrders, FormatToman(st.TotalRevenue), FormatToman(st.TotalDeposits), st.PendingCount, st.ConfirmedCount, st.ShippedCount,
	)
	b.api.SendMessage(chatID, text, adminMenuKeyboard())
}
