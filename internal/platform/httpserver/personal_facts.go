package httpserver

import (
	"regexp"
	"strings"
	"unicode"

	"nova.local/core/internal/core"
)

var (
	emailPattern = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	phonePattern = regexp.MustCompile(`\+?\d[\d\s().-]{5,}\d`)
)

func ensureMissingPersonalFactInvitation(userText, assistantText string) string {
	if !looksLikePersonalFactQuestion(userText) {
		return assistantText
	}
	if !assistantIndicatesMissingFact(assistantText) || assistantAlreadyRequestsFact(assistantText) {
		return assistantText
	}
	invitation := "Поділись цим, і я запамʼятаю, щоб наступного разу підказати."
	if personalFactKind(normalizePersonalFactText(userText)) == "phone" {
		invitation = "Поділись цим номером, і я запамʼятаю, щоб наступного разу підказати."
	}
	assistantText = strings.TrimSpace(assistantText)
	if assistantText == "" {
		return "Я ще не знаю. " + invitation
	}
	return assistantText + " " + invitation
}

func deterministicPersonalFactMemoryAction(text string) (core.NovaAction, bool) {
	normalized := normalizePersonalFactText(text)
	if looksLikePersonalFactQuestion(text) {
		return core.NovaAction{}, false
	}
	subject := personalFactSubject(normalized)
	if subject == "" {
		return core.NovaAction{}, false
	}

	kind := personalFactKind(normalized)
	switch kind {
	case "phone":
		phone := extractPhoneNumber(text)
		if phone == "" {
			return core.NovaAction{}, false
		}
		return core.NovaAction{
			Type:       core.ActionMemorySave,
			Content:    "Номер телефону " + subject + ": " + phone,
			Kind:       "fact",
			Confidence: 0.95,
		}, true
	case "email":
		email := emailPattern.FindString(text)
		if email == "" {
			return core.NovaAction{}, false
		}
		return core.NovaAction{
			Type:       core.ActionMemorySave,
			Content:    "Email " + subject + ": " + email,
			Kind:       "fact",
			Confidence: 0.95,
		}, true
	default:
		return core.NovaAction{}, false
	}
}

func looksLikePersonalFactQuestion(text string) bool {
	normalized := normalizePersonalFactText(text)
	if personalFactSubject(normalized) == "" || personalFactKind(normalized) == "" {
		return false
	}
	if strings.Contains(text, "?") {
		return true
	}
	return containsAny(normalized,
		"скажи", "підкаж", "який", "яка", "яке", "які",
		"знаєш", "пам'ятаєш", "згадай", "пригадай", "дай",
	)
}

func assistantIndicatesMissingFact(text string) bool {
	normalized := normalizePersonalFactText(text)
	return containsAny(normalized,
		"не знаю", "незнаю", "ще не знаю", "не маю",
		"немає", "не знайш", "мені невідом", "невідомо",
		"не збереж", "не пам'ята", "don't know",
	)
}

func assistantAlreadyRequestsFact(text string) bool {
	normalized := normalizePersonalFactText(text)
	return containsAny(normalized,
		"поділис", "поділися", "напиши", "надішли",
		"скажи мені", "дай мені", "запам'ятаю", "запамятаю", "збережу",
	)
}

func personalFactKind(normalized string) string {
	switch {
	case containsAny(normalized, "номер телефону", "телефон", "телефону", "мобіль", "мобiль"):
		return "phone"
	case strings.Contains(normalized, "номер") && personalFactSubject(normalized) != "":
		return "phone"
	case containsAny(normalized, "email", "e-mail", "емейл", "електронна пошта", "пошта"):
		return "email"
	case containsAny(normalized, "адрес", "де я живу"):
		return "address"
	case containsAny(normalized, "день народження", "дата народження"):
		return "birthday"
	default:
		return ""
	}
}

func personalFactSubject(normalized string) string {
	switch {
	case containsAny(normalized, "дружин", "жінк", "жiнк"):
		return "дружини користувача"
	case containsAny(normalized, "чоловік", "чоловiк", "чоловіка", "чоловiка"):
		return "чоловіка користувача"
	case containsAny(normalized, "мами", "мама", "матері", "матерi"):
		return "мами користувача"
	case containsAny(normalized, "тата", "батька", "батьків", "батькiв"):
		return "батька користувача"
	case containsAny(normalized, "брата"):
		return "брата користувача"
	case containsAny(normalized, "сестри"):
		return "сестри користувача"
	case containsAny(normalized, "сина"):
		return "сина користувача"
	case containsAny(normalized, "доньки", "дочки"):
		return "доньки користувача"
	case containsAny(normalized, "мій", "мiй", "моя", "моє", "мої", "моi", "мене", "мійого", "мого", "моєї", "моєі"):
		return "користувача"
	default:
		return ""
	}
}

func extractPhoneNumber(text string) string {
	for _, candidate := range phonePattern.FindAllString(text, -1) {
		digits := 0
		for _, symbol := range candidate {
			if unicode.IsDigit(symbol) {
				digits++
			}
		}
		if digits < 7 || digits > 15 {
			continue
		}
		candidate = strings.Trim(candidate, " \t\n\r.,;:")
		return strings.Join(strings.Fields(candidate), " ")
	}
	return ""
}

func normalizePersonalFactText(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	replacer := strings.NewReplacer(
		"’", "'",
		"ʼ", "'",
		"`", "'",
		"´", "'",
	)
	return strings.Join(strings.Fields(replacer.Replace(text)), " ")
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}
