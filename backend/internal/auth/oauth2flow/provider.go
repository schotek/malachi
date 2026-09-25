// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package oauth2flow

import (
	"net"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/oauth2"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The provider table: endpoints, scopes and extra authorisation-request
// parameters. Nothing here comes from an account; accounts only choose the
// provider and, optionally, the client.

const (
	googleAuthURL  = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL = "https://oauth2.googleapis.com/token"

	microsoftBase    = "https://login.microsoftonline.com/"
	microsoftTenant  = "common"
	microsoftAuthEnd = "/oauth2/v2.0/authorize"
	microsoftTokEnd  = "/oauth2/v2.0/token"
)

// googleIssuers are the iss values Google's ID tokens carry.
var googleIssuers = []string{"https://accounts.google.com", "accounts.google.com"}

// providerScopes are what the engines need: IMAP/SMTP plus the identity
// for Google, Graph mail plus /me for Microsoft; offline access for the
// refresh token.
var providerScopes = map[Provider][]string{
	Google: {"https://mail.google.com/", "openid", "email"},
	Microsoft: {
		"https://graph.microsoft.com/Mail.ReadWrite",
		"https://graph.microsoft.com/Mail.Send",
		"https://graph.microsoft.com/User.Read",
		"offline_access",
	},
}

// builtinClients are client registrations shipped with the backend, used
// when neither the account nor config.toml names one (docs/architecture.md
// §7). Client ids of public clients are not secrets.
//
// Microsoft: the "Malachi Mail" app registration (multitenant and personal
// Microsoft accounts, public client with PKCE, loopback redirect
// http://127.0.0.1, delegated Graph Mail.ReadWrite, Mail.Send, User.Read,
// offline_access). It is not publisher-verified, so users of organisations
// that restrict consent need their administrator to approve it once.
// Google: none; the restricted Gmail scope needs Google's verification and
// a yearly security assessment before a client can be shipped.
var builtinClients = map[Provider]Client{
	Microsoft: {ID: "af56e4e6-c2ea-4ad8-bbee-bd52597f5b4e"},
}

func knownProvider(p Provider) bool {
	_, ok := providerScopes[p]
	return ok
}

func defaultEndpoints(p Provider, tenant string) Endpoints {
	switch p {
	case Google:
		return Endpoints{AuthURL: googleAuthURL, TokenURL: googleTokenURL}
	case Microsoft:
		if tenant == "" {
			tenant = microsoftTenant
		}
		t := url.PathEscape(tenant)
		return Endpoints{
			AuthURL:  microsoftBase + t + microsoftAuthEnd,
			TokenURL: microsoftBase + t + microsoftTokEnd,
		}
	}
	return Endpoints{}
}

// authParams are the provider-specific parameters of the authorisation
// request besides PKCE and state.
func authParams(p Provider, loginHint string) []oauth2.AuthCodeOption {
	var opts []oauth2.AuthCodeOption
	if p == Google {
		// A refresh token on every consent, also for a re-sign-in.
		opts = append(opts, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
	}
	if loginHint != "" {
		opts = append(opts, oauth2.SetAuthURLParam("login_hint", loginHint))
	}
	return opts
}

// oauthConfig assembles the library configuration. Public clients send
// client_id (and client_secret only when set) in the request body.
func oauthConfig(p Provider, c Client, ep Endpoints, redirectURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     c.ID,
		ClientSecret: c.Secret,
		Endpoint: oauth2.Endpoint{
			AuthURL:   ep.AuthURL,
			TokenURL:  ep.TokenURL,
			AuthStyle: oauth2.AuthStyleInParams,
		},
		RedirectURL: redirectURL,
		Scopes:      append([]string(nil), providerScopes[p]...),
	}
}

// redirectURL is the loopback redirect for a listener address: the root
// path on the literal IP and the port the listener got.
func redirectURL(addr net.Addr) string {
	host, port := "127.0.0.1", ""
	if ta, ok := addr.(*net.TCPAddr); ok {
		if ip4 := ta.IP.To4(); ip4 != nil {
			host = ip4.String()
		}
		port = strconv.Itoa(ta.Port)
	} else if h, p, err := net.SplitHostPort(addr.String()); err == nil {
		host, port = h, p
	}
	return "http://" + net.JoinHostPort(host, port) + "/"
}

func (r *Registry) configured(p Provider) Client {
	if r == nil || r.Configured == nil {
		return Client{}
	}
	return r.Configured[p]
}

// resolve: the account's client id > the configured one > the built-in
// one. The secret and the default tenant belong to a registration, so they
// apply only when the effective id is that registration's; the account's
// tenant (Microsoft only) overrides the default.
func (r *Registry) resolve(p Provider, o *api.OAuth2Config) (Client, error) {
	if !knownProvider(p) {
		return Client{}, api.NewError(api.CodeInvalidArgument, "unknown OAuth2 provider %q", string(p))
	}
	conf, builtin := r.configured(p), builtinClients[p]
	var own, ownTenant string
	if o != nil {
		own = strings.TrimSpace(o.ClientID)
		ownTenant = strings.TrimSpace(o.TenantID)
	}
	var c Client
	switch {
	case own != "":
		c.ID = own
		switch {
		case own == conf.ID:
			c.Secret, c.Tenant = conf.Secret, conf.Tenant
		case own == builtin.ID:
			c.Secret, c.Tenant = builtin.Secret, builtin.Tenant
		}
	case conf.ID != "":
		c = conf
	case builtin.ID != "":
		c = builtin
	default:
		return Client{}, api.NewError(api.CodeOAuthClientMissing,
			"no OAuth client id for provider %q (config.toml [oauth2.*] or the account's clientId)", string(p))
	}
	if p != Microsoft {
		c.Tenant = ""
	} else if ownTenant != "" {
		c.Tenant = ownTenant
	}
	return c, nil
}

func (r *Registry) available(p Provider) bool {
	return r.configured(p).ID != "" || builtinClients[p].ID != ""
}
