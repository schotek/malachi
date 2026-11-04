// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package crm is the view logic of saving mail to eWay-CRM (account kind
// eway, capability crm; docs/eway.md, docs/api.md §4.17), in all three
// clients: which eWay-CRM account a client uses (Available), which
// messages can be saved (CanSave, OfferFor), the state of the message's
// button and its menu (ButtonStateFor, ButtonFor, SavedMenu), the model of
// the dialog that picks the contacts, companies, deals and projects a
// message is linked to (Dialog), the bar under a saved message's header
// and the list pill (BarFor, PillFor), the confirmation before removal,
// the toasts and error texts, and the texts of the account assistant
// (WizardTexts, CheckServiceInput, FailureOf; the flow itself is
// ui/internal/accountwizard eway_flow.go).
//
// The daemon does every CRM call; this package only turns its API values
// into small view models and texts. It is pure (no GTK, no gettext, no
// cgo: the caller passes a Translator), every msgid is in text.go, and it
// is the reference ported one to one, tests included, to macOS
// (macos/Sources/MalachiCore/CRM/) and Windows
// (windows/src/Malachi.Core/Crm/).
//
// Every name and detail of a CRM item is untrusted display text: the
// daemon cleans and caps it, and this package cleans and caps it again
// (jira.Clean); the clients show it as plain text only. Every URL here
// (api.CrmItem.URL, api.CrmRecord.URL) is built by the daemon from the
// account's stored service URL and a GUID of fixed shape, never taken from
// the CRM or from mail, so a client opens it in the browser without the
// "Open this link?" question (docs/eway.md D15); OpenURL only refuses
// anything that is not https.
//
// One eWay-CRM account per installation (docs/eway.md D11, §5 Q12): the
// first enabled one in account.list order; the dialog has no account
// popup.
package crm

import (
	"net/url"
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/jira"
)

// Translator translates a msgid of the malachi domain (jira.Translator: T
// a plain one, N a plural form for n, C one with a context). GTK passes
// i18n.Tr.
type Translator = jira.Translator

// Brand is the service's name in texts; a brand, never translated.
const Brand = "eWay-CRM"

// PillLabel is the text of the list pill of a saved message; a brand,
// never translated.
const PillLabel = "eWay"

// KindBadge is the capsule of an eWay-CRM account's kind beside its name
// (Preferences → Integrations; window/model.go accountHeaderBadge), as
// jira.KindBadge is "JIRA": a brand, never translated.
const KindBadge = "EWAY"

// MaxMessages is the most messages one save takes (api.MaxCrmMessages).
const MaxMessages = api.MaxCrmMessages

// Icon is the GTK symbolic icon of eWay-CRM (Adwaita has no CRM icon);
// the other clients map it to their own.
const Icon = "x-office-address-book-symbolic"

// Accounts are the enabled eWay-CRM accounts, in the order of accounts
// (account.list order).
func Accounts(accounts []api.Account) []api.Account {
	var out []api.Account
	for _, a := range accounts {
		if a.Enabled && a.Config.Protocol() == api.AccountEway {
			out = append(out, a)
		}
	}
	return out
}

// Available is the eWay-CRM account the client saves to: the first
// enabled one in account.list order (docs/eway.md D11). false when there
// is none; then every CRM control is hidden.
func Available(accounts []api.Account) (*api.Account, bool) {
	list := Accounts(accounts)
	if len(list) == 0 {
		return nil, false
	}
	a := list[0]
	return &a, true
}

// IsMailAccount reports an account whose messages can be saved: a mail
// account (kind imap, the empty kind, or graph). Never an issue tracker's
// synthesised messages, never a calendar or CRM account.
func IsMailAccount(a api.Account) bool {
	switch a.Config.Protocol() {
	case api.AccountIMAP, api.AccountGraph:
		return true
	}
	return false
}

// CanSave reports whether one message can be saved to eWay-CRM
// (docs/eway.md D6): a message of a mail account, not in the Outbox or a
// Drafts folder (role outbox or drafts), and not a local draft. Junk is
// allowed. The daemon refuses the same with invalidArgument.
func CanSave(messageAccount api.Account, folderRole api.FolderRole, localDraft bool) bool {
	if !IsMailAccount(messageAccount) || localDraft {
		return false
	}
	switch folderRole {
	case api.RoleOutbox, api.RoleDrafts:
		return false
	}
	return true
}

