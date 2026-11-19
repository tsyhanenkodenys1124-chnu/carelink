package notify

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeBotAPI відтворює ті методи Bot API, якими користується застосунок.
type fakeBotAPI struct {
	mu       sync.Mutex
	sent     []map[string]any
	updates  []map[string]any
	served   bool
	server   *httptest.Server
	updateCh chan struct{}
}

func startFakeBot(t *testing.T) *fakeBotAPI {
	t.Helper()
	bot := &fakeBotAPI{updateCh: make(chan struct{}, 4)}
	bot.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if raw, err := io.ReadAll(r.Body); err == nil && len(raw) > 0 {
			_ = json.Unmarshal(raw, &body)
		}
		w.Header().Set("Content-Type", "application/json")

		switch {
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			bot.mu.Lock()
			bot.sent = append(bot.sent, body)
			bot.mu.Unlock()
			select {
			case bot.updateCh <- struct{}{}:
			default:
			}
			_, _ = w.Write([]byte(`{"ok":true}`))

		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			bot.mu.Lock()
			first := !bot.served
			bot.served = true
			updates := bot.updates
			bot.mu.Unlock()
			if !first {
				_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
				return
			}
			payload, _ := json.Marshal(map[string]any{"ok": true, "result": updates})
			_, _ = w.Write(payload)

		default:
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}))
	t.Cleanup(bot.server.Close)
	return bot
}

func (b *fakeBotAPI) sender() *TelegramSender {
	sender := NewTelegramSender("тестовий-токен", discardLogger())
	sender.baseURL = b.server.URL
	return sender
}

func (b *fakeBotAPI) messages() []map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]map[string]any(nil), b.sent...)
}

func TestTelegramSendsMessageToChat(t *testing.T) {
	bot := startFakeBot(t)
	sender := bot.sender()

	err := sender.Send(context.Background(), Target{TelegramChatID: "12345"}, Message{
		Kind:  KindEscalation,
		Title: "Критичну справу не виконано",
		Body:  "Маріє Іванівно не відмітила ранкові ліки.",
		URL:   "https://care.example.org/?tab=signals",
	})
	if err != nil {
		t.Fatalf("надсилання: %v", err)
	}

	sent := bot.messages()
	if len(sent) != 1 {
		t.Fatalf("очікувалося одне повідомлення, надіслано %d", len(sent))
	}
	if sent[0]["chat_id"] != "12345" {
		t.Fatalf("неправильний чат: %v", sent[0]["chat_id"])
	}
	text, _ := sent[0]["text"].(string)
	for _, want := range []string{"Критичну справу не виконано", "ранкові ліки", "care.example.org"} {
		if !strings.Contains(text, want) {
			t.Fatalf("у тексті немає %q: %q", want, text)
		}
	}
}

func TestTelegramRejectsChatlessTarget(t *testing.T) {
	bot := startFakeBot(t)
	err := bot.sender().Send(context.Background(), Target{}, Message{Title: "Перевірка"})
	if !IsPermanent(err) {
		t.Fatalf("без чату має бути стала помилка, отримано %v", err)
	}
}

func TestTelegramLinksChatByStartCode(t *testing.T) {
	bot := startFakeBot(t)
	bot.updates = []map[string]any{
		{
			"update_id": 100,
			"message": map[string]any{
				"text": "/start код-прив-язки",
				"chat": map[string]any{"id": 777},
			},
		},
	}
	sender := bot.sender()

	var gotCode, gotChat string
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sender.Poll(ctx, func(_ context.Context, code, chatID string) (string, bool) {
		gotCode, gotChat = code, chatID
		return "Олена", true
	})

	select {
	case <-bot.updateCh:
	case <-time.After(5 * time.Second):
		t.Fatal("бот не обробив команду /start")
	}
	cancel()

	if gotCode != "код-прив-язки" {
		t.Fatalf("код прив'язки прочитано неправильно: %q", gotCode)
	}
	if gotChat != "777" {
		t.Fatalf("ідентифікатор чату прочитано неправильно: %q", gotChat)
	}
	sent := bot.messages()
	if len(sent) == 0 {
		t.Fatal("бот не підтвердив прив'язку")
	}
	text, _ := sent[0]["text"].(string)
	if !strings.Contains(text, "Олена") {
		t.Fatalf("у підтвердженні немає імені помічника: %q", text)
	}
}
