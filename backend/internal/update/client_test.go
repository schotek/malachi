// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package update_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/update"
	"github.com/schotek/malachi/backend/internal/update/updatetest"
)

const payload = `{"schema":1,"issuedAt":"2026-10-28T12:00:00Z","releases":[]}`

func TestClient(t *testing.T) {
	s := updatetest.New(t)
	c := s.Client()
	ctx := context.Background()

	// Nothing published: 503 with Retry-After.
	_, err := c.FetchPrimary(ctx, "")
	var fe *update.FetchError
	if !errors.As(err, &fe) || fe.Class != update.FailServer || fe.Status != http.StatusServiceUnavailable || fe.RetryAfter != 5*time.Minute {
		t.Fatalf("nothing published: %v", err)
	}

	s.Publish([]byte(payload))
	r, err := c.FetchPrimary(ctx, "")
	if err != nil || r.NotModified || r.ETag == "" {
		t.Fatalf("first: %+v %v", r, err)
	}
	if p, err := update.Verify(r.Envelope, s.Keys()); err != nil || string(p) != payload {
		t.Fatalf("verify: %q %v", p, err)
	}
	r2, err := c.FetchPrimary(ctx, r.ETag)
	if err != nil || !r2.NotModified {
		t.Fatalf("If-None-Match: %+v %v", r2, err)
	}
	if ua := s.UserAgents(); ua[len(ua)-1] != "MalachiMail/0.0.0-test" {
		t.Errorf("User-Agent %q", ua)
	}

	s.Fail(http.StatusTooManyRequests, "86400000")
	if _, err := c.FetchPrimary(ctx, ""); !errors.As(err, &fe) || fe.RetryAfter != update.MaxRetryAfter {
		t.Errorf("429: %v", err)
	}
	s.FailAPI(http.StatusInternalServerError)
	if _, err := c.FetchPrimary(ctx, ""); !errors.As(err, &fe) || fe.Class != update.FailServer {
		t.Errorf("500: %v", err)
	}
	r, err = c.FetchFallback(ctx)
	if err != nil || r.ETag != "" {
		t.Fatalf("fallback: %+v %v", r, err)
	}
	if _, err := update.Verify(r.Envelope, s.Keys()); err != nil {
		t.Fatal(err)
	}
}

func TestClientRefusesARedirectAndNonJSON(t *testing.T) {
	s := updatetest.New(t)
	c := s.Client()
	mux := http.NewServeMux()
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/api/v1/updates", http.StatusFound)
	})
	mux.HandleFunc("/html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>sign in</html>"))
	})
	s.Config.Handler = mux
	var fe *update.FetchError
	c.PrimaryURL = s.URL + "/redirect"
	if _, err := c.FetchPrimary(context.Background(), ""); !errors.As(err, &fe) || fe.Status != http.StatusFound {
		t.Errorf("redirect: %v", err)
	}
	c.PrimaryURL = s.URL + "/html"
	if _, err := c.FetchPrimary(context.Background(), ""); !errors.As(err, &fe) || fe.Class != update.FailServer {
		t.Errorf("html: %v", err)
	}
	c.FallbackURL = s.URL + "/redirect"
	c.FallbackHosts = []string{"github.com"}
	if _, err := c.FetchFallback(context.Background()); !errors.As(err, &fe) || fe.Class != update.FailNetwork {
		t.Errorf("fallback redirect off its hosts: %v", err)
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	for h, want := range map[string]time.Duration{
		"": 0, "x": 0, "-5": 0, "0": 0, "120": 2 * time.Minute, "999999999999": update.MaxRetryAfter,
		"Mon, 02 Nov 2026 08:10:00 GMT": 10 * time.Minute, "Mon, 02 Nov 2026 07:00:00 GMT": 0,
	} {
		if got := update.RetryAfter(h, now); got != want {
			t.Errorf("RetryAfter(%q) = %v, want %v", h, got, want)
		}
	}
}
