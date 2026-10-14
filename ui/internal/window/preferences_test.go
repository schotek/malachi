// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"slices"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/boardtriage"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// The Disk Space Used row: the total as the value, one line of details
// per thing worth saying. i18n is not bound in tests, so the English
// msgids come back verbatim.
func TestStorageTexts(t *testing.T) {
	cases := []struct {
		name           string
		r              api.SystemStorageResult
		value, details string
	}{
		{"empty store", api.SystemStorageResult{}, "0 B", ""},
		{"uncompressed, everything local",
			api.SystemStorageResult{TotalBytes: 700 << 20, MessageBytes: 650 << 20, MessageUncompressedBytes: 650 << 20},
			"700.0 MiB", ""},
		{"compressed",
			api.SystemStorageResult{TotalBytes: 3 << 30, SavedBytes: 512 << 20},
			"3.0 GiB", "Compression saves 512.0 MiB"},
		{"attachments on the server",
			api.SystemStorageResult{TotalBytes: 3 << 30, RemoteAttachmentBytes: 2 << 30},
			"3.0 GiB", "2.0 GiB of attachments are on the server only"},
		{"both",
			api.SystemStorageResult{TotalBytes: 1<<30 + 512<<20, SavedBytes: 300 << 10, RemoteAttachmentBytes: 40 << 20},
			"1.5 GiB", "Compression saves 300 KiB\n40.0 MiB of attachments are on the server only"},
		{"no saving to speak of", api.SystemStorageResult{TotalBytes: 5, SavedBytes: -3}, "5 B", ""},
		{"converting",
			api.SystemStorageResult{TotalBytes: 3 << 30, SavedBytes: 512 << 20, Conversion: api.StorageConversionRunning},
			"3.0 GiB", "Converting the stored mail in the background\nCompression saves 512.0 MiB"},
		{"disk full",
			api.SystemStorageResult{TotalBytes: 3 << 30, Conversion: api.StorageConversionNoSpace},
			"3.0 GiB", "Converting stopped: the disk is full"},
		{"idle says nothing", api.SystemStorageResult{TotalBytes: 5, Conversion: api.StorageConversionIdle}, "5 B", ""},
	}
	for _, c := range cases {
		value, details := storageTexts(c.r)
		if value != c.value || details != c.details {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", c.name, value, details, c.value, c.details)
		}
	}
}

