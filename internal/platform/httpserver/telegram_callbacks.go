package httpserver

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"nova.local/core/internal/integrations/telegram"
	"nova.local/core/internal/policy"
	"nova.local/core/internal/storage"
)

type telegramCallbackClient interface {
	AnswerCallback(ctx context.Context, id, text string) error
	FinishReminderMessage(ctx context.Context, chatID, messageID int64, text string) error
}

// Callback data is untrusted. These write actions require an authenticated
// webhook and an explicitly allowlisted private chat, even in permissive dev mode.
func (s *Server) telegramReminderCallbackAllowed(query telegram.CallbackQuery) bool {
	userID := strings.TrimSpace(s.cfg.TelegramAllowedUserID)
	chatID := strings.TrimSpace(s.cfg.TelegramAllowedChatID)
	if strings.TrimSpace(s.cfg.TelegramWebhookSecret) == "" || (userID == "" && chatID == "") {
		return false
	}
	if query.From == nil || query.From.IsBot || query.From.ID <= 0 || query.Message == nil {
		return false
	}
	if query.ID == "" || len(query.ID) > 256 || query.Message.MessageID <= 0 || query.Message.Date == 0 ||
		query.Message.Chat.Type != "private" || query.Message.Chat.ID != query.From.ID {
		return false
	}
	actual := strconv.FormatInt(query.From.ID, 10)
	return (userID == "" || userID == actual) && (chatID == "" || chatID == actual)
}

func (s *Server) telegramReminderCallback(w http.ResponseWriter, r *http.Request, query telegram.CallbackQuery) {
	client, ok := s.telegram.(telegramCallbackClient)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "telegram reminder controls are unavailable")
		return
	}
	answer := func(text string) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := client.AnswerCallback(ctx, query.ID, text); err != nil {
			// Provider transport errors can include credentials in their URL.
			s.logger.Warn("telegram reminder callback acknowledgement failed")
		}
	}
	if !s.telegramReminderCallbackAllowed(query) {
		answer("Керування доступне лише власнику в налаштованому приватному чаті.")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	grantID, action, err := telegram.ParseReminderCallback(query.Data)
	if err != nil {
		answer("Ця кнопка не підтримується.")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	decision := policy.Evaluate(policy.Context{UserID: s.userID, ToolName: action.ActionName(),
		Permission: policy.Write, Authenticated: true, ToolEnabled: true})
	if decision.Outcome != policy.Allow {
		answer("Цю дію зараз заборонено.")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	conversation, err := s.telegramConversation(ctx, s.userID)
	var result storage.ReminderQuickActionResult
	if err == nil {
		result, err = s.store.ApplyReminderQuickAction(ctx, s.userID, grantID, "telegram",
			strconv.FormatInt(query.Message.Chat.ID, 10), conversation.ID, newTraceID(), action)
	}
	if errors.Is(err, storage.ErrNotFound) || errors.Is(err, storage.ErrReminderActionExpired) {
		answer("Ця кнопка вже неактуальна. Перевір список нагадувань.")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		s.logger.Error("telegram reminder action failed", "error", err)
		answer("Не вдалося зберегти зміну. Спробуй ще раз.")
		writeError(w, http.StatusServiceUnavailable, "reminder action could not be saved")
		return
	}
	if result.Replayed {
		answer("Цю дію вже виконано. Повторної зміни не було.")
	} else {
		answer(result.Reply)
	}
	// The database commit is authoritative. UI/network failures must not repeat
	// the mutation, audit entry or shared conversation messages.
	editCtx, editCancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer editCancel()
	if err := client.FinishReminderMessage(editCtx, query.Message.Chat.ID, query.Message.MessageID, truncateTelegramText(result.Reply)); err != nil {
		s.logger.Warn("telegram reminder keyboard update failed", "reminder_id", result.Reminder.ID)
	}
	w.WriteHeader(http.StatusNoContent)
}
