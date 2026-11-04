// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package jira

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The assistant that adds a Jira account has three pages: the site
// (account.detectSite), the credentials (checked by account.listSpaces,
// which also lists the spaces) and the spaces with the offline window
// (account.add). Editing an account opens on the credentials page and
// saves with account.update.

// Page is a page of the Jira account assistant.
type Page int

// The pages, in order.
const (
	PageSite Page = iota
	PageCredentials
	PageSpaces
)

// Step is a call of the assistant that can fail.
type Step int

// The steps.
const (
	StepDetect Step = iota // account.detectSite, from the site page
	StepSpaces             // account.listSpaces, from the credentials page
	StepSave               // account.add, or account.update when editing
)

// TokenHelpURL is where a Jira Cloud user creates an API token. Data
// Center has no such page: its personal access tokens are made in the
// user's profile on the site.
const TokenHelpURL = "https://id.atlassian.com/manage-profile/security/api-tokens"

// SitePlaceholder is the example in the empty site address field; not
// translated.
const SitePlaceholder = "example.atlassian.net"

// maxNameBytes is the daemon's limit of AccountConfig.Name.
const maxNameBytes = 256

// WizardStrings are the fixed texts of the Jira account assistant.
type WizardStrings struct {
	// AddMenu is the menu item (with a mnemonic) and Title the window's
	// title.
	AddMenu, Title string
	// The site page: its title, the explanation, the field and the
	// progress while the site is looked up.
	SiteTitle, SiteDescription, SiteAddress, LookingUp string
	// The spaces page: the progress while the spaces load, the title, the
	// explanation, the text of an empty list.
	LoadingSpaces, SpacesTitle, SpacesDescription, NoSpaces string
	// The switch that keeps only the user's issues, with its subtitle.
	OnlyMine, OnlyMineSubtitle string
	// The offline window's row, with its subtitle.
	KeepOffline, KeepOfflineSubtitle string
	// Adding and Saving are the progress of account.add and
	// account.update.
	Adding, Saving string
}

// WizardTexts returns the fixed texts, translated.
func WizardTexts(tr Translator) WizardStrings {
	return WizardStrings{
		// TRANSLATORS: menu item; "Jira" is a product name.
		AddMenu: tr.T("Add _Jira Account…"),
		Title:   tr.T("Add Jira Account"),
		// TRANSLATORS: title of the page that asks for the address of a Jira installation.
		SiteTitle:       tr.T("Jira Site"),
		SiteDescription: tr.T("Enter the address of your Jira site. Malachi Mail finds out whether it runs in the cloud or in your company's data center."),
		SiteAddress:     tr.T("Site Address"),
		LookingUp:       tr.T("Looking up the Jira site"),
		LoadingSpaces:   tr.T("Loading the spaces"),
		// TRANSLATORS: Jira projects, which Jira calls spaces.
		SpacesTitle:       tr.C("jira", "Spaces"),
		SpacesDescription: tr.T("Choose the spaces whose issues appear as folders."),
		NoSpaces:          tr.T("No spaces are visible to this account"),
		OnlyMine:          tr.T("Only Issues Involving Me"),
		OnlyMineSubtitle:  tr.T("Assigned to you, reported by you or watched by you"),
		KeepOffline:       tr.T("Keep Issues Offline For"),
		// TRANSLATORS: subtitle of "Keep Issues Offline For".
		KeepOfflineSubtitle: tr.T("Older issues stay on the site and are not shown"),
		Adding:              tr.T("Adding the account"),
		Saving:              tr.T("Saving the account"),
	}
}

// CredentialPage is how the credentials page asks for the sign-in of a
// deployment: Jira Cloud wants the Atlassian account's e-mail address and
// an API token, Data Center a personal access token alone.
type CredentialPage struct {
	// ShowsLogin says whether the e-mail field is shown; LoginLabel is its
	// label.
	ShowsLogin bool
	LoginLabel string
	// TokenLabel labels the secret field; Help explains where the token
	// comes from.
	TokenLabel, Help string
	// HelpButton opens HelpURL; both "" when the deployment has no such
	// page.
	HelpButton, HelpURL string
	// TokenPrompt is the page's banner when a token is needed and none is
	// stored (editing an account whose token is missing).
	TokenPrompt string
	// Rejected is the page's banner when the site refused the token.
	Rejected string
}

