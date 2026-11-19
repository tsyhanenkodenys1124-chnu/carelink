// Пакет config читає налаштування середовища один раз під час запуску.
package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config містить усі параметри запуску сервера.
type Config struct {
	Address       string
	DatabasePath  string
	MediaDir      string
	WebDir        string
	WebOrigin     string
	PublicURL     string
	SecureCookies bool

	SMTP     SMTP
	Telegram Telegram
	Push     Push

	SessionTTL    time.Duration
	PairingTTL    time.Duration
	InvitationTTL time.Duration
	UndoWindow    time.Duration
}

// SMTP описує поштовий сервер для листів підтвердження та відновлення пароля.
type SMTP struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	StartTLS bool
}

// Enabled повідомляє, чи налаштована реальна відправка пошти.
func (s SMTP) Enabled() bool { return s.Host != "" && s.From != "" }

// Telegram описує бота для сповіщень помічників.
type Telegram struct {
	BotToken string
	BotName  string
}

// Enabled повідомляє, чи можна надсилати повідомлення через Telegram.
func (t Telegram) Enabled() bool { return t.BotToken != "" }

// Push описує ключі VAPID для Web Push.
type Push struct {
	PublicKey  string
	PrivateKey string
	Subject    string
}

// Enabled повідомляє, чи можна підписувати push-повідомлення.
func (p Push) Enabled() bool { return p.PublicKey != "" && p.PrivateKey != "" }

// Load збирає конфігурацію зі змінних середовища та безпечних типових значень.
func Load() Config {
	return Config{
		Address:       value("ADDR", ":8080"),
		DatabasePath:  value("DATABASE_PATH", "./data/doglyad.db"),
		MediaDir:      value("MEDIA_DIR", "./data/media"),
		WebDir:        value("WEB_DIR", "./web"),
		WebOrigin:     value("WEB_ORIGIN", "http://127.0.0.1:5173"),
		PublicURL:     strings.TrimRight(value("PUBLIC_URL", "http://localhost:8080"), "/"),
		SecureCookies: boolean("COOKIE_SECURE", false),
		SMTP: SMTP{
			Host:     value("SMTP_HOST", ""),
			Port:     number("SMTP_PORT", 587),
			Username: value("SMTP_USERNAME", ""),
			Password: value("SMTP_PASSWORD", ""),
			From:     value("SMTP_FROM", ""),
			StartTLS: boolean("SMTP_STARTTLS", true),
		},
		Telegram: Telegram{
			BotToken: value("TELEGRAM_BOT_TOKEN", ""),
			BotName:  value("TELEGRAM_BOT_NAME", ""),
		},
		Push: Push{
			PublicKey:  value("VAPID_PUBLIC_KEY", ""),
			PrivateKey: value("VAPID_PRIVATE_KEY", ""),
			Subject:    value("VAPID_SUBJECT", "mailto:admin@localhost"),
		},
		SessionTTL:    30 * 24 * time.Hour,
		PairingTTL:    15 * time.Minute,
		InvitationTTL: 72 * time.Hour,
		UndoWindow:    90 * time.Second,
	}
}

func value(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func number(key string, fallback int) int {
	v, err := strconv.Atoi(value(key, ""))
	if err != nil {
		return fallback
	}
	return v
}

func boolean(key string, fallback bool) bool {
	switch strings.ToLower(value(key, "")) {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	}
	return fallback
}