// Row is what the rules need of one selected row (a message, or a folded
// conversation's members in the folder standing for it).
type Row struct {
	// Account is the message's account.
	Account api.Account
	// Role is the role of the message's folder.
	Role api.FolderRole
	// LocalDraft: the row is a local draft (a draft card, a draft opened
	// in the compose window), not a message.
	LocalDraft bool
	// Saved: the message is saved in eWay-CRM (IsSaved).
	Saved bool
	// Conversation: the row is a conversation's row (folded or not), which
	// stands for its members in the folder rather than for one message.
	// Such a row offers nothing (owner's decision 2026-10-17): the user
	// unfolds it and saves the members, one or several.
	Conversation bool
}

// Offer is what the button, the context menu and the multi-selection
// offer: one rule for all three.
type Offer struct {
	// Shown: the CRM item exists for the selection (else hidden).
	Shown bool
	// Enabled: it can run now.
	Enabled bool
	// Count is the number of rows (the label of several).
	Count int
	// Saved: one row, saved; the saved-state items replace Save.
	Saved bool
}

// OfferFor is the Offer for the selected rows (one account: the selection
// never spans two, ui/internal/selection Restrict). Nothing when no
// eWay-CRM account is enabled or nothing is selected. One row: shown and
// enabled when it CanSave. Several: shown when their account is a mail
// account, enabled when every row CanSave and there are at most
// MaxMessages; several rows are always offered as Save, saved or not (the
// daemon finds a saved one and only adds its links).
func OfferFor(accounts []api.Account, rows []Row) Offer {
	if _, ok := Available(accounts); !ok || len(rows) == 0 {
		return Offer{}
	}
	for _, r := range rows {
		if r.Conversation {
			return Offer{}
		}
	}
	if len(rows) == 1 {
		r := rows[0]
		if !CanSave(r.Account, r.Role, r.LocalDraft) {
			return Offer{}
		}
		return Offer{Shown: true, Enabled: true, Count: 1, Saved: r.Saved}
	}
	o := Offer{Shown: IsMailAccount(rows[0].Account), Count: len(rows)}
	if !o.Shown {
		return Offer{}
	}
	o.Enabled = len(rows) <= MaxMessages
	for _, r := range rows {
		if !CanSave(r.Account, r.Role, r.LocalDraft) {
			o.Enabled = false
		}
	}
	return o
}

// RecordOf is the message's record in eWay-CRM account crm (Message.Crm,
// from message.get); crm "" takes the first record. nil when the message
// is not saved there.
func RecordOf(m *api.Message, crm api.AccountID) *api.CrmRecord {
	if m == nil {
		return nil
	}
	for i := range m.Crm {
		if crm == "" || m.Crm[i].AccountID == crm {
			r := m.Crm[i]
			return &r
		}
	}
	return nil
}

// IsSaved reports whether the message is saved in eWay-CRM account crm:
// the loaded message's record when m is the message s (message.get is the
// newer word), else the list's MessageSummary.CrmSaved (which already
// counts only enabled CRM accounts).
func IsSaved(s api.MessageSummary, m *api.Message, crm api.AccountID) bool {
	if m != nil && m.ID == s.ID && m.AccountID == s.AccountID {
		return RecordOf(m, crm) != nil
	}
	return s.CrmSaved
}

// Key names a message of a mail account for InFlight.
type Key struct {
	Account api.AccountID
	Message api.MessageID
}

// InFlight is the set of messages a crm.save or crm.remove runs for, so
// that their button reads Saving and a second save is not started (the
// daemon would answer conflict). Not safe for concurrent use: the clients
// touch it on their main thread. The zero value is empty and ready.
type InFlight struct {
	keys map[Key]int
}

// Begin marks the messages ids of account as busy; End releases them
// (counted, so overlapping calls nest).
func (f *InFlight) Begin(account api.AccountID, ids []api.MessageID) {
	if f.keys == nil {
		f.keys = map[Key]int{}
	}
	for _, id := range ids {
		f.keys[Key{account, id}]++
	}
}

// End releases what Begin marked.
func (f *InFlight) End(account api.AccountID, ids []api.MessageID) {
	for _, id := range ids {
		k := Key{account, id}
		if f.keys[k] <= 1 {
			delete(f.keys, k)
		} else {
			f.keys[k]--
		}
	}
}

// Has reports whether a call runs for the message.
func (f *InFlight) Has(account api.AccountID, id api.MessageID) bool {
	return f.keys[Key{account, id}] > 0
}

// Any reports whether a call runs for any of the messages.
func (f *InFlight) Any(account api.AccountID, ids []api.MessageID) bool {
	for _, id := range ids {
		if f.Has(account, id) {
			return true
		}
	}
	return false
}

// ButtonState is the state of the CRM button on an open message.
type ButtonState int

