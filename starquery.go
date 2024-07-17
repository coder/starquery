// Package starquery provides functionality to query and manage GitHub stargazers.
package starquery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/starquery/kv"
	"github.com/google/go-github/v52/github"
)

// API handles GitHub stargazer queries.
type API struct {
	client        *http.Client
	kv            kv.Store
	logger        *slog.Logger
	repos         []Repo
	mux           *http.ServeMux
	webhookSecret string
	wg            sync.WaitGroup
	closeFunc     context.CancelFunc
}

// Options holds configuration for the API.
type Options struct {
	Client        *http.Client
	KV            kv.Store
	Logger        *slog.Logger
	Repos         []Repo
	WebhookSecret string
}

// New creates a new API handler that fetches stargazers for the given repos.
func New(ctx context.Context, opts Options) *API {
	if opts.Client == nil {
		opts.Client = http.DefaultClient
	}
	if opts.KV == nil {
		opts.KV = kv.NewMemory()
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	ctx, cancel := context.WithCancel(ctx)

	api := &API{
		client:        opts.Client,
		kv:            opts.KV,
		logger:        opts.Logger,
		repos:         opts.Repos,
		mux:           http.NewServeMux(),
		webhookSecret: opts.WebhookSecret,
		closeFunc:     cancel,
	}

	api.mux.HandleFunc("GET /{org}/{repo}/user/{username}", api.handleStarredByUser)
	api.mux.HandleFunc("POST /webhook", api.handleWebhook)

	api.wg.Add(1)
	go api.fetchLoop(ctx)

	return api
}

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mux.ServeHTTP(w, r)
}

// Close shuts down the API and waits for all goroutines to finish.
func (a *API) Close() {
	a.closeFunc()
	a.wg.Wait()
}

