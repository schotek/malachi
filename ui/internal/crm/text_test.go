// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package crm

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestContextMenuLabel(t *testing.T) {
	tr := msgids{}
	if got := SaveMenuLabel(1, tr); got != "Save to eWay-CRM…" {
		t.Errorf("one %q", got)
	}
	if got := SaveMenuLabel(0, tr); got != "Save to eWay-CRM…" {
		t.Errorf("none %q", got)
	}
	if got := SaveMenuLabel(3, tr); got != "Save 3 Messages to eWay-CRM…" {
		t.Errorf("three %q", got)
	}
}

func TestDialogTexts(t *testing.T) {
	tr := msgids{}
	s := DialogTexts(false, tr)
	want := DialogStrings{
		Title: "Save to eWay-CRM", Search: "Search eWay-CRM", SuperiorItem: "Superior Item", Private: "Private",
		PrivateHint: "Only you will see this message in eWay-CRM.",
		Empty:       "Nothing in eWay-CRM matches these addresses.",
		Partial:     "eWay-CRM did not answer in time; some suggestions may be missing.",
		SearchHint:  "Deals and projects are added by searching above",
		Save:        "_Save",
	}
	if s != want {
		t.Errorf("got  %+v\nwant %+v", s, want)
	}
	if DialogTexts(true, tr).Title != "Edit Links" {
		t.Error("edit title")
	}
	var titles []string
	for _, k := range Kinds {
		titles = append(titles, GroupTitle(k, tr))
	}
	if strings.Join(titles, " · ") != "Contacts · Companies · Deals · Projects" || GroupTitle("lead", tr) != "" {
		t.Errorf("groups %v", titles)
	}
}

func TestBar(t *testing.T) {
	tr := msgids{}
	if _, ok := BarFor(nil, tr); ok {
		t.Error("bar without a record")
	}
	rec := &api.CrmRecord{
		AccountID: "e1", ItemGUID: guid(100), URL: "https://open.eway-crm.com/?l=e",
		Items: []api.CrmItem{
			{Kind: api.CrmDeal, GUID: guid(5), Name: "Fleet renewal", Superior: true, URL: "https://open.eway-crm.com/?l=5"},
			{Kind: api.CrmContact, GUID: guid(1), Name: "Novák,\u200b Jan", URL: "http://evil.example/"},
			{Kind: api.CrmCompany, GUID: guid(2), Name: "Acme"},
			{Kind: api.CrmCompany, GUID: guid(3), Name: " \u202e "},
			{Kind: "lead", GUID: guid(4), Name: "unknown"},
		},
	}
	b, ok := BarFor(rec, tr)
	if !ok {
		t.Fatal("bar")
	}
	want := Bar{
		Title: "Saved in eWay-CRM", URL: "https://open.eway-crm.com/?l=e", OpenLabel: "Open in eWay-CRM",
		Names: "Novák, Jan, Acme, Fleet renewal",
		Items: []BarItem{
			{Kind: api.CrmContact, Name: "Novák, Jan"},
			{Kind: api.CrmCompany, Name: "Acme"},
			{Kind: api.CrmDeal, Name: "Fleet renewal", Superior: true, URL: "https://open.eway-crm.com/?l=5"},
		},
	}
	if !reflect.DeepEqual(b, want) {
		t.Errorf("got  %+v\nwant %+v", b, want)
	}
	rec.Private = true
	rec.Items = nil
	for i := 0; i < 40; i++ {
		rec.Items = append(rec.Items, api.CrmItem{Kind: api.CrmContact, GUID: guid(i), Name: strings.Repeat("n", 30)})
	}
	b, _ = BarFor(rec, tr)
	if b.Note != "Only you will see this message in eWay-CRM." || len(b.Names) > api.MaxCrmDetailBytes || !strings.HasSuffix(b.Names, "…") || len(b.Items) != 40 {
		t.Errorf("private, long: %q (%d) %q", b.Note, len(b.Names), b.Names[len(b.Names)-8:])
	}
}

func TestPill(t *testing.T) {
	if _, ok := PillFor(api.MessageSummary{}, msgids{}); ok {
		t.Error("pill of an unsaved message")
	}
	if p, ok := PillFor(api.MessageSummary{CrmSaved: true}, msgids{}); !ok || p != (Pill{Label: "eWay", Tooltip: "Saved in eWay-CRM"}) {
		t.Errorf("pill %+v", p)
	}
}

