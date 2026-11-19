package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"doglyad/backend/internal/security"
)

// portrait — узагальнений профіль підопічного для перевірки.
//
// Портрети описані в docs/doslidzhennya.md. Тут перевіряється, що анкета
// функціональних потреб перетворюється на очікувані налаштування екрана
// і що поведінка застосунку від портрета не залежить: відмітити справу,
// попросити допомоги й отримати захист від подвійного приймання має вміти
// кожен, незалежно від того, які саме обмеження має людина.
type portrait struct {
	name    string
	summary string
	answers map[string]string
	expect  accessibilitySettings
	// expectChanges — скільки правил адаптації має спрацювати.
	expectChanges int
}

func portraits() []portrait {
	return []portrait{
		{
			name:    "Марія, 78 — слабкий зір",
			summary: "Читає з лупою, пам'ять добра, із кнопками справляється.",
			answers: map[string]string{"visionText": "yes"},
			expect: accessibilitySettings{
				Scale: 200, Palette: "standard", LargeControls: false,
				DyslexicFont: false, DementiaMode: false, SpeechEnabled: false,
			},
			expectChanges: 1,
		},
		{
			name:    "Іван, 82 — помірна деменція",
			summary: "Забуває, чи приймав ліки. Текст читає, але дрібний — важко.",
			answers: map[string]string{"visionText": "partly", "memory": "yes"},
			expect: accessibilitySettings{
				Scale: 150, Palette: "standard", LargeControls: false,
				DyslexicFont: false, DementiaMode: true, SpeechEnabled: false,
			},
			expectChanges: 2,
		},
		{
			name:    "Ганна, 71 — після інсульту, тремор рук",
			summary: "Бачить добре, але не влучає в невеликі кнопки.",
			answers: map[string]string{"motor": "yes"},
			expect: accessibilitySettings{
				Scale: 100, Palette: "standard", LargeControls: true,
				DyslexicFont: false, DementiaMode: false, SpeechEnabled: false,
			},
			expectChanges: 1,
		},
		{
			name:    "Петро, 85 — дальтонізм і туговухість",
			summary: "Не розрізняє червоний і зелений, може не почути сигнал.",
			answers: map[string]string{"visionColors": "yes", "hearing": "yes"},
			expect: accessibilitySettings{
				Scale: 100, Palette: "color-safe", LargeControls: false,
				DyslexicFont: false, DementiaMode: false, SpeechEnabled: false,
			},
			// Два правила: палітра і застереження про звук.
			expectChanges: 2,
		},
		{
			name:    "Олена, 69 — дислексія",
			summary: "Плутає літери, легше сприймає на слух.",
			answers: map[string]string{"dyslexia": "yes", "prefersAudio": "yes"},
			expect: accessibilitySettings{
				Scale: 100, Palette: "standard", LargeControls: false,
				DyslexicFont: true, DementiaMode: false, SpeechEnabled: true,
			},
			expectChanges: 2,
		},
		{
			name:    "Степан, 90 — поєднання обмежень",
			summary: "Найважчий випадок: зір, кольори, слух, руки, читання, пам'ять.",
			answers: map[string]string{
				"visionText": "yes", "visionColors": "yes", "hearing": "yes",
				"motor": "yes", "dyslexia": "yes", "memory": "yes", "prefersAudio": "yes",
			},
			expect: accessibilitySettings{
				Scale: 200, Palette: "color-safe", LargeControls: true,
				DyslexicFont: true, DementiaMode: true, SpeechEnabled: true,
			},
			expectChanges: 7,
		},
		{
			name:    "Ніна, 75 — анкету пропущено",
			summary: "Помічник не захотів відповідати на питання.",
			answers: map[string]string{
				"visionText": "skip", "visionColors": "skip", "hearing": "skip",
				"motor": "skip", "dyslexia": "skip", "memory": "skip", "prefersAudio": "skip",
			},
			expect: accessibilitySettings{
				Scale: 100, Palette: "standard", LargeControls: false,
				DyslexicFont: false, DementiaMode: false, SpeechEnabled: false,
			},
			expectChanges: 0,
		},
	}
}

