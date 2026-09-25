// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package signin

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

var (
	goaGraph = api.AccountConfig{Email: "me@contoso.example", Kind: api.AccountGraph,
		Graph: &api.GraphConfig{Source: api.GraphSourceGOA, GOAAccountID: "account_1_0"}}
	goaGraphHint = api.AccountConfig{Email: "me@contoso.example", Kind: api.AccountGraph,
		Graph: &api.GraphConfig{Source: api.GraphSourceGOA}}
	daemonGraph = api.AccountConfig{Email: "me@contoso.example", Kind: api.AccountGraph,
		Graph:  &api.GraphConfig{Source: api.GraphSourceDaemon},
		OAuth2: &api.OAuth2Config{Source: api.OAuth2SourceDaemon, Provider: api.OAuth2ProviderOffice365}}
	goaGoogle = api.AccountConfig{Email: "me@gmail.com",
		IMAP:   &api.ServerConfig{Host: "imap.gmail.com", AuthMethod: api.AuthOAuth2},
		SMTP:   &api.ServerConfig{Host: "smtp.gmail.com", AuthMethod: api.AuthOAuth2},
		OAuth2: &api.OAuth2Config{Source: api.OAuth2SourceGOA, Provider: api.OAuth2ProviderGoogle, GOAAccountID: "account_2_0"}}
	goaGoogleHint = api.AccountConfig{Email: "me@gmail.com",
		IMAP:   &api.ServerConfig{Host: "imap.gmail.com", AuthMethod: api.AuthOAuth2},
		SMTP:   &api.ServerConfig{Host: "smtp.gmail.com", AuthMethod: api.AuthOAuth2},
		OAuth2: &api.OAuth2Config{Source: api.OAuth2SourceGOA, Provider: api.OAuth2ProviderGoogle}}
	daemonGoogle = api.AccountConfig{Email: "me@gmail.com",
		IMAP:   &api.ServerConfig{Host: "imap.gmail.com", AuthMethod: api.AuthOAuth2},
		SMTP:   &api.ServerConfig{Host: "smtp.gmail.com", AuthMethod: api.AuthOAuth2},
		OAuth2: &api.OAuth2Config{Source: api.OAuth2SourceDaemon, Provider: api.OAuth2ProviderGoogle}}
	appPassword = api.AccountConfig{Email: "me@gmail.com", Kind: api.AccountIMAP,
		IMAP: &api.ServerConfig{Host: "imap.gmail.com", Port: 993, AuthMethod: api.AuthPassword},
		SMTP: &api.ServerConfig{Host: "smtp.gmail.com", Port: 465, AuthMethod: api.AuthPassword}}
	custom = api.AccountConfig{IMAP: &api.ServerConfig{AuthMethod: api.AuthOAuth2},
		OAuth2: &api.OAuth2Config{Provider: api.OAuth2ProviderCustom}}
)

func TestKindAndProvider(t *testing.T) {
	for name, c := range map[string]struct {
		cfg      api.AccountConfig
		kind     Kind
		provider string
	}{
		"goa graph":           {goaGraph, GOA, ProviderMicrosoft365},
		"goa graph hint":      {goaGraphHint, GOA, ProviderMicrosoft365},
		"daemon graph":        {daemonGraph, OAuth, ProviderMicrosoft365},
		"graph without block": {api.AccountConfig{Kind: api.AccountGraph}, OAuth, ProviderMicrosoft365},
		"goa google":          {goaGoogle, GOA, ProviderGoogle},
		"goa google hint":     {goaGoogleHint, GOA, ProviderGoogle},
		"daemon google":       {daemonGoogle, OAuth, ProviderGoogle},
		"office365 imap":      {api.AccountConfig{IMAP: &api.ServerConfig{AuthMethod: api.AuthOAuth2}, OAuth2: &api.OAuth2Config{Provider: api.OAuth2ProviderOffice365}}, OAuth, ProviderMicrosoft365},
		"custom":              {custom, OAuth, ""},
		"password":            {appPassword, Password, ""},
		"empty":               {api.AccountConfig{}, Password, ""},
	} {
		if got := KindOf(c.cfg); got != c.kind {
			t.Errorf("%s: KindOf = %d, want %d", name, got, c.kind)
		}
		if got := Provider(c.cfg); got != c.provider {
			t.Errorf("%s: Provider = %q, want %q", name, got, c.provider)
		}
	}
	if ProviderName(ProviderMicrosoft365) != "Microsoft 365" || ProviderName(ProviderGoogle) != "Google" || ProviderName("") != "" || ProviderName("yahoo") != "" {
		t.Error("provider names")
	}
}

