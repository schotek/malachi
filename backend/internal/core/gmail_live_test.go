// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/auth/goa"
	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/imap"
	"github.com/schotek/malachi/backend/internal/smtp"
	"github.com/schotek/malachi/backend/pkg/api"
)

// TestGmailLive signs in to the real Gmail servers with the token of the
// first Google account in GNOME Online Accounts, through account.test:
// IMAP and SMTP are probed (CAPABILITY, EHLO and AUTH), nothing is read
// or sent. Opt-in: MALACHI_TEST_GMAIL=1 go test -run GmailLive -v. The
// token and the address are never printed.
func TestGmailLive(t *testing.T) {
	if os.Getenv("MALACHI_TEST_GMAIL") != "1" {
		t.Skip("set MALACHI_TEST_GMAIL=1 with a session bus, a Google account in GNOME Online Accounts and network access to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	b := newTestBackend(t, config.Default())
	b.GOA = goa.New(nil)
	b.ProbeIMAP, b.ProbeSMTP = imap.Probe, smtp.Probe

	accounts, err := b.GOA.Accounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var cfg *api.AccountConfig
	for _, a := range accounts {
		if a.ProviderType != goa.ProviderGoogle {
			continue
		}
		if c, _, ok := goaConfigFor(a); ok {
			cfg = c
			break
		}
	}
	if cfg == nil {
		t.Skip("no usable Google account in GNOME Online Accounts")
	}
	if err := validateAccountConfig(cfg); err != nil {
		t.Fatalf("config from GOA does not validate: %v", err)
	}
	t.Logf("imap %s:%d %s, smtp %s:%d %s", cfg.IMAP.Host, cfg.IMAP.Port, cfg.IMAP.Security, cfg.SMTP.Host, cfg.SMTP.Port, cfg.SMTP.Security)

	res, err := b.Accounts().Test(ctx, api.AccountTestParams{Config: *cfg})
	if err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]*api.EndpointTestResult{"imap": res.IMAP, "smtp": res.SMTP} {
		if r == nil || !r.OK {
			t.Errorf("%s: %+v", name, r)
			continue
		}
		t.Logf("%s ok in %d ms, %d capabilities (%s…)", name, r.LatencyMS, len(r.Capabilities), strings.Join(r.Capabilities[:min(6, len(r.Capabilities))], " "))
	}
}