// compareSettings порівнює лише ті поля, які визначає анкета.
func compareSettings(t *testing.T, got, want accessibilitySettings) {
	t.Helper()
	if got.Scale != want.Scale {
		t.Errorf("масштаб: отримано %d, очікувалося %d", got.Scale, want.Scale)
	}
	if got.Palette != want.Palette {
		t.Errorf("палітра: отримано %q, очікувалося %q", got.Palette, want.Palette)
	}
	if got.LargeControls != want.LargeControls {
		t.Errorf("великі кнопки: отримано %v, очікувалося %v", got.LargeControls, want.LargeControls)
	}
	if got.DyslexicFont != want.DyslexicFont {
		t.Errorf("шрифт для дислексії: отримано %v, очікувалося %v", got.DyslexicFont, want.DyslexicFont)
	}
	if got.DementiaMode != want.DementiaMode {
		t.Errorf("режим підказок: отримано %v, очікувалося %v", got.DementiaMode, want.DementiaMode)
	}
	if got.SpeechEnabled != want.SpeechEnabled {
		t.Errorf("озвучення: отримано %v, очікувалося %v", got.SpeechEnabled, want.SpeechEnabled)
	}
}

// TestPortraitsGetExpectedScreen перевіряє перетворення анкети на налаштування
// та пояснення кожної зміни.
func TestPortraitsGetExpectedScreen(t *testing.T) {
	for _, person := range portraits() {
		t.Run(person.name, func(t *testing.T) {
			client := newClient(t)
			client.registerHelper(security.ID() + "@doglyad.local")
			wardID := client.createWard(person.name)

			response := client.do(http.MethodPut, "/wards/"+wardID+"/needs", person.answers)
			client.expect(response, http.StatusOK)

			var applied struct {
				Settings accessibilitySettings `json:"settings"`
				Changes  []adaptationChange    `json:"changes"`
			}
			client.decode(response, &applied)

			compareSettings(t, applied.Settings, person.expect)
			if len(applied.Changes) != person.expectChanges {
				t.Errorf("пояснень змін: отримано %d, очікувалося %d (%+v)",
					len(applied.Changes), person.expectChanges, applied.Changes)
			}
			for _, change := range applied.Changes {
				if change.Setting == "" || change.Value == "" || change.Reason == "" {
					t.Errorf("зміна без пояснення: %+v", change)
				}
			}

			// Налаштування мають пройти власну перевірку сервера без виправлень.
			settings := applied.Settings
			if err := validateAccessibility(&settings); err != nil {
				t.Fatalf("сервер відхилив власні налаштування: %v", err)
			}
			compareSettings(t, settings, person.expect)
		})
	}
}

// TestPortraitsSeeTheirScreen перевіряє, що пристрій підопічного отримує
// саме ті налаштування й повний набір даних для показу.
func TestPortraitsSeeTheirScreen(t *testing.T) {
	for _, person := range portraits() {
		t.Run(person.name, func(t *testing.T) {
			client := newClient(t)
			client.registerHelper(security.ID() + "@doglyad.local")
			wardID := client.createWard(person.name)

			answers := client.do(http.MethodPut, "/wards/"+wardID+"/needs", person.answers)
			client.expect(answers, http.StatusOK)
			client.createTask(wardID, "Ранкові ліки", "medicine", "08:00", true)
			token := client.pairDevice(wardID)

			response := client.deviceDo(http.MethodGet, "/device/today", token, nil)
			client.expect(response, http.StatusOK)

			var screen struct {
				Ward struct {
					Name     string `json:"name"`
					Timezone string `json:"timezone"`
				} `json:"ward"`
				Settings    accessibilitySettings `json:"settings"`
				Occurrences []struct {
					ID          string `json:"id"`
					Title       string `json:"title"`
					Status      string `json:"status"`
					ScheduledAt string `json:"scheduledAt"`
					Category    string `json:"category"`
				} `json:"occurrences"`
				ServerTime string `json:"serverTime"`
			}
			client.decode(response, &screen)

			compareSettings(t, screen.Settings, person.expect)
			if screen.Ward.Name != person.name || screen.Ward.Timezone == "" {
				t.Fatalf("екран без даних підопічного: %+v", screen.Ward)
			}
			if screen.ServerTime == "" {
				t.Fatal("екран без часу сервера: пристрій не зможе звірити годинник")
			}
			if len(screen.Occurrences) == 0 {
				t.Fatal("екран не показав жодної справи")
			}
			for _, item := range screen.Occurrences {
				if item.ID == "" || item.Title == "" || item.Status == "" || item.ScheduledAt == "" {
					t.Fatalf("справа з порожніми полями: %+v", item)
				}
			}
		})
	}
}

