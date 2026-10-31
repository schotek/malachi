// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package contactsctl

import (
	"slices"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/contacts"
)

// Contact photos (docs/plans/address-book.md §9, F8–F10): one Photos for
// the application, shared by the views that draw avatars. Each view tells
// it what it shows near its viewport (Show: contacts by id for the
// Contacts window, sender addresses for mail); Photos asks the daemon
// only for those:
//
//   - senders: contact.lookup {withPhotos} for the addresses it has no
//     answer for, gathered for LookupDelay (Loop.After) and sent in
//     batches of api.MaxContactLookupAddresses; every answer is kept by
//     address, also "no contact with a photo";
//   - photos: contact.photo for a contact whose photo is ready or remote
//     only while a view shows it, nearest first, at most
//     MaxPhotoFetches at a time; "none" is remembered;
//   - the bytes (re-encoded by the daemon, a JPEG or PNG ≤ 256 px) of the
//     MaxPhotos most recently used photos are kept; the views decode them
//     (and may keep their decoded image by Photo.Key).
//
// notify.contactsChanged (Changed) forgets what the changed books may
// have changed and asks again for what is shown; a view redraws on
// OnPhoto. A failed call is not repeated until the next change.

const (
	// MaxPhotos is how many photos' bytes are kept (F9).
	MaxPhotos = 200
	// MaxPhotoFetches is how many contact.photo calls run at once (F3).
	MaxPhotoFetches = 4
	// LookupDelay gathers the senders of a scrolled list into one
	// contact.lookup.
	LookupDelay = 150 * time.Millisecond
	// PhotoTimeout bounds contact.photo (the daemon gives Graph 15 s).
	PhotoTimeout = 20 * time.Second
)

// Photo is a contact's photo as the daemon sent it.
type Photo struct {
	Contact  api.ContactID
	Revision string
	MIME     string
	Data     []byte
}

// Key names this version of the photo: a view's decoded image of the same
// key is still good.
func (p Photo) Key() string { return string(p.Contact) + "\x00" + p.Revision }

// ContactRef is a contact a view shows, with the photo state its summary
// or card reports.
type ContactRef struct {
	ID    api.ContactID
	Book  api.AddressBookID
	State contacts.PhotoState
}

// ContactKey and SenderKey are the keys OnPhoto reports: a contact's
// avatars (by id) and a sender's (by address).
func ContactKey(id api.ContactID) string { return "contact:" + string(id) }
func SenderKey(address string) string    { return "sender:" + contacts.NormalAddress(address) }

// Photos is the photo cache of the application.
type Photos struct {
	Opts Options
	// OnPhoto: what the avatars of key (ContactKey, SenderKey) show
	// changed (a photo arrived, changed or went away); they redraw.
	OnPhoto func(key string)

	views map[string]photoView

	senders  map[string]senderEntry // by normal address
	queued   []string               // addresses waiting for the lookup
	lookupAt bool                   // a flush is scheduled
	asking   map[string]bool        // addresses of lookups on their way

	states   map[api.ContactID]contacts.PhotoState // from refs and lookups
	books    map[api.ContactID]api.AddressBookID   // known books of contacts
	cache    map[api.ContactID]Photo
	order    []api.ContactID // LRU, most recent last
	none     map[api.ContactID]bool
	stale    map[api.ContactID]bool
	failed   map[api.ContactID]bool
	fetching map[api.ContactID]bool

	gen int
}

type photoView struct {
	contacts []api.ContactID
	senders  []string
}

type senderEntry struct {
	contact api.ContactID // "" = no contact with a photo
	state   contacts.PhotoState
	failed  bool // the lookup failed; asked again after a change
	stale   bool // the books changed: shown until the new answer
}

// NewPhotos is the cache.
func NewPhotos(opts Options) *Photos {
	p := &Photos{Opts: opts}
	p.reset()
	return p
}

