// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/imap"
	"github.com/schotek/malachi/backend/internal/smtp"
	"github.com/schotek/malachi/backend/internal/transport/transporttest"
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

	t.Run("oauth2 without source not implemented", func(t *testing.T) {
		b := newTestBackend(t, config.Default())
		c := validConfig()
		c.IMAP.AuthMethod = api.AuthOAuth2
		c.OAuth2 = &api.OAuth2Config{Provider: "office365"}
		// The reserved flow without a source has no token source: nothing
		// is dialled, both endpoints say so. (Source daemon: see
		// oauth_sessions_test.go.)
		probed := false
		b.ProbeIMAP = func(context.Context, api.ServerConfig, string) (imap.ProbeResult, error) {
			probed = true
			return imap.ProbeResult{}, nil
		}
		b.ProbeSMTP = func(context.Context, api.ServerConfig, string) (smtp.ProbeResult, error) {
			probed = true
			return smtp.ProbeResult{}, nil
		}
		res, err := b.Accounts().Test(ctx, api.AccountTestParams{Config: c})
		if err != nil {
			t.Fatal(err)
		}
		if res.IMAP.Error == nil || res.IMAP.Error.Code != api.CodeNotImplemented ||
			res.SMTP.Error == nil || res.SMTP.Error.Code != api.CodeNotImplemented || probed {
			t.Fatalf("res = %+v probed %v", res, probed)
		}
	})

	// The real probes against a server whose certificate is not the
	// pinned one: both endpoints say so with the details, and the details
	// survive the wire (a client decodes them from a map).
	t.Run("tls details", func(t *testing.T) {
		b := newTestBackend(t, config.Default())
		srvTLS, cert := transporttest.SelfSigned(t)
		ln, err := tls.Listen("tcp", "127.0.0.1:0", srvTLS)
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				go func() { c.(*tls.Conn).Handshake(); c.Close() }()
			}
		}()
		port := ln.Addr().(*net.TCPAddr).Port
		wrong := strings.Repeat("ab", 32)
		c := validConfig()
		for _, sc := range []*api.ServerConfig{c.IMAP, c.SMTP} {
			sc.Host, sc.Port, sc.Security, sc.CertificateSHA256 = "127.0.0.1", port, api.SecurityTLS, strings.ToUpper(wrong)
		}
		res, err := b.Accounts().Test(ctx, api.AccountTestParams{Config: c, Credentials: api.Credentials{Password: "x"}})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(res)
		if err != nil {
			t.Fatal(err)
		}
		var wire api.AccountTestResult
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}
		for name, r := range map[string]*api.EndpointTestResult{"imap": res.IMAP, "smtp": res.SMTP, "imap wire": wire.IMAP, "smtp wire": wire.SMTP} {
			d, ok := api.TLSErrorDataOf(r.Error)
			if r.OK || !ok || d.Reason != api.TLSPinMismatch || d.ExpectedSHA256 != wrong ||
				d.Certificate == nil || d.Certificate.SHA256 != transporttest.Fingerprint(cert) || d.Certificate.Subject != "localhost" {
				t.Fatalf("%s: %+v data %+v", name, r, r.Error)
			}
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
