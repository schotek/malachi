// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package jira

import (
	"errors"
	"fmt"
	"net/mail"
	"reflect"
	"regexp"
	"regexp/syntax"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The settings of a Jira account are one page: the site (read only, with
// the button that replaces the token), the spaces, the synchronisation,
// the folders with the statuses that count as closed, what a notification
// e-mail of the site does, and how comments posted by bots are cleaned.
// The page opens with account.listSpaces (the stored token), which lists
// the spaces and the statuses to choose from; when that fails the page
// still edits what is stored. Save is account.update with empty
// credentials, which keeps the token.
//
// SettingsForm holds the edited copy; Apply turns it into the
// configuration to save. The daemon validates everything again: what is
// checked here is immediate feedback.

// SuggestedBotName and SuggestedMetadataFilter are what the page offers to
// add with one click while the lists lack them: the integration that
// mirrors comments between two Jira sites, and the technical line it puts
// under the comment's header. Data, not translated.
const (
	SuggestedBotName        = "Issue Sync – Synchronization for Jira"
	SuggestedMetadataFilter = `^Remote comment create date:.*$`
)

// minBotNameRunes is the shortest bot name the page accepts: the daemon
// looks for a name of three characters or more inside an author's name,
// and takes a shorter one only when it is the whole name.
const minBotNameRunes = 3

// SettingsStrings are the fixed texts of the settings page.
type SettingsStrings struct {
	// Title is the window's title.
	Title string
	// The site: the section, its rows and the button of the token's row.
	SiteTitle, SiteAddress, AccountName, SignedInAs, ReplaceToken string
	// The spaces: the section, its explanation, the text of an empty list.
	SpacesTitle, SpacesDescription, NoSpaces string
	// The synchronisation: the section, the offline window and the two
	// switches.
	SyncTitle, KeepOffline, KeepOfflineSubtitle string
	OnlyMine, OnlyMineSubtitle, ShowEvents      string
	// The folders: the section (its switches are VirtualFolderTitle) and
	// the picker of the closed statuses.
	FoldersTitle, ClosedStatuses, ClosedStatusesSubtitle string
	// The notification e-mails: the section, the mode and the senders.
	NotificationTitle, NotificationMode, Senders, SendersSubtitle string
	// The comments of bots: the section and its three lists.
	BotsTitle                          string
	BotNames, BotNamesSubtitle         string
	HiddenLines, HiddenLinesSubtitle   string
	NamePrefixes, NamePrefixesSubtitle string
	// Add and Remove are the buttons of a list: the one next to the field
	// and the one of an entry.
	Add, Remove string
	// Loading and Saving are the progress of account.listSpaces and
	// account.update.
	Loading, Saving string
}

// SettingsTexts returns the fixed texts, translated.
func SettingsTexts(tr Translator) SettingsStrings {
	return SettingsStrings{
		// TRANSLATORS: title of the window with the settings of a Jira account; "Jira" is a product name.
		Title:       tr.T("Jira Account"),
		SiteTitle:   tr.T("Jira Site"),
		SiteAddress: tr.T("Site Address"),
		AccountName: tr.T("Account Name"),
		// TRANSLATORS: label of the user a Jira account signs in as.
		SignedInAs: tr.T("Signed In As"),
		// TRANSLATORS: button that asks for a new API token of a Jira account.
		ReplaceToken:        tr.T("Replace Token…"),
		SpacesTitle:         tr.C("jira", "Spaces"),
		SpacesDescription:   tr.T("Choose the spaces whose issues appear as folders."),
		NoSpaces:            tr.T("No spaces are visible to this account"),
		SyncTitle:           tr.T("Synchronisation"),
		KeepOffline:         tr.T("Keep Issues Offline For"),
		KeepOfflineSubtitle: tr.T("Older issues stay on the site and are not shown"),
		OnlyMine:            tr.T("Only Issues Involving Me"),
		OnlyMineSubtitle:    tr.T("Assigned to you, reported by you or watched by you"),
		// TRANSLATORS: switch; the changes are listed among the comments of an issue.
		ShowEvents:     tr.T("Show Status and Assignee Changes"),
		FoldersTitle:   tr.T("Folders"),
		ClosedStatuses: tr.T("Closed Statuses"),
		// TRANSLATORS: "Open" is the folder of a Jira account with the issues that are not closed yet.
		ClosedStatusesSubtitle: tr.T("Issues in these statuses are left out of Open"),
		// TRANSLATORS: the e-mails Jira sends about changes of issues.
		NotificationTitle: tr.T("Notification E-mails"),
		NotificationMode:  tr.T("When a Jira Notification Arrives"),
		// TRANSLATORS: the addresses notification e-mails of Jira come from.
		Senders: tr.T("Senders"),
		// TRANSLATORS: "@example.org" is an example, keep it as it is.
		SendersSubtitle: tr.T("An address, or a domain such as @example.org"),
		BotsTitle:       tr.T("Comments Posted by Bots"),
		// TRANSLATORS: the Jira accounts of integrations that post comments for other people.
		BotNames:         tr.T("Bot Accounts"),
		BotNamesSubtitle: tr.T("Their comments are shown under the person they name"),
		// TRANSLATORS: lines of a comment that are not shown.
		HiddenLines:         tr.T("Hidden Lines"),
		HiddenLinesSubtitle: tr.T("Lines matching these patterns are removed from comments"),
		// TRANSLATORS: words a bot puts in front of a person's name, such as a company name.
		NamePrefixes:         tr.T("Name Prefixes"),
		NamePrefixesSubtitle: tr.T("Removed from the start of authors' names"),
		// TRANSLATORS: button that adds what was typed to a list.
		Add:     tr.T("Add"),
		Remove:  tr.T("Remove"),
		Loading: tr.T("Loading the spaces"),
		Saving:  tr.T("Saving the account"),
	}
}

// SiteInfo is the read-only part of the page.
type SiteInfo struct {
	// Address is the site's URL, Deployment its brand name ("Jira Cloud").
	Address, Deployment string
	// User is who the account signs in as: the user's name once
	// account.listSpaces told it, the login or the account's address
	// before. UserDetail is the address under a name, "" when User is the
	// address already.
	User, UserDetail string
	// TokenLabel names the token of the deployment ("API Token").
	TokenLabel string
}

// SettingsSite is the site of an account; user is the one
// account.listSpaces signed in as, nil while it is not known.
func SettingsSite(cfg api.AccountConfig, user *api.SiteUser, tr Translator) SiteInfo {
	var jc api.JiraConfig
	if cfg.Jira != nil {
		jc = *cfg.Jira
	}
	info := SiteInfo{
		Address:    Clean(jc.SiteURL),
		Deployment: DeploymentName(jc.Deployment),
		TokenLabel: CredentialFields(jc.Deployment, tr).TokenLabel,
	}
	address := Clean(jc.Login)
	if address == "" {
		address = Clean(cfg.Email)
	}
	if user != nil {
		info.User = Clean(user.Name)
		if email := Clean(user.Email); email != "" {
			address = email
		}
	}
	if info.User == "" {
		info.User = address
	} else if address != info.User {
		info.UserDetail = address
	}
	return info
}

// SettingsSpaceRows lists the spaces the page chooses from: the spaces of
// account.listSpaces in the daemon's order, then the stored ones the
// listing lacks (a space the token no longer sees, or every stored space
// when the listing failed), so that nothing stored is dropped unseen.
func SettingsSpaceRows(stored []api.SpaceRef, listed []api.Space, tr Translator) []SpaceRow {
	rows := SpaceRows(listed, tr)
	seen := make(map[string]bool, len(listed))
	for _, s := range listed {
		seen[s.ID] = true
	}
	for _, ref := range stored {
		if seen[ref.ID] {
			continue
		}
		seen[ref.ID] = true
		rows = append(rows, SpaceRow{ID: ref.ID, Title: SpaceTitle(api.Space{Key: ref.Key, Name: ref.Name})})
	}
	return rows
}

// SetSpaceSelected returns the chosen spaces after the check box of the
// space id changed: selected are the chosen ones so far, stored and listed
// what SettingsSpaceRows shows. The result is in the order of the rows,
// with the key and name of the listing where it has the space.
func SetSpaceSelected(selected, stored []api.SpaceRef, listed []api.Space, id string, on bool) []api.SpaceRef {
	chosen := make(map[string]bool, len(selected)+1)
	for _, ref := range selected {
		chosen[ref.ID] = true
	}
	chosen[id] = on
	var out []api.SpaceRef
	done := map[string]bool{}
	add := func(ref api.SpaceRef) {
		if chosen[ref.ID] && !done[ref.ID] {
			out = append(out, ref)
		}
		done[ref.ID] = true
	}
	for _, s := range listed {
		add(api.SpaceRef{ID: s.ID, Key: s.Key, Name: s.Name})
	}
	for _, ref := range stored {
		add(ref)
	}
	return out
}

// VirtualFolders are the fixed views the page has a switch for, in the
// order of the sidebar.
var VirtualFolders = []api.VirtualFolder{api.VirtualAssignedToMe, api.VirtualWatching, api.VirtualOpen}

// FolderShown reports whether the view v is shown: it is not among the
// disabled ones (JiraConfig.DisabledFolders).
func FolderShown(disabled []api.VirtualFolder, v api.VirtualFolder) bool {
	for _, d := range disabled {
		if d == v {
			return false
		}
	}
	return true
}

// SetFolderShown returns JiraConfig.DisabledFolders after the switch of
// the view v changed: the disabled views in the order of VirtualFolders,
// each once; nil when every view is shown.
func SetFolderShown(disabled []api.VirtualFolder, v api.VirtualFolder, shown bool) []api.VirtualFolder {
	var out []api.VirtualFolder
	for _, known := range VirtualFolders {
		off := !FolderShown(disabled, known)
		if known == v {
			off = !shown
		}
		if off {
			out = append(out, known)
		}
	}
	return out
}

// NotificationModes are the choices of what a notification e-mail does,
// in the order shown.
var NotificationModes = []api.NotificationMailMode{
	api.NotificationMailSync, api.NotificationMailHide, api.NotificationMailIgnore,
}

// NotificationModeLabels are the labels of NotificationModes, in order.
func NotificationModeLabels(tr Translator) []string {
	return []string{
		// TRANSLATORS: what a notification e-mail of Jira does: the issue it names is synchronised.
		tr.T("Check the Issue at Once"),
		// TRANSLATORS: what a notification e-mail of Jira does: the issue is synchronised and the e-mail is not listed.
		tr.T("Check the Issue and Hide the E-mail"),
		// TRANSLATORS: what a notification e-mail of Jira does: nothing, it is an e-mail like any other.
		tr.T("Do Nothing"),
	}
}

// IndexOfNotificationMode is the position in NotificationModes shown for
// JiraConfig.NotificationMail; the empty mode and one this client does
// not know are the default, the first.
func IndexOfNotificationMode(m api.NotificationMailMode) int {
	for i, known := range NotificationModes {
		if known == m {
			return i
		}
	}
	return 0
}

// NotificationHint is the text under the mode: what hiding means; "" for
// the other modes.
func NotificationHint(m api.NotificationMailMode, tr Translator) string {
	if m == api.NotificationMailHide {
		return tr.T("Hidden e-mails stay in your mailbox and come back when you turn this off")
	}
	return ""
}

// SendersEditable reports whether the senders matter in mode m: not when
// notification e-mails are left alone.
func SendersEditable(m api.NotificationMailMode) bool {
	return m != api.NotificationMailIgnore
}

// DefaultSenders is what an empty list of senders stands for, the
// placeholder of its field: every address of the site's host for Jira
// Cloud ("@acme.atlassian.net"), "" for Data Center, which has no default.
func DefaultSenders(cfg api.AccountConfig) string {
	if cfg.Jira == nil || cfg.Jira.Deployment != api.JiraCloud {
		return ""
	}
	if host := SiteHost(cfg); host != "" {
		return "@" + host
	}
	return ""
}

// StatusChoice is one check box of the picker of closed statuses: a name,
// which stands for every status of the site called so in its category
// (team-managed spaces each have their own "Done").
type StatusChoice struct {
	Name     string
	IDs      []string
	Selected bool
}

// StatusGroup are the statuses of a category, under its title and in its
// colour.
type StatusGroup struct {
	// Category is "" for the group of the statuses without a known one.
	Category api.IssueStatusCategory
	Title    string
	Style    StatusStyle
	Choices  []StatusChoice
}

// statusCategories are the groups of the picker, in order; the last one
// takes the statuses of any other category.
var statusCategories = []api.IssueStatusCategory{
	api.StatusCategoryTodo, api.StatusCategoryInProgress, api.StatusCategoryDone, "",
}

// StatusCategoryTitle is the title of a group of the picker.
func StatusCategoryTitle(c api.IssueStatusCategory, tr Translator) string {
	switch c {
	case api.StatusCategoryTodo:
		// TRANSLATORS: a category of issue statuses, as Jira calls it.
		return tr.C("status category", "To Do")
	case api.StatusCategoryInProgress:
		// TRANSLATORS: a category of issue statuses, as Jira calls it.
		return tr.C("status category", "In Progress")
	case api.StatusCategoryDone:
		// TRANSLATORS: a category of issue statuses, as Jira calls it.
		return tr.C("status category", "Done")
	}
	// TRANSLATORS: the issue statuses that belong to no category.
	return tr.C("status category", "Other")
}

// knownCategory is c when the picker has a group for it, "" otherwise.
func knownCategory(c api.IssueStatusCategory) api.IssueStatusCategory {
	switch c {
	case api.StatusCategoryTodo, api.StatusCategoryInProgress, api.StatusCategoryDone:
		return c
	}
	return ""
}

// DefaultClosedStatuses is what an empty JiraConfig.ClosedStatuses stands
// for: the statuses of the category done.
func DefaultClosedStatuses(statuses []api.IssueStatus) []api.StatusRef {
	var out []api.StatusRef
	seen := map[string]bool{}
	for _, s := range statuses {
		if s.Category != api.StatusCategoryDone || s.ID == "" || seen[s.ID] {
			continue
		}
		seen[s.ID] = true
		out = append(out, api.StatusRef{ID: s.ID, Name: s.Name})
	}
	return out
}

// StatusGroups is the picker of the closed statuses: the statuses of the
// site (account.listSpaces) by category, one choice per name, and in the
// last group the stored ones the site does not list (all of them when the
// listing failed). closed is JiraConfig.ClosedStatuses; while it is empty
// the statuses of the category done are the selected ones. A group without
// statuses is left out.
func StatusGroups(statuses []api.IssueStatus, closed []api.StatusRef, tr Translator) []StatusGroup {
	selected := map[string]bool{}
	for _, ref := range closed {
		selected[ref.ID] = true
	}
	byDefault := len(closed) == 0

	type slot struct{ group, choice int }
	groups := make([]StatusGroup, len(statusCategories))
	for i, c := range statusCategories {
		groups[i] = StatusGroup{Category: c, Title: StatusCategoryTitle(c, tr), Style: StyleOf(c)}
	}
	index := map[string]slot{}
	listed := map[string]bool{}
	add := func(category api.IssueStatusCategory, id, name string, on bool) {
		if id == "" || listed[id] {
			return
		}
		listed[id] = true
		if name = Clean(name); name == "" {
			name = Clean(id)
		}
		g := 0
		for i, c := range statusCategories {
			if c == category {
				g = i
			}
		}
		key := string(category) + "\x00" + name
		at, ok := index[key]
		if !ok {
			at = slot{g, len(groups[g].Choices)}
			index[key] = at
			groups[g].Choices = append(groups[g].Choices, StatusChoice{Name: name})
		}
		ch := &groups[at.group].Choices[at.choice]
		ch.IDs = append(ch.IDs, id)
		ch.Selected = ch.Selected || on
	}
	for _, s := range statuses {
		on := selected[s.ID]
		if byDefault {
			on = s.Category == api.StatusCategoryDone
		}
		add(knownCategory(s.Category), s.ID, s.Name, on)
	}
	for _, ref := range closed {
		add("", ref.ID, ref.Name, true)
	}

	out := groups[:0]
	for _, g := range groups {
		if len(g.Choices) > 0 {
			out = append(out, g)
		}
	}
	return out
}

// SetStatusSelected returns JiraConfig.ClosedStatuses after the check box
// of choice changed. Ticking stores every status of the name. The result
// is nil, the default, when it names exactly the statuses of the category
// done, and also when nothing is left: an empty list cannot say "no status
// is closed", so the default comes back.
func SetStatusSelected(statuses []api.IssueStatus, closed []api.StatusRef, choice StatusChoice, on bool) []api.StatusRef {
	current := closed
	if len(current) == 0 {
		current = DefaultClosedStatuses(statuses)
	}
	touched := map[string]bool{}
	for _, id := range choice.IDs {
		touched[id] = true
	}
	var out []api.StatusRef
	have := map[string]bool{}
	for _, ref := range current {
		if ref.ID == "" || have[ref.ID] || (touched[ref.ID] && !on) {
			continue
		}
		have[ref.ID] = true
		out = append(out, ref)
	}
	if on {
		names := map[string]string{}
		for _, s := range statuses {
			names[s.ID] = s.Name
		}
		for _, id := range choice.IDs {
			if id == "" || have[id] {
				continue
			}
			have[id] = true
			name, ok := names[id]
			if !ok {
				name = choice.Name
			}
			out = append(out, api.StatusRef{ID: id, Name: name})
		}
	}
	if def := DefaultClosedStatuses(statuses); len(def) > 0 && len(def) == len(out) {
		same := true
		for _, ref := range def {
			same = same && have[ref.ID]
		}
		if same {
			return nil
		}
	}
	return out
}

// StatusesProblem is why the chosen closed statuses cannot be saved; ""
// when they can.
func StatusesProblem(closed []api.StatusRef, tr Translator) string {
	if len(closed) > api.MaxJiraStatuses {
		return fmt.Sprintf(tr.N("Select at most %d status", "Select at most %d statuses", api.MaxJiraStatuses), api.MaxJiraStatuses)
	}
	return ""
}

// ListKind is one of the lists of texts the page edits.
type ListKind int

// The lists.
const (
	ListBotNames        ListKind = iota // JiraConfig.BotNames
	ListMetadataFilters                 // JiraConfig.MetadataFilters
	ListAuthorPrefixes                  // JiraConfig.AuthorPrefixes
	ListSenders                         // JiraConfig.NotificationSenders
)

// NormaliseEntry is an entry as it is stored: without the spaces around
// it; a sender in lower case.
func NormaliseEntry(kind ListKind, raw string) string {
	s := strings.TrimSpace(raw)
	if kind == ListSenders {
		s = strings.ToLower(s)
	}
	return s
}

// entryKey is what two entries of a list are the same by: a pattern and a
// sender as they are, a name prefix ignoring case, and a bot name the way
// the daemon compares names (visible text, lower case, every dash a
// hyphen).
func entryKey(kind ListKind, entry string) string {
	switch kind {
	case ListBotNames:
		return strings.Map(func(r rune) rune {
			switch r {
			case 0x2010, 0x2011, 0x2012, 0x2013, 0x2014, 0x2015, 0x2212, 0xfe58, 0xfe63, 0xff0d:
				return '-'
			}
			return r
		}, strings.ToLower(Clean(entry)))
	case ListAuthorPrefixes:
		return strings.ToLower(Clean(entry))
	}
	return entry
}

// NormaliseList is a list as it is stored: every entry normalised, empty
// ones and repetitions left out; nil when nothing is left.
func NormaliseList(kind ListKind, list []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, raw := range list {
		entry := NormaliseEntry(kind, raw)
		if entry == "" {
			continue
		}
		key := entryKey(kind, entry)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, entry)
	}
	return out
}

// PatternError is why pattern is not a regular expression of the daemon
// (RE2, Go's regexp): the reason in the words of regexp/syntax ("missing
// closing )"), technical English; "" for a valid pattern.
func PatternError(pattern string) string {
	_, err := regexp.Compile(pattern)
	if err == nil {
		return ""
	}
	var se *syntax.Error
	if errors.As(err, &se) {
		return string(se.Code)
	}
	return string(syntax.ErrInternalError)
}

// validSender reports an entry of the senders: a bare address, or "@" and
// a host name.
func validSender(s string) bool {
	if host, ok := strings.CutPrefix(s, "@"); ok {
		return validHostName(host)
	}
	a, err := mail.ParseAddress(s)
	return err == nil && a.Name == "" && a.Address == s
}

// validHostName reports a DNS name: labels of letters, digits and inner
// hyphens, at most 63 bytes each and 253 together.
func validHostName(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(h, "."), ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

// CheckEntry checks what the user typed to add to a list that holds have.
// entry is what to add, normalised; it is "" when there is nothing to add:
// the field is empty (problem is "" too) or the entry cannot be added, and
// problem says why under the field.
func CheckEntry(kind ListKind, raw string, have []string, tr Translator) (entry, problem string) {
	s := NormaliseEntry(kind, raw)
	if s == "" {
		return "", ""
	}
	if len(s) > api.MaxJiraPatternBytes {
		return "", tr.T("This entry is too long")
	}
	if !utf8.ValidString(s) || strings.IndexFunc(s, unicode.IsControl) >= 0 {
		return "", tr.T("This entry contains control characters")
	}
	switch kind {
	case ListBotNames:
		if utf8.RuneCountInString(Clean(s)) < minBotNameRunes {
			return "", tr.T("A bot name needs at least 3 characters")
		}
	case ListMetadataFilters:
		if reason := PatternError(s); reason != "" {
			// TRANSLATORS: %s says what is wrong with a regular expression, in English ("missing closing )").
			return "", fmt.Sprintf(tr.T("This pattern is not valid: %s"), reason)
		}
	case ListAuthorPrefixes:
		if Clean(s) == "" {
			return "", tr.T("This entry contains control characters")
		}
	case ListSenders:
		if !validSender(s) {
			return "", tr.T("Enter an address, or a domain such as @example.org")
		}
	}
	key := entryKey(kind, s)
	for _, other := range have {
		if entryKey(kind, NormaliseEntry(kind, other)) == key {
			return "", tr.T("This entry is already in the list")
		}
	}
	if len(have) >= api.MaxJiraListEntries {
		return "", fmt.Sprintf(tr.N("The list holds at most %d entry", "The list holds at most %d entries", api.MaxJiraListEntries), api.MaxJiraListEntries)
	}
	return s, ""
}

// Suggestion is an entry the page offers to add with one click.
type Suggestion struct {
	// Value is the entry, Label the button's text.
	Value, Label string
}

// Suggestions are the entries offered for a list that holds have: the
// suggested bot name and the suggested pattern while their lists lack
// them and have room.
func Suggestions(kind ListKind, have []string, tr Translator) []Suggestion {
	var value string
	switch kind {
	case ListBotNames:
		value = SuggestedBotName
	case ListMetadataFilters:
		value = SuggestedMetadataFilter
	default:
		return nil
	}
	if len(have) >= api.MaxJiraListEntries {
		return nil
	}
	key := entryKey(kind, value)
	for _, other := range have {
		if entryKey(kind, NormaliseEntry(kind, other)) == key {
			return nil
		}
	}
	// TRANSLATORS: button that adds a suggested entry to a list; %s is the entry, such as the name of a bot.
	return []Suggestion{{Value: value, Label: fmt.Sprintf(tr.T("Add %s"), value)}}
}

// SettingsForm is the edited copy of what the page changes.
type SettingsForm struct {
	// Name is the account's name.
	Name string
	// Spaces are the chosen spaces.
	Spaces []api.SpaceRef
	// OfflineDays is the offline window as stored (0 = the default) until
	// the user picks one of OfflineChoices.
	OfflineDays int
	OnlyMine    bool
	// ShowEvents is "Show Status and Assignee Changes": not HideEvents.
	ShowEvents bool
	// DisabledFolders are the views switched off.
	DisabledFolders []api.VirtualFolder
	// ClosedStatuses are the statuses that count as closed; empty = those
	// of the category done.
	ClosedStatuses []api.StatusRef
	// NotificationMail is the mode, never "".
	NotificationMail                                               api.NotificationMailMode
	NotificationSenders, BotNames, MetadataFilters, AuthorPrefixes []string
}

// NewSettingsForm is the form of an account as it is stored.
func NewSettingsForm(cfg api.AccountConfig) SettingsForm {
	f := SettingsForm{Name: cfg.Name, ShowEvents: true, NotificationMail: api.NotificationMailSync}
	jc := cfg.Jira
	if jc == nil {
		return f
	}
	f.Spaces = append([]api.SpaceRef(nil), jc.Spaces...)
	f.OfflineDays = jc.OfflineDays
	f.OnlyMine = jc.OnlyMine
	f.ShowEvents = !jc.HideEvents
	f.DisabledFolders = append([]api.VirtualFolder(nil), jc.DisabledFolders...)
	f.ClosedStatuses = append([]api.StatusRef(nil), jc.ClosedStatuses...)
	f.NotificationMail = NotificationModes[IndexOfNotificationMode(jc.NotificationMail)]
	f.NotificationSenders = append([]string(nil), jc.NotificationSenders...)
	f.BotNames = append([]string(nil), jc.BotNames...)
	f.MetadataFilters = append([]string(nil), jc.MetadataFilters...)
	f.AuthorPrefixes = append([]string(nil), jc.AuthorPrefixes...)
	return f
}

// Apply is the configuration of account.update: cfg, the account as it is
// stored, with what the form holds, normalised (texts trimmed, repetitions
// and empty entries dropped, an empty list nil, the default mode left
// out). The connection (site, deployment, login) and everything the page
// does not edit stay as they are. A configuration of another kind comes
// back unchanged.
func (f SettingsForm) Apply(cfg api.AccountConfig) api.AccountConfig {
	if cfg.Jira == nil {
		return cfg
	}
	jc := *cfg.Jira
	name := truncate(strings.TrimSpace(f.Name), maxNameBytes)
	if name == "" {
		if name = SiteHost(cfg); name == "" {
			name = "Jira"
		}
	}
	cfg.Name = name

	jc.Spaces = make([]api.SpaceRef, 0, len(f.Spaces))
	seen := map[string]bool{}
	for _, ref := range f.Spaces {
		if ref.ID == "" || seen[ref.ID] {
			continue
		}
		seen[ref.ID] = true
		jc.Spaces = append(jc.Spaces, ref)
	}
	jc.OfflineDays = min(max(f.OfflineDays, 0), api.MaxJiraOfflineDays)
	jc.OnlyMine = f.OnlyMine
	jc.HideEvents = !f.ShowEvents
	jc.DisabledFolders = nil
	for _, v := range VirtualFolders {
		if !FolderShown(f.DisabledFolders, v) {
			jc.DisabledFolders = append(jc.DisabledFolders, v)
		}
	}
	jc.ClosedStatuses = nil
	seen = map[string]bool{}
	for _, ref := range f.ClosedStatuses {
		ref.ID, ref.Name = strings.TrimSpace(ref.ID), strings.TrimSpace(ref.Name)
		if ref.ID == "" || seen[ref.ID] {
			continue
		}
		seen[ref.ID] = true
		jc.ClosedStatuses = append(jc.ClosedStatuses, ref)
	}
	jc.NotificationMail = NotificationModes[IndexOfNotificationMode(f.NotificationMail)]
	if jc.NotificationMail == api.NotificationMailSync {
		jc.NotificationMail = ""
	}
	jc.NotificationSenders = NormaliseList(ListSenders, f.NotificationSenders)
	jc.BotNames = NormaliseList(ListBotNames, f.BotNames)
	jc.MetadataFilters = NormaliseList(ListMetadataFilters, f.MetadataFilters)
	jc.AuthorPrefixes = NormaliseList(ListAuthorPrefixes, f.AuthorPrefixes)
	cfg.Jira = &jc
	return cfg
}

// SettingsProblem is why the form cannot be saved; "" when it can.
func (f SettingsForm) SettingsProblem(tr Translator) string {
	if p := SpacesProblem(len(f.Spaces), tr); p != "" {
		return p
	}
	return StatusesProblem(f.ClosedStatuses, tr)
}

// Changed reports whether updated differs from old in what the daemon
// acts on: the page saves only then. Two configurations that say the same
// in different words are equal: the default written out or left out (the
// offline window, the mode), lists that differ in spaces, repetitions or,
// where the order means nothing, in order, and the names of spaces and
// statuses, which are for display.
func Changed(old, updated api.AccountConfig) bool {
	return !reflect.DeepEqual(compared(old), compared(updated))
}

// compared is cfg in the form Changed compares.
func compared(cfg api.AccountConfig) api.AccountConfig {
	cfg.Name = strings.TrimSpace(cfg.Name)
	cfg.Kind = cfg.Protocol()
	if cfg.Jira == nil {
		return cfg
	}
	jc := *cfg.Jira
	spaces := make([]api.SpaceRef, 0, len(jc.Spaces))
	seen := map[string]bool{}
	for _, ref := range jc.Spaces {
		if !seen[ref.ID] {
			seen[ref.ID] = true
			spaces = append(spaces, api.SpaceRef{ID: ref.ID, Key: ref.Key})
		}
	}
	sort.Slice(spaces, func(i, j int) bool { return spaces[i].ID < spaces[j].ID })
	jc.Spaces = spaces

	statuses := make([]api.StatusRef, 0, len(jc.ClosedStatuses))
	seen = map[string]bool{}
	for _, ref := range jc.ClosedStatuses {
		if id := strings.TrimSpace(ref.ID); id != "" && !seen[id] {
			seen[id] = true
			statuses = append(statuses, api.StatusRef{ID: id})
		}
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].ID < statuses[j].ID })
	jc.ClosedStatuses = statuses

	disabled := make([]api.VirtualFolder, 0, len(jc.DisabledFolders))
	for _, v := range jc.DisabledFolders {
		if FolderShown(disabled, v) {
			disabled = append(disabled, v)
		}
	}
	sort.Slice(disabled, func(i, j int) bool { return disabled[i] < disabled[j] })
	jc.DisabledFolders = disabled

	if jc.OfflineDays <= 0 {
		jc.OfflineDays = api.DefaultJiraOfflineDays
	}
	if jc.NotificationMail == "" {
		jc.NotificationMail = api.NotificationMailSync
	}
	jc.NotificationSenders = keys(ListSenders, jc.NotificationSenders, true)
	jc.BotNames = keys(ListBotNames, jc.BotNames, true)
	jc.MetadataFilters = keys(ListMetadataFilters, jc.MetadataFilters, true)
	// The prefixes are stripped in their order.
	jc.AuthorPrefixes = keys(ListAuthorPrefixes, jc.AuthorPrefixes, false)
	cfg.Jira = &jc
	return cfg
}

// keys is what the entries of a list are compared by (entryKey), each
// once, in byte order when the order of the list means nothing; never nil.
func keys(kind ListKind, list []string, anyOrder bool) []string {
	out := []string{}
	for _, entry := range NormaliseList(kind, list) {
		out = append(out, entryKey(kind, entry))
	}
	if anyOrder {
		sort.Strings(out)
	}
	return out
}
