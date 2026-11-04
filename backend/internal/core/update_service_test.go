// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/update/updatetest"
	"github.com/schotek/malachi/backend/pkg/api"
)

type updateRecorder struct {
	boardRecorder
	n atomic.Int32
}

func (r *updateRecorder) UpdateChanged(api.UpdateChangedNotification) { r.n.Add(1) }

const updPayload = `{"schema":1,"issuedAt":"%ISSUED%","releases":[
 {"version":"0.1.4","publishedAt":"2026-10-30T10:00:00Z","security":false,"withdrawn":false,
  "notes":{"en":"Four.","cs":"Čtyři."},"infoUrl":"https://malachimail.app/download.html",
  "packages":{"macos-dmg":{"url":"https://github.com/schotek/malachi/releases/download/v0.1.4/a.dmg","sha256":"%SHA%","size":10}}},
 {"version":"0.1.3","publishedAt":"2026-10-28T10:00:00Z","security":%SEC%,"withdrawn":false,
  "notes":{"en":"Three."},"infoUrl":"https://malachimail.app/download.html",
  "packages":{"macos-dmg":{"url":"https://github.com/schotek/malachi/releases/download/v0.1.3/a.dmg","sha256":"%SHA%","size":10}}}],
 "notice":{"id":"n-1","below":"0.1.3","text":{"en":"Please update.","cs":"Aktualizujte."}}}`

func updManifest(issued string, security bool) []byte {
	sec := "false"
	if security {
		sec = "true"
	}
	return []byte(strings.NewReplacer("%ISSUED%", issued, "%SEC%", sec, "%SHA%", strings.Repeat("ab", 32)).Replace(updPayload))
}

func newUpdateBackend(t *testing.T) (*Backend, *updatetest.Server, *updateRecorder, *time.Time) {
	t.Helper()
	b := newTestBackend(t, config.Default())
	b.Version = "0.1.2"
	s := updatetest.New(t)
	now := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	b.Updates = UpdateSettings{Channel: "macos-dmg", Client: s.Client(), Keys: s.Keys(), Now: func() time.Time { return now }}
	rec := &updateRecorder{}
	b.SetNotifier(rec)
	return b, s, rec, &now
}

