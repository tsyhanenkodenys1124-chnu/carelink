package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"doglyad/backend/internal/notify"
)

// recordingSender запам'ятовує доставлені повідомлення та може повертати задану помилку.
type recordingSender struct {
	mu       sync.Mutex
	name     string
	messages []notify.Message
	fail     error
}

func (r *recordingSender) Name() string { return r.name }

func (r *recordingSender) Send(_ context.Context, _ notify.Target, message notify.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil {
		return r.fail
	}
	r.messages = append(r.messages, message)
	return nil
}

func (r *recordingSender) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.messages)
}

func TestDispatcherDeliversAndMarksSent(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")
	client.createTask(wardID, "Обід", "food", "13:00", false)
	token := client.pairDevice(wardID)

	sender := &recordingSender{name: notify.ChannelEmail}
	client.server.senders[notify.ChannelEmail] = sender
	// Реєстрація вже поставила в чергу лист підтвердження — прибираємо його,
	// щоб перевіряти саме сигнал про допомогу.
	client.clearQueue()

	response := client.deviceDo(http.MethodPost, "/device/help", token, map[string]string{"message": "Потрібна вода"})
	client.expect(response, http.StatusCreated)

	client.server.deliverBatch(context.Background())

	if sender.count() == 0 {
		t.Fatal("повідомлення не доставлено поштою")
	}
	if !strings.Contains(sender.messages[0].Body, "Потрібна вода") {
		t.Fatalf("текст повідомлення втрачено: %+v", sender.messages[0])
	}
	if !sender.messages[0].Urgent {
		t.Fatal("запит про допомогу має бути позначений як терміновий")
	}

	var status string
	if err := client.server.db.QueryRow(
		"SELECT status FROM notifications WHERE channel=? ORDER BY created_at DESC LIMIT 1",
		notify.ChannelEmail).Scan(&status); err != nil {
		t.Fatalf("читання стану доставки: %v", err)
	}
	if status != statusSent {
		t.Fatalf("очікувався стан %q, отримано %q", statusSent, status)
	}
}

func TestDispatcherRetriesTemporaryFailure(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	helperID := client.helperID()

	client.server.senders[notify.ChannelEmail] = &recordingSender{
		name: notify.ChannelEmail,
		fail: notify.Retryable{Err: errors.New("поштовий сервер недоступний")},
	}
	client.server.enqueue(context.Background(), helperID, "", notify.ChannelEmail, notify.KindHelp,
		notify.Message{Kind: notify.KindHelp, Title: "Перевірка"})

	client.server.deliverBatch(context.Background())

	var status, nextAttempt string
	var attempt int
	if err := client.server.db.QueryRow(
		"SELECT status,attempt,COALESCE(next_attempt_at,'') FROM notifications ORDER BY created_at DESC LIMIT 1").
		Scan(&status, &attempt, &nextAttempt); err != nil {
		t.Fatalf("читання черги: %v", err)
	}
	if status != statusCreated {
		t.Fatalf("тимчасова помилка має лишити повідомлення в черзі, стан %q", status)
	}
	if attempt != 1 {
		t.Fatalf("очікувалася одна спроба, отримано %d", attempt)
	}
	if nextAttempt <= client.server.nowStamp() {
		t.Fatal("наступну спробу не відкладено")
	}
}

func TestDispatcherStopsOnPermanentFailure(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	helperID := client.helperID()

	client.server.senders[notify.ChannelEmail] = &recordingSender{
		name: notify.ChannelEmail,
		fail: notify.Permanent{Err: errors.New("адресу видалено")},
	}
	client.server.enqueue(context.Background(), helperID, "", notify.ChannelEmail, notify.KindHelp,
		notify.Message{Kind: notify.KindHelp, Title: "Перевірка"})

	client.server.deliverBatch(context.Background())

	var status string
	if err := client.server.db.QueryRow(
		"SELECT status FROM notifications ORDER BY created_at DESC LIMIT 1").Scan(&status); err != nil {
		t.Fatalf("читання черги: %v", err)
	}
	if status != statusFailed {
		t.Fatalf("стала помилка має завершити доставку, стан %q", status)
	}
}

