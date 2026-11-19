// Пакет api містить HTTP-інтерфейс застосунку «Догляд».
package api

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"doglyad/backend/internal/config"
	"doglyad/backend/internal/notify"
)

// Server обслуговує API помічників, API пристрою підопічного та статику PWA.
type Server struct {
	db      *sql.DB
	cfg     config.Config
	log     *slog.Logger
	hub     *Hub
	senders map[string]notify.Sender
	// telegram потрібен окремо для довгого опитування оновлень бота.
	telegram *notify.TelegramSender
	// clock дозволяє тестам керувати часом.
	clock func() time.Time
}

// New створює сервер і готує канали доставки за конфігурацією.
func New(db *sql.DB, cfg config.Config, log *slog.Logger) (*Server, error) {
	s := &Server{
		db:      db,
		cfg:     cfg,
		log:     log,
		hub:     NewHub(),
		senders: map[string]notify.Sender{},
	}
	if err := os.MkdirAll(cfg.MediaDir, 0o750); err != nil {
		return nil, err
	}

	if cfg.Push.Enabled() {
		sender, err := notify.NewPushSender(cfg.Push.PublicKey, cfg.Push.PrivateKey, cfg.Push.Subject)
		if err != nil {
			return nil, err
		}
		s.senders[notify.ChannelPush] = sender
	} else {
		log.Warn("Web Push вимкнено: не задано VAPID_PUBLIC_KEY та VAPID_PRIVATE_KEY")
		s.senders[notify.ChannelPush] = notify.NewLogSender(notify.ChannelPush, log)
	}

	if cfg.Telegram.Enabled() {
		sender := notify.NewTelegramSender(cfg.Telegram.BotToken, log)
		s.telegram = sender
		s.senders[notify.ChannelTelegram] = sender
	} else {
		log.Warn("Telegram вимкнено: не задано TELEGRAM_BOT_TOKEN")
		s.senders[notify.ChannelTelegram] = notify.NewLogSender(notify.ChannelTelegram, log)
	}

	if cfg.SMTP.Enabled() {
		s.senders[notify.ChannelEmail] = notify.NewEmailSender(
			cfg.SMTP.Host, cfg.SMTP.Port, cfg.SMTP.Username, cfg.SMTP.Password, cfg.SMTP.From, cfg.SMTP.StartTLS)
	} else {
		log.Warn("Пошта вимкнена: не задано SMTP_HOST та SMTP_FROM, листи потраплять у журнал")
		s.senders[notify.ChannelEmail] = notify.NewLogSender(notify.ChannelEmail, log)
	}
	return s, nil
}

// Handler збирає маршрути API та роздачу PWA.
func (s *Server) Handler() http.Handler {
	api := http.NewServeMux()
	s.routes(api)

	root := http.NewServeMux()
	root.Handle("/api/", http.StripPrefix("/api", s.cors(api)))
	root.Handle("/", s.static())
	return s.recovery(root)
}

