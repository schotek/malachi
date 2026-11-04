// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package graph is the Microsoft Graph mail backend: a sync engine and a
// delivery function for Microsoft 365 / Outlook.com accounts, beside
// internal/imap. It speaks the Graph REST API over HTTPS with an access
// token supplied by the caller (internal/auth/goa) and fills the same
// store as the IMAP engine. There is no push channel a desktop client can
// use, so folders are polled with delta queries.
package graph

import (
	"context"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// ProbeResult is what account.test learns from a Graph mailbox.
type ProbeResult struct {
	// Email is the mailbox's primary address as the server reports it.
	Email        string
	Capabilities []string
	Latency      time.Duration
}

// Probe checks that the token opens the mailbox: it reads the signed-in
// user's address and the inbox. Errors are *api.Error.
func Probe(ctx context.Context, token string) (ProbeResult, error) {
	return ProbeWith(ctx, Options{Token: func(context.Context) (string, error) { return token, nil }})
}

// ProbeWith is Probe over a configured client (tests override BaseURL).
func ProbeWith(ctx context.Context, opts Options) (ProbeResult, error) {
	c := NewClient(opts)
	start := time.Now()
	var me struct {
		Mail              string `json:"mail"`
		UserPrincipalName string `json:"userPrincipalName"`
	}
	if err := c.Get(ctx, "me?$select=mail,userPrincipalName", &me); err != nil {
		return ProbeResult{}, ToAPIError(err)
	}
	latency := time.Since(start)
	var inbox mailFolder
	if err := c.Get(ctx, "me/mailFolders/inbox?$select=id", &inbox); err != nil {
		return ProbeResult{}, ToAPIError(err)
	}
	if inbox.ID == "" {
		return ProbeResult{}, api.NewError(api.CodeServerError, "graph: mailbox has no inbox")
	}
	email := me.Mail
	if email == "" {
		email = me.UserPrincipalName
	}
	return ProbeResult{Email: email, Capabilities: []string{"graph"}, Latency: latency}, nil
}
