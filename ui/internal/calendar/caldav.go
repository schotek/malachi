// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/certtrust"
)

// The Add CalDAV Calendar sheet: its flow (Wizard: entering →
// discovering → listed → adding → added), which fields and buttons are
// enabled, the certificate-trust offer (the rules of
// ui/internal/certtrust; the confirmation is the account assistant's), the
// account it adds, and its texts.

// WizardPhase is where the sheet is.
type WizardPhase int

// The phases.
const (
	// WizardEntering: the fields are editable, Discover Calendars is the
	// next step (after an error too).
	WizardEntering WizardPhase = iota
	// WizardDiscovering: account.discoverCalDAV, then
	// account.listCalDAVCalendars run (Step says which).
	WizardDiscovering
	// WizardListed: the calendars are listed with checkboxes; Add adds.
	WizardListed
	// WizardAdding: account.add runs.
	WizardAdding
	// WizardAdded: the account exists; the sheet closes.
	WizardAdded
)

// WizardStep is the call of WizardDiscovering.
type WizardStep int

// The steps.
const (
	StepDiscover WizardStep = iota
	StepList
)

// Wizard is the sheet's state. Start from NewWizard, change it only
// through its methods; the password lives here only while the sheet is
// open and goes to the daemon's keyring with account.add.
type Wizard struct {
	Phase WizardPhase
	Step  WizardStep
	// The fields.
	Name, Server, Username, Password string
	// Discovered is account.discoverCalDAV's answer (nil before).
	Discovered *api.AccountDiscoverCalDAVResult
	// Listed is account.listCalDAVCalendars's answer (nil before).
	Listed *api.AccountListCalDAVCalendarsResult
	// Selected are the hrefs to synchronise.
	Selected map[string]bool
	// MailAccount is the mail account invitations leave from (client
	// scheduling); "" none.
	MailAccount api.AccountID
	// Pin is the certificate the user trusted ("" none).
	Pin string
	// Err is the last call's error (nil none); AccountID the added
	// account.
	Err       *api.Error
	AccountID api.AccountID
}

// NewWizard is an empty sheet; mailAccount is the default mail account
// for invitations (the first compose account, "" none).
func NewWizard(mailAccount api.AccountID) *Wizard {
	return &Wizard{Selected: map[string]bool{}, MailAccount: mailAccount}
}

// FieldsEnabled reports editable fields (not while a call runs).
func (w *Wizard) FieldsEnabled() bool {
	return w.Phase == WizardEntering || w.Phase == WizardListed
}

// Edit takes the fields. A change of the server, the username or the
// password drops what was found (back to entering); the trusted
// certificate stays only while the server's host is the same. Ignored
// while a call runs.
func (w *Wizard) Edit(name, server, username, password string) {
	if !w.FieldsEnabled() {
		return
	}
	changed := strings.TrimSpace(server) != strings.TrimSpace(w.Server) || username != w.Username || password != w.Password
	if !strings.EqualFold(serverHost(server), serverHost(w.Server)) {
		w.Pin = ""
	}
	w.Name, w.Server, w.Username, w.Password = name, server, username, password
	if changed {
		w.Phase, w.Discovered, w.Listed, w.Err = WizardEntering, nil, nil, nil
		clear(w.Selected)
	}
}

