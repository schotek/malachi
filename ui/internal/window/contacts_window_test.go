// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/schotek/malachi/ui/internal/contacts"
)

// The Contacts window folds where contacts.GTKBreakpoints says (W6).
func TestContactsWindowBreakpoints(t *testing.T) {
	raw, err := os.ReadFile("../../data/ui/contacts_window.blp")
	if err != nil {
		t.Fatal(err)
	}
	blp := string(raw)
	for _, w := range []float64{contacts.GTKBreakpoints.Sidebar, contacts.GTKBreakpoints.Detail} {
		if c := fmt.Sprintf(`condition ("max-width: %gsp")`, w); !strings.Contains(blp, c) {
			t.Errorf("contacts_window.blp has no %s", c)
		}
	}
}
