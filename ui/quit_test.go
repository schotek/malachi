// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import "testing"

func TestBoardExitWaitsForSave(t *testing.T) {
	var gate boardExit
	var saved func(bool)
	quit, resumed, asked := 0, 0, 0
	save := func(done func(bool)) { saved = done }
	confirm := func(func(bool)) { asked++ }
	resume := func() { resumed++ }
	proceed := func() { quit++ }
	gate.request(save, confirm, resume, proceed)
	gate.request(func(func(bool)) { t.Fatal("second close started another save") }, confirm, resume, proceed)
	if quit != 0 || !gate.pending {
		t.Fatal("closed before save finished")
	}
	saved(true)
	if quit != 1 || asked != 0 || resumed != 0 || gate.pending {
		t.Fatalf("quit=%d asked=%d resumed=%d pending=%v", quit, asked, resumed, gate.pending)
	}
}

func TestBoardExitUnsavedChoice(t *testing.T) {
	for _, allow := range []bool{false, true} {
		t.Run(map[bool]string{false: "stay", true: "quit"}[allow], func(t *testing.T) {
			var gate boardExit
			var answer func(bool)
			quit, resumed := 0, 0
			gate.request(func(done func(bool)) { done(false) }, func(done func(bool)) { answer = done }, func() { resumed++ }, func() { quit++ })
			if answer == nil || !gate.pending || quit != 0 || resumed != 0 {
				t.Fatal("did not wait for the unsaved reply decision")
			}
			gate.request(func(func(bool)) { t.Fatal("reentered during confirmation") }, nil, nil, nil)
			answer(allow)
			if gate.pending || (allow && (quit != 1 || resumed != 0)) || (!allow && (quit != 0 || resumed != 1)) {
				t.Fatalf("quit=%d resumed=%d pending=%v", quit, resumed, gate.pending)
			}
			if !allow {
				gate.request(func(done func(bool)) { done(true) }, nil, nil, func() { quit++ })
				if quit != 1 {
					t.Fatal("cancel prevented a later quit")
				}
			}
		})
	}
}
