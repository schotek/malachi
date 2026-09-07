// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package client

import "testing"

// The UI must dial where the daemon listens (backend/internal/config).
func TestDefaultSocketPath(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/home/u/.cache")
	for _, tc := range []struct {
		name, override, rt, flatpak, want string
	}{
		{"session", "", "/run/user/1000", "", "/run/user/1000/malachi/rpc.sock"},
		{"flatpak", "", "/run/user/1000", "io.github.schotek.Malachi", "/run/user/1000/app/io.github.schotek.Malachi/malachi/rpc.sock"},
		{"no runtime dir", "", "", "", "/home/u/.cache/malachi/run/rpc.sock"},
		{"override", "/tmp/x.sock", "/run/user/1000", "io.github.schotek.Malachi", "/tmp/x.sock"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MALACHI_SOCKET", tc.override)
			t.Setenv("XDG_RUNTIME_DIR", tc.rt)
			t.Setenv("FLATPAK_ID", tc.flatpak)
			if got := DefaultSocketPath(); got != tc.want {
				t.Errorf("DefaultSocketPath = %q, want %q", got, tc.want)
			}
		})
	}
}