// The Keep Attachments Offline For row is insensitive only while the
// daemon confirms that attachments are never stored.
func TestAttachmentDaysApply(t *testing.T) {
	cases := []struct {
		name string
		p    api.Preferences
		want bool
	}{
		{"older daemon, field absent", api.Preferences{AttachmentOfflineDays: api.Ptr(30)}, true},
		{"never store off", api.Preferences{AttachmentOfflineDays: api.Ptr(30), NeverStoreAttachments: api.Ptr(false)}, true},
		{"never store on", api.Preferences{AttachmentOfflineDays: api.Ptr(30), NeverStoreAttachments: api.Ptr(true)}, false},
		{"on, days unknown", api.Preferences{NeverStoreAttachments: api.Ptr(true)}, false},
	}
	for _, c := range cases {
		if got := attachmentDaysApply(c.p); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// The Board group of Preferences → AI (macOS AIPaneViewController
// updateBoardGroup): shown while triage is offered; turning off is always
// possible once the daemon's preferences are known, turning on needs a
// triage that can run; automatic triage needs consent, its interval and
// cap need it on; nothing changes while consent is being given.
func TestBoardGroupFor(t *testing.T) {
	known := func(auto bool, minutes, cases int) api.BoardPreferences {
		p := api.DefaultBoardPreferences()
		p.Assistant, p.AutoTriage, p.AutoTriageMinutes, p.AutoTriageDailyCases = true, auto, minutes, cases
		return p
	}
	ready := boardtriage.View{Control: boardtriage.ControlTriage}
	running := boardtriage.View{Control: boardtriage.ControlStop, Running: true}
	signedOut := boardtriage.View{Control: boardtriage.ControlSignIn}
	type want struct {
		consentOn, consentSensitive, autoOn, autoSensitive, scheduleSensitive bool
		minutes, cases                                                        int
	}
	cases := []struct {
		name string
		in   boardGroupInputs
		want want
	}{
		{"preferences not known yet", boardGroupInputs{view: ready},
			want{minutes: 30, cases: 60}},
		{"no consent: only the consent can be turned on",
			boardGroupInputs{view: ready, prefs: known(false, 30, 60), prefsKnown: true},
			want{consentSensitive: true, minutes: 30, cases: 60}},
		{"consent: automatic triage can be turned on",
			boardGroupInputs{view: ready, prefs: known(false, 30, 60), prefsKnown: true, consentGiven: true},
			want{consentOn: true, consentSensitive: true, autoSensitive: true, minutes: 30, cases: 60}},
		{"automatic triage on: its schedule can be changed",
			boardGroupInputs{view: ready, prefs: known(true, 60, 150), prefsKnown: true, consentGiven: true},
			want{true, true, true, true, true, 60, 150}},
		{"a run under way is ready too",
			boardGroupInputs{view: running, prefs: known(true, 15, 20), prefsKnown: true, consentGiven: true},
			want{true, true, true, true, true, 15, 20}},
		{"a value outside the choices is kept",
			boardGroupInputs{view: ready, prefs: known(true, 45, 0), prefsKnown: true, consentGiven: true},
			want{true, true, true, true, true, 45, 0}},
		{"signed out: both can only be turned off",
			boardGroupInputs{view: signedOut, prefs: known(true, 30, 60), prefsKnown: true, consentGiven: true},
			want{consentOn: true, consentSensitive: true, autoOn: true, autoSensitive: true, minutes: 30, cases: 60}},
		{"signed out without consent: nothing to turn on",
			boardGroupInputs{view: signedOut, prefs: known(false, 30, 60), prefsKnown: true},
			want{minutes: 30, cases: 60}},
		{"automatic triage on without consent can be turned off",
			boardGroupInputs{view: ready, prefs: known(true, 30, 60), prefsKnown: true},
			want{consentSensitive: true, autoOn: true, autoSensitive: true, minutes: 30, cases: 60}},
		{"giving consent: the switch as asked, nothing changes meanwhile",
			boardGroupInputs{view: ready, prefs: known(false, 30, 60), prefsKnown: true, giving: true, switchOn: true},
			want{consentOn: true, minutes: 30, cases: 60}},
	}
	for _, c := range cases {
		st := boardGroupFor(c.in, i18n.Tr)
		got := want{st.consentOn, st.consentSensitive, st.autoOn, st.autoSensitive, st.scheduleSensitive, st.minutes, st.cases}
		if !st.shown || got != c.want {
			t.Errorf("%s: shown %v, got %+v, want %+v", c.name, st.shown, got, c.want)
		}
	}

	if st := boardGroupFor(boardGroupInputs{view: boardtriage.View{}, prefsKnown: true, consentGiven: true}, i18n.Tr); st != (boardGroupState{}) {
		t.Errorf("not offered: %+v, want the group hidden and nothing else", st)
	}
}

// The group's texts are the triage view's: the description says why
// triage cannot run, the status row the pause or the status line with
// today's count, and the tokens' row shows once a board.list said them.
func TestBoardGroupTexts(t *testing.T) {
	v := boardtriage.ViewOf(boardtriage.ViewInputs{Shown: true, ClaudeFound: false, Bridge: true}, i18n.Tr)
	if st := boardGroupFor(boardGroupInputs{view: v}, i18n.Tr); !st.shown || st.description != board.TriageSettingsNeedsClaudeCode(i18n.Tr) {
		t.Errorf("without Claude Code: %+v", st)
	}
	v = boardtriage.View{
		Control: boardtriage.ControlTriage, StatusLine: "line", TodayLine: "today",
		UsageShown: true, UsageValue: "1\u202f234", UsageDetail: "split\nruns", UsageToolTip: "only ours",
	}
	st := boardGroupFor(boardGroupInputs{view: v}, i18n.Tr)
	if st.description != "" || st.status != "line · today" || !st.usageShown || st.usageValue != "1\u202f234" ||
		st.usageDetail != "split\nruns" || st.usageToolTip != "only ours" {
		t.Errorf("texts: %+v", st)
	}
	v.Paused = "paused"
	if st := boardGroupFor(boardGroupInputs{view: v}, i18n.Tr); st.status != "paused" {
		t.Errorf("paused: status %q", st.status)
	}
}

// A value of the daemon's outside the offered ones is one more item, and
// the offered list is never changed.
func TestChoiceValues(t *testing.T) {
	offered := []int{15, 30, 60, 180}
	if got := choiceValues(offered, 30); !slices.Equal(got, offered) {
		t.Errorf("offered value: %v", got)
	}
	a := choiceValues(offered, 45)
	b := choiceValues(offered, 90)
	if !slices.Equal(a, []int{15, 30, 60, 180, 45}) || !slices.Equal(b, []int{15, 30, 60, 180, 90}) {
		t.Errorf("extra values: %v, %v", a, b)
	}
	if !slices.Equal(offered, []int{15, 30, 60, 180}) || !slices.Equal(boardTriageIntervals, []int{15, 30, 60, 180}) ||
		!slices.Equal(boardTriageDailyCaps, []int{20, 60, 150}) {
		t.Errorf("the offered lists changed: %v %v %v", offered, boardTriageIntervals, boardTriageDailyCaps)
	}
}