// serverHost is the host of a typed server ("cloud.example.org",
// "https://cloud.example.org/dav").
func serverHost(s string) string {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// typedURL is the typed server as a URL (https assumed).
func typedURL(s string) string {
	s = strings.TrimRight(strings.TrimSpace(s), "/")
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	return s
}

// CanDiscover reports Discover Calendars enabled: editable, a server, a
// username and a password.
func (w *Wizard) CanDiscover() bool {
	return w.FieldsEnabled() && serverHost(w.Server) != "" && strings.TrimSpace(w.Username) != "" && w.Password != ""
}

// BeginDiscover starts discovery: account.discoverCalDAV
// (DiscoverParams), then account.listCalDAVCalendars (ListParams). False
// when it cannot start.
func (w *Wizard) BeginDiscover() bool {
	if !w.CanDiscover() {
		return false
	}
	w.Phase, w.Step, w.Err, w.Discovered, w.Listed = WizardDiscovering, StepDiscover, nil, nil, nil
	return true
}

// DiscoverParams are account.discoverCalDAV's params, with the trusted
// certificate of the typed server.
func (w *Wizard) DiscoverParams() api.AccountDiscoverCalDAVParams {
	return api.AccountDiscoverCalDAVParams{URL: strings.TrimSpace(w.Server), CertificateSHA256: w.Pin}
}

// pinFor is the pin for the server the account uses: the trusted one
// while that server is on the typed host (a pin is trusted for the typed
// server only, never for one a redirect named).
func (w *Wizard) pinFor() string {
	if w.Pin == "" || !strings.EqualFold(serverHost(w.ServerURL()), serverHost(w.Server)) {
		return ""
	}
	return w.Pin
}

// DiscoverDone takes account.discoverCalDAV's answer: an error ends in
// entering with Err; else the step goes on to the listing.
func (w *Wizard) DiscoverDone(res *api.AccountDiscoverCalDAVResult, err *api.Error) {
	if w.Phase != WizardDiscovering || w.Step != StepDiscover {
		return
	}
	if err != nil || res == nil {
		w.Phase, w.Err = WizardEntering, err
		return
	}
	w.Discovered, w.Step = res, StepList
}

// ServerURL is the server the account uses: the discovered one, else
// the typed one.
func (w *Wizard) ServerURL() string {
	if w.Discovered != nil && w.Discovered.ServerURL != "" {
		return w.Discovered.ServerURL
	}
	return typedURL(w.Server)
}

// config is the account's configuration.
func (w *Wizard) config() api.AccountConfig {
	cfg := api.AccountConfig{
		Kind: api.AccountCalDAV,
		Name: strings.TrimSpace(w.Name),
		CalDAV: &api.CalDAVConfig{
			ServerURL:         w.ServerURL(),
			Username:          strings.TrimSpace(w.Username),
			CertificateSHA256: w.pinFor(),
		},
	}
	if cfg.Name == "" {
		cfg.Name = serverHost(w.Server)
	}
	if w.Listed != nil {
		cfg.CalDAV.PrincipalURL, cfg.CalDAV.HomeSetURL = w.Listed.PrincipalURL, w.Listed.HomeSetURL
		if len(w.Listed.Addresses) > 0 {
			cfg.Email = w.Listed.Addresses[0]
		}
		if w.Listed.Scheduling == api.CalDAVSchedulingClient {
			cfg.CalDAV.MailAccountID = w.MailAccount
		}
	}
	if cfg.Email == "" && strings.Contains(cfg.CalDAV.Username, "@") {
		cfg.Email = cfg.CalDAV.Username
	}
	return cfg
}

// ListParams are account.listCalDAVCalendars's params.
func (w *Wizard) ListParams() api.AccountListCalDAVCalendarsParams {
	return api.AccountListCalDAVCalendarsParams{Config: w.config(), Credentials: api.Credentials{Password: w.Password}}
}

// ListDone takes account.listCalDAVCalendars's answer: an error ends in
// entering with Err; else listed with every calendar selected.
func (w *Wizard) ListDone(res *api.AccountListCalDAVCalendarsResult, err *api.Error) {
	if w.Phase != WizardDiscovering || w.Step != StepList {
		return
	}
	if err != nil || res == nil {
		w.Phase, w.Err = WizardEntering, err
		return
	}
	w.Phase, w.Listed = WizardListed, res
	clear(w.Selected)
	for _, c := range res.Calendars {
		w.Selected[c.Href] = true
	}
	if strings.TrimSpace(w.Name) == "" {
		if n := cleanLine(res.DisplayName); n != "" {
			w.Name = n
		}
	}
}

// Toggle selects or deselects a listed calendar.
func (w *Wizard) Toggle(href string, on bool) {
	if w.Phase != WizardListed || w.Listed == nil {
		return
	}
	if slices.ContainsFunc(w.Listed.Calendars, func(c api.CalDAVCalendar) bool { return c.Href == href }) {
		w.Selected[href] = on
	}
}

// SetMailAccount chooses the mail account for invitations.
func (w *Wizard) SetMailAccount(id api.AccountID) { w.MailAccount = id }

// ShowsMailAccount reports the mail-account popup: the server leaves the
// invitations to the client.
func (w *Wizard) ShowsMailAccount() bool {
	return w.Listed != nil && w.Listed.Scheduling == api.CalDAVSchedulingClient
}

// CanAdd reports Add enabled: listed, a calendar selected.
func (w *Wizard) CanAdd() bool {
	if w.Phase != WizardListed || w.Listed == nil {
		return false
	}
	for _, c := range w.Listed.Calendars {
		if w.Selected[c.Href] {
			return true
		}
	}
	return false
}

// BeginAdd starts account.add with AddParams; false when it cannot.
func (w *Wizard) BeginAdd() bool {
	if !w.CanAdd() {
		return false
	}
	w.Phase, w.Err = WizardAdding, nil
	return true
}

// AddParams are account.add's params: the configuration with the
// deselected calendars disabled, the server's default (when selected) as
// the default calendar and, when the listing found address books, the
// contacts on with their home set.
func (w *Wizard) AddParams() api.AccountAddParams {
	cfg := w.config()
	cal := &api.CalendarConfig{Sync: true}
	if w.Listed != nil {
		for _, c := range w.Listed.Calendars {
			switch {
			case !w.Selected[c.Href]:
				cal.DisabledCalendars = append(cal.DisabledCalendars, c.Href)
			case c.IsDefault:
				cal.DefaultCalendar = c.Href
			}
		}
	}
	cfg.Calendar = cal
	if w.Listed != nil && len(w.Listed.AddressBooks) > 0 {
		// The server's address books come with the account: its contacts
		// are on, the books chosen in Preferences → Contacts.
		cfg.CalDAV.AddressBookHomeSetURL, cfg.CalDAV.Contacts = w.Listed.AddressBookHomeSetURL, true
	}
	return api.AccountAddParams{Config: cfg, Credentials: api.Credentials{Password: w.Password}}
}

// AddDone takes account.add's answer: added, or back to listed with Err.
func (w *Wizard) AddDone(id api.AccountID, err *api.Error) {
	if w.Phase != WizardAdding {
		return
	}
	if err != nil {
		w.Phase, w.Err = WizardListed, err
		return
	}
	w.Phase, w.AccountID, w.Password = WizardAdded, id, ""
}

// TrustOffer is the certificate the sheet offers to trust after a
// tlsError: one with a valid fingerprint whose problem is the
// certificate (not the connection), at an https server, not the one
// already pinned. The Problem is what the confirmation shows.
func (w *Wizard) TrustOffer() (certtrust.Problem, bool) {
	if w.Phase != WizardEntering || w.Err == nil || w.Err.Code != api.CodeTLSError ||
		!strings.HasPrefix(strings.ToLower(w.ServerURL()), "https://") {
		return certtrust.Problem{}, false
	}
	p, ok := certtrust.Details(w.Err)
	if !ok || p.Cert == nil || p.Category() == certtrust.Connection || p.Cert.SHA256 == w.Pin {
		return certtrust.Problem{}, false
	}
	return p, true
}

// Trust pins the offered certificate after the user confirmed it and
// starts discovery again (docs/calendar.md §5.8): back to discovering,
// the caller sends DiscoverParams, which carry the pin, as do the list
// and add params after it. False when nothing was offered.
func (w *Wizard) Trust() bool {
	p, ok := w.TrustOffer()
	if !ok {
		return false
	}
	w.Pin, w.Err = p.Cert.SHA256, nil
	return w.BeginDiscover()
}

// ---------------------------------------------------------------------------
// Texts
// ---------------------------------------------------------------------------

// CalDAVTitle is the sheet's title.
func CalDAVTitle(tr Translator) string { return tr.T("Add CalDAV Calendar") }

// CalDAVSubtitle is the sheet's subtitle with the server examples.
func CalDAVSubtitle(tr Translator) string {
	return tr.T("Nextcloud, Fastmail, iCloud, Radicale and other servers that speak CalDAV.")
}

// CalDAVField is a field's label.
func CalDAVField(f CalDAVFieldKind, tr Translator) string {
	switch f {
	case CalDAVServer:
		return tr.T("Server Address")
	case CalDAVUsername:
		return tr.T("Username")
	case CalDAVPassword:
		return tr.T("Password")
	}
	return tr.T("Account Name")
}

// CalDAVFieldKind is a field of the sheet.
type CalDAVFieldKind int

// The fields, in the sheet's order.
const (
	CalDAVName CalDAVFieldKind = iota
	CalDAVServer
	CalDAVUsername
	CalDAVPassword
)

// ServerPlaceholder is the server field's example; not translated.
const ServerPlaceholder = "cloud.example.org"

// PasswordNote is the note under the password.
func PasswordNote(tr Translator) string {
	return tr.T("The password goes to the system keyring. Malachi Mail never shows it again.")
}

// DiscoverButton is the button that finds the calendars.
func DiscoverButton(tr Translator) string { return tr.T("Discover Calendars") }

// WizardProgress is the line while a call runs ("" otherwise).
func WizardProgress(w *Wizard, tr Translator) string {
	switch w.Phase {
	case WizardDiscovering:
		return tr.T("Looking for calendars…")
	case WizardAdding:
		return tr.T("Adding the account…")
	}
	return ""
}

// FoundVia is how the server was found, for FoundLine.
func FoundVia(v api.CalDAVFoundVia, tr Translator) string {
	switch v {
	case api.CalDAVFoundWellKnown:
		return "/.well-known/caldav"
	case api.CalDAVFoundSRV:
		// TRANSLATORS: how a CalDAV server was found: a DNS record.
		return tr.T("a DNS SRV record")
	}
	// TRANSLATORS: how a CalDAV server was found: at the address typed.
	return tr.T("the address you entered")
}

// FoundLine is the status line after the listing: "Found through
// /.well-known/caldav · 4 calendars"; "" before.
func FoundLine(w *Wizard, tr Translator) string {
	if w.Listed == nil {
		return ""
	}
	via := api.CalDAVFoundURL
	if w.Discovered != nil {
		via = w.Discovered.FoundVia
	}
	n := len(w.Listed.Calendars)
	// TRANSLATORS: the Add CalDAV sheet's status: how the server was found
	// and how many calendars it has.
	return fmt.Sprintf(tr.N("Found through %s · %d calendar", "Found through %s · %d calendars", n), FoundVia(via, tr), n)
}

// ServerChanged is the line when discovery ended at another host than
// the one typed (a .well-known redirect), shown before the password goes
// there.
func ServerChanged(w *Wizard, tr Translator) string {
	if w.Discovered == nil || strings.EqualFold(serverHost(w.Discovered.ServerURL), serverHost(w.Server)) {
		return ""
	}
	return fmt.Sprintf(tr.T("The calendars are at %s."), serverHost(w.Discovered.ServerURL))
}

// CertificateLabel is the certificate row's label.
func CertificateLabel(tr Translator) string { return tr.T("Certificate") }

// CertificateText is the certificate row's value: "Verified (Let's
// Encrypt R11)", "Trusted by you" for a pin, "" before discovery.
func CertificateText(w *Wizard, tr Translator) string {
	switch {
	case w.Pin != "":
		return tr.T("Trusted by you")
	case w.Discovered != nil && w.Discovered.Certificate != nil:
		if issuer := cleanLine(w.Discovered.Certificate.Issuer); issuer != "" {
			// TRANSLATORS: the certificate row of the Add CalDAV sheet; %s is
			// the certificate's issuer.
			return fmt.Sprintf(tr.T("Verified (%s)"), issuer)
		}
		return tr.T("Verified")
	case w.Discovered != nil && strings.HasPrefix(strings.ToLower(w.ServerURL()), "http://"):
		return tr.T("Not encrypted (local server)")
	}
	return ""
}

// TrustButton offers to trust the server's certificate.
func TrustButton(tr Translator) string { return tr.T("Trust Certificate…") }

// CalendarsHeading heads the list of found calendars.
func CalendarsHeading(tr Translator) string { return tr.T("Calendars") }

// ListedNote is the note after a found calendar's name: "default", "read
// only".
func ListedNote(c api.CalDAVCalendar, tr Translator) string {
	return CalendarRowNote(api.Calendar{IsDefault: c.IsDefault, Access: c.Access}, tr)
}

// CalDAVFooter is the sheet's footer note.
func CalDAVFooter(tr Translator) string {
	return tr.T("Not shown in Mail; in Calendar with the CALDAV capsule.")
}

// AddButton is the sheet's primary button.
func AddButton(tr Translator) string {
	// TRANSLATORS: the Add CalDAV sheet's button that adds the account.
	return tr.C("caldav", "Add")
}

// WizardErrorText is the sentence of the sheet's error ("" none).
func WizardErrorText(w *Wizard, tr Translator) string {
	if w.Err == nil {
		return ""
	}
	switch w.Err.Code {
	case api.CodeAuthFailed, api.CodeAuthRequired:
		return tr.T("The server did not accept the username or password.")
	case api.CodeTLSError:
		return tr.T("The server’s certificate is not trusted.")
	case api.CodeNetworkError, api.CodeServerTimeout, api.CodeOffline:
		return tr.T("The server could not be reached.")
	case api.CodeServerError:
		return tr.T("The server answered, but it is not a CalDAV server.")
	case api.CodeInvalidArgument:
		return tr.T("This is not a server address Malachi Mail can use.")
	case api.CodeKeyringError:
		return tr.T("The password could not be stored in the system keyring.")
	}
	return tr.T("The calendars could not be found.")
}