func (p *Photos) reset() {
	p.views = map[string]photoView{}
	p.senders = map[string]senderEntry{}
	p.asking = map[string]bool{}
	p.states = map[api.ContactID]contacts.PhotoState{}
	p.books = map[api.ContactID]api.AddressBookID{}
	p.cache = map[api.ContactID]Photo{}
	p.none = map[api.ContactID]bool{}
	p.stale = map[api.ContactID]bool{}
	p.failed = map[api.ContactID]bool{}
	p.fetching = map[api.ContactID]bool{}
}

// Show sets what view (any name: "contacts", "list", "conversation", …)
// shows near its viewport, nearest first: contacts by id with the state
// their summary or card reports (a ref without a photo is ignored) and
// sender addresses (contacts.Avatar.Sender). It replaces the view's
// previous set; Show(view, nil, nil) when the view closes.
func (p *Photos) Show(view string, refs []ContactRef, senders []string) {
	var v photoView
	for _, r := range refs {
		if r.ID == "" || contacts.IsRecent(r.Book, r.ID) {
			continue
		}
		if r.Book != "" {
			p.books[r.ID] = r.Book
		}
		if prev, ok := p.states[r.ID]; ok && prev != r.State {
			// The list reports a change the cache may not know of yet.
			p.stale[r.ID] = p.stale[r.ID] || p.cache[r.ID].Contact != "" || p.none[r.ID]
		}
		p.states[r.ID] = r.State
		if r.State.MayHave() && !slices.Contains(v.contacts, r.ID) {
			v.contacts = append(v.contacts, r.ID)
		}
	}
	for _, s := range senders {
		n := contacts.NormalAddress(s)
		if n == "" || slices.Contains(v.senders, n) {
			continue
		}
		v.senders = append(v.senders, n)
	}
	if len(v.contacts) == 0 && len(v.senders) == 0 {
		delete(p.views, view)
	} else {
		p.views[view] = v
	}
	p.queueSenders(v.senders)
	p.scheduleLookup()
	p.pump()
}

// ForContact is the photo of a contact's avatar; false shows initials.
func (p *Photos) ForContact(id api.ContactID) (Photo, bool) {
	ph, ok := p.cache[id]
	if ok {
		p.touch(id)
	}
	return ph, ok
}

// ForSender is the photo of a sender's avatar (contacts.Avatar.Sender);
// false shows initials.
func (p *Photos) ForSender(address string) (Photo, bool) {
	e, ok := p.senders[contacts.NormalAddress(address)]
	if !ok || e.contact == "" {
		return Photo{}, false
	}
	return p.ForContact(e.contact)
}

// For is the photo of an avatar; false shows initials.
func (p *Photos) For(a contacts.Avatar) (Photo, bool) {
	switch {
	case a.Contact != "":
		return p.ForContact(a.Contact)
	case a.Sender != "":
		return p.ForSender(a.Sender)
	}
	return Photo{}, false
}

// Changed is notify.contactsChanged: the answers the changed books may
// have changed are asked for again (every sender's, as lookups name no
// book, every "no photo"; all of them when bookIds is empty) for what the
// views show; until the new answer the old one stays on screen. A change of the
// recent book only changes nothing (recent contacts have no photos).
func (p *Photos) Changed(n api.ContactsChangedNotification) {
	if n.AccountID == "" && len(n.BookIDs) > 0 && !slices.ContainsFunc(n.BookIDs, func(b api.AddressBookID) bool { return b != api.AddressBookRecent }) {
		return
	}
	affected := func(id api.ContactID) bool {
		if len(n.BookIDs) == 0 {
			return true
		}
		b, ok := p.books[id]
		return !ok || slices.Contains(n.BookIDs, b)
	}
	p.gen++
	p.asking = map[string]bool{}
	// Photos on their way still count against MaxPhotoFetches; their
	// answers are dropped and the contacts asked again.
	for id := range p.fetching {
		if affected(id) {
			p.stale[id] = true
		}
	}
	for a, e := range p.senders {
		// A contact may have been added to any book: "no contact" is
		// always asked again.
		if e.contact == "" || affected(e.contact) {
			e.stale = true
			p.senders[a] = e
		}
	}
	for id := range p.none {
		if affected(id) {
			delete(p.none, id)
		}
	}
	for id := range p.failed {
		if affected(id) {
			delete(p.failed, id)
		}
	}
	for id := range p.cache {
		if affected(id) {
			p.stale[id] = true
		}
	}
	p.queued = nil
	names := make([]string, 0, len(p.views))
	for n := range p.views {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		p.queueSenders(p.views[n].senders)
	}
	p.scheduleLookup()
	p.pump()
}

