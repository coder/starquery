package starquery_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/starquery"
	"github.com/coder/starquery/kv"
	"github.com/google/go-github/v52/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebhook(t *testing.T) {
	t.Parallel()

	t.Run("Store", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		kv := kv.NewMemory()
		api := starquery.New(ctx, starquery.Options{
			KV:            kv,
			WebhookSecret: "secret",
		})
		defer api.Close()
		repo := starquery.Repo{Owner: "coder", Name: "coder"}
		req := generateWebhook(t, "secret", generateEvent(repo, "kylecarbs", "created"))
		res := httptest.NewRecorder()
		api.ServeHTTP(res, req)
		require.Equal(t, http.StatusOK, res.Code, "unexpected status code")
		v, err := kv.Get(ctx, repo.Key("kylecarbs"))
		require.NoError(t, err)
		require.NotEmpty(t, v)
	})

	t.Run("Delete", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		kv := kv.NewMemory()
		api := starquery.New(ctx, starquery.Options{
			KV:            kv,
			WebhookSecret: "secret",
		})
		defer api.Close()
		repo := starquery.Repo{Owner: "coder", Name: "coder"}
		err := kv.Setex(ctx, 60, [][2]string{{repo.Key("kylecarbs"), "true"}})
		require.NoError(t, err)
		req := generateWebhook(t, "secret", generateEvent(repo, "kylecarbs", "deleted"))
		res := httptest.NewRecorder()
		api.ServeHTTP(res, req)
		require.Equal(t, http.StatusOK, res.Code, "unexpected status code")
		v, err := kv.Get(ctx, repo.Key("kylecarbs"))
		require.NoError(t, err)
		require.Empty(t, v)
	})

	t.Run("InvalidSignature", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		api := starquery.New(ctx, starquery.Options{
			KV:            kv.NewMemory(),
			WebhookSecret: "secret",
		})
		defer api.Close()
		repo := starquery.Repo{Owner: "coder", Name: "coder"}
		req := generateWebhook(t, "wrong_secret", generateEvent(repo, "kylecarbs", "created"))
		res := httptest.NewRecorder()
		api.ServeHTTP(res, req)
		require.Equal(t, http.StatusBadRequest, res.Code, "expected bad request for invalid signature")
	})

	t.Run("UnsupportedEvent", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		api := starquery.New(ctx, starquery.Options{
			KV:            kv.NewMemory(),
			WebhookSecret: "secret",
		})
		defer api.Close()
		req := httptest.NewRequest(http.MethodPost, "/webhook", nil)
		req.Header.Set("X-GitHub-Event", "unsupported_event")
		res := httptest.NewRecorder()
		api.ServeHTTP(res, req)
		require.Equal(t, http.StatusBadRequest, res.Code, "expected bad request for unsupported event")
	})
}

func TestStarredByUser(t *testing.T) {
	t.Parallel()

	t.Run("Starred", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		kv := kv.NewMemory()
		api := starquery.New(ctx, starquery.Options{KV: kv})
		defer api.Close()
		repo := starquery.Repo{Owner: "coder", Name: "coder"}
		err := kv.Setex(ctx, 60, [][2]string{{repo.Key("kylecarbs"), "true"}})
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodGet, "/coder/coder/user/kylecarbs", nil)
		res := httptest.NewRecorder()
		api.ServeHTTP(res, req)
		require.Equal(t, http.StatusOK, res.Code, "unexpected status code")
	})

	t.Run("Not", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		api := starquery.New(ctx, starquery.Options{KV: kv.NewMemory()})
		defer api.Close()
		req := httptest.NewRequest(http.MethodGet, "/coder/coder/user/kylecarbs", nil)
		res := httptest.NewRecorder()
		api.ServeHTTP(res, req)
		require.Equal(t, http.StatusNotFound, res.Code, "unexpected status code")
	})
}

func TestFetchStargazers(t *testing.T) {
	t.Parallel()

	t.Run("Successful", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		kv := kv.NewMemory()
		api := starquery.New(ctx, starquery.Options{
			Client: &http.Client{
				Transport: roundTripper(func(req *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body: io.NopCloser(bytes.NewBufferString(`{
								"data": {
									"repository": {
										"stargazers": {
											"edges": [
												{"node": {"login": "user1"}, "cursor": "cursor1"},
												{"node": {"login": "user2"}, "cursor": "cursor2"}
											]
										}
									},
										"rateLimit": {
										"remaining": 50,
										"resetAt": "2023-04-01T00:00:00Z"
									}
								}
							}`)),
					}, nil
				}),
			},
			KV:    kv,
			Repos: []starquery.Repo{{Owner: "coder", Name: "coder"}},
		})
		defer api.Close()

		require.Eventually(t, func() bool {
			v, err := kv.Get(ctx, "stargazers:coder/coder/user1")
			if !assert.NoError(t, err) {
				return false
			}
			if !assert.NotEmpty(t, v) {
				return false
			}
			v, err = kv.Get(ctx, "stargazers:coder/coder/user2")
			if !assert.NoError(t, err) {
				return false
			}
			if !assert.NotEmpty(t, v) {
				return false
			}
			return true
		}, time.Second, time.Millisecond)
	})
}

func generateEvent(repo starquery.Repo, username string, action string) github.StarEvent {
	return github.StarEvent{
		Action: &action,
		Repo: &github.Repository{
			Name: &repo.Name,
			Owner: &github.User{
				Login: &repo.Owner,
			},
		},
		Sender: &github.User{
			Login: &username,
		},
	}
}

func generateWebhook(t *testing.T, secret string, payload github.StarEvent) *http.Request {
	data, err := json.Marshal(payload)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(data))
	req.Header.Set("X-GitHub-Event", "star")

	// generate sha256
	hash := hmac.New(sha256.New, []byte(secret))
	hash.Write(data)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(hash.Sum(nil)))
	return req
}

type roundTripper func(req *http.Request) (*http.Response, error)

func (rt roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return rt(req)
}