func TestSaveToast(t *testing.T) {
	tr := msgids{}
	dup := &api.Error{Code: api.CodeCrmDuplicate}
	net := &api.Error{Code: api.CodeNetworkError}
	res := func(errs ...*api.Error) api.CrmSaveResult {
		var r api.CrmSaveResult
		for i, e := range errs {
			o := api.CrmSaveOutcome{MessageID: api.MessageID(rune('a' + i)), Error: e}
			if e == nil {
				o.Record = &api.CrmRecord{}
			}
			r.Results = append(r.Results, o)
		}
		return r
	}
	cases := []struct {
		name string
		res  api.CrmSaveResult
		text string
		err  *api.Error
	}{
		{"one", res(nil), "Saved to eWay-CRM", nil},
		{"three", res(nil, nil, nil), "3 messages saved to eWay-CRM", nil},
		{"one duplicate", res(dup), "eWay-CRM already has this message.", nil},
		{"one network error", res(net), "", net},
		{"some failed", res(nil, dup, net), "2 messages were not saved to eWay-CRM.", nil},
		{"one of two failed", res(nil, net), "1 message was not saved to eWay-CRM.", nil},
	}
	for _, c := range cases {
		s := Summarize(c.res)
		text, err := SaveToast(s, tr)
		if text != c.text || err != c.err {
			t.Errorf("%s: %q %v, want %q %v", c.name, text, err, c.text, c.err)
		}
	}
	if s := Summarize(res(nil, dup, net)); s.Saved != 1 || s.Failed != 2 || s.Err != dup {
		t.Errorf("summary %+v", s)
	}
	if RemovedToast(tr) != "Removed from eWay-CRM" {
		t.Error("removed")
	}
}

func TestRemoveQuestion(t *testing.T) {
	q := RemoveQuestion("Offer\u202e  for 2027", msgids{})
	want := Question{
		Heading: "Remove from eWay-CRM?",
		Body:    "“Offer for 2027” will be deleted from eWay-CRM with its links, for everyone who uses it. The message in Malachi Mail stays.",
		Confirm: "_Remove",
	}
	if q != want {
		t.Errorf("got  %+v\nwant %+v", q, want)
	}
	if q := RemoveQuestion(" ", msgids{}); !strings.HasPrefix(q.Body, "“(No subject)”") {
		t.Errorf("no subject %q", q.Body)
	}
	if q := RemoveQuestion(strings.Repeat("s", 500), msgids{}); len(q.Body) > 400 {
		t.Errorf("long subject %d", len(q.Body))
	}
}

func TestErrorText(t *testing.T) {
	tr := msgids{}
	cases := []struct {
		err  error
		want string
	}{
		{&api.Error{Code: api.CodeCrmSignInUnsupported, Data: map[string]any{"reason": "microsoft"}},
			"This eWay-CRM signs in with a Microsoft account, which Malachi Mail cannot do yet."},
		{&api.Error{Code: api.CodeCrmSignInUnsupported, Data: map[string]any{"reason": "plan"}},
			"This eWay-CRM does not allow access through its API."},
		{&api.Error{Code: api.CodeCrmSignInUnsupported}, ""},
		{&api.Error{Code: api.CodeCrmDuplicate}, "eWay-CRM already has this message."},
		{&api.Error{Code: api.CodeCrmItemNotFound}, "eWay-CRM no longer has this item."},
		{&api.Error{Code: api.CodePartNotDownloaded}, ""},
		{&api.Error{Code: api.CodeAuthFailed}, ""},
		{context.DeadlineExceeded, ""},
		{nil, ""},
	}
	for _, c := range cases {
		if got := ErrorText(c.err, tr); got != c.want {
			t.Errorf("%v: %q, want %q", c.err, got, c.want)
		}
	}
	wrapped := errors.Join(errors.New("rpc"), &api.Error{Code: api.CodeCrmDuplicate})
	if ErrorText(wrapped, tr) == "" {
		t.Error("a wrapped error")
	}
	acts := map[Action]string{
		ActionSave: "Saving to eWay-CRM", ActionLookup: "Contacting eWay-CRM", ActionSearch: "Searching eWay-CRM",
		ActionRemove: "Removing from eWay-CRM", ActionDownload: "Downloading the message",
	}
	for a, w := range acts {
		if got := ActionText(a, tr); got != w {
			t.Errorf("action %d: %q", a, got)
		}
	}
	if DownloadingText(1, tr) != "Downloading the message first…" || DownloadingText(4, tr) != "Downloading 4 messages first…" {
		t.Error(DownloadingText(1, tr), DownloadingText(4, tr))
	}
}