// The states (docs/eway.md D20, the approved wireframe).
const (
	// ButtonHidden: no eWay-CRM account, or the message cannot be saved.
	ButtonHidden ButtonState = iota
	// ButtonNotSaved: "Save to eWay-CRM", which opens the dialog.
	ButtonNotSaved
	// ButtonSaving: a save or removal runs; the button is off.
	ButtonSaving
	// ButtonSaved: "In eWay-CRM", which opens SavedMenu.
	ButtonSaved
)

// ButtonStateFor is the button of the open message r: hidden unless the
// message can be saved (OfferFor of the one row), Saving while busy (a
// call of InFlight runs for it), else Saved or NotSaved by r.Saved.
func ButtonStateFor(accounts []api.Account, r Row, busy bool) ButtonState {
	o := OfferFor(accounts, []Row{r})
	switch {
	case !o.Shown:
		return ButtonHidden
	case busy:
		return ButtonSaving
	case o.Saved:
		return ButtonSaved
	}
	return ButtonNotSaved
}

// MenuID names an item of the saved message's menu (the button's menu,
// and the context menu of one saved message).
type MenuID string

// The items, in this order.
const (
	// MenuOpen opens the saved e-mail item (api.CrmRecord.URL).
	MenuOpen MenuID = "open"
	// MenuEditLinks opens the dialog over the saved message (Dialog in
	// its editing mode).
	MenuEditLinks MenuID = "editLinks"
	// MenuRemove asks RemoveQuestion, then calls crm.remove.
	MenuRemove MenuID = "remove"
)

// MenuItem is an entry of SavedMenu.
type MenuItem struct {
	ID    MenuID
	Label string
	// Destructive: the client marks it as such (it deletes the item from
	// the CRM for every user, after a question).
	Destructive bool
}

// NotDownloaded is the messages crm.save needs downloaded first: the
// data of its partNotDownloaded (docs/eway.md D6). The client runs
// message.download for each, shows DownloadingText meanwhile, and calls
// crm.save again. false for any other error.
func NotDownloaded(err error) ([]api.MessageID, bool) {
	e := apiError(err)
	if e == nil || e.Code != api.CodePartNotDownloaded {
		return nil, false
	}
	var d api.MessagesNotDownloadedData
	if !decodeData(e.Data, &d) || len(d.MessageIDs) == 0 {
		return nil, false
	}
	seen := map[api.MessageID]bool{}
	var out []api.MessageID
	for _, id := range d.MessageIDs {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, len(out) > 0
}

// SignInReason is why the instance cannot be signed in to: the data of
// crmSignInUnsupported (1205). false for any other error or an unknown
// reason.
func SignInReason(err error) (api.CrmSignInReason, bool) {
	e := apiError(err)
	if e == nil || e.Code != api.CodeCrmSignInUnsupported {
		return "", false
	}
	var d api.CrmSignInUnsupportedData
	if !decodeData(e.Data, &d) {
		return "", false
	}
	switch d.Reason {
	case api.CrmSignInMicrosoft, api.CrmSignInPlan:
		return d.Reason, true
	}
	return "", false
}

// OpenURL is the URL a client opens for a record or an item: u when it
// is an https URL with a host, else "" (nothing to open). The daemon
// builds it (docs/eway.md D15), so no question is asked.
func OpenURL(u string) string {
	p, err := url.Parse(u)
	if err != nil || !strings.EqualFold(p.Scheme, "https") || p.Host == "" || p.User != nil {
		return ""
	}
	return u
}

// DefaultName is the name an added account gets: the last segment of the
// service URL's path ("https://hosting.eway-crm.com/company" → "company"),
// else its host. "" for an address that is not a URL.
func DefaultName(serviceURL string) string {
	p, err := url.Parse(strings.TrimSpace(serviceURL))
	if err != nil || p.Host == "" {
		return ""
	}
	path := strings.Trim(p.Path, "/")
	if i := strings.LastIndex(path, "/"); i >= 0 {
		path = path[i+1:]
	}
	if s, err := url.PathUnescape(path); err == nil {
		path = s
	}
	if name := jira.Clean(path); name != "" {
		return name
	}
	return jira.Clean(strings.ToLower(p.Hostname()))
}

// ServiceHost is the host and path of an account's service URL, the
// subtitle of its row ("hosting.eway-crm.com/company"); "" without one.
func ServiceHost(a api.Account) string {
	if a.Config.Eway == nil {
		return ""
	}
	p, err := url.Parse(strings.TrimSpace(a.Config.Eway.ServiceURL))
	if err != nil || p.Host == "" {
		return ""
	}
	return jira.Clean(strings.ToLower(p.Host) + strings.TrimRight(p.Path, "/"))
}