func TestQuietHoursSkipRoutineButNotUrgent(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")

	// Тихі години на цілу добу: звичайні сповіщення не мають проходити взагалі.
	prefs := client.do(http.MethodPut, "/me/notifications", []map[string]any{
		{"channel": "email", "enabled": true, "quietStart": "00:00", "quietEnd": "23:59"},
		{"channel": "push", "enabled": false},
		{"channel": "telegram", "enabled": false},
	})
	client.expect(prefs, http.StatusNoContent)
	client.clearQueue()

	client.server.notifyWard(context.Background(), wardID,
		notify.Message{Kind: notify.KindReminder, Title: "Нагадування", Urgent: false}, false)
	routine, err := client.server.pendingNotifications(context.Background(), statusCreated)
	if err != nil {
		t.Fatalf("черга: %v", err)
	}
	if routine != 0 {
		t.Fatalf("звичайне нагадування пройшло крізь тихі години: %d", routine)
	}

	client.server.notifyWard(context.Background(), wardID,
		notify.Message{Kind: notify.KindHelp, Title: "Потрібна допомога", Urgent: true}, false)
	urgent, err := client.server.pendingNotifications(context.Background(), statusCreated)
	if err != nil {
		t.Fatalf("черга: %v", err)
	}
	if urgent != 1 {
		t.Fatalf("терміновий сигнал має обійти тихі години, у черзі %d", urgent)
	}
}

func TestEventStreamSendsSignalToPanel(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")

	request := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	request.AddCookie(client.cookie)
	ctx, cancel := context.WithCancel(request.Context())
	request = request.WithContext(ctx)
	recorder := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		client.handler.ServeHTTP(recorder, request)
		close(done)
	}()

	// Чекаємо, поки потік зареєструється, і надсилаємо подію.
	deadline := time.Now().Add(3 * time.Second)
	for client.server.hub.Subscribers() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if client.server.hub.Subscribers() == 0 {
		cancel()
		t.Fatal("потік подій не відкрився")
	}
	client.server.publish(context.Background(), wardID, EventHelp, "Потрібна допомога", true, nil)
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done

	body := recorder.Body.String()
	if !strings.Contains(body, "event: "+EventHelp) || !strings.Contains(body, "Потрібна допомога") {
		t.Fatalf("подія не дійшла до панелі: %q", body)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("неправильний тип потоку: %q", got)
	}
}

func TestEventStreamIgnoresForeignWard(t *testing.T) {
	owner := newClient(t)
	owner.registerHelper("olena@doglyad.local")
	wardID := owner.createWard("Маріє Іванівно")

	stranger := &testClient{t: t, server: owner.server, handler: owner.handler}
	stranger.registerHelper("petro@doglyad.local")

	// Передплатник стороннього помічника не має отримати подію чужого підопічного.
	subscriber := owner.server.hub.Subscribe(stranger.helperID())
	defer owner.server.hub.Unsubscribe(subscriber)

	owner.server.publish(context.Background(), wardID, EventHelp, "Потрібна допомога", true, nil)
	select {
	case event := <-subscriber.events:
		t.Fatalf("подію отримав сторонній підписник: %+v", event)
	case <-time.After(300 * time.Millisecond):
	}
}

// clearQueue прибирає з черги листи, створені під час підготовки тесту.
func (c *testClient) clearQueue() {
	c.t.Helper()
	if _, err := c.server.db.Exec("DELETE FROM notifications"); err != nil {
		c.t.Fatalf("очищення черги: %v", err)
	}
}

// helperID повертає ідентифікатор поточного помічника.
func (c *testClient) helperID() string {
	c.t.Helper()
	response := c.do(http.MethodGet, "/me", nil)
	c.expect(response, http.StatusOK)
	var me struct {
		ID string `json:"id"`
	}
	c.decode(response, &me)
	return me.ID
}

func TestEscalationRepeatsWhenNobodyResponds(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")

	// Годинник зупинено на конкретній миті, а завдання одноразове: інакше результат
	// залежав би від часу доби, коли запускають тести, і від сусідніх виконань.
	kyiv, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Fatalf("часовий пояс: %v", err)
	}
	moment := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	client.server.clock = func() time.Time { return moment }

	// Справа о 08:00 за Києвом — на чотири години раніше за поточний час.
	created := client.do(http.MethodPost, "/wards/"+wardID+"/tasks", map[string]any{
		"title": "Ранкові ліки", "category": "medicine", "critical": true,
		"localStartDate": moment.In(kyiv).Format("2006-01-02"),
		"localTime":      "08:00", "recurrenceKind": "once",
		"completionWindowMinutes": 120, "escalationWindowMinutes": 30,
	})
	client.expect(created, http.StatusCreated)

	client.server.runCycle(context.Background())

	var repeated string
	if err := client.server.db.QueryRow(
		"SELECT COALESCE(repeated_at,'') FROM escalations WHERE ward_id=? LIMIT 1", wardID).Scan(&repeated); err != nil {
		t.Fatalf("читання ескалації: %v", err)
	}
	if repeated != "" {
		t.Fatal("повтор надійшов одразу, хоча мав чекати чверть години")
	}

	// Ще двадцять хвилин без реакції — сигнал має повторитися рівно один раз.
	client.server.clock = func() time.Time { return moment.Add(20 * time.Minute) }
	client.clearQueue()
	client.server.runCycle(context.Background())

	if err := client.server.db.QueryRow(
		"SELECT COALESCE(repeated_at,'') FROM escalations WHERE ward_id=? LIMIT 1", wardID).Scan(&repeated); err != nil {
		t.Fatalf("читання ескалації: %v", err)
	}
	if repeated == "" {
		t.Fatal("ескалацію не повторено")
	}
	first, err := client.server.pendingNotifications(context.Background(), statusCreated)
	if err != nil {
		t.Fatalf("черга: %v", err)
	}
	if first == 0 {
		t.Fatal("повторне сповіщення не поставлено в чергу")
	}

	client.clearQueue()
	client.server.clock = func() time.Time { return moment.Add(time.Hour) }
	client.server.runCycle(context.Background())
	again, err := client.server.pendingNotifications(context.Background(), statusCreated)
	if err != nil {
		t.Fatalf("черга: %v", err)
	}
	if again != 0 {
		t.Fatalf("сигнал повторився більше одного разу: %d", again)
	}
}

