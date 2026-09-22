package bale

// User represents a Bale end user or bot account.
type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

// Chat represents the chat a message belongs to.
type Chat struct {
	ID int64 `json:"id"`
}

// PhotoSize is one resolution of a photo sent by a user.
type PhotoSize struct {
	FileID string `json:"file_id"`
}

// Location is a geographic point shared by a user, e.g. in response to a
// request_location reply-keyboard button.
type Location struct {
	Longitude float64 `json:"longitude"`
	Latitude  float64 `json:"latitude"`
}

// Message is a Bale message, mirroring the Telegram-compatible Bot API.
type Message struct {
	MessageID         int64              `json:"message_id"`
	From              *User              `json:"from"`
	Chat              Chat               `json:"chat"`
	Text              string             `json:"text"`
	Photo             []PhotoSize        `json:"photo"`
	Location          *Location          `json:"location,omitempty"`
	SuccessfulPayment *SuccessfulPayment `json:"successful_payment,omitempty"`
}

// CallbackQuery is fired when a user taps an inline keyboard button.
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

// LabeledPrice is one price line of an invoice sent via sendInvoice.
type LabeledPrice struct {
	Label  string `json:"label"`
	Amount int    `json:"amount"`
}

// PreCheckoutQuery arrives right before Bale charges the customer; it must
// be answered (via AnswerPreCheckoutQuery) within a short deadline.
type PreCheckoutQuery struct {
	ID             string `json:"id"`
	From           User   `json:"from"`
	Currency       string `json:"currency"`
	TotalAmount    int    `json:"total_amount"`
	InvoicePayload string `json:"invoice_payload"`
}

// SuccessfulPayment is attached to the message Bale sends once a payment
// completes.
type SuccessfulPayment struct {
	Currency                string `json:"currency"`
	TotalAmount             int    `json:"total_amount"`
	InvoicePayload          string `json:"invoice_payload"`
	ProviderPaymentChargeID string `json:"provider_payment_charge_id,omitempty"`
}

// Update is a single item returned by getUpdates.
type Update struct {
	UpdateID         int64             `json:"update_id"`
	Message          *Message          `json:"message"`
	CallbackQuery    *CallbackQuery    `json:"callback_query"`
	PreCheckoutQuery *PreCheckoutQuery `json:"pre_checkout_query,omitempty"`
}

// InlineKeyboardButton is one button of an inline keyboard. Exactly one of
// CallbackData (handled by the bot) or URL (opened directly by the client,
// e.g. to start a chat with support) is normally set.
type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
}

// InlineKeyboardMarkup is an inline keyboard attached to a message.
type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

// KeyboardButton is one button of a persistent, chat-wide reply keyboard.
// RequestLocation, when true, makes tapping the button share the user's
// current location as a message instead of sending the button's text.
type KeyboardButton struct {
	Text            string `json:"text"`
	RequestLocation bool   `json:"request_location,omitempty"`
}

// ReplyKeyboardMarkup shows a persistent keyboard below the chat's text
// input, so people can tap instead of typing a command like /start.
type ReplyKeyboardMarkup struct {
	Keyboard       [][]KeyboardButton `json:"keyboard"`
	ResizeKeyboard bool               `json:"resize_keyboard,omitempty"`
}
