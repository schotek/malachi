// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package crm

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/jira"
)

// Every text of the package, so that the template and the ports see them
// in one place (docs/eway.md D20). "eWay-CRM" and "eWay" are brands and
// stay as they are in every language.

// Button is the CRM button of an open message.
type Button struct {
	State ButtonState
	// Label and Tooltip are "" when hidden.
	Label, Tooltip string
	// Enabled: it can be pressed (not while Saving).
	Enabled bool
	// Menu: the button opens SavedMenu instead of the dialog.
	Menu bool
}

// ButtonFor is the button of state.
func ButtonFor(state ButtonState, tr Translator) Button {
	b := Button{State: state}
	switch state {
	case ButtonNotSaved:
		// TRANSLATORS: the button of a message that saves it to eWay-CRM (a CRM; a brand name).
		b.Label = tr.T("Save to eWay-CRM")
		b.Tooltip, b.Enabled = b.Label, true
	case ButtonSaving:
		b.Label = tr.T("Save to eWay-CRM")
		// TRANSLATORS: tooltip of the button while the message is being saved to eWay-CRM.
		b.Tooltip = tr.T("Saving to eWay-CRM…")
	case ButtonSaved:
		// TRANSLATORS: the button of a message saved in eWay-CRM; it opens a menu.
		b.Label = tr.T("In eWay-CRM")
		b.Tooltip, b.Enabled, b.Menu = savedText(tr), true, true
	}
	return b
}

// savedText is the tooltip of the saved message's button and pill, and
// the bar's title.
func savedText(tr Translator) string {
	// TRANSLATORS: the message is saved in eWay-CRM (pill tooltip, bar title).
	return tr.T("Saved in eWay-CRM")
}

// MenuLabel is the label of an item of SavedMenu.
func MenuLabel(id MenuID, tr Translator) string {
	switch id {
	case MenuOpen:
		// TRANSLATORS: opens the saved message (or an item) in eWay-CRM in the browser.
		return tr.T("Open in eWay-CRM")
	case MenuEditLinks:
		// TRANSLATORS: menu item: change which contacts, companies, deals and projects the saved message is linked to.
		return tr.T("Edit Links…")
	case MenuRemove:
		return tr.T("Remove from eWay-CRM")
	}
	return ""
}

// SavedMenu is the menu of a saved message: Open in eWay-CRM, Edit
// Links…, Remove from eWay-CRM.
func SavedMenu(tr Translator) []MenuItem {
	return []MenuItem{
		{ID: MenuOpen, Label: MenuLabel(MenuOpen, tr)},
		{ID: MenuEditLinks, Label: MenuLabel(MenuEditLinks, tr)},
		{ID: MenuRemove, Label: MenuLabel(MenuRemove, tr), Destructive: true},
	}
}

// SaveMenuLabel is the context menu's Save item for count selected rows.
func SaveMenuLabel(count int, tr Translator) string {
	if count > 1 {
		// TRANSLATORS: context menu of several selected messages; %d is their number.
		return fmt.Sprintf(tr.N("Save %d Message to eWay-CRM…", "Save %d Messages to eWay-CRM…", count), count)
	}
	// TRANSLATORS: context menu of a message: opens the dialog that saves it to eWay-CRM.
	return tr.T("Save to eWay-CRM…")
}

// DialogStrings are the fixed texts of the dialog.
type DialogStrings struct {
	// Title: Save to eWay-CRM, or Edit Links over a saved message.
	Title string
	// Search is the search field's placeholder.
	Search string
	// SuperiorItem labels the marker of the superior deal or project.
	SuperiorItem string
	// Private is the check box; PrivateHint explains it.
	Private, PrivateHint string
	// Empty is shown when nothing is listed; Partial when the CRM did
	// not answer every lookup in time.
	Empty, Partial string
	// SearchHint is the quiet line under the groups when no deal or
	// project is listed: they are found by the search field
	// (Dialog.OffersSearchHint).
	SearchHint string
	// Save is the default button (with its mnemonic).
	Save string
}

