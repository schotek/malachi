// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import "testing"

func TestObservers(t *testing.T) {
	var o Observers
	o.Notify() // the zero value works
	var log []string
	var second *ObserverToken
	first := o.Add(func() {
		log = append(log, "first")
		second.Cancel() // another's token: not called after this
	})
	second = o.Add(func() { log = append(log, "second") })
	var third *ObserverToken
	third = o.Add(func() {
		log = append(log, "third")
		third.Cancel() // its own
	})
	o.Notify()
	eq(t, "first round", log, []string{"first", "third"})
	eq(t, "left", o.Len(), 1)
	log = nil
	o.Notify()
	eq(t, "second round", log, []string{"first"})
	first.Cancel()
	first.Cancel() // twice is nothing
	var none *ObserverToken
	none.Cancel()
	log = nil
	o.Notify()
	eq(t, "none left", len(log), 0)
	// Added during a round: called from the next one.
	o.Add(func() {
		log = append(log, "outer")
		o.Add(func() { log = append(log, "inner") })
	})
	o.Notify()
	eq(t, "added", log, []string{"outer"})
}
