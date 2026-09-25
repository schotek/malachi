// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package oauth2flow

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

// httpTimeout bounds one request to a token endpoint.
const httpTimeout = 30 * time.Second

// errRedirect refuses every redirect of a token endpoint.
var errRedirect = errors.New("redirect refused")

func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout: httpTimeout,
		Transport: &http.Transport{
			TLSClientConfig:     transport.TLSConfig(""),
			Proxy:               http.ProxyFromEnvironment,
			DisableKeepAlives:   true,
			TLSHandshakeTimeout: 10 * time.Second,
			ForceAttemptHTTP2:   true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return errRedirect },
	}
}

// withHTTP hands the client to x/oauth2 through the context.
func withHTTP(ctx context.Context, c *http.Client) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, c)
}

// redacted replaces each secret in s. Secrets shorter than minSecret are
// skipped (they would mangle ordinary words and are not credentials).
func redacted(s string, secrets ...string) string {
	for _, v := range secrets {
		if len(v) >= minSecret {
			s = strings.ReplaceAll(s, v, "<redacted>")
		}
	}
	return s
}

const minSecret = 4

// cleanMsg makes text safe for an error message: secrets redacted before
// and after cleaning, control characters gone, capped by
// transport.CleanMessage.
func cleanMsg(s string, secrets ...string) string {
	s = redacted(s, secrets...)
	s = transport.CleanMessage(s)
	return redacted(s, secrets...)
}

// tokenPhase says which request failed; it decides how a provider's
// refusal is reported.
type tokenPhase int

const (
	phaseExchange tokenPhase = iota // authorization code → tokens
	phaseRefresh                    // refresh token → access token
)

// Provider error codes that mean the stored sign-in is gone (RFC 6749
// §5.2; the last three are OpenID Connect / Microsoft).
var signInLost = map[string]bool{
	"invalid_grant":        true,
	"interaction_required": true,
	"login_required":       true,
	"consent_required":     true,
}

// classifyToken maps a failure of a token-endpoint request to the
// contract error; the message names the provider's error code and a
// cleaned, redacted description, never a secret.
func classifyToken(ctx context.Context, phase tokenPhase, err error, secrets ...string) *api.Error {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		detail := "token endpoint refused the request"
		if re.ErrorCode != "" {
			detail += ": " + re.ErrorCode
			if re.ErrorDescription != "" {
				detail += ": " + re.ErrorDescription
			}
		} else if re.Response != nil {
			detail += ": HTTP " + re.Response.Status
		}
		msg := cleanMsg(detail, secrets...)
		status := 0
		if re.Response != nil {
			status = re.Response.StatusCode
		}
		switch {
		case re.ErrorCode == "invalid_client" || re.ErrorCode == "unauthorized_client":
			return api.NewError(api.CodeAuthFailed, "%s", msg)
		case signInLost[re.ErrorCode]:
			if phase == phaseRefresh {
				return api.NewError(api.CodeAuthRequired, "%s", msg)
			}
			return api.NewError(api.CodeAuthFailed, "%s", msg)
		case re.ErrorCode == "temporarily_unavailable", status >= 500, status == http.StatusTooManyRequests,
			status >= 300 && status < 400:
			return api.NewError(api.CodeServerError, "%s", msg)
		default:
			return api.NewError(api.CodeAuthFailed, "%s", msg)
		}
	}
	if errors.Is(err, errRedirect) {
		return api.NewError(api.CodeServerError, "%s", cleanMsg("token endpoint: "+err.Error(), secrets...))
	}
	c := transport.Classify(ctx, transport.StageCommand, err)
	if c.Code == api.CodeCancelled {
		return c
	}
	return api.NewError(c.Code, "%s", cleanMsg("token endpoint: "+err.Error(), secrets...))
}
