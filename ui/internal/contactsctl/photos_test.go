// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package contactsctl

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/contacts"
)

// photoDaemon answers contact.lookup {withPhotos} from refs (address →
// contact with a photo) and contact.photo from revs (contact → revision;
// absent = none).
type photoDaemon struct {
	*daemon
	refs map[string]api.ContactPhotoRef
	revs map[api.ContactID]string
}

func newPhotoDaemon() (*photoDaemon, *loop) {
	d := &photoDaemon{daemon: newDaemon(), refs: map[string]api.ContactPhotoRef{}, revs: map[api.ContactID]string{}}
	d.on(api.MethodContactLookup, func(raw json.RawMessage) (any, error) {
		var p api.ContactLookupParams
		_ = json.Unmarshal(raw, &p)
		r := api.ContactLookupResult{Known: map[string]api.ContactID{}, Photos: map[string]api.ContactPhotoRef{}}
		for _, a := range p.Addresses {
			if ref, ok := d.refs[a]; ok {
				r.Known[a] = ref.ContactID
				if p.WithPhotos {
					r.Photos[a] = ref
				}
			}
		}
		return r, nil
	})
	d.on(api.MethodContactPhoto, func(raw json.RawMessage) (any, error) {
		var p api.ContactPhotoParams
		_ = json.Unmarshal(raw, &p)
		rev, ok := d.revs[p.ContactID]
		if !ok {
			return api.ContactPhotoResult{State: "none"}, nil
		}
		return api.ContactPhotoResult{State: "ready", MIME: "image/jpeg", Data: []byte("jpeg:" + string(p.ContactID) + ":" + rev), Revision: rev}, nil
	})
	return d, &loop{}
}

// photosAsked are the contact ids of contact.photo calls so far.
func (d *photoDaemon) photosAsked() []api.ContactID {
	var out []api.ContactID
	for _, raw := range d.params[api.MethodContactPhoto] {
		var p api.ContactPhotoParams
		_ = json.Unmarshal(raw, &p)
		out = append(out, p.ContactID)
	}
	return out
}

func newTestPhotos(d *photoDaemon, l *loop) (*Photos, *[]string) {
	p := NewPhotos(options(d.daemon, l))
	var keys []string
	p.OnPhoto = func(k string) { keys = append(keys, k) }
	return p, &keys
}

func TestPhotosBySender(t *testing.T) {
	d, l := newPhotoDaemon()
	d.refs["jana@firma.cz"] = api.ContactPhotoRef{ContactID: "p_1", State: "ready"}
	d.refs["petr@firma.cz"] = api.ContactPhotoRef{ContactID: "p_2", State: "remote"}
	d.refs["eva@firma.cz"] = api.ContactPhotoRef{ContactID: "p_3", State: ""} // a contact without a photo
	d.revs["p_1"] = "e1"
	p, keys := newTestPhotos(d, l)

	p.Show("list", nil, []string{"Jana@Firma.cz", "petr@firma.cz", "eva@firma.cz", "x@y.cz"})
	p.Show("conversation", nil, []string{"jana@firma.cz"})
	if d.count(api.MethodContactLookup) != 0 {
		t.Fatal("asked before the debounce")
	}
	l.fire()
	var lp api.ContactLookupParams
	d.last(api.MethodContactLookup, &lp)
	if d.count(api.MethodContactLookup) != 1 || !lp.WithPhotos ||
		!slices.Equal(lp.Addresses, []string{"jana@firma.cz", "petr@firma.cz", "eva@firma.cz", "x@y.cz"}) {
		t.Fatalf("lookup %d %+v", d.count(api.MethodContactLookup), lp)
	}
	l.run()
	// p_1 ready, p_2 remote: both asked lazily; p_3 and x@y.cz not.
	if got := d.photosAsked(); !slices.Equal(got, []api.ContactID{"p_1", "p_2"}) {
		t.Fatalf("photos asked %v", got)
	}
	ph, ok := p.ForSender("JANA@firma.cz")
	if !ok || string(ph.Data) != "jpeg:p_1:e1" || ph.MIME != "image/jpeg" || ph.Key() != "p_1\x00e1" {
		t.Fatalf("photo %+v %v", ph, ok)
	}
	if _, ok := p.ForSender("petr@firma.cz"); ok {
		t.Fatal("a remote contact the daemon has no photo of")
	}
	if _, ok := p.For(contacts.AvatarOfSender("eva@firma.cz", "Eva", false)); ok {
		t.Fatal("eva")
	}
	if ph, ok := p.For(contacts.AvatarOfSender("jana@firma.cz", "Jana", false)); !ok || ph.Contact != "p_1" {
		t.Fatal("For")
	}
	if !slices.Equal(*keys, []string{ContactKey("p_1"), SenderKey("jana@firma.cz")}) {
		t.Fatalf("keys %v", *keys)
	}

	// Answers are kept, also the negative ones: nothing is asked again.
	p.Show("list", nil, []string{"x@y.cz", "eva@firma.cz", "jana@firma.cz"})
	l.fire()
	if d.count(api.MethodContactLookup) != 1 || d.count(api.MethodContactPhoto) != 2 {
		t.Fatal("asked again")
	}
}

