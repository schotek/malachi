// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package config

import "testing"

func TestResolvePathsSocket(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/home/u/.cache")
	for _, tc := range []struct {
		name, rt, flatpak, want string
	}{
		{"session", "/run/user/1000", "", "/run/user/1000/malachi/rpc.sock"},
		{"flatpak", "/run/user/1000", "io.github.schotek.Malachi", "/run/user/1000/app/io.github.schotek.Malachi/malachi/rpc.sock"},
		{"no runtime dir", "", "", "/home/u/.cache/malachi/run/rpc.sock"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_RUNTIME_DIR", tc.rt)
			t.Setenv("FLATPAK_ID", tc.flatpak)
			p, err := ResolvePaths()
			if err != nil {
				t.Fatal(err)
			}
			if got := p.SocketFile(); got != tc.want {
				t.Errorf("SocketFile = %q, want %q", got, tc.want)
			}
		})
	}
}
