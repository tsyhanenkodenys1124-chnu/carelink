package api

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"doglyad/backend/internal/notify"
	"doglyad/backend/internal/security"
)

// Стани рядка в черзі доставки.
const (
	statusCreated = "created"
	statusSending = "sending"
	statusSent    = "sent"
	statusFailed  = "failed"
	statusSkipped = "skipped"
)

// maxDeliveryAttempts обмежує кількість повторів доставки.
const maxDeliveryAttempts = 5

// channelPref описує налаштування одного каналу для помічника.
type channelPref struct {
	Channel    string `json:"channel"`
	Enabled    bool   `json:"enabled"`
	QuietStart string `json:"quietStart"`
	QuietEnd   string `json:"quietEnd"`
}

// defaultPrefs задає поведінку до того, як помічник щось налаштував.
// Термінові сигнали — допомога та ескалація — типово йдуть усіма каналами,
// зокрема поштою, бо саме вони вимагають негайної реакції.
func defaultPrefs() map[string]channelPref {
	return map[string]channelPref{
		notify.ChannelPush:     {Channel: notify.ChannelPush, Enabled: true},
		notify.ChannelTelegram: {Channel: notify.ChannelTelegram, Enabled: true},
		notify.ChannelEmail:    {Channel: notify.ChannelEmail, Enabled: true},
	}
}

// enqueue додає одне повідомлення до черги доставки.
func (s *Server) enqueue(ctx context.Context, helperID, wardID, channel, kind string, message notify.Message) {
	payload, err := message.JSON()
	if err != nil {
		s.logError("Не вдалося серіалізувати повідомлення", err)
		return
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO notifications(id,helper_id,ward_id,channel,kind,payload_json,status,next_attempt_at,created_at)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		security.ID(), nullIfEmpty(helperID), nullIfEmpty(wardID), channel, kind, payload,
		statusCreated, s.nowStamp(), s.nowStamp()); err != nil {
		s.logError("Не вдалося поставити повідомлення в чергу", err, "канал", channel)
	}
}

// notifyWard ставить у чергу повідомлення для всіх помічників підопічного
// та одразу показує подію у відкритих панелях.
func (s *Server) notifyWard(ctx context.Context, wardID string, message notify.Message, onlyPrimary bool) {
	query := "SELECT helper_id FROM care_links WHERE ward_id=? AND revoked_at IS NULL"
	if onlyPrimary {
		query += " AND is_primary=1"
	}
	rows, err := s.db.QueryContext(ctx, query, wardID)
	if err != nil {
		s.logError("Не вдалося знайти помічників для сповіщення", err)
		return
	}
	helperIDs := []string{}
	for rows.Next() {
		var helperID string
		if err := rows.Scan(&helperID); err != nil {
			rows.Close()
			s.logError("Не вдалося прочитати помічника", err)
			return
		}
		helperIDs = append(helperIDs, helperID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		s.logError("Не вдалося перелічити помічників", err)
		return
	}
	rows.Close()

	for _, helperID := range helperIDs {
		prefs := s.prefsFor(ctx, helperID, wardID)
		for _, channel := range []string{notify.ChannelPush, notify.ChannelTelegram, notify.ChannelEmail} {
			pref := prefs[channel]
			if !pref.Enabled {
				continue
			}
			// Тихі години не стосуються термінових сигналів.
			if !message.Urgent && s.inQuietHours(ctx, helperID, pref) {
				continue
			}
			s.enqueue(ctx, helperID, wardID, channel, message.Kind, message)
		}
	}
}

// prefsFor читає налаштування каналів: спершу для підопічного, потім загальні.
func (s *Server) prefsFor(ctx context.Context, helperID, wardID string) map[string]channelPref {
	prefs := defaultPrefs()
	rows, err := s.db.QueryContext(ctx, `
		SELECT channel,enabled,COALESCE(quiet_start,''),COALESCE(quiet_end,''),COALESCE(ward_id,'')
		FROM notification_prefs WHERE helper_id=? AND (ward_id IS NULL OR ward_id=?)
		ORDER BY ward_id IS NULL DESC`, helperID, wardID)
	if err != nil {
		s.log.Warn("Не вдалося прочитати налаштування сповіщень", "помилка", err)
		return prefs
	}
	defer rows.Close()
	// Спершу застосовуємо загальні рядки, потім рядки конкретного підопічного — вони мають пріоритет.
	for rows.Next() {
		var channel, quietStart, quietEnd, prefWard string
		var enabled int
		if err := rows.Scan(&channel, &enabled, &quietStart, &quietEnd, &prefWard); err != nil {
			s.log.Warn("Не вдалося прочитати рядок налаштувань", "помилка", err)
			return prefs
		}
		prefs[channel] = channelPref{Channel: channel, Enabled: enabled == 1, QuietStart: quietStart, QuietEnd: quietEnd}
	}
	if err := rows.Err(); err != nil {
		s.log.Warn("Не вдалося перелічити налаштування", "помилка", err)
	}
	return prefs
}

// inQuietHours перевіряє, чи потрапляє поточний час у тихі години помічника.
func (s *Server) inQuietHours(ctx context.Context, helperID string, pref channelPref) bool {
	if pref.QuietStart == "" || pref.QuietEnd == "" {
		return false
	}
	var timezone string
	if err := s.db.QueryRowContext(ctx, "SELECT timezone FROM helpers WHERE id=?", helperID).Scan(&timezone); err != nil {
		timezone = "Europe/Kyiv"
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		location = time.UTC
	}
	current := s.now().In(location).Format("15:04")
	if pref.QuietStart <= pref.QuietEnd {
		return current >= pref.QuietStart && current < pref.QuietEnd
	}
	// Інтервал через північ, наприклад 22:00–07:00.
	return current >= pref.QuietStart || current < pref.QuietEnd
}

// runDispatcher доставляє повідомлення з черги, поки працює сервер.
func (s *Server) runDispatcher(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.deliverBatch(ctx)
		}
	}
}

