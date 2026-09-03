// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
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
