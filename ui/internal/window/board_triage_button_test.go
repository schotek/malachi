// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"testing"

	"github.com/schotek/malachi/ui/internal/boardtriage"
)

func TestBoardTriageClickFor(t *testing.T) {
	cases := []struct {
		control boardtriage.Control
		want    boardTriageClick
	}{
		{boardtriage.ControlHidden, clickNone},
		{boardtriage.ControlGetClaudeCode, clickInstall},
		{boardtriage.ControlSignIn, clickSignIn},
		{boardtriage.ControlUnavailable, clickNone},
		{boardtriage.ControlTriage, clickStart},
		{boardtriage.ControlStop, clickStop},
	}
	for _, c := range cases {
		if got := boardTriageClickFor(c.control); got != c.want {
			t.Errorf("boardTriageClickFor(%v) = %v, want %v", c.control, got, c.want)
		}
	}
}
