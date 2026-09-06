// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package imap

import (
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-sasl"

	"github.com/schotek/malachi/backend/internal/auth"
	"github.com/schotek/malachi/backend/pkg/api"
)

// saslSession adds SASL XOAUTH2 to a memserver session, the way Gmail
// takes a token: the right token for the user logs the session in with
// the user's memserver password; a wrong one gets the JSON report Google
// sends, then the NO.
type saslSession struct {
	imapserver.Session
	username, password, token string
}

func (s *saslSession) AuthenticateMechanisms() []string { return []string{auth.XOAuth2} }

// Move keeps the memserver's MOVE reachable: the server panics on a
// connection whose session lacks an advertised extension, and embedding
// the interface hides it.
func (s *saslSession) Move(w *imapserver.MoveWriter, numSet imap.NumSet, dest string) error {
	return s.Session.(imapserver.SessionMove).Move(w, numSet, dest)
}

func (s *saslSession) Authenticate(mech string) (sasl.Server, error) {
	if mech != auth.XOAuth2 {
		return nil, imapserver.ErrAuthFailed
	}
	return &xoauth2Server{s: s}, nil
}

type xoauth2Server struct {
	s      *saslSession
	failed bool
}

func (x *xoauth2Server) Next(resp []byte) (challenge []byte, done bool, err error) {
	switch {
	case x.failed:
		return nil, false, imapserver.ErrAuthFailed
	case resp == nil:
		return []byte{}, false, nil // no initial response: ask for one
	case string(resp) == "user="+x.s.username+"\x01auth=Bearer "+x.s.token+"\x01\x01":
		return nil, true, x.s.Session.Login(x.s.username, x.s.password)
	default:
		x.failed = true
		return []byte(`{"status":"400","schemes":"Bearer","scope":"https://mail.google.com/"}`), false, nil
	}
}

// A refused token parks the syncer like a wrong password and tells the
// token source; the right one, after Wake, synchronises.
func TestSyncXOAuth2(t *testing.T) {
	h := newHarness(t, harnessOptions{token: "ya29.good"})
	h.append("INBOX", "From: a@example.test\r\nSubject: Token\r\n\r\nhi\r\n", time.Now())
	h.setPassword("ya29.bad", nil)
	h.start()
	h.waitStatus(api.SyncAuthRequired)
	select {
	case n := <-h.notes.authCh:
		if n.Reason != api.CodeAuthFailed || strings.Contains(n.Message, "ya29") {
			t.Fatalf("auth notification = %+v", n)
		}
	case <-time.After(waitTimeout):
		t.Fatal("no authRequired notification")
	}
	if h.authFailed.Load() != 1 {
		t.Fatalf("AuthFailed called %d times", h.authFailed.Load())
	}

	h.setPassword("ya29.good", nil)
	h.syncer.Wake()
	h.waitIdle(time.Time{})
	if got := h.messages(h.folder("INBOX").ID); len(got) != 1 || got[0].Subject != "Token" {
		t.Fatalf("messages after XOAUTH2 sign-in = %+v", got)
	}
	if h.authFailed.Load() != 1 {
		t.Fatalf("AuthFailed called %d times after success", h.authFailed.Load())
	}
}

// Without an OAuth2 mechanism the server is at fault, not the token:
// serverError, no sign-in notification.
func TestSyncXOAuth2WithoutMechanism(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	h.acc.Config.IMAP.AuthMethod = api.AuthOAuth2
	h.syncer = NewSyncer(h.acc, h.syncer.deps)
	h.start()
	h.waitStatus(api.SyncError)
	if st := h.syncer.State(); st.Error == nil || st.Error.Code != api.CodeServerError {
		t.Fatalf("state = %+v", st)
	}
	if h.authFailed.Load() != 0 || h.notes.authCount() != 0 {
		t.Fatalf("auth hooks fired: %d %d", h.authFailed.Load(), h.notes.authCount())
	}
}
