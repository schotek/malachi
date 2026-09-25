// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package oauth2flow

import (
	"net"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestDefaultEndpoints(t *testing.T) {
	g := DefaultEndpoints(Google, "ignored")
	if g.AuthURL != "https://accounts.google.com/o/oauth2/v2/auth" || g.TokenURL != "https://oauth2.googleapis.com/token" {
		t.Errorf("google endpoints %+v", g)
	}
	ms := DefaultEndpoints(Microsoft, "")
	if ms.AuthURL != "https://login.microsoftonline.com/common/oauth2/v2.0/authorize" ||
		ms.TokenURL != "https://login.microsoftonline.com/common/oauth2/v2.0/token" {
		t.Errorf("microsoft common endpoints %+v", ms)
	}
	ten := DefaultEndpoints(Microsoft, "contoso.onmicrosoft.com")
	if ten.AuthURL != "https://login.microsoftonline.com/contoso.onmicrosoft.com/oauth2/v2.0/authorize" ||
		ten.TokenURL != "https://login.microsoftonline.com/contoso.onmicrosoft.com/oauth2/v2.0/token" {
		t.Errorf("microsoft tenant endpoints %+v", ten)
	}
	if strings.Contains(DefaultEndpoints(Microsoft, "a/b?c").AuthURL, "a/b?c") {
		t.Error("tenant not escaped")
	}
	if e := DefaultEndpoints("custom", ""); e != (Endpoints{}) {
		t.Errorf("unknown provider endpoints %+v", e)
	}
}

func TestRegistryResolve(t *testing.T) {
	builtinClients[Google] = Client{ID: "builtin-g", Secret: "builtin-secret"}
	t.Cleanup(func() { delete(builtinClients, Google) })

	r := &Registry{Configured: map[Provider]Client{
		Google:    {ID: "conf-g", Secret: "conf-secret"},
		Microsoft: {ID: "conf-ms", Tenant: "conf-tenant"},
	}}
	cases := []struct {
		name string
		reg  *Registry
		p    Provider
		o    *api.OAuth2Config
		want Client
	}{
		{"configured", r, Google, nil, Client{ID: "conf-g", Secret: "conf-secret"}},
		{"account overrides, no secret", r, Google, &api.OAuth2Config{ClientID: "own-g"}, Client{ID: "own-g"}},
		{"account equals configured keeps secret", r, Google, &api.OAuth2Config{ClientID: "conf-g"}, Client{ID: "conf-g", Secret: "conf-secret"}},
		{"account equals builtin keeps builtin secret", r, Google, &api.OAuth2Config{ClientID: "builtin-g"}, Client{ID: "builtin-g", Secret: "builtin-secret"}},
		{"builtin fallback", &Registry{}, Google, nil, Client{ID: "builtin-g", Secret: "builtin-secret"}},
		{"nil registry uses builtin", nil, Google, &api.OAuth2Config{}, Client{ID: "builtin-g", Secret: "builtin-secret"}},
		{"google ignores tenant", r, Google, &api.OAuth2Config{TenantID: "x"}, Client{ID: "conf-g", Secret: "conf-secret"}},
		{"microsoft configured tenant", r, Microsoft, nil, Client{ID: "conf-ms", Tenant: "conf-tenant"}},
		{"microsoft account tenant wins", r, Microsoft, &api.OAuth2Config{TenantID: "acct-tenant"}, Client{ID: "conf-ms", Tenant: "acct-tenant"}},
		{"microsoft own client drops configured tenant", r, Microsoft, &api.OAuth2Config{ClientID: "own-ms"}, Client{ID: "own-ms"}},
		{"microsoft own client and tenant", r, Microsoft, &api.OAuth2Config{ClientID: "own-ms", TenantID: "t"}, Client{ID: "own-ms", Tenant: "t"}},
	}
	for _, c := range cases {
		got, err := c.reg.Resolve(c.p, c.o)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

// withoutBuiltinClients runs a test as if the backend shipped no client.
func withoutBuiltinClients(t *testing.T) {
	t.Helper()
	saved := builtinClients
	builtinClients = map[Provider]Client{}
	t.Cleanup(func() { builtinClients = saved })
}

func TestRegistryBuiltinMicrosoft(t *testing.T) {
	r := &Registry{}
	c, err := r.Resolve(Microsoft, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.ID == "" || c.Secret != "" || c.Tenant != "" {
		t.Errorf("built-in Microsoft client = %+v, want an id, no secret, the default tenant", c)
	}
	if !r.Available(Microsoft) || r.Available(Google) {
		t.Error("Available: want the built-in Microsoft client and no Google one")
	}
	// config.toml still wins over the built-in one.
	r = &Registry{Configured: map[Provider]Client{Microsoft: {ID: "own"}}}
	if c, _ := r.Resolve(Microsoft, nil); c.ID != "own" {
		t.Errorf("configured client not preferred: %+v", c)
	}
}

func TestRegistryMissingClient(t *testing.T) {
	withoutBuiltinClients(t)
	r := &Registry{Configured: map[Provider]Client{Google: {ID: "g"}}}
	_, err := r.Resolve(Microsoft, &api.OAuth2Config{TenantID: "t"})
	if apiCode(err) != api.CodeOAuthClientMissing {
		t.Fatalf("err = %v, want oauthClientMissing", err)
	}
	if !strings.Contains(err.Error(), "office365") {
		t.Errorf("message does not name the provider: %v", err)
	}
	if _, err := r.Resolve("custom", nil); apiCode(err) != api.CodeInvalidArgument {
		t.Errorf("unknown provider: %v", err)
	}
	if !r.Available(Google) || r.Available(Microsoft) {
		t.Error("Available wrong")
	}
	var nilReg *Registry
	if nilReg.Available(Google) {
		t.Error("nil registry available")
	}
}

func TestRedirectURL(t *testing.T) {
	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 49152}
	if got := redirectURL(addr); got != "http://127.0.0.1:49152/" {
		t.Errorf("redirectURL = %q", got)
	}
}
