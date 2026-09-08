package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"nova.local/core/internal/integrations/githubstatus"
	"nova.local/core/internal/jobs"
	"nova.local/core/internal/storage"
)

const (
	checkStatusOK             = "ok"
	checkStatusDegraded       = "degraded"
	checkStatusNotConfigured  = "not_configured"
	workerHeartbeatStaleAfter = 90 * time.Second
)

type operationalStatusResponse struct {
	Service   string                            `json:"service"`
	Status    string                            `json:"status"`
	Version   string                            `json:"version"`
	Timestamp string                            `json:"timestamp"`
	Checks    map[string]operationalStatusCheck `json:"checks"`
	Git       *githubstatus.Status              `json:"git,omitempty"`
}

type operationalStatusCheck struct {
	Label      string `json:"label"`
	Status     string `json:"status"`
	Detail     string `json:"detail,omitempty"`
	LastSeenAt string `json:"lastSeenAt,omitempty"`
	AgeSeconds int64  `json:"ageSeconds,omitempty"`
}

func (s *Server) getOperationalStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, s.buildOperationalStatus(ctx, s.requestUserID(r)))
}

func (s *Server) buildOperationalStatus(ctx context.Context, userID string) operationalStatusResponse {
	response := operationalStatusResponse{
		Service:   "nova-api",
		Status:    checkStatusOK,
		Version:   "0.1.0",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Checks:    make(map[string]operationalStatusCheck),
	}
	addCheck := func(key, label, status, detail string) {
		response.Checks[key] = operationalStatusCheck{Label: label, Status: status, Detail: detail}
		if status != checkStatusOK {
			response.Status = checkStatusDegraded
		}
	}

	addCheck("api", "API", checkStatusOK, "Core відповідає.")

	if s.db == nil {
		addCheck("postgres", "PostgreSQL", checkStatusNotConfigured, "База даних не підключена.")
	} else if err := s.db.Ping(ctx); err != nil {
		addCheck("postgres", "PostgreSQL", checkStatusDegraded, "База даних не відповідає.")
	} else {
		addCheck("postgres", "PostgreSQL", checkStatusOK, "База даних онлайн.")
	}

	redisOK := false
	if s.redis == nil {
		addCheck("redis", "Redis", checkStatusNotConfigured, "Redis не підключений.")
	} else if err := s.redis.Ping(ctx).Err(); err != nil {
		addCheck("redis", "Redis", checkStatusDegraded, "Redis не відповідає.")
	} else {
		redisOK = true
		addCheck("redis", "Redis", checkStatusOK, "Redis онлайн.")
	}

	if s.reminderQueue == nil {
		addCheck("reminderQueue", "Черга нагадувань", checkStatusNotConfigured, "Черга нагадувань не підключена.")
	} else if !redisOK {
		addCheck("reminderQueue", "Черга нагадувань", checkStatusDegraded, "Черга залежить від Redis, який зараз недоступний.")
	} else {
		addCheck("reminderQueue", "Черга нагадувань", checkStatusOK, "Черга прийому задач доступна.")
	}

	response.Checks["worker"] = s.workerStatusCheck(ctx, redisOK, time.Now().UTC())
	if response.Checks["worker"].Status != checkStatusOK {
		response.Status = checkStatusDegraded
	}

	if s.store == nil {
		addCheck("reminders", "Нагадування", checkStatusNotConfigured, "Сховище нагадувань не підключене.")
	} else {
		reminders, err := s.store.ListReminders(ctx, userID, "scheduled", 100)
		if err != nil {
			addCheck("reminders", "Нагадування", checkStatusDegraded, "Не вдалося прочитати заплановані нагадування.")
		} else {
			addCheck("reminders", "Нагадування", checkStatusOK, fmt.Sprintf("Заплановано: %d.", len(reminders)))
		}
	}

	if s.cfg.OpenAIAPIKey == "" {
		addCheck("openai", "OpenAI", checkStatusNotConfigured, "AI-відповіді без ключа недоступні.")
	} else {
		addCheck("openai", "OpenAI", checkStatusOK, "AI-відповіді увімкнені.")
	}

	switch {
	case !s.cfg.TelegramEnabled:
		addCheck("telegram", "Telegram", checkStatusNotConfigured, "Telegram вимкнений у конфігурації.")
	case s.cfg.TelegramBotToken == "":
		addCheck("telegram", "Telegram", checkStatusDegraded, "Telegram увімкнений, але bot token не заданий.")
	case !s.telegramReadyForDelivery():
		addCheck("telegram", "Telegram", checkStatusDegraded, "Bot token є, але немає chat/user id для доставки.")
	default:
		addCheck("telegram", "Telegram", checkStatusOK, "Telegram готовий для відповідей і нагадувань.")
	}

	if s.gitStatus == nil {
		addCheck("git", "Git / Deploy", checkStatusNotConfigured, "GitHub статус не підключений.")
	} else {
		gitStatus, err := s.gitStatus.Status(ctx, s.cfg.GitDeployedCommitSHA)
		if err != nil {
			addCheck("git", "Git / Deploy", checkStatusDegraded, "Не вдалося прочитати GitHub/deploy статус.")
		} else {
			response.Git = &gitStatus
			if gitStatus.Deployment.IsLatest {
				addCheck("git", "Git / Deploy", checkStatusOK, "Production працює на останньому commit.")
			} else {
				addCheck("git", "Git / Deploy", checkStatusDegraded, gitStatus.Deployment.Summary)
			}
		}
	}

	return response
}