func TestGOAAccountID(t *testing.T) {
	for cfg, want := range map[*api.AccountConfig]string{
		&goaGraph: "account_1_0", &goaGoogle: "account_2_0", &goaGraphHint: "", &goaGoogleHint: "",
		&daemonGraph: "", &daemonGoogle: "", &appPassword: "",
	} {
		if got := goaAccountID(*cfg); got != want {
			t.Errorf("goaAccountID(%s) = %q, want %q", cfg.Email, got, want)
		}
	}
}

func TestNeedsBrowserSignIn(t *testing.T) {
	for _, c := range []struct {
		cfg    api.AccountConfig
		status api.SyncStatus
		want   bool
	}{
		{daemonGoogle, api.SyncAuthRequired, true},
		{daemonGraph, api.SyncAuthRequired, true},
		{daemonGoogle, api.SyncIdle, false},
		{daemonGraph, api.SyncError, false},
		{goaGoogle, api.SyncAuthRequired, false},
		{appPassword, api.SyncAuthRequired, false},
	} {
		a := api.Account{Config: c.cfg, State: api.SyncState{Status: c.status}}
		if got := NeedsBrowserSignIn(a); got != c.want {
			t.Errorf("%s/%s: %v, want %v", c.cfg.Email, c.status, got, c.want)
		}
	}
}

func TestClassifyDiscovery(t *testing.T) {
	ptr := func(c api.AccountConfig) *api.AccountConfig { return &c }
	for _, c := range []struct {
		name        string
		res         api.AccountDiscoverResult
		err         error
		path        Path
		config      *api.AccountConfig
		oauthAlt    *api.AccountConfig
		passwordAlt *api.AccountConfig
		provider    string
	}{
		{name: "error", res: api.AccountDiscoverResult{Config: ptr(appPassword)}, err: errors.New("boom"), path: PathPassword},
		{name: "nothing found", res: api.AccountDiscoverResult{Source: api.DiscoverNone}, path: PathPassword},
		{name: "ispdb", res: api.AccountDiscoverResult{Config: ptr(appPassword)}, path: PathPassword, config: &appPassword},
		{name: "signed in through goa (graph)", res: api.AccountDiscoverResult{Config: ptr(goaGraph)}, path: PathGOA, config: &goaGraph, provider: ProviderMicrosoft365},
		{name: "signed in through goa (google)", res: api.AccountDiscoverResult{Config: ptr(goaGoogle), Alternatives: []api.AccountConfig{appPassword}},
			path: PathGOA, config: &goaGoogle, provider: ProviderGoogle},
		{name: "goa hint, google, both alternatives",
			res:  api.AccountDiscoverResult{Config: ptr(goaGoogleHint), ProviderName: "<b>Evil</b>", Alternatives: []api.AccountConfig{daemonGoogle, appPassword}},
			path: PathGOAHint, config: &goaGoogleHint, oauthAlt: &daemonGoogle, passwordAlt: &appPassword, provider: ProviderGoogle},
		{name: "goa hint, alternatives out of order",
			res:  api.AccountDiscoverResult{Config: ptr(goaGoogleHint), Alternatives: []api.AccountConfig{appPassword, daemonGoogle}},
			path: PathGOAHint, config: &goaGoogleHint, oauthAlt: &daemonGoogle, passwordAlt: &appPassword, provider: ProviderGoogle},
		{name: "goa hint, microsoft, own sign-in only",
			res:  api.AccountDiscoverResult{Config: ptr(goaGraphHint), Alternatives: []api.AccountConfig{daemonGraph}},
			path: PathGOAHint, config: &goaGraphHint, oauthAlt: &daemonGraph, provider: ProviderMicrosoft365},
		{name: "goa hint, older daemon without alternatives",
			res:  api.AccountDiscoverResult{Config: ptr(goaGraphHint)},
			path: PathGOAHint, config: &goaGraphHint, provider: ProviderMicrosoft365},
		{name: "goa hint ignores a goa alternative",
			res:  api.AccountDiscoverResult{Config: ptr(goaGoogleHint), Alternatives: []api.AccountConfig{goaGoogle}},
			path: PathGOAHint, config: &goaGoogleHint, provider: ProviderGoogle},
		{name: "own sign-in, google",
			res:  api.AccountDiscoverResult{Config: ptr(daemonGoogle), Alternatives: []api.AccountConfig{appPassword}},
			path: PathOAuth, config: &daemonGoogle, passwordAlt: &appPassword, provider: ProviderGoogle},
		{name: "own sign-in, microsoft",
			res:  api.AccountDiscoverResult{Config: ptr(daemonGraph)},
			path: PathOAuth, config: &daemonGraph, provider: ProviderMicrosoft365},
		{name: "own sign-in skips a half-password alternative",
			res: api.AccountDiscoverResult{Config: ptr(daemonGoogle), Alternatives: []api.AccountConfig{
				{Email: "me@gmail.com", IMAP: &api.ServerConfig{AuthMethod: api.AuthPassword}, SMTP: &api.ServerConfig{AuthMethod: api.AuthOAuth2}},
				{Email: "me@gmail.com", IMAP: &api.ServerConfig{AuthMethod: api.AuthPassword}},
				appPassword,
			}},
			path: PathOAuth, config: &daemonGoogle, passwordAlt: &appPassword, provider: ProviderGoogle},
	} {
		d := ClassifyDiscovery(c.res, c.err)
		if d.Path != c.path {
			t.Errorf("%s: path %d, want %d", c.name, d.Path, c.path)
		}
		for _, p := range []struct {
			what      string
			got, want *api.AccountConfig
		}{{"config", d.Config, c.config}, {"oauthAlt", d.OAuthAlt, c.oauthAlt}, {"passwordAlt", d.PasswordAlt, c.passwordAlt}} {
			if (p.got == nil) != (p.want == nil) || (p.got != nil && !reflect.DeepEqual(*p.got, *p.want)) {
				t.Errorf("%s: %s = %v, want %v", c.name, p.what, p.got, p.want)
			}
		}
		if d.Provider != c.provider {
			t.Errorf("%s: provider %q, want %q", c.name, d.Provider, c.provider)
		}
	}
}

