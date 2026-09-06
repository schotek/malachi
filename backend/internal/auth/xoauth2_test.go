// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package auth

import "testing"

func TestXOAuth2Client(t *testing.T) {
	c := NewXOAuth2Client("me@example.org", "ya29.token")
	mech, ir, err := c.Start()
	if err != nil || mech != "XOAUTH2" {
		t.Fatalf("Start = %q, %v", mech, err)
	}
	if want := "user=me@example.org\x01auth=Bearer ya29.token\x01\x01"; string(ir) != want {
		t.Fatalf("initial response = %q, want %q", ir, want)
	}
	// The server's error report is acknowledged with an empty line, once.
	resp, err := c.Next([]byte(`{"status":"401","scope":"https://mail.google.com/"}`))
	if err != nil || resp == nil || len(resp) != 0 {
		t.Fatalf("Next = %q, %v", resp, err)
	}
	if _, err := c.Next(nil); err == nil {
		t.Fatal("a second challenge was accepted")
	}
}