// CredentialFields returns the credentials page of a deployment; an
// unknown one is treated as Data Center.
func CredentialFields(d api.JiraDeployment, tr Translator) CredentialPage {
	p := CredentialPage{
		LoginLabel: tr.T("E-mail Address"),
		Rejected:   tr.T("The Jira site rejected the token"),
	}
	if d == api.JiraCloud {
		p.ShowsLogin = true
		p.TokenLabel = tr.T("API Token")
		p.Help = tr.T("Create an API token for Malachi Mail in your Atlassian account, then paste it here.")
		// TRANSLATORS: button that opens id.atlassian.com in the browser.
		p.HelpButton = tr.T("Create API Token…")
		p.HelpURL = TokenHelpURL
		p.TokenPrompt = tr.T("Enter the API token for this account")
		return p
	}
	p.TokenLabel = tr.T("Personal Access Token")
	p.Help = tr.T("Create a personal access token in your Jira profile, then paste it here.")
	p.TokenPrompt = tr.T("Enter the personal access token for this account")
	return p
}

// NeedsEmail reports a Data Center sign-in whose user the site does not
// give an e-mail address: the assistant asks for it (the account's
// address). A Jira Cloud account's address is its login.
func NeedsEmail(d api.JiraDeployment, user api.SiteUser) bool {
	return d != api.JiraCloud && strings.TrimSpace(user.Email) == ""
}

// CheckSiteInput checks what the user typed as the site's address before
// account.detectSite: a host ("acme.atlassian.net") or an http(s) URL. ok
// enables Next; problem is the text under the field, "" while the field
// is empty or fine. The daemon checks the address again.
func CheckSiteInput(raw string, tr Translator) (ok bool, problem string) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return false, ""
	}
	bad := func() (bool, string) {
		return false, tr.T("This is not a web address")
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\\' {
			return bad()
		}
	}
	if i := strings.Index(s, "://"); i >= 0 {
		if scheme := strings.ToLower(s[:i]); scheme != "https" && scheme != "http" {
			return bad()
		}
	} else {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || u.User != nil || u.Hostname() == "" {
		return bad()
	}
	return true, ""
}

// Detected is the text under the site field once account.detectSite
// answered: the site's title (its deployment's name when it has none),
// and for Data Center its version.
func Detected(res api.AccountDetectSiteResult, tr Translator) string {
	title := Clean(res.Title)
	if title == "" {
		title = DeploymentName(res.Deployment)
	}
	if v := Clean(res.Version); v != "" && res.Deployment == api.JiraDataCenter {
		// TRANSLATORS: the first %s is the name of a Jira site, the second its version ("9.12.4").
		return fmt.Sprintf(tr.T("Found %s, version %s"), title, v)
	}
	// TRANSLATORS: %s is the name of a Jira site.
	return fmt.Sprintf(tr.T("Found %s"), title)
}

// DefaultAccountName is the name of a new account: the site's title,
// else its host, else "Jira".
func DefaultAccountName(res api.AccountDetectSiteResult) string {
	name := Clean(res.Title)
	if name == "" {
		if u, err := url.Parse(strings.TrimSpace(res.SiteURL)); err == nil {
			name = Clean(strings.ToLower(u.Hostname()))
		}
	}
	if name == "" {
		return "Jira"
	}
	return truncate(name, maxNameBytes)
}

// SpaceTitle is a space as the spaces page lists it: "KEY – Name", or
// whichever of the two it has.
func SpaceTitle(s api.Space) string {
	key, name := Clean(s.Key), Clean(s.Name)
	switch {
	case key == "":
		return name
	case name == "":
		return key
	}
	return key + " – " + name
}