// DialogTexts are the dialog's texts; editing as Dialog.Editing.
func DialogTexts(editing bool, tr Translator) DialogStrings {
	s := DialogStrings{
		Title: tr.T("Save to eWay-CRM"),
		// TRANSLATORS: placeholder of the search field of the Save to eWay-CRM dialog.
		Search: tr.T("Search eWay-CRM"),
		// TRANSLATORS: marks the one deal or project the message belongs under in eWay-CRM.
		SuperiorItem: tr.T("Superior Item"),
		// TRANSLATORS: check box: the saved message is visible to its owner only in eWay-CRM.
		Private:     tr.C("crm", "Private"),
		PrivateHint: tr.T("Only you will see this message in eWay-CRM."),
		Empty:       tr.T("Nothing in eWay-CRM matches these addresses."),
		Partial:     tr.T("eWay-CRM did not answer in time; some suggestions may be missing."),
		// TRANSLATORS: under the groups of the Save to eWay-CRM dialog when no deal or project is listed.
		SearchHint: tr.T("Deals and projects are added by searching above"),
		Save:       tr.T("_Save"),
	}
	if editing {
		// TRANSLATORS: title of the dialog that changes a saved message's links in eWay-CRM.
		s.Title = tr.T("Edit Links")
	}
	return s
}

// GroupTitle is the heading of a kind's group in the dialog.
func GroupTitle(k api.CrmItemKind, tr Translator) string {
	switch k {
	case api.CrmContact:
		// TRANSLATORS: eWay-CRM contacts (people).
		return tr.C("crm", "Contacts")
	case api.CrmCompany:
		// TRANSLATORS: eWay-CRM companies.
		return tr.C("crm", "Companies")
	case api.CrmDeal:
		// TRANSLATORS: eWay-CRM deals (sales opportunities, "Leads" in its API).
		return tr.C("crm", "Deals")
	case api.CrmProject:
		// TRANSLATORS: eWay-CRM projects.
		return tr.C("crm", "Projects")
	}
	return ""
}

// BarItem is an item a saved message is linked to, as the bar shows it.
type BarItem struct {
	Kind     api.CrmItemKind
	Name     string
	Superior bool
	// URL opens the item (OpenURL); "" when there is none.
	URL string
}

// Bar is the strip under a saved message's header: plain texts only.
type Bar struct {
	Title string
	// Items are the linked items in Kinds' order; Names their names
	// joined for one line (capped at api.MaxCrmDetailBytes), "" when the
	// message is linked to nothing.
	Items []BarItem
	Names string
	// URL opens the saved e-mail item; OpenLabel is its button.
	URL, OpenLabel string
	// Note is PrivateHint when the saved item is private, else "".
	Note string
}

// BarFor is the bar of the message's record (RecordOf); false when there
// is none.
func BarFor(rec *api.CrmRecord, tr Translator) (Bar, bool) {
	if rec == nil {
		return Bar{}, false
	}
	b := Bar{Title: savedText(tr), URL: OpenURL(rec.URL), OpenLabel: MenuLabel(MenuOpen, tr)}
	var names []string
	for _, k := range Kinds {
		for _, it := range rec.Items {
			ref, ok := refOf(it.Kind, it.GUID)
			if !ok || ref.Kind != k {
				continue
			}
			c := cleanItem(it, ref)
			if c.Name == "" {
				continue
			}
			b.Items = append(b.Items, BarItem{Kind: k, Name: c.Name, Superior: it.Superior, URL: c.URL})
			names = append(names, c.Name)
		}
	}
	b.Names = capBytes(strings.Join(names, ", "), api.MaxCrmDetailBytes)
	if rec.Private {
		b.Note = tr.T("Only you will see this message in eWay-CRM.")
	}
	return b, true
}

// Pill is the list pill of a saved message.
type Pill struct {
	Label, Tooltip string
}

// PillFor is the pill of a list row; false when the message is not saved.
func PillFor(s api.MessageSummary, tr Translator) (Pill, bool) {
	if !s.CrmSaved {
		return Pill{}, false
	}
	return Pill{Label: PillLabel, Tooltip: savedText(tr)}, true
}

// SaveSummary counts the outcomes of crm.save.
type SaveSummary struct {
	Saved, Failed int
	// Err is the first message's error; nil when none failed.
	Err *api.Error
}

