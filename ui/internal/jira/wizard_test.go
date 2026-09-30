// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package jira

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestWizardTexts(t *testing.T) {
	s := WizardTexts(tr)
	v := reflect.ValueOf(s)
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).String() == "" {
			t.Errorf("WizardStrings.%s is empty", v.Type().Field(i).Name)
		}
	}
	if s.AddMenu != "Add _Jira Account…" || s.SpacesTitle != "Spaces" || s.Adding != "Adding the account" {
		t.Errorf("WizardTexts = %+v", s)
	}
	cs := catalog{{"jira", "Spaces"}: "Prostory", {"", "Spaces"}: "Mezery"}
	if got := WizardTexts(cs).SpacesTitle; got != "Prostory" {
		t.Errorf("SpacesTitle in Czech = %q", got)
	}
}

func TestCredentialFields(t *testing.T) {
	cloud := CredentialFields(api.JiraCloud, tr)
	want := CredentialPage{
		ShowsLogin: true, LoginLabel: "E-mail Address", TokenLabel: "API Token",
		Help:       "Create an API token for Malachi Mail in your Atlassian account, then paste it here.",
		HelpButton: "Create API Token…", HelpURL: "https://id.atlassian.com/manage-profile/security/api-tokens",
		TokenPrompt: "Enter the API token for this account", Rejected: "The Jira site rejected the token",
	}
	if cloud != want {
		t.Errorf("cloud = %+v", cloud)
	}
	dc := CredentialFields(api.JiraDataCenter, tr)
	if dc.ShowsLogin || dc.TokenLabel != "Personal Access Token" || dc.HelpURL != "" || dc.HelpButton != "" ||
		dc.Help != "Create a personal access token in your Jira profile, then paste it here." ||
		dc.TokenPrompt != "Enter the personal access token for this account" {
		t.Errorf("datacenter = %+v", dc)
	}
	if unknown := CredentialFields("server", tr); unknown != dc {
		t.Errorf("an unknown deployment = %+v, want the Data Center page", unknown)
	}
}

func TestNeedsEmail(t *testing.T) {
	tests := []struct {
		d    api.JiraDeployment
		user api.SiteUser
		want bool
	}{
		{api.JiraCloud, api.SiteUser{Name: "Jana"}, false},
		{api.JiraDataCenter, api.SiteUser{Name: "Jana"}, true},
		{api.JiraDataCenter, api.SiteUser{Name: "Jana", Email: " "}, true},
		{api.JiraDataCenter, api.SiteUser{Name: "Jana", Email: "jana@acme.example"}, false},
	}
	for _, tt := range tests {
		if got := NeedsEmail(tt.d, tt.user); got != tt.want {
			t.Errorf("NeedsEmail(%q, %+v) = %v", tt.d, tt.user, got)
		}
	}
}

func TestCheckSiteInput(t *testing.T) {
	const bad = "This is not a web address"
	tests := []struct {
		in      string
		ok      bool
		problem string
	}{
		{"", false, ""},
		{"   ", false, ""},
		{"acme.atlassian.net", true, ""},
		{" acme.atlassian.net ", true, ""},
		{"https://acme.atlassian.net", true, ""},
		{"HTTPS://acme.atlassian.net/", true, ""},
		{"http://jira.local:8080/jira", true, ""},
		{"jira", true, ""},
		{"jira.acme.example:8443/jira", true, ""},
		{"ftp://acme.atlassian.net", false, bad},
		{"javascript://acme.atlassian.net", false, bad},
		{"acme atlassian.net", false, bad},
		{"acme.atlassian.net\\x", false, bad},
		{"jana@acme.atlassian.net", false, bad},
		{"https://jana:secret@acme.atlassian.net", false, bad},
		{"https://", false, bad},
		{"https:///browse", false, bad},
		{"acme" + zwsp + ".atlassian.net", false, bad},
		{"https://[bad", false, bad},
		{"://acme.atlassian.net", false, bad},
	}
	for _, tt := range tests {
		ok, problem := CheckSiteInput(tt.in, tr)
		if ok != tt.ok || problem != tt.problem {
			t.Errorf("CheckSiteInput(%q) = %v, %q; want %v, %q", tt.in, ok, problem, tt.ok, tt.problem)
		}
	}
}

