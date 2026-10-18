// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package eway

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/eway/ewaytest"
	"github.com/schotek/malachi/backend/pkg/api"
)

type harness struct {
	f     *ewaytest.Server
	c     *Client
	logs  *bytes.Buffer
	slept []time.Duration
}

func newHarness(t *testing.T, password string) *harness {
	t.Helper()
	f := ewaytest.New()
	t.Cleanup(f.Close)
	h := &harness{f: f, logs: &bytes.Buffer{}}
	c, err := NewClient(Options{
		ServiceURL: f.ServiceURL() + "/API.svc",
		Username:   "user",
		Password:   func(context.Context) (string, error) { return password, nil },
		ClientID:   "0123456789abcdef0123456789abcdef",
		HTTP:       f.Client(),
		UserAgent:  UserAgent("1.2.3"),
		Log:        slog.New(slog.NewTextHandler(h.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Sleep: func(_ context.Context, d time.Duration) error {
			h.slept = append(h.slept, d)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.c = c
	return h
}

func code(err error) api.ErrorCode {
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return 0
}

func TestNormaliseServiceURL(t *testing.T) {
	for in, want := range map[string]string{
		"hosting.eway-crm.com/acme":                         "https://hosting.eway-crm.com/acme",
		"https://Hosting.eWay-CRM.com/acme/":                "https://hosting.eway-crm.com/acme",
		"https://hosting.eway-crm.com/acme/API.svc":         "https://hosting.eway-crm.com/acme",
		"https://hosting.eway-crm.com/acme/api.svc/LogIn":   "https://hosting.eway-crm.com/acme",
		"https://hosting.eway-crm.com/acme/InsecureAPI.svc": "https://hosting.eway-crm.com/acme",
		"https://h.example:443/x?y=1#z":                     "https://h.example/x",
		"https://h.example:8443/x":                          "https://h.example:8443/x",
		"  https://crm.example.org  ":                       "https://crm.example.org",
	} {
		got, err := NormaliseServiceURL(in)
		if err != nil || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "http://crm.example.org", "ftp://x", "https://u:p@crm.example.org", "https://", "https://" + strings.Repeat("a", 3000)} {
		if _, err := NormaliseServiceURL(in); code(err) != api.CodeInvalidArgument {
			t.Errorf("%q: %v, want invalidArgument", in, err)
		}
	}
	if _, err := NormaliseServiceURL("http://crm.example.org"); err == nil || !strings.Contains(err.Error(), ErrHTTPOnly) {
		t.Errorf("http: %v", err)
	}
}

func TestOpenLink(t *testing.T) {
	g := "0829CF36-A0DB-4F90-9A3B-1C2D3E4F5A6B"
	got := OpenLink("https://hosting.eway-crm.com/acme", FolderEmails, g)
	want := "https://open.eway-crm.com/?ws=aHR0cHM6Ly9ob3N0aW5nLmV3YXktY3JtLmNvbS9hY21l&l=ZXdheTovL0VtYWlscy8wODI5Y2YzNi1hMGRiLTRmOTAtOWEzYi0xYzJkM2U0ZjVhNmI"
	if got != want {
		t.Errorf("link = %s", got)
	}
	for _, bad := range []string{"", "x", "javascript:alert(1)", g + "/../x"} {
		if OpenLink("https://h", FolderEmails, bad) != "" {
			t.Errorf("a link for %q", bad)
		}
	}
	if OpenLink("https://h", "Users", g) != "" {
		t.Error("a link to an unknown folder")
	}
}

func TestDetectAuth(t *testing.T) {
	h := newHarness(t, "secret")
	s, err := h.c.DetectAuth(context.Background())
	if err != nil || !s.Known || s.AzureAuth {
		t.Fatalf("detect = %+v %v", s, err)
	}
	h.f.AzureAuth = true
	if s, err = h.c.DetectAuth(context.Background()); err != nil || !s.AzureAuth {
		t.Fatalf("azure = %+v %v", s, err)
	}
	h.f.AzureAuth, h.f.NoAuthSettings = false, true
	if s, err = h.c.DetectAuth(context.Background()); err != nil || s.Known {
		t.Fatalf("an older server = %+v %v", s, err)
	}
	if h.f.Logins() != 0 {
		t.Error("detection signed in")
	}
}

// The raw message goes up first under the client's GUID, then SaveEmail
// names it with the fields of D7.
func TestSaveMessageUploadsFirst(t *testing.T) {
	h := newHarness(t, "secret")
	ctx := context.Background()
	m := Message{Raw: []byte("From: a@x\r\n\r\nhi"), FileName: "Hi.eml", MessageID: "abc@example.org",
		Subject: "Hi", Sender: "Alice@Example.org", To: []string{"b@example.org", "not an address", "c@example.org"},
		Cc: []string{"d@example.org"}, SentOn: time.Date(2026, 10, 17, 10, 0, 0, 0, time.FixedZone("", 7200)),
		Received: time.Date(2026, 10, 17, 8, 1, 0, 0, time.UTC), Note: "hi", Attachments: 1}
	s, err := h.c.SaveMessage(ctx, m)
	if err != nil || !s.Created || !ValidGUID(s.GUID) {
		t.Fatalf("save = %+v %v", s, err)
	}
	calls := h.f.Calls()
	up, save := slices.Index(calls, "SaveBinaryAttachment"), slices.Index(calls, "SaveEmail")
	if up < 0 || save < up {
		t.Fatalf("calls = %v", calls)
	}
	es := h.f.Emails()
	if len(es) != 1 {
		t.Fatalf("emails = %+v", es)
	}
	e := es[0]
	if e.MessageID != "<abc@example.org>" || e.Sender != "alice@example.org" || e.To != "b@example.org; c@example.org" ||
		e.Cc != "d@example.org" || e.FileAs != "Hi" || e.SentOn != "2026-10-17T10:00:00+02:00" || string(e.Binary) != string(m.Raw) || e.Attachments != 1 {
		t.Errorf("email = %+v", e)
	}

	// Saved again: found by Message-ID, nothing uploaded.
	before := h.f.Binaries()
	again, err := h.c.SaveMessage(ctx, m)
	if err != nil || again.Created || again.GUID != s.GUID || h.f.Binaries() != before {
		t.Fatalf("again = %+v %v (%d binaries)", again, err, h.f.Binaries())
	}
}

// A duplicate by the server's hash that the Message-ID does not find is
// crmDuplicate, and the upload is dropped; without a sender or recipient
// none of the hash's fields goes.
func TestSaveMessageDuplicate(t *testing.T) {
	h := newHarness(t, "secret")
	ctx := context.Background()
	sent := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	h.f.AddEmail(ewaytest.Email{FileAs: "Hi", Sender: "a@example.org", To: "b@example.org", SentOn: sent.Format(time.RFC3339),
		Hash: "hi|a@example.org|b@example.org|" + strings.ToLower(sent.Format(time.RFC3339)), MessageID: "<private@x>"})
	_, err := h.c.SaveMessage(ctx, Message{Raw: []byte("x"), FileName: "x.eml", MessageID: "other@x", Subject: "Hi",
		Sender: "a@example.org", To: []string{"b@example.org"}, SentOn: sent})
	if code(err) != api.CodeCrmDuplicate {
		t.Fatalf("duplicate: %v", err)
	}
	if calls := h.f.Calls(); calls[len(calls)-1] != "DeleteEmail" {
		t.Errorf("the upload was not dropped: %v", calls)
	}
	s, err := h.c.SaveMessage(ctx, Message{Raw: []byte("y"), FileName: "y.eml", Subject: "No recipients", Sender: "a@example.org"})
	if err != nil || !s.Created {
		t.Fatalf("no recipients: %+v %v", s, err)
	}
}

// A dead session is signed in again exactly once; a second refusal is an
// error that does not carry the session id.
func TestSessionRenewedOnce(t *testing.T) {
	h := newHarness(t, "secret")
	ctx := context.Background()
	if _, err := h.c.ContactsByEmail(ctx, "a@example.org"); err != nil {
		t.Fatal(err)
	}
	h.f.ExpireSessions()
	if _, err := h.c.ContactsByEmail(ctx, "a@example.org"); err != nil || h.f.Logins() != 2 {
		t.Fatalf("renewed: %v, %d logins", err, h.f.Logins())
	}
	h.f.Override("SearchContacts", []byte(`{"ReturnCode":"rcBadSession","Description":"Invalid SID 'SECRETSID' supplied."}`))
	_, err := h.c.ContactsByEmail(ctx, "a@example.org")
	if code(err) != api.CodeServerError || h.f.Logins() != 3 {
		t.Fatalf("twice: %v, %d logins", err, h.f.Logins())
	}
	if strings.Contains(err.Error(), "SECRETSID") || strings.Contains(h.logs.String(), "SECRETSID") {
		t.Error("the description of rcBadSession leaked")
	}
}

// A refused sign-in latches: no further LogIn until the client is
// rebuilt, and nothing secret is logged.
func TestAuthFailedLatch(t *testing.T) {
	h := newHarness(t, "wrong")
	ctx := context.Background()
	if _, err := h.c.SignIn(ctx); code(err) != api.CodeAuthFailed {
		t.Fatalf("sign-in: %v", err)
	}
	n := len(h.f.Calls())
	if _, err := h.c.ContactsByEmail(ctx, "a@example.org"); code(err) != api.CodeAuthFailed || len(h.f.Calls()) != n {
		t.Fatalf("latched: %v, calls %v", err, h.f.Calls()[n:])
	}
	if st := h.c.State("crm"); st.Status != api.SyncAuthRequired {
		t.Errorf("state = %+v", st)
	}
	if h.c.AuthFailed() == nil {
		t.Error("no latch")
	}
}

func TestSignInPlan(t *testing.T) {
	h := newHarness(t, "secret")
	h.f.LoginCode = "rcLicenseExpired"
	_, err := h.c.SignIn(context.Background())
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.CodeCrmSignInUnsupported {
		t.Fatalf("plan: %v", err)
	}
	if d, ok := ae.Data.(api.CrmSignInUnsupportedData); !ok || d.Reason != api.CrmSignInPlan {
		t.Errorf("data = %#v", ae.Data)
	}
}

// SignIn reads the user's address and logs nothing of the user record.
func TestSignInUser(t *testing.T) {
	h := newHarness(t, "secret")
	u, err := h.c.SignIn(context.Background())
	if err != nil || u.Email != "user@example.org" || !ValidGUID(u.GUID) {
		t.Fatalf("user = %+v %v", u, err)
	}
	if strings.Contains(h.logs.String(), "never-log-me") || strings.Contains(h.logs.String(), "passwordHash") {
		t.Error("the log carries a secret")
	}
	h.c.LogOut(context.Background())
	if c := h.f.Calls(); c[len(c)-1] != "LogOut" {
		t.Errorf("calls = %v", c)
	}
}

func TestBlockedUserAgent(t *testing.T) {
	f := ewaytest.New()
	defer f.Close()
	c, err := NewClient(Options{ServiceURL: f.ServiceURL(), Username: "user", HTTP: f.Client(), UserAgent: "Go-http-client/1.1",
		Password: func(context.Context) (string, error) { return "secret", nil }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.SignIn(context.Background()); code(err) != api.CodeServerError || !strings.Contains(err.Error(), "blockedUserAgent") {
		t.Fatalf("blocked: %v", err)
	}
}

func TestRetryAfter(t *testing.T) {
	h := newHarness(t, "secret")
	h.f.Throttle(2)
	if _, err := h.c.ContactsByEmail(context.Background(), "a@example.org"); err != nil {
		t.Fatal(err)
	}
	if len(h.slept) != 2 || h.slept[0] != time.Second {
		t.Errorf("slept %v", h.slept)
	}
	h.f.Throttle(3)
	if _, err := h.c.ContactsByEmail(context.Background(), "b@example.org"); code(err) != api.CodeServerTimeout {
		t.Errorf("third throttle: %v", err)
	}
}

// Redirects: never for a POST, a GET only within the origin, never to
// http.
func TestRedirects(t *testing.T) {
	h := newHarness(t, "secret")
	ctx := context.Background()
	h.f.RedirectNext("https://evil.example/acme/API.svc/LogIn")
	if _, err := h.c.SignIn(ctx); code(err) != api.CodeServerError {
		t.Errorf("POST redirect: %v", err)
	}
	h.f.RedirectNext("https://evil.example/acme/API.svc/GetServiceAuthSettings")
	if _, err := h.c.DetectAuth(ctx); code(err) != api.CodeServerError {
		t.Errorf("cross-origin GET redirect: %v", err)
	}
	h.f.RedirectNext(strings.Replace(h.f.ServiceURL(), "https://", "http://", 1) + "/API.svc/GetServiceAuthSettings")
	if _, err := h.c.DetectAuth(ctx); code(err) != api.CodeServerError {
		t.Errorf("https → http: %v", err)
	}
	h.f.RedirectNext(h.f.ServiceURL() + "/API.svc/GetServiceAuthSettings")
	if s, err := h.c.DetectAuth(ctx); err != nil || !s.Known {
		t.Errorf("same-origin GET redirect: %+v %v", s, err)
	}
}

func TestOversizedAnswer(t *testing.T) {
	h := newHarness(t, "secret")
	big := append([]byte(`{"ReturnCode":"rcSuccess","Data":[{"ItemGUID":"0829cf36-a0db-4f90-9a3b-1c2d3e4f5a6b","FileAs":"`), bytes.Repeat([]byte("a"), maxJSONBytes)...)
	h.f.Override("SearchContacts", append(big, []byte(`"}]}`)...))
	if _, err := h.c.ContactsByEmail(context.Background(), "a@example.org"); code(err) != api.CodeServerError {
		t.Fatalf("oversized: %v", err)
	}
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "eway", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Hostile answers: names cleaned and capped, wrong GUIDs skipped, wrong
// types and shapes an error, never an empty success.
func TestHostileAnswers(t *testing.T) {
	h := newHarness(t, "secret")
	ctx := context.Background()
	h.f.Override("SearchContacts", fixture(t, "contacts_hostile.json"))
	items, err := h.c.ContactsByEmail(ctx, "a@example.org")
	if err != nil || len(items) != 3 {
		t.Fatalf("hostile: %d %v", len(items), err)
	}
	for _, it := range items {
		if !ValidGUID(it.GUID) || len(it.Name) > api.MaxCrmNameBytes || strings.ContainsAny(it.Name, "\r\n\x07\xe2\x80\xae") {
			t.Errorf("item %+v", it)
		}
		if strings.ContainsAny(it.Email, "<>\xe2\x80\xae") || it.CompanyGUID != "" {
			t.Errorf("email %q company %q", it.Email, it.CompanyGUID)
		}
	}
	for _, f := range []string{"wrong_types.json", "data_not_list.json", "no_return_code.json", "return_code_number.json", "not_json.txt"} {
		h.f.Override("SearchContacts", fixture(t, f))
		if _, err := h.c.ContactsByEmail(ctx, "a@example.org"); code(err) != api.CodeServerError {
			t.Errorf("%s: %v", f, err)
		}
	}
	h.f.Override("GetEmailsByItemGuids", fixture(t, "email_wrong_private.json"))
	if _, err := h.c.GetEmail(ctx, "0829cf36-a0db-4f90-9a3b-1c2d3e4f5a6b"); code(err) != api.CodeServerError {
		t.Errorf("wrong private: %v", err)
	}
	h.f.Override("GetEmailsByItemGuids", fixture(t, "email_relations_hostile.json"))
	e, err := h.c.GetEmail(ctx, "0829cf36-a0db-4f90-9a3b-1c2d3e4f5a6b")
	if err != nil || e == nil || e.Superior != nil || len(e.Relations) != 1 || e.Relations[0].Kind != "" || e.Relations[0].RelationData != "" {
		t.Fatalf("hostile relations: %+v %v", e, err)
	}
}

// Suggestions from addresses: contacts, companies by e-mail and tracked
// domain (not freemail), the company of a contact, the one open deal
// pre-checked as superior, a closed project left out.
func TestSuggest(t *testing.T) {
	h := newHarness(t, "secret")
	f := h.f
	acme := f.Add("Companies", ewaytest.Item{FileAs: "Acme", Email: "office@acme.example"})
	tracked := f.Add("Companies", ewaytest.Item{FileAs: "Tracked", TrackedDomains: "beta.example; beta-mail.example"})
	f.Add("Companies", ewaytest.Item{FileAs: "Substring only", TrackedDomains: "xbeta.example"})
	f.Add("Companies", ewaytest.Item{FileAs: "Gmail", TrackedDomains: "gmail.com"})
	jan := f.Add("Contacts", ewaytest.Item{FileAs: "Novák, Jan", Email2: "Jan@Acme.example", CompanyGUID: acme})
	deal := f.Add("Leads", ewaytest.Item{FileAs: "Big deal", Changed: time.Now()})
	closed := f.Add("Projects", ewaytest.Item{FileAs: "Old project", IsCompleted: true})
	f.Relate(jan, "Contacts", deal, "Leads", "GENERAL")
	f.Relate(acme, "Companies", closed, "Projects", "GENERAL")

	res, err := h.c.Suggest(context.Background(), []string{"jan@acme.example", "someone@beta.example", "x@gmail.com"}, 0)
	if err != nil || res.Partial {
		t.Fatalf("suggest: %+v %v", res, err)
	}
	got := map[string]Suggestion{}
	for _, s := range res.Items {
		got[s.GUID] = s
	}
	if s := got[jan]; s.Reason != api.CrmReasonAddress || s.Kind != api.CrmContact || s.Name != "Novák, Jan" {
		t.Errorf("contact %+v", s)
	}
	if s := got[acme]; s.Reason != api.CrmReasonContact {
		t.Errorf("company of the contact %+v", s)
	}
	if s := got[tracked]; s.Reason != api.CrmReasonDomain {
		t.Errorf("tracked domain %+v", s)
	}
	if s := got[deal]; !s.Superior || s.Kind != api.CrmDeal || s.Reason != api.CrmReasonContact {
		t.Errorf("deal %+v", s)
	}
	if _, ok := got[closed]; ok || len(got) != 4 {
		t.Errorf("suggestions %+v", res.Items)
	}
	if res.Items[0].Reason != api.CrmReasonAddress || res.Items[len(res.Items)-1].GUID != deal {
		t.Errorf("order %+v", res.Items)
	}
	n := len(f.Calls())
	if _, err := h.c.Suggest(context.Background(), []string{"jan@acme.example"}, 0); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.Calls()[n:] {
		if c == "SearchContacts" {
			t.Error("the address cache was not used")
		}
	}
}

func TestApplyLinks(t *testing.T) {
	h := newHarness(t, "secret")
	ctx := context.Background()
	f := h.f
	contact := f.Add("Contacts", ewaytest.Item{FileAs: "C"})
	company := f.Add("Companies", ewaytest.Item{FileAs: "Co"})
	deal := f.Add("Leads", ewaytest.Item{FileAs: "D"})
	project := f.Add("Projects", ewaytest.Item{FileAs: "P"})
	s, err := h.c.SaveMessage(ctx, Message{Raw: []byte("x"), FileName: "x.eml", Subject: "s", Sender: "a@x.example",
		To: []string{"b@x.example"}, SentOn: time.Now(), Superior: &Ref{Kind: api.CrmDeal, GUID: deal}})
	if err != nil {
		t.Fatal(err)
	}
	e, err := h.c.GetEmail(ctx, s.GUID)
	if err != nil || e.Superior == nil || e.Superior.GUID != deal {
		t.Fatalf("superior on save: %+v %v", e, err)
	}
	err = h.c.ApplyLinks(ctx, e, Links{Link: []Ref{{api.CrmContact, contact}, {api.CrmCompany, company}},
		Superior: &Ref{Kind: api.CrmProject, GUID: project}})
	if err != nil {
		t.Fatal(err)
	}
	e, _ = h.c.GetEmail(ctx, s.GUID)
	if e.Superior == nil || e.Superior.GUID != project || len(e.Relations) != 3 {
		t.Fatalf("after links: %+v", e)
	}
	for _, r := range e.Relations {
		if r.Kind == api.CrmProject && r.Type != RelationSuperior || r.Kind == api.CrmContact && r.Type != RelationGeneral {
			t.Errorf("relation %+v", r)
		}
	}
	// Linking again is idempotent; unlinking deletes by RelationDataGUID.
	if err := h.c.ApplyLinks(ctx, e, Links{Link: []Ref{{api.CrmContact, contact}}, Unlink: []Ref{{api.CrmCompany, company}, {api.CrmProject, project}}}); err != nil {
		t.Fatal(err)
	}
	e, _ = h.c.GetEmail(ctx, s.GUID)
	if len(e.Relations) != 1 || e.Relations[0].ForeignGUID != contact || e.Superior != nil {
		t.Fatalf("after unlink: %+v", e)
	}
	if err := h.c.CheckItems(ctx, []Ref{{api.CrmContact, contact}, {api.CrmContact, ewaytest.NewGUID()}}); code(err) != api.CodeCrmItemNotFound {
		t.Errorf("unknown item: %v", err)
	}
	if err := h.c.CheckItems(ctx, []Ref{{api.CrmContact, contact}, {api.CrmProject, project}}); err != nil {
		t.Errorf("known items: %v", err)
	}
	if err := h.c.SetPrivate(ctx, s.GUID, true); err != nil {
		t.Fatal(err)
	}
	if e, _ = h.c.GetEmail(ctx, s.GUID); !e.Private {
		t.Error("not private")
	}
	if err := h.c.Delete(ctx, s.GUID); err != nil {
		t.Fatal(err)
	}
	if e, err = h.c.GetEmail(ctx, s.GUID); e != nil || err != nil {
		t.Errorf("after delete: %+v %v", e, err)
	}
}

func TestSearchByName(t *testing.T) {
	h := newHarness(t, "secret")
	h.f.Add("Contacts", ewaytest.Item{FileAs: "Novák, Jan", Email1: "jan@acme.example"})
	h.f.Add("Leads", ewaytest.Item{FileAs: "Acme renewal"})
	items, err := h.c.SearchByName(context.Background(), api.CrmContact, "acme", 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("contacts by e-mail: %+v %v", items, err)
	}
	items, err = h.c.SearchByName(context.Background(), api.CrmDeal, "REN%_", 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("deals: %+v %v", items, err)
	}
}

func TestSupervisor(t *testing.T) {
	f := ewaytest.New()
	defer f.Close()
	s := NewSupervisor(SupervisorDeps{
		Password: func(context.Context, string) (string, error) { return "secret", nil },
		HTTP:     f.Client(), UserAgent: UserAgent("t"),
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	a := accountFor(f)
	s.Start(a)
	c, ok := s.Client(a.ID)
	if !ok {
		t.Fatal("no client")
	}
	if st, ok := s.State(a.ID); !ok || st.Status != api.SyncIdle || s.Trigger(a.ID, "", false) {
		t.Fatalf("state %+v", st)
	}
	if _, err := c.SignIn(context.Background()); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-done
	if calls := f.Calls(); calls[len(calls)-1] != "LogOut" {
		t.Errorf("not signed out: %v", calls)
	}
}

// FuzzDecode feeds arbitrary answers to the decoder: it must never panic
// and never yield an item without a GUID of the right shape.
func FuzzDecode(f *testing.F) {
	for _, name := range []string{"contacts_hostile.json", "wrong_types.json", "data_not_list.json", "no_return_code.json", "email_relations_hostile.json"} {
		if b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "eway", name)); err == nil && len(b) < 64<<10 {
			f.Add(b)
		}
	}
	f.Add([]byte(`{"ReturnCode":"rcSuccess","Data":null}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		env, err := decodeEnvelope(raw)
		if err != nil {
			return
		}
		items, err := decodeItems(env.Data)
		if err != nil {
			return
		}
		for _, w := range items {
			it := itemOf(api.CrmContact, w)
			if !ValidGUID(it.GUID) || len(it.Name) > api.MaxCrmNameBytes {
				t.Fatalf("item %+v", it)
			}
			_ = emailOf(w)
		}
	})
}

// A Message-ID is no proof: an item with the message's Message-ID but
// another sender or date is not found, and saving creates a new item
// instead of taking it over.
func TestForgedMessageID(t *testing.T) {
	h := newHarness(t, "secret")
	ctx := context.Background()
	sent := time.Date(2026, 10, 17, 10, 0, 30, 0, time.FixedZone("", 7200))
	theirs := h.f.AddEmail(ewaytest.Email{MessageID: "<victim@example.org>", Sender: "boss@example.org",
		SentOn: "2026-09-01T08:00:00+02:00"})
	if e, err := h.c.FindEmail(ctx, "victim@example.org", "attacker@example.net", sent); e != nil || err != nil {
		t.Fatalf("forged find = %+v %v", e, err)
	}
	s, err := h.c.SaveMessage(ctx, Message{Raw: []byte("x"), FileName: "x.eml", MessageID: "victim@example.org", Subject: "s",
		Sender: "attacker@example.net", To: []string{"me@example.org"}, SentOn: sent})
	if err != nil || !s.Created || s.GUID == strings.ToLower(theirs) {
		t.Fatalf("save over a forged id = %+v %v", s, err)
	}
	// The genuine message (same sender, same minute) is found.
	if e, err := h.c.FindEmail(ctx, "victim@example.org", "Attacker@Example.net", sent.Add(10*time.Second)); err != nil || e == nil || e.GUID != s.GUID {
		t.Fatalf("own find = %+v %v", e, err)
	}
}

func TestMatches(t *testing.T) {
	sent := time.Date(2026, 10, 17, 10, 0, 30, 0, time.FixedZone("", 7200))
	for on, want := range map[string]bool{
		"2026-10-17T10:00:00+02:00": true,
		"2026-10-17T08:00:59Z":      true,
		"2026-10-17T10:01:00+02:00": false,
		"2026-10-17 08:00:00":       true, // no zone: as UTC
		"2026-10-17T10:00:00":       true, // no zone: the date's own wall clock
		"2026-10-17 09:00:00":       false,
		"yesterday":                 false,
		"":                          false,
	} {
		e := &Email{Sender: "a@example.org", SentOn: on}
		if got := e.Matches("A@example.org", sent); got != want {
			t.Errorf("%q: %v", on, got)
		}
	}
	if (&Email{Sender: "b@example.org", SentOn: "2026-10-17T10:00:00+02:00"}).Matches("a@example.org", sent) {
		t.Error("another sender matched")
	}
}

// Unlinking the superior item deletes its relation and sets its foreign
// key to an explicit null.
func TestUnlinkSuperiorClearsTheKey(t *testing.T) {
	h := newHarness(t, "secret")
	ctx := context.Background()
	deal := h.f.Add("Leads", ewaytest.Item{FileAs: "D"})
	s, err := h.c.SaveMessage(ctx, Message{Raw: []byte("x"), FileName: "x.eml", Subject: "s", Sender: "a@x.example",
		To: []string{"b@x.example"}, SentOn: time.Now(), Superior: &Ref{Kind: api.CrmDeal, GUID: deal}})
	if err != nil {
		t.Fatal(err)
	}
	e, _ := h.c.GetEmail(ctx, s.GUID)
	if err := h.c.ApplyLinks(ctx, e, Links{Unlink: []Ref{{api.CrmDeal, deal}}}); err != nil {
		t.Fatal(err)
	}
	e, _ = h.c.GetEmail(ctx, s.GUID)
	if e.Superior != nil || len(e.Relations) != 0 || h.f.NullForeignKeys() != 1 {
		t.Fatalf("after unlink: %+v, %d null keys", e, h.f.NullForeignKeys())
	}
}

// A failed SaveEmail drops the upload.
func TestFailedSaveForgetsTheUpload(t *testing.T) {
	h := newHarness(t, "secret")
	h.f.Override("SaveEmail", []byte("not json"))
	_, err := h.c.SaveMessage(context.Background(), Message{Raw: []byte("x"), FileName: "x.eml", Subject: "s"})
	if code(err) != api.CodeServerError {
		t.Fatalf("save: %v", err)
	}
	if calls := h.f.Calls(); calls[len(calls)-1] != "DeleteEmail" {
		t.Errorf("calls = %v", calls)
	}
}

// A closed client signs in no more and logs its session out.
func TestClosedClient(t *testing.T) {
	h := newHarness(t, "secret")
	ctx := context.Background()
	if _, err := h.c.SignIn(ctx); err != nil {
		t.Fatal(err)
	}
	h.c.Close(ctx)
	if c := h.f.Calls(); c[len(c)-1] != "LogOut" || h.c.Context().Err() == nil {
		t.Fatalf("calls = %v", c)
	}
	n := h.f.Logins()
	if _, err := h.c.ContactsByEmail(ctx, "a@example.org"); code(err) != api.CodeUnavailable || h.f.Logins() != n {
		t.Errorf("after close: %v, %d logins", err, h.f.Logins())
	}
}

// A failure after the caller's context ended (a lookup's budget) does not
// mark the account offline.
func TestExpiredBudgetIsNotOffline(t *testing.T) {
	h := newHarness(t, "secret")
	if _, err := h.c.SignIn(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)
	if _, err := h.c.ContactsByEmail(ctx, "a@example.org"); err == nil {
		t.Fatal("no error after the deadline")
	}
	if st := h.c.State("crm"); st.Status != api.SyncIdle || st.Error != nil {
		t.Errorf("state = %+v", st)
	}
}

// Switching the superior item while the old one stays checked as a plain
// link keeps the old one linked (GENERAL): the key replacement drops its
// OUTLOOKPROJECT relation.
func TestSuperiorSwitchKeepsOldAsLink(t *testing.T) {
	h := newHarness(t, "secret")
	ctx := context.Background()
	oldDeal := h.f.Add("Leads", ewaytest.Item{FileAs: "Old"})
	newProject := h.f.Add("Projects", ewaytest.Item{FileAs: "New"})
	s, err := h.c.SaveMessage(ctx, Message{Raw: []byte("x"), FileName: "x.eml", Subject: "s", Sender: "a@x.example",
		To: []string{"b@x.example"}, SentOn: time.Now(), Superior: &Ref{Kind: api.CrmDeal, GUID: oldDeal}})
	if err != nil {
		t.Fatal(err)
	}
	e, _ := h.c.GetEmail(ctx, s.GUID)
	if err := h.c.ApplyLinks(ctx, e, Links{Link: []Ref{{api.CrmDeal, oldDeal}},
		Superior: &Ref{Kind: api.CrmProject, GUID: newProject}}); err != nil {
		t.Fatal(err)
	}
	e, _ = h.c.GetEmail(ctx, s.GUID)
	if e.Superior == nil || e.Superior.GUID != newProject {
		t.Fatalf("superior = %+v", e.Superior)
	}
	types := map[string]string{}
	for _, r := range e.Relations {
		types[r.ForeignGUID] = r.Type
	}
	if types[oldDeal] != RelationGeneral || types[newProject] != RelationSuperior || len(types) != 2 {
		t.Errorf("relations = %v", types)
	}
}