func TestUpdateCheck(t *testing.T) {
	ctx := context.Background()
	b, s, rec, now := newUpdateBackend(t)
	svc := b.Update()

	st, err := svc.Status(ctx, api.UpdateStatusParams{})
	if err != nil || st.State != api.UpdateIdle || !st.Enabled || st.Channel != "macos-dmg" || st.Current != "0.1.2" {
		t.Fatalf("before a check: %+v %v", st, err)
	}

	// Nothing published: the API and the fallback answer 503.
	st, _ = svc.Check(ctx, api.UpdateCheckParams{})
	if st.State != api.UpdateFailed || st.Failure != api.UpdateFailServer {
		t.Fatalf("nothing published: %+v", st)
	}
	if s.Requests("/api/v1/updates") != 1 || s.Requests("/updates.json") != 1 {
		t.Errorf("requests: api %d, fallback %d", s.Requests("/api/v1/updates"), s.Requests("/updates.json"))
	}

	s.Publish(updManifest("2026-11-01T00:00:00Z", false))
	// Within a minute of the last manual check nothing is asked.
	st, _ = svc.Check(ctx, api.UpdateCheckParams{})
	if st.State != api.UpdateFailed || s.Requests("/api/v1/updates") != 1 {
		t.Fatalf("second check within a minute: %+v", st)
	}
	*now = now.Add(time.Minute)
	st, _ = svc.Check(ctx, api.UpdateCheckParams{Locale: "cs-CZ"})
	if st.State != api.UpdateAvailable || st.Failure != "" || st.CheckedAt == nil {
		t.Fatalf("published: %+v", st)
	}
	a := st.Available
	if a.Version != "0.1.4" || a.Security || a.Notes != "Čtyři." || a.Size != 10 || a.Prompt ||
		a.URL != "https://github.com/schotek/malachi/releases/download/v0.1.4/a.dmg" {
		t.Fatalf("available %+v", a)
	}
	if st.Notice == nil || st.Notice.ID != "n-1" || st.Notice.Text != "Aktualizujte." {
		t.Fatalf("notice %+v", st.Notice)
	}
	if rec.n.Load() < 4 {
		t.Errorf("notifications: %d", rec.n.Load())
	}

	// Later, then skip.
	st, err = svc.Dismiss(ctx, api.UpdateDismissParams{Version: "0.1.4", Action: api.UpdateLater})
	if err != nil || st.Available.RemindAfter == nil || !st.Available.RemindAfter.Equal(now.Add(72*time.Hour)) {
		t.Fatalf("later: %+v %v", st, err)
	}
	*now = now.Add(time.Minute)
	if st, _ = svc.Check(ctx, api.UpdateCheckParams{}); st.Available.RemindAfter != nil {
		t.Errorf("a check by hand keeps the later: %+v", st.Available)
	}
	if _, err := svc.Dismiss(ctx, api.UpdateDismissParams{Version: "0.1.4", Action: api.UpdateLater}); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(73 * time.Hour)
	if st, _ = svc.Status(ctx, api.UpdateStatusParams{}); st.Available.RemindAfter != nil {
		t.Errorf("remind after passed: %+v", st.Available)
	}
	if st, _ = svc.Dismiss(ctx, api.UpdateDismissParams{Version: "0.1.4", Action: api.UpdateSkip}); !st.Available.Skipped {
		t.Errorf("skip: %+v", st.Available)
	}
	if _, err := svc.Dismiss(ctx, api.UpdateDismissParams{Version: "0.1.3", Action: api.UpdateSkip}); errCode(t, err) != api.CodeInvalidArgument {
		t.Error("skipping a version not offered")
	}
	if _, err := svc.Dismiss(ctx, api.UpdateDismissParams{Version: "0.1.4", Action: "never"}); errCode(t, err) != api.CodeInvalidArgument {
		t.Error("unknown action")
	}
	if st, _ = svc.DismissNotice(ctx, api.UpdateDismissNoticeParams{ID: "n-1"}); st.Notice != nil {
		t.Errorf("notice after dismissal: %+v", st.Notice)
	}

	// Unchanged: 304.
	*now = now.Add(time.Minute)
	if st, _ = svc.Check(ctx, api.UpdateCheckParams{}); st.State != api.UpdateAvailable || st.Failure != "" {
		t.Fatalf("304: %+v", st)
	}

	// A replayed older manifest is refused and the newer one kept.
	s.Publish(updManifest("2026-10-31T00:00:00Z", false))
	*now = now.Add(time.Minute)
	if st, _ = svc.Check(ctx, api.UpdateCheckParams{}); st.Failure != api.UpdateFailFormat || st.Available == nil || st.Available.Version != "0.1.4" {
		t.Fatalf("replay: %+v", st)
	}

	// Another key: the API's answer does not verify, nor the fallback's.
	s.PublishEnvelope(updatetest.Sign(otherKey(t), updatetest.KeyID, updManifest("2026-11-02T00:00:00Z", false)))
	*now = now.Add(time.Minute)
	if st, _ = svc.Check(ctx, api.UpdateCheckParams{}); st.Failure != api.UpdateFailSignature || st.Available.Version != "0.1.4" {
		t.Fatalf("bad signature: %+v", st)
	}

	// The API down: the fallback serves.
	s.Publish(updManifest("2026-11-02T00:00:00Z", true))
	s.FailAPI(http.StatusBadGateway)
	*now = now.Add(time.Minute)
	st, _ = svc.Check(ctx, api.UpdateCheckParams{})
	if st.Failure != "" || st.Available == nil || !st.Available.Security || !st.Available.Prompt || st.Available.Skipped {
		t.Fatalf("security via fallback: %+v %+v", st, st.Available)
	}
	if _, err := svc.Dismiss(ctx, api.UpdateDismissParams{Version: "0.1.4", Action: api.UpdateLater}); errCode(t, err) != api.CodeInvalidArgument {
		t.Error("later of a security release")
	}
	if st, _ = svc.Dismiss(ctx, api.UpdateDismissParams{Version: "0.1.4", Action: api.UpdatePrompted}); st.Available.Prompt {
		t.Error("prompted twice")
	}

	// The state survives a restart of the daemon.
	b2 := New("0.1.2", b.store, config.Default(), nil)
	b2.Updates = UpdateSettings{Channel: "macos-dmg", Keys: s.Keys(), Now: b.Updates.Now}
	if st, _ = b2.Update().Status(ctx, api.UpdateStatusParams{}); st.Available == nil || st.Available.Prompt || st.CheckedAt == nil {
		t.Fatalf("after a restart: %+v", st)
	}

	// Off.
	off := false
	if st, _ = svc.SetPrefs(ctx, api.UpdateSetPrefsParams{Enabled: &off}); st.State != api.UpdateDisabled || st.Available != nil {
		t.Fatalf("disabled: %+v", st)
	}
	before := s.Requests("/api/v1/updates")
	*now = now.Add(time.Hour)
	svc.Check(ctx, api.UpdateCheckParams{})
	if s.Requests("/api/v1/updates") != before {
		t.Error("a disabled check asked")
	}
}

func TestUpdateUnsupported(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct{ version, channel string }{
		{"0.1.2-3-gabc1234", "macos-dmg"}, {"dev", "macos-dmg"}, {"0.1.2", ""}, {"0.1.2", "windows-zip-x64"},
	} {
		b, s, _, _ := newUpdateBackend(t)
		b.Version, b.Updates.Channel = c.version, c.channel
		st, _ := b.Update().Check(ctx, api.UpdateCheckParams{})
		if st.State != api.UpdateUnsupported || s.Requests("/api/v1/updates") != 0 {
			t.Errorf("%+v: %+v", c, st)
		}
		if done := b.startUpdates(ctx); done != nil {
			<-done // returns at once
		}
	}
}

func TestNextUpdateCheck(t *testing.T) {
	start := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	if got := nextUpdateCheck(start, storedUpdate{}, 0, 0); !got.Equal(start.Add(5 * time.Minute)) {
		t.Errorf("first: %v", got)
	}
	if got := nextUpdateCheck(start, storedUpdate{}, 0.999999, 0); got.Sub(start) > 15*time.Minute {
		t.Errorf("first, late: %v", got)
	}
	recent := start.Add(-time.Hour)
	if got := nextUpdateCheck(start, storedUpdate{AttemptAt: &recent}, 0, 0.5); !got.Equal(recent.Add(24 * time.Hour)) {
		t.Errorf("a day after the last: %v", got)
	}
	old := start.Add(-48 * time.Hour)
	if got := nextUpdateCheck(start, storedUpdate{AttemptAt: &old}, 0, 0); !got.Equal(start.Add(5 * time.Minute)) {
		t.Errorf("long ago: %v", got)
	}
	nb := start.Add(30 * time.Hour)
	if got := nextUpdateCheck(start, storedUpdate{AttemptAt: &recent, NotBefore: &nb}, 0, 0.5); !got.Equal(nb) {
		t.Errorf("retry-after: %v", got)
	}
}

func otherKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, k, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}
