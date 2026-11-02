// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package updates

import (
	"reflect"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
)

const dl = "https://github.com/schotek/malachi/releases/download/v1.0.0/Malachi-Mail-1.0.0-macos-universal.dmg"

// msgids is a Translator that returns the msgid.
type msgids struct{}

func (msgids) T(m string) string { return m }
func (msgids) N(s, p string, n int) string {
	if n == 1 {
		return s
	}
	return p
}
func (msgids) C(_, m string) string { return m }

func info(security bool) *api.UpdateInfo {
	return &api.UpdateInfo{
		Version: "1.0.0", PublishedAt: time.Date(2026, 11, 1, 19, 0, 0, 0, time.UTC), Security: security,
		Notes: "  ## New\n- Things.\n", InfoURL: "https://malachimail.app/download.html", URL: dl, Size: 1,
		Prompt: security,
	}
}

// statuses are one of every state and offer.
func statuses(now time.Time) []*api.UpdateStatus {
	at := now.Add(-time.Hour)
	return []*api.UpdateStatus{
		nil,
		{State: api.UpdateDisabled},
		{Enabled: true, State: api.UpdateUnsupported},
		{Enabled: true, State: api.UpdateIdle},
		{Enabled: true, State: api.UpdateChecking},
		{Enabled: true, State: api.UpdateUpToDate, CheckedAt: &at},
		{Enabled: true, State: api.UpdateUpToDate},
		{Enabled: true, State: api.UpdateFailed, Failure: api.UpdateFailNetwork},
		{Enabled: true, State: api.UpdateAvailable, Available: info(false)},
		{Enabled: true, State: api.UpdateAvailable, Available: info(true)},
		{Enabled: true, State: api.UpdateUpToDate, Notice: &api.UpdateNotice{ID: "n-1", Text: "Hello.", URL: "https://malachimail.app/news.html"}},
		{Enabled: true, State: api.UpdateUpToDate, Notice: &api.UpdateNotice{ID: "n-2", Text: "Hello."}},
	}
}

func labels(b Banner) []Action {
	var out []Action
	for _, x := range b.Buttons {
		out = append(out, x.Action)
	}
	return out
}

func TestBannerFor(t *testing.T) {
	now := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	tr := msgids{}
	st := statuses(now)
	for i, want := range []BannerKind{BannerNone, BannerNone, BannerNone, BannerNone, BannerNone, BannerNone,
		BannerNone, BannerNone, BannerUpdate, BannerSecurity, BannerNotice, BannerNotice} {
		if got := BannerFor(st[i], now, tr); got.Kind != want {
			t.Errorf("status %d: %v, want %v", i, got.Kind, want)
		}
	}
	b := BannerFor(st[8], now, tr)
	if b.Title != "Malachi Mail 1.0.0 is available." || b.URL != dl || b.Version != "1.0.0" ||
		!reflect.DeepEqual(labels(b), []Action{ActionDownload, ActionWhatsNew, ActionLater, ActionSkip}) {
		t.Errorf("update banner %+v", b)
	}
	b = BannerFor(st[9], now, tr)
	if !reflect.DeepEqual(labels(b), []Action{ActionDownload, ActionWhatsNew}) {
		t.Errorf("security banner %+v", b)
	}
	b = BannerFor(st[10], now, tr)
	if b.Title != "Hello." || b.NoticeID != "n-1" || b.URL != "https://malachimail.app/news.html" ||
		!reflect.DeepEqual(labels(b), []Action{ActionMoreInfo, ActionClose}) {
		t.Errorf("notice banner %+v", b)
	}
	if b = BannerFor(st[11], now, tr); !reflect.DeepEqual(labels(b), []Action{ActionClose}) {
		t.Errorf("notice without a page %+v", b)
	}

	// Skipped or put off: the notice, if any, shows instead.
	later := now.Add(time.Hour)
	s := &api.UpdateStatus{Enabled: true, State: api.UpdateAvailable, Available: info(false),
		Notice: &api.UpdateNotice{ID: "n", Text: "x"}}
	s.Available.RemindAfter = &later
	if b := BannerFor(s, now, tr); b.Kind != BannerNotice {
		t.Errorf("put off: %v", b.Kind)
	}
	if b := BannerFor(s, later, tr); b.Kind != BannerUpdate {
		t.Errorf("after the reminder: %v", b.Kind)
	}
	s.Available.RemindAfter, s.Available.Skipped, s.Notice = nil, true, nil
	if b := BannerFor(s, now, tr); b.Shown() {
		t.Errorf("skipped: %v", b.Kind)
	}
	// A security release is never put off, whatever the status says.
	s.Available.Security = true
	if b := BannerFor(s, now, tr); b.Kind != BannerSecurity {
		t.Errorf("skipped security: %v", b.Kind)
	}
	// A URL off the allowed hosts gives no Download.
	s.Available.URL = "https://example.com/x.dmg"
	if b := BannerFor(s, now, tr); !reflect.DeepEqual(labels(b), []Action{ActionWhatsNew}) || b.URL != "" {
		t.Errorf("foreign URL %+v", b)
	}
	// Disabled: nothing, even with an offer.
	s.Enabled, s.State = false, api.UpdateDisabled
	if b := BannerFor(s, now, tr); b.Shown() {
		t.Error("disabled shows a banner")
	}
}

