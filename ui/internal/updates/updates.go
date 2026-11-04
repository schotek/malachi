// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package updates is the view logic of the update check
// (docs/plans/update-check.md): the banner in the main window, the
// question a security release asks once, the What's New dialog, the
// Updates group of Preferences → General (switch, status line, Check
// Now), the status line of the About window, and the first-run note.
//
// The daemon checks and decides what is offered (update.*, docs/api.md
// §4.20); this package only turns an api.UpdateStatus into texts and small
// view models. It is pure (no GTK, no gettext, no cgo: the caller passes a
// Translator) and the reference that is ported one to one to macOS
// (macos/Sources/MalachiCore/Updates/) and Windows
// (windows/src/Malachi.Core/Updates/).
//
// The flow every client follows:
//
//   - at start and on every notify.updateChanged, call update.status with
//     its locale and keep the result;
//   - show BannerFor(status, now) at the top of the main window in every
//     mode (Mail, Board, Calendar), nothing in other windows;
//   - when PromptFor(status) is not nil, ask it once (download, then quit)
//     and call update.dismiss {version, "prompted"} whatever the answer,
//     so that no other window or client asks again;
//   - Download opens Available.URL in the browser (OpenableURL), What's
//     New shows NotesFor, Later and Skip call update.dismiss, a notice's
//     Close calls update.dismissNotice;
//   - Preferences → General shows AboutLine with Check Now (update.check,
//     a 75 s timeout: the reply comes when the check ends); the About
//     window shows AboutLine where it has room for it.
//
// The notes and the notice text come from the signed manifest and were
// checked by the daemon; they are still shown as plain text only.
package updates

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/jira"
)

// Translator translates a msgid of the malachi domain (jira.Translator).
type Translator = jira.Translator

// BannerKind is what the banner is, which picks its look.
type BannerKind int

const (
	// BannerNone: no banner.
	BannerNone BannerKind = iota
	// BannerUpdate: a newer version (information).
	BannerUpdate
	// BannerSecurity: a security release (warning); it stays until the
	// update is installed.
	BannerSecurity
	// BannerNotice: a message of the project (information).
	BannerNotice
)

// Action is what a banner button does.
type Action int

const (
	// ActionDownload opens Banner.URL (the package) in the browser.
	ActionDownload Action = iota + 1
	// ActionWhatsNew shows NotesFor.
	ActionWhatsNew
	// ActionLater calls update.dismiss {Banner.Version, "later"}.
	ActionLater
	// ActionSkip calls update.dismiss {Banner.Version, "skip"}.
	ActionSkip
	// ActionMoreInfo opens Banner.URL (the notice's page).
	ActionMoreInfo
	// ActionClose calls update.dismissNotice {Banner.NoticeID}.
	ActionClose
)

// Button is one button of the banner, in display order; the first is the
// default one.
type Button struct {
	Action Action
	Label  string
}

// Banner is the bar above the message list.
type Banner struct {
	Kind     BannerKind
	Title    string
	Buttons  []Button
	Version  string // BannerUpdate, BannerSecurity
	URL      string // the package, or the notice's page; "" when none
	NoticeID string // BannerNotice
}

// Shown reports a banner to show.
func (b Banner) Shown() bool { return b.Kind != BannerNone }

// active: the check is on and the build has a channel.
func active(s *api.UpdateStatus) bool {
	return s != nil && s.Enabled && s.State != api.UpdateDisabled && s.State != api.UpdateUnsupported
}

