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
	customerMenuButton  = "🍉 منو و سفارش میوه"
	adminOrdersButton   = "📦 سفارش‌های اخیر"
	adminStatsButton    = "📊 آمار"
	adminWalletsButton  = "👛 کیف‌پول‌های بدهکار"
	adminFruitsButton   = "🍉 قیمت میوه‌ها"

	// supportChatURL opens a direct chat with support on Bale. This is a
	// best-effort profile link (mirrors Telegram's t.me/<username> scheme,
	// which Bale's own links at ble.ir/<username> follow) — if it doesn't
	// resolve correctly, the actual Bale username should be swapped in
	// here instead of the phone-number-shaped id.
	supportChatURL = "https://ble.ir/9104304566"

	// Delivery fee: free for orders at or above the threshold, a flat fee
	// below it. Both are in Toman, applied to the cart's item subtotal
	// (before the fee itself).
	freeDeliveryThreshold = 1000000
	deliveryFeeAmount     = 120000
)

// supportButtonRow is appended to every keyboard shown during the ordering
// flow, so a customer can always reach support with one tap.
func supportButtonRow() []bale.InlineKeyboardButton {
	return []bale.InlineKeyboardButton{{Text: "🆘 پشتیبانی", URL: supportChatURL}}
}

// deliveryFeeFor returns the delivery fee for a cart whose items add up to
// itemsTotal Toman.
func deliveryFeeFor(itemsTotal int) int {
	if itemsTotal >= freeDeliveryThreshold {
		return 0
	}
	return deliveryFeeAmount
}

// orderGrandTotal is what the customer actually owes: the cart's item
// subtotal plus delivery.
func orderGrandTotal(sess *Session) int {
	itemsTotal := sess.Total()
	return itemsTotal + deliveryFeeFor(itemsTotal)
}

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
		expectedToman = orderGrandTotal(b.sessions.Get(q.From.ID))
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
			b.api.SendMessage(chatID, "لطفا آدرس کامل و دقیق خود را وارد کنید (حداقل شامل خیابان و پلاک):", nil)
			return
		}
		if sess.Neighborhood != "" {
			sess.Address = sess.Neighborhood + "، " + text
		} else {
			sess.Address = text
		}
		if lastPhone := b.lastKnownPhone(chatID); lastPhone != "" {
			sess.Stage = StageAwaitingPhone
			b.api.SendMessage(chatID, fmt.Sprintf("ممنون 🙏\nشماره تماس قبلی شما: %s\nهمین شماره تحویل‌گیرنده‌ی این سفارشه؟", lastPhone), phoneChoiceKeyboard())
			return
		}
		sess.Stage = StageAwaitingPhone
		b.api.SendMessage(chatID, "ممنون 🙏\nلطفا شماره تماس خود را وارد کنید:", nil)

	case StageAwaitingPhone:
		phone := normalizeDigits(text)
		if !isValidPhone(phone) {
			b.api.SendMessage(chatID, "شماره تماس معتبر نیست. لطفا یک شماره موبایل صحیح وارد کنید (مثال: 09121234567):", nil)
			return
		}
		sess.Phone = phone
		sess.Stage = StageInvoice
		if err := b.data.SaveAddress(chatID, sess.Address, sess.Phone); err != nil {
			log.Printf("SaveAddress(%d): %v", chatID, err)
		}
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

// normalizeDigits converts Persian (۰-۹) and Arabic-Indic (٠-٩) digits, as
// typed by many mobile keyboards set to Persian, into plain ASCII digits.
func normalizeDigits(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= '۰' && r <= '۹':
			return '0' + (r - '۰')
		case r >= '٠' && r <= '٩':
			return '0' + (r - '٠')
		default:
			return r
		}
	}, s)
}

