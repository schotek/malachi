// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package chatgpt provides native SIWC and an isolated Codex App Server client.
// Mailbox operations still pass exclusively through the existing MCP bridge.
package chatgpt

import (
	"context"
	"net/http"
	"time"
)

// Status is safe connection presentation, independent of Claude registration.
type Status int

const (
	Disconnected Status = iota
	SigningIn
	Connected
	ReconnectRequired
)

// Connection never contains tokens or an authorization URL.
type Connection struct {
	Status                                 Status
	Email, ConnectionID, ClientID, Subject string
}

// AuthError contains a stable diagnostic code, never a response body.
type AuthError struct{ Code string }

func (e *AuthError) Error() string { return "chatgpt_" + e.Code }
func authError(code string) error  { return &AuthError{Code: code} }

// Registration is non-secret native application metadata.
type Registration struct{ HostID, ConnectionID, ClientID, Subject, Email string }

// Tokens are consumed only by the coordinator and Secret Service adapter.
type Tokens struct {
	ConnectionID, AccessToken, RefreshToken, IDToken, Scope string
	ExpiresAt                                               time.Time
}

func (Tokens) String() string   { return "ChatGPT tokens" }
func (Tokens) GoString() string { return "ChatGPT tokens" }

// CredentialStore operations occur while the returned process-shared lease is held.
type CredentialStore interface {
	Acquire(context.Context) (func(), error)
	ReadRegistration(context.Context) (*Registration, error)
	WriteRegistration(context.Context, Registration) error
	ReadTokens(context.Context) (*Tokens, error)
	WriteTokens(context.Context, Tokens) error
	DeleteTokens(context.Context) error
}

// Browser binds a loopback callback before launching an injected system browser.
type Browser interface {
	Authorize(context.Context, func(string) (string, error), string) (string, error)
}

// TokenSource retains actual access tokens exclusively in the native gateway.
type TokenSource interface {
	GetAccessToken(context.Context) (string, error)
	SessionContext() context.Context
}

// InferenceObserver invalidates only the still-current access token on HTTP 401.
type InferenceObserver interface {
	InferenceRejected(context.Context, string, int) error
}

// Options captures non-secret runtime configuration at session creation.
type Options struct {
	Executable                func() string
	Bridge, Socket, Directory string
	Env                       []string
	Model                     func() string
	HasConsent                func() bool
	AcceptConsent             func()
	HTTPClient                *http.Client
}

// Model is a provider catalog entry without mail or credentials.
type Model struct{ ID, Name string }
