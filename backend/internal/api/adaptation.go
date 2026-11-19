package api

import "context"

// adaptationChange — одне правило, що спрацювало: що змінилося і через яку відповідь.
//
// Повертається помічникові, щоб зв'язок «потреба → налаштування» був видимим,
// а не відбувався мовчки десь усередині застосунку.
type adaptationChange struct {
	Setting string `json:"setting"`
	Value   string `json:"value"`
	Reason  string `json:"reason"`
}

// Назви відповідей анкети для пояснень.
var answerNames = map[string]string{
	"yes":    "так",
	"partly": "частково",
	"no":     "ні",
	"skip":   "пропущено",
}

// adaptationRules обчислює налаштування екрана за відповідями анкети
// та пояснює кожну зміну людською мовою.
//
// Правила свідомо прості й оголошені тут повністю: це предмет дослідження,
// і його треба мати змогу перевірити та змінити, не читаючи решту коду.
func adaptationRules(in needsAnswers) (accessibilitySettings, []adaptationChange) {
	settings := accessibilitySettings{
		Scale:          100,
		Palette:        "standard",
		MagnifierScale: 2,
		LetterSpacing:  "0",
		WordSpacing:    "0",
		LineHeight:     "1.5",
		SpeechRate:     1,
	}
	changes := []adaptationChange{}
	yesOrPartly := func(value string) bool { return value == "yes" || value == "partly" }
	reason := func(question, answer string) string {
		return "відповідь «" + question + "»: " + answerNames[answer]
	}

	switch in.VisionText {
	case "yes":
		settings.Scale = 200
		changes = append(changes, adaptationChange{"Масштаб тексту", "200%",
			reason("важко читати звичайний текст", in.VisionText)})
	case "partly":
		settings.Scale = 150
		changes = append(changes, adaptationChange{"Масштаб тексту", "150%",
			reason("важко читати звичайний текст", in.VisionText)})
	}

	if yesOrPartly(in.VisionColors) {
		settings.Palette = "color-safe"
		changes = append(changes, adaptationChange{"Палітра", "безпечна для розрізнення кольорів",
			reason("погано розрізняє кольори", in.VisionColors)})
	}

	if yesOrPartly(in.Motor) {
		settings.LargeControls = true
		changes = append(changes, adaptationChange{"Великі кнопки", "увімкнено",
			reason("важко влучати в невеликі кнопки", in.Motor)})
	}

	if yesOrPartly(in.Dyslexia) {
		settings.DyslexicFont = true
		settings.LetterSpacing = "0.05em"
		changes = append(changes, adaptationChange{"Шрифт для дислексії", "увімкнено",
			reason("повільно читає або плутає літери", in.Dyslexia)})
	}

	if yesOrPartly(in.Memory) {
		settings.DementiaMode = true
		changes = append(changes, adaptationChange{"Режим підказок", "дата, день тижня та підтвердження приймання",
			reason("забуває зроблене або поточний день", in.Memory)})
	}

	// Озвучення вмикається двома різними відповідями, тому пояснення вибираємо точне.
	if yesOrPartly(in.Dyslexia) || in.PrefersAudio == "yes" {
		settings.SpeechEnabled = true
		cause := reason("легше слухати, ніж читати", in.PrefersAudio)
		if in.PrefersAudio != "yes" {
			cause = reason("повільно читає або плутає літери", in.Dyslexia)
		}
		changes = append(changes, adaptationChange{"Озвучення тексту", "увімкнено", cause})
	}

	// Екран підопічного звуком не користується взагалі, тому міняти нічого не треба.
	// Правило лишається, щоб помічник бачив: відповідь врахована, а не загублена.
	if yesOrPartly(in.Hearing) {
		changes = append(changes, adaptationChange{"Звук", "не використовується",
			reason("може не почути звуковий сигнал", in.Hearing)})
	}

	return settings, changes
}

// applyNeedDefaults зберігає обчислені налаштування та повертає перелік змін.
// Це не діагноз: будь-яке значення помічник потім змінює вручну.
func (s *Server) applyNeedDefaults(ctx context.Context, wardID, helperID string, in needsAnswers) ([]adaptationChange, error) {
	settings, changes := adaptationRules(in)
	// Параметри, яких анкета не визначає, лишаємо такими, як їх виставив помічник.
	if current, err := s.readAccessibility(ctx, wardID); err == nil {
		settings.Magnifier = current.Magnifier
		settings.MagnifierScale = current.MagnifierScale
		settings.SpeechRate = current.SpeechRate
		settings.WordSpacing = current.WordSpacing
		settings.LineHeight = current.LineHeight
	}
	if err := s.saveAccessibility(ctx, wardID, helperID, settings); err != nil {
		return nil, err
	}
	return changes, nil
}
