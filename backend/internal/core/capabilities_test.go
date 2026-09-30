// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"slices"
	"testing"

	"github.com/schotek/malachi/backend/internal/auth/goa"
	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Every account in account.list carries its capabilities: the mail set
// for a mailbox, comment and forward for an issue tracker.
func TestAccountListCapabilities(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	b.Supervisor, b.Delivery = newFakeSupervisor(), newFakeOutbox()
	if _, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: validConfig()}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: graphConfig()}); err != nil {
		t.Fatal(err)
	}
	// Stored directly and paused: nothing is to start for it.
	jira := store.Account{Name: "Acme", Enabled: false, Config: api.AccountConfig{
		Name: "Acme", Email: "me@example.invalid", Kind: api.AccountJira,
		Jira: &api.JiraConfig{SiteURL: "https://acme.atlassian.net", Deployment: api.JiraCloud, Login: "me@example.invalid"},
	}}
	if err := b.store.AddAccount(ctx, &jira); err != nil {
		t.Fatal(err)
	}
	res, err := b.Accounts().List(ctx, api.AccountListParams{})
	if err != nil || len(res.Accounts) != 3 {
		t.Fatalf("list: %+v %v", res, err)
	}
	for _, a := range res.Accounts[:2] {
		if !slices.Equal(a.Capabilities, api.MailCapabilities) {
			t.Errorf("%s: capabilities %v", a.Config.Protocol(), a.Capabilities)
		}
	}
	res.Accounts[0].Capabilities[0] = "changed"
	if api.MailCapabilities[0] != api.CapabilityCompose {
		t.Fatal("a listed account shares MailCapabilities")
	}
	if caps := res.Accounts[2].Capabilities; !slices.Equal(caps, []api.AccountCapability{api.CapabilityComment, api.CapabilityForward, api.CapabilityTransition}) {
		t.Errorf("jira: capabilities %v", caps)
	}
}

// A mail kind carries no jira block; an issue-tracker account does not
// make the address of a desktop account "configured".
func TestJiraConfigOnMailAccountsAndLinked(t *testing.T) {
	ctx := context.Background()
	c := validConfig()
	c.Jira = &api.JiraConfig{SiteURL: "https://acme.atlassian.net"}
	if err := validateAccountConfig(&c); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("imap with jira settings: %v", err)
	}
	g := graphConfig()
	g.Jira = &api.JiraConfig{}
	if err := validateAccountConfig(&g); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("graph with jira settings: %v", err)
	}

	b := newTestBackend(t, config.Default())
	b.Supervisor, b.Delivery = newFakeSupervisor(), newFakeOutbox()
	b.GOA = &fakeGOA{accounts: []goa.Account{
		{ID: goaID, ProviderType: goa.ProviderMicrosoft365, Email: "me@contoso.invalid", OAuth2: true},
	}}
	jira := store.Account{Name: "Contoso Jira", Enabled: false, Config: api.AccountConfig{
		Name: "Contoso Jira", Email: "me@contoso.invalid", Kind: api.AccountJira,
		Jira: &api.JiraConfig{SiteURL: "https://contoso.atlassian.net", Deployment: api.JiraCloud, Login: "me@contoso.invalid"},
	}}
	if err := b.store.AddAccount(ctx, &jira); err != nil {
		t.Fatal(err)
	}
	res, err := b.Accounts().Linked(ctx, api.AccountLinkedParams{})
	if err != nil || len(res.Accounts) != 1 || res.Accounts[0].Configured {
		t.Fatalf("linked with a jira account of the address: %+v %v", res, err)
	}
	// The mailbox itself can still be added beside it, and then counts.
	if _, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: graphConfig()}); err != nil {
		t.Fatalf("mailbox beside the jira account: %v", err)
	}
	if res, _ := b.Accounts().Linked(ctx, api.AccountLinkedParams{}); !res.Accounts[0].Configured {
		t.Fatalf("linked with the mailbox: %+v", res.Accounts)
	}
}
