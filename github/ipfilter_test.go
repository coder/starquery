package github_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/coder/starquery/github"
	xgithub "github.com/google/go-github/v52/github"
)

func TestIPFilter(t *testing.T) {
	t.Parallel()
	f := github.NewIPFilter(context.Background(), &github.IPFilterOptions{
		Client: xgithub.NewClient(&http.Client{
			Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
				meta := &xgithub.APIMeta{
					Hooks: []string{
						"192.30.252.0/22",
						"185.199.108.0/22",
						"140.82.112.0/20",
						"143.55.64.0/20",
						"2a0a:a440::/29",
						"2606:50c0::/32",
					},
				}
				data, _ := json.Marshal(meta)
				return &http.Response{
					Body:       io.NopCloser(bytes.NewReader(data)),
					StatusCode: http.StatusOK,
				}, nil
			}),
		}),
	})
	ensure(t, f, "192.30.252.1", true)
	ensure(t, f, "1.1.1.1", false)
	ensure(t, f, "oskasdfds", false)
	ensure(t, f, "", false)
}

type roundTripper func(*http.Request) (*http.Response, error)

func (r roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return r(req)
}

func ensure(t *testing.T, f *github.IPFilter, addr string, valid bool) {
	req := &http.Request{
		RemoteAddr: addr,
	}
	if f.Valid(req) == valid {
		return
	}
	if valid {
		t.Fatalf("%s is not in the subnet, but was supoosed to be", addr)
	} else {
		t.Fatalf("%s is in the subnet, but wasn't supposed to be", addr)
	}
}