// handleStarredByUser returns 404 if the user has not starred, and
// 204 if the user has starred the repo.
func (a *API) handleStarredByUser(w http.ResponseWriter, r *http.Request) {
	org := r.PathValue("org")
	repoName := r.PathValue("repo")
	username := r.PathValue("username")

	repo := Repo{Owner: org, Name: repoName}
	value, err := a.kv.Get(r.Context(), repo.Key(username))
	if err != nil {
		a.logger.Error("failed to get stargazer data", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if value == "" {
		http.NotFound(w, r)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// handleWebhook handles a GitHub webhook event.
func (a *API) handleWebhook(w http.ResponseWriter, r *http.Request) {
	payload, err := github.ValidatePayload(r, []byte(a.webhookSecret))
	if err != nil {
		http.Error(w, fmt.Sprintf("invalid signature: %s", err), http.StatusBadRequest)
		return
	}

	event, err := github.ParseWebHook(github.WebHookType(r), payload)
	if err != nil {
		http.Error(w, "failed to parse request body", http.StatusBadRequest)
		return
	}

	var starEvent *github.StarEvent
	switch event := event.(type) {
	case *github.StarEvent:
		starEvent = event
	case *github.PingEvent:
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
		return
	}

	owner := starEvent.Repo.Owner.GetLogin()
	if owner == "" {
		http.Error(w, "missing owner", http.StatusBadRequest)
		return
	}

	name := starEvent.Repo.GetName()
	if name == "" {
		http.Error(w, "missing name", http.StatusBadRequest)
		return
	}

	repo := Repo{Owner: owner, Name: name}
	username := starEvent.Sender.GetLogin()

	switch starEvent.GetAction() {
	case "created":
		a.logger.Info("star added", "repo", starEvent.Repo.GetFullName(), "user", username)
		err = a.storeStargazers(r.Context(), repo, []Stargazer{{Login: username}})
	case "deleted":
		a.logger.Info("star removed", "repo", starEvent.Repo.GetFullName(), "user", username)
		err = a.kv.Delete(r.Context(), repo.Key(username))
	default:
		http.Error(w, "unsupported action", http.StatusBadRequest)
		return
	}

	if err != nil {
		a.logger.Error("failed to update stargazer data", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (a *API) fetchLoop(ctx context.Context) {
	defer a.wg.Done()

	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()

	for {
		for _, repo := range a.repos {
			if err := a.fetchByRepo(ctx, repo); err != nil && ctx.Err() == nil {
				a.logger.Error("failed to fetch stargazers", "repo", repo, "error", err)
			}
		}

		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

// fetchByRepo fetches stargazers for the given repo.
func (a *API) fetchByRepo(ctx context.Context, repo Repo) error {
	var cursor string
	for {
		a.logger.Info("fetching stargazers", "repo", repo)
		stargazers, resetTime, remaining, err := a.fetchStargazersFromGitHub(ctx, repo, cursor)
		if err != nil {
			return fmt.Errorf("fetch stargazers: %w", err)
		}

		if err := a.storeStargazers(ctx, repo, stargazers); err != nil {
			return fmt.Errorf("store stargazers: %w", err)
		}

		a.logger.Info("stored stargazers", "repo", repo, "count", len(stargazers), "rate_limit_remaining", remaining)

		if !resetTime.IsZero() {
			waitDuration := time.Until(resetTime) + time.Second
			a.logger.Info("rate limit reached", "repo", repo, "wait", waitDuration)
			select {
			case <-time.After(waitDuration):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		if len(stargazers) == 0 {
			break
		}
		cursor = stargazers[len(stargazers)-1].Cursor
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return nil
}

// storeStargazers stores the stargazers for the given repo.
func (a *API) storeStargazers(ctx context.Context, repo Repo, stargazers []Stargazer) error {
	if len(stargazers) == 0 {
		return nil
	}
	pairs := make([][2]string, len(stargazers))
	for i, s := range stargazers {
		pairs[i] = [2]string{repo.Key(s.Login), "true"}
	}
	// Store for 24hrs!
	return a.kv.Setex(ctx, 24*60*60, pairs)
}

// Repo represents a GitHub repository.
type Repo struct {
	Owner string
	Name  string
}

func (r Repo) String() string {
	return fmt.Sprintf("%s/%s", r.Owner, r.Name)
}

// Key returns the storage key for the repo with the username.
func (r Repo) Key(username string) string {
	return fmt.Sprintf("stargazers:%s/%s/%s", r.Owner, r.Name, username)
}

// Stargazer stores the username and cursor of the user starring.
type Stargazer struct {
	Login  string
	Cursor string
}

// fetchStargazersFromGitHub fetches stargazers for the given repo from GitHub.
func (a *API) fetchStargazersFromGitHub(ctx context.Context, repo Repo, cursor string) ([]Stargazer, time.Time, int, error) {
	variables := map[string]string{
		"owner": repo.Owner,
		"name":  repo.Name,
		"after": cursor,
	}
	query := `
	query($owner: String!, $name: String!, $after: String) {
		repository(owner: $owner, name: $name) {
			stargazers(first: 100, after: $after) {
				edges {
					node {
						login
					}
					cursor
				}
			}
		}
		rateLimit {
			remaining
			resetAt
		}
	}`

	reqBody, err := json.Marshal(map[string]any{
		"query":     query,
		"variables": variables,
	})
	if err != nil {
		return nil, time.Time{}, 0, fmt.Errorf("marshal query: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.github.com/graphql", bytes.NewReader(reqBody))
	if err != nil {
		return nil, time.Time{}, 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, time.Time{}, 0, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, time.Time{}, 0, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var response struct {
		Data struct {
			Repository struct {
				Stargazers struct {
					Edges []struct {
						Node struct {
							Login string `json:"login"`
						} `json:"node"`
						Cursor string `json:"cursor"`
					} `json:"edges"`
				} `json:"stargazers"`
			} `json:"repository"`
			RateLimit struct {
				Remaining int    `json:"remaining"`
				ResetAt   string `json:"resetAt"`
			} `json:"rateLimit"`
		} `json:"data"`
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, time.Time{}, 0, fmt.Errorf("read response: %w", err)
	}

	if err := json.Unmarshal(body, &response); err != nil {
		return nil, time.Time{}, 0, fmt.Errorf("decode response: %w", err)
	}

	var resetTime time.Time
	if response.Data.RateLimit.Remaining == 0 {
		resetTime, err = time.Parse(time.RFC3339, response.Data.RateLimit.ResetAt)
		if err != nil {
			return nil, time.Time{}, 0, fmt.Errorf("parse reset time: %w: %s", err, body)
		}
	}

	var stargazers []Stargazer
	for _, edge := range response.Data.Repository.Stargazers.Edges {
		stargazers = append(stargazers, Stargazer{
			Login:  edge.Node.Login,
			Cursor: edge.Cursor,
		})
	}

	return stargazers, resetTime, response.Data.RateLimit.Remaining, nil
}
