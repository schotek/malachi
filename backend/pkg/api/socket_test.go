// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import "testing"

func TestSocketBase(t *testing.T) {
	for _, tc := range []struct {
		rt, id, want string
	}{
		{"", "", ""},
		{"", "io.github.schotek.Malachi", ""},
		{"/run/user/1000", "", "/run/user/1000"},
		{"/run/user/1000", "io.github.schotek.Malachi", "/run/user/1000/app/io.github.schotek.Malachi"},
	} {
		if got := SocketBase(tc.rt, tc.id); got != tc.want {
			t.Errorf("SocketBase(%q, %q) = %q, want %q", tc.rt, tc.id, got, tc.want)
		}
	}
	// sun_path is 108 bytes; the Flatpak path must leave room for the file.
	if p := SocketBase("/run/user/1000", "io.github.schotek.Malachi") + "/" + SocketRelPath; len(p) > 107 {
		t.Errorf("Flatpak socket path is %d bytes: %s", len(p), p)
	}
}