// BannerFor is the banner of status at now: an offered version that the
// user has not skipped or put off, else a notice not yet dismissed, else
// none. A security release is never skipped or put off.
func BannerFor(s *api.UpdateStatus, now time.Time, tr Translator) Banner {
	if !active(s) {
		return Banner{}
	}
	if a := s.Available; a != nil {
		url, _ := OpenableURL(a.URL)
		if a.Security {
			b := Banner{
				Kind: BannerSecurity,
				// TRANSLATORS: %s is a version number such as 0.2.1
				Title:   fmt.Sprintf(tr.T("Malachi Mail %s fixes a security problem. Please update."), a.Version),
				Version: a.Version,
				URL:     url,
			}
			if url != "" {
				b.Buttons = append(b.Buttons, Button{ActionDownload, tr.T("Download")})
			}
			b.Buttons = append(b.Buttons, Button{ActionWhatsNew, tr.T("What's New")})
			return b
		}
		putOff := a.RemindAfter != nil && now.Before(*a.RemindAfter)
		if !a.Skipped && !putOff {
			b := Banner{
				Kind: BannerUpdate,
				// TRANSLATORS: %s is a version number such as 0.2.1
				Title:   fmt.Sprintf(tr.T("Malachi Mail %s is available."), a.Version),
				Version: a.Version,
				URL:     url,
			}
			if url != "" {
				b.Buttons = append(b.Buttons, Button{ActionDownload, tr.T("Download")})
			}
			b.Buttons = append(b.Buttons,
				Button{ActionWhatsNew, tr.T("What's New")},
				Button{ActionLater, tr.T("Remind Me Later")},
				Button{ActionSkip, tr.T("Skip This Version")})
			return b
		}
	}
	if n := s.Notice; n != nil && strings.TrimSpace(n.Text) != "" {
		b := Banner{Kind: BannerNotice, Title: n.Text, NoticeID: n.ID}
		if u, ok := OpenableURL(n.URL); ok {
			b.URL = u
			b.Buttons = append(b.Buttons, Button{ActionMoreInfo, tr.T("More Information")})
		}
		b.Buttons = append(b.Buttons, Button{ActionClose, tr.T("Close")})
		return b
	}
	return Banner{}
}

// Prompt is the question a security release asks once.
type Prompt struct {
	Version  string
	URL      string // the package
	Title    string
	Body     string
	Download string // the default button
	Cancel   string
	// Quit labels the check box, on by default: after the browser opened
	// the download, the app quits so that the installer can replace it.
	Quit string
}

// PromptFor is the question to ask: a security release no client has
// asked about yet (UpdateInfo.Prompt) with a package to open. nil: none.
func PromptFor(s *api.UpdateStatus, tr Translator) *Prompt {
	if !active(s) || s.Available == nil || !s.Available.Security || !s.Available.Prompt {
		return nil
	}
	a := s.Available
	u, ok := OpenableURL(a.URL)
	if !ok {
		return nil
	}
	return &Prompt{
		Version: a.Version,
		URL:     u,
		Title:   tr.T("Security Update Available"),
		// TRANSLATORS: %s is a version number such as 0.2.1
		Body: fmt.Sprintf(tr.T("Malachi Mail %s fixes a security problem. Download the installer and install it once Malachi Mail has quit."),
			a.Version),
		Download: tr.T("Download"),
		Cancel:   tr.T("Cancel"),
		Quit:     tr.T("Quit Malachi Mail after the download starts"),
	}
}

// Notes is the What's New dialog.
type Notes struct {
	Title    string
	Subtitle string // the release date
	Body     string // the notes as written (Markdown)
	// Blocks are Body read as Markdown (assistant.Markdown: headings,
	// lists, bold, italic, code, never HTML), with every link as plain
	// text: the way out of the dialog is More Information.
	Blocks   []assistant.Block
	Download string
	URL      string // the package; "" hides Download
	MoreInfo string
	InfoURL  string // NewsURL; "" hides More Information
	Close    string
}

