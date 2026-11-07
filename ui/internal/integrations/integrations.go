// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package integrations is the view logic of the two account pages of the
// preferences: Accounts, which lists the mail accounts (kind imap, the
// empty kind, and graph), and Integrations, which lists every other
// account — the services beside the mail, today Jira (kind jira), CalDAV
// calendars (kind caldav), eWay-CRM (kind eway) and AI provider accounts
// (kind ai, docs/plans/ai-api.md D3); a kind added later lands there by
// itself — and the catalogue of services that can be added, in two
// sections: the services and the AI (docs/plans/ai-api.md §7.2).
//
// The AI section also names two integrations that are not accounts of
// the daemon but state of this client (D15): Claude Code / Claude Desktop
// (the bridge registered in the Claude apps, Settings → AI's Register in
// Claude, and the Claude Code the panel runs) and ChatGPT (Codex). Each
// is in one place at a time (owner's decision 2026-11-07): in the
// catalogue with Add… while it is not connected, and as a row after the
// daemon's accounts once it is (LocalRows, from the state the window
// has; Offered says which catalogue entries remain); they are never in
// FullOrder.
//
// Both pages show rows of the same shape (Row): the switch, the editor
// and the sentence of the remove confirmation depend on the kind. The
// order of the accounts is one list owned by the daemon; a page that
// reorders its own rows sends the whole list (FullOrder), mail accounts
// first, so account.reorder never has to guess where the other page's
// accounts go.
//
// The package is pure (no GTK, no gettext, no cgo: the caller passes a
// Translator) so its rules are tested without a display and the other
// clients port it one to one: macOS MalachiCore/Model/Integrations.swift,
// Windows Malachi.Core/Model/Integrations.cs. GTK uses it with i18n.Tr.
package integrations

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/crm"
	"github.com/schotek/malachi/ui/internal/jira"
)

// Translator translates a msgid of the malachi domain. GTK passes
// i18n.Tr; the tests pass one that returns the msgid.
type Translator interface {
	T(msgid string) string
}

// plural adapts a Translator to assistant.Translator for the texts shared
// with the assistant (the names of the AI providers, the ChatGPT labels);
// none of them has a plural, so N just picks a form.
type plural struct{ Translator }

func (p plural) N(singular, pl string, n int) string {
	if n == 1 {
		return p.T(singular)
	}
	return p.T(pl)
}

// ShowsInAccounts reports an account the Accounts page lists: a mail
// account (kind imap, also the empty kind, or graph).
func ShowsInAccounts(a api.Account) bool {
	switch a.Config.Protocol() {
	case api.AccountIMAP, api.AccountGraph:
		return true
	}
	return false
}

// ShowsInIntegrations reports an account the Integrations page lists:
// every account that is not a mail account.
func ShowsInIntegrations(a api.Account) bool { return !ShowsInAccounts(a) }

// Split divides accounts into the mail accounts and the integrations,
// each in the order of accounts.
func Split(accounts []api.Account) (mail, integrations []api.Account) {
	for _, a := range accounts {
		if ShowsInAccounts(a) {
			mail = append(mail, a)
		} else {
			integrations = append(integrations, a)
		}
	}
	return mail, integrations
}

// Page is one of the two account pages.
type Page int

// The pages.
const (
	PageAccounts Page = iota
	PageIntegrations
)

