// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/auth/goa"
	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/discover"
	"github.com/schotek/malachi/backend/internal/graph"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

const goaID = "account_1788512854_0"

// fakeGOA serves canned accounts and tokens.
type fakeGOA struct {
	accounts    []goa.Account
	listErr     error
	tokens      map[string]string
	tokenErr    error
	invalidated []string
}

func (f *fakeGOA) Accounts(context.Context) ([]goa.Account, error) { return f.accounts, f.listErr }
func (f *fakeGOA) AccessToken(_ context.Context, id string) (string, time.Time, error) {
	if f.tokenErr != nil {
		return "", time.Time{}, f.tokenErr
	}
	tok, ok := f.tokens[id]
	if !ok {
		return "", time.Time{}, api.NewError(api.CodeAuthRequired, "no such account")
	}
	return tok, time.Now().Add(time.Hour), nil
}
func (f *fakeGOA) Invalidate(id string) { f.invalidated = append(f.invalidated, id) }

func graphConfig() api.AccountConfig {
	return api.AccountConfig{
		Name: "Contoso", Email: "me@contoso.invalid", Kind: api.AccountGraph,
		Graph: &api.GraphConfig{Source: api.GraphSourceGOA, GOAAccountID: goaID},
	}
}

func TestGraphAccountValidation(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*api.AccountConfig)
		ok   bool
	}{
		{"valid", func(*api.AccountConfig) {}, true},
		{"id trimmed", func(c *api.AccountConfig) { c.Graph.GOAAccountID = " " + goaID + " " }, true},
		{"no graph settings", func(c *api.AccountConfig) { c.Graph = nil }, false},
		{"unknown source", func(c *api.AccountConfig) { c.Graph.Source = "keyring" }, false},
		{"empty id", func(c *api.AccountConfig) { c.Graph.GOAAccountID = "" }, false},
		{"path in id", func(c *api.AccountConfig) { c.Graph.GOAAccountID = "../x" }, false},
		{"imap given", func(c *api.AccountConfig) { c.IMAP = validConfig().IMAP }, false},
		{"smtp given", func(c *api.AccountConfig) { c.SMTP = validConfig().SMTP }, false},
		{"oauth2 given", func(c *api.AccountConfig) { c.OAuth2 = &api.OAuth2Config{Provider: "office365"} }, false},
		{"unknown kind", func(c *api.AccountConfig) { c.Kind = "ews" }, false},
		{"imap kind without servers", func(c *api.AccountConfig) { c.Kind = api.AccountIMAP }, false},
		{"imap kind with graph", func(c *api.AccountConfig) {
			c.Kind = api.AccountIMAP
			c.IMAP, c.SMTP = validConfig().IMAP, validConfig().SMTP
		}, false},
	}
	for _, tc := range cases {
		c := graphConfig()
		tc.mut(&c)
		err := validateAccountConfig(&c)
		if tc.ok && err != nil {
			t.Errorf("%s: unexpected error %v", tc.name, err)
		}
		if !tc.ok && errCode(t, err) != api.CodeInvalidArgument {
			t.Errorf("%s: code %v, want invalidArgument", tc.name, err)
		}
		if tc.ok && c.Graph.GOAAccountID != goaID {
			t.Errorf("%s: id not trimmed: %q", tc.name, c.Graph.GOAAccountID)
		}
	}
	// The empty kind still means imap and still requires the servers.
	c := validConfig()
	if err := validateAccountConfig(&c); err != nil || c.Protocol() != api.AccountIMAP {
		t.Fatalf("imap default: %v %q", err, c.Protocol())
	}
}