// TestPortraitsCanCompleteTheirDay перевіряє, що базовий сценарій однаково
// працює для кожного портрета: відмітити ліки, не прийняти їх удруге,
// скасувати помилкове натискання, попросити допомоги.
func TestPortraitsCanCompleteTheirDay(t *testing.T) {
	for _, person := range portraits() {
		t.Run(person.name, func(t *testing.T) {
			client := newClient(t)
			client.registerHelper(security.ID() + "@doglyad.local")
			wardID := client.createWard(person.name)

			answers := client.do(http.MethodPut, "/wards/"+wardID+"/needs", person.answers)
			client.expect(answers, http.StatusOK)
			client.createDueTask(wardID, "Ранкові ліки", "medicine", true)
			token := client.pairDevice(wardID)
			occurrenceID := client.firstPlannedOccurrence(token)

			// Відмітка з однієї спроби.
			marked := client.deviceDo(http.MethodPost, "/device/mark", token, map[string]string{
				"occurrenceID": occurrenceID, "idempotencyKey": security.Token(),
				"deviceTime": time.Now().UTC().Format(time.RFC3339),
			})
			client.expect(marked, http.StatusOK)

			// Захист від повторного приймання — для всіх однаково.
			again := client.deviceDo(http.MethodPost, "/device/mark", token, map[string]string{
				"occurrenceID": occurrenceID, "idempotencyKey": security.Token(),
				"deviceTime": time.Now().UTC().Format(time.RFC3339),
			})
			client.expect(again, http.StatusConflict)

			// Скасування помилкового натискання.
			undone := client.deviceDo(http.MethodPost, "/device/undo", token,
				map[string]string{"occurrenceID": occurrenceID})
			client.expect(undone, http.StatusOK)

			// Прохання про допомогу доходить до панелі помічника.
			helped := client.deviceDo(http.MethodPost, "/device/help", token,
				map[string]string{"message": ""})
			client.expect(helped, http.StatusCreated)

			signals := client.do(http.MethodGet, "/signals", nil)
			client.expect(signals, http.StatusOK)
			var list []signal
			client.decode(signals, &list)
			if len(list) == 0 {
				t.Fatal("сигнал про допомогу не дійшов до помічника")
			}
		})
	}
}

// TestForgetfulPortraitGetsAnswerWithoutHelper перевіряє ключовий для деменції
// випадок: на питання «чи я вже приймав» застосунок відповідає сам, якщо
// відмітка вже є, і не турбує помічника.
func TestForgetfulPortraitGetsAnswerWithoutHelper(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Іван, 82")

	answers := client.do(http.MethodPut, "/wards/"+wardID+"/needs",
		map[string]string{"memory": "yes", "visionText": "partly"})
	client.expect(answers, http.StatusOK)
	client.createDueTask(wardID, "Ранкові ліки", "medicine", true)
	token := client.pairDevice(wardID)
	occurrenceID := client.firstPlannedOccurrence(token)

	// Поки справу не виконано, питання йде помічникові.
	asked := client.deviceDo(http.MethodPost, "/device/unsure", token,
		map[string]string{"occurrenceID": occurrenceID})
	client.expect(asked, http.StatusCreated)

	marked := client.deviceDo(http.MethodPost, "/device/mark", token, map[string]string{
		"occurrenceID": occurrenceID, "idempotencyKey": security.Token(),
		"deviceTime": time.Now().UTC().Format(time.RFC3339),
	})
	client.expect(marked, http.StatusOK)

	// Після відмітки застосунок відповідає сам і нового запиту не створює.
	answeredBySystem := client.deviceDo(http.MethodPost, "/device/unsure", token,
		map[string]string{"occurrenceID": occurrenceID})
	client.expect(answeredBySystem, http.StatusOK)

	var answer struct {
		Message string `json:"message"`
	}
	client.decode(answeredBySystem, &answer)
	if answer.Message == "" {
		t.Fatal("застосунок не дав відповіді про вже прийняті ліки")
	}

	var openRequests int
	if err := client.server.db.QueryRow(
		"SELECT COUNT(*) FROM unsure_requests WHERE ward_id=? AND status='open'", wardID).Scan(&openRequests); err != nil {
		t.Fatalf("підрахунок питань: %v", err)
	}
	if openRequests != 1 {
		t.Fatalf("після відмітки з'явилося зайве питання до помічника: відкритих %d", openRequests)
	}
}

