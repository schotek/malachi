package store

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "store.db"), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPreferencesRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if _, ok, err := s.GetPreference(ctx, "sync.interval_seconds"); err != nil || ok {
		t.Fatalf("unset key: ok=%v err=%v", ok, err)
	}
	if err := s.SetPreference(ctx, "sync.interval_seconds", "900"); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := s.GetPreference(ctx, "sync.interval_seconds"); err != nil || !ok || v != "900" {
		t.Fatalf("after set: v=%q ok=%v err=%v", v, ok, err)
	}
	if err := s.SetPreference(ctx, "sync.interval_seconds", "0"); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := s.GetPreference(ctx, "sync.interval_seconds"); v != "0" {
		t.Fatalf("overwrite failed: %q", v)
	}
}

func TestKnownSenders(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.AddKnownSender(ctx, " Alice@Example.invalid ", "user"); err != nil {
		t.Fatal(err)
	}
	// Same address, different case and source: ignored, original kept.
	if err := s.AddKnownSender(ctx, "alice@example.invalid", "sent"); err != nil {
		t.Fatal(err)
	}
	for _, addr := range []string{"alice@example.invalid", "ALICE@EXAMPLE.INVALID", "Alice@Example.invalid"} {
		if ok, err := s.IsKnownSender(ctx, addr); err != nil || !ok {
			t.Errorf("IsKnownSender(%q) = %v, %v; want true", addr, ok, err)
		}
	}
	if ok, _ := s.IsKnownSender(ctx, "bob@example.invalid"); ok {
		t.Error("unknown address reported as known")
	}

	list, err := s.ListKnownSenders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Address != "alice@example.invalid" || list[0].Source != "user" {
		t.Fatalf("list = %+v", list)
	}
	if list[0].AddedAt.IsZero() || time.Since(list[0].AddedAt) > time.Hour {
		t.Errorf("AddedAt not parsed: %v", list[0].AddedAt)
	}

	if err := s.AddKnownSender(ctx, "", "user"); err == nil {
		t.Error("empty address accepted")
	}
	if err := s.AddKnownSender(ctx, "x@y", "bogus"); err == nil {
		t.Error("invalid source accepted (CHECK constraint)")
	}

	if err := s.RemoveKnownSender(ctx, "ALICE@example.invalid"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.IsKnownSender(ctx, "alice@example.invalid"); ok {
		t.Error("address still known after removal")
	}
	if err := s.RemoveKnownSender(ctx, "nobody@example.invalid"); err != nil {
		t.Errorf("removing unknown address errored: %v", err)
	}
}