// Помічник створює підопічного вже після відкриття панелі: події нового профілю
// мають приходити без перезавантаження сторінки.
func TestEventStreamCoversWardCreatedAfterSubscribe(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")

	subscriber := client.server.hub.Subscribe(client.helperID())
	defer client.server.hub.Unsubscribe(subscriber)

	wardID := client.createWard("Маріє Іванівно")
	client.server.publish(context.Background(), wardID, EventHelp, "Потрібна допомога", true, nil)

	select {
	case event := <-subscriber.events:
		if event.WardID != wardID {
			t.Fatalf("подія про чужого підопічного: %+v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("подія нового підопічного не дійшла до відкритої панелі")
	}
}

// Після відкликання доступу помічник не має отримувати події цього підопічного.
func TestEventStreamStopsAfterAccessRevoked(t *testing.T) {
	owner := newClient(t)
	owner.registerHelper("olena@doglyad.local")
	wardID := owner.createWard("Маріє Іванівно")

	invitation := owner.do(http.MethodPost, "/wards/"+wardID+"/links", map[string]any{
		"email": "petro@doglyad.local", "editTasks": true,
	})
	owner.expect(invitation, http.StatusCreated)
	var created struct {
		Token string `json:"token"`
	}
	owner.decode(invitation, &created)

	invitee := &testClient{t: t, server: owner.server, handler: owner.handler}
	invitee.registerHelper("petro@doglyad.local")
	accepted := invitee.do(http.MethodPost, "/invitations/accept", map[string]string{"token": created.Token})
	invitee.expect(accepted, http.StatusOK)

	subscriber := owner.server.hub.Subscribe(invitee.helperID())
	defer owner.server.hub.Unsubscribe(subscriber)

	// Поки доступ є, подія приходить.
	owner.server.publish(context.Background(), wardID, EventHelp, "Потрібна допомога", true, nil)
	select {
	case <-subscriber.events:
	case <-time.After(2 * time.Second):
		t.Fatal("запрошений помічник не отримав подію")
	}

	links := owner.do(http.MethodGet, "/wards/"+wardID+"/links", nil)
	owner.expect(links, http.StatusOK)
	var list []struct {
		ID     string `json:"id"`
		Rights struct {
			Primary bool `json:"primary"`
		} `json:"rights"`
	}
	owner.decode(links, &list)
	for _, link := range list {
		if link.Rights.Primary {
			continue
		}
		revoked := owner.do(http.MethodDelete, "/wards/"+wardID+"/links/"+link.ID, nil)
		owner.expect(revoked, http.StatusNoContent)
	}

	owner.server.publish(context.Background(), wardID, EventHelp, "Потрібна допомога", true, nil)
	select {
	case event := <-subscriber.events:
		t.Fatalf("подія дійшла до помічника без доступу: %+v", event)
	case <-time.After(500 * time.Millisecond):
	}
}

// TestReminderShowsWardLocalTime перевіряє, що в тексті нагадування стоїть час
// підопічного, а не час сервера. Баг був помітний лише в журналі: справа о 08:00
// за Києвом приходила помічникові як «Заплановано на 05:00».
func TestReminderShowsWardLocalTime(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")

	kyiv, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Fatalf("часовий пояс: %v", err)
	}
	// Полудень за UTC — 15:00 за Києвом, тож нагадування про справу о 08:00
	// уже мало піти, а різниця між поясами видна в тексті.
	moment := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	client.server.clock = func() time.Time { return moment }

	created := client.do(http.MethodPost, "/wards/"+wardID+"/tasks", map[string]any{
		"title": "Ранкові ліки", "category": "medicine", "critical": true,
		"localStartDate": moment.In(kyiv).Format("2006-01-02"),
		"localTime":      "08:00", "recurrenceKind": "once",
		"completionWindowMinutes": 120, "escalationWindowMinutes": 30,
	})
	client.expect(created, http.StatusCreated)

	client.clearQueue()
	client.server.runCycle(context.Background())

	bodies := client.queuedBodies("Нагадування: Ранкові ліки")
	if len(bodies) == 0 {
		t.Fatal("нагадування не поставлено в чергу")
	}
	for _, body := range bodies {
		if !strings.Contains(body, "Заплановано на 08:00.") {
			t.Fatalf("у тексті нагадування чужий час: %q", body)
		}
	}
}

// TestWardLocationFallsBackToUTC перевіряє, що зламаний пояс у базі не валить
// сервер і не підставляє випадковий час: лишається UTC і запис у журналі.
func TestWardLocationFallsBackToUTC(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")

	if _, err := client.server.db.Exec(
		"UPDATE wards SET timezone='Mars/Olympus' WHERE id=?", wardID); err != nil {
		t.Fatalf("псування поясу: %v", err)
	}
	if location := client.server.wardLocation(context.Background(), wardID); location != time.UTC {
		t.Fatalf("очікували UTC, маємо %v", location)
	}
	if location := client.server.wardLocation(context.Background(), "немає такого"); location != time.UTC {
		t.Fatalf("для невідомого підопічного очікували UTC, маємо %v", location)
	}
}

// queuedBodies повертає тексти листів у черзі, у яких збігся заголовок.
func (c *testClient) queuedBodies(title string) []string {
	c.t.Helper()
	rows, err := c.server.db.Query("SELECT payload_json FROM notifications")
	if err != nil {
		c.t.Fatalf("читання черги: %v", err)
	}
	// Payload читаємо у зріз і лише потім розбираємо: запит усередині відкритого
	// курсора на одному з'єднанні SQLite заблокував би сервер.
	var payloads []string
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			rows.Close()
			c.t.Fatalf("читання листа: %v", err)
		}
		payloads = append(payloads, payload)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		c.t.Fatalf("перебір черги: %v", err)
	}
	rows.Close()

	var bodies []string
	for _, payload := range payloads {
		var message notify.Message
		if err := json.Unmarshal([]byte(payload), &message); err != nil {
			c.t.Fatalf("розбір листа: %v", err)
		}
		if message.Title == title {
			bodies = append(bodies, message.Body)
		}
	}
	return bodies
}

