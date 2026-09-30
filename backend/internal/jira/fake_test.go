// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"context"
	"time"

	"github.com/schotek/malachi/backend/internal/jira/jiratest"
)

// The fake site the tests talk to is jiratest.Server (shared with the
// packages that drive a Jira account end to end); these helpers build the
// package's own clients over it.

// optionsOf are client options for the fake site with its current token (a
// later change of f.Token makes them stale) and no waiting (Sleep returns
// at once).
func optionsOf(f *jiratest.Server) Options {
	cfg := f.Config()
	var token string
	f.Set(func(f *jiratest.Server) { token = f.Token })
	return Options{
		SiteURL: cfg.SiteURL, Deployment: cfg.Deployment, CloudID: cfg.CloudID, Login: cfg.Login,
		Token: func(context.Context) (string, error) { return token, nil },
		HTTP:  f.HTTPClient(),
		Sleep: func(context.Context, time.Duration) error { return nil },
	}
}

// remoteOf is a Remote over optionsOf(f), edited by edit.
func remoteOf(f *jiratest.Server, edit ...func(*Options)) Remote {
	t := f.TB()
	t.Helper()
	o := optionsOf(f)
	for _, e := range edit {
		e(&o)
	}
	c, err := NewClient(o)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return NewRemote(c)
}
