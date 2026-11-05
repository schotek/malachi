// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package updates

import "testing"

func TestEditionLine(t *testing.T) {
	for _, tc := range []struct {
		e    Edition
		want string
	}{
		{EditionWebsite, "Version from malachimail.app"},
		{EditionMacAppStore, "Version from the Mac App Store"},
		{EditionMicrosoftStore, "Version from the Microsoft Store"},
		{Edition(99), "Version from malachimail.app"},
	} {
		if got := EditionLine(tc.e, msgids{}); got != tc.want {
			t.Errorf("EditionLine(%d) = %q, want %q", tc.e, got, tc.want)
		}
	}
}