// ApproxCount is the estimate of a space's issues in the offline window
// (Space.Issues); "" when the daemon did not count (-1).
func ApproxCount(n int, tr Translator) string {
	if n < 0 {
		return ""
	}
	// TRANSLATORS: an estimate of the issues of a Jira space.
	return fmt.Sprintf(tr.N("about %d issue", "about %d issues", n), n)
}

// SpaceRow is one space of the spaces page: a check box with Title and
// the estimate Count ("" when not counted).
type SpaceRow struct {
	ID, Title, Count string
	ServiceDesk      bool
}

// SpaceRows lists the spaces of account.listSpaces for the spaces page, in
// the daemon's order.
func SpaceRows(spaces []api.Space, tr Translator) []SpaceRow {
	rows := make([]SpaceRow, 0, len(spaces))
	for _, s := range spaces {
		rows = append(rows, SpaceRow{
			ID:          s.ID,
			Title:       SpaceTitle(s),
			Count:       ApproxCount(s.Issues, tr),
			ServiceDesk: s.ServiceDesk,
		})
	}
	return rows
}

// SpacesProblem is the reason the spaces page cannot add the account with
// selected spaces chosen; "" when it can.
func SpacesProblem(selected int, tr Translator) string {
	if selected <= 0 {
		return tr.T("Select at least one space")
	}
	if selected > api.MaxJiraSpaces {
		return fmt.Sprintf(tr.N("Select at most %d space", "Select at most %d spaces", api.MaxJiraSpaces), api.MaxJiraSpaces)
	}
	return ""
}

// OfflineChoices are the offline windows the assistant offers, in days:
// 1 week, 1 month, 3 months, 1 year. There is no "Everything": a Jira
// account keeps at most api.MaxJiraOfflineDays.
var OfflineChoices = []int{7, 30, 90, 365}

// OfflineChoiceLabels are the labels of OfflineChoices, in order.
func OfflineChoiceLabels(tr Translator) []string {
	return []string{tr.T("1 week"), tr.T("1 month"), tr.T("3 months"), tr.T("1 year")}
}

// IndexOfOfflineDays is the position in OfflineChoices shown for
// JiraConfig.OfflineDays: 0 (or less) is the default window
// (api.DefaultJiraOfflineDays), any other value the nearest choice, a tie
// going to the shorter.
func IndexOfOfflineDays(days int) int {
	if days <= 0 {
		days = api.DefaultJiraOfflineDays
	}
	best, bestDiff := 0, -1
	for i, v := range OfflineChoices {
		diff := v - days
		if diff < 0 {
			diff = -diff
		}
		if bestDiff < 0 || diff < bestDiff {
			best, bestDiff = i, diff
		}
	}
	return best
}

// Setup is what the assistant collected for a new account.
type Setup struct {
	Site api.AccountDetectSiteResult
	// Login is the Atlassian account's e-mail address (Jira Cloud), which
	// is also the account's address.
	Login string
	// Email is the account's address on Data Center: the signed-in user's
	// (account.listSpaces), or what the user typed when the site hides it.
	Email string
	// Name is the account's name; "" = DefaultAccountName.
	Name string
	// Spaces are the chosen spaces, in the order shown.
	Spaces   []api.Space
	OnlyMine bool
	// OfflineDays is the offline window; 0 = api.DefaultJiraOfflineDays.
	OfflineDays int
}