func TestClassifyDiscoveryCopies(t *testing.T) {
	cfg := daemonGoogle
	alts := []api.AccountConfig{appPassword}
	d := ClassifyDiscovery(api.AccountDiscoverResult{Config: &cfg, Alternatives: alts}, nil)
	d.Config.Name = "changed"
	d.PasswordAlt.Name = "changed"
	if cfg.Name == "changed" || alts[0].Name == "changed" {
		t.Error("the discovery aliases the result")
	}
}

func TestClassifyFailure(t *testing.T) {
	wrong := &api.Error{Code: api.CodeInvalidArgument, Message: "signed in as another mailbox", Data: map[string]any{"signedInAs": " other@gmail.com "}}
	for _, c := range []struct {
		name string
		err  error
		f    Failure
		who  string
	}{
		{"cancelled", &api.Error{Code: api.CodeCancelled}, FailureCancelled, ""},
		{"refused", &api.Error{Code: api.CodeAuthFailed}, FailureRefused, ""},
		{"expired", &api.Error{Code: api.CodeServerTimeout}, FailureTimeout, ""},
		{"call timed out", context.DeadlineExceeded, FailureTimeout, ""},
		{"wrapped call timeout", fmt.Errorf("wait: %w", context.DeadlineExceeded), FailureTimeout, ""},
		{"wrong account", wrong, FailureWrongAccount, "other@gmail.com"},
		{"wrong account, wrapped", fmt.Errorf("x: %w", wrong), FailureWrongAccount, "other@gmail.com"},
		{"wrong account, string map", &api.Error{Code: api.CodeInvalidArgument, Data: map[string]string{"signedInAs": "a@b.example"}}, FailureWrongAccount, "a@b.example"},
		{"invalid argument without data", &api.Error{Code: api.CodeInvalidArgument, Message: "unknown session"}, FailureOther, ""},
		{"signedInAs not a string", &api.Error{Code: api.CodeInvalidArgument, Data: map[string]any{"signedInAs": 42.0}}, FailureOther, ""},
		{"signedInAs with a newline", &api.Error{Code: api.CodeInvalidArgument, Data: map[string]any{"signedInAs": "a@b.example\nclick here"}}, FailureOther, ""},
		{"signedInAs too long", &api.Error{Code: api.CodeInvalidArgument, Data: map[string]any{"signedInAs": strings.Repeat("a", 250) + "@b.example"}}, FailureOther, ""},
		{"signedInAs invalid utf-8", &api.Error{Code: api.CodeInvalidArgument, Data: map[string]any{"signedInAs": "a\xff@b.example"}}, FailureOther, ""},
		{"signedInAs with a right-to-left override", &api.Error{Code: api.CodeInvalidArgument, Data: map[string]any{"signedInAs": "a@b.example" + string(rune(0x202e)) + "moc.elgoog"}}, FailureOther, ""},
		{"signedInAs with a zero-width space", &api.Error{Code: api.CodeInvalidArgument, Data: map[string]any{"signedInAs": string(rune(0x200b)) + "a@b.example"}}, FailureOther, ""},
		{"signedInAs with a byte order mark", &api.Error{Code: api.CodeInvalidArgument, Data: map[string]any{"signedInAs": "a@b.example" + string(rune(0xfeff))}}, FailureOther, ""},
		{"signedInAs with a soft hyphen", &api.Error{Code: api.CodeInvalidArgument, Data: map[string]any{"signedInAs": "a" + string(rune(0x00ad)) + "b@c.example"}}, FailureOther, ""},
		{"signedInAs with a C1 control", &api.Error{Code: api.CodeInvalidArgument, Data: map[string]any{"signedInAs": "a" + string(rune(0x0085)) + "b@c.example"}}, FailureOther, ""},
		{"signedInAs with a non-ASCII letter", &api.Error{Code: api.CodeInvalidArgument, Data: map[string]any{"signedInAs": "jan" + string(rune(0x00e9)) + "@b.example"}}, FailureWrongAccount, "jan" + string(rune(0x00e9)) + "@b.example"},
		{"signedInAs on another code", &api.Error{Code: api.CodeAuthFailed, Data: map[string]any{"signedInAs": "a@b.example"}}, FailureRefused, ""},
		{"network", &api.Error{Code: api.CodeNetworkError}, FailureOther, ""},
		{"plain error", errors.New("disconnected"), FailureOther, ""},
		{"nil", nil, FailureOther, ""},
	} {
		f, who := ClassifyFailure(c.err)
		if f != c.f || who != c.who {
			t.Errorf("%s: (%d, %q), want (%d, %q)", c.name, f, who, c.f, c.who)
		}
	}
}