// TestPortraitWithTremorKeepsUndoWindow перевіряє, що людині з тремором
// вистачає часу скасувати випадкове натискання.
func TestPortraitWithTremorKeepsUndoWindow(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Ганна, 71")

	answers := client.do(http.MethodPut, "/wards/"+wardID+"/needs", map[string]string{"motor": "yes"})
	client.expect(answers, http.StatusOK)
	client.createDueTask(wardID, "Обід", "food", false)
	token := client.pairDevice(wardID)
	occurrenceID := client.firstPlannedOccurrence(token)

	start := time.Now().UTC()
	client.server.clock = func() time.Time { return start }
	marked := client.deviceDo(http.MethodPost, "/device/mark", token, map[string]string{
		"occurrenceID": occurrenceID, "idempotencyKey": security.Token(),
		"deviceTime": start.Format(time.RFC3339),
	})
	client.expect(marked, http.StatusOK)

	// За хвилину скасувати ще можна.
	client.server.clock = func() time.Time { return start.Add(time.Minute) }
	undone := client.deviceDo(http.MethodPost, "/device/undo", token,
		map[string]string{"occurrenceID": occurrenceID})
	client.expect(undone, http.StatusOK)

	// Після закінчення вікна — вже ні, і підопічний отримує зрозуміле пояснення.
	marked = client.deviceDo(http.MethodPost, "/device/mark", token, map[string]string{
		"occurrenceID": occurrenceID, "idempotencyKey": security.Token(),
		"deviceTime": start.Add(time.Minute).Format(time.RFC3339),
	})
	client.expect(marked, http.StatusOK)

	client.server.clock = func() time.Time { return start.Add(10 * time.Minute) }
	late := client.deviceDo(http.MethodPost, "/device/undo", token,
		map[string]string{"occurrenceID": occurrenceID})
	client.expect(late, http.StatusConflict)
	var refusal struct {
		Error string `json:"error"`
	}
	client.decode(late, &refusal)
	if refusal.Error == "" {
		t.Fatal("відмова без пояснення підопічному")
	}
}

// TestEarlyMarkIsRefused перевіряє найнебезпечніший випадок: увечері підопічний
// бачить завтрашні ранкові ліки й натискає «Прийняв». Якби відмітка пройшла,
// вранці ліки ніхто б не випив, ескалація не спрацювала б, а помічник бачив би
// «виконано» — застосунок приховав би саме те, заради чого існує.
func TestEarlyMarkIsRefused(t *testing.T) {
	client := newClient(t)
	client.registerHelper("olena@doglyad.local")
	wardID := client.createWard("Маріє Іванівно")
	token := client.pairDevice(wardID)

	kyiv, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Fatalf("часовий пояс: %v", err)
	}
	// 21:00 за Києвом. Ліки — завтра о 08:00.
	evening := time.Date(2026, 6, 15, 18, 0, 0, 0, time.UTC)
	client.server.clock = func() time.Time { return evening }

	created := client.do(http.MethodPost, "/wards/"+wardID+"/tasks", map[string]any{
		"title": "Ранкові ліки", "category": "medicine", "critical": true,
		"localStartDate": evening.In(kyiv).AddDate(0, 0, 1).Format("2006-01-02"),
		"localTime":      "08:00", "recurrenceKind": "once",
		"completionWindowMinutes": 120, "escalationWindowMinutes": 30, "warningMinutes": 10,
	})
	client.expect(created, http.StatusCreated)

	occurrenceID := client.firstPlannedOccurrence(token)
	refused := client.deviceDo(http.MethodPost, "/device/mark", token, map[string]any{
		"occurrenceID": occurrenceID, "idempotencyKey": "key-1",
		"deviceTime": evening.Format(time.RFC3339),
	})
	client.expect(refused, http.StatusConflict)
	if body := refused.Body.String(); !strings.Contains(body, "Ще рано") || !strings.Contains(body, "08:00") {
		t.Fatalf("відмова не пояснює підопічному, коли саме справа: %s", body)
	}

	var status string
	if err := client.server.db.QueryRow(
		"SELECT status FROM task_occurrences WHERE id=?", occurrenceID).Scan(&status); err != nil {
		t.Fatalf("читання виконання: %v", err)
	}
	if status != "planned" {
		t.Fatalf("справу відмічено завчасно: %s", status)
	}

	// Спрацювання захисту потрапляє в показники дослідження.
	metrics, err := client.server.buildMetrics(context.Background(), wardID,
		stamp(evening.AddDate(0, 0, -1)), stamp(evening.AddDate(0, 0, 2)))
	if err != nil {
		t.Fatalf("показники: %v", err)
	}
	if metrics.BlockedEarlyMarks != 1 {
		t.Fatalf("захист не врахований у показниках: %d", metrics.BlockedEarlyMarks)
	}

	// О 07:55 — за п'ять хвилин до часу — вікно вже відкрите, і відмітка проходить.
	client.server.clock = func() time.Time {
		return time.Date(2026, 6, 16, 4, 55, 0, 0, time.UTC)
	}
	accepted := client.deviceDo(http.MethodPost, "/device/mark", token, map[string]any{
		"occurrenceID": occurrenceID, "idempotencyKey": "key-2",
		"deviceTime": time.Date(2026, 6, 16, 4, 55, 0, 0, time.UTC).Format(time.RFC3339),
	})
	client.expect(accepted, http.StatusOK)
}
