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

// Message is a Bale message, mirroring the Telegram-compatible Bot API.
type Message struct {
	MessageID int64       `json:"message_id"`
	From      *User       `json:"from"`
	Chat      Chat        `json:"chat"`
	Text      string      `json:"text"`
	Photo     []PhotoSize `json:"photo"`
}

// CallbackQuery is fired when a user taps an inline keyboard button.
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

// Update is a single item returned by getUpdates.
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

// InlineKeyboardButton is one button of an inline keyboard.
type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
}

// InlineKeyboardMarkup is an inline keyboard attached to a message.
type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}