// Summarize counts res.
func Summarize(res api.CrmSaveResult) SaveSummary {
	var s SaveSummary
	for _, o := range res.Results {
		switch {
		case o.Error != nil:
			s.Failed++
			if s.Err == nil {
				s.Err = o.Error
			}
		default:
			s.Saved++
		}
	}
	return s
}

// SaveToast is the toast after crm.save: Saved to eWay-CRM, the number
// saved, or what failed. When text is "" and err is not nil, the client
// shows its general sentence for err (widget.RPCErrorText with
// ActionText(ActionSave)).
func SaveToast(s SaveSummary, tr Translator) (text string, err *api.Error) {
	switch {
	case s.Failed == 0 && s.Saved <= 1:
		// TRANSLATORS: toast after one message was saved to eWay-CRM.
		return tr.T("Saved to eWay-CRM"), nil
	case s.Failed == 0:
		// TRANSLATORS: toast after several messages were saved to eWay-CRM; %d is their number.
		return fmt.Sprintf(tr.N("%d message saved to eWay-CRM", "%d messages saved to eWay-CRM", s.Saved), s.Saved), nil
	case s.Saved+s.Failed == 1:
		if t := ErrorText(s.Err, tr); t != "" {
			return t, nil
		}
		return "", s.Err
	}
	// TRANSLATORS: toast after saving several messages to eWay-CRM, some of which failed; %d is the number that failed.
	return fmt.Sprintf(tr.N("%d message was not saved to eWay-CRM.", "%d messages were not saved to eWay-CRM.", s.Failed), s.Failed), nil
}

// RemovedToast is the toast after crm.remove.
func RemovedToast(tr Translator) string {
	// TRANSLATORS: toast after a message was removed from eWay-CRM.
	return tr.T("Removed from eWay-CRM")
}

// Question is a confirmation: its heading, its text and the label of the
// destructive button that goes ahead.
type Question struct {
	Heading, Body, Confirm string
}

// maxSubjectBytes caps the subject quoted in RemoveQuestion.
const maxSubjectBytes = 200

// RemoveQuestion asks before crm.remove deletes the message's item from
// eWay-CRM, for every user of the CRM (docs/eway.md D18). subject is the
// message's subject (untrusted; cleaned and capped here).
func RemoveQuestion(subject string, tr Translator) Question {
	s := capBytes(jira.Clean(subject), maxSubjectBytes)
	if s == "" {
		s = tr.T("(No subject)")
	}
	return Question{
		// TRANSLATORS: heading of the question before a saved message is deleted from eWay-CRM.
		Heading: tr.T("Remove from eWay-CRM?"),
		// TRANSLATORS: %s is the message's subject.
		Body:    fmt.Sprintf(tr.T("“%s” will be deleted from eWay-CRM with its links, for everyone who uses it. The message in Malachi Mail stays."), s),
		Confirm: tr.T("_Remove"),
	}
}

// ErrorText is the sentence for an error of the CRM's own (docs/eway.md
// D14): sign-in unsupported (microsoft, plan), a duplicate, an item gone.
// "" for any other error: the client shows its general sentence with an
// ActionText (and on partNotDownloaded downloads first, NotDownloaded).
func ErrorText(err error, tr Translator) string {
	if reason, ok := SignInReason(err); ok {
		return signInText(reason, tr)
	}
	e := apiError(err)
	if e == nil {
		return ""
	}
	switch e.Code {
	case api.CodeCrmDuplicate:
		// TRANSLATORS: eWay-CRM refused the message as a duplicate of another saved item.
		return tr.T("eWay-CRM already has this message.")
	case api.CodeCrmItemNotFound:
		// TRANSLATORS: a contact, company, deal or project (or the saved message) was deleted in eWay-CRM meanwhile.
		return tr.T("eWay-CRM no longer has this item.")
	}
	return ""
}

// signInText is the sentence of crmSignInUnsupported's reason.
func signInText(reason api.CrmSignInReason, tr Translator) string {
	if reason == api.CrmSignInPlan {
		return tr.T("This eWay-CRM does not allow access through its API.")
	}
	return tr.T("This eWay-CRM signs in with a Microsoft account, which Malachi Mail cannot do yet.")
}