// Reset forgets everything (the daemon restarted); the views Show again.
func (p *Photos) Reset() {
	p.gen++
	p.queued = nil
	p.reset()
}

// --- lookups ---------------------------------------------------------------

// queueSenders queues the addresses without a current answer that are
// not on their way.
func (p *Photos) queueSenders(addrs []string) {
	for _, a := range addrs {
		if e, ok := p.senders[a]; (ok && !e.stale) || p.asking[a] || slices.Contains(p.queued, a) {
			continue
		}
		p.queued = append(p.queued, a)
	}
}

func (p *Photos) scheduleLookup() {
	if len(p.queued) == 0 || p.lookupAt {
		return
	}
	if p.Opts.Loop == nil {
		p.flush()
		return
	}
	p.lookupAt = true
	p.Opts.after(LookupDelay, func() {
		p.lookupAt = false
		p.flush()
	})
}

func (p *Photos) flush() {
	shown := p.shownSenders()
	var addrs []string
	for _, a := range p.queued {
		// Only what a view still shows.
		if shown[a] {
			addrs = append(addrs, a)
		}
	}
	p.queued = nil
	for start := 0; start < len(addrs); start += api.MaxContactLookupAddresses {
		part := addrs[start:min(start+api.MaxContactLookupAddresses, len(addrs))]
		for _, a := range part {
			p.asking[a] = true
		}
		gen := p.gen
		call(p.Opts, api.MethodContactLookup, api.ContactLookupParams{Addresses: part, WithPhotos: true}, 0,
			func(r *api.ContactLookupResult, e *api.Error) { p.answered(gen, part, r, e) })
	}
}

func (p *Photos) answered(gen int, asked []string, r *api.ContactLookupResult, e *api.Error) {
	if gen != p.gen {
		return
	}
	got := map[string]api.ContactPhotoRef{}
	if r != nil {
		for a, ref := range r.Photos {
			got[contacts.NormalAddress(a)] = ref
		}
	}
	for _, a := range asked {
		delete(p.asking, a)
		prev, had := p.senders[a]
		var next senderEntry
		switch ref, ok := got[a]; {
		case e != nil:
			next = senderEntry{failed: true}
		case ok && contacts.PhotoState(ref.State).MayHave() && ref.ContactID != "" && !contacts.IsRecent("", ref.ContactID):
			next = senderEntry{contact: ref.ContactID, state: contacts.PhotoState(ref.State)}
			if old, known := p.states[ref.ContactID]; known && old != next.state {
				p.stale[ref.ContactID] = p.stale[ref.ContactID] || p.cache[ref.ContactID].Contact != "" || p.none[ref.ContactID]
			}
			p.states[ref.ContactID] = next.state
		}
		p.senders[a] = next
		if had && prev.contact != next.contact {
			p.report(SenderKey(a))
		}
	}
	p.pump()
}

func (p *Photos) shownSenders() map[string]bool {
	out := map[string]bool{}
	for _, v := range p.views {
		for _, a := range v.senders {
			out[a] = true
		}
	}
	return out
}

// --- photos ----------------------------------------------------------------

