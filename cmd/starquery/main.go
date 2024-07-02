package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/coder/starquery"
	"github.com/coder/starquery/kv"
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
	err := http.ListenAndServe(":8085", starquery.New(ctx, starquery.Options{
		Client:        http.DefaultClient,
		KV:            kv.NewMemory(),
		Logger:        logger,
		Repos:         []starquery.Repo{{"coder", "coder"}},
		WebhookSecret: "potato",
	}))
	return err
}