func TestCheckServiceInput(t *testing.T) {
	tr := msgids{}
	cases := []struct {
		in      string
		ok      bool
		problem string
	}{
		{"", false, ""},
		{"   ", false, ""},
		{"hosting.eway-crm.com/acme", true, ""},
		{"https://hosting.eway-crm.com/acme/API.svc", true, ""},
		{"HTTPS://crm.example.org", true, ""},
		{"http://crm.example.org", false, "Malachi Mail connects to eWay-CRM over https only."},
		{"HTTP://crm.example.org", false, "Malachi Mail connects to eWay-CRM over https only."},
		{"ftp://crm.example.org", false, "This is not a web address"},
		{"crm example.org", false, "This is not a web address"},
		{"https://user@crm.example.org", false, "This is not a web address"},
		{"https://", false, "This is not a web address"},
		{"crm.example.org/\u202e", false, "This is not a web address"},
	}
	for _, c := range cases {
		ok, problem := CheckServiceInput(c.in, tr)
		if ok != c.ok || problem != c.problem {
			t.Errorf("%q: %v %q, want %v %q", c.in, ok, problem, c.ok, c.problem)
		}
	}
}

func TestFailureOf(t *testing.T) {
	tr := msgids{}
	ms := &api.Error{Code: api.CodeCrmSignInUnsupported, Data: map[string]any{"reason": "microsoft"}}
	plan := &api.Error{Code: api.CodeCrmSignInUnsupported, Data: map[string]any{"reason": "plan"}}
	cases := []struct {
		name    string
		step    WizardStep
		err     error
		editing bool
		want    Failure
	}{
		{"microsoft at detect", StepDetect, ms, false,
			Failure{PageService, "This eWay-CRM signs in with a Microsoft account, which Malachi Mail cannot do yet.", "Contacting eWay-CRM", FieldService}},
		{"microsoft at add", StepSave, ms, false,
			Failure{PageService, "This eWay-CRM signs in with a Microsoft account, which Malachi Mail cannot do yet.", "Adding the account", FieldService}},
		{"microsoft when editing", StepTest, ms, true,
			Failure{PageCredentials, "This eWay-CRM signs in with a Microsoft account, which Malachi Mail cannot do yet.", "Signing in", FieldNone}},
		{"plan at test", StepTest, plan, false,
			Failure{PageCredentials, "This eWay-CRM does not allow access through its API.", "Signing in", FieldNone}},
		{"wrong password", StepTest, &api.Error{Code: api.CodeAuthFailed}, false,
			Failure{PageCredentials, "The server rejected the user name or password", "Signing in", FieldPassword}},
		{"no password", StepTest, &api.Error{Code: api.CodeAuthRequired}, true,
			Failure{PageCredentials, "Enter the password for this account", "Signing in", FieldPassword}},
		{"not an address", StepDetect, &api.Error{Code: api.CodeInvalidArgument}, false,
			Failure{PageService, "This is not a web address", "Contacting eWay-CRM", FieldService}},
		{"not eWay-CRM", StepDetect, &api.Error{Code: api.CodeServerError}, false,
			Failure{PageService, "", "Contacting eWay-CRM", FieldService}},
		{"offline at detect", StepDetect, &api.Error{Code: api.CodeNetworkError}, false,
			Failure{PageService, "", "Contacting eWay-CRM", FieldNone}},
		{"conflict at add", StepSave, &api.Error{Code: api.CodeConflict}, false,
			Failure{PageCredentials, "", "Adding the account", FieldNone}},
		{"timeout at update", StepSave, context.DeadlineExceeded, true,
			Failure{PageCredentials, "", "Saving the account", FieldNone}},
	}
	for _, c := range cases {
		if got := FailureOf(c.step, c.err, c.editing, tr); got != c.want {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, got, c.want)
		}
	}
	w := WizardTexts(tr)
	if w.Title != "Add eWay-CRM Account" || w.ServiceLabel != "Web Service Address" || w.UserLabel != "User Name" ||
		w.PasswordLabel != "Password" || w.Contacting != "Contacting eWay-CRM" || w.Save != "_Save" {
		t.Errorf("texts %+v", w)
	}
}