// NotesFor is the What's New dialog of the offered version; date formats
// the release date in the user's locale. nil: nothing is offered.
func NotesFor(s *api.UpdateStatus, date func(time.Time) string, tr Translator) *Notes {
	if !active(s) || s.Available == nil {
		return nil
	}
	a := s.Available
	n := &Notes{
		// TRANSLATORS: %s is a version number such as 0.2.1
		Title: fmt.Sprintf(tr.T("What's New in Malachi Mail %s"), a.Version),
		// TRANSLATORS: %s is a date
		Subtitle: fmt.Sprintf(tr.T("Released %s"), date(a.PublishedAt)),
		Body:     strings.TrimSpace(a.Notes),
		Download: tr.T("Download"),
		MoreInfo: tr.T("More Information"),
		Close:    tr.T("Close"),
	}
	n.URL, _ = OpenableURL(a.URL)
	n.Blocks = NotesBlocks(n.Body)
	n.InfoURL = NewsURL
	return n
}

// NewsURL is the page More Information opens: the project's news, where
// every release is described (the owner's choice over the release's
// infoUrl, 2026-11-02).
const NewsURL = "https://malachimail.app/news.html"

// NotesBlocks reads notes as Markdown, with links as plain text.
func NotesBlocks(notes string) []assistant.Block {
	blocks := assistant.Markdown(notes)
	for i := range blocks {
		for j := range blocks[i].Spans {
			blocks[i].Spans[j].Link = ""
		}
	}
	return blocks
}

// AboutLine is the status line of the About window; date formats the
// time of the last check.
func AboutLine(s *api.UpdateStatus, date func(time.Time) string, tr Translator) string {
	if s == nil {
		return ""
	}
	switch s.State {
	case api.UpdateDisabled:
		return tr.T("Automatic update checks are off.")
	case api.UpdateUnsupported:
		return tr.T("This build does not check for updates.")
	case api.UpdateChecking:
		return tr.T("Checking for updates…")
	case api.UpdateAvailable:
		if s.Available != nil {
			// TRANSLATORS: %s is a version number such as 0.2.1
			return fmt.Sprintf(tr.T("Malachi Mail %s is available."), s.Available.Version)
		}
	case api.UpdateFailed:
		return tr.T("Could not check for updates.")
	case api.UpdateUpToDate:
		if s.CheckedAt != nil {
			// TRANSLATORS: %s is the date and time of the last check
			return fmt.Sprintf(tr.T("You are using the latest version (checked %s)."), date(*s.CheckedAt))
		}
		return tr.T("You are using the latest version.")
	}
	return tr.T("Not checked for updates yet.")
}

// CheckNow labels the About window's button.
func CheckNow(tr Translator) string { return tr.T("Check Now") }

// CanCheckNow: Check Now is enabled (the check is on, the build has a
// channel, no check is running).
func CanCheckNow(s *api.UpdateStatus) bool {
	return active(s) && s.State != api.UpdateChecking
}

// PrefsGroup, PrefsTitle and PrefsSubtitle are the group, the switch and
// its explanation in Preferences → General.
func PrefsGroup(tr Translator) string { return tr.T("Updates") }

func PrefsTitle(tr Translator) string { return tr.T("Check for Updates Automatically") }

func PrefsSubtitle(tr Translator) string {
	return tr.T("Once a day, Malachi Mail asks its website which versions exist. It sends only its version number: nothing about you, your mail or your computer.")
}

// WizardNote is the sentence of the first-run assistant (U11).
func WizardNote(tr Translator) string {
	return tr.T("Malachi Mail checks once a day whether a new version exists and sends only its version number. You can turn this off in Preferences.")
}

// downloadHosts are the hosts a package or page may be on
// (docs/services.md §2.3); the daemon checked them already.
var downloadHosts = []string{"github.com", "malachimail.app"}

// OpenableURL is raw when it is an https URL on github.com or
// malachimail.app, exactly so (the daemon's rule, checked again before a
// browser opens it).
func OpenableURL(raw string) (string, bool) {
	if raw == "" || len(raw) > 2048 || !strings.HasPrefix(raw, "https://") || strings.ContainsAny(raw, "#\\") {
		return "", false
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] <= 0x20 || raw[i] >= 0x7f {
			return "", false
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" {
		return "", false
	}
	for _, h := range downloadHosts {
		if u.Host == h {
			return raw, true
		}
	}
	return "", false
}
