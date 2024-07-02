package github

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/retry"
	"github.com/google/go-github/v52/github"
)

type IPFilterOptions struct {
	Client          *github.Client
	Logger          *slog.Logger
	RefreshInterval time.Duration
}

// IPFilter creates a filter that only allows requests from GitHub hooks.
// It periodically checks GitHub for the latest set of subnets.
func NewIPFilter(ctx context.Context, options *IPFilterOptions) *IPFilter {
	if options == nil {
		options = &IPFilterOptions{}
	}
	if options.Client == nil {
		options.Client = github.NewClient(nil)
	}
	if options.Logger == nil {
		options.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if options.RefreshInterval == 0 {
		options.RefreshInterval = time.Hour
	}
	ctx, cancel := context.WithCancel(ctx)
	f := &IPFilter{
		IPFilterOptions: options,
		closeCancel:     cancel,
		nets:            make([]*net.IPNet, 0),
	}
	f.refresh(ctx)
	f.wg.Add(1)
	go f.run(ctx)
	return f
}

type IPFilter struct {
	*IPFilterOptions

	closeCancel context.CancelFunc
	wg          sync.WaitGroup
	mu          sync.RWMutex
	nets        []*net.IPNet
}

// Valid returns true if the request is from a GitHub hook.
func (f *IPFilter) Valid(req *http.Request) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()

	ip := net.ParseIP(req.RemoteAddr)
	if ip == nil {
		return false
	}
	for _, net := range f.nets {
		if net.Contains(ip) {
			return true
		}
	}
	return false
}

func (f *IPFilter) run(ctx context.Context) {
	defer f.wg.Done()
	ticker := time.NewTicker(f.RefreshInterval)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		f.refresh(ctx)
	}
}

// refresh fetches from GitHub for the latest subnets.
func (f *IPFilter) refresh(ctx context.Context) {
	r := retry.New(time.Second, time.Second*10)
retry:
	meta, _, err := f.Client.APIMeta(ctx)
	if err != nil {
		f.Logger.Error("get API meta", "err", err)
		if !r.Wait(ctx) {
			return
		}
		goto retry
	}
	nets := make([]*net.IPNet, 0, len(meta.Hooks))
	for _, hook := range meta.Hooks {
		_, net, err := net.ParseCIDR(hook)
		if err != nil {
			f.Logger.Error("parse CIDR", "hook", hook, "err", err)
			continue
		}
		nets = append(nets, net)
	}
	f.mu.Lock()
	f.nets = nets
	f.mu.Unlock()
}

func (f *IPFilter) Close() {
	f.closeCancel()
	f.wg.Wait()
}