// FullOrder is the order account.reorder gets when page was reordered to
// order: the mail accounts first, then the integrations. The reordered
// page's accounts follow order; the other page's keep their order in all.
// An id of order that is not in all, or that belongs to the other page,
// is ignored, a repeated one counts once, and an account of the page
// that order omits follows the listed ones in its order in all. The
// result names every account of all exactly once.
func FullOrder(all []api.Account, page Page, order []api.AccountID) []api.AccountID {
	mail, other := Split(all)
	ordered := func(group []api.Account, reordered bool) []api.AccountID {
		ids := make([]api.AccountID, 0, len(group))
		if !reordered {
			for _, a := range group {
				ids = append(ids, a.ID)
			}
			return ids
		}
		in := make(map[api.AccountID]bool, len(group))
		for _, a := range group {
			in[a.ID] = true
		}
		seen := make(map[api.AccountID]bool, len(group))
		for _, id := range order {
			if in[id] && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		for _, a := range group {
			if !seen[a.ID] {
				seen[a.ID] = true
				ids = append(ids, a.ID)
			}
		}
		return ids
	}
	out := ordered(mail, page == PageAccounts)
	return append(out, ordered(other, page == PageIntegrations)...)
}

// Icons of the kinds (GTK symbolic icon names; the other clients map
// them to their own). A mail account's row shows its provider's icon
// when the theme has one (widget.ProviderIcon); MailIcon is the fallback.
const (
	MailIcon   = "mail-unread-symbolic"
	JiraIcon   = "checkbox-checked-symbolic" // Adwaita has no ticket
	CalDAVIcon = "x-office-calendar-symbolic"
	EwayIcon   = crm.Icon // Adwaita has no CRM
	// AIIcon is the icon of an AI provider account and of the local AI
	// integrations (ui/data/icons, the assistant's sparkle).
	AIIcon = "malachi-assistant-symbolic"
)

// Section is a section of the catalogue.
type Section int

// The sections, in the order of the page.
const (
	// SectionServices: Jira, CalDAV calendars, eWay-CRM.
	SectionServices Section = iota
	// SectionAI: the AI integrations (docs/plans/ai-api.md §7.2).
	SectionAI
)

// LocalKind is an integration that is state of this client, not an
// account of the daemon (docs/plans/ai-api.md D15).
type LocalKind int

// The local kinds; LocalNone marks a catalogue entry that adds an account.
const (
	LocalNone LocalKind = iota
	// LocalClaude is Claude Code / Claude Desktop: the bridge registered
	// in the Claude apps and the Claude Code the panel runs.
	LocalClaude
	// LocalChatGPT is ChatGPT through Codex.
	LocalChatGPT
)

// Service is one entry of the catalogue on the Integrations page: an
// account kind to add (Kind; for an AI provider account also Provider),
// or a local integration (Local, with an empty Kind).
type Service struct {
	Section     Section
	Kind        api.AccountKind
	Provider    api.AIProvider // AccountAI only
	Local       LocalKind      // set only with an empty Kind
	Name        string         // the service's name; Jira, eWay-CRM, Claude and ChatGPT are brands, not translated
	Description string
	Icon        string // GTK symbolic icon name
}

// Catalog is the services that can be added, in the order the page
// lists them: the services first, then the AI section.
func Catalog(tr Translator) []Service {
	return append(services(tr), aiServices(tr)...)
}

// CatalogSection is the entries of one section of Catalog, in its order.
func CatalogSection(tr Translator, s Section) []Service {
	var out []Service
	for _, e := range Catalog(tr) {
		if e.Section == s {
			out = append(out, e)
		}
	}
	return out
}

// Offered reports whether catalogue entry s is shown with the local
// integrations in the states c and g: an account kind always (another
// account can be added), a local integration only while it is not
// connected — connected, it is a row of Connected Services instead.
func Offered(s Service, c ClaudeState, g ChatGPTState) bool {
	return s.Local == LocalNone || !LocalConnected(s.Local, c, g)
}

// aiServices is the AI section of the catalogue (docs/plans/ai-api.md
// §7.2): the two local integrations, then the three provider kinds.
func aiServices(tr Translator) []Service {
	names := assistant.APIText(plural{tr})
	return []Service{
		{
			Section: SectionAI, Local: LocalClaude, Name: claudeName,
			// TRANSLATORS: Preferences → Integrations: what the Claude apps on this computer bring.
			Description: tr.T("Hand mail to Claude Desktop or Claude Code, and run the assistant in the app with Claude Code"),
			Icon:        AIIcon,
		},
		{
			Section: SectionAI, Local: LocalChatGPT, Name: chatGPTName,
			// TRANSLATORS: Preferences → Integrations: what ChatGPT (Codex) brings.
			Description: tr.T("Run the assistant in the app through Codex, using your ChatGPT plan"),
			Icon:        AIIcon,
		},
		{
			Section: SectionAI, Kind: api.AccountAI, Provider: api.AIAnthropic, Name: names.Anthropic,
			// TRANSLATORS: Preferences → Integrations: what an Anthropic API account brings.
			Description: tr.T("Run the assistant in the app over the Anthropic API with your own API key"),
			Icon:        AIIcon,
		},
		{
			Section: SectionAI, Kind: api.AccountAI, Provider: api.AIOpenAI, Name: names.OpenAI,
			// TRANSLATORS: Preferences → Integrations: what an OpenAI API account brings.
			Description: tr.T("Run the assistant in the app over the OpenAI API with your own API key"),
			Icon:        AIIcon,
		},
		{
			Section: SectionAI, Kind: api.AccountAI, Provider: api.AIOpenAICompatible, Name: names.OpenAICompatible,
			// TRANSLATORS: Preferences → Integrations: what an OpenAI-compatible server brings; the product names stay as they are.
			Description: tr.T("Ollama, llama.cpp, LM Studio, vLLM and gateways such as OpenRouter: a model on your computer or a server of your choice"),
			Icon:        AIIcon,
		},
	}
}

// services is the catalogue's first section.
func services(tr Translator) []Service {
	return []Service{
		{
			Section: SectionServices, Kind: api.AccountJira, Name: jiraName,
			// TRANSLATORS: Preferences → Integrations: what a Jira
			// account brings ("issues" as in Jira's own word).
			Description: tr.T("Issues read as mail, comments and status changes"),
			Icon:        JiraIcon,
		},
		{
			Section: SectionServices, Kind: api.AccountCalDAV,
			// TRANSLATORS: Preferences → Integrations: the name of the
			// service in the list of services that can be added.
			Name:        tr.T("CalDAV Calendar"),
			Description: tr.T("Nextcloud, iCloud, Fastmail and other CalDAV servers"),
			Icon:        CalDAVIcon,
		},
		{
			Section: SectionServices, Kind: api.AccountEway, Name: crm.Brand,
			// TRANSLATORS: Preferences → Integrations: what an eWay-CRM
			// account brings ("deals" as eWay-CRM's sales opportunities).
			Description: tr.T("Save messages to your CRM with their contacts, companies and deals"),
			Icon:        EwayIcon,
		},
	}
}

// The brands, never translated.
const (
	jiraName    = "Jira"
	claudeName  = "Claude Code / Claude Desktop"
	chatGPTName = "ChatGPT (Codex)"
)

// Editor is what a row's Edit button opens.
type Editor int

// The editors.
const (
	// EditorNone: the row has no Edit button (a kind this client does
	// not know).
	EditorNone Editor = iota
	// EditorMailWizard is the mail account assistant in its edit mode.
	EditorMailWizard
	// EditorJira is a Jira account's settings (the token assistant when
	// a sign-in route asks for the token).
	EditorJira
	// EditorCalendarPage switches the preferences to the Calendar page,
	// where a CalDAV account's server, password, certificate and
	// calendars are.
	EditorCalendarPage
	// EditorEway is the eWay-CRM account assistant in its edit mode
	// (ui/internal/accountwizard eway_flow.go): the user name, the
	// password and the name; the service address stays.
	EditorEway
	// EditorAI is an AI provider account's dialog in its edit mode
	// (ui/internal/aiaccount): the name, the model, the key (Replace
	// Key…), the server of an OpenAI-compatible one; the provider stays.
	EditorAI
	// EditorClaude is the settings of the Claude apps on this computer
	// (Register in Claude, where Claude Code is, its sign-in).
	EditorClaude
	// EditorChatGPT is the settings of ChatGPT (Codex): Continue with
	// ChatGPT, where Codex is, the model.
	EditorChatGPT
)

// Row is what an account's row on either page shows besides its status:
// plain texts (nothing is markup), the icon, whether it has the Enabled
// switch (account.setEnabled pauses the sync, or an eWay-CRM account's
// saving; CalDAV has no pause), what
// its Edit button opens and is called, and the body of its remove
// confirmation.
type Row struct {
	ID           api.AccountID
	Kind         api.AccountKind
	Title        string
	Subtitle     string
	Icon         string
	OffersSwitch bool
	Editor       Editor
	EditLabel    string // the Edit button's tooltip
	RemoveBody   string
}

// RowFor is the row of account a.
//
// Title: the account name as the user typed it (nothing trimmed), else
// the Jira site's host, else the address (an AI provider account without
// a name: its provider). Subtitle: the host of the Jira site or of the
// CalDAV server, the host and path of the eWay-CRM service, the provider
// and model of an AI provider account ("Anthropic API · claude-opus-5-5"),
// else — and when the title shows the host already — the address.
func RowFor(a api.Account, tr Translator) Row {
	kind := a.Config.Protocol()
	r := Row{ID: a.ID, Kind: kind, Title: title(a), OffersSwitch: true}
	r.Subtitle = a.Config.Email
	if host := host(a); host != "" && host != r.Title {
		r.Subtitle = host
	}
	switch kind {
	case api.AccountIMAP, api.AccountGraph:
		r.Icon, r.Editor, r.EditLabel = MailIcon, EditorMailWizard, tr.T("Edit Account")
		// TRANSLATORS: %s is the account's e-mail address.
		r.RemoveBody = fmt.Sprintf(tr.T("%s will be removed from Malachi Mail. Mail on the server is not affected."), a.Config.Email)
	case api.AccountJira:
		r.Icon, r.Editor, r.EditLabel = JiraIcon, EditorJira, tr.T("Edit Account")
		// TRANSLATORS: %s is the account's name (or its site's host).
		r.RemoveBody = fmt.Sprintf(tr.T("%s will be removed from Malachi Mail. Issues on the Jira site are not affected."), r.Title)
	case api.AccountCalDAV:
		r.Icon, r.Editor, r.EditLabel = CalDAVIcon, EditorCalendarPage, calendarSettings(tr)
		r.OffersSwitch = false
		// TRANSLATORS: %s is the calendar account's name.
		r.RemoveBody = fmt.Sprintf(tr.T("%s will be removed from Malachi Mail. Calendars on the server are not affected."), r.Title)
	case api.AccountEway:
		r.Icon, r.Editor, r.EditLabel = EwayIcon, EditorEway, tr.T("Edit Account")
		// TRANSLATORS: %s is the eWay-CRM account's name.
		r.RemoveBody = fmt.Sprintf(tr.T("%s will be removed from Malachi Mail. Messages saved in eWay-CRM stay there."), r.Title)
	case api.AccountAI:
		r.Icon, r.Editor, r.EditLabel = AIIcon, EditorAI, tr.T("Edit Account")
		if r.Title == "" {
			r.Title = aiProvider(a, tr)
		}
		r.Subtitle = aiSubtitle(a, tr)
		// TRANSLATORS: %s is the AI provider account's name; its API key leaves the keyring with it.
		r.RemoveBody = fmt.Sprintf(tr.T("%s will be removed from Malachi Mail, and its API key with it."), r.Title)
	default:
		r.Icon = MailIcon
		// TRANSLATORS: %s is the account's name.
		r.RemoveBody = fmt.Sprintf(tr.T("%s will be removed from Malachi Mail."), r.Title)
	}
	return r
}

// title is the name of the row: the name, the Jira site's host, the
// address.
func title(a api.Account) string {
	if a.Config.Name != "" {
		return a.Config.Name
	}
	if h := jira.SiteHost(a.Config); h != "" {
		return h
	}
	return a.Config.Email
}

// aiProvider names an AI provider account's provider kind.
func aiProvider(a api.Account, tr Translator) string {
	if a.Config.AI == nil {
		return assistant.ProviderName(plural{tr}, "")
	}
	return assistant.ProviderName(plural{tr}, a.Config.AI.Provider)
}

// aiSubtitle is "<provider> · <model>" of an AI provider account; the
// provider alone without a model. The model id is cleaned as any string
// the daemon stores from the user's input.
func aiSubtitle(a api.Account, tr Translator) string {
	s := aiProvider(a, tr)
	if a.Config.AI == nil {
		return s
	}
	if m := jira.Clean(a.Config.AI.Model); m != "" {
		s += " · " + m
	}
	return s
}

// host is the host the subtitle shows: a Jira account's site, a CalDAV
// account's server, an eWay-CRM account's service (with its path, which
// tells two instances on one host apart), the host of an AI provider
// account's effective base URL; empty for anything else.
func host(a api.Account) string {
	switch a.Config.Protocol() {
	case api.AccountAI:
		if a.Config.AI == nil {
			return ""
		}
		u, err := url.Parse(strings.TrimSpace(a.Config.AI.EffectiveBaseURL()))
		if err != nil {
			return ""
		}
		return jira.Clean(strings.ToLower(u.Hostname()))
	case api.AccountJira:
		return jira.SiteHost(a.Config)
	case api.AccountCalDAV:
		if a.Config.CalDAV == nil {
			return ""
		}
		u, err := url.Parse(strings.TrimSpace(a.Config.CalDAV.ServerURL))
		if err != nil {
			return ""
		}
		return jira.Clean(strings.ToLower(u.Hostname()))
	case api.AccountEway:
		return crm.ServiceHost(a)
	}
	return ""
}

// ClaudeState is what this client knows of the Claude apps on this
// computer (the window fills it from its Assistant state): whether the
// bridge is registered in them (Settings → AI's Register in Claude), and
// the Claude Code the panel runs — found, where, which version, and what
// claude auth status said (SignInKnown false while it did not answer).
type ClaudeState struct {
	Registered  bool
	Present     bool
	Path        string
	Version     string
	SignInKnown bool
	SignedIn    bool
}

// ChatGPTState is what this client knows of ChatGPT (Codex): whether the
// user continued with ChatGPT (Connected, as Account), and whether Codex
// was found.
type ChatGPTState struct {
	Connected  bool
	Account    string
	CodexFound bool
}

// LocalRow is the row of a local integration on the Integrations page,
// shown after the daemon's accounts: plain texts (nothing is markup), the
// icon, what its Edit button opens, and whether it has a Remove button
// (unregister, disconnect) and what that is called.
type LocalRow struct {
	Kind        LocalKind
	Title       string
	Subtitle    string
	Editor      Editor
	Icon        string
	CanRemove   bool
	RemoveLabel string
}

// LocalKinds are the local integrations in the order of the page.
var LocalKinds = []LocalKind{LocalClaude, LocalChatGPT}

// LocalConnected reports whether local integration kind is connected: the
// bridge registered in the Claude apps, or ChatGPT continued with. A
// connected one has a row of Connected Services and leaves the catalogue
// (Offered); Remove on its row disconnects it.
func LocalConnected(kind LocalKind, c ClaudeState, g ChatGPTState) bool {
	switch kind {
	case LocalClaude:
		return c.Registered
	case LocalChatGPT:
		return g.Connected
	}
	return false
}

// LocalRows are the rows of the connected local integrations, Claude
// first, from LocalRowFor; none while nothing is connected.
func LocalRows(c ClaudeState, g ChatGPTState, tr Translator) []LocalRow {
	var rows []LocalRow
	for _, kind := range LocalKinds {
		if r, shown := LocalRowFor(kind, c, g, tr); shown {
			rows = append(rows, r)
		}
	}
	return rows
}

// LocalRowFor is the row of local integration kind, and whether the page
// shows it (LocalConnected; a view that keeps a widget per kind hides the
// row of one that is not connected). Claude Code / Claude Desktop says
// "Registered in Claude Desktop and Claude Code", then the Claude Code
// found (its version, else its path) or "Claude Code not found", and
// "signed in" or "not signed in" once known; ChatGPT (Codex) says
// "Connected as …" and "Codex not found" when it is missing. Remove is
// Unregister and Disconnect; CanRemove is the row's being shown.
func LocalRowFor(kind LocalKind, c ClaudeState, g ChatGPTState, tr Translator) (LocalRow, bool) {
	shown := LocalConnected(kind, c, g)
	switch kind {
	case LocalClaude:
		var claude []string
		// TRANSLATORS: Preferences → Integrations, the Claude row: the bridge is registered in both Claude apps.
		claude = append(claude, tr.T("Registered in Claude Desktop and Claude Code"))
		switch {
		case !c.Present:
			claude = append(claude, tr.T("Claude Code not found"))
		case c.Version != "":
			// TRANSLATORS: %s is the version of Claude Code found on this computer.
			claude = append(claude, fmt.Sprintf(tr.T("Claude Code %s"), jira.Clean(c.Version)))
		case c.Path != "":
			claude = append(claude, jira.Clean(c.Path))
		}
		if c.Present && c.SignInKnown {
			if c.SignedIn {
				// TRANSLATORS: Preferences → Integrations, the Claude row: Claude Code is signed in (lower case, after " · ").
				claude = append(claude, tr.T("signed in"))
			} else {
				// TRANSLATORS: Preferences → Integrations, the Claude row: Claude Code is not signed in (lower case, after " · ").
				claude = append(claude, tr.T("not signed in"))
			}
		}
		return LocalRow{
			Kind: LocalClaude, Title: claudeName, Subtitle: strings.Join(claude, " · "),
			Editor: EditorClaude, Icon: AIIcon, CanRemove: shown,
			// TRANSLATORS: the Remove button of the Claude row in Preferences → Integrations: it unregisters the bridge from the Claude apps.
			RemoveLabel: tr.T("Unregister"),
		}, shown
	case LocalChatGPT:
		labels := assistant.ChatGPTText(plural{tr})
		chatgpt := []string{fmt.Sprintf(labels.Connected, jira.Clean(g.Account))}
		if !g.CodexFound {
			chatgpt = append(chatgpt, tr.T("Codex not found"))
		}
		return LocalRow{
			Kind: LocalChatGPT, Title: chatGPTName, Subtitle: strings.Join(chatgpt, " · "),
			Editor: EditorChatGPT, Icon: AIIcon, CanRemove: shown,
			RemoveLabel: labels.Disconnect,
		}, shown
	}
	return LocalRow{}, false
}

// PageTexts are the fixed texts of the Integrations page.
type PageTexts struct {
	Title            string // the page's title in the preferences' navigation
	Description      string // under the title
	ConnectedTitle   string // the group of the added services
	AvailableTitle   string // the group of the catalogue (its services section)
	AITitle          string // the catalogue's AI section
	EmptyTitle       string // the connected group's placeholder row
	EmptySubtitle    string
	Add              string // a catalogue row's button
	Settings         string // a local row's Edit tooltip
	CalendarSettings string // a CalDAV row's Edit tooltip
	// The confirmations of the local rows' Remove: the Claude row's
	// Unregister and the ChatGPT row's Disconnect.
	UnregisterHeading, UnregisterBody, DisconnectHeading, DisconnectBody string
}

// Texts are the Integrations page's texts.
func Texts(tr Translator) PageTexts {
	return PageTexts{
		// TRANSLATORS: a page of the preferences: services beside the
		// mail, such as Jira, CalDAV calendars, eWay-CRM and AI.
		Title:          tr.T("Integrations"),
		Description:    tr.T("Services beside your mail: issue trackers, calendars, CRM and AI"),
		ConnectedTitle: tr.T("Connected Services"),
		AvailableTitle: tr.T("Available Services"),
		// TRANSLATORS: the AI section of the catalogue in Preferences → Integrations.
		AITitle:       tr.T("AI"),
		EmptyTitle:    tr.T("No integrations yet"),
		EmptySubtitle: tr.T("Add one from the list below"),
		// TRANSLATORS: the button of a service in Preferences →
		// Integrations → Available Services.
		Add: tr.T("Add…"),
		// TRANSLATORS: tooltip of the Edit button of a local integration (Claude, ChatGPT) in Preferences → Integrations.
		Settings:         tr.T("Settings"),
		CalendarSettings: calendarSettings(tr),
		// TRANSLATORS: confirmation of the Claude row's Unregister in Preferences → Integrations.
		UnregisterHeading: tr.T("Unregister from Claude?"),
		UnregisterBody:    tr.T("Claude Desktop and Claude Code will no longer read your mail through Malachi Mail. The assistant in the app keeps working."),
		// TRANSLATORS: confirmation of the ChatGPT row's Disconnect in Preferences → Integrations.
		DisconnectHeading: tr.T("Disconnect ChatGPT?"),
		DisconnectBody:    tr.T("Malachi Mail signs out of ChatGPT on this computer. You can connect it again any time."),
	}
}

// calendarSettings names what a CalDAV row's Edit opens.
func calendarSettings(tr Translator) string {
	// TRANSLATORS: tooltip of a CalDAV account's Edit button in
	// Preferences → Integrations; it opens the Calendar page.
	return tr.T("Calendar Settings")
}
