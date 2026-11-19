package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// pinDo виконує запит у режимі помічника на пристрої підопічного.
func (c *testClient) pinDo(method, path, token string, body any) *httptest.ResponseRecorder {
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
	request.Header.Set("X-PIN-Token", token)
	recorder := httptest.NewRecorder()
	c.handler.ServeHTTP(recorder, request)
	return recorder
}

// openPIN задає PIN-код і відкриває режим помічника на пристрої.
func (c *testClient) openPIN(wardID, deviceToken, pin string) string {
	c.t.Helper()
	saved := c.do(http.MethodPut, "/wards/"+wardID+"/pin", map[string]string{"pin": pin})
	c.expect(saved, http.StatusNoContent)

	opened := c.deviceDo(http.MethodPost, "/device/pin-login", deviceToken, map[string]string{"pin": pin})
	c.expect(opened, http.StatusOK)
	var out struct {
		PINToken string `json:"pinToken"`
		WardName string `json:"wardName"`
	}
	c.decode(opened, &out)
	if out.PINToken == "" {
		c.t.Fatal("вхід за PIN не повернув токена")
	}
	if out.WardName == "" {
		c.t.Fatal("вхід за PIN не назвав підопічного")
	}
	return out.PINToken
}

func TestPINModeShowsAndMarksTasksOfItsWard(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")
	client.createDueTask(wardID, "Обід", "food", false)
	deviceToken := client.pairDevice(wardID)
	pinToken := client.openPIN(wardID, deviceToken, "4821")

	overview := client.pinDo(http.MethodGet, "/device/helper/overview", pinToken, nil)
	client.expect(overview, http.StatusOK)
	var screen struct {
		Ward struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"ward"`
		Occurrences []occurrenceRow `json:"occurrences"`
		Signals     []signal        `json:"signals"`
	}
	client.decode(overview, &screen)
	if screen.Ward.ID != wardID || screen.Ward.Name != "Маріє Іванівно" {
		t.Fatalf("огляд не про того підопічного: %+v", screen.Ward)
	}
	if len(screen.Occurrences) == 0 {
		t.Fatal("огляд не показав жодної справи")
	}

	occurrenceID := screen.Occurrences[0].ID
	marked := client.pinDo(http.MethodPost, "/device/helper/occurrences/"+occurrenceID+"/mark", pinToken,
		map[string]string{"status": "done"})
	client.expect(marked, http.StatusNoContent)

	var status, markedBy string
	if err := client.server.db.QueryRow(
		"SELECT status,COALESCE(marked_by_helper_id,'') FROM task_occurrences WHERE id=?",
		occurrenceID).Scan(&status, &markedBy); err != nil {
		t.Fatalf("читання виконання: %v", err)
	}
	if status != "done" {
		t.Fatalf("справу не відмічено: %s", status)
	}
	// Обліковий запис у режимі PIN невідомий, і підставляти чужий не можна:
	// у звіті стояло б ім'я помічника, якого поруч не було.
	if markedBy != "" {
		t.Fatalf("відмітку приписано обліковому запису: %s", markedBy)
	}

	// Журнал має назвати діяча, а не записати дію на «Систему».
	journal := client.do(http.MethodGet, "/audit?wardId="+wardID, nil)
	client.expect(journal, http.StatusOK)
	var entries []map[string]string
	client.decode(journal, &entries)
	found := false
	for _, entry := range entries {
		if entry["action"] == "позначено done" {
			found = true
			if entry["actor"] != actorPIN {
				t.Fatalf("журнал назвав не того діяча: %q", entry["actor"])
			}
		}
	}
	if !found {
		t.Fatalf("дії в режимі PIN немає в журналі: %+v", entries)
	}

	undone := client.pinDo(http.MethodPost, "/device/helper/occurrences/"+occurrenceID+"/undo", pinToken, nil)
	client.expect(undone, http.StatusNoContent)
}

func TestPINModeClosesSignalsOfItsWard(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")
	client.createDueTask(wardID, "Ранкові ліки", "medicine", true)
	deviceToken := client.pairDevice(wardID)

	help := client.deviceDo(http.MethodPost, "/device/help", deviceToken, map[string]string{})
	client.expect(help, http.StatusCreated)

	pinToken := client.openPIN(wardID, deviceToken, "4821")
	overview := client.pinDo(http.MethodGet, "/device/helper/overview", pinToken, nil)
	client.expect(overview, http.StatusOK)
	var screen struct {
		Signals []signal `json:"signals"`
	}
	client.decode(overview, &screen)
	if len(screen.Signals) != 1 || screen.Signals[0].Type != EventHelp {
		t.Fatalf("сигнал про допомогу не видно в режимі PIN: %+v", screen.Signals)
	}

	closed := client.pinDo(http.MethodPost, "/device/helper/help/"+screen.Signals[0].ID+"/close", pinToken, nil)
	client.expect(closed, http.StatusNoContent)

	again := client.pinDo(http.MethodGet, "/device/helper/overview", pinToken, nil)
	client.expect(again, http.StatusOK)
	client.decode(again, &screen)
	if len(screen.Signals) != 0 {
		t.Fatalf("закритий сигнал лишився у списку: %+v", screen.Signals)
	}
}

// TestPINModeCannotReachForeignWard — головна перевірка ізоляції: з планшета
// однієї людини не видно й не змінити дані іншої.
func TestPINModeCannotReachForeignWard(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	mine := client.createWard("Маріє Іванівно")
	foreign := client.createWard("Іване Петровичу")
	client.createDueTask(foreign, "Обід", "food", false)

	foreignToken := client.pairDevice(foreign)
	foreignOccurrence := client.firstPlannedOccurrence(foreignToken)

	deviceToken := client.pairDevice(mine)
	pinToken := client.openPIN(mine, deviceToken, "4821")

	overview := client.pinDo(http.MethodGet, "/device/helper/overview", pinToken, nil)
	client.expect(overview, http.StatusOK)
	var screen struct {
		Ward struct {
			ID string `json:"id"`
		} `json:"ward"`
		Occurrences []occurrenceRow `json:"occurrences"`
	}
	client.decode(overview, &screen)
	if screen.Ward.ID != mine {
		t.Fatalf("огляд показав чужого підопічного: %s", screen.Ward.ID)
	}
	for _, item := range screen.Occurrences {
		if item.Title == "Обід" {
			t.Fatal("в огляді видно справу чужого підопічного")
		}
	}

	refused := client.pinDo(http.MethodPost, "/device/helper/occurrences/"+foreignOccurrence+"/mark", pinToken,
		map[string]string{"status": "done"})
	client.expect(refused, http.StatusNotFound)

	var status string
	if err := client.server.db.QueryRow(
		"SELECT status FROM task_occurrences WHERE id=?", foreignOccurrence).Scan(&status); err != nil {
		t.Fatalf("читання виконання: %v", err)
	}
	if status != "planned" {
		t.Fatalf("чужу справу змінено з іншого пристрою: %s", status)
	}
}

// TestPINModeOpensNoAccountDoors перевіряє, що PIN не замінює пароль: з нього
// не керують доступом, пристроями й самим профілем. Пристрій лежить у чужій
// квартирі, і чотири цифри не мають відкривати обліковий запис.
func TestPINModeOpensNoAccountDoors(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")
	deviceToken := client.pairDevice(wardID)
	pinToken := client.openPIN(wardID, deviceToken, "4821")

	closed := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, "/wards", nil},
		{http.MethodGet, "/wards/" + wardID, nil},
		{http.MethodPatch, "/wards/" + wardID, map[string]string{"addressName": "Інше ім'я"}},
		{http.MethodDelete, "/wards/" + wardID, nil},
		{http.MethodPut, "/wards/" + wardID + "/pin", map[string]string{"pin": "9999"}},
		{http.MethodPut, "/wards/" + wardID + "/accessibility", map[string]any{"scale": 200}},
		{http.MethodGet, "/wards/" + wardID + "/links", nil},
		{http.MethodPost, "/wards/" + wardID + "/links", map[string]string{"email": "chuzhyi@doglyad.local"}},
		{http.MethodGet, "/wards/" + wardID + "/devices", nil},
		{http.MethodPost, "/wards/" + wardID + "/pairing", map[string]any{}},
		{http.MethodGet, "/me", nil},
		{http.MethodGet, "/me/sessions", nil},
		{http.MethodGet, "/audit?wardId=" + wardID, nil},
		{http.MethodGet, "/reports/export.csv?wardId=" + wardID, nil},
	}
	for _, call := range closed {
		response := client.pinDo(call.method, call.path, pinToken, call.body)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s відкритий за PIN: код %d, тіло %s",
				call.method, call.path, response.Code, response.Body.String())
		}
	}
}

func TestPINSessionExpiresAndLogsOut(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")
	deviceToken := client.pairDevice(wardID)

	moment := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	client.server.clock = func() time.Time { return moment }
	pinToken := client.openPIN(wardID, deviceToken, "4821")

	// Користування продовжує сеанс: за хвилину до кінця він ще живий.
	client.server.clock = func() time.Time { return moment.Add(pinSessionTTL - time.Minute) }
	alive := client.pinDo(http.MethodGet, "/device/helper/overview", pinToken, nil)
	client.expect(alive, http.StatusOK)

	// Планшет, залишений на столі, закриває доступ сам.
	client.server.clock = func() time.Time { return moment.Add(2 * pinSessionTTL) }
	expired := client.pinDo(http.MethodGet, "/device/helper/overview", pinToken, nil)
	client.expect(expired, http.StatusUnauthorized)
	if body := expired.Body.String(); !strings.Contains(body, "Введіть PIN-код ще раз") {
		t.Fatalf("відмова не підказує, що робити: %s", body)
	}

	// Явний вихід закриває сеанс одразу.
	client.server.clock = func() time.Time { return moment.Add(3 * pinSessionTTL) }
	second := client.openPIN(wardID, deviceToken, "4821")
	out := client.pinDo(http.MethodPost, "/device/helper/logout", second, nil)
	client.expect(out, http.StatusNoContent)
	after := client.pinDo(http.MethodGet, "/device/helper/overview", second, nil)
	client.expect(after, http.StatusUnauthorized)
}

// TestPINSessionDiesWithDevice перевіряє, що відключення пристрою помічником
// миттєво закриває і відкритий на ньому режим.
func TestPINSessionDiesWithDevice(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")
	deviceToken := client.pairDevice(wardID)
	pinToken := client.openPIN(wardID, deviceToken, "4821")

	alive := client.pinDo(http.MethodGet, "/device/helper/overview", pinToken, nil)
	client.expect(alive, http.StatusOK)

	var deviceID string
	if err := client.server.db.QueryRow(
		"SELECT id FROM devices WHERE ward_id=? AND revoked_at IS NULL", wardID).Scan(&deviceID); err != nil {
		t.Fatalf("пошук пристрою: %v", err)
	}
	revoked := client.do(http.MethodDelete, "/wards/"+wardID+"/devices/"+deviceID, nil)
	client.expect(revoked, http.StatusNoContent)

	after := client.pinDo(http.MethodGet, "/device/helper/overview", pinToken, nil)
	client.expect(after, http.StatusUnauthorized)
}