func TestPromptFor(t *testing.T) {
	now := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	st := statuses(now)
	for i, s := range st {
		if p := PromptFor(s, msgids{}); (p != nil) != (i == 9) {
			t.Errorf("status %d: %+v", i, p)
		}
	}
	p := PromptFor(st[9], msgids{})
	if p.URL != dl || p.Version != "1.0.0" || p.Quit == "" || p.Download != "Download" {
		t.Errorf("%+v", p)
	}
	s := *st[9]
	a := *s.Available
	a.Prompt = false
	s.Available = &a
	if PromptFor(&s, msgids{}) != nil {
		t.Error("asked twice")
	}
}

func TestNotesAndAbout(t *testing.T) {
	now := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	date := func(t time.Time) string { return t.Format("2006-01-02") }
	st := statuses(now)
	n := NotesFor(st[8], date, msgids{})
	if n.Title != "What's New in Malachi Mail 1.0.0" || n.Subtitle != "Released 2026-11-01" || n.Body != "## New\n- Things." ||
		n.URL != dl || n.InfoURL != NewsURL {
		t.Errorf("notes %+v", n)
	}
	if len(n.Blocks) != 2 || n.Blocks[0].Kind != assistant.BlockHeading || n.Blocks[1].Kind != assistant.BlockBullet {
		t.Errorf("blocks %+v", n.Blocks)
	}
	b := NotesBlocks("See [the site](https://malachimail.app/) and https://github.com/x.")
	for _, s := range b[0].Spans {
		if s.Link != "" {
			t.Errorf("a link stayed: %+v", s)
		}
	}
	if NotesFor(st[5], date, msgids{}) != nil {
		t.Error("notes without an offer")
	}
	for i, want := range []string{
		"", "Automatic update checks are off.", "This build does not check for updates.", "Not checked for updates yet.",
		"Checking for updates…", "You are using the latest version (checked 2026-11-02).", "You are using the latest version.",
		"Could not check for updates.", "Malachi Mail 1.0.0 is available.", "Malachi Mail 1.0.0 is available.",
	} {
		if got := AboutLine(st[i], date, msgids{}); got != want {
			t.Errorf("status %d: %q, want %q", i, got, want)
		}
	}
	for i, want := range []bool{false, false, false, true, false, true, true, true, true, true} {
		if got := CanCheckNow(st[i]); got != want {
			t.Errorf("CanCheckNow(status %d) = %v", i, got)
		}
	}
}

func TestOpenableURL(t *testing.T) {
	for s, ok := range map[string]bool{
		dl:                                 true,
		"https://malachimail.app/":         true,
		"http://malachimail.app/":          false,
		"https://malachimail.app:443/":     false,
		"https://user@github.com/":         false,
		"https://github.com/#x":            false,
		"https://GITHUB.com/":              false,
		"https://github.com.evil/":         false,
		"javascript:alert(1)":              false,
		"":                                 false,
		"https://github.com/a b":           false,
		"https://github.com\\@example.com": false,
	} {
		if _, got := OpenableURL(s); got != ok {
			t.Errorf("OpenableURL(%q) = %v", s, got)
		}
	}
}