func TestIsClientMissing(t *testing.T) {
	if !IsClientMissing(&api.Error{Code: api.CodeOAuthClientMissing}) || !IsClientMissing(fmt.Errorf("start: %w", &api.Error{Code: api.CodeOAuthClientMissing})) {
		t.Error("oauthClientMissing not recognised")
	}
	if IsClientMissing(&api.Error{Code: api.CodeAuthFailed}) || IsClientMissing(errors.New("x")) || IsClientMissing(nil) {
		t.Error("other errors taken for oauthClientMissing")
	}
}

func TestTestNeedsSignIn(t *testing.T) {
	ok := &api.EndpointTestResult{OK: true}
	refused := &api.EndpointTestResult{Error: &api.Error{Code: api.CodeAuthFailed}}
	signedOut := &api.EndpointTestResult{Error: &api.Error{Code: api.CodeAuthRequired}}
	unreachable := &api.EndpointTestResult{Error: &api.Error{Code: api.CodeNetworkError}}
	for _, c := range []struct {
		name string
		res  api.AccountTestResult
		err  error
		want bool
	}{
		{"all fine", api.AccountTestResult{IMAP: ok, SMTP: ok}, nil, false},
		{"imap refused", api.AccountTestResult{IMAP: refused, SMTP: ok}, nil, true},
		{"smtp signed out", api.AccountTestResult{IMAP: ok, SMTP: signedOut}, nil, true},
		{"graph signed out", api.AccountTestResult{Graph: signedOut}, nil, true},
		{"network only", api.AccountTestResult{IMAP: unreachable, SMTP: ok}, nil, false},
		{"call auth required", api.AccountTestResult{}, &api.Error{Code: api.CodeAuthRequired}, true},
		{"call failed otherwise", api.AccountTestResult{}, &api.Error{Code: api.CodeKeyringError}, false},
		{"disconnected", api.AccountTestResult{}, errors.New("disconnected"), false},
		{"empty", api.AccountTestResult{}, nil, false},
	} {
		if got := TestNeedsSignIn(c.res, c.err); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestBrowserURL(t *testing.T) {
	for u, want := range map[string]bool{
		"https://accounts.google.com/o/oauth2/v2/auth?client_id=x&state=y":                  true,
		"https://login.microsoftonline.com/common/oauth2/v2.0/authorize?login_hint=a%40b.c": true,
		"HTTPS://accounts.google.com/":                                                      true,
		"http://accounts.google.com/":                                                       false,
		"file:///etc/passwd":                                                                false,
		"javascript:alert(1)":                                                               false,
		"https:opaque":                                                                      false,
		"https:///path-only":                                                                false,
		"https://user:pass@accounts.google.com/":                                            false,
		"":                                                                                  false,
		"accounts.google.com/o/oauth2":                                                      false,
		"https://[::1":                                                                      false,
	} {
		if got := BrowserURL(u); got != want {
			t.Errorf("BrowserURL(%q) = %v, want %v", u, got, want)
		}
	}
}
