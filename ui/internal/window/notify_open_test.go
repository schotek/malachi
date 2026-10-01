// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"testing"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
)

func TestNotificationTarget(t *testing.T) {
	acc, id, ok := ParseNotificationTarget(NotificationTarget("acc-1", "msg;=%1"))
	if !ok || acc != "acc-1" || id != "msg;=%1" {
		t.Errorf("round trip: %q %q %v", acc, id, ok)
	}
	for name, v := range map[string]*glib.Variant{
		"nil":           nil,
		"string":        glib.NewVariantString("msg"),
		"empty account": NotificationTarget("", "msg"),
		"empty message": NotificationTarget("acc", ""),
		"three strings": glib.NewVariantTuple([]*glib.Variant{
			glib.NewVariantString("a"), glib.NewVariantString("b"), glib.NewVariantString("c"),
		}),
	} {
		if _, _, ok := ParseNotificationTarget(v); ok {
			t.Errorf("%s: accepted", name)
		}
	}
}
