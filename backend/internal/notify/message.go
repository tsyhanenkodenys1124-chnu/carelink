// Пакет notify доставляє повідомлення помічникам через push, Telegram і пошту.
package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Види повідомлень, які створює застосунок.
const (
	KindReminder    = "нагадування"
	KindEscalation  = "ескалація"
	KindHelp        = "допомога"
	KindUnsure      = "непевність"
	KindPIN         = "pin"
	KindInvitation  = "запрошення"
	KindConfirmMail = "підтвердження пошти"
	KindResetMail   = "відновлення пароля"
	KindDigest      = "підсумок дня"
)

// Канали доставки.
const (
	ChannelPush     = "push"
	ChannelTelegram = "telegram"
	ChannelEmail    = "email"
)

// Message — готовий до відправки текст із посиланням на застосунок.
type Message struct {
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	URL      string `json:"url,omitempty"`
	WardName string `json:"wardName,omitempty"`
	// Urgent вмикає звук і обхід тихих годин для критичних сигналів.
	Urgent bool `json:"urgent,omitempty"`
}

// JSON серіалізує повідомлення для черги доставки.
func (m Message) JSON() (string, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("серіалізація повідомлення: %w", err)
	}
	return string(raw), nil
}

// ParseMessage відновлює повідомлення з черги.
func ParseMessage(raw string) (Message, error) {
	var m Message
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return Message{}, fmt.Errorf("розбір повідомлення: %w", err)
	}
	return m, nil
}

// PlainText повертає текст для Telegram і пошти.
func (m Message) PlainText() string {
	text := m.Title
	if m.Body != "" {
		text += "\n" + m.Body
	}
	if m.URL != "" {
		text += "\n" + m.URL
	}
	return text
}

// Sender доставляє одне повідомлення в конкретний канал.
type Sender interface {
	// Name повертає назву каналу.
	Name() string
	// Send доставляє повідомлення. Target залежить від каналу:
	// адреса пошти, ідентифікатор чату Telegram або підписка push.
	Send(ctx context.Context, target Target, message Message) error
}

// Target описує адресата в межах каналу.
type Target struct {
	Email          string
	TelegramChatID string
	Push           Subscription
}

// Subscription — дані підписки Web Push із браузера.
type Subscription struct {
	Endpoint string
	P256DH   string
	Auth     string
}

// Retryable позначає помилки, які варто повторити пізніше.
type Retryable struct{ Err error }

func (r Retryable) Error() string { return r.Err.Error() }
func (r Retryable) Unwrap() error { return r.Err }

// Permanent позначає помилки, після яких повтор не має сенсу:
// підписку відкликано, адресу видалено, бота заблоковано.
type Permanent struct{ Err error }

func (p Permanent) Error() string { return p.Err.Error() }
func (p Permanent) Unwrap() error { return p.Err }

// Backoff повертає затримку перед наступною спробою доставки.
func Backoff(attempt int) time.Duration {
	switch {
	case attempt <= 1:
		return time.Minute
	case attempt == 2:
		return 5 * time.Minute
	case attempt == 3:
		return 15 * time.Minute
	default:
		return time.Hour
	}
}

// IsPermanent повідомляє, що повторювати доставку не варто.
func IsPermanent(err error) bool {
	var permanent Permanent
	return errors.As(err, &permanent)
}