func isValidPhone(text string) bool {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' || r == '+' {
			return r
		}
		return -1
	}, normalizeDigits(text))
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
		b.api.AnswerCallbackQuery(cq.ID, "", false)
		addresses, err := b.data.ListAddresses(chatID)
		if err != nil {
			log.Printf("ListAddresses(%d): %v", chatID, err)
		}
		if len(addresses) > 0 {
			b.api.SendMessage(chatID, "یکی از آدرس‌های قبلی‌تون رو انتخاب کنید یا آدرس جدید وارد کنید:", addressChoiceKeyboard(addresses))
			return
		}
		sess.Stage = StageChoosingNeighborhood
		b.api.SendMessage(chatID, neighborhoodPrompt, neighborhoodKeyboard())

	case strings.HasPrefix(data, "addr:use:"):
		addr, err := b.data.GetAddress(parseInt64(strings.TrimPrefix(data, "addr:use:")))
		if err != nil || addr == nil || addr.ChatID != chatID {
			b.api.AnswerCallbackQuery(cq.ID, "آدرس یافت نشد", true)
			return
		}
		sess.Address = addr.Address
		sess.Phone = addr.Phone
		sess.Stage = StageInvoice
		b.api.AnswerCallbackQuery(cq.ID, "", false)
		b.sendInvoice(chatID, sess)

	case data == "addr:new":
		sess.Stage = StageChoosingNeighborhood
		b.api.AnswerCallbackQuery(cq.ID, "", false)
		b.api.SendMessage(chatID, neighborhoodPrompt, neighborhoodKeyboard())

	case strings.HasPrefix(data, "hood:"):
		name, ok := neighborhoodByID[strings.TrimPrefix(data, "hood:")]
		if !ok {
			b.api.AnswerCallbackQuery(cq.ID, "", false)
			return
		}
		sess.Neighborhood = name
		sess.Stage = StageAwaitingAddress
		b.api.AnswerCallbackQuery(cq.ID, fmt.Sprintf("محله %s انتخاب شد", name), false)
		b.api.SendMessage(chatID, fmt.Sprintf("محله «%s» ثبت شد ✅\nحالا لطفا آدرس کامل خود را وارد کنید (خیابان، کوچه، پلاک):", name), nil)

	case data == "phone:reuse":
		phone := b.lastKnownPhone(chatID)
		if phone == "" {
			b.api.AnswerCallbackQuery(cq.ID, "", false)
			return
		}
		sess.Phone = phone
		sess.Stage = StageInvoice
		b.api.AnswerCallbackQuery(cq.ID, "", false)
		if err := b.data.SaveAddress(chatID, sess.Address, sess.Phone); err != nil {
			log.Printf("SaveAddress(%d): %v", chatID, err)
		}
		b.sendInvoice(chatID, sess)

	case data == "phone:new":
		sess.Stage = StageAwaitingPhone
		b.api.AnswerCallbackQuery(cq.ID, "", false)
		b.api.SendMessage(chatID, "لطفا شماره تماس تحویل‌گیرنده را وارد کنید:", nil)

	case data == "pay:deposit" || data == "pay:full":
		full := data == "pay:full"
		sess.PayingFull = full
		b.api.AnswerCallbackQuery(cq.ID, "", false)

		amount := b.cfg.DepositAmount
		payload := depositInvoicePayload
		label := "ودیعه سفارش"
		if full {
			amount = orderGrandTotal(sess)
			payload = fullInvoicePayload
			label = "مبلغ کامل سفارش"
		}

		// Online wallet payment: Bale confirms the exact amount via
		// successful_payment, so the draft is marked verified right away.
		if _, err := b.createDraftOrder(chatID, sess, amount, true); err != nil {
			log.Printf("CreateDraftOrder(%d): %v", chatID, err)
		}
		b.sendPaymentInvoice(chatID, payload, amount, label)

	case data == "pay:manual":
		b.api.AnswerCallbackQuery(cq.ID, "", false)
		// Manual card-to-card transfer: nothing is confirmed until the
		// admin reviews the receipt photo, so record it unverified with
		// nothing paid yet — the whole order total counts as owed until
		// then (see db.Order.Remaining / ConfirmManualPayment).
		if _, err := b.createDraftOrder(chatID, sess, 0, false); err != nil {
			log.Printf("CreateDraftOrder(%d): %v", chatID, err)
		}
		sess.Stage = StageAwaitingReceipt
		text := fmt.Sprintf(
			"لطفا مبلغ %s تومان بابت ودیعه سفارش را به شماره کارت زیر واریز کنید:\n\n💳 %s\nبه نام: %s\n\nپس از واریز، لطفا تصویر رسید پرداخت را همینجا ارسال کنید. سفارش شما ثبت می‌شود و پس از بررسی فیش توسط ادمین نهایی خواهد شد.",
			FormatToman(b.cfg.DepositAmount), b.cfg.CardNumber, b.cfg.CardHolder,
		)
		b.api.SendMessage(chatID, text, nil)

	case chatID == b.cfg.AdminChatID && strings.HasPrefix(data, "admin:confirm:"):
		b.api.AnswerCallbackQuery(cq.ID, "", false)
		order, err := b.ConfirmOrder(parseInt64(strings.TrimPrefix(data, "admin:confirm:")))
		if err != nil || order == nil {
			log.Printf("ConfirmOrder: %v", err)
			return
		}
		if err := b.api.EditMessageText(chatID, messageID, adminOrderText(*order), adminOrderKeyboard(*order)); err != nil {
			log.Printf("EditMessageText (confirm order #%d): %v", order.ID, err)
		}

	case chatID == b.cfg.AdminChatID && strings.HasPrefix(data, "admin:ship:"):
		b.api.AnswerCallbackQuery(cq.ID, "", false)
		order, err := b.ShipOrder(parseInt64(strings.TrimPrefix(data, "admin:ship:")))
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

