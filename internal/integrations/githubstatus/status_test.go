package githubstatus

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeRepository(t *testing.T) {
	tests := map[string]string{
		"politop123/nova":                        "politop123/nova",
		"https://github.com/politop123/nova":     "politop123/nova",
		"https://github.com/politop123/nova.git": "politop123/nova",
		"git@github.com:politop123/nova.git":     "politop123/nova",
	}
	for input, want := range tests {
		got, err := NormalizeRepository(input)
		if err != nil {
			t.Fatalf("NormalizeRepository(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("NormalizeRepository(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestStatusReportsCurrentDeployment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/politop123/nova/commits/dev":
			w.Write([]byte(`{
				"sha": "abcdef1234567890",
				"html_url": "https://github.com/politop123/nova/commit/abcdef1234567890",
				"commit": {
					"message": "feat: devops awareness\n\nMore details",
					"author": { "name": "NOVA", "date": "2026-09-08T10:00:00Z" },
					"committer": { "name": "NOVA", "date": "2026-09-08T10:01:00Z" }
				}
			}`))
		case r.URL.Path == "/repos/politop123/nova/actions/runs":
			if got := r.URL.Query().Get("branch"); got != "dev" {
				t.Fatalf("branch query = %q, want dev", got)
			}
			w.Write([]byte(`{
				"workflow_runs": [{
					"id": 42,
					"name": "Deploy to Oracle VM",
					"path": ".github/workflows/deploy-dev.yml",
					"status": "completed",
					"conclusion": "success",
					"head_sha": "abcdef1234567890",
					"html_url": "https://github.com/politop123/nova/actions/runs/42",
					"created_at": "2026-09-08T10:02:00Z",
					"updated_at": "2026-09-08T10:05:00Z"
				}]
			}`))
		default:
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := New("politop123/nova", "dev", "secret", WithBaseURL(server.URL), WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	status, err := client.Status(context.Background(), "abcdef1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.LatestCommit.ShortSHA != "abcdef1" || status.LatestCommit.Title != "feat: devops awareness" {
		t.Fatalf("unexpected latest commit: %#v", status.LatestCommit)
	}
	if !status.Deployment.IsLatest || status.Deployment.State != "current" {
		t.Fatalf("unexpected deployment: %#v", status.Deployment)
	}
	if status.LatestDeployRun == nil || status.LatestDeployRun.ID != 42 {
		t.Fatalf("unexpected deploy run: %#v", status.LatestDeployRun)
	}
}

func TestStatusKeepsCommitWhenActionsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/politop123/nova/commits/dev":
			w.Write([]byte(`{
				"sha": "abcdef1234567890",
				"html_url": "",
				"commit": {
					"message": "fix: something",
					"author": { "name": "NOVA", "date": "2026-09-08T10:00:00Z" },
					"committer": { "name": "NOVA", "date": "2026-09-08T10:01:00Z" }
				}
			}`))
		case r.URL.Path == "/repos/politop123/nova/actions/runs":
			http.Error(w, "no actions", http.StatusForbidden)
		default:
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := New("politop123/nova", "dev", "", WithBaseURL(server.URL), WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	status, err := client.Status(context.Background(), "1234567")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.ActionsError == "" || !strings.Contains(status.ActionsError, "403") {
		t.Fatalf("expected actions error, got %q", status.ActionsError)
	}
	if status.Deployment.State != "behind" {
		t.Fatalf("deployment state = %q, want behind", status.Deployment.State)
	}
}

func TestMatchesSHA(t *testing.T) {
	if !MatchesSHA("abcdef1", "abcdef1234567890") {
		t.Fatal("expected short SHA to match full SHA")
	}
	if MatchesSHA("abc", "abcdef1234567890") {
		t.Fatal("did not expect too-short SHA to match")
	}
}