func TestPhotosBatches(t *testing.T) {
	d, l := newPhotoDaemon()
	p, _ := newTestPhotos(d, l)
	var many []string
	for i := range 450 {
		many = append(many, fmt.Sprintf("a%d@x.cz", i))
	}
	p.Show("list", nil, many[:100])
	p.Show("list", nil, many)
	l.fire()
	if n := d.count(api.MethodContactLookup); n != 3 {
		t.Fatalf("batches %d", n)
	}
	for _, raw := range d.params[api.MethodContactLookup] {
		var lp api.ContactLookupParams
		_ = json.Unmarshal(raw, &lp)
		if len(lp.Addresses) > api.MaxContactLookupAddresses {
			t.Fatal("a batch over the limit")
		}
	}
	// What a view no longer shows when the debounce runs is not asked.
	p.Show("list", nil, []string{"gone@x.cz"})
	p.Show("list", nil, nil)
	l.fire()
	if d.count(api.MethodContactLookup) != 3 {
		t.Fatal("asked for an address no view shows")
	}
	// An address on its way is not asked twice.
	p.Show("list", nil, []string{"b@x.cz"})
	l.fire()
	p.Show("other", nil, []string{"b@x.cz"})
	l.fire()
	if d.count(api.MethodContactLookup) != 4 {
		t.Fatal("asked twice")
	}
}

func TestPhotosByContact(t *testing.T) {
	d, l := newPhotoDaemon()
	d.revs["p_1"] = "e1"
	p, keys := newTestPhotos(d, l)
	p.Show("contacts", []ContactRef{
		{ID: "p_1", Book: "b_1", State: contacts.PhotoReady},
		{ID: "p_2", Book: "b_1", State: contacts.PhotoRemote},
		{ID: "p_3", Book: "b_1"},
		{ID: "r_1", Book: api.AddressBookRecent, State: contacts.PhotoReady},
	}, nil)
	l.run()
	if got := d.photosAsked(); !slices.Equal(got, []api.ContactID{"p_1", "p_2"}) {
		t.Fatalf("asked %v", got)
	}
	if _, ok := p.ForContact("p_1"); !ok {
		t.Fatal("p_1")
	}
	if _, ok := p.ForContact("p_2"); ok {
		t.Fatal("p_2 has none")
	}
	if !slices.Equal(*keys, []string{ContactKey("p_1")}) {
		t.Fatalf("keys %v", *keys)
	}
	// "none" is remembered.
	p.Show("contacts", []ContactRef{{ID: "p_2", Book: "b_1", State: contacts.PhotoRemote}}, nil)
	l.run()
	if d.count(api.MethodContactPhoto) != 2 {
		t.Fatal("none asked again")
	}
	// The list reports a new state: asked again.
	d.revs["p_2"] = "e7"
	p.Show("contacts", []ContactRef{{ID: "p_2", Book: "b_1", State: contacts.PhotoReady}}, nil)
	l.run()
	if ph, ok := p.ForContact("p_2"); !ok || ph.Revision != "e7" {
		t.Fatal("new state")
	}
}

