package starquery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/starquery/kv"
	"github.com/google/go-github/v52/github"
)

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
	ctx, cancelFunc := context.WithCancel(ctx)
	mux := http.NewServeMux()
	api := &API{
		closeFunc:     cancelFunc,
		client:        opts.Client,
		kv:            opts.KV,
		logger:        opts.Logger,
		repos:         opts.Repos,
		mux:           mux,
		webhookSecret: opts.WebhookSecret,
	}
	mux.HandleFunc("GET /{org}/{repo}/user/{username}", func(w http.ResponseWriter, r *http.Request) {
		org, repoName := r.PathValue("org"), r.PathValue("repo")
		username := r.PathValue("username")
		repo := Repo{org, repoName}
		value, err := api.kv.Get(r.Context(), repo.Key(username))
		if err != nil {
			api.logger.Error("failed to get stargazer data", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		if value == "" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /webhook", func(w http.ResponseWriter, r *http.Request) {
		payload, err := github.ValidatePayload(r, []byte(api.webhookSecret))
		if err != nil {
			http.Error(w, "Invalid signature", http.StatusBadRequest)
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
		default:
			http.Error(w, "unsupported event type", http.StatusBadRequest)
			return
		}
		switch starEvent.GetAction() {
		case "created":
			// Do something with the star event.
			api.logger.Info("star added", "repo", starEvent.Repo.GetFullName(), "user", starEvent.Sender.GetLogin())
			storeStargazers(ctx, api.kv, Repo{starEvent.Repo.Owner.GetLogin(), starEvent.Repo.GetName()}, []Stargazer{{
				Login: starEvent.Sender.GetLogin(),
			}})
		case "deleted":
			api.logger.Info("star removed", "repo", starEvent.Repo.GetFullName(), "user", starEvent.Sender.GetLogin())
		}
	})
	api.wg.Add(1)
	go api.fetchLoop(ctx)
	return api
}

// API handles GitHub stargazer queries.
type API struct {
	closeFunc     context.CancelFunc
	client        *http.Client
	kv            kv.Store
	logger        *slog.Logger
	repos         []Repo
	mux           *http.ServeMux
	webhookSecret string
	wg            sync.WaitGroup
}

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mux.ServeHTTP(w, r)
}

func (a *API) Close() {
	a.closeFunc()
	a.wg.Wait()
}

func (a *API) fetchLoop(ctx context.Context) {
	defer a.wg.Done()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		for _, repo := range a.repos {
			if err := a.fetchByRepo(ctx, repo); err != nil {
				if ctx.Err() != nil {
					return
				}
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
		stargazers, resetTime, err := fetchStargazersFromGitHub(ctx, a.client, repo, cursor)
		if err != nil {
			return fmt.Errorf("fetch stargazers: %w", err)
		}

		if err := storeStargazers(ctx, a.kv, repo, stargazers); err != nil {
			return fmt.Errorf("store stargazers: %w", err)
		}

		a.logger.Info("stored stargazers", "repo", repo, "count", len(stargazers))

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
	}
	return nil
}

// storeStargazers stores the stargazers for the given repo.
func storeStargazers(ctx context.Context, kv kv.Store, repo Repo, stargazers []Stargazer) error {
	pairs := make([][2]string, len(stargazers))
	for i, s := range stargazers {
		pairs[i] = [2]string{repo.Key(s.Login), "true"}
	}
	// Store for 24hrs!
	return kv.Setex(ctx, 24*60*60, pairs)
}

// Repo represents a GitHub repository.
type Repo struct {
	Owner string
	Name  string
}

func (r Repo) String() string {
	return r.Owner + "/" + r.Name
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
func fetchStargazersFromGitHub(ctx context.Context, client *http.Client, repo Repo, cursor string) ([]Stargazer, time.Time, error) {
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

	body, err := json.Marshal(map[string]any{
		"query":     query,
		"variables": variables,
	})
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("marshal query: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.github.com/graphql", bytes.NewReader(body))
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, time.Time{}, fmt.Errorf("unexpected status: %d", resp.StatusCode)
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
		} `json:"data"`
		RateLimit struct {
			Remaining int    `json:"remaining"`
			ResetAt   string `json:"resetAt"`
		} `json:"rateLimit"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, time.Time{}, fmt.Errorf("decode response: %w", err)
	}

	var stargazers []Stargazer
	for _, edge := range response.Data.Repository.Stargazers.Edges {
		stargazers = append(stargazers, Stargazer{
			Login:  edge.Node.Login,
			Cursor: edge.Cursor,
		})
	}

	var resetTime time.Time
	if response.RateLimit.Remaining == 0 {
		resetTime, err = time.Parse(time.RFC3339, response.RateLimit.ResetAt)
		if err != nil {
			return nil, time.Time{}, fmt.Errorf("parse reset time: %w", err)
		}
	}

	return stargazers, resetTime, nil
}