func TestDetected(t *testing.T) {
	tests := []struct {
		res  api.AccountDetectSiteResult
		want string
	}{
		{api.AccountDetectSiteResult{Deployment: api.JiraCloud, Title: "Acme", Version: "1001.0.0-SNAPSHOT"}, "Found Acme"},
		{api.AccountDetectSiteResult{Deployment: api.JiraCloud}, "Found Jira Cloud"},
		{api.AccountDetectSiteResult{Deployment: api.JiraDataCenter, Title: "Acme Jira", Version: "9.12.4"}, "Found Acme Jira, version 9.12.4"},
		{api.AccountDetectSiteResult{Deployment: api.JiraDataCenter, Version: "9.12.4"}, "Found Jira Data Center, version 9.12.4"},
		{api.AccountDetectSiteResult{Deployment: api.JiraDataCenter, Title: "Acme" + rlo + "\n"}, "Found Acme"},
		{api.AccountDetectSiteResult{Title: zwsp}, "Found Jira"},
	}
	for _, tt := range tests {
		if got := Detected(tt.res, tr); got != tt.want {
			t.Errorf("Detected(%+v) = %q, want %q", tt.res, got, tt.want)
		}
	}
}

func TestDefaultAccountName(t *testing.T) {
	tests := []struct {
		res  api.AccountDetectSiteResult
		want string
	}{
		{api.AccountDetectSiteResult{Title: " Acme Jira ", SiteURL: "https://acme.atlassian.net"}, "Acme Jira"},
		{api.AccountDetectSiteResult{SiteURL: "https://ACME.atlassian.net"}, "acme.atlassian.net"},
		{api.AccountDetectSiteResult{Title: rlo, SiteURL: "https://jira.acme.example:8443/jira"}, "jira.acme.example"},
		{api.AccountDetectSiteResult{SiteURL: "https://[bad"}, "Jira"},
		{api.AccountDetectSiteResult{}, "Jira"},
	}
	for _, tt := range tests {
		if got := DefaultAccountName(tt.res); got != tt.want {
			t.Errorf("DefaultAccountName(%+v) = %q, want %q", tt.res, got, tt.want)
		}
	}
	long := DefaultAccountName(api.AccountDetectSiteResult{Title: strings.Repeat("ř", 300)})
	if len(long) > maxNameBytes || len(long) < maxNameBytes-1 {
		t.Errorf("a long title makes a name of %d bytes", len(long))
	}
}