func TestGraphAccountAddAndRoute(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	imapSup, graphSup := newFakeSupervisor(), newFakeSupervisor()
	imapOut, graphOut := newFakeOutbox(), newFakeOutbox()
	b.Supervisor = newKindSupervisor(imapSup, graphSup)
	b.Delivery = newKindOutbox(imapOut, graphOut)

	// A password makes no sense for a Graph account; no keyring is needed.
	if _, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: graphConfig(), Credentials: api.Credentials{Password: "x"}}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("password accepted: %v", err)
	}
	added, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: graphConfig()})
	if err != nil {
		t.Fatal(err)
	}
	id := string(added.AccountID)
	if fmt.Sprint(graphSup.calls) != "[start:"+id+"]" || len(imapSup.calls) != 0 {
		t.Fatalf("sync routing: graph=%v imap=%v", graphSup.calls, imapSup.calls)
	}
	if fmt.Sprint(graphOut.calls) != "[start:"+id+"]" || len(imapOut.calls) != 0 {
		t.Fatalf("outbox routing: graph=%v imap=%v", graphOut.calls, imapOut.calls)
	}
	list, _ := b.Accounts().List(ctx, api.AccountListParams{})
	if len(list.Accounts) != 1 || list.Accounts[0].Config.Kind != api.AccountGraph || list.Accounts[0].Config.IMAP != nil ||
		list.Accounts[0].Config.Graph.GOAAccountID != goaID {
		t.Fatalf("list = %+v", list.Accounts)
	}

	// Trigger, state and wake follow the kind; an IMAP account goes the other way.
	graphSup.setState(id, api.SyncState{Status: api.SyncSyncing, Progress: 10})
	if st, ok := b.Supervisor.State(id); !ok || st.Status != api.SyncSyncing {
		t.Fatalf("state via dispatcher: %+v %v", st, ok)
	}
	if !b.Supervisor.Trigger(id, "", false) || !b.Delivery.Wake(id) {
		t.Fatal("trigger/wake not routed")
	}
	if b.Supervisor.Trigger("acc_unknown", "", false) || b.Delivery.Wake("acc_unknown") {
		t.Fatal("unknown account routed")
	}
	imapAdded, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: validConfig()})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fmt.Sprint(imapSup.calls), "start:"+string(imapAdded.AccountID)) {
		t.Fatalf("imap account not started on the imap supervisor: %v", imapSup.calls)
	}

	// Update and remove stay on the graph side; reload reaches both.
	cfg := graphConfig()
	cfg.Name = "Renamed"
	if _, err := b.Accounts().Update(ctx, api.AccountUpdateParams{AccountID: api.AccountID(id), Config: cfg}); err != nil {
		t.Fatal(err)
	}
	b.Supervisor.Reload()
	if _, err := b.Accounts().Remove(ctx, api.AccountRemoveParams{AccountID: api.AccountID(id)}); err != nil {
		t.Fatal(err)
	}
	want := "[start:" + id + " trigger:" + id + "::false restart:" + id + " reload stop:" + id + "]"
	if got := fmt.Sprint(graphSup.calls); got != want {
		t.Fatalf("graph calls = %s, want %s", got, want)
	}
	if !strings.Contains(fmt.Sprint(imapSup.calls), "reload") {
		t.Fatalf("imap side not reloaded: %v", imapSup.calls)
	}
	if _, ok := b.Supervisor.State(id); ok {
		t.Fatal("removed account still known to the dispatcher")
	}
}

func TestKindSupervisorRestartAcrossKinds(t *testing.T) {
	imapSup, graphSup := newFakeSupervisor(), newFakeSupervisor()
	k := newKindSupervisor(imapSup, graphSup)
	a := store.Account{ID: "acc_1", Config: validConfig()}
	k.Start(a)
	a.Config = graphConfig()
	k.Restart(a)
	if fmt.Sprint(imapSup.calls) != "[start:acc_1 stop:acc_1]" || fmt.Sprint(graphSup.calls) != "[start:acc_1]" {
		t.Fatalf("kind change: imap=%v graph=%v", imapSup.calls, graphSup.calls)
	}
	k.Restart(a)
	if fmt.Sprint(graphSup.calls) != "[start:acc_1 restart:acc_1]" {
		t.Fatalf("same-kind restart: %v", graphSup.calls)
	}
}

