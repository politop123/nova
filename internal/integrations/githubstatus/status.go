package githubstatus

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultAPIBase = "https://api.github.com"
	defaultBranch  = "dev"
)

// Client reads the public or token-authenticated GitHub state NOVA needs to
// answer operational questions. It is intentionally read-only.
type Client struct {
	repository string
	branch     string
	token      string
	apiBase    *url.URL
	httpClient *http.Client
}

type Option func(*Client) error

func WithBaseURL(rawURL string) Option {
	return func(client *Client) error {
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" {
			return nil
		}
		parsed, err := url.Parse(rawURL)
		if err != nil {
			return fmt.Errorf("parse GitHub API base URL: %w", err)
		}
		if parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("GitHub API base URL must include scheme and host")
		}
		client.apiBase = parsed
		return nil
	}
}

func WithHTTPClient(httpClient *http.Client) Option {
	return func(client *Client) error {
		if httpClient != nil {
			client.httpClient = httpClient
		}
		return nil
	}
}

func New(repository, branch, token string, options ...Option) (*Client, error) {
	normalizedRepository, err := NormalizeRepository(repository)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(branch) == "" {
		branch = defaultBranch
	}
	apiBase, _ := url.Parse(defaultAPIBase)
	client := &Client{
		repository: normalizedRepository,
		branch:     strings.TrimSpace(branch),
		token:      strings.TrimSpace(token),
		apiBase:    apiBase,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(client); err != nil {
			return nil, err
		}
	}
	return client, nil
}

type Status struct {
	Repository      string       `json:"repository"`
	Branch          string       `json:"branch"`
	LatestCommit    Commit       `json:"latestCommit"`
	DeployedCommit  CommitRef    `json:"deployedCommit"`
	LatestDeployRun *WorkflowRun `json:"latestDeployRun,omitempty"`
	Deployment      Deployment   `json:"deployment"`
	ActionsError    string       `json:"actionsError,omitempty"`
	CheckedAt       time.Time    `json:"checkedAt"`
}

type Commit struct {
	SHA         string    `json:"sha"`
	ShortSHA    string    `json:"shortSha"`
	Message     string    `json:"message"`
	Title       string    `json:"title"`
	AuthorName  string    `json:"authorName,omitempty"`
	CommittedAt time.Time `json:"committedAt,omitempty"`
	URL         string    `json:"url,omitempty"`
}

type CommitRef struct {
	SHA      string `json:"sha,omitempty"`
	ShortSHA string `json:"shortSha,omitempty"`
}

type WorkflowRun struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Path         string    `json:"path,omitempty"`
	Status       string    `json:"status"`
	Conclusion   string    `json:"conclusion,omitempty"`
	HeadSHA      string    `json:"headSha"`
	ShortHeadSHA string    `json:"shortHeadSha"`
	URL          string    `json:"url,omitempty"`
	CreatedAt    time.Time `json:"createdAt,omitempty"`
	UpdatedAt    time.Time `json:"updatedAt,omitempty"`
}

type Deployment struct {
	State    string `json:"state"`
	IsLatest bool   `json:"isLatest"`
	Summary  string `json:"summary"`
}

func (client *Client) Status(ctx context.Context, deployedSHA string) (Status, error) {
	latestCommit, err := client.fetchLatestCommit(ctx)
	if err != nil {
		return Status{}, err
	}

	status := Status{
		Repository:     client.repository,
		Branch:         client.branch,
		LatestCommit:   latestCommit,
		DeployedCommit: CommitRef{SHA: strings.TrimSpace(deployedSHA), ShortSHA: ShortSHA(deployedSHA)},
		CheckedAt:      time.Now().UTC(),
	}

	runs, err := client.fetchWorkflowRuns(ctx)
	if err != nil {
		status.ActionsError = err.Error()
	}
	status.LatestDeployRun = chooseDeployRun(runs)
	status.Deployment = buildDeployment(latestCommit.SHA, status.DeployedCommit.SHA, status.LatestDeployRun)
	return status, nil
}

func NormalizeRepository(repository string) (string, error) {
	repository = strings.TrimSpace(repository)
	repository = strings.TrimSuffix(repository, ".git")
	repository = strings.TrimPrefix(repository, "git@github.com:")
	if parsed, err := url.Parse(repository); err == nil && parsed.Host != "" {
		if !strings.EqualFold(parsed.Host, "github.com") {
			return "", fmt.Errorf("only github.com repositories are supported")
		}
		repository = strings.TrimPrefix(parsed.Path, "/")
		repository = strings.TrimSuffix(repository, ".git")
	}
	repository = strings.Trim(repository, "/")
	parts := strings.Split(repository, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", fmt.Errorf("GitHub repository must be owner/name")
	}
	return strings.ToLower(strings.TrimSpace(parts[0])) + "/" + strings.TrimSpace(parts[1]), nil
}

func ShortSHA(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}

func MatchesSHA(left, right string) bool {
	left = strings.ToLower(strings.TrimSpace(left))
	right = strings.ToLower(strings.TrimSpace(right))
	if left == "" || right == "" {
		return false
	}
	if left == right {
		return true
	}
	if len(left) < 7 || len(right) < 7 {
		return false
	}
	if len(left) < len(right) {
		return strings.HasPrefix(right, left)
	}
	return strings.HasPrefix(left, right)
}

