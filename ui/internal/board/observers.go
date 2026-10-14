// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import "slices"

// Several listeners for one of the board's application-wide objects (the
// board preferences, the triage run, its schedule, the suggested reply):
// each Add returns a token that removes its handler. The macOS client
// leads (MalachiCore/Board/BoardObservers.swift); this is its port. Like
// everything of the board it runs on the main loop only.

// ObserverToken removes a handler installed by Observers.Add; dropping the
// token does not.
type ObserverToken struct {
	remove func()
}

// Cancel removes the handler; a second call does nothing.
func (t *ObserverToken) Cancel() {
	if t == nil || t.remove == nil {
		return
	}
	r := t.remove
	t.remove = nil
	r()
}

// Observers are the handlers, called in the order they were added. The
// zero value is ready to use.
type Observers struct {
	handlers map[int]func()
	next     int
}

// Add installs f and returns the token that removes it.
func (o *Observers) Add(f func()) *ObserverToken {
	if o.handlers == nil {
		o.handlers = map[int]func(){}
	}
	id := o.next
	o.next++
	o.handlers[id] = f
	return &ObserverToken{remove: func() { delete(o.handlers, id) }}
}

// Notify calls every handler, in the order they were added. A handler may
// cancel its own token, or another's: one cancelled before its turn is not
// called.
func (o *Observers) Notify() {
	ids := make([]int, 0, len(o.handlers))
	for id := range o.handlers {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if f, ok := o.handlers[id]; ok {
			f()
		}
	}
}

// Len is the number of handlers installed.
func (o *Observers) Len() int { return len(o.handlers) }