func TestGraphAccountTest(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	fg := &fakeGOA{tokens: map[string]string{goaID: "tok-secret"}}
	b.GOA = fg
	var gotToken string
	b.ProbeGraph = func(_ context.Context, token string) (graph.ProbeResult, error) {
		gotToken = token
		return graph.ProbeResult{Email: "Me@Contoso.invalid", Capabilities: []string{"graph"}, Latency: 42 * time.Millisecond}, nil
	}

	res, err := b.Accounts().Test(ctx, api.AccountTestParams{Config: graphConfig()})
	if err != nil {
		t.Fatal(err)
	}
	if res.IMAP != nil || res.SMTP != nil || res.Graph == nil || !res.Graph.OK || res.Graph.LatencyMS != 42 || gotToken != "tok-secret" {
		t.Fatalf("result = %+v (token %q)", res, gotToken)
	}

	// The signed-in mailbox must be the configured address.
	cfg := graphConfig()
	cfg.Email = "other@contoso.invalid"
	res, _ = b.Accounts().Test(ctx, api.AccountTestParams{Config: cfg})
	if res.Graph.OK || res.Graph.Error.Code != api.CodeInvalidArgument {
		t.Fatalf("mismatched mailbox: %+v", res.Graph)
	}

	// Token problems are the endpoint's outcome, not a call failure.
	fg.tokenErr = api.NewError(api.CodeAuthRequired, "sign in again")
	res, err = b.Accounts().Test(ctx, api.AccountTestParams{Config: graphConfig()})
	if err != nil || res.Graph.OK || res.Graph.Error.Code != api.CodeAuthRequired {
		t.Fatalf("auth required: %+v, %v", res.Graph, err)
	}
	fg.tokenErr = api.NewError(api.CodeUnavailable, "no bus")
	res, _ = b.Accounts().Test(ctx, api.AccountTestParams{Config: graphConfig()})
	if res.Graph.Error.Code != api.CodeUnavailable {
		t.Fatalf("unavailable: %+v", res.Graph)
	}
	fg.tokenErr = nil
	b.ProbeGraph = func(context.Context, string) (graph.ProbeResult, error) {
		return graph.ProbeResult{}, errors.New("dial tcp: connection refused tok-secret")
	}
	res, _ = b.Accounts().Test(ctx, api.AccountTestParams{Config: graphConfig()})
	if res.Graph.OK || res.Graph.Error.Code != api.CodeServerError {
		t.Fatalf("plain error: %+v", res.Graph)
	}
}

func TestGraphTokenFor(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	fg := &fakeGOA{tokens: map[string]string{goaID: "tok"}}
	b.GOA = fg
	b.Supervisor, b.Delivery = newFakeSupervisor(), newFakeOutbox()
	added, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: graphConfig()})
	if err != nil {
		t.Fatal(err)
	}
	if tok, err := b.GraphTokenFor(ctx, string(added.AccountID)); err != nil || tok != "tok" {
		t.Fatalf("token: %q %v", tok, err)
	}
	if _, err := b.GraphTokenFor(ctx, "acc_nope"); errCode(t, err) != api.CodeAccountNotFound {
		t.Fatalf("unknown: %v", err)
	}
	imapAdded, _ := b.Accounts().Add(ctx, api.AccountAddParams{Config: validConfig()})
	if _, err := b.GraphTokenFor(ctx, string(imapAdded.AccountID)); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("imap account: %v", err)
	}
	b.InvalidateGraphToken(graphConfig())
	if fmt.Sprint(fg.invalidated) != "["+goaID+"]" {
		t.Fatalf("invalidate: %v", fg.invalidated)
	}
	b.GOA = nil
	if _, err := b.GraphTokenFor(ctx, string(added.AccountID)); errCode(t, err) != api.CodeUnavailable {
		t.Fatalf("no goa: %v", err)
	}
}

