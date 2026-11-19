package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"doglyad/backend/internal/security"
)

// reportRange повертає проміжок, що охоплює і вчорашній, і завтрашній день.
func reportRange() (string, string) {
	now := time.Now().UTC()
	return stamp(now.AddDate(0, 0, -2)), stamp(now.AddDate(0, 0, 2))
}

func (c *testClient) metrics(wardID string) researchMetrics {
	c.t.Helper()
	from, to := reportRange()
	response := c.do(http.MethodGet, "/reports?wardId="+wardID+"&from="+from+"&to="+to, nil)
	c.expect(response, http.StatusOK)
	var summary reportSummary
	c.decode(response, &summary)
	return summary.Metrics
}

func TestMetricsCountBlockedDoubleDose(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")
	client.createDueTask(wardID, "Ранкові ліки", "medicine", true)
	token := client.pairDevice(wardID)
	occurrenceID := client.firstPlannedOccurrence(token)

	marked := client.deviceDo(http.MethodPost, "/device/mark", token, map[string]string{
		"occurrenceID": occurrenceID, "idempotencyKey": security.Token(),
		"deviceTime": time.Now().UTC().Format(time.RFC3339),
	})
	client.expect(marked, http.StatusOK)

	// Друга спроба прийняти ті самі ліки має бути заблокована та порахована.
	blocked := client.deviceDo(http.MethodPost, "/device/mark", token, map[string]string{
		"occurrenceID": occurrenceID, "idempotencyKey": security.Token(),
		"deviceTime": time.Now().UTC().Format(time.RFC3339),
	})
	client.expect(blocked, http.StatusConflict)

	metrics := client.metrics(wardID)
	if metrics.BlockedDoubleDoses != 1 {
		t.Fatalf("очікувалося одне спрацювання захисту, отримано %d", metrics.BlockedDoubleDoses)
	}
	if metrics.CompletedFirstTry != 1 {
		t.Fatalf("справа мала порахуватися як виконана з першого разу: %+v", metrics)
	}
}

func TestMetricsSeparateFirstTryFromCorrected(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")
	client.createDueTask(wardID, "Обід", "food", false)
	token := client.pairDevice(wardID)
	occurrenceID := client.firstPlannedOccurrence(token)

	// Відмітили, скасували, відмітили ще раз — це виконання з виправленням.
	for attempt := 0; attempt < 2; attempt++ {
		marked := client.deviceDo(http.MethodPost, "/device/mark", token, map[string]string{
			"occurrenceID": occurrenceID, "idempotencyKey": security.Token(),
			"deviceTime": time.Now().UTC().Format(time.RFC3339),
		})
		client.expect(marked, http.StatusOK)
		if attempt == 0 {
			undone := client.deviceDo(http.MethodPost, "/device/undo", token,
				map[string]string{"occurrenceID": occurrenceID})
			client.expect(undone, http.StatusOK)
		}
	}

	metrics := client.metrics(wardID)
	if metrics.CompletedWithCorrection != 1 {
		t.Fatalf("очікувалося одне виконання з виправленням: %+v", metrics)
	}
	if metrics.CompletedFirstTry != 0 {
		t.Fatalf("виконання з виправленням не має рахуватися як зроблене з першого разу: %+v", metrics)
	}
}

func TestMetricsMeasureHelperResponseTime(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")
	client.createTask(wardID, "Обід", "food", "13:00", false)
	token := client.pairDevice(wardID)

	start := time.Now().UTC()
	client.server.clock = func() time.Time { return start }
	asked := client.deviceDo(http.MethodPost, "/device/help", token, map[string]string{"message": ""})
	client.expect(asked, http.StatusCreated)

	signals := client.do(http.MethodGet, "/signals", nil)
	client.expect(signals, http.StatusOK)
	var list []signal
	client.decode(signals, &list)
	if len(list) == 0 {
		t.Fatal("сигнал не потрапив у панель")
	}

	// Помічник відгукнувся через три хвилини.
	client.server.clock = func() time.Time { return start.Add(3 * time.Minute) }
	acked := client.do(http.MethodPost, "/help-requests/"+list[0].ID+"/ack", nil)
	client.expect(acked, http.StatusNoContent)

	client.server.clock = nil
	metrics := client.metrics(wardID)
	if metrics.MedianHelpResponseSeconds != 180 {
		t.Fatalf("очікувалося 180 секунд реакції, отримано %d", metrics.MedianHelpResponseSeconds)
	}
	if metrics.UnansweredSignals != 0 {
		t.Fatalf("сигнал відмічено як без відповіді: %+v", metrics)
	}
}

