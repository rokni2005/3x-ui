// Package bale is a minimal client for the Bale Messenger Bot API
// (https://dev.bale.ai), which mirrors the Telegram Bot API shape.
package bale

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strconv"
	"time"
)

const apiBaseURL = "https://tapi.bale.ai/bot%s/%s"

// Client talks to the Bale Bot API over HTTPS.
type Client struct {
	token      string
	httpClient *http.Client

	// Debug, when true, logs the raw JSON body of every API response.
	// The payments endpoints (pre_checkout_query / successful_payment) are
	// not officially documented at the level of exact field names here, so
	// this is the way to confirm/diagnose their real shape against a live
	// test transaction.
	Debug bool
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
	if c.Debug {
		log.Printf("bale debug: %s raw response: %s", method, string(data))
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

func (c *Client) callMultipart(method string, fields map[string]string, fileField, filename string, fileData io.Reader, out any) error {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return fmt.Errorf("bale: write field %s for %s: %w", key, method, err)
		}
	}
	part, err := writer.CreateFormFile(fileField, filename)
	if err != nil {
		return fmt.Errorf("bale: create form file for %s: %w", method, err)
	}
	if _, err := io.Copy(part, fileData); err != nil {
		return fmt.Errorf("bale: copy file data for %s: %w", method, err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("bale: close multipart writer for %s: %w", method, err)
	}

	url := fmt.Sprintf(apiBaseURL, c.token, method)
	resp, err := c.httpClient.Post(url, writer.FormDataContentType(), body)
	if err != nil {
		return fmt.Errorf("bale: request %s: %w", method, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("bale: read response for %s: %w", method, err)
	}
	if c.Debug {
		log.Printf("bale debug: %s raw response: %s", method, string(data))
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

// SendPhoto uploads a photo (read from photo, named filename purely to hint
// at its format) as a new message, with an optional caption and keyboard.
// Uploading the bytes directly means we never need a publicly reachable URL
// for the image.
func (c *Client) SendPhoto(chatID int64, filename string, photo io.Reader, caption string, markup any) (*Message, error) {
	fields := map[string]string{
		"chat_id": strconv.FormatInt(chatID, 10),
	}
	if caption != "" {
		fields["caption"] = caption
	}
	if markup != nil {
		markupJSON, err := json.Marshal(markup)
		if err != nil {
			return nil, fmt.Errorf("bale: marshal reply_markup: %w", err)
		}
		fields["reply_markup"] = string(markupJSON)
	}

	var msg Message
	if err := c.callMultipart("sendPhoto", fields, "photo", filename, photo, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// EditMessageCaption edits the caption/keyboard of a previously sent photo message.
func (c *Client) EditMessageCaption(chatID, messageID int64, caption string, markup any) error {
	params := map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"caption":    caption,
	}
	if markup != nil {
		params["reply_markup"] = markup
	}
	return c.call("editMessageCaption", params, nil)
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

// SendMessage sends a text message, optionally with a keyboard: either an
// *InlineKeyboardMarkup (buttons attached to this message) or a
// *ReplyKeyboardMarkup (a persistent keyboard for the whole chat). Pass nil
// for no keyboard.
func (c *Client) SendMessage(chatID int64, text string, markup any) (*Message, error) {
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

// SendInvoice sends a native payable invoice. providerToken is the payment
// provider's token (e.g. a Bale wallet "WALLET-..." token); prices are given
// in the currency's smallest unit, matching the Telegram Bot Payments API
// that Bale's Bot API is modeled on.
func (c *Client) SendInvoice(chatID int64, title, description, payload, providerToken, currency string, prices []LabeledPrice) (*Message, error) {
	params := map[string]any{
		"chat_id":        chatID,
		"title":          title,
		"description":    description,
		"payload":        payload,
		"provider_token": providerToken,
		"currency":       currency,
		"prices":         prices,
	}
	var msg Message
	if err := c.call("sendInvoice", params, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// AnswerPreCheckoutQuery must be called shortly after a PreCheckoutQuery is
// received, approving (ok=true) or rejecting (ok=false, with errorMessage
// shown to the customer) the charge before Bale actually processes it.
func (c *Client) AnswerPreCheckoutQuery(id string, ok bool, errorMessage string) error {
	params := map[string]any{
		"pre_checkout_query_id": id,
		"ok":                    ok,
	}
	if !ok && errorMessage != "" {
		params["error_message"] = errorMessage
	}
	return c.call("answerPreCheckoutQuery", params, nil)
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
