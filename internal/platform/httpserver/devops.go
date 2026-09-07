package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"nova.local/core/internal/integrations/githubstatus"
)

var commitReferencePattern = regexp.MustCompile(`(?i)\b[0-9a-f]{7,40}\b`)

func (s *Server) getGitStatus(w http.ResponseWriter, r *http.Request) {
	if s.gitStatus == nil {
		writeError(w, http.StatusServiceUnavailable, "GitHub status is not configured")
		return
	}
	status, err := s.gitStatus.Status(r.Context(), s.cfg.GitDeployedCommitSHA)
	if err != nil {
		s.logger.Warn("GitHub status request failed", "error", err)
		writeError(w, http.StatusBadGateway, "GitHub status is temporarily unavailable")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) gitStatusAssistantText(ctx context.Context, userText string) string {
	if s.gitStatus == nil {
		return "GitHub статус NOVA ще не підключений на бекенді. Потрібно вказати репозиторій і, якщо repo приватний, серверний GitHub token."
	}
	status, err := s.gitStatus.Status(ctx, s.cfg.GitDeployedCommitSHA)
	if err != nil {
		return "Я не змогла зараз прочитати GitHub статус NOVA. Якщо репозиторій приватний, додай серверний `NOVA_GITHUB_TOKEN`, і наступного разу я зможу перевірити commit/deploy."
	}
	return formatGitStatusAssistantText(status, userText)
}

func formatGitStatusAssistantText(status githubstatus.Status, userText string) string {
	latest := status.LatestCommit
	latestTitle := strings.TrimSpace(latest.Title)
	if latestTitle == "" {
		latestTitle = "без назви"
	}

	askedSHA := extractCommitReference(userText)
	lines := make([]string, 0, 4)
	if askedSHA != "" {
		lines = append(lines, formatAskedCommitStatus(askedSHA, status))
	} else {
		lines = append(lines, fmt.Sprintf(
			"Останній commit у `%s`: `%s` — %s.",
			status.Branch,
			latest.ShortSHA,
			latestTitle,
		))
		if status.Deployment.IsLatest {
			lines = append(lines, "Так, він уже задеплоєний у production.")
		} else {
			lines = append(lines, status.Deployment.Summary)
			if status.DeployedCommit.ShortSHA != "" {
				lines = append(lines, fmt.Sprintf("Зараз production показує `%s`.", status.DeployedCommit.ShortSHA))
			}
		}
	}

	if run := status.LatestDeployRun; run != nil {
		lines = append(lines, fmt.Sprintf(
			"Останній deploy workflow: %s для `%s`.",
			humanWorkflowStatus(run.Status, run.Conclusion),
			run.ShortHeadSHA,
		))
	}
	if status.ActionsError != "" {
		lines = append(lines, "Commit я бачу, але GitHub Actions зараз не прочитались — можливо, бракує GitHub token або спрацював rate limit.")
	}
	return strings.Join(lines, "\n")
}

func formatAskedCommitStatus(askedSHA string, status githubstatus.Status) string {
	askedShortSHA := githubstatus.ShortSHA(askedSHA)
	switch {
	case githubstatus.MatchesSHA(askedSHA, status.DeployedCommit.SHA):
		return fmt.Sprintf("Так, commit `%s` зараз запущений у production.", askedShortSHA)
	case githubstatus.MatchesSHA(askedSHA, status.LatestCommit.SHA) && status.Deployment.IsLatest:
		return fmt.Sprintf("Так, commit `%s` — це останній commit у `%s`, і він уже в production.", askedShortSHA, status.Branch)
	case githubstatus.MatchesSHA(askedSHA, status.LatestCommit.SHA):
		deployed := status.DeployedCommit.ShortSHA
		if deployed == "" {
			deployed = "невідомий"
		}
		return fmt.Sprintf("Commit `%s` — останній у `%s`, але production зараз показує `%s`.", askedShortSHA, status.Branch, deployed)
	default:
		return fmt.Sprintf("Commit `%s` не схожий ні на останній commit у `%s`, ні на поточний production SHA.", askedShortSHA, status.Branch)
	}
}

func extractCommitReference(text string) string {
	return strings.ToLower(commitReferencePattern.FindString(text))
}

func humanWorkflowStatus(status, conclusion string) string {
	status = strings.TrimSpace(strings.ToLower(status))
	conclusion = strings.TrimSpace(strings.ToLower(conclusion))
	if status == "completed" {
		switch conclusion {
		case "success":
			return "успішно завершився"
		case "failure", "timed_out", "action_required":
			return "завершився з помилкою"
		case "cancelled":
			return "скасований"
		case "skipped":
			return "пропущений"
		case "":
			return "завершився"
		default:
			return "завершився: " + conclusion
		}
	}
	switch status {
	case "queued", "requested", "waiting", "pending":
		return "у черзі"
	case "in_progress":
		return "ще виконується"
	default:
		if status == "" {
			return "статус невідомий"
		}
		return status
	}
}