func TestMetricsCountUnansweredSignal(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")
	client.createTask(wardID, "Обід", "food", "13:00", false)
	token := client.pairDevice(wardID)

	asked := client.deviceDo(http.MethodPost, "/device/help", token, map[string]string{"message": ""})
	client.expect(asked, http.StatusCreated)

	metrics := client.metrics(wardID)
	if metrics.UnansweredSignals != 1 {
		t.Fatalf("сигнал без реакції не порахований: %+v", metrics)
	}
	if metrics.MedianHelpResponseSeconds != 0 {
		t.Fatalf("без реакції час не визначений, має бути нуль: %+v", metrics)
	}
}

func TestMetricsMeasureEscalationResponseTime(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")
	client.createTask(wardID, "Вечірні ліки", "medicine", "20:00", true)

	// Минула доба: ескалація відкривається.
	base := time.Now().UTC().Add(24 * time.Hour)
	client.server.clock = func() time.Time { return base }
	client.server.runCycle(context.Background())

	escalations := client.do(http.MethodGet, "/escalations?wardId="+wardID, nil)
	client.expect(escalations, http.StatusOK)
	var list []map[string]string
	client.decode(escalations, &list)
	if len(list) == 0 {
		t.Fatal("ескалацію не створено")
	}

	// Помічник узяв її через десять хвилин.
	client.server.clock = func() time.Time { return base.Add(10 * time.Minute) }
	taken := client.do(http.MethodPost, "/escalations/"+list[0]["id"]+"/take", nil)
	client.expect(taken, http.StatusNoContent)

	client.server.clock = nil
	from, to := stamp(base.Add(-time.Hour)), stamp(base.Add(2*time.Hour))
	response := client.do(http.MethodGet, "/reports?wardId="+wardID+"&from="+from+"&to="+to, nil)
	client.expect(response, http.StatusOK)
	var summary reportSummary
	client.decode(response, &summary)
	if summary.Metrics.MedianEscalationResponseSeconds != 600 {
		t.Fatalf("очікувалося 600 секунд реакції, отримано %d", summary.Metrics.MedianEscalationResponseSeconds)
	}
}

func TestMedianIgnoresSingleOutlier(t *testing.T) {
	if got := median([]int{60, 120, 10800}); got != 120 {
		t.Fatalf("медіана має бути стійкою до викиду, отримано %d", got)
	}
	if got := median(nil); got != 0 {
		t.Fatalf("порожній набір має давати нуль, отримано %d", got)
	}
	if got := median([]int{30, 90}); got != 60 {
		t.Fatalf("для двох значень очікувалося 60, отримано %d", got)
	}
}

func TestNeedsAnswersExplainEveryChange(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")

	response := client.do(http.MethodPut, "/wards/"+wardID+"/needs", map[string]string{
		"visionText": "partly", "motor": "yes", "memory": "yes", "prefersAudio": "yes",
	})
	client.expect(response, http.StatusOK)

	var applied struct {
		Settings accessibilitySettings `json:"settings"`
		Changes  []adaptationChange    `json:"changes"`
	}
	client.decode(response, &applied)

	if applied.Settings.Scale != 150 || !applied.Settings.LargeControls ||
		!applied.Settings.DementiaMode || !applied.Settings.SpeechEnabled {
		t.Fatalf("анкета не застосувалася до налаштувань: %+v", applied.Settings)
	}
	if len(applied.Changes) != 4 {
		t.Fatalf("очікувалося чотири пояснені зміни, отримано %d: %+v", len(applied.Changes), applied.Changes)
	}
	for _, change := range applied.Changes {
		if change.Setting == "" || change.Value == "" || change.Reason == "" {
			t.Fatalf("зміна без пояснення: %+v", change)
		}
	}
}

func TestNeedsDoNotResetManualSettings(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")

	// Помічник вручну увімкнув лупу та сповільнив озвучення.
	manual := client.do(http.MethodPut, "/wards/"+wardID+"/accessibility", map[string]any{
		"scale": 100, "palette": "standard", "magnifier": true, "magnifierScale": 3,
		"speechRate": 0.8, "lineHeight": "1.8",
	})
	client.expect(manual, http.StatusNoContent)

	answered := client.do(http.MethodPut, "/wards/"+wardID+"/needs", map[string]string{"visionText": "yes"})
	client.expect(answered, http.StatusOK)

	var applied struct {
		Settings accessibilitySettings `json:"settings"`
	}
	client.decode(answered, &applied)
	if applied.Settings.Scale != 200 {
		t.Fatalf("анкета мала збільшити масштаб: %+v", applied.Settings)
	}
	if !applied.Settings.Magnifier || applied.Settings.MagnifierScale != 3 || applied.Settings.SpeechRate != 0.8 {
		t.Fatalf("ручні налаштування затерті анкетою: %+v", applied.Settings)
	}
}
