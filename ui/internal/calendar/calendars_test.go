// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestActiveCalendars(t *testing.T) {
	got := ActiveCalendars([]api.Calendar{{ID: "a"}, {ID: "b", Disabled: true}, {ID: "c", Visible: false}, {ID: "d"}})
	if len(got) != 3 || got[0].ID != "a" || got[1].ID != "c" || got[2].ID != "d" {
		t.Errorf("ActiveCalendars = %+v", got)
	}
	if got := ActiveCalendars(nil); got == nil || len(got) != 0 {
		t.Errorf("empty in: %#v", got)
	}
	if got := ActiveCalendars([]api.Calendar{{ID: "x", Disabled: true}}); got == nil || len(got) != 0 {
		t.Errorf("all disabled: %#v", got)
	}
}
