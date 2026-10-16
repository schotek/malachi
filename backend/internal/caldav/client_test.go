// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package caldav

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/caldav/caldavtest"
	"github.com/schotek/malachi/backend/pkg/api"
)

// newClient is a client of s with the right password.
func newClient(t *testing.T, s *caldavtest.Server, edit ...func(*Options)) *Client {
	t.Helper()
	opts := Options{
		ServerURL: s.ServerURL(), Username: caldavtest.Username,
		Password: func(context.Context) (string, error) { return caldavtest.Password, nil },
		HTTP:     s.HTTPClient(),
		Sleep:    func(context.Context, time.Duration) error { return nil },
	}
	for _, e := range edit {
		e(&opts)
	}
	c, err := NewClient(opts)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// event is a small VEVENT object.
func event(uid string, start time.Time) []byte {
	const f = "20060102T150405Z"
	return []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\nBEGIN:VEVENT\r\nUID:" + uid +
		"\r\nDTSTAMP:20261001T000000Z\r\nDTSTART:" + start.UTC().Format(f) +
		"\r\nDTEND:" + start.Add(time.Hour).UTC().Format(f) + "\r\nSUMMARY:Test " + uid +
		"\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
}

func code(err error) api.ErrorCode {
	if err == nil {
		return 0
	}
	return ToAPIError(err).Code
}

func TestNormaliseServerURL(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"cloud.example.org", "https://cloud.example.org"},
		{"  https://Cloud.Example.org/remote.php/dav/ ", "https://cloud.example.org/remote.php/dav"},
		{"https://cloud.example.org:8443/dav//", "https://cloud.example.org:8443/dav"},
		{"http://127.0.0.1:5232/", "http://127.0.0.1:5232"},
		{"http://localhost/dav", "http://localhost/dav"},
		{"HTTPS://x.test", "https://x.test"},
		{"http://cloud.example.org", ""},
		{"https://jana:pw@cloud.example.org", ""},
		{"https://cloud.example.org/?x=1", ""},
		{"https://cloud.example.org/#f", ""},
		{"ftp://cloud.example.org", ""},
		{"https://bad_host!/", ""},
		{"https://cloud.example.org/a/../b", ""},
		{"https://cloud.example.org:0/", ""},
		{"https://cloud.example.org/a b", ""},
		{"https://cloud.example.org/\x00", ""},
		{"", ""},
		{"https://x.test/" + strings.Repeat("a", MaxServerURLBytes), ""},
	} {
		got, err := NormaliseServerURL(tc.in)
		if tc.want == "" {
			if err == nil || code(err) != api.CodeInvalidArgument {
				t.Errorf("%q: got %q, %v; want invalidArgument", tc.in, got, err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%q: got %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}

func TestNewClientRules(t *testing.T) {
	pin := strings.Repeat("ab", 32)
	if _, err := NewClient(Options{ServerURL: "http://127.0.0.1:8080", CertificateSHA256: pin}); code(err) != api.CodeInvalidArgument {
		t.Errorf("pin with http: %v", err)
	}
	if _, err := NewClient(Options{ServerURL: "https://x.test", CertificateSHA256: "nope"}); code(err) != api.CodeInvalidArgument {
		t.Errorf("bad pin: %v", err)
	}
	if _, err := NewClient(Options{ServerURL: "https://x.test", Origins: []string{"http://y.test"}}); code(err) != api.CodeInvalidArgument {
		t.Errorf("http origin: %v", err)
	}
	if _, err := NewClient(Options{ServerURL: "https://x.test", CertificateSHA256: pin}); err != nil {
		t.Errorf("pinned https: %v", err)
	}
}

func TestToAPIError(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want api.ErrorCode
	}{
		{&StatusError{Status: 401}, api.CodeAuthFailed},
		{&StatusError{Status: 403}, api.CodeReadOnly},
		{&StatusError{Status: 404}, api.CodeEventGone},
		{&StatusError{Status: 410}, api.CodeEventGone},
		{&StatusError{Status: 412}, api.CodeConflict},
		{&StatusError{Status: 429}, api.CodeServerTimeout},
		{&StatusError{Status: 507}, api.CodeServerError},
		{&StatusError{Status: 500}, api.CodeServerError},
		{&StatusError{Status: 400}, api.CodeServerError},
		{ErrInvalidSyncToken, api.CodeServerError},
		{fmt.Errorf("x: %w", ErrTooLarge), api.CodeServerError},
		{ErrOtherOrigin, api.CodeServerError},
		{context.Canceled, api.CodeCancelled},
		{context.DeadlineExceeded, api.CodeServerTimeout},
		{errors.New("boom"), api.CodeNetworkError},
		{api.NewError(api.CodeKeyringError, "k"), api.CodeKeyringError},
	} {
		if got := code(tc.err); got != tc.want {
			t.Errorf("%v: %v, want %v", tc.err, got, tc.want)
		}
	}
	if ToAPIError(nil) != nil {
		t.Error("nil")
	}
}

func TestPasswordCallback(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Nextcloud)
	c := newClient(t, s, func(o *Options) {
		o.Password = func(context.Context) (string, error) { return "", api.NewError(api.CodeKeyringError, "locked") }
	})
	if _, err := c.ListCalendars(t.Context()); code(err) != api.CodeKeyringError {
		t.Fatalf("keyring error: %v", err)
	}
	c = newClient(t, s, func(o *Options) {
		o.Password = func(context.Context) (string, error) { return "", nil }
	})
	if _, err := c.ListCalendars(t.Context()); code(err) != api.CodeAuthRequired {
		t.Fatalf("empty password: %v", err)
	}
	if len(s.Requests()) != 0 {
		t.Fatalf("requests without a password: %d", len(s.Requests()))
	}
	c = newClient(t, s, func(o *Options) {
		o.Password = func(context.Context) (string, error) { return "wrong", nil }
	})
	if _, err := c.ListCalendars(t.Context()); code(err) != api.CodeAuthFailed {
		t.Fatalf("wrong password: %v", err)
	}
}

func TestOptions(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Nextcloud)
	caps, err := newClient(t, s).Options(t.Context(), "")
	if err != nil || !caps.CalendarAccess || !caps.AutoSchedule || caps.Status != http.StatusOK {
		t.Fatalf("nextcloud: %+v %v", caps, err)
	}
	r := caldavtest.New(t, caldavtest.Radicale)
	caps, err = newClient(t, r).Options(t.Context(), "")
	if err != nil || !caps.CalendarAccess || caps.AutoSchedule {
		t.Fatalf("radicale: %+v %v", caps, err)
	}
}