func TestPhotosConcurrency(t *testing.T) {
	d, l := newPhotoDaemon()
	var refs []ContactRef
	for i := range 10 {
		id := api.ContactID(fmt.Sprintf("p_%d", i))
		d.revs[id] = "e"
		refs = append(refs, ContactRef{ID: id, Book: "b_1", State: contacts.PhotoRemote})
	}
	p, _ := newTestPhotos(d, l)
	p.Show("contacts", refs, nil)
	if n := d.count(api.MethodContactPhoto); n != MaxPhotoFetches {
		t.Fatalf("%d at once", n)
	}
	// Nearest first; the next start as the first answers come.
	if got := d.photosAsked(); !slices.Equal(got, []api.ContactID{"p_0", "p_1", "p_2", "p_3"}) {
		t.Fatalf("order %v", got)
	}
	f := l.queue[0]
	l.queue = l.queue[1:]
	f()
	if n := d.count(api.MethodContactPhoto); n != MaxPhotoFetches+1 {
		t.Fatalf("after one answer %d", n)
	}
	// What scrolled away is not asked.
	p.Show("contacts", refs[:6], nil)
	l.run()
	if n := d.count(api.MethodContactPhoto); n != 6 {
		t.Fatalf("asked %d", n)
	}
}

func TestPhotosChanged(t *testing.T) {
	d, l := newPhotoDaemon()
	d.refs["jana@firma.cz"] = api.ContactPhotoRef{ContactID: "p_1", State: "ready"}
	d.revs["p_1"] = "e1"
	d.revs["p_9"] = "e1"
	p, keys := newTestPhotos(d, l)
	p.Show("list", nil, []string{"jana@firma.cz", "x@y.cz"})
	p.Show("contacts", []ContactRef{{ID: "p_9", Book: "b_2", State: contacts.PhotoReady}}, nil)
	l.fire()
	if d.count(api.MethodContactPhoto) != 2 {
		t.Fatal("setup")
	}
	*keys = nil

	// The recent book only: nothing.
	p.Changed(api.ContactsChangedNotification{BookIDs: []api.AddressBookID{api.AddressBookRecent}})
	l.fire()
	if d.count(api.MethodContactLookup) != 1 || d.count(api.MethodContactPhoto) != 2 {
		t.Fatal("the recent book changed something")
	}

	// Book b_1 changed: senders (book unknown) and negatives are asked
	// again, p_9 of b_2 is not; the photo stays shown meanwhile.
	d.refs["x@y.cz"] = api.ContactPhotoRef{ContactID: "p_5", State: "ready"}
	d.revs["p_5"] = "e1"
	p.Changed(api.ContactsChangedNotification{AccountID: "acc_m", BookIDs: []api.AddressBookID{"b_1"}})
	if _, ok := p.ForSender("jana@firma.cz"); !ok {
		t.Fatal("the photo went away before the new answer")
	}
	l.fire()
	var lp api.ContactLookupParams
	d.last(api.MethodContactLookup, &lp)
	if !slices.Equal(lp.Addresses, []string{"jana@firma.cz", "x@y.cz"}) {
		t.Fatalf("asked again %v", lp.Addresses)
	}
	if got := d.photosAsked()[2:]; !slices.Equal(got, []api.ContactID{"p_1", "p_5"}) {
		t.Fatalf("photos again %v", got)
	}
	// p_1's revision did not change: no redraw; x@y.cz got a photo.
	if !slices.Equal(*keys, []string{SenderKey("x@y.cz"), ContactKey("p_5"), SenderKey("x@y.cz")}) {
		t.Fatalf("keys %v", *keys)
	}

	// All books: a new revision redraws; a removed photo too.
	*keys = nil
	d.revs["p_1"] = "e2"
	delete(d.revs, "p_9")
	p.Changed(api.ContactsChangedNotification{AccountID: "acc_m"})
	l.fire()
	if ph, _ := p.ForSender("jana@firma.cz"); ph.Revision != "e2" {
		t.Fatal("new revision")
	}
	if _, ok := p.ForContact("p_9"); ok {
		t.Fatal("removed photo kept")
	}
	if !slices.Contains(*keys, ContactKey("p_1")) || !slices.Contains(*keys, SenderKey("jana@firma.cz")) || !slices.Contains(*keys, ContactKey("p_9")) {
		t.Fatalf("keys %v", *keys)
	}

	// An answer on its way when the books change is dropped and asked
	// again.
	p.Changed(api.ContactsChangedNotification{})
	n := d.count(api.MethodContactLookup)
	l.timers[0].f() // the flush
	l.timers = nil
	p.Changed(api.ContactsChangedNotification{})
	l.run()
	if _, ok := p.ForSender("jana@firma.cz"); !ok {
		t.Fatal("photo lost")
	}
	l.fire()
	if d.count(api.MethodContactLookup) != n+2 {
		t.Fatalf("stale answer not asked again: %d", d.count(api.MethodContactLookup)-n)
	}
}

