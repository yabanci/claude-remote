package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

type InlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

type InlineKeyboard struct {
	Rows [][]InlineButton `json:"inline_keyboard"`
}

type SendOptions struct {
	ReplyTo  int64
	Keyboard *InlineKeyboard
}

func (c *Client) Send(ctx context.Context, chatID int64, text string, opts SendOptions) error {
	form := url.Values{
		"chat_id": {strconv.FormatInt(chatID, 10)},
		"text":    {text},
	}
	if opts.ReplyTo != 0 {
		form.Set("reply_to_message_id", strconv.FormatInt(opts.ReplyTo, 10))
		form.Set("allow_sending_without_reply", "true")
	}
	if opts.Keyboard != nil {
		encoded, err := json.Marshal(opts.Keyboard)
		if err != nil {
			return fmt.Errorf("marshal keyboard: %w", err)
		}
		form.Set("reply_markup", string(encoded))
	}

	_, err := c.call(ctx, "sendMessage", form)
	return err
}

func (c *Client) SendChatAction(ctx context.Context, chatID int64, action string) error {
	form := url.Values{
		"chat_id": {strconv.FormatInt(chatID, 10)},
		"action":  {action},
	}
	_, err := c.call(ctx, "sendChatAction", form)
	return err
}

func (c *Client) AnswerCallback(ctx context.Context, callbackID, text string) error {
	form := url.Values{"callback_query_id": {callbackID}}
	if text != "" {
		form.Set("text", text)
	}
	_, err := c.call(ctx, "answerCallbackQuery", form)
	return err
}