// wanted are the contacts whose photo a view shows and the cache lacks
// (or holds stale), nearest first.
func (p *Photos) wanted() []api.ContactID {
	names := make([]string, 0, len(p.views))
	for n := range p.views {
		names = append(names, n)
	}
	slices.Sort(names)
	var out []api.ContactID
	for _, id := range p.shownContacts() {
		if p.fetching[id] || p.failed[id] {
			continue
		}
		_, have := p.cache[id]
		if (have || p.none[id]) && !p.stale[id] {
			continue
		}
		out = append(out, id)
	}
	return out
}

// shownContacts are the contacts with a photo the views show, in their
// order (each view's nearest first, views by name), at most MaxPhotos: a
// photo shown is never evicted for another one shown.
func (p *Photos) shownContacts() []api.ContactID {
	names := make([]string, 0, len(p.views))
	for n := range p.views {
		names = append(names, n)
	}
	slices.Sort(names)
	var out []api.ContactID
	add := func(id api.ContactID) {
		if id != "" && len(out) < MaxPhotos && p.states[id].MayHave() && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	for _, n := range names {
		v := p.views[n]
		for _, id := range v.contacts {
			add(id)
		}
		for _, a := range v.senders {
			add(p.senders[a].contact)
		}
	}
	return out
}

func (p *Photos) pump() {
	for _, id := range p.wanted() {
		if len(p.fetching) >= MaxPhotoFetches {
			return
		}
		p.fetch(id)
	}
}

func (p *Photos) fetch(id api.ContactID) {
	p.fetching[id] = true
	gen := p.gen
	call(p.Opts, api.MethodContactPhoto, api.ContactPhotoParams{ContactID: id}, PhotoTimeout,
		func(r *api.ContactPhotoResult, e *api.Error) {
			delete(p.fetching, id)
			if gen == p.gen {
				p.fetched(id, r, e)
			}
			p.pump()
		})
}

func (p *Photos) fetched(id api.ContactID, r *api.ContactPhotoResult, e *api.Error) {
	delete(p.stale, id)
	old, had := p.cache[id]
	switch {
	case e != nil:
		// Keep what is shown; not asked again until a change.
		p.failed[id] = true
		return
	case contacts.PhotoState(r.State) != contacts.PhotoReady || len(r.Data) == 0:
		p.none[id] = true
		p.drop(id)
		if had {
			p.reportContact(id)
		}
		return
	}
	delete(p.none, id)
	ph := Photo{Contact: id, Revision: r.Revision, MIME: r.MIME, Data: r.Data}
	p.cache[id] = ph
	p.touch(id)
	if len(p.order) > MaxPhotos {
		// The least recently used photo no view shows.
		shown := p.shownContacts()
		for i := 0; i < len(p.order) && len(p.order) > MaxPhotos; {
			if c := p.order[i]; !slices.Contains(shown, c) {
				p.drop(c)
				continue
			}
			i++
		}
	}
	if !had || old.Key() != ph.Key() || !slices.Equal(old.Data, ph.Data) {
		p.reportContact(id)
	}
}

func (p *Photos) touch(id api.ContactID) {
	p.order = append(slices.DeleteFunc(p.order, func(c api.ContactID) bool { return c == id }), id)
}

func (p *Photos) drop(id api.ContactID) {
	delete(p.cache, id)
	p.order = slices.DeleteFunc(p.order, func(c api.ContactID) bool { return c == id })
}

// reportContact reports the contact's key and the key of every sender
// whose contact it is.
func (p *Photos) reportContact(id api.ContactID) {
	p.report(ContactKey(id))
	var addrs []string
	for a, e := range p.senders {
		if e.contact == id {
			addrs = append(addrs, a)
		}
	}
	slices.Sort(addrs)
	for _, a := range addrs {
		p.report(SenderKey(a))
	}
}

func (p *Photos) report(key string) {
	if p.OnPhoto != nil {
		p.OnPhoto(key)
	}
}
