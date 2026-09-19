// Package bale is a minimal client for the Bale Messenger Bot API
// (https://dev.bale.ai), which mirrors the Telegram Bot API shape.
package bale

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const apiBaseURL = "https://tapi.bale.ai/bot%s/%s"

// Client talks to the Bale Bot API over HTTPS.
type Client struct {
	token      string
	httpClient *http.Client
}

// NewClient builds a Bale API client for the given bot token.
func NewClient(token string) *Client {
	return &Client{
		token:      token,
		httpClient: &http.Client{Timeout: 65 * time.Second},
	}
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
}

func (c *Client) call(method string, params map[string]any, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("bale: marshal params for %s: %w", method, err)
	}

	url := fmt.Sprintf(apiBaseURL, c.token, method)
	resp, err := c.httpClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("bale: request %s: %w", method, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("bale: read response for %s: %w", method, err)
	}

	var envelope apiResponse
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("bale: invalid response for %s: %s", method, string(data))
	}
	if !envelope.OK {
		return fmt.Errorf("bale: %s failed: %s", method, envelope.Description)
	}
	if out != nil && len(envelope.Result) > 0 {
		if err := json.Unmarshal(envelope.Result, out); err != nil {
			return fmt.Errorf("bale: decode result for %s: %w", method, err)
		}
	}
	return nil
}

// GetUpdates long-polls for new updates starting at offset.
func (c *Client) GetUpdates(offset int64) ([]Update, error) {
	var updates []Update
	params := map[string]any{
		"offset":  offset,
		"timeout": 50,
	}
	if err := c.call("getUpdates", params, &updates); err != nil {
		return nil, err
	}
	return updates, nil
}

// SendMessage sends a text message, optionally with an inline keyboard.
func (c *Client) SendMessage(chatID int64, text string, markup *InlineKeyboardMarkup) (*Message, error) {
	params := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	if markup != nil {
		params["reply_markup"] = markup
	}
	var msg Message
	if err := c.call("sendMessage", params, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// EditMessageText edits the text/keyboard of a previously sent message.
func (c *Client) EditMessageText(chatID, messageID int64, text string, markup *InlineKeyboardMarkup) error {
	params := map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       text,
	}
	if markup != nil {
		params["reply_markup"] = markup
	}
	return c.call("editMessageText", params, nil)
}

// AnswerCallbackQuery acknowledges a tapped inline button.
func (c *Client) AnswerCallbackQuery(id, text string, showAlert bool) error {
	params := map[string]any{
		"callback_query_id": id,
	}
	if text != "" {
		params["text"] = text
		params["show_alert"] = showAlert
	}
	return c.call("answerCallbackQuery", params, nil)
}

// ForwardMessage forwards a message (e.g. a payment receipt photo) to another chat.
func (c *Client) ForwardMessage(toChatID, fromChatID, messageID int64) error {
	params := map[string]any{
		"chat_id":      toChatID,
		"from_chat_id": fromChatID,
		"message_id":   messageID,
	}
	return c.call("forwardMessage", params, nil)
}
