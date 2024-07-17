package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"

	"github.com/coder/starquery"
	"github.com/coder/starquery/kv"
	"golang.org/x/oauth2"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	err := run(context.Background(), logger)
	if err != nil {
		logger.Error("run", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	bindAddress, ok := os.LookupEnv("BIND_ADDRESS")
	if !ok {
		bindAddress = "127.0.0.1:8080"
	}
	githubToken, ok := os.LookupEnv("GITHUB_TOKEN")
	if !ok {
		logger.Warn("missing GITHUB_TOKEN, unauthenticated requests will be rate-limited")
	}

	redisURL, ok := os.LookupEnv("REDIS_URL")
	var store kv.Store
	if !ok {
		logger.Warn("missing REDIS_URL, using in-memory store")
		store = kv.NewMemory()
	} else {
		store = kv.NewRedis(redisURL)
	}

	webhookSecret, ok := os.LookupEnv("WEBHOOK_SECRET")
	if !ok {
		return errors.New("missing WEBHOOK_SECRET")
	}

	err := http.ListenAndServe(bindAddress, starquery.New(ctx, starquery.Options{
		Client: oauth2.NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: githubToken})),
		KV:     store,
		Logger: logger,
		Repos: []starquery.Repo{{
			Owner: "coder",
			Name:  "coder",
		}},
		WebhookSecret: webhookSecret,
	}))
	return err
}
