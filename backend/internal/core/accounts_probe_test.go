// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/imap"
	"github.com/schotek/malachi/backend/internal/smtp"
	"github.com/schotek/malachi/backend/pkg/api"
)

func TestAccountTest(t *testing.T) {
	ctx := context.Background()

	t.Run("both ok", func(t *testing.T) {
		b := newTestBackend(t, config.Default())
		b.ProbeIMAP = func(context.Context, api.ServerConfig, string) (imap.ProbeResult, error) {
			return imap.ProbeResult{Capabilities: []string{"IDLE"}, Latency: 12 * time.Millisecond}, nil
		}
		b.ProbeSMTP = func(context.Context, api.ServerConfig, string) (smtp.ProbeResult, error) {
			return smtp.ProbeResult{Capabilities: []string{"AUTH PLAIN"}, Latency: 3 * time.Millisecond}, nil
		}
		res, err := b.Accounts().Test(ctx, api.AccountTestParams{Config: validConfig(), Credentials: api.Credentials{Password: "x"}})
		if err != nil {
			t.Fatal(err)
		}
		if !res.IMAP.OK || res.IMAP.LatencyMS != 12 || res.IMAP.Capabilities[0] != "IDLE" || !res.SMTP.OK || res.SMTP.LatencyMS != 3 {
			t.Fatalf("res = %+v", res)
		}
	})

	t.Run("one endpoint fails", func(t *testing.T) {
		b := newTestBackend(t, config.Default())
		b.ProbeIMAP = func(context.Context, api.ServerConfig, string) (imap.ProbeResult, error) {
			return imap.ProbeResult{}, api.NewError(api.CodeAuthFailed, "no")
		}
		b.ProbeSMTP = func(context.Context, api.ServerConfig, string) (smtp.ProbeResult, error) {
			return smtp.ProbeResult{}, errors.New("plain failure")
		}
		res, err := b.Accounts().Test(ctx, api.AccountTestParams{Config: validConfig()})
		if err != nil {
			t.Fatal(err)
		}
		if res.IMAP.OK || res.IMAP.Error == nil || res.IMAP.Error.Code != api.CodeAuthFailed {
			t.Fatalf("imap = %+v", res.IMAP)
		}
		if res.SMTP.OK || res.SMTP.Error == nil || res.SMTP.Error.Code != api.CodeServerError {
			t.Fatalf("smtp = %+v", res.SMTP)
		}
	})

	t.Run("oauth2 endpoint not implemented", func(t *testing.T) {
		b := newTestBackend(t, config.Default())
		c := validConfig()
		c.IMAP.AuthMethod = api.AuthOAuth2
		c.OAuth2 = &api.OAuth2Config{Provider: "office365"}
		b.ProbeIMAP = imap.Probe // real one refuses oauth2 before touching the network
		b.ProbeSMTP = func(context.Context, api.ServerConfig, string) (smtp.ProbeResult, error) {
			return smtp.ProbeResult{}, nil
		}
		res, err := b.Accounts().Test(ctx, api.AccountTestParams{Config: c})
		if err != nil {
			t.Fatal(err)
		}
		if res.IMAP.Error == nil || res.IMAP.Error.Code != api.CodeNotImplemented || !res.SMTP.OK {
			t.Fatalf("res = %+v", res)
		}
	})

	t.Run("invalid config", func(t *testing.T) {
		b := newTestBackend(t, config.Default())
		c := validConfig()
		c.IMAP.Port = 0
		if _, err := b.Accounts().Test(ctx, api.AccountTestParams{Config: c}); errCode(t, err) != api.CodeInvalidArgument {
			t.Fatalf("err = %v", err)
		}
		c = validConfig()
		c.IMAP.AuthMethod, c.SMTP.AuthMethod = api.AuthOAuth2, api.AuthOAuth2
		c.OAuth2 = &api.OAuth2Config{Provider: "office365"}
		if _, err := b.Accounts().Test(ctx, api.AccountTestParams{Config: c, Credentials: api.Credentials{Password: "x"}}); errCode(t, err) != api.CodeInvalidArgument {
			t.Fatalf("password for oauth2: %v", err)
		}
	})

	t.Run("probes run concurrently", func(t *testing.T) {
		b := newTestBackend(t, config.Default())
		started := make(chan struct{}, 2)
		release := make(chan struct{})
		b.ProbeIMAP = func(context.Context, api.ServerConfig, string) (imap.ProbeResult, error) {
			started <- struct{}{}
			<-release
			return imap.ProbeResult{}, nil
		}
		b.ProbeSMTP = func(context.Context, api.ServerConfig, string) (smtp.ProbeResult, error) {
			started <- struct{}{}
			<-release
			return smtp.ProbeResult{}, nil
		}
		done := make(chan *api.AccountTestResult)
		go func() {
			res, _ := b.Accounts().Test(ctx, api.AccountTestParams{Config: validConfig()})
			done <- res
		}()
		for i := 0; i < 2; i++ {
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("probes did not start concurrently")
			}
		}
		close(release)
		if res := <-done; !res.IMAP.OK || !res.SMTP.OK {
			t.Fatalf("res = %+v", res)
		}
	})
}
