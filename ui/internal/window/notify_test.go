// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestNotificationText(t *testing.T) {
	n := api.NewMessageNotification{Message: api.MessageSummary{
		From:    []api.Address{{Name: "Alice Example", Address: "alice@example.invalid"}},
		Subject: "  Lunch?  ",
	}}
	title, body := notificationText(n)
	if title != "Alice Example" || body != "Lunch?" {
		t.Errorf("got %q / %q", title, body)
	}

	n.Message.From = nil
	n.Message.Subject = ""
	title, body = notificationText(n)
	if title != "New message" || body != "(No subject)" {
		t.Errorf("fallbacks: got %q / %q", title, body)
	}

	n.Message.Subject = strings.Repeat("ž", 500)
	_, body = notificationText(n)
	if len([]rune(body)) > notificationBodyMax+1 || !strings.HasSuffix(body, "…") || !strings.ContainsRune(body, 'ž') {
		t.Errorf("cap failed: len=%d", len(body))
	}
	if strings.Contains(body, "�") {
		t.Error("cap left an invalid UTF-8 sequence")
	}
}

func TestNearestInterval(t *testing.T) {
	cases := map[int]uint{0: 0, -5: 0, 60: 1, 300: 1, 500: 1, 700: 2, 900: 2, 1300: 2, 1400: 3, 1800: 3, 99999: 3}
	for in, want := range cases {
		if got := nearestInterval(in); got != want {
			t.Errorf("nearestInterval(%d) = %d, want %d", in, got, want)
		}
	}
	if indexOfPolicy(api.RemoteAllow) != 2 || indexOfPolicy("bogus") != 0 {
		t.Error("indexOfPolicy mapping wrong")
	}
}

func TestIndexOfRetention(t *testing.T) {
	cases := map[int]uint{7: 0, 10: 0, 30: 1, 60: 1, 90: 2, 200: 2, 365: 3, 1000: 3, 0: 4, -1: 4}
	for in, want := range cases {
		if got := indexOfRetention(in); got != want {
			t.Errorf("indexOfRetention(%d) = %d, want %d", in, got, want)
		}
	}
	for i, days := range retentionChoices {
		if got := indexOfRetention(days); got != uint(i) {
			t.Errorf("retentionChoices[%d]=%d maps back to %d", i, days, got)
		}
	}
}

func TestIndexOfAttachmentDays(t *testing.T) {
	// Small Attachments Only, 1 week, 1 month, 3 months, Everything; a tie
	// goes to the shorter.
	cases := map[int]uint{
		api.AttachmentOfflineNone: 0, -7: 0,
		1: 1, 7: 1, 18: 1, 19: 2, 30: 2, 60: 2, 61: 3, 90: 3, 365: 3, api.AttachmentOfflineDaysMax: 3,
		0: 4,
	}
	for in, want := range cases {
		if got := indexOfAttachmentDays(in); got != want {
			t.Errorf("indexOfAttachmentDays(%d) = %d, want %d", in, got, want)
		}
	}
	for i, days := range attachmentChoices {
		if got := indexOfAttachmentDays(days); got != uint(i) {
			t.Errorf("attachmentChoices[%d]=%d maps back to %d", i, days, got)
		}
	}
}

// A save sends the attachment days as the daemon confirmed them while the
// row still shows them; only a changed row sends its own value.
func TestAttachmentDaysToSave(t *testing.T) {
	cases := []struct {
		current  int
		selected uint
		want     int
	}{
		{14, 1, 14}, // shown as 1 week: another row was saved
		{14, 2, 30}, // changed to 1 month
		{14, 0, api.AttachmentOfflineNone},
		{14, 4, 0},
		{45, 2, 45},   // shown as 1 month
		{45, 3, 90},   // changed to 3 months
		{365, 3, 365}, // shown as 3 months
		{30, 2, 30},
		{30, 1, 7},
		{0, 4, 0},
		{0, 0, api.AttachmentOfflineNone},
		{api.AttachmentOfflineNone, 0, api.AttachmentOfflineNone},
		{api.AttachmentOfflineNone, 4, 0},
		{14, 5, 14},        // out of the table
		{14, ^uint(0), 14}, // nothing selected (GTK_INVALID_LIST_POSITION)
		{api.AttachmentOfflineDaysMax, 3, api.AttachmentOfflineDaysMax},
	}
	for _, c := range cases {
		if got := attachmentDaysToSave(c.current, c.selected); got != c.want {
			t.Errorf("attachmentDaysToSave(%d, %d) = %d, want %d", c.current, c.selected, got, c.want)
		}
	}
	// Every offered value survives a save of another row.
	for i, days := range attachmentChoices {
		if got := attachmentDaysToSave(days, uint(i)); got != days {
			t.Errorf("attachmentChoices[%d]=%d saved as %d", i, days, got)
		}
	}
}