func TestPhotosFailures(t *testing.T) {
	d, l := newPhotoDaemon()
	d.fail(api.MethodContactLookup, &api.Error{Code: api.CodeUnavailable})
	p, _ := newTestPhotos(d, l)
	p.Show("list", nil, []string{"jana@firma.cz"})
	l.fire()
	p.Show("list", nil, []string{"jana@firma.cz"})
	l.fire()
	if d.count(api.MethodContactLookup) != 1 {
		t.Fatal("a failed lookup repeated")
	}
	d.fail(api.MethodContactPhoto, &api.Error{Code: api.CodeUnavailable})
	p.Show("contacts", []ContactRef{{ID: "p_1", Book: "b_1", State: contacts.PhotoRemote}}, nil)
	l.run()
	p.Show("contacts", []ContactRef{{ID: "p_1", Book: "b_1", State: contacts.PhotoRemote}}, nil)
	l.run()
	if d.count(api.MethodContactPhoto) != 1 {
		t.Fatal("a failed photo repeated")
	}
	// A change asks again.
	p.Changed(api.ContactsChangedNotification{})
	l.fire()
	if d.count(api.MethodContactLookup) != 2 || d.count(api.MethodContactPhoto) != 2 {
		t.Fatal("not asked after a change")
	}
}

func TestPhotosLRU(t *testing.T) {
	d, l := newPhotoDaemon()
	p, _ := newTestPhotos(d, l)
	ref := func(i int) ContactRef {
		id := api.ContactID(fmt.Sprintf("p_%d", i))
		d.revs[id] = "e"
		return ContactRef{ID: id, Book: "b_1", State: contacts.PhotoReady}
	}
	// Pages of 50 shown one after another: the least recently used
	// photos go, those shown never.
	for page := range 5 {
		var refs []ContactRef
		for i := page * 50; i < page*50+50; i++ {
			refs = append(refs, ref(i))
		}
		p.Show("contacts", refs, nil)
		l.run()
	}
	if len(p.cache) != MaxPhotos || len(p.order) != MaxPhotos {
		t.Fatalf("kept %d", len(p.cache))
	}
	if _, ok := p.ForContact("p_0"); ok {
		t.Fatal("the oldest kept")
	}
	if _, ok := p.ForContact("p_249"); !ok {
		t.Fatal("a shown one evicted")
	}
	// A shown photo is used again: kept over older ones.
	p.ForContact("p_50")
	p.Show("contacts", []ContactRef{ref(300)}, nil)
	l.run()
	if _, ok := p.ForContact("p_50"); !ok {
		t.Fatal("recently used evicted")
	}
	if _, ok := p.ForContact("p_51"); ok {
		t.Fatal("LRU")
	}
	// More shown than kept: no endless refetching.
	var all []ContactRef
	for i := range MaxPhotos + 20 {
		all = append(all, ref(1000+i))
	}
	n := d.count(api.MethodContactPhoto)
	p.Show("contacts", all, nil)
	l.run()
	if got := d.count(api.MethodContactPhoto) - n; got != MaxPhotos {
		t.Fatalf("fetched %d", got)
	}
}

func TestPhotosOwnAndReset(t *testing.T) {
	d, l := newPhotoDaemon()
	d.refs["jana@firma.cz"] = api.ContactPhotoRef{ContactID: "p_1", State: "ready"}
	d.revs["p_1"] = "e1"
	p, _ := newTestPhotos(d, l)
	// The user's own message never asks (its avatar has no Sender).
	own := contacts.AvatarOfSender("jana@firma.cz", "Jana", true)
	p.Show("list", nil, []string{own.Sender})
	l.fire()
	if d.count(api.MethodContactLookup) != 0 {
		t.Fatal("own message asked")
	}
	if _, ok := p.For(own); ok {
		t.Fatal("own message has a photo")
	}
	p.Show("list", nil, []string{"jana@firma.cz"})
	l.fire()
	p.Reset()
	if _, ok := p.ForSender("jana@firma.cz"); ok {
		t.Fatal("not reset")
	}
}