func (s *Server) systemStatusAssistantText(ctx context.Context, userID string) string {
	statusCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	status := s.buildOperationalStatus(statusCtx, userID)
	return formatSystemStatusAssistantText(status)
}

func formatSystemStatusAssistantText(status operationalStatusResponse) string {
	headline := "Стан NOVA: все живе."
	if status.Status != checkStatusOK {
		headline = "Стан NOVA: є нюанси, але я на звʼязку."
	}
	lines := []string{headline}

	if check, ok := status.Checks["api"]; ok {
		lines = append(lines, "API: "+humanCheckStatus(check)+".")
	}
	if check, ok := status.Checks["postgres"]; ok {
		lines = append(lines, "База: "+humanCheckStatus(check)+".")
	}
	if check, ok := status.Checks["redis"]; ok {
		lines = append(lines, "Redis: "+humanCheckStatus(check)+".")
	}
	if check, ok := status.Checks["worker"]; ok {
		lines = append(lines, "Worker: "+humanCheckStatus(check)+".")
	}
	if check, ok := status.Checks["reminders"]; ok {
		lines = append(lines, "Нагадування: "+humanCheckStatus(check)+".")
	}
	if check, ok := status.Checks["telegram"]; ok {
		lines = append(lines, "Telegram: "+humanCheckStatus(check)+".")
	}
	if status.Git != nil {
		gitLine := fmt.Sprintf("Git: latest `%s`", status.Git.LatestCommit.ShortSHA)
		if status.Git.DeployedCommit.ShortSHA != "" {
			gitLine += fmt.Sprintf(", prod `%s`", status.Git.DeployedCommit.ShortSHA)
		}
		if status.Git.Deployment.IsLatest {
			gitLine += " — актуально."
		} else {
			gitLine += " — " + strings.TrimSuffix(status.Git.Deployment.Summary, ".") + "."
		}
		lines = append(lines, gitLine)
	} else if check, ok := status.Checks["git"]; ok {
		lines = append(lines, "Git: "+humanCheckStatus(check)+".")
	}

	attention := degradedChecks(status.Checks)
	if len(attention) > 0 {
		lines = append(lines, "Потребує уваги: "+strings.Join(attention, "; ")+".")
	}
	return strings.Join(lines, "\n")
}

func (s *Server) workerStatusCheck(ctx context.Context, redisOK bool, now time.Time) operationalStatusCheck {
	if s.store != nil {
		heartbeat, err := s.store.LatestServiceHeartbeat(ctx, jobs.ServiceWorker)
		if err == nil {
			return workerHeartbeatCheck(heartbeat, now)
		}
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return operationalStatusCheck{
				Label:  "Worker",
				Status: checkStatusDegraded,
				Detail: "Не вдалося прочитати worker heartbeat.",
			}
		}
	}
	return s.workerQueueVisibilityCheck(redisOK)
}