type queuedNotification struct {
	id       string
	helperID string
	channel  string
	payload  string
	attempt  int
}

// deliverBatch обробляє порцію черги. Рядки спершу зчитуються повністю,
// бо пул має єдине з'єднання і запит усередині курсора заблокував би базу.
func (s *Server) deliverBatch(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id,COALESCE(helper_id,''),channel,payload_json,attempt FROM notifications
		WHERE status=? AND (next_attempt_at IS NULL OR next_attempt_at<=?)
		ORDER BY created_at LIMIT 50`, statusCreated, s.nowStamp())
	if err != nil {
		s.logError("Не вдалося прочитати чергу доставки", err)
		return
	}
	queue := []queuedNotification{}
	for rows.Next() {
		var item queuedNotification
		if err := rows.Scan(&item.id, &item.helperID, &item.channel, &item.payload, &item.attempt); err != nil {
			rows.Close()
			s.logError("Не вдалося прочитати рядок черги", err)
			return
		}
		queue = append(queue, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		s.logError("Не вдалося перелічити чергу", err)
		return
	}
	rows.Close()

	for _, item := range queue {
		if ctx.Err() != nil {
			return
		}
		s.deliverOne(ctx, item)
	}
}

func (s *Server) deliverOne(ctx context.Context, item queuedNotification) {
	// Займаємо рядок, щоб інший цикл не взяв його повторно.
	result, err := s.db.ExecContext(ctx,
		"UPDATE notifications SET status=?,attempt=attempt+1 WHERE id=? AND status=?",
		statusSending, item.id, statusCreated)
	if err != nil {
		s.logError("Не вдалося зайняти повідомлення", err)
		return
	}
	if affected, err := result.RowsAffected(); err != nil || affected == 0 {
		return
	}

	message, err := notify.ParseMessage(item.payload)
	if err != nil {
		s.finishNotification(ctx, item.id, statusFailed, err)
		return
	}
	err = s.sendThroughChannel(ctx, item.helperID, item.channel, message)
	switch {
	case err == nil:
		s.finishNotification(ctx, item.id, statusSent, nil)
	case notify.IsPermanent(err):
		s.finishNotification(ctx, item.id, statusFailed, err)
	case item.attempt+1 >= maxDeliveryAttempts:
		s.finishNotification(ctx, item.id, statusFailed, err)
	default:
		s.retryNotification(ctx, item.id, item.attempt+1, err)
	}
}

func (s *Server) finishNotification(ctx context.Context, notificationID, status string, cause error) {
	message := ""
	if cause != nil {
		message = cause.Error()
		s.log.Warn("Повідомлення не доставлено", "id", notificationID, "помилка", cause)
	}
	if _, err := s.db.ExecContext(ctx,
		"UPDATE notifications SET status=?,error=?,sent_at=? WHERE id=?",
		status, nullIfEmpty(message), s.nowStamp(), notificationID); err != nil {
		s.logError("Не вдалося завершити повідомлення", err)
	}
}

func (s *Server) retryNotification(ctx context.Context, notificationID string, attempt int, cause error) {
	next := stamp(s.now().Add(notify.Backoff(attempt)))
	if _, err := s.db.ExecContext(ctx,
		"UPDATE notifications SET status=?,error=?,next_attempt_at=? WHERE id=?",
		statusCreated, cause.Error(), next, notificationID); err != nil {
		s.logError("Не вдалося перепланувати повідомлення", err)
	}
}

// sendThroughChannel доставляє повідомлення конкретним каналом.
func (s *Server) sendThroughChannel(ctx context.Context, helperID, channel string, message notify.Message) error {
	sender, ok := s.senders[channel]
	if !ok {
		return notify.Permanent{Err: errors.New("невідомий канал доставки: " + channel)}
	}
	switch channel {
	case notify.ChannelEmail:
		var email, confirmed string
		err := s.db.QueryRowContext(ctx,
			"SELECT email,COALESCE(email_confirmed_at,'') FROM helpers WHERE id=? AND deleted_at IS NULL",
			helperID).Scan(&email, &confirmed)
		if err != nil {
			return notify.Permanent{Err: errors.New("помічника не знайдено")}
		}
		return sender.Send(ctx, notify.Target{Email: email}, message)

	case notify.ChannelTelegram:
		var chatID string
		err := s.db.QueryRowContext(ctx,
			"SELECT COALESCE(chat_id,'') FROM telegram_links WHERE helper_id=? AND revoked_at IS NULL AND chat_id IS NOT NULL",
			helperID).Scan(&chatID)
		if err != nil || chatID == "" {
			return notify.Permanent{Err: errors.New("Telegram не підключено")}
		}
		return sender.Send(ctx, notify.Target{TelegramChatID: chatID}, message)

	case notify.ChannelPush:
		return s.sendPush(ctx, sender, helperID, message)
	}
	return notify.Permanent{Err: errors.New("канал не підтримується")}
}

// sendPush розсилає повідомлення на всі активні підписки помічника
// та прибирає ті, які браузер уже відкликав.
func (s *Server) sendPush(ctx context.Context, sender notify.Sender, helperID string, message notify.Message) error {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id,endpoint,p256dh,auth FROM push_subscriptions WHERE helper_id=? AND revoked_at IS NULL", helperID)
	if err != nil {
		return notify.Retryable{Err: err}
	}
	type subscription struct {
		id string
		notify.Subscription
	}
	subs := []subscription{}
	for rows.Next() {
		var item subscription
		if err := rows.Scan(&item.id, &item.Endpoint, &item.P256DH, &item.Auth); err != nil {
			rows.Close()
			return notify.Retryable{Err: err}
		}
		subs = append(subs, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return notify.Retryable{Err: err}
	}
	rows.Close()

	if len(subs) == 0 {
		return notify.Permanent{Err: errors.New("немає підписок push")}
	}
	var lastRetryable error
	delivered := false
	for _, sub := range subs {
		err := sender.Send(ctx, notify.Target{Push: sub.Subscription}, message)
		switch {
		case err == nil:
			delivered = true
		case notify.IsPermanent(err):
			if _, dbErr := s.db.ExecContext(ctx,
				"UPDATE push_subscriptions SET revoked_at=? WHERE id=?", s.nowStamp(), sub.id); dbErr != nil {
				s.log.Warn("Не вдалося відкликати підписку push", "помилка", dbErr)
			}
		default:
			lastRetryable = err
		}
	}
	if delivered {
		return nil
	}
	if lastRetryable != nil {
		return lastRetryable
	}
	return notify.Permanent{Err: errors.New("усі підписки push відкликано")}
}

// linkTelegramChat прив'язує чат до помічника за одноразовим кодом із посилання.
func (s *Server) linkTelegramChat(ctx context.Context, code, chatID string) (string, bool) {
	code = strings.TrimSpace(code)
	if code == "" {
		return "", false
	}
	var linkID, helperID, name, expires string
	err := s.db.QueryRowContext(ctx, `
		SELECT t.id,t.helper_id,h.display_name,COALESCE(t.expires_at,'')
		FROM telegram_links t JOIN helpers h ON h.id=t.helper_id
		WHERE t.link_code_hash=? AND t.revoked_at IS NULL AND t.chat_id IS NULL`,
		security.Hash(code)).Scan(&linkID, &helperID, &name, &expires)
	if err != nil {
		if !isNoRows(err) {
			s.log.Warn("Не вдалося знайти код Telegram", "помилка", err)
		}
		return "", false
	}
	if expires != "" && expires < s.nowStamp() {
		return "", false
	}
	// Один помічник — один чат: попередні прив'язки відкликаємо.
	if _, err := s.db.ExecContext(ctx,
		"UPDATE telegram_links SET revoked_at=? WHERE helper_id=? AND id<>? AND revoked_at IS NULL",
		s.nowStamp(), helperID, linkID); err != nil {
		s.log.Warn("Не вдалося відкликати попередні прив'язки Telegram", "помилка", err)
	}
	if _, err := s.db.ExecContext(ctx,
		"UPDATE telegram_links SET chat_id=?,link_code_hash=NULL WHERE id=?", chatID, linkID); err != nil {
		s.logError("Не вдалося прив'язати чат Telegram", err)
		return "", false
	}
	s.audit(ctx, "", helperID, "підключено", "telegram", linkID)
	return name, true
}

// pendingNotifications повертає кількість повідомлень у черзі. Використовується у тестах.
func (s *Server) pendingNotifications(ctx context.Context, status string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM notifications WHERE status=?", status).Scan(&count)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return count, err
}
