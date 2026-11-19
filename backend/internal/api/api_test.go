package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"doglyad/backend/internal/config"
	"doglyad/backend/internal/notify"
	"doglyad/backend/internal/security"
	"doglyad/backend/internal/store"
)

// testClient тримає сервер, cookie сесії та токен CSRF між запитами.
type testClient struct {
	t       *testing.T
	server  *Server
	handler http.Handler
	cookie  *http.Cookie
	csrf    string
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	db, err := store.OpenMemory(security.ID())
	if err != nil {
		t.Fatalf("база в пам'яті: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	cfg := config.Load()
	cfg.MediaDir = t.TempDir()
	cfg.WebDir = t.TempDir()
	cfg.PublicURL = "http://localhost:8080"
	cfg.UndoWindow = 90 * time.Second

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	server, err := New(db, cfg, log)
	if err != nil {
		t.Fatalf("сервер: %v", err)
	}
	return server
}

func newClient(t *testing.T) *testClient {
	t.Helper()
	server := newTestServer(t)
	return &testClient{t: t, server: server, handler: server.Handler()}
}

// do виконує запит до API та повертає відповідь.
func (c *testClient) do(method, path string, body any) *httptest.ResponseRecorder {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("серіалізація тіла: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	request := httptest.NewRequest(method, "/api"+path, reader)
	request.Header.Set("Content-Type", "application/json")
	if c.cookie != nil {
		request.AddCookie(c.cookie)
	}
	if c.csrf != "" {
		request.Header.Set("X-CSRF-Token", c.csrf)
	}
	recorder := httptest.NewRecorder()
	c.handler.ServeHTTP(recorder, request)
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == sessionCookie && cookie.Value != "" {
			c.cookie = cookie
		}
	}
	return recorder
}

// deviceDo виконує запит від імені пристрою підопічного.
func (c *testClient) deviceDo(method, path, token string, body any) *httptest.ResponseRecorder {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("серіалізація тіла: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	request := httptest.NewRequest(method, "/api"+path, reader)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Device-Token", token)
	recorder := httptest.NewRecorder()
	c.handler.ServeHTTP(recorder, request)
	return recorder
}

func (c *testClient) decode(recorder *httptest.ResponseRecorder, dst any) {
	c.t.Helper()
	if err := json.Unmarshal(recorder.Body.Bytes(), dst); err != nil {
		c.t.Fatalf("розбір відповіді %q: %v", recorder.Body.String(), err)
	}
}

func (c *testClient) expect(recorder *httptest.ResponseRecorder, code int) {
	c.t.Helper()
	if recorder.Code != code {
		c.t.Fatalf("очікувався код %d, отримано %d: %s", code, recorder.Code, recorder.Body.String())
	}
}

// registerHelper створює помічника та зберігає токен CSRF.
func (c *testClient) registerHelper(email string) {
	c.t.Helper()
	response := c.do(http.MethodPost, "/auth/register", map[string]string{
		"email": email, "password": "DemoCare2026!", "name": "Олена", "timezone": "Europe/Kyiv",
	})
	c.expect(response, http.StatusCreated)
	c.refreshCSRF()
}

func (c *testClient) refreshCSRF() {
	c.t.Helper()
	response := c.do(http.MethodGet, "/me", nil)
	c.expect(response, http.StatusOK)
	var me struct {
		CSRFToken string `json:"csrfToken"`
	}
	c.decode(response, &me)
	c.csrf = me.CSRFToken
}

// createWard створює профіль підопічного.
func (c *testClient) createWard(name string) string {
	c.t.Helper()
	response := c.do(http.MethodPost, "/wards", map[string]string{
		"addressName": name, "timezone": "Europe/Kyiv", "emergencyPhone": "103",
	})
	c.expect(response, http.StatusCreated)
	var created struct {
		ID string `json:"id"`
	}
	c.decode(response, &created)
	return created.ID
}

// createTask створює щоденне завдання на вказаний час.
func (c *testClient) createTask(wardID, title, category, clock string, critical bool) string {
	c.t.Helper()
	response := c.do(http.MethodPost, "/wards/"+wardID+"/tasks", map[string]any{
		"title": title, "category": category, "critical": critical,
		"localStartDate": time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02"),
		"localTime":      clock, "recurrenceKind": "daily",
		"completionWindowMinutes": 120, "escalationWindowMinutes": 30,
		"reminderRepeats": 1, "reminderIntervalMinutes": 10,
	})
	c.expect(response, http.StatusCreated)
	var created struct {
		ID string `json:"id"`
	}
	c.decode(response, &created)
	return created.ID
}

// createDueTask створює завдання, час якого щойно настав.
//
// Потрібне окремо від createTask: справу, до якої ще години, відмітити не можна
// (інакше завтрашні ліки можна було б позначити прийнятими сьогодні ввечері).
// Тести, які перевіряють саму відмітку, мають працювати о будь-якій годині доби,
// тому час рахується від поточного, а не задається константою.
func (c *testClient) createDueTask(wardID, title, category string, critical bool) string {
	c.t.Helper()
	kyiv, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		c.t.Fatalf("часовий пояс: %v", err)
	}
	// П'ять хвилин тому: вікно вже відкрите, але до кінця виконання ще далеко.
	due := c.server.now().In(kyiv).Add(-5 * time.Minute)
	response := c.do(http.MethodPost, "/wards/"+wardID+"/tasks", map[string]any{
		"title": title, "category": category, "critical": critical,
		"localStartDate": due.Format("2006-01-02"),
		"localTime":      due.Format("15:04"), "recurrenceKind": "daily",
		"completionWindowMinutes": 120, "escalationWindowMinutes": 30,
		"reminderRepeats": 1, "reminderIntervalMinutes": 10,
	})
	c.expect(response, http.StatusCreated)
	var created struct {
		ID string `json:"id"`
	}
	c.decode(response, &created)
	return created.ID
}

// pairDevice створює код підключення та обмінює його на токен пристрою.
func (c *testClient) pairDevice(wardID string) string {
	c.t.Helper()
	response := c.do(http.MethodPost, "/wards/"+wardID+"/pairing", map[string]any{})
	c.expect(response, http.StatusCreated)
	var pairing struct {
		Code string `json:"code"`
		QR   string `json:"qr"`
	}
	c.decode(response, &pairing)
	if len(pairing.Code) != 6 {
		c.t.Fatalf("очікувався шестизначний код, отримано %q", pairing.Code)
	}
	if !strings.HasPrefix(pairing.QR, "data:image/png;base64,") {
		c.t.Fatal("код підключення не має QR-зображення")
	}

	paired := c.do(http.MethodPost, "/pairing", map[string]string{
		"code": pairing.Code, "label": "Планшет",
	})
	c.expect(paired, http.StatusCreated)
	var device struct {
		DeviceToken string `json:"deviceToken"`
	}
	c.decode(paired, &device)
	return device.DeviceToken
}

func TestSchedulerDoesNotBlockSingleConnection(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")
	client.createTask(wardID, "Ранкові ліки", "medicine", "08:00", true)

	done := make(chan struct{})
	go func() {
		client.server.runCycle(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("планувальник заблокував єдине з'єднання SQLite")
	}

	var count int
	if err := client.server.db.QueryRow("SELECT COUNT(*) FROM task_occurrences").Scan(&count); err != nil {
		t.Fatalf("підрахунок виконань: %v", err)
	}
	if count == 0 {
		t.Fatal("планувальник не створив жодного виконання")
	}
}

func TestDeviceScreenReturnsFullOccurrences(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")
	taskID := client.createTask(wardID, "Ранкові ліки", "medicine", "08:00", true)
	// Порожній опис колись повертався з бази як NULL і ламав екран пристрою.
	// Тепер схема цього не дозволяє: перевіряємо і обмеження, і порожній рядок.
	if _, err := client.server.db.Exec("UPDATE tasks SET description=NULL WHERE id=?", taskID); err == nil {
		t.Fatal("схема дозволила NULL в описі завдання")
	}
	if _, err := client.server.db.Exec("UPDATE tasks SET description='' WHERE id=?", taskID); err != nil {
		t.Fatalf("порожній опис: %v", err)
	}
	token := client.pairDevice(wardID)

	response := client.deviceDo(http.MethodGet, "/device/today", token, nil)
	client.expect(response, http.StatusOK)
	var screen struct {
		Ward struct {
			Name string `json:"name"`
		} `json:"ward"`
		Occurrences []struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			Status      string `json:"status"`
			ScheduledAt string `json:"scheduledAt"`
			Deadline    string `json:"deadline"`
		} `json:"occurrences"`
	}
	client.decode(response, &screen)
	if screen.Ward.Name != "Маріє Іванівно" {
		t.Fatalf("неправильне звертання: %q", screen.Ward.Name)
	}
	if len(screen.Occurrences) == 0 {
		t.Fatal("екран пристрою не показав жодної справи")
	}
	for _, item := range screen.Occurrences {
		if item.ID == "" || item.Title == "" || item.Status == "" || item.ScheduledAt == "" || item.Deadline == "" {
			t.Fatalf("виконання повернулося з порожніми полями: %+v", item)
		}
	}
}

func TestDeviceMarkIsIdempotentAndBlocksDoubleMedicine(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")
	client.createDueTask(wardID, "Ранкові ліки", "medicine", true)
	token := client.pairDevice(wardID)

	occurrenceID := client.firstPlannedOccurrence(token)
	key := security.Token()
	body := map[string]string{
		"occurrenceID": occurrenceID, "idempotencyKey": key,
		"deviceTime": time.Now().UTC().Format(time.RFC3339),
	}
	first := client.deviceDo(http.MethodPost, "/device/mark", token, body)
	client.expect(first, http.StatusOK)

	// Повтор із тим самим ключем не повинен створювати другий запис.
	repeat := client.deviceDo(http.MethodPost, "/device/mark", token, body)
	client.expect(repeat, http.StatusOK)

	// Нова спроба з іншим ключем має отримати захист від повторного приймання ліків.
	second := client.deviceDo(http.MethodPost, "/device/mark", token, map[string]string{
		"occurrenceID": occurrenceID, "idempotencyKey": security.Token(),
		"deviceTime": time.Now().UTC().Format(time.RFC3339),
	})
	client.expect(second, http.StatusConflict)
	if !strings.Contains(second.Body.String(), "вже позначено") {
		t.Fatalf("очікувалося попередження про повторне приймання: %s", second.Body.String())
	}
}

func TestDeviceUndoRestoresPlannedStatus(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")
	client.createDueTask(wardID, "Обід", "food", false)
	token := client.pairDevice(wardID)

	occurrenceID := client.firstPlannedOccurrence(token)
	marked := client.deviceDo(http.MethodPost, "/device/mark", token, map[string]string{
		"occurrenceID": occurrenceID, "idempotencyKey": security.Token(),
		"deviceTime": time.Now().UTC().Format(time.RFC3339),
	})
	client.expect(marked, http.StatusOK)

	undone := client.deviceDo(http.MethodPost, "/device/undo", token, map[string]string{
		"occurrenceID": occurrenceID,
	})
	client.expect(undone, http.StatusOK)

	var status string
	if err := client.server.db.QueryRow("SELECT status FROM task_occurrences WHERE id=?", occurrenceID).Scan(&status); err != nil {
		t.Fatalf("читання статусу: %v", err)
	}
	if status != "planned" {
		t.Fatalf("після скасування очікувався статус planned, отримано %q", status)
	}
}

// firstPlannedOccurrence повертає найближчу заплановану справу з екрана пристрою.
func (c *testClient) firstPlannedOccurrence(token string) string {
	c.t.Helper()
	response := c.deviceDo(http.MethodGet, "/device/today", token, nil)
	c.expect(response, http.StatusOK)
	var screen struct {
		Occurrences []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"occurrences"`
	}
	c.decode(response, &screen)
	for _, item := range screen.Occurrences {
		if item.Status == "planned" {
			return item.ID
		}
	}
	c.t.Fatal("немає запланованих справ")
	return ""
}

func TestHelpRequestAppearsInHelperPanel(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")
	client.createTask(wardID, "Обід", "food", "13:00", false)
	token := client.pairDevice(wardID)

	// Панель помічника має отримати подію в реальному часі.
	subscriber := client.server.hub.Subscribe(client.helperID())
	defer client.server.hub.Unsubscribe(subscriber)

	response := client.deviceDo(http.MethodPost, "/device/help", token, map[string]string{
		"message": "Не можу відкрити банку",
	})
	client.expect(response, http.StatusCreated)

	select {
	case event := <-subscriber.events:
		if event.Type != EventHelp {
			t.Fatalf("очікувалася подія допомоги, отримано %q", event.Type)
		}
		if !event.Urgent {
			t.Fatal("сигнал про допомогу має бути терміновим")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("панель помічника не отримала подію про допомогу")
	}

	// Той самий сигнал має бути у списку відкритих сигналів.
	signals := client.do(http.MethodGet, "/signals", nil)
	client.expect(signals, http.StatusOK)
	var list []signal
	client.decode(signals, &list)
	if len(list) != 1 {
		t.Fatalf("очікувався один відкритий сигнал, отримано %d: %s", len(list), signals.Body.String())
	}
	if list[0].Type != EventHelp || list[0].Message != "Не можу відкрити банку" {
		t.Fatalf("сигнал повернувся неповним: %+v", list[0])
	}
	if list[0].WardName != "Маріє Іванівно" {
		t.Fatalf("сигнал без імені підопічного: %+v", list[0])
	}

	// Повторне натискання не створює другий запит.
	again := client.deviceDo(http.MethodPost, "/device/help", token, map[string]string{"message": ""})
	client.expect(again, http.StatusOK)

	// Сигнал має потрапити в чергу доставки всіма каналами.
	pending, err := client.server.pendingNotifications(context.Background(), statusCreated)
	if err != nil {
		t.Fatalf("черга доставки: %v", err)
	}
	if pending < 3 {
		t.Fatalf("очікувалася доставка трьома каналами, у черзі %d", pending)
	}

	// Помічник закриває сигнал.
	closed := client.do(http.MethodPost, "/help-requests/"+list[0].ID+"/close", nil)
	client.expect(closed, http.StatusNoContent)
	after := client.do(http.MethodGet, "/signals", nil)
	client.expect(after, http.StatusOK)
	var remaining []signal
	client.decode(after, &remaining)
	if len(remaining) != 0 {
		t.Fatalf("після закриття сигнал лишився: %+v", remaining)
	}
}

func TestUnsureRequestAnswerMarksTask(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")
	client.createTask(wardID, "Ранкові ліки", "medicine", "08:00", true)
	token := client.pairDevice(wardID)
	occurrenceID := client.firstPlannedOccurrence(token)

	asked := client.deviceDo(http.MethodPost, "/device/unsure", token, map[string]string{
		"occurrenceID": occurrenceID,
	})
	client.expect(asked, http.StatusCreated)

	signals := client.do(http.MethodGet, "/signals", nil)
	client.expect(signals, http.StatusOK)
	var list []signal
	client.decode(signals, &list)
	var requestID string
	for _, item := range list {
		if item.Type == EventUnsure {
			requestID = item.ID
		}
	}
	if requestID == "" {
		t.Fatalf("питання підопічного не потрапило в панель: %s", signals.Body.String())
	}

	answered := client.do(http.MethodPost, "/unsure-requests/"+requestID+"/answer", map[string]any{
		"answer": "Так, ви вже прийняли. Більше не треба.", "markAsDone": true,
	})
	client.expect(answered, http.StatusNoContent)

	var status string
	if err := client.server.db.QueryRow("SELECT status FROM task_occurrences WHERE id=?", occurrenceID).Scan(&status); err != nil {
		t.Fatalf("читання статусу: %v", err)
	}
	if status != "done" {
		t.Fatalf("після відповіді справа мала стати виконаною, отримано %q", status)
	}

	// Відповідь має дійти до екрана підопічного.
	screen := client.deviceDo(http.MethodGet, "/device/today", token, nil)
	client.expect(screen, http.StatusOK)
	if !strings.Contains(screen.Body.String(), "Більше не треба") {
		t.Fatalf("екран пристрою не показав відповідь помічника: %s", screen.Body.String())
	}
}

func TestEscalationOpensForCriticalTask(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")
	client.createTask(wardID, "Вечірні ліки", "medicine", "20:00", true)

	// Переносимо годинник уперед: вікно ескалації має минути.
	client.server.clock = func() time.Time { return time.Now().UTC().Add(48 * time.Hour) }
	client.server.runCycle(context.Background())

	var count int
	if err := client.server.db.QueryRow("SELECT COUNT(*) FROM escalations WHERE ward_id=?", wardID).Scan(&count); err != nil {
		t.Fatalf("підрахунок ескалацій: %v", err)
	}
	if count == 0 {
		t.Fatal("ескалацію не відкрито для критичної справи")
	}
}

func TestAcceptedSuggestionChangesSettings(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")

	// Сигнали за тиждень мають дати рекомендацію збільшити екран.
	if _, err := client.server.db.Exec(`
		INSERT INTO adaptation_signals(id,ward_id,date,viewport_zoom_sessions,created_at)
		VALUES(?,?,?,?,?)`,
		security.ID(), wardID, time.Now().Format("2006-01-02"), 4, client.server.nowStamp()); err != nil {
		t.Fatalf("сигнали адаптації: %v", err)
	}
	client.server.makeSuggestions(context.Background())

	response := client.do(http.MethodGet, "/suggestions?wardId="+wardID, nil)
	client.expect(response, http.StatusOK)
	var suggestions []struct {
		ID            string `json:"id"`
		Rule          string `json:"rule"`
		HumanProposal string `json:"humanProposal"`
	}
	client.decode(response, &suggestions)
	if len(suggestions) == 0 {
		t.Fatalf("рекомендацію не створено: %s", response.Body.String())
	}
	if suggestions[0].HumanProposal == "" {
		t.Fatal("рекомендація без людського опису")
	}

	accepted := client.do(http.MethodPost, "/suggestions/"+suggestions[0].ID+"/decision",
		map[string]string{"decision": "accepted"})
	client.expect(accepted, http.StatusNoContent)

	var scale int
	if err := client.server.db.QueryRow("SELECT scale FROM accessibility_settings WHERE ward_id=?", wardID).Scan(&scale); err != nil {
		t.Fatalf("читання масштабу: %v", err)
	}
	if scale != 125 {
		t.Fatalf("прийнята рекомендація не змінила масштаб: %d", scale)
	}

	// Ефект має зберегтися для звіту.
	var before, after string
	if err := client.server.db.QueryRow(
		"SELECT COALESCE(effect_before_json,''),COALESCE(effect_after_json,'') FROM adaptation_suggestions WHERE id=?",
		suggestions[0].ID).Scan(&before, &after); err != nil {
		t.Fatalf("читання ефекту: %v", err)
	}
	if before == "" || after == "" {
		t.Fatal("ефект рекомендації не збережено")
	}
}

func TestCSRFProtectsStateChanges(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	valid := client.csrf
	client.csrf = "підроблений"
	response := client.do(http.MethodPost, "/wards", map[string]string{"addressName": "Маріє"})
	client.expect(response, http.StatusForbidden)

	client.csrf = valid
	allowed := client.do(http.MethodPost, "/wards", map[string]string{"addressName": "Маріє"})
	client.expect(allowed, http.StatusCreated)
}

func TestForeignHelperHasNoAccess(t *testing.T) {
	owner := newClient(t)
	owner.registerHelper("olena@doglyad.local")
	wardID := owner.createWard("Маріє Іванівно")

	stranger := &testClient{t: t, server: owner.server, handler: owner.handler}
	stranger.registerHelper("petro@doglyad.local")

	response := stranger.do(http.MethodGet, "/wards/"+wardID, nil)
	stranger.expect(response, http.StatusForbidden)

	tasks := stranger.do(http.MethodGet, "/occurrences?wardId="+wardID, nil)
	stranger.expect(tasks, http.StatusForbidden)
}

func TestInvitationGrantsLimitedRights(t *testing.T) {
	owner := newClient(t)
	owner.registerHelper("olena@doglyad.local")
	wardID := owner.createWard("Маріє Іванівно")

	invitation := owner.do(http.MethodPost, "/wards/"+wardID+"/links", map[string]any{
		"email": "petro@doglyad.local", "editTasks": true, "editSettings": false, "escalations": true,
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

	// Право на завдання є.
	task := invitee.do(http.MethodPost, "/wards/"+wardID+"/tasks", map[string]any{
		"title": "Вечірні ліки", "category": "medicine",
		"localStartDate": time.Now().Format("2006-01-02"), "localTime": "20:00", "recurrenceKind": "daily",
	})
	invitee.expect(task, http.StatusCreated)

	// Права на налаштування немає.
	settings := invitee.do(http.MethodPut, "/wards/"+wardID+"/accessibility", map[string]any{
		"scale": 150, "palette": "standard", "magnifierScale": 2, "speechRate": 1,
	})
	invitee.expect(settings, http.StatusForbidden)
}

func TestTaskValidationRejectsLongTitle(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")

	response := client.do(http.MethodPost, "/wards/"+wardID+"/tasks", map[string]any{
		"title":    "Дуже довга назва завдання яка не вміщається у пʼять слів",
		"category": "other", "localStartDate": time.Now().Format("2006-01-02"),
		"localTime": "09:00", "recurrenceKind": "once",
	})
	client.expect(response, http.StatusBadRequest)
	if !strings.Contains(response.Body.String(), "п'ять слів") {
		t.Fatalf("очікувалося пояснення про довжину назви: %s", response.Body.String())
	}
}

func TestWeeklyRecurrenceRequiresWeekdays(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")

	response := client.do(http.MethodPost, "/wards/"+wardID+"/tasks", map[string]any{
		"title": "Візит лікаря", "category": "visit",
		"localStartDate": time.Now().Format("2006-01-02"), "localTime": "11:00",
		"recurrenceKind": "weekly", "recurrenceDays": []int{},
	})
	client.expect(response, http.StatusBadRequest)

	valid := client.do(http.MethodPost, "/wards/"+wardID+"/tasks", map[string]any{
		"title": "Візит лікаря", "category": "visit",
		"localStartDate": time.Now().Format("2006-01-02"), "localTime": "11:00",
		"recurrenceKind": "weekly", "recurrenceDays": []int{1, 4},
	})
	client.expect(valid, http.StatusCreated)
}

func TestPasswordResetChangesPasswordAndEndsSessions(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")

	requested := client.do(http.MethodPost, "/auth/password-reset", map[string]string{
		"email": "olena@doglyad.local",
	})
	client.expect(requested, http.StatusAccepted)

	// Лист із кодом має потрапити в чергу доставки.
	var payload string
	if err := client.server.db.QueryRow(
		"SELECT payload_json FROM notifications WHERE kind=? ORDER BY created_at DESC LIMIT 1",
		notify.KindResetMail).Scan(&payload); err != nil {
		t.Fatalf("лист відновлення не створено: %v", err)
	}
	message, err := notify.ParseMessage(payload)
	if err != nil {
		t.Fatalf("розбір листа: %v", err)
	}
	_, token, found := strings.Cut(message.URL, "token=")
	if !found || token == "" {
		t.Fatalf("у листі немає коду: %q", message.URL)
	}

	confirmed := client.do(http.MethodPost, "/auth/password-reset/confirm", map[string]string{
		"token": token, "password": "NovyiParol2026!",
	})
	client.expect(confirmed, http.StatusOK)

	// Старий сеанс має завершитися.
	after := client.do(http.MethodGet, "/me", nil)
	client.expect(after, http.StatusUnauthorized)

	// Повторне використання коду заборонене.
	repeat := client.do(http.MethodPost, "/auth/password-reset/confirm", map[string]string{
		"token": token, "password": "ShcheOdyn2026!",
	})
	client.expect(repeat, http.StatusBadRequest)

	// Новий пароль працює.
	login := client.do(http.MethodPost, "/auth/login", map[string]string{
		"email": "olena@doglyad.local", "password": "NovyiParol2026!",
	})
	client.expect(login, http.StatusOK)
}

func TestLoginLocksAfterRepeatedFailures(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")

	for attempt := 0; attempt < loginAttemptLimit; attempt++ {
		response := client.do(http.MethodPost, "/auth/login", map[string]string{
			"email": "olena@doglyad.local", "password": "неправильний",
		})
		client.expect(response, http.StatusUnauthorized)
	}
	blocked := client.do(http.MethodPost, "/auth/login", map[string]string{
		"email": "olena@doglyad.local", "password": "DemoCare2026!",
	})
	client.expect(blocked, http.StatusTooManyRequests)
}

func TestReportCountsAndCSVExport(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")
	client.createDueTask(wardID, "Обід", "food", false)
	token := client.pairDevice(wardID)
	occurrenceID := client.firstPlannedOccurrence(token)

	marked := client.deviceDo(http.MethodPost, "/device/mark", token, map[string]string{
		"occurrenceID": occurrenceID, "idempotencyKey": security.Token(),
		"deviceTime": time.Now().UTC().Format(time.RFC3339),
	})
	client.expect(marked, http.StatusOK)

	// Проміжок беремо ширший за добу: щоденне завдання може випадати на завтра.
	from := time.Now().UTC().AddDate(0, 0, -2).Format(time.RFC3339Nano)
	to := time.Now().UTC().AddDate(0, 0, 2).Format(time.RFC3339Nano)
	report := client.do(http.MethodGet, "/reports?wardId="+wardID+"&from="+from+"&to="+to, nil)
	client.expect(report, http.StatusOK)
	var summary reportSummary
	client.decode(report, &summary)
	if summary.Done < 1 {
		t.Fatalf("звіт не порахував виконану справу: %+v", summary)
	}

	export := client.do(http.MethodGet, "/reports/export.csv?wardId="+wardID+"&from="+from+"&to="+to, nil)
	client.expect(export, http.StatusOK)
	body := export.Body.String()
	if !strings.Contains(body, "Справа") || !strings.Contains(body, "Обід") {
		t.Fatalf("експорт CSV неповний: %s", body)
	}
	if !strings.HasPrefix(body, "\xEF\xBB\xBF") {
		t.Fatal("експорт CSV без BOM не відкриється в Excel правильно")
	}
}

func TestPINLoginLocksAfterFiveAttempts(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")
	token := client.pairDevice(wardID)

	saved := client.do(http.MethodPut, "/wards/"+wardID+"/pin", map[string]string{"pin": "4821"})
	client.expect(saved, http.StatusNoContent)

	for attempt := 0; attempt < pinAttemptLimit; attempt++ {
		response := client.deviceDo(http.MethodPost, "/device/pin-login", token, map[string]string{"pin": "0000"})
		client.expect(response, http.StatusUnauthorized)
	}
	blocked := client.deviceDo(http.MethodPost, "/device/pin-login", token, map[string]string{"pin": "4821"})
	client.expect(blocked, http.StatusTooManyRequests)
}

func TestRevokedDeviceLosesAccess(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")
	token := client.pairDevice(wardID)

	devices := client.do(http.MethodGet, "/wards/"+wardID+"/devices", nil)
	client.expect(devices, http.StatusOK)
	var list []struct {
		ID string `json:"id"`
	}
	client.decode(devices, &list)
	if len(list) != 1 {
		t.Fatalf("очікувався один пристрій, отримано %d", len(list))
	}

	revoked := client.do(http.MethodDelete, "/wards/"+wardID+"/devices/"+list[0].ID, nil)
	client.expect(revoked, http.StatusNoContent)

	response := client.deviceDo(http.MethodGet, "/device/today", token, nil)
	client.expect(response, http.StatusUnauthorized)
}

func TestNotificationRespectsDisabledChannel(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")
	client.createTask(wardID, "Обід", "food", "13:00", false)
	token := client.pairDevice(wardID)

	// Вимикаємо пошту та Telegram, лишаємо тільки push.
	prefs := client.do(http.MethodPut, "/me/notifications", []map[string]any{
		{"channel": "email", "enabled": false},
		{"channel": "telegram", "enabled": false},
		{"channel": "push", "enabled": true},
	})
	client.expect(prefs, http.StatusNoContent)

	response := client.deviceDo(http.MethodPost, "/device/help", token, map[string]string{"message": ""})
	client.expect(response, http.StatusCreated)

	var channels []string
	rows, err := client.server.db.Query("SELECT channel FROM notifications WHERE kind=?", notify.KindHelp)
	if err != nil {
		t.Fatalf("черга доставки: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var channel string
		if err := rows.Scan(&channel); err != nil {
			t.Fatalf("читання каналу: %v", err)
		}
		channels = append(channels, channel)
	}
	if len(channels) != 1 || channels[0] != notify.ChannelPush {
		t.Fatalf("очікувався лише канал push, отримано %v", channels)
	}
}

func TestMediaUploadAndAccessControl(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")

	body, contentType := pngUpload(t)
	request := httptest.NewRequest(http.MethodPost, "/api/wards/"+wardID+"/photo", bytes.NewReader(body))
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("X-CSRF-Token", client.csrf)
	request.AddCookie(client.cookie)
	recorder := httptest.NewRecorder()
	client.handler.ServeHTTP(recorder, request)
	client.expect(recorder, http.StatusCreated)

	var uploaded struct {
		ID string `json:"id"`
	}
	client.decode(recorder, &uploaded)

	// Власник бачить файл.
	view := client.do(http.MethodGet, "/media/"+uploaded.ID, nil)
	client.expect(view, http.StatusOK)

	// Сторонній помічник — ні.
	stranger := &testClient{t: t, server: client.server, handler: client.handler}
	stranger.registerHelper("petro@doglyad.local")
	denied := stranger.do(http.MethodGet, "/media/"+uploaded.ID, nil)
	stranger.expect(denied, http.StatusForbidden)
}

// pngUpload готує multipart-запит із найменшим коректним PNG.
func pngUpload(t *testing.T) ([]byte, string) {
	t.Helper()
	pixel := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
		0x89, 0x00, 0x00, 0x00, 0x0A, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
		0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
		0x42, 0x60, 0x82,
	}
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	part, err := writer.CreateFormFile(photoField, "photo.png")
	if err != nil {
		t.Fatalf("форма: %v", err)
	}
	if _, err := part.Write(pixel); err != nil {
		t.Fatalf("запис файлу: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("закриття форми: %v", err)
	}
	return buffer.Bytes(), writer.FormDataContentType()
}
