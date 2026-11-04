// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package contactsctl

import (
	"slices"
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/contacts"
)

// The address chips' contacts item (W2): Show Contact for an address a
// visible book has exactly (contact.lookup, never by name), else Add to
// Contacts…. The lookup is asked once per shown message for all its
// header addresses (key is the message's id, or any key the view uses)
// and kept for the LookupKept most recent messages; notify.contactsChanged
// forgets every answer (Invalidate), and the chips ask again.

// LookupKept is how many messages' answers are kept.
const LookupKept = 50

// Lookup is the address chips' lookup cache.
type Lookup struct {
	Opts Options
	// OnChange: the answer for key arrived (the chips of that message
	// rebuild their menus).
	OnChange func(key string)

	answers map[string]map[string]api.ContactID
	pending map[string]int
	order   []string
	gen     int
}

// NewLookup is the cache.
func NewLookup(opts Options) *Lookup {
	return &Lookup{Opts: opts, answers: map[string]map[string]api.ContactID{}, pending: map[string]int{}}
}

// NormalAddress is an address as contact.lookup's keys are: trimmed, lower
// case.
func NormalAddress(a string) string { return strings.ToLower(strings.TrimSpace(a)) }

// Ask asks for the addresses of a message once (a key answered or on its
// way is not asked again); at most api.MaxContactLookupAddresses per
// call.
func (l *Lookup) Ask(key string, addresses []string) {
	if _, ok := l.answers[key]; ok {
		return
	}
	if _, ok := l.pending[key]; ok {
		return
	}
	var addrs []string
	for _, a := range addresses {
		if n := NormalAddress(a); n != "" && !slices.Contains(addrs, n) {
			addrs = append(addrs, n)
		}
	}
	gen := l.gen
	chunks := 0
	known := map[string]api.ContactID{}
	for start := 0; start < len(addrs); start += api.MaxContactLookupAddresses {
		chunks++
	}
	if chunks == 0 {
		l.store(key, known)
		return
	}
	l.pending[key] = chunks
	for start := 0; start < len(addrs); start += api.MaxContactLookupAddresses {
		part := addrs[start:min(start+api.MaxContactLookupAddresses, len(addrs))]
		call(l.Opts, api.MethodContactLookup, api.ContactLookupParams{Addresses: part}, 0, func(r *api.ContactLookupResult, e *api.Error) {
			if gen != l.gen {
				return
			}
			if e == nil {
				for a, id := range r.Known {
					known[NormalAddress(a)] = id
				}
			}
			l.pending[key]--
			if l.pending[key] > 0 {
				return
			}
			delete(l.pending, key)
			if e != nil {
				// Not known is the safe answer; ask again next time.
				if l.OnChange != nil {
					l.OnChange(key)
				}
				return
			}
			l.store(key, known)
		})
	}
}

func (l *Lookup) store(key string, known map[string]api.ContactID) {
	l.answers[key] = known
	l.order = append(slices.DeleteFunc(l.order, func(k string) bool { return k == key }), key)
	for len(l.order) > LookupKept {
		delete(l.answers, l.order[0])
		l.order = l.order[1:]
	}
	if l.OnChange != nil {
		l.OnChange(key)
	}
}

// Known is the contact of an address of key's message; answered is false
// before the answer.
func (l *Lookup) Known(key, address string) (id api.ContactID, answered bool) {
	m, ok := l.answers[key]
	if !ok {
		return "", false
	}
	return m[NormalAddress(address)], true
}

// Item is the chip's contacts item for an address (books are the
// window's or the preferences' addressBook.list; nil = no book can take a
// new contact).
func (l *Lookup) Item(key, address string, books []api.AddressBook) (contacts.ChipItem, api.ContactID) {
	id, answered := l.Known(key, address)
	if !answered {
		return contacts.ChipNone, ""
	}
	return contacts.ChipItemFor(id != "", books), id
}

// Invalidate forgets every answer (notify.contactsChanged); answers on
// their way are dropped.
func (l *Lookup) Invalidate() {
	l.gen++
	l.answers = map[string]map[string]api.ContactID{}
	l.pending = map[string]int{}
	l.order = nil
}