func TestSpaces(t *testing.T) {
	titles := map[string]api.Space{
		"ITSD – IT Service Desk": {Key: "ITSD", Name: "IT Service Desk"},
		"WEB":                    {Key: "WEB"},
		"Mobile":                 {Name: "Mobile"},
		"MOB – Mobile app":       {Key: " MOB ", Name: "Mobile\napp" + zwsp},
		"":                       {},
	}
	for want, s := range titles {
		if got := SpaceTitle(s); got != want {
			t.Errorf("SpaceTitle(%+v) = %q, want %q", s, got, want)
		}
	}
	counts := map[int]string{-1: "", 0: "about 0 issues", 1: "about 1 issue", 5: "about 5 issues"}
	for n, want := range counts {
		if got := ApproxCount(n, tr); got != want {
			t.Errorf("ApproxCount(%d) = %q, want %q", n, got, want)
		}
	}
	rows := SpaceRows([]api.Space{
		{ID: "10001", Key: "ITSD", Name: "IT Service Desk", ServiceDesk: true, Issues: 42},
		{ID: "10002", Key: "WEB", Name: "Website", Issues: -1},
	}, tr)
	want := []SpaceRow{
		{ID: "10001", Title: "ITSD – IT Service Desk", Count: "about 42 issues", ServiceDesk: true},
		{ID: "10002", Title: "WEB – Website"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("SpaceRows = %+v", rows)
	}
	if rows := SpaceRows(nil, tr); rows == nil || len(rows) != 0 {
		t.Errorf("SpaceRows(nil) = %#v, want an empty list", rows)
	}
	problems := map[int]string{
		-1: "Select at least one space", 0: "Select at least one space", 1: "", api.MaxJiraSpaces: "",
		api.MaxJiraSpaces + 1: fmt.Sprintf("Select at most %d spaces", api.MaxJiraSpaces),
	}
	for n, want := range problems {
		if got := SpacesProblem(n, tr); got != want {
			t.Errorf("SpacesProblem(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestOfflineChoices(t *testing.T) {
	labels := OfflineChoiceLabels(tr)
	if len(labels) != len(OfflineChoices) || labels[0] != "1 week" || labels[3] != "1 year" {
		t.Errorf("labels = %q", labels)
	}
	for _, d := range OfflineChoices {
		if d <= 0 || d > api.MaxJiraOfflineDays {
			t.Errorf("choice %d is outside 1..%d", d, api.MaxJiraOfflineDays)
		}
	}
	if OfflineChoices[IndexOfOfflineDays(0)] != api.DefaultJiraOfflineDays {
		t.Error("0 does not select the default window")
	}
	tests := map[int]int{-5: 1, 0: 1, 1: 0, 7: 0, 18: 0, 19: 1, 30: 1, 60: 1, 61: 2, 90: 2, 227: 2, 228: 3, 365: 3, 1000: 3}
	for days, want := range tests {
		if got := IndexOfOfflineDays(days); got != want {
			t.Errorf("IndexOfOfflineDays(%d) = %d, want %d", days, got, want)
		}
	}
}

func TestSetupConfig(t *testing.T) {
	spaces := []api.Space{
		{ID: "10001", Key: "ITSD", Name: "IT Service Desk", ServiceDesk: true, Issues: 42},
		{ID: "10002", Key: "WEB", Name: "Website", Issues: -1},
	}
	cloud := Setup{
		Site: api.AccountDetectSiteResult{
			Kind: api.AccountJira, SiteURL: "https://acme.atlassian.net", Deployment: api.JiraCloud,
			CloudID: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", Title: "Acme",
		},
		Login: " jana@acme.example ", Email: "ignored@acme.example", Spaces: spaces, OnlyMine: true, OfflineDays: 90,
	}
	want := api.AccountConfig{
		Name: "Acme", Email: "jana@acme.example", Kind: api.AccountJira,
		Jira: &api.JiraConfig{
			SiteURL: "https://acme.atlassian.net", Deployment: api.JiraCloud,
			CloudID: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", Login: "jana@acme.example",
			Spaces:   []api.SpaceRef{{ID: "10001", Key: "ITSD", Name: "IT Service Desk"}, {ID: "10002", Key: "WEB", Name: "Website"}},
			OnlyMine: true, OfflineDays: 90,
		},
	}
	if got := cloud.Config(); !reflect.DeepEqual(got, want) {
		t.Errorf("cloud Config =\n%+v %+v\nwant\n%+v %+v", got, got.Jira, want, want.Jira)
	}

	dc := Setup{
		Site:  api.AccountDetectSiteResult{SiteURL: "https://jira.acme.example/jira", Deployment: api.JiraDataCenter, CloudID: "stray"},
		Login: "jana", Email: " jana@acme.example ", Name: " Work Jira ", OfflineDays: 9999,
	}
	got := dc.Config()
	if got.Name != "Work Jira" || got.Email != "jana@acme.example" || got.Kind != api.AccountJira ||
		got.Jira.Login != "" || got.Jira.CloudID != "" || got.Jira.OfflineDays != api.MaxJiraOfflineDays ||
		got.Jira.Spaces == nil || len(got.Jira.Spaces) != 0 || got.IMAP != nil || got.SMTP != nil || got.Graph != nil {
		t.Errorf("datacenter Config = %+v %+v", got, got.Jira)
	}
	if got := (Setup{OfflineDays: -3}).Config(); got.Jira.OfflineDays != 0 || got.Name != "Jira" {
		t.Errorf("negative window = %d, name %q", got.Jira.OfflineDays, got.Name)
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		err  error
		want ErrorClass
	}{
		{nil, ErrOther},
		{errors.New("disconnected"), ErrOther},
		{api.NewError(api.CodeInvalidArgument, "bad url"), ErrInvalid},
		{api.NewError(api.CodeInvalidParams, "bad params"), ErrInvalid},
		{fmt.Errorf("detect: %w", api.NewError(api.CodeServerError, "not jira")), ErrServer},
		{api.NewError(api.CodeNetworkError, "refused"), ErrNetwork},
		{api.NewError(api.CodeOffline, "offline"), ErrNetwork},
		{api.NewError(api.CodeServerTimeout, "slow"), ErrNetwork},
		{api.NewError(api.CodeTLSError, "untrusted"), ErrTLS},
		{api.NewError(api.CodeAuthFailed, "401"), ErrAuthFailed},
		{api.NewError(api.CodeAuthRequired, "no token"), ErrAuthRequired},
		{api.NewError(api.CodeConflict, "exists"), ErrConflict},
		{api.NewError(api.CodeKeyringError, "locked"), ErrOther},
		{api.NewError(api.CodeNotImplemented, "old daemon"), ErrOther},
	}
	for _, tt := range tests {
		if got := Classify(tt.err); got != tt.want {
			t.Errorf("Classify(%v) = %d, want %d", tt.err, got, tt.want)
		}
	}
	if ClassOf(0) != ErrOther {
		t.Error("ClassOf(0)")
	}
}

func TestFailureOf(t *testing.T) {
	const (
		notJira  = "This address is not a Jira site"
		notWeb   = "This is not a web address"
		rejected = "The Jira site rejected the token"
		exists   = "An account for this Jira site already exists"
		lookUp   = "Looking up the Jira site"
		loading  = "Loading the spaces"
		adding   = "Adding the account"
		saving   = "Saving the account"
	)
	tests := []struct {
		name    string
		step    Step
		class   ErrorClass
		d       api.JiraDeployment
		editing bool
		want    Failure
	}{
		{"detect: not jira", StepDetect, ErrServer, "", false, Failure{PageSite, notJira, lookUp}},
		{"detect: bad address", StepDetect, ErrInvalid, "", false, Failure{PageSite, notWeb, lookUp}},
		{"detect: network", StepDetect, ErrNetwork, "", false, Failure{PageSite, "", lookUp}},
		{"detect: tls", StepDetect, ErrTLS, "", false, Failure{PageSite, "", lookUp}},
		{"detect: other", StepDetect, ErrOther, "", false, Failure{PageSite, "", lookUp}},
		{"spaces: rejected", StepSpaces, ErrAuthFailed, api.JiraCloud, false, Failure{PageCredentials, rejected, loading}},
		{"spaces: no token (cloud)", StepSpaces, ErrAuthRequired, api.JiraCloud, true, Failure{PageCredentials, "Enter the API token for this account", loading}},
		{"spaces: no token (dc)", StepSpaces, ErrAuthRequired, api.JiraDataCenter, true, Failure{PageCredentials, "Enter the personal access token for this account", loading}},
		{"spaces: server error is not 'not jira'", StepSpaces, ErrServer, api.JiraCloud, false, Failure{PageCredentials, "", loading}},
		{"spaces: network", StepSpaces, ErrNetwork, api.JiraCloud, false, Failure{PageCredentials, "", loading}},
		{"save: conflict", StepSave, ErrConflict, api.JiraCloud, false, Failure{PageSpaces, exists, adding}},
		{"save: rejected", StepSave, ErrAuthFailed, api.JiraCloud, false, Failure{PageCredentials, rejected, adding}},
		{"save: other", StepSave, ErrOther, api.JiraCloud, false, Failure{PageSpaces, "", adding}},
		{"edit: other", StepSave, ErrNetwork, api.JiraDataCenter, true, Failure{PageCredentials, "", saving}},
		{"edit: conflict", StepSave, ErrConflict, api.JiraDataCenter, true, Failure{PageCredentials, exists, saving}},
		{"edit: no token", StepSave, ErrAuthRequired, api.JiraDataCenter, true, Failure{PageCredentials, "Enter the personal access token for this account", saving}},
	}
	for _, tt := range tests {
		if got := FailureOf(tt.step, tt.class, tt.d, tt.editing, tr); got != tt.want {
			t.Errorf("%s: FailureOf = %+v, want %+v", tt.name, got, tt.want)
		}
	}
}