func parseInt64(s string) int64 {
	id, _ := strconv.ParseInt(s, 10, 64)
	return id
}

// lastKnownPhone returns the phone number from a customer's most recently
// saved address, or "" if they have none yet.
func (b *Bot) lastKnownPhone(chatID int64) string {
	addresses, err := b.data.ListAddresses(chatID)
	if err != nil || len(addresses) == 0 {
		return ""
	}
	return addresses[0].Phone
}

// addressChoiceKeyboard lets a returning customer pick a previously used
// address (which carries its phone number along with it) instead of typing
// it again, or start a fresh one.
func addressChoiceKeyboard(addresses []db.Address) *bale.InlineKeyboardMarkup {
	var rows [][]bale.InlineKeyboardButton
	for _, a := range addresses {
		label := []rune(a.Address)
		if len(label) > 40 {
			label = label[:40]
		}
		rows = append(rows, []bale.InlineKeyboardButton{
			{Text: fmt.Sprintf("📍 %s… (%s)", string(label), a.Phone), CallbackData: fmt.Sprintf("addr:use:%d", a.ID)},
		})
	}
	rows = append(rows, []bale.InlineKeyboardButton{{Text: "➕ آدرس جدید", CallbackData: "addr:new"}})
	return &bale.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func phoneChoiceKeyboard() *bale.InlineKeyboardMarkup {
	return &bale.InlineKeyboardMarkup{InlineKeyboard: [][]bale.InlineKeyboardButton{
		{{Text: "✅ بله، همین شماره", CallbackData: "phone:reuse"}},
		{{Text: "✏️ گیرنده شخص دیگریه (شماره جدید)", CallbackData: "phone:new"}},
	}}
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
	rows = append(rows, supportButtonRow())
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
		supportButtonRow(),
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
	itemsTotal := sess.Total()
	fee := deliveryFeeFor(itemsTotal)
	sb.WriteString(fmt.Sprintf("\nجمع اقلام: %s تومان\n", FormatToman(itemsTotal)))
	if fee > 0 {
		sb.WriteString(fmt.Sprintf("🚚 هزینه پیک: %s تومان (سفارش‌های بالای %s تومان پیک رایگان دارند)\n", FormatToman(fee), FormatToman(freeDeliveryThreshold)))
	} else {
		sb.WriteString("🚚 هزینه پیک: رایگان 🎉\n")
	}
	sb.WriteString(fmt.Sprintf("جمع کل: %s تومان", FormatToman(itemsTotal+fee)))
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
		supportButtonRow(),
	)
	return &bale.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func (b *Bot) editCart(chatID, messageID int64, sess *Session) {
	if err := b.api.EditMessageText(chatID, messageID, cartText(sess), cartKeyboard(sess)); err != nil {
		b.api.SendMessage(chatID, cartText(sess), cartKeyboard(sess))
	}
}

func (b *Bot) sendInvoice(chatID int64, sess *Session) {
	itemsTotal := sess.Total()
	fee := deliveryFeeFor(itemsTotal)
	total := itemsTotal + fee
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
	sb.WriteString(fmt.Sprintf("\nجمع اقلام: %s تومان\n", FormatToman(itemsTotal)))
	if fee > 0 {
		sb.WriteString(fmt.Sprintf("🚚 هزینه پیک: %s تومان\n", FormatToman(fee)))
	} else {
		sb.WriteString("🚚 هزینه پیک: رایگان 🎉\n")
	}
	sb.WriteString(fmt.Sprintf("جمع کل: %s تومان\n\n", FormatToman(total)))
	sb.WriteString(fmt.Sprintf("برای ثبت نهایی سفارش، مبلغ %s تومان بابت ودیعه پرداخت می‌شود و مابقی مبلغ (%s تومان) پس از تحویل سفارش دریافت خواهد شد.\n\n", FormatToman(deposit), FormatToman(remaining)))
	sb.WriteString(fmt.Sprintf("📍 آدرس: %s\n📞 شماره تماس: %s", sess.Address, sess.Phone))

	var rows [][]bale.InlineKeyboardButton
	if b.cfg.PaymentProviderToken != "" {
		rows = append(rows,
			[]bale.InlineKeyboardButton{{Text: fmt.Sprintf("💳 پرداخت ودیعه با کیف‌پول (%s تومان)", FormatToman(deposit)), CallbackData: "pay:deposit"}},
			[]bale.InlineKeyboardButton{{Text: fmt.Sprintf("💰 پرداخت کامل با کیف‌پول (%s تومان)", FormatToman(total)), CallbackData: "pay:full"}},
		)
	}
	rows = append(rows,
		[]bale.InlineKeyboardButton{{Text: fmt.Sprintf("🏦 واریز کارت‌به‌کارت (ودیعه %s تومان)", FormatToman(deposit)), CallbackData: "pay:manual"}},
		supportButtonRow(),
	)
	b.api.SendMessage(chatID, sb.String(), &bale.InlineKeyboardMarkup{InlineKeyboard: rows})
}

// neighborhoods lists the north-Tehran areas we currently deliver to. The
// customer picks one at checkout instead of typing it, and it's prefixed
// onto whatever street/plate address they type next.
var neighborhoods = []struct{ ID, Name string }{
	{"niavaran", "نیاوران"},
	{"velenjak", "ولنجک"},
	{"farmaniyeh", "فرمانیه"},
	{"aghdasiyeh", "اقدسیه"},
	{"kamraniyeh", "کامرانیه"},
	{"zafaraniyeh", "زعفرانیه"},
	{"jordan", "جردن"},
	{"fereshteh", "فرشته"},
	{"elahiyeh", "الهیه"},
	{"pasdaran", "پاسداران"},
	{"dorous", "دروس"},
}

var neighborhoodByID = func() map[string]string {
	m := make(map[string]string, len(neighborhoods))
	for _, n := range neighborhoods {
		m[n.ID] = n.Name
	}
	return m
}()

const neighborhoodPrompt = "لطفا محله خود را انتخاب کنید:\n\n⚠️ در حال حاضر خدمات فقط در تهران و محله‌های فوق ارائه می‌شود."

func neighborhoodKeyboard() *bale.InlineKeyboardMarkup {
	var rows [][]bale.InlineKeyboardButton
	for i := 0; i < len(neighborhoods); i += 2 {
		row := []bale.InlineKeyboardButton{{Text: neighborhoods[i].Name, CallbackData: "hood:" + neighborhoods[i].ID}}
		if i+1 < len(neighborhoods) {
			row = append(row, bale.InlineKeyboardButton{Text: neighborhoods[i+1].Name, CallbackData: "hood:" + neighborhoods[i+1].ID})
		}
		rows = append(rows, row)
	}
	rows = append(rows, supportButtonRow())
	return &bale.InlineKeyboardMarkup{InlineKeyboard: rows}
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

// createDraftOrder snapshots the customer's cart/address/phone to the
// database the moment they commit to a payment method, so the checkout
// survives a bot restart even though the in-memory session cart doesn't.
// paidAmount/verified describe what's confirmed so far: the full amount and
// true for an online wallet payment, or 0 and false for an unverified
// card-to-card receipt.
func (b *Bot) createDraftOrder(chatID int64, sess *Session, paidAmount int, verified bool) (int64, error) {
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
	return b.data.CreateDraftOrder(db.Order{
		ChatID:          chatID,
		Address:         sess.Address,
		Phone:           sess.Phone,
		Items:           items,
		Total:           orderGrandTotal(sess),
		Deposit:         paidAmount,
		PaymentVerified: verified,
	})
}

// finalizeOrder is called once a payment actually completes (a wallet
// successful_payment) or a card-to-card receipt photo arrives. It reads the
// order back from the draft created at checkout time (createDraftOrder)
// rather than trusting the in-memory session, so a bot restart between
// "customer tapped pay" and "payment/receipt arrived" can't silently lose
// the order the way relying on sess.Cart alone would.
func (b *Bot) finalizeOrder(chatID int64, sess *Session, trigger *bale.Message) {
	draft, err := b.data.GetLatestDraftOrder(chatID)
	if err != nil {
		log.Printf("GetLatestDraftOrder(%d): %v", chatID, err)
	}

	var order db.Order
	if draft != nil {
		if err := b.data.UpdateOrderStatus(draft.ID, db.StatusPending); err != nil {
			log.Printf("UpdateOrderStatus(%d, pending): %v", draft.ID, err)
		}
		order = *draft
		order.Status = db.StatusPending
	} else {
		// No draft on record (e.g. bot restarted before the draft could be
		// written, or this is a stray duplicate delivery) — fall back to
		// whatever the session still has, same as before this existed.
		if len(sess.Cart) == 0 {
			return
		}
		items := make([]db.OrderItem, 0, len(sess.Cart))
		for _, item := range sess.Cart {
			items = append(items, db.OrderItem{
				FruitID: item.FruitID, Emoji: item.Emoji, Name: item.Name,
				WeightKg: item.WeightKg, PricePerKg: item.PricePerKg,
			})
		}
		total := orderGrandTotal(sess)
		paid := b.cfg.DepositAmount
		if sess.PayingFull {
			paid = total
		}
		if paid > total {
			paid = total
		}
		order = db.Order{
			ChatID: chatID, Address: sess.Address, Phone: sess.Phone,
			Items: items, Total: total, Deposit: paid, PaymentVerified: true,
		}
		id, err := b.data.SaveOrder(order)
		if err != nil {
			log.Printf("SaveOrder: %v", err)
		}
		order.ID = id
		order.Status = db.StatusPending
	}

	remaining := order.Remaining()
	if remaining > 0 {
		if err := b.data.AddToWalletDebt(chatID, remaining); err != nil {
			log.Printf("AddToWalletDebt(%d): %v", chatID, err)
		}
	}

	var confirmation string
	switch {
	case !order.PaymentVerified:
		confirmation = "✅ رسید شما دریافت شد. سفارش شما ثبت شد و پس از بررسی فیش واریزی توسط ادمین نهایی می‌شود. همکاران ما به زودی جهت هماهنگی نهایی با شما تماس خواهند گرفت.\n\nبا تشکر از خرید شما 🌿"
	case trigger != nil && trigger.SuccessfulPayment != nil:
		confirmation = "✅ پرداخت با موفقیت انجام شد و سفارش شما ثبت شد. همکاران ما به زودی جهت هماهنگی نهایی با شما تماس خواهند گرفت.\n\nبا تشکر از خرید شما 🌿"
	default:
		confirmation = "✅ رسید شما دریافت شد. سفارش شما ثبت شد و همکاران ما به زودی جهت هماهنگی نهایی با شما تماس خواهند گرفت.\n\nبا تشکر از خرید شما 🌿"
	}
	if order.PaymentVerified && remaining > 0 {
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
	sb.WriteString(fmt.Sprintf("\nجمع اقلام: %s تومان", FormatToman(o.ItemsTotal())))
	if fee := o.DeliveryFee(); fee > 0 {
		sb.WriteString(fmt.Sprintf("\nهزینه پیک: %s تومان", FormatToman(fee)))
	} else {
		sb.WriteString("\nهزینه پیک: رایگان")
	}
	sb.WriteString(fmt.Sprintf("\nجمع کل: %s تومان", FormatToman(o.Total)))
	if !o.PaymentVerified {
		sb.WriteString("\n⚠️ فیش کارت‌به‌کارت در انتظار بررسیه — مبلغ واریزی رو از پنل وب (بخش سفارش‌ها) ثبت کنید.")
	} else {
		sb.WriteString(fmt.Sprintf("\nپرداخت‌شده: %s تومان", FormatToman(o.Deposit)))
		if o.Remaining() > 0 {
			sb.WriteString(fmt.Sprintf("\nباقی‌مانده (دریافت هنگام تحویل): %s تومان", FormatToman(o.Remaining())))
		}
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
		Keyboard: [][]bale.KeyboardButton{
			{{Text: adminOrdersButton}, {Text: adminStatsButton}},
			{{Text: adminWalletsButton}, {Text: adminFruitsButton}},
		},
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
	case adminStatsButton, "/stats":
		b.sendStatsToAdmin(chatID)
	case adminWalletsButton, "/wallets":
		b.sendWalletsToAdmin(chatID)
	case adminFruitsButton, "/fruits":
		b.sendFruitsToAdmin(chatID)
	case "/start":
		b.api.SendMessage(chatID, "👋 پنل مدیریت ربات میوه.\nاز دکمه‌های پایین صفحه استفاده کنید.", adminMenuKeyboard())
	default:
		b.api.SendMessage(chatID, "متوجه نشدم 🙏 از دکمه‌های پایین صفحه استفاده کنید.", adminMenuKeyboard())
	}
}

func (b *Bot) sendWalletsToAdmin(chatID int64) {
	customers, err := b.data.ListCustomersWithDebt()
	if err != nil {
		log.Printf("ListCustomersWithDebt: %v", err)
		b.api.SendMessage(chatID, "خطا در خواندن کیف‌پول‌ها.", adminMenuKeyboard())
		return
	}
	if len(customers) == 0 {
		b.api.SendMessage(chatID, "هیچ مشتری‌ای در حال حاضر بدهی باقی‌مانده ندارد.", adminMenuKeyboard())
		return
	}
	var sb strings.Builder
	sb.WriteString("👛 کیف‌پول‌های بدهکار:\n\n")
	for _, c := range customers {
		sb.WriteString(fmt.Sprintf("👤 %s (chat id: %d)\n   بدهی: %s تومان\n\n", c.FullName(), c.ChatID, FormatToman(c.WalletDebt)))
	}
	sb.WriteString("برای صفر کردن یا ویرایش، از پنل وب ادمین (بخش کیف‌پول) استفاده کنید.")
	b.api.SendMessage(chatID, sb.String(), adminMenuKeyboard())
}

func (b *Bot) sendFruitsToAdmin(chatID int64) {
	fruits, err := b.data.ListFruits()
	if err != nil {
		log.Printf("ListFruits: %v", err)
		b.api.SendMessage(chatID, "خطا در خواندن لیست میوه‌ها.", adminMenuKeyboard())
		return
	}
	var sb strings.Builder
	sb.WriteString("🍉 قیمت میوه‌ها:\n\n")
	for _, f := range fruits {
		sb.WriteString(fmt.Sprintf("%s %s — %s تومان/کیلو (حداقل %s کیلو)\n", f.Emoji, f.Name, FormatToman(f.Price), FormatWeight(f.MinWeightKg)))
	}
	sb.WriteString("\nبرای ویرایش قیمت، عکس یا افزودن/حذف میوه، از پنل وب ادمین استفاده کنید.")
	b.api.SendMessage(chatID, sb.String(), adminMenuKeyboard())
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