func (s *Server) routes(m *http.ServeMux) {
	m.HandleFunc("GET /health", s.health)

	// Обліковий запис помічника.
	m.HandleFunc("POST /auth/register", s.register)
	m.HandleFunc("POST /auth/login", s.login)
	m.HandleFunc("POST /auth/logout", s.helperOnly(s.logout))
	m.HandleFunc("POST /auth/password-reset", s.requestPasswordReset)
	m.HandleFunc("POST /auth/password-reset/confirm", s.confirmPasswordReset)
	m.HandleFunc("POST /auth/email/confirm", s.confirmEmail)
	m.HandleFunc("POST /auth/email/resend", s.helperOnly(s.resendConfirmation))

	m.HandleFunc("GET /me", s.helperOnly(s.me))
	m.HandleFunc("PATCH /me", s.helperOnly(s.updateMe))
	m.HandleFunc("GET /me/sessions", s.helperOnly(s.listSessions))
	m.HandleFunc("DELETE /me/sessions/{id}", s.helperOnly(s.revokeSession))
	m.HandleFunc("GET /me/notifications", s.helperOnly(s.listNotificationPrefs))
	m.HandleFunc("PUT /me/notifications", s.helperOnly(s.putNotificationPrefs))
	m.HandleFunc("POST /me/push", s.helperOnly(s.subscribePush))
	m.HandleFunc("DELETE /me/push", s.helperOnly(s.unsubscribePush))
	m.HandleFunc("GET /me/push/key", s.helperOnly(s.pushPublicKey))
	m.HandleFunc("POST /me/telegram", s.helperOnly(s.createTelegramLink))
	m.HandleFunc("DELETE /me/telegram", s.helperOnly(s.revokeTelegramLink))

	// Підопічні та налаштування.
	m.HandleFunc("GET /wards", s.helperOnly(s.listWards))
	m.HandleFunc("POST /wards", s.helperOnly(s.createWard))
	m.HandleFunc("GET /wards/{id}", s.helperOnly(s.getWard))
	m.HandleFunc("PATCH /wards/{id}", s.helperOnly(s.updateWard))
	m.HandleFunc("DELETE /wards/{id}", s.helperOnly(s.deleteWard))
	m.HandleFunc("POST /wards/{id}/photo", s.helperOnly(s.uploadWardPhoto))
	m.HandleFunc("GET /wards/{id}/needs", s.helperOnly(s.getNeeds))
	m.HandleFunc("PUT /wards/{id}/needs", s.helperOnly(s.putNeeds))
	m.HandleFunc("GET /wards/{id}/accessibility", s.helperOnly(s.getAccessibility))
	m.HandleFunc("PUT /wards/{id}/accessibility", s.helperOnly(s.putAccessibility))
	m.HandleFunc("PUT /wards/{id}/pin", s.helperOnly(s.putPIN))

	// Пристрої підопічного.
	m.HandleFunc("GET /wards/{id}/devices", s.helperOnly(s.listDevices))
	m.HandleFunc("POST /wards/{id}/pairing", s.helperOnly(s.createPairing))
	m.HandleFunc("DELETE /wards/{id}/devices/{deviceID}", s.helperOnly(s.revokeDevice))

	// Помічники та запрошення.
	m.HandleFunc("GET /wards/{id}/links", s.helperOnly(s.listLinks))
	m.HandleFunc("POST /wards/{id}/links", s.helperOnly(s.createInvitation))
	m.HandleFunc("GET /wards/{id}/invitations", s.helperOnly(s.listInvitations))
	m.HandleFunc("PATCH /wards/{id}/links/{linkID}", s.helperOnly(s.updateLink))
	m.HandleFunc("DELETE /wards/{id}/links/{linkID}", s.helperOnly(s.revokeLink))
	m.HandleFunc("DELETE /wards/{id}/invitations/{invitationID}", s.helperOnly(s.revokeInvitation))
	m.HandleFunc("POST /invitations/accept", s.helperOnly(s.acceptInvitation))
	m.HandleFunc("GET /invitations/{token}", s.previewInvitation)

	// Завдання та виконання.
	m.HandleFunc("GET /wards/{id}/tasks", s.helperOnly(s.listTasks))
	m.HandleFunc("POST /wards/{id}/tasks", s.helperOnly(s.createTask))
	m.HandleFunc("GET /tasks/{id}", s.helperOnly(s.getTask))
	m.HandleFunc("PATCH /tasks/{id}", s.helperOnly(s.updateTask))
	m.HandleFunc("DELETE /tasks/{id}", s.helperOnly(s.deleteTask))
	m.HandleFunc("POST /tasks/{id}/photo", s.helperOnly(s.uploadTaskPhoto))
	m.HandleFunc("GET /occurrences", s.helperOnly(s.listOccurrences))
	m.HandleFunc("POST /occurrences/{id}/mark", s.helperOnly(s.markByHelper))
	m.HandleFunc("POST /occurrences/{id}/undo", s.helperOnly(s.undoByHelper))

	// Сигнали від підопічного та ескалації.
	m.HandleFunc("GET /signals", s.helperOnly(s.listSignals))
	m.HandleFunc("GET /events", s.helperOnly(s.streamEvents))
	m.HandleFunc("GET /help-requests", s.helperOnly(s.listHelp))
	m.HandleFunc("POST /help-requests/{id}/ack", s.helperOnly(s.ackHelp))
	m.HandleFunc("POST /help-requests/{id}/close", s.helperOnly(s.closeHelp))
	m.HandleFunc("GET /unsure-requests", s.helperOnly(s.listUnsure))
	m.HandleFunc("POST /unsure-requests/{id}/answer", s.helperOnly(s.answerUnsure))
	m.HandleFunc("GET /escalations", s.helperOnly(s.listEscalations))
	m.HandleFunc("POST /escalations/{id}/take", s.helperOnly(s.takeEscalation))
	m.HandleFunc("POST /escalations/{id}/close", s.helperOnly(s.closeEscalation))

	// Рекомендації, звіти, журнал.
	m.HandleFunc("GET /suggestions", s.helperOnly(s.listSuggestions))
	m.HandleFunc("POST /suggestions/{id}/decision", s.helperOnly(s.decideSuggestion))
	m.HandleFunc("GET /reports", s.helperOnly(s.report))
	m.HandleFunc("GET /reports/export.csv", s.helperOnly(s.exportReport))
	m.HandleFunc("GET /audit", s.helperOnly(s.auditJournal))

	// Медіа доступні помічникам і пристрою підопічного.
	m.HandleFunc("GET /media/{id}", s.serveMedia)

	// API пристрою підопічного.
	m.HandleFunc("POST /pairing", s.pairDevice)
	m.HandleFunc("GET /device/today", s.deviceOnly(s.deviceToday))
	m.HandleFunc("POST /device/mark", s.deviceOnly(s.deviceMark))
	m.HandleFunc("POST /device/undo", s.deviceOnly(s.deviceUndo))
	m.HandleFunc("POST /device/help", s.deviceOnly(s.deviceHelp))
	m.HandleFunc("POST /device/unsure", s.deviceOnly(s.deviceUnsure))
	m.HandleFunc("POST /device/signals", s.deviceOnly(s.deviceSignals))
	m.HandleFunc("POST /device/pin-login", s.deviceOnly(s.devicePINLogin))

	// Режим помічника на пристрої підопічного. Поверхня навмисно вузька:
	// лише справи й сигнали одного підопічного, жодних налаштувань доступу.
	m.HandleFunc("GET /device/helper/overview", s.pinOnly(s.pinOverview))
	m.HandleFunc("POST /device/helper/occurrences/{id}/mark", s.pinOnly(s.pinMark))
	m.HandleFunc("POST /device/helper/occurrences/{id}/undo", s.pinOnly(s.pinUndo))
	m.HandleFunc("POST /device/helper/help/{id}/close", s.pinOnly(s.pinCloseHelp))
	m.HandleFunc("POST /device/helper/unsure/{id}/answer", s.pinOnly(s.pinAnswerUnsure))
	m.HandleFunc("POST /device/helper/escalations/{id}/close", s.pinOnly(s.pinCloseEscalation))
	m.HandleFunc("POST /device/helper/logout", s.pinOnly(s.pinLogout))
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.db.PingContext(r.Context()); err != nil {
		s.fail(w, http.StatusServiceUnavailable, "База даних недоступна.", err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// static роздає зібраний застосунок і повертає index.html для маршрутів PWA.
func (s *Server) static() http.Handler {
	root := s.cfg.WebDir
	fileServer := http.FileServer(http.Dir(root))
	index := filepath.Join(root, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := os.Stat(index); err != nil {
			s.writeError(w, http.StatusNotFound, "Інтерфейс не зібрано. Виконайте npm run build у каталозі web.")
			return
		}
		clean := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if clean == "." {
			clean = "index.html"
		}
		target := filepath.Join(root, clean)
		if info, err := os.Stat(target); err == nil && !info.IsDir() {
			// Service worker не можна кешувати: інакше оновлення не доходять до пристрою.
			if clean == "sw.js" {
				w.Header().Set("Cache-Control", "no-cache")
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, index)
	})
}

// Background запускає планувальник, доставку сповіщень і бота Telegram.
func (s *Server) Background(ctx context.Context) {
	go s.runScheduler(ctx)
	go s.runDispatcher(ctx)
	if s.telegram != nil {
		go s.telegram.Poll(ctx, s.linkTelegramChat)
	}
}
