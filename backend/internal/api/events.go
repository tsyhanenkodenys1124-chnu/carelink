package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Типи подій, які отримує панель помічника в реальному часі.
const (
	EventHelp       = "допомога"
	EventUnsure     = "непевність"
	EventEscalation = "ескалація"
	EventOccurrence = "виконання"
	EventSignalDone = "сигнал закрито"
)

// Event — одна подія для відкритих панелей помічників.
type Event struct {
	Type    string         `json:"type"`
	WardID  string         `json:"wardId"`
	Title   string         `json:"title"`
	Urgent  bool           `json:"urgent"`
	At      string         `json:"at"`
	Payload map[string]any `json:"payload,omitempty"`
}

// Hub роздає події відкритим з'єднанням SSE.
//
// Передплата ведеться за помічником, а не за переліком підопічних: перелік
// змінюється під час роботи — помічник створює профіль або приймає запрошення,
// і доступ так само можуть відкликати. Тому коло адресатів визначається в мить
// надсилання події, а не в мить відкриття потоку.
//
// Кожен передплатник отримує власний буферизований канал. Якщо панель не встигає
// читати, подія відкидається: панель однаково перезавантажує стан за таймером.
type Hub struct {
	mu          sync.RWMutex
	subscribers map[*subscriber]struct{}
}

type subscriber struct {
	helperID string
	events   chan Event
}

// NewHub створює порожній роздавач подій.
func NewHub() *Hub {
	return &Hub{subscribers: map[*subscriber]struct{}{}}
}

// Subscribe відкриває канал подій для помічника.
func (h *Hub) Subscribe(helperID string) *subscriber {
	sub := &subscriber{helperID: helperID, events: make(chan Event, 32)}
	h.mu.Lock()
	h.subscribers[sub] = struct{}{}
	h.mu.Unlock()
	return sub
}

// Unsubscribe закриває канал подій.
func (h *Hub) Unsubscribe(sub *subscriber) {
	h.mu.Lock()
	if _, ok := h.subscribers[sub]; ok {
		delete(h.subscribers, sub)
		close(sub.events)
	}
	h.mu.Unlock()
}

// Publish надсилає подію переліченим помічникам.
func (h *Hub) Publish(event Event, recipients []string) {
	allowed := make(map[string]bool, len(recipients))
	for _, helperID := range recipients {
		allowed[helperID] = true
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for sub := range h.subscribers {
		if !allowed[sub.helperID] {
			continue
		}
		select {
		case sub.events <- event:
		default:
		}
	}
}

// Subscribers повертає кількість відкритих панелей. Використовується у тестах.
func (h *Hub) Subscribers() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subscribers)
}

// publish створює подію та віддає її панелям помічників, які мають доступ
// до цього підопічного саме зараз.
func (s *Server) publish(ctx context.Context, wardID, kind, title string, urgent bool, payload map[string]any) {
	recipients, err := s.helperIDsFor(ctx, wardID)
	if err != nil {
		s.logError("Не вдалося визначити адресатів події", err)
		return
	}
	s.hub.Publish(Event{
		Type:    kind,
		WardID:  wardID,
		Title:   title,
		Urgent:  urgent,
		At:      s.nowStamp(),
		Payload: payload,
	}, recipients)
}

// streamEvents віддає потік подій SSE для всіх підопічних помічника.
func (s *Server) streamEvents(w http.ResponseWriter, r *http.Request) {
	helper := helperOf(r)
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.writeError(w, http.StatusInternalServerError, "Сервер не підтримує потокові відповіді.")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("retry: 5000\n\n")); err != nil {
		return
	}
	flusher.Flush()

	sub := s.hub.Subscribe(helper.ID)
	defer s.hub.Unsubscribe(sub)

	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := w.Write([]byte(": пульс\n\n")); err != nil {
				return
			}
			flusher.Flush()
		case event, open := <-sub.events:
			if !open {
				return
			}
			raw, err := json.Marshal(event)
			if err != nil {
				s.log.Warn("Не вдалося серіалізувати подію", "помилка", err)
				continue
			}
			if _, err := w.Write([]byte("event: " + event.Type + "\ndata: " + string(raw) + "\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