// Action is a CRM call, for the client's general error sentence.
type Action int

// The actions.
const (
	ActionSave Action = iota
	ActionLookup
	ActionSearch
	ActionRemove
	ActionDownload
)

// ActionText is the action in progressive form, the "%s" of the client's
// general error sentence ("%s failed: …").
func ActionText(a Action, tr Translator) string {
	switch a {
	case ActionLookup:
		return contacting(tr)
	case ActionSearch:
		// TRANSLATORS: progressive form for an error text ("Searching eWay-CRM failed: …").
		return tr.T("Searching eWay-CRM")
	case ActionRemove:
		// TRANSLATORS: progressive form for an error text ("Removing from eWay-CRM failed: …").
		return tr.T("Removing from eWay-CRM")
	case ActionDownload:
		// TRANSLATORS: progressive form for an error text ("Downloading the message failed: …").
		return tr.T("Downloading the message")
	}
	// TRANSLATORS: progressive form for an error text ("Saving to eWay-CRM failed: …").
	return tr.T("Saving to eWay-CRM")
}

// contacting is the progress of account.detectEway and crm.lookup.
func contacting(tr Translator) string {
	// TRANSLATORS: progress, and progressive form for an error text ("Contacting eWay-CRM failed: …").
	return tr.T("Contacting eWay-CRM")
}

// DownloadingText is the status line while the n messages crm.save
// named (NotDownloaded) are downloaded before it is called again.
func DownloadingText(n int, tr Translator) string {
	if n <= 1 {
		// TRANSLATORS: status line: a message is downloaded before it is saved to eWay-CRM.
		return tr.T("Downloading the message first…")
	}
	// TRANSLATORS: status line: messages are downloaded before they are saved to eWay-CRM; %d is their number.
	return fmt.Sprintf(tr.N("Downloading %d message first…", "Downloading %d messages first…", n), n)
}

// The account assistant (ui/internal/accountwizard eway_flow.go).

// WizardPage is a page of the assistant.
type WizardPage int

// The pages. Editing an account opens on PageCredentials (the service
// address stays the account's).
const (
	PageService WizardPage = iota
	PageCredentials
)

// WizardStrings are the assistant's fixed texts.
type WizardStrings struct {
	// Title adds, EditTitle edits.
	Title, EditTitle string
	// CredentialsTitle is the title of the credentials page.
	CredentialsTitle string
	// ServiceLabel and ServiceHint are the address field's label and the
	// line under it.
	ServiceLabel, ServiceHint string
	NameLabel, UserLabel      string
	// PasswordLabel adds; NewPasswordLabel edits (empty keeps it).
	PasswordLabel, NewPasswordLabel string
	// PasswordPrompt is the credentials page's banner when a password
	// is needed.
	PasswordPrompt string
	// Next, Add and Save are the pages' buttons (with mnemonics).
	Next, Add, Save string
	// Progress texts of the calls (also the actions of their errors).
	Contacting, SigningIn, Adding, Saving string
	// FollowThreads labels the sign-in page's check box of
	// EwayConfig.FollowThreads (docs/eway.md D22); FollowThreadsHint is
	// the line under it.
	FollowThreads, FollowThreadsHint string
}