func workerHeartbeatCheck(heartbeat storage.ServiceHeartbeat, now time.Time) operationalStatusCheck {
	age := now.Sub(heartbeat.LastSeenAt)
	if age < 0 {
		age = 0
	}
	status := checkStatusOK
	detail := fmt.Sprintf("Останній heartbeat %s тому.", humanDuration(age))
	if heartbeat.InstanceID != "" {
		detail += " Instance: " + heartbeat.InstanceID + "."
	}
	if heartbeat.Status != "" && heartbeat.Status != checkStatusOK {
		status = checkStatusDegraded
		detail = "Worker відмітив статус `" + heartbeat.Status + "`. " + detail
	}
	if age > workerHeartbeatStaleAfter {
		status = checkStatusDegraded
		detail = fmt.Sprintf("Останній heartbeat %s тому — сигнал застарів.", humanDuration(age))
	}
	return operationalStatusCheck{
		Label:      "Worker",
		Status:     status,
		Detail:     detail,
		LastSeenAt: heartbeat.LastSeenAt.UTC().Format(time.RFC3339),
		AgeSeconds: int64(age.Seconds()),
	}
}

func (s *Server) workerQueueVisibilityCheck(redisOK bool) operationalStatusCheck {
	if s.redis == nil {
		return operationalStatusCheck{
			Label:  "Worker",
			Status: checkStatusNotConfigured,
			Detail: "Worker не можна перевірити без Redis.",
		}
	}
	if !redisOK {
		return operationalStatusCheck{
			Label:  "Worker",
			Status: checkStatusDegraded,
			Detail: "Worker не можна перевірити, бо Redis недоступний.",
		}
	}
	servers, err := asynq.NewInspectorFromRedisClient(s.redis).Servers()
	if err != nil {
		return operationalStatusCheck{
			Label:  "Worker",
			Status: checkStatusDegraded,
			Detail: "Не вдалося прочитати стан worker.",
		}
	}
	if len(servers) == 0 {
		return operationalStatusCheck{
			Label:  "Worker",
			Status: checkStatusDegraded,
			Detail: "У Redis не видно активного worker, і heartbeat ще не записувався.",
		}
	}
	return operationalStatusCheck{
		Label:  "Worker",
		Status: checkStatusDegraded,
		Detail: fmt.Sprintf("Worker видно в Redis (%d), але heartbeat ще не записувався.", len(servers)),
	}
}

func humanDuration(duration time.Duration) string {
	switch {
	case duration < time.Second:
		return "щойно"
	case duration < time.Minute:
		seconds := int(duration.Round(time.Second) / time.Second)
		return fmt.Sprintf("%d сек", seconds)
	case duration < time.Hour:
		minutes := int(duration.Round(time.Minute) / time.Minute)
		return fmt.Sprintf("%d хв", minutes)
	default:
		hours := int(duration.Round(time.Hour) / time.Hour)
		return fmt.Sprintf("%d год", hours)
	}
}

func humanCheckStatus(check operationalStatusCheck) string {
	detail := strings.TrimSuffix(strings.TrimSpace(check.Detail), ".")
	if detail == "" {
		detail = check.Label
	}
	switch check.Status {
	case checkStatusOK:
		return "ок — " + detail
	case checkStatusNotConfigured:
		return "не налаштовано — " + detail
	default:
		return "проблема — " + detail
	}
}

func degradedChecks(checks map[string]operationalStatusCheck) []string {
	priority := []string{"postgres", "redis", "worker", "reminderQueue", "reminders", "telegram", "openai", "git"}
	result := make([]string, 0)
	for _, key := range priority {
		check, ok := checks[key]
		if !ok || check.Status == checkStatusOK {
			continue
		}
		result = append(result, check.Label+": "+strings.TrimSuffix(check.Detail, "."))
	}
	return result
}
