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
