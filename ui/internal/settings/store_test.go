// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package settings

import (
	"io"
	"log/slog"
	"testing"
)

func TestMemoryDefaults(t *testing.T) {
	s := NewMemory()
	if s.Persistent() {
		t.Fatal("memory store must not report persistent")
	}
	if got := s.ColorScheme(); got != ColorSchemeSystem {
		t.Errorf("ColorScheme = %q, want %q", got, ColorSchemeSystem)
	}
	if got := s.Density(); got != DensityComfortable {
		t.Errorf("Density = %q, want %q", got, DensityComfortable)
	}
	if !s.ShowPreviewLine() || !s.ShowAvatars() || s.MonochromeAvatars() || s.MonospacePlainText() {
		t.Errorf("bool defaults wrong: preview=%v avatars=%v monochrome=%v mono=%v",
			s.ShowPreviewLine(), s.ShowAvatars(), s.MonochromeAvatars(), s.MonospacePlainText())
	}
	if got := s.TextZoom(); got != 100 {
		t.Errorf("TextZoom = %d, want 100", got)
	}
	if s.LaunchAtLogin() || s.RunInBackground() || !s.ConfirmDelete() || !s.DesktopNotifications() || s.NotificationSound() {
		t.Error("general bool defaults wrong")
	}
	if got := s.MarkReadDelay(); got != 2 {
		t.Errorf("MarkReadDelay = %d, want 2", got)
	}
	s.SetMarkReadDelay(999)
	if got := s.MarkReadDelay(); got != MarkReadDelayMax {
		t.Errorf("MarkReadDelay not clamped: %d", got)
	}
}

func TestMemorySetAndNotify(t *testing.T) {
	s := NewMemory()
	calls := 0
	remove := s.OnChanged(KeyTextZoom, func() { calls++ })

	s.SetTextZoom(120)
	if s.TextZoom() != 120 || calls != 1 {
		t.Fatalf("after set: zoom=%d calls=%d", s.TextZoom(), calls)
	}
	s.SetTextZoom(120) // unchanged: no notification
	if calls != 1 {
		t.Fatalf("unchanged set fired handler: calls=%d", calls)
	}
	remove()
	s.SetTextZoom(130)
	if calls != 1 {
		t.Fatalf("removed handler still fired: calls=%d", calls)
	}
}

func TestMemoryValidation(t *testing.T) {
	s := NewMemory()

	s.SetTextZoom(10)
	if got := s.TextZoom(); got != TextZoomMin {
		t.Errorf("zoom below min: got %d, want %d", got, TextZoomMin)
	}
	s.SetTextZoom(1000)
	if got := s.TextZoom(); got != TextZoomMax {
		t.Errorf("zoom above max: got %d, want %d", got, TextZoomMax)
	}

	s.SetColorScheme("neon")
	if got := s.ColorScheme(); got != ColorSchemeSystem {
		t.Errorf("invalid scheme accepted: %q", got)
	}
	s.SetColorScheme(ColorSchemeDark)
	if got := s.ColorScheme(); got != ColorSchemeDark {
		t.Errorf("ColorScheme = %q, want dark", got)
	}

	s.SetDensity("sardine")
	if got := s.Density(); got != DensityComfortable {
		t.Errorf("invalid density accepted: %q", got)
	}
}

func TestHandlerMayRemoveItself(t *testing.T) {
	s := NewMemory()
	var remove func()
	calls := 0
	remove = s.OnChanged(KeyShowAvatars, func() {
		calls++
		remove()
	})
	s.SetShowAvatars(false)
	s.SetShowAvatars(true)
	if calls != 1 {
		t.Fatalf("self-removing handler called %d times, want 1", calls)
	}
}

// TestOpenFallsBack checks that an unknown schema yields a memory store
// instead of aborting the process (gio.NewSettings would g_error).
func TestOpenFallsBack(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := open(SchemaID+".DoesNotExist", log)
	if s == nil || s.Persistent() {
		t.Fatal("expected in-memory fallback for unknown schema")
	}
	if s.TextZoom() != 100 {
		t.Errorf("fallback defaults not applied: zoom=%d", s.TextZoom())
	}
}

func TestCoerce(t *testing.T) {
	cases := []struct {
		v, like, want any
	}{
		{120, 0.0, 120.0},
		{120.4, 0, 120},
		{119.6, 0, 120},
		{true, false, true},
		{"x", "", "x"},
		{7, uint(0), uint(7)},
	}
	for _, c := range cases {
		if got := coerce(c.v, c.like); got != c.want {
			t.Errorf("coerce(%v, %T) = %v (%T), want %v (%T)", c.v, c.like, got, got, c.want, c.want)
		}
	}
}

func TestStringListRoundTrip(t *testing.T) {
	s := NewMemory()
	if got := s.CollapsedFolders(); len(got) != 0 {
		t.Fatalf("CollapsedFolders() = %v, want empty by default", got)
	}

	changed := 0
	s.OnChanged(KeyCollapsedFolders, func() { changed++ })

	s.SetCollapsedFolders([]string{"acc_1/f_1", "acc_1/f_2"})
	got := s.CollapsedFolders()
	if len(got) != 2 || got[0] != "acc_1/f_1" || got[1] != "acc_1/f_2" {
		t.Fatalf("CollapsedFolders() = %v", got)
	}
	if changed != 1 {
		t.Errorf("changed fired %d times, want 1", changed)
	}

	// Writing the same contents is not a change; slices are compared element
	// by element, because == on two of them panics.
	s.SetCollapsedFolders([]string{"acc_1/f_1", "acc_1/f_2"})
	if changed != 1 {
		t.Errorf("changed fired %d times after a no-op write, want 1", changed)
	}

	// The store hands out copies, so a caller cannot mutate it from outside.
	got[0] = "tampered"
	if s.CollapsedFolders()[0] != "acc_1/f_1" {
		t.Error("mutating the returned slice reached the store")
	}

	s.SetCollapsedFolders(nil)
	if got := s.CollapsedFolders(); len(got) != 0 {
		t.Errorf("CollapsedFolders() = %v, want empty after clearing", got)
	}
	if changed != 2 {
		t.Errorf("changed fired %d times, want 2", changed)
	}
}