// Config is the AccountConfig of account.add (and of account.listSpaces,
// which accepts it without spaces).
func (s Setup) Config() api.AccountConfig {
	jc := &api.JiraConfig{
		SiteURL:     s.Site.SiteURL,
		Deployment:  s.Site.Deployment,
		CloudID:     s.Site.CloudID,
		Spaces:      make([]api.SpaceRef, 0, len(s.Spaces)),
		OnlyMine:    s.OnlyMine,
		OfflineDays: min(max(s.OfflineDays, 0), api.MaxJiraOfflineDays),
	}
	for _, sp := range s.Spaces {
		jc.Spaces = append(jc.Spaces, api.SpaceRef{ID: sp.ID, Key: sp.Key, Name: sp.Name})
	}
	email := strings.TrimSpace(s.Email)
	if s.Site.Deployment == api.JiraCloud {
		jc.Login = strings.TrimSpace(s.Login)
		email = jc.Login
	} else {
		jc.CloudID = ""
	}
	name := truncate(strings.TrimSpace(s.Name), maxNameBytes)
	if name == "" {
		name = DefaultAccountName(s.Site)
	}
	return api.AccountConfig{Name: name, Email: email, Kind: api.AccountJira, Jira: jc}
}

// ErrorClass sorts the errors of the assistant's calls by what the user
// can do about them.
type ErrorClass int

// The classes.
const (
	ErrOther        ErrorClass = iota // anything else, also no reply (disconnected, timed out)
	ErrInvalid                        // invalidArgument: the daemon refused the input
	ErrServer                         // serverError: the site answered, but not as Jira does
	ErrNetwork                        // offline, networkError, serverTimeout
	ErrTLS                            // tlsError
	ErrAuthFailed                     // authFailed: the site refused the token
	ErrAuthRequired                   // authRequired: no token typed and none stored
	ErrConflict                       // conflict: an account for this site and address exists
)

// ClassOf is the class of an API error code; 0 (no API error) is ErrOther.
func ClassOf(code api.ErrorCode) ErrorClass {
	switch code {
	case api.CodeInvalidArgument, api.CodeInvalidParams:
		return ErrInvalid
	case api.CodeServerError:
		return ErrServer
	case api.CodeOffline, api.CodeNetworkError, api.CodeServerTimeout:
		return ErrNetwork
	case api.CodeTLSError:
		return ErrTLS
	case api.CodeAuthFailed:
		return ErrAuthFailed
	case api.CodeAuthRequired:
		return ErrAuthRequired
	case api.CodeConflict:
		return ErrConflict
	}
	return ErrOther
}

// Classify is ClassOf for an error of the RPC client.
func Classify(err error) ErrorClass {
	var e *api.Error
	if errors.As(err, &e) {
		return ClassOf(e.Code)
	}
	return ErrOther
}

// Failure is what the assistant does after a failed step: it shows Page
// with Banner. An empty Banner means the client's general sentence for
// the error (widget.RPCErrorText in GTK) with What, the step's action.
type Failure struct {
	Page   Page
	Banner string
	What   string
}

// FailureOf decides a failed step of the assistant for a deployment;
// editing is true when the assistant edits an existing account (it saves
// from the credentials page).
func FailureOf(step Step, class ErrorClass, d api.JiraDeployment, editing bool, tr Translator) Failure {
	creds := CredentialFields(d, tr)
	switch step {
	case StepDetect:
		f := Failure{Page: PageSite, What: tr.T("Looking up the Jira site")}
		switch class {
		case ErrServer:
			f.Banner = tr.T("This address is not a Jira site")
		case ErrInvalid:
			f.Banner = tr.T("This is not a web address")
		}
		return f
	case StepSpaces:
		f := Failure{Page: PageCredentials, What: tr.T("Loading the spaces")}
		switch class {
		case ErrAuthFailed:
			f.Banner = creds.Rejected
		case ErrAuthRequired:
			f.Banner = creds.TokenPrompt
		}
		return f
	}
	f := Failure{Page: PageSpaces, What: tr.T("Adding the account")}
	if editing {
		f = Failure{Page: PageCredentials, What: tr.T("Saving the account")}
	}
	switch class {
	case ErrConflict:
		f.Banner = tr.T("An account for this Jira site already exists")
	case ErrAuthFailed:
		f.Page, f.Banner = PageCredentials, creds.Rejected
	case ErrAuthRequired:
		f.Page, f.Banner = PageCredentials, creds.TokenPrompt
	}
	return f
}
