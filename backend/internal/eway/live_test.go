// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package eway

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestLive runs against a real eWay-CRM instance, only when
// MALACHI_TEST_EWAY (the service URL), MALACHI_TEST_EWAY_USER and
// MALACHI_TEST_EWAY_PASSWORD are set: it signs in, saves one synthetic
// message linked to nothing, finds it by its Message-ID, deletes it and
// signs out. Nothing of a real mailbox is sent.
func TestLive(t *testing.T) {
	ws, user, pw := os.Getenv("MALACHI_TEST_EWAY"), os.Getenv("MALACHI_TEST_EWAY_USER"), os.Getenv("MALACHI_TEST_EWAY_PASSWORD")
	if ws == "" || user == "" || pw == "" {
		t.Skip("MALACHI_TEST_EWAY, MALACHI_TEST_EWAY_USER and MALACHI_TEST_EWAY_PASSWORD not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	c, err := NewClient(Options{ServiceURL: ws, Username: user, ClientID: "malachi-live-test",
		Password: func(context.Context) (string, error) { return pw, nil }, UserAgent: UserAgent("live-test")})
	if err != nil {
		t.Fatal(err)
	}
	if s, err := c.DetectAuth(ctx); err != nil || s.AzureAuth {
		t.Fatalf("detect: %+v %v", s, err)
	}
	if _, err := c.SignIn(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.LogOut(context.Background())
	now := time.Now().Truncate(time.Second)
	mid := fmt.Sprintf("malachi-live-%d@malachi.invalid", now.UnixNano())
	raw := []byte("From: Malachi Live Test <live@malachi.invalid>\r\nTo: nobody@malachi.invalid\r\n" +
		"Subject: Malachi live test\r\nDate: " + now.Format(time.RFC1123Z) + "\r\nMessage-ID: <" + mid + ">\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nA synthetic message; delete it.\r\n")
	s, err := c.SaveMessage(ctx, Message{Raw: raw, FileName: "Malachi live test.eml", MessageID: mid, Subject: "Malachi live test",
		Sender: "live@malachi.invalid", To: []string{"nobody@malachi.invalid"}, SentOn: now, Received: now})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := c.Delete(context.Background(), s.GUID); err != nil {
			t.Errorf("delete: %v", err)
		}
	}()
	found, err := c.FindEmail(ctx, mid, "live@malachi.invalid", now)
	if err != nil || found == nil || found.GUID != s.GUID {
		t.Fatalf("find: %+v %v", found, err)
	}
	t.Logf("saved and found; open link %s", OpenLink(c.ServiceURL(), FolderEmails, s.GUID))
}