// TestAdaptationSignalsUseWardDate перевіряє, що добові сигнали зручності екрана
// датуються за годинником підопічного. За UTC усе до третьої ночі в Києві
// зараховувалося б у попередню добу й зсувало б дані дослідження.
func TestAdaptationSignalsUseWardDate(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")
	token := client.pairDevice(wardID)

	// 23:30 за UTC — це вже 02:30 наступної доби за Києвом.
	moment := time.Date(2026, 6, 15, 23, 30, 0, 0, time.UTC)
	client.server.clock = func() time.Time { return moment }

	response := client.deviceDo(http.MethodPost, "/device/signals", token, map[string]any{
		"magnifierUses": 2, "quickUndos": 1,
	})
	client.expect(response, http.StatusNoContent)

	var date string
	if err := client.server.db.QueryRow(
		"SELECT date FROM adaptation_signals WHERE ward_id=?", wardID).Scan(&date); err != nil {
		t.Fatalf("читання сигналів: %v", err)
	}
	if date != "2026-06-16" {
		t.Fatalf("сигнал зарахований не в ту добу: %s", date)
	}
}

// TestLogErrorKeepsCancellationOutOfErrors перевіряє, що штатна зупинка сервера
// не лишає в журналі рядків рівня ERROR: інакше після кожного перезапуску
// журнал повний «помилок», і справжню поломку в ньому не видно.
func TestLogErrorKeepsCancellationOutOfErrors(t *testing.T) {
	client := newClient(t)

	var written strings.Builder
	client.server.log = slog.New(slog.NewTextHandler(&written, &slog.HandlerOptions{Level: slog.LevelInfo}))

	client.server.logError("Не вдалося прочитати нагадування", context.Canceled)
	if strings.Contains(written.String(), "level=ERROR") {
		t.Fatalf("скасування контексту потрапило в помилки: %s", written.String())
	}
	if !strings.Contains(written.String(), "роботу перервано") {
		t.Fatalf("скасування не згадане в журналі: %s", written.String())
	}

	written.Reset()
	client.server.logError("Не вдалося прочитати нагадування", errors.New("база недоступна"), "завдання", "t-1")
	log := written.String()
	if !strings.Contains(log, "level=ERROR") {
		t.Fatalf("справжню помилку приховано: %s", log)
	}
	if !strings.Contains(log, "завдання=t-1") || !strings.Contains(log, "база недоступна") {
		t.Fatalf("журнал втратив подробиці: %s", log)
	}
}