// WizardTexts are the assistant's texts.
func WizardTexts(tr Translator) WizardStrings {
	return WizardStrings{
		// TRANSLATORS: title of the assistant that adds an eWay-CRM account.
		Title:            tr.T("Add eWay-CRM Account"),
		EditTitle:        tr.T("Edit Account"),
		CredentialsTitle: tr.T("Sign In"),
		// TRANSLATORS: the address of the eWay-CRM web service, the one its Outlook add-in uses.
		ServiceLabel: tr.T("Web Service Address"),
		// TRANSLATORS: under the address field of the eWay-CRM assistant; where the address is found.
		ServiceHint:      tr.T("The address of your eWay-CRM, such as https://hosting.eway-crm.com/company. In the web application, point at your company's name in your account panel to see it; in Outlook it is under eWay-CRM → Settings → Connection."),
		NameLabel:        tr.T("Account Name"),
		UserLabel:        tr.T("User Name"),
		PasswordLabel:    tr.T("Password"),
		NewPasswordLabel: tr.T("New Password (leave empty to keep)"),
		PasswordPrompt:   tr.T("Enter the password for this account"),
		Next:             tr.T("_Next"),
		Add:              tr.T("_Add Account"),
		Save:             tr.T("_Save"),
		Contacting:       contacting(tr),
		// TRANSLATORS: progressive form for the RPC error text
		SigningIn: tr.T("Signing in"),
		Adding:    tr.T("Adding the account"),
		Saving:    tr.T("Saving the account"),
		// TRANSLATORS: check box of the eWay-CRM account: once a message is saved, the later messages of its conversation are saved too.
		FollowThreads: tr.T("Also save later messages of saved conversations"),
		// TRANSLATORS: the line under the check box "Also save later messages of saved conversations".
		FollowThreadsHint: tr.T("Replies and new messages in a conversation you saved are saved to eWay-CRM with the same links."),
	}
}

// CheckServiceInput checks the address field as typed: ok when it can be
// looked up, else the problem to show under it ("" while it is empty).
// https is assumed when no scheme is typed; http is refused here, before
// anything is asked (docs/eway.md D2).
func CheckServiceInput(raw string, tr Translator) (ok bool, problem string) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return false, ""
	}
	bad := func() (bool, string) { return false, tr.T("This is not a web address") }
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\\' {
			return bad()
		}
	}
	if i := strings.Index(s, "://"); i >= 0 {
		switch strings.ToLower(s[:i]) {
		case "https":
		case "http":
			return false, httpOnly(tr)
		default:
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

// httpOnly says that http is not used.
func httpOnly(tr Translator) string {
	return tr.T("Malachi Mail connects to eWay-CRM over https only.")
}

// WizardStep is a call of the assistant.
type WizardStep int

// The steps.
const (
	// StepDetect is account.detectEway.
	StepDetect WizardStep = iota
	// StepTest is account.test (one sign-in).
	StepTest
	// StepSave is account.add, or account.update when editing.
	StepSave
)

// WizardField is a field the assistant flags and focuses.
type WizardField int

// The fields.
const (
	FieldNone WizardField = iota
	FieldService
	FieldUser
	FieldPassword
)

// Failure is what the assistant does after a failed step: it shows Page
// with Banner and flags and focuses Field. An empty Banner means the
// client's general sentence for the error with What, the step's action.
type Failure struct {
	Page   WizardPage
	Banner string
	What   string
	Field  WizardField
}

// FailureOf decides a failed step; editing as the assistant edits an
// existing account (it has no service page). A Microsoft sign-in is told
// on the service page before any password (crmSignInUnsupported
// microsoft), a refused password flags the password field with the
// wizard's existing sentence.
func FailureOf(step WizardStep, err error, editing bool, tr Translator) Failure {
	page := PageCredentials
	if step == StepDetect && !editing {
		page = PageService
	}
	f := Failure{Page: page}
	switch step {
	case StepDetect:
		f.What = contacting(tr)
	case StepTest:
		f.What = tr.T("Signing in")
	default:
		f.What = tr.T("Adding the account")
		if editing {
			f.What = tr.T("Saving the account")
		}
	}
	if reason, ok := SignInReason(err); ok {
		f.Banner = signInText(reason, tr)
		if reason == api.CrmSignInMicrosoft && !editing {
			f.Page, f.Field = PageService, FieldService
		}
		return f
	}
	e := apiError(err)
	if e == nil {
		return f
	}
	switch e.Code {
	case api.CodeAuthFailed:
		f.Page, f.Field = PageCredentials, FieldPassword
		f.Banner = tr.T("The server rejected the user name or password")
	case api.CodeAuthRequired:
		f.Page, f.Field = PageCredentials, FieldPassword
		f.Banner = tr.T("Enter the password for this account")
	case api.CodeInvalidArgument, api.CodeServerError:
		if f.Page == PageService {
			f.Field = FieldService
			if e.Code == api.CodeInvalidArgument {
				f.Banner = tr.T("This is not a web address")
			}
		}
	}
	return f
}