func TestAccountLinked(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	b.Supervisor, b.Delivery = newFakeSupervisor(), newFakeOutbox()
	fg := &fakeGOA{accounts: []goa.Account{
		{ID: "account_1_0", ProviderType: "owncloud", Email: "cloud@example.invalid", OAuth2: false},
		{ID: goaID, ProviderType: goa.ProviderMicrosoft365, Email: "Me@Contoso.invalid", Name: " Me Myself ", OAuth2: true, AttentionNeeded: true},
		{ID: "account_2_0", ProviderType: goa.ProviderMicrosoft365, Email: "off@contoso.invalid", OAuth2: true, MailDisabled: true},
		{ID: "account_3_0", ProviderType: goa.ProviderMicrosoft365, Identity: "fallback@contoso.invalid", OAuth2: true},
		{ID: "account_4_0", ProviderType: goa.ProviderMicrosoft365, Email: "not an address", OAuth2: true},
		{ID: "account_5_0", ProviderType: goa.ProviderMicrosoft365, Email: "bad@contoso.invalid", Name: "a\x01b", OAuth2: true},
	}}
	b.GOA = fg

	cfg := graphConfig()
	cfg.Email = "ME@contoso.invalid"
	if _, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: cfg}); err != nil {
		t.Fatal(err)
	}
	res, err := b.Accounts().Linked(ctx, api.AccountLinkedParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Accounts) != 3 {
		t.Fatalf("linked = %+v", res.Accounts)
	}
	me := res.Accounts[0]
	if me.Provider != "microsoft365" || me.Email != "Me@Contoso.invalid" || me.Name != "Me Myself" || me.GOAAccountID != goaID ||
		!me.Configured || !me.AttentionNeeded {
		t.Fatalf("first = %+v", me)
	}
	if res.Accounts[1].Email != "fallback@contoso.invalid" || res.Accounts[1].Configured {
		t.Fatalf("identity fallback = %+v", res.Accounts[1])
	}
	if res.Accounts[2].Name != "" {
		t.Fatalf("control characters in name kept: %+v", res.Accounts[2])
	}

	// No GOA: an empty list. Other failures propagate.
	fg.listErr = api.NewError(api.CodeUnavailable, "no bus")
	res, err = b.Accounts().Linked(ctx, api.AccountLinkedParams{})
	if err != nil || len(res.Accounts) != 0 || res.Accounts == nil {
		t.Fatalf("unavailable: %+v, %v", res, err)
	}
	fg.listErr = api.NewError(api.CodeServerError, "boom")
	if _, err := b.Accounts().Linked(ctx, api.AccountLinkedParams{}); errCode(t, err) != api.CodeServerError {
		t.Fatalf("server error: %v", err)
	}
	b.GOA = nil
	if res, err := b.Accounts().Linked(ctx, api.AccountLinkedParams{}); err != nil || len(res.Accounts) != 0 {
		t.Fatalf("nil client: %+v, %v", res, err)
	}
}

func TestGOAAccountForAndProviderDiscovery(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	b.GOA = &fakeGOA{accounts: []goa.Account{
		{ID: "account_1_0", ProviderType: goa.ProviderMicrosoft365, Email: "Me@Contoso.invalid", OAuth2: true},
		{ID: "account_2_0", ProviderType: goa.ProviderMicrosoft365, Identity: "ident@contoso.invalid", OAuth2: true},
		{ID: "account_3_0", ProviderType: goa.ProviderMicrosoft365, Email: "off@contoso.invalid", OAuth2: true, MailDisabled: true},
		{ID: "account_4_0", ProviderType: "owncloud", Email: "cloud@example.invalid"},
	}}
	cases := map[string]string{
		"me@contoso.invalid": "account_1_0", "IDENT@contoso.invalid": "account_2_0",
		"off@contoso.invalid": "", "cloud@example.invalid": "", "nobody@contoso.invalid": "",
	}
	for email, want := range cases {
		id, ok := b.goaAccountFor(ctx, email)
		if id != want || ok != (want != "") {
			t.Errorf("goaAccountFor(%q) = %q, %v", email, id, ok)
		}
	}
	b.GOA = &fakeGOA{listErr: api.NewError(api.CodeUnavailable, "no bus")}
	if _, ok := b.goaAccountFor(ctx, "me@contoso.invalid"); ok {
		t.Error("match without GOA")
	}

	// A provider answer passes through unvalidated; a goa answer is validated.
	b.Discover = func(_ context.Context, email string) (discover.Result, error) {
		return discover.Result{
			Config: &api.AccountConfig{Name: "contoso.invalid", Email: email, Kind: api.AccountGraph, Graph: &api.GraphConfig{Source: api.GraphSourceGOA}},
			Source: api.DiscoverProvider, ProviderName: "Microsoft 365",
		}, nil
	}
	res, err := b.Accounts().Discover(ctx, api.AccountDiscoverParams{Email: "me@contoso.invalid"})
	if err != nil || res.Source != api.DiscoverProvider || res.Config == nil || res.Config.Graph.GOAAccountID != "" {
		t.Fatalf("provider discovery: %+v, %v", res, err)
	}
	b.Discover = func(_ context.Context, email string) (discover.Result, error) {
		return discover.Result{
			Config: &api.AccountConfig{Name: "contoso.invalid", Email: email, Kind: api.AccountGraph, Graph: &api.GraphConfig{Source: api.GraphSourceGOA}},
			Source: api.DiscoverGOA,
		}, nil
	}
	res, err = b.Accounts().Discover(ctx, api.AccountDiscoverParams{Email: "me@contoso.invalid"})
	if err != nil || res.Source != api.DiscoverNone || res.Config != nil {
		t.Fatalf("invalid goa answer not rejected: %+v, %v", res, err)
	}
}
