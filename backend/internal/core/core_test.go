package core

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

func newTestBackend(t *testing.T, cfg config.Config) *Backend {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "store.db"), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New("test", st, cfg)
}

func errCode(t *testing.T, err error) api.ErrorCode {
	t.Helper()
	var e *api.Error
	if !errors.As(err, &e) {
		t.Fatalf("expected *api.Error, got %T: %v", err, err)
	}
	return e.Code
}

func TestConfigDefaultsFromToml(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()
	cfg.Sync.IntervalSeconds = 900
	b := newTestBackend(t, cfg)

	res, err := b.Config().Get(ctx, api.ConfigGetParams{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Preferences.SyncIntervalSeconds != 900 || res.Preferences.RemoteContent != api.RemoteBlock {
		t.Fatalf("defaults = %+v", res.Preferences)
	}
}

func TestConfigSetOverridesAndPersists(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())

	want := api.Preferences{SyncIntervalSeconds: 0, RemoteContent: api.RemoteKnownSenders}
	res, err := b.Config().Set(ctx, api.ConfigSetParams{Preferences: want})
	if err != nil {
		t.Fatal(err)
	}
	if res.Preferences != want {
		t.Fatalf("set echoed %+v", res.Preferences)
	}
	got, err := b.Config().Get(ctx, api.ConfigGetParams{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Preferences != want {
		t.Fatalf("get after set = %+v, want %+v", got.Preferences, want)
	}

	// The stored value wins over config.toml on a fresh backend over the same store.
	cfg := config.Default()
	cfg.Sync.IntervalSeconds = 123
	b2 := New("test", b.store, cfg)
	got, _ = b2.Config().Get(ctx, api.ConfigGetParams{})
	if got.Preferences.SyncIntervalSeconds != 0 {
		t.Fatalf("store did not take precedence over config.toml: %+v", got.Preferences)
	}
}

func TestConfigSetValidation(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	cases := []api.Preferences{
		{SyncIntervalSeconds: 30, RemoteContent: api.RemoteBlock},
		{SyncIntervalSeconds: -1, RemoteContent: api.RemoteBlock},
		{SyncIntervalSeconds: 300, RemoteContent: "sometimes"},
		{SyncIntervalSeconds: 300},
	}
	for _, p := range cases {
		_, err := b.Config().Set(ctx, api.ConfigSetParams{Preferences: p})
		if code := errCode(t, err); code != api.CodeInvalidArgument {
			t.Errorf("Set(%+v): code %d, want invalidArgument", p, code)
		}
	}
}

func TestSenders(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	s := b.Senders()

	if _, err := s.Add(ctx, api.SenderAddParams{Address: "Alice Example <Alice@Example.invalid>"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(ctx, api.SenderAddParams{Address: "not an address"}); errCode(t, err) != api.CodeInvalidArgument {
		t.Error("garbage address accepted")
	}
	list, err := s.List(ctx, api.SenderListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Senders) != 1 || list.Senders[0].Address != "alice@example.invalid" ||
		list.Senders[0].Source != api.KnownSenderSourceUser {
		t.Fatalf("list = %+v", list.Senders)
	}
	if _, err := s.Remove(ctx, api.SenderRemoveParams{Address: "alice@example.invalid"}); err != nil {
		t.Fatal(err)
	}
	list, _ = s.List(ctx, api.SenderListParams{})
	if len(list.Senders) != 0 {
		t.Fatalf("list after remove = %+v", list.Senders)
	}
}

func TestResolveRemoteContent(t *testing.T) {
	ctx := context.Background()
	known := func(_ context.Context, addr string) (bool, error) {
		return addr == "alice@example.invalid", nil
	}
	alice := []api.Address{{Address: "alice@example.invalid"}}
	mallory := []api.Address{{Name: "Alice", Address: "mallory@evil.invalid"}} // spoofed display name
	both := append(append([]api.Address{}, alice...), mallory...)

	cases := []struct {
		name      string
		stored    api.RemoteContentPolicy
		override  api.RemoteContentPolicy
		senders   []api.Address
		decrypted bool
		want      api.RemoteContentPolicy
		wantErr   bool
	}{
		{"stored block", api.RemoteBlock, "", alice, false, api.RemoteBlock, false},
		{"stored allow", api.RemoteAllow, "", mallory, false, api.RemoteAllow, false},
		{"known sender", api.RemoteKnownSenders, "", alice, false, api.RemoteAllow, false},
		{"spoofed name unknown address", api.RemoteKnownSenders, "", mallory, false, api.RemoteBlock, false},
		{"one unknown of two", api.RemoteKnownSenders, "", both, false, api.RemoteBlock, false},
		{"no senders", api.RemoteKnownSenders, "", nil, false, api.RemoteBlock, false},
		{"override allow beats block", api.RemoteBlock, api.RemoteAllow, mallory, false, api.RemoteAllow, false},
		{"override block beats allow", api.RemoteAllow, api.RemoteBlock, alice, false, api.RemoteBlock, false},
		{"decrypted always blocks", api.RemoteAllow, api.RemoteAllow, alice, true, api.RemoteBlock, false},
		{"knownSenders as override", api.RemoteBlock, api.RemoteKnownSenders, alice, false, api.RemoteBlock, true},
		{"unknown stored value", "whatever", "", alice, false, api.RemoteBlock, false},
	}
	for _, c := range cases {
		got, err := ResolveRemoteContent(ctx, c.stored, c.override, c.senders, c.decrypted, known)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", c.name, err, c.wantErr)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
		if got == api.RemoteKnownSenders {
			t.Errorf("%s: knownSenders leaked to the sanitiser", c.name)
		}
	}
}

func TestRemoteContentForUsesStore(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	if _, err := b.Config().Set(ctx, api.ConfigSetParams{Preferences: api.Preferences{RemoteContent: api.RemoteKnownSenders}}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Senders().Add(ctx, api.SenderAddParams{Address: "alice@example.invalid"}); err != nil {
		t.Fatal(err)
	}
	got, err := b.RemoteContentFor(ctx, "", []api.Address{{Address: "ALICE@example.invalid"}}, false)
	if err != nil || got != api.RemoteAllow {
		t.Fatalf("known sender via store: %q, %v", got, err)
	}
	got, _ = b.RemoteContentFor(ctx, "", []api.Address{{Address: "bob@example.invalid"}}, false)
	if got != api.RemoteBlock {
		t.Fatalf("unknown sender via store: %q", got)
	}
}