func (client *Client) fetchLatestCommit(ctx context.Context) (Commit, error) {
	var response commitResponse
	if err := client.fetchJSON(ctx, client.endpoint(client.repositoryPath()+"/commits/"+url.PathEscape(client.branch), nil), &response); err != nil {
		return Commit{}, fmt.Errorf("fetch latest commit: %w", err)
	}
	title := strings.TrimSpace(strings.Split(response.Commit.Message, "\n")[0])
	return Commit{
		SHA:         response.SHA,
		ShortSHA:    ShortSHA(response.SHA),
		Message:     strings.TrimSpace(response.Commit.Message),
		Title:       title,
		AuthorName:  strings.TrimSpace(response.Commit.Author.Name),
		CommittedAt: response.Commit.Committer.Date,
		URL:         response.HTMLURL,
	}, nil
}

func (client *Client) fetchWorkflowRuns(ctx context.Context) ([]WorkflowRun, error) {
	query := url.Values{}
	query.Set("branch", client.branch)
	query.Set("per_page", "20")
	var response workflowRunsResponse
	if err := client.fetchJSON(ctx, client.endpoint(client.repositoryPath()+"/actions/runs", query), &response); err != nil {
		return nil, fmt.Errorf("fetch workflow runs: %w", err)
	}
	runs := make([]WorkflowRun, 0, len(response.WorkflowRuns))
	for _, run := range response.WorkflowRuns {
		runs = append(runs, WorkflowRun{
			ID:           run.ID,
			Name:         strings.TrimSpace(run.Name),
			Path:         strings.TrimSpace(run.Path),
			Status:       strings.TrimSpace(run.Status),
			Conclusion:   strings.TrimSpace(run.Conclusion),
			HeadSHA:      strings.TrimSpace(run.HeadSHA),
			ShortHeadSHA: ShortSHA(run.HeadSHA),
			URL:          strings.TrimSpace(run.HTMLURL),
			CreatedAt:    run.CreatedAt,
			UpdatedAt:    run.UpdatedAt,
		})
	}
	return runs, nil
}

func (client *Client) fetchJSON(ctx context.Context, requestURL string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "nova-core")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if client.token != "" {
		request.Header.Set("Authorization", "Bearer "+client.token)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		detail := strings.TrimSpace(string(body))
		if detail != "" {
			return fmt.Errorf("%s returned %s: %s", request.URL.Path, response.Status, detail)
		}
		return fmt.Errorf("%s returned %s", request.URL.Path, response.Status)
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		return fmt.Errorf("decode GitHub response: %w", err)
	}
	return nil
}

func (client *Client) repositoryPath() string {
	owner, name, _ := strings.Cut(client.repository, "/")
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
}

func (client *Client) endpoint(path string, query url.Values) string {
	parsed := *client.apiBase
	parsed.Path = strings.TrimRight(parsed.Path, "/") + path
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func chooseDeployRun(runs []WorkflowRun) *WorkflowRun {
	for index := range runs {
		if isDeployRun(runs[index]) {
			return &runs[index]
		}
	}
	if len(runs) == 0 {
		return nil
	}
	return &runs[0]
}

func isDeployRun(run WorkflowRun) bool {
	needle := strings.ToLower(run.Name + " " + run.Path)
	return strings.Contains(needle, "deploy") && strings.Contains(needle, "dev")
}

func buildDeployment(latestSHA, deployedSHA string, run *WorkflowRun) Deployment {
	switch {
	case strings.TrimSpace(deployedSHA) == "" || strings.EqualFold(strings.TrimSpace(deployedSHA), "dev"):
		return Deployment{
			State:    "unknown",
			IsLatest: false,
			Summary:  "NOVA ще не бачить SHA запущеної production-версії.",
		}
	case MatchesSHA(deployedSHA, latestSHA):
		return Deployment{
			State:    "current",
			IsLatest: true,
			Summary:  "Production уже працює на останньому commit.",
		}
	case run != nil && MatchesSHA(run.HeadSHA, latestSHA) && run.Status != "completed":
		return Deployment{
			State:    "deploying",
			IsLatest: false,
			Summary:  "Останній commit зараз проходить deploy workflow.",
		}
	case run != nil && MatchesSHA(run.HeadSHA, latestSHA) && run.Conclusion == "success":
		return Deployment{
			State:    "published",
			IsLatest: false,
			Summary:  "Workflow для останнього commit успішний, але production ще показує інший SHA.",
		}
	case run != nil && MatchesSHA(run.HeadSHA, latestSHA) && run.Conclusion != "":
		return Deployment{
			State:    "failed",
			IsLatest: false,
			Summary:  "Deploy workflow для останнього commit завершився неуспішно.",
		}
	default:
		return Deployment{
			State:    "behind",
			IsLatest: false,
			Summary:  "Production відстає від останнього commit у Git.",
		}
	}
}

type commitResponse struct {
	SHA     string `json:"sha"`
	HTMLURL string `json:"html_url"`
	Commit  struct {
		Message string `json:"message"`
		Author  struct {
			Name string    `json:"name"`
			Date time.Time `json:"date"`
		} `json:"author"`
		Committer struct {
			Name string    `json:"name"`
			Date time.Time `json:"date"`
		} `json:"committer"`
	} `json:"commit"`
}

type workflowRunsResponse struct {
	WorkflowRuns []workflowRunResponse `json:"workflow_runs"`
}

type workflowRunResponse struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	HeadSHA    string    `json:"head_sha"`
	HTMLURL    string    `json:"html_url"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}
