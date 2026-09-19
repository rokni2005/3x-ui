package bot

import (
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"balebot/bale"
)

const (
	minWeightKg  = 0.5
	maxWeightKg  = 30
	weightStepKg = 0.5
)

// Config holds the deposit/payment details shown to customers.
type Config struct {
	AdminChatID   int64
	DepositAmount int
	CardNumber    string
	CardHolder    string
}

// Bot wires together the Bale API client, session store and business rules
// for the fruit-ordering conversation.
type Bot struct {
	api   *bale.Client
	store *Store
	cfg   Config
}

// New builds a Bot ready to Run.
func New(api *bale.Client, cfg Config) *Bot {
	return &Bot{api: api, store: NewStore(), cfg: cfg}
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

	if u.CallbackQuery != nil {
		b.handleCallback(*u.CallbackQuery)
		return
	}
	if u.Message != nil {
		b.handleMessage(*u.Message)
	}
}

// ---- messages ----

func (b *Bot) handleMessage(msg bale.Message) {
	chatID := msg.Chat.ID
	sess := b.store.Get(chatID)
	text := strings.TrimSpace(msg.Text)

	if text == "/start" {
		sess.resetOrder()
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
			b.finalizeOrder(chatID, sess, msg)
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
	sess := b.store.Get(chatID)
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
		fruit := FindFruit(id)
		if fruit == nil {
			b.api.AnswerCallbackQuery(cq.ID, "این میوه یافت نشد", true)
			return
		}
		sess.CurrentFruit = id
		sess.CurrentWeight = 1
		sess.Stage = StageViewingFruit
		b.api.AnswerCallbackQuery(cq.ID, "", false)
		b.editFruitDetail(chatID, messageID, sess, fruit)

	case data == "w:inc" || data == "w:dec":
		fruit := FindFruit(sess.CurrentFruit)
		if fruit == nil {
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
			if sess.CurrentWeight < minWeightKg {
				sess.CurrentWeight = minWeightKg
			}
		}
		b.api.AnswerCallbackQuery(cq.ID, "", false)
		b.editFruitDetail(chatID, messageID, sess, fruit)

	case strings.HasPrefix(data, "add:"):
		id := strings.TrimPrefix(data, "add:")
		fruit := FindFruit(id)
		if fruit == nil {
			b.api.AnswerCallbackQuery(cq.ID, "این میوه یافت نشد", true)
			return
		}
		sess.AddToCart(id, sess.CurrentWeight)
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
		sess.Stage = StageAwaitingReceipt
		b.api.AnswerCallbackQuery(cq.ID, "", false)
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
	b.api.SendMessage(chatID, catalogText(sess), catalogKeyboard(sess))
}

func (b *Bot) editCatalog(chatID, messageID int64, sess *Session) {
	if err := b.api.EditMessageText(chatID, messageID, catalogText(sess), catalogKeyboard(sess)); err != nil {
		b.api.SendMessage(chatID, catalogText(sess), catalogKeyboard(sess))
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

func catalogKeyboard(sess *Session) *bale.InlineKeyboardMarkup {
	var rows [][]bale.InlineKeyboardButton
	for i := 0; i < len(Catalog); i += 2 {
		row := []bale.InlineKeyboardButton{
			{Text: Catalog[i].Emoji + " " + Catalog[i].Name, CallbackData: "fruit:" + Catalog[i].ID},
		}
		if i+1 < len(Catalog) {
			row = append(row, bale.InlineKeyboardButton{
				Text: Catalog[i+1].Emoji + " " + Catalog[i+1].Name, CallbackData: "fruit:" + Catalog[i+1].ID,
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

func fruitDetailText(sess *Session, fruit *Fruit) string {
	amount := int(sess.CurrentWeight * float64(fruit.Price))
	return fmt.Sprintf(
		"%s %s درجه یک\nقیمت: %s تومان به ازای هر کیلو\n\nمقدار انتخابی: %s کیلوگرم\nمبلغ این قلم: %s تومان",
		fruit.Emoji, fruit.Name, FormatToman(fruit.Price), FormatWeight(sess.CurrentWeight), FormatToman(amount),
	)
}

func fruitDetailKeyboard(sess *Session, fruit *Fruit) *bale.InlineKeyboardMarkup {
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

func (b *Bot) editFruitDetail(chatID, messageID int64, sess *Session, fruit *Fruit) {
	if err := b.api.EditMessageText(chatID, messageID, fruitDetailText(sess, fruit), fruitDetailKeyboard(sess, fruit)); err != nil {
		b.api.SendMessage(chatID, fruitDetailText(sess, fruit), fruitDetailKeyboard(sess, fruit))
	}
}

func cartText(sess *Session) string {
	var sb strings.Builder
	sb.WriteString("🛒 سبد خرید شما:\n\n")
	for _, item := range sess.Cart {
		fruit := FindFruit(item.FruitID)
		if fruit == nil {
			continue
		}
		price := int(item.WeightKg * float64(fruit.Price))
		sb.WriteString(fmt.Sprintf("%s %s — %s کیلوگرم — %s تومان\n", fruit.Emoji, fruit.Name, FormatWeight(item.WeightKg), FormatToman(price)))
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
		fruit := FindFruit(item.FruitID)
		if fruit == nil {
			continue
		}
		price := int(item.WeightKg * float64(fruit.Price))
		sb.WriteString(fmt.Sprintf("%s %s — %s کیلوگرم — %s تومان\n", fruit.Emoji, fruit.Name, FormatWeight(item.WeightKg), FormatToman(price)))
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

func (b *Bot) finalizeOrder(chatID int64, sess *Session, receipt bale.Message) {
	b.api.SendMessage(chatID, "✅ رسید شما دریافت شد. سفارش شما ثبت شد و همکاران ما به زودی جهت هماهنگی نهایی با شما تماس خواهند گرفت.\n\nبا تشکر از خرید شما 🌿", nil)

	if b.cfg.AdminChatID != 0 {
		summary := fmt.Sprintf("📦 سفارش جدید\n\n%s\n\nجمع کل: %s تومان\nودیعه: %s تومان\n📍 آدرس: %s\n📞 تماس: %s",
			cartLinesOnly(sess), FormatToman(sess.Total()), FormatToman(b.cfg.DepositAmount), sess.Address, sess.Phone)
		b.api.SendMessage(b.cfg.AdminChatID, summary, nil)
		b.api.ForwardMessage(b.cfg.AdminChatID, chatID, receipt.MessageID)
	}

	sess.resetOrder()
}

func cartLinesOnly(sess *Session) string {
	var sb strings.Builder
	for _, item := range sess.Cart {
		fruit := FindFruit(item.FruitID)
		if fruit == nil {
			continue
		}
		price := int(item.WeightKg * float64(fruit.Price))
		sb.WriteString(fmt.Sprintf("%s %s — %s کیلوگرم — %s تومان\n", fruit.Emoji, fruit.Name, FormatWeight(item.WeightKg), FormatToman(price)))
	}
	return strings.TrimRight(sb.String(), "\n")
}
