// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/discover"
	"github.com/schotek/malachi/backend/pkg/api"
)

func TestAccountDiscover(t *testing.T) {
	ctx := context.Background()

	t.Run("bad email", func(t *testing.T) {
		b := newTestBackend(t, config.Default())
		if _, err := b.Accounts().Discover(ctx, api.AccountDiscoverParams{Email: "nope"}); errCode(t, err) != api.CodeInvalidArgument {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("suggestion echoed", func(t *testing.T) {
		b := newTestBackend(t, config.Default())
		var got string
		b.Discover = func(_ context.Context, email string) (discover.Result, error) {
			got = email
			cfg := validConfig()
			return discover.Result{Config: &cfg, Source: api.DiscoverSRV, ProviderName: "Example"}, nil
		}
		res, err := b.Accounts().Discover(ctx, api.AccountDiscoverParams{Email: "  me@example.invalid "})
		if err != nil {
			t.Fatal(err)
		}
		if got != "me@example.invalid" || res.Source != api.DiscoverSRV || res.Config == nil || res.Config.IMAP.Host != "imap.example.invalid" || res.ProviderName != "Example" {
			t.Fatalf("res = %+v", res)
		}
	})

	t.Run("invalid suggestion dropped", func(t *testing.T) {
		b := newTestBackend(t, config.Default())
		b.Discover = func(context.Context, string) (discover.Result, error) {
			cfg := validConfig()
			cfg.IMAP.Security = api.SecurityNone // not loopback → invalid
			return discover.Result{Config: &cfg, Source: api.DiscoverGuess}, nil
		}
		res, err := b.Accounts().Discover(ctx, api.AccountDiscoverParams{Email: "me@example.invalid"})
		if err != nil || res.Config != nil || res.Source != api.DiscoverNone {
			t.Fatalf("res = %+v err = %v", res, err)
		}
	})

	t.Run("nothing found", func(t *testing.T) {
		b := newTestBackend(t, config.Default())
		b.Discover = func(context.Context, string) (discover.Result, error) {
			return discover.Result{Source: api.DiscoverNone}, nil
		}
		res, err := b.Accounts().Discover(ctx, api.AccountDiscoverParams{Email: "me@example.invalid"})
		if err != nil || res.Config != nil || res.Source != api.DiscoverNone {
			t.Fatalf("res = %+v err = %v", res, err)
		}
	})
}

// noDNS answers every lookup with "no such host".
type noDNS struct{}

func (noDNS) LookupSRV(_ context.Context, _, _, name string) (string, []*net.SRV, error) {
	return "", nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}
func (noDNS) LookupMX(_ context.Context, name string) ([]*net.MX, error) {
	return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

// A Gmail address not signed in to GNOME Online Accounts: the GOA hint
// where GOA runs, else the own sign-in; the app password last. Whether an
// OAuth client is configured does not change the answer.
func TestAccountDiscoverProviderAlternatives(t *testing.T) {
	ctx := context.Background()
	ispdb := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(ispdb.Close)
	for _, clientID := range []string{"", testGoogleClient} {
		cfg := config.Default()
		cfg.OAuth2.Google.ClientID = clientID
		b := newTestBackend(t, cfg)
		d := discover.New(nil)
		d.HTTP, d.ISPDBBase, d.Provider, d.Resolver = ispdb.Client(), ispdb.URL+"/", false, noDNS{}
		d.GOA, d.GOAAvailable = b.goaAccountFor, b.goaAvailable
		b.Discover = d.Discover

		b.GOA = &fakeGOA{}
		res, err := b.Accounts().Discover(ctx, api.AccountDiscoverParams{Email: "me@gmail.com"})
		if err != nil || res.Source != api.DiscoverProvider || res.ProviderName != discover.GoogleProviderName ||
			res.Config == nil || res.Config.OAuth2 == nil || res.Config.OAuth2.Source != api.OAuth2SourceGOA {
			t.Fatalf("client %q, with GOA: %+v, %v", clientID, res, err)
		}
		if len(res.Alternatives) != 2 || res.Alternatives[0].OAuth2 == nil || res.Alternatives[0].OAuth2.Source != api.OAuth2SourceDaemon ||
			res.Alternatives[1].OAuth2 != nil || res.Alternatives[1].IMAP.AuthMethod != api.AuthPassword {
			t.Fatalf("client %q, with GOA: alternatives %+v", clientID, res.Alternatives)
		}
		for _, alt := range res.Alternatives {
			if err := validateAccountConfig(&alt); err != nil {
				t.Fatalf("alternative does not pass add: %v", err)
			}
		}

		b.GOA = &fakeGOA{listErr: api.NewError(api.CodeUnavailable, "no bus")}
		res, err = b.Accounts().Discover(ctx, api.AccountDiscoverParams{Email: "me@gmail.com"})
		if err != nil || res.Source != api.DiscoverProvider || res.Config == nil || res.Config.OAuth2 == nil ||
			res.Config.OAuth2.Source != api.OAuth2SourceDaemon || validateAccountConfig(res.Config) != nil {
			t.Fatalf("client %q, without GOA: %+v, %v", clientID, res, err)
		}
		if len(res.Alternatives) != 1 || res.Alternatives[0].IMAP.AuthMethod != api.AuthPassword {
			t.Fatalf("client %q, without GOA: alternatives %+v", clientID, res.Alternatives)
		}
	}
}

func TestAccountDiscoverDropsInvalidAlternatives(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	bad := validConfig()
	bad.IMAP.Port = 0
	good := validConfig()
	good.Name = "  Trimmed  "
	b.Discover = func(_ context.Context, email string) (discover.Result, error) {
		cfg := validConfig()
		return discover.Result{Config: &cfg, Source: api.DiscoverProvider, Alternatives: []api.AccountConfig{bad, good}}, nil
	}
	res, err := b.Accounts().Discover(ctx, api.AccountDiscoverParams{Email: "me@example.invalid"})
	if err != nil || len(res.Alternatives) != 1 || res.Alternatives[0].Name != "Trimmed" {
		t.Fatalf("res = %+v, %v", res, err)
	}
	// No answer, no alternatives.
	b.Discover = func(context.Context, string) (discover.Result, error) {
		return discover.Result{Source: api.DiscoverNone, Alternatives: []api.AccountConfig{good}}, nil
	}
	res, err = b.Accounts().Discover(ctx, api.AccountDiscoverParams{Email: "me@example.invalid"})
	if err != nil || res.Config != nil || len(res.Alternatives) != 0 {
		t.Fatalf("none = %+v, %v", res, err)
	}
}
