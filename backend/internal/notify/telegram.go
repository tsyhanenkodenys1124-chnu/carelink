package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// telegramAPI — адреса Bot API. Тести підміняють її на локальний сервер.
const telegramAPI = "https://api.telegram.org"

// TelegramSender надсилає повідомлення через Bot API.
type TelegramSender struct {
	token   string
	baseURL string
	client  *http.Client
	log     *slog.Logger
}

// NewTelegramSender створює клієнт бота.
func NewTelegramSender(token string, log *slog.Logger) *TelegramSender {
	return &TelegramSender{
		token:   token,
		baseURL: telegramAPI,
		client:  &http.Client{Timeout: 20 * time.Second},
		log:     log,
	}
}

// Name повертає назву каналу.
func (t *TelegramSender) Name() string { return ChannelTelegram }

// Send надсилає текст у чат помічника.
func (t *TelegramSender) Send(ctx context.Context, target Target, message Message) error {
	if target.TelegramChatID == "" {
		return Permanent{Err: errors.New("помічник не підключив Telegram")}
	}
	body := map[string]any{
		"chat_id":                  target.TelegramChatID,
		"text":                     message.PlainText(),
		"disable_web_page_preview": true,
	}
	if message.Urgent {
		body["disable_notification"] = false
	}
	var response struct {
		OK          bool   `json:"ok"`
		ErrorCode   int    `json:"error_code"`
		Description string `json:"description"`
	}
	if err := t.call(ctx, "sendMessage", body, &response); err != nil {
		return err
	}
	if !response.OK {
		if response.ErrorCode == 403 || response.ErrorCode == 400 {
			return Permanent{Err: fmt.Errorf("Telegram відхилив повідомлення: %s", response.Description)}
		}
		return Retryable{Err: fmt.Errorf("Telegram повернув помилку: %s", response.Description)}
	}
	return nil
}

func (t *TelegramSender) call(ctx context.Context, method string, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return Permanent{Err: err}
	}
	endpoint := t.baseURL + "/bot" + t.token + "/" + method
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return Permanent{Err: err}
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := t.client.Do(request)
	if err != nil {
		return Retryable{Err: fmt.Errorf("запит до Telegram: %w", err)}
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Retryable{Err: err}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return Retryable{Err: fmt.Errorf("відповідь Telegram: %w", err)}
	}
	return nil
}

// LinkFunc прив'язує чат до помічника за одноразовим кодом.
// Повертає ім'я помічника та ознаку успіху.
type LinkFunc func(ctx context.Context, code, chatID string) (string, bool)

// Poll читає оновлення бота та обробляє команду /start із кодом прив'язки.
// Метод блокує виклик до скасування контексту.
func (t *TelegramSender) Poll(ctx context.Context, link LinkFunc) {
	offset := 0
	for {
		if ctx.Err() != nil {
			return
		}
		updates, next, err := t.updates(ctx, offset)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			t.log.Warn("Не вдалося отримати оновлення Telegram", "помилка", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(15 * time.Second):
			}
			continue
		}
		offset = next
		for _, update := range updates {
			t.handle(ctx, update, link)
		}
	}
}

type telegramUpdate struct {
	UpdateID int `json:"update_id"`
	Message  struct {
		Text string `json:"text"`
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
	} `json:"message"`
}

func (t *TelegramSender) updates(ctx context.Context, offset int) ([]telegramUpdate, int, error) {
	var response struct {
		OK     bool             `json:"ok"`
		Result []telegramUpdate `json:"result"`
	}
	// Довге опитування: запит висить до 30 секунд, поки не буде оновлень.
	pollCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	body := map[string]any{"timeout": 30, "allowed_updates": []string{"message"}}
	if offset > 0 {
		body["offset"] = offset
	}
	if err := t.call(pollCtx, "getUpdates", body, &response); err != nil {
		return nil, offset, err
	}
	next := offset
	for _, update := range response.Result {
		if update.UpdateID >= next {
			next = update.UpdateID + 1
		}
	}
	return response.Result, next, nil
}

func (t *TelegramSender) handle(ctx context.Context, update telegramUpdate, link LinkFunc) {
	text := strings.TrimSpace(update.Message.Text)
	chatID := strconv.FormatInt(update.Message.Chat.ID, 10)
	if chatID == "0" || text == "" {
		return
	}
	reply := func(message string) {
		if err := t.Send(ctx, Target{TelegramChatID: chatID}, Message{Title: message}); err != nil {
			t.log.Warn("Не вдалося відповісти в Telegram", "помилка", err)
		}
	}
	if !strings.HasPrefix(text, "/start") {
		reply("Щоб отримувати сповіщення, відкрийте застосунок «Догляд», розділ «Профіль», і натисніть «Підключити Telegram».")
		return
	}
	code := strings.TrimSpace(strings.TrimPrefix(text, "/start"))
	if code == "" {
		reply("Надішліть посилання з застосунку «Догляд», щоб підключити сповіщення.")
		return
	}
	name, ok := link(ctx, code, chatID)
	if !ok {
		reply("Код недійсний або вже використаний. Створіть нове посилання в застосунку.")
		return
	}
	reply("Готово, " + name + ". Сюди надходитимуть нагадування, ескалації та запити про допомогу.")
}
