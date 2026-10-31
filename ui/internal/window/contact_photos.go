// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"encoding/json"
	"slices"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/contacts"
	"github.com/schotek/malachi/ui/internal/contactsctl"
	"github.com/schotek/malachi/ui/internal/conversation"
	"github.com/schotek/malachi/ui/internal/settings"
)

// Contact photos in the avatars (docs/plans/address-book.md §9, F9–F10):
// one contactsctl.Photos for the application, fed by the views that draw
// avatars. Each view registers a provider (addPhotoView) that returns its
// avatars near the viewport, nearest first, each with the contacts.Avatar
// it stands for; the hub tells Photos what each view shows (Show) and puts
// the photo it has into every avatar (Adw.Avatar custom-image), or clears
// it, so the initials show. The Contacts window's list and detail show a
// contact's photo by id (F9); the message list (with show-avatars), the
// conversation's timeline, the chat of Mail and of the Board show a
// sender's by the exact From address, never the user's own (F10). The
// bytes are the daemon's re-encoded JPEG or PNG; a photo GDK cannot decode
// shows the initials. monochrome-avatars greys only the background of the
// initials (internal/style), a photo draws over it.
//
// A view calls photosChanged when what it shows changed (rows built,
// re-rendered, scrolled, shown or hidden); the hub refreshes once on the
// next idle. Everything runs on the main loop.

// photoAvatar is an avatar a view shows and what it stands for.
type photoAvatar struct {
	av *adw.Avatar
	a  contacts.Avatar
}

// photoProvider returns a view's avatars near its viewport, nearest
// first, and the contacts among them with the photo state their summary
// or card reports (the Contacts window); nil when the view shows none
// (hidden, closed, avatars off).
type photoProvider func() ([]photoAvatar, []contactsctl.ContactRef)

// photoHub is the application's photo state.
type photoHub struct {
	w      *Window
	photos *contactsctl.Photos
	views  map[string]photoProvider
	// shown are the views that told Photos something, so a view that
	// shows nothing any more tells it so once.
	shown   map[string]bool
	pending bool

	// textures are the decoded photos by Photo.Key, nil for bytes GDK
	// could not decode; order is least recently used first.
	textures map[string]*gdk.Texture
	order    []string
}

var contactPhotos *photoHub

// photoHub is the application's hub, made on first use with the main
// window w (whose client the photos are asked of).
func (w *Window) photoHub() *photoHub {
	if contactPhotos != nil && contactPhotos.w == nil {
		contactPhotos.w = w
	}
	if contactPhotos == nil {
		contactPhotos = &photoHub{w: w, views: map[string]photoProvider{}, shown: map[string]bool{},
			textures: map[string]*gdk.Texture{}}
		listenContacts(contactPhotos.handle)
	}
	return contactPhotos
}

// photosChanged refreshes the avatars on the next idle.
func (w *Window) photosChanged() { w.photoHub().refresh() }

// ensure makes the Photos once the daemon (or the samples) is there; nil
// without either.
func (h *photoHub) ensure() *contactsctl.Photos {
	if h.photos != nil || h.w == nil {
		return h.photos
	}
	caller := contactsCaller(h.w.client)
	if caller == nil {
		return nil
	}
	h.photos = contactsctl.NewPhotos(contactsctl.Options{Caller: caller, Loop: glibLoop{}, Log: h.w.log})
	h.photos.OnPhoto = func(string) { h.refresh() }
	return h.photos
}

// addPhotoView registers a view's provider under name (replacing one of
// the same name) and refreshes.
func (h *photoHub) addPhotoView(name string, p photoProvider) {
	h.views[name] = p
	h.refresh()
}

// removePhotoView forgets a view (its window closed).
func (h *photoHub) removePhotoView(name string) {
	delete(h.views, name)
	if h.shown[name] && h.photos != nil {
		h.photos.Show(name, nil, nil)
	}
	delete(h.shown, name)
}

// handle takes notify.contactsChanged: the changed books' answers are
// asked again for what is shown.
func (h *photoHub) handle(method string, params json.RawMessage) {
	n, ok := contactsctl.ContactsChanged(method, params)
	if !ok || h.photos == nil {
		return
	}
	h.photos.Changed(n)
	h.refresh()
}

// connected: a daemon (maybe a new one) answers again; every answer is
// asked again for what is shown, the old ones stay on screen until then.
func (h *photoHub) connected() {
	if h.photos != nil {
		h.photos.Changed(api.ContactsChangedNotification{})
	}
	h.refresh()
}

// refresh runs update on the next idle, once.
func (h *photoHub) refresh() {
	if h.pending {
		return
	}
	h.pending = true
	glib.IdleAdd(func() {
		h.pending = false
		h.update()
	})
}

// update tells Photos what every view shows and puts the photos it has
// into the avatars.
func (h *photoHub) update() {
	photos := h.ensure()
	names := make([]string, 0, len(h.views))
	for n := range h.views {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, name := range names {
		avatars, refs := h.views[name]()
		var senders []string
		for _, a := range avatars {
			if a.a.Sender != "" && !slices.Contains(senders, a.a.Sender) {
				senders = append(senders, a.a.Sender)
			}
		}
		if photos != nil && (len(refs) > 0 || len(senders) > 0 || h.shown[name]) {
			photos.Show(name, refs, senders)
			h.shown[name] = len(refs) > 0 || len(senders) > 0
		}
		for _, a := range avatars {
			h.apply(a.av, a.a)
		}
	}
}

// apply shows a's photo in av when Photos has it (and GDK decodes it),
// else av's initials.
func (h *photoHub) apply(av *adw.Avatar, a contacts.Avatar) {
	if av == nil {
		return
	}
	var tex *gdk.Texture
	if h.photos != nil && a.MayShowPhoto() {
		if ph, ok := h.photos.For(a); ok {
			tex = h.texture(ph)
		}
	}
	// A nil *gdk.Texture is not a nil gdk.Paintabler.
	if tex == nil {
		if av.CustomImage() != nil {
			av.SetCustomImage(nil)
		}
		return
	}
	if cur := av.CustomImage(); cur == nil || cur.Native() != tex.Native() {
		av.SetCustomImage(tex)
	}
}

// texture is ph decoded, nil when GDK cannot decode it (remembered).
func (h *photoHub) texture(ph contactsctl.Photo) *gdk.Texture {
	key := ph.Key()
	tex, ok := h.textures[key]
	if !ok {
		var err error
		tex, err = gdk.NewTextureFromBytes(glib.NewBytes(ph.Data))
		if err != nil {
			// Only the class of the failure, never the bytes.
			if h.w != nil && h.w.log != nil {
				h.w.log.Debug("contact photo not decoded", "contact", string(ph.Contact))
			}
			tex = nil
		}
		h.textures[key] = tex
	}
	h.order = append(slices.DeleteFunc(h.order, func(k string) bool { return k == key }), key)
	for len(h.order) > contactsctl.MaxPhotos {
		// An avatar showing an evicted texture keeps its own reference.
		delete(h.textures, h.order[0])
		h.order = h.order[1:]
	}
	return tex
}

// nearListRows are the indexes of list's rows near its viewport (the
// visible rows and a screen's worth above and below, contacts.NearRows),
// nearest first; count is how many rows the list has.
func nearListRows(list *gtk.ListBox, scroller *gtk.ScrolledWindow, count int) []int {
	if count <= 0 || !scroller.Mapped() {
		return nil
	}
	adj := scroller.VAdjustment()
	first, last := 0, count-1
	if r := list.RowAtY(int(adj.Value())); r != nil {
		first = r.Index()
	}
	if r := list.RowAtY(int(adj.Value() + adj.PageSize() - 1)); r != nil {
		last = r.Index()
	}
	from, to := contacts.NearRows(first, last, count)
	out := make([]int, 0, to-from)
	for i := from; i < to; i++ {
		out = append(out, i)
	}
	dist := func(i int) int {
		switch {
		case i < first:
			return first - i
		case i > last:
			return i - last
		}
		return 0
	}
	slices.SortStableFunc(out, func(a, b int) int { return dist(a) - dist(b) })
	return out
}

// ownFolder reports a folder whose messages are the user's own (sent,
// outbox, drafts): their sender's avatar never shows a photo (F10).
func ownFolder(role api.FolderRole) bool {
	return role == api.RoleSent || role == api.RoleOutbox || role == api.RoleDrafts
}

// ownAddress reports an address of one of the user's accounts.
func (w *Window) ownAddress(address string) bool {
	n := contacts.NormalAddress(address)
	if n == "" {
		return false
	}
	for _, a := range w.model.accounts {
		if contacts.NormalAddress(a.Config.Email) == n {
			return true
		}
	}
	return false
}

// senderAvatar is the avatar of a message's first sender with an
// address; mine (the user's own) never shows a photo.
func senderAvatar(from []api.Address, mine bool) contacts.Avatar {
	for _, a := range from {
		if a.Address != "" {
			return contacts.AvatarOfSender(a.Address, a.Name, mine)
		}
	}
	return contacts.Avatar{}
}

// listPhotos is the message list's provider: the senders of the rows
// near the viewport while the list shows avatars.
func (w *Window) listPhotos() ([]photoAvatar, []contactsctl.ContactRef) {
	if w.settings == nil || !w.settings.ShowAvatars() || w.messageList == nil {
		return nil, nil
	}
	byIndex := make(map[int]*listRowAvatar, len(w.rows))
	count := 0
	for key, r := range w.rows {
		i := r.Index()
		if i < 0 {
			continue
		}
		byIndex[i] = &listRowAvatar{key: key, av: r.Avatar()}
		count = max(count, i+1)
	}
	near := nearListRows(w.messageList, w.listScroller, count)
	out := make([]photoAvatar, 0, len(near))
	for _, i := range near {
		ra, ok := byIndex[i]
		if !ok {
			continue
		}
		out = append(out, photoAvatar{av: ra.av, a: w.listRowPhoto(ra.key)})
	}
	return out, nil
}

type listRowAvatar struct {
	key listKey
	av  *adw.Avatar
}

// listRowPhoto is the avatar of the list's row key: a message's first
// sender, a conversation's first participant (the one its avatar shows).
func (w *Window) listRowPhoto(key listKey) contacts.Avatar {
	row, ok := w.model.rowAt(w.model.rowIndexOf(key))
	if !ok {
		return contacts.Avatar{}
	}
	if row.Thread {
		var first []api.Address
		if len(row.Summary.Participants) > 0 {
			first = row.Summary.Participants[:1]
		}
		mine := ownFolder(w.model.folderRole(w.model.listFolder))
		if len(first) > 0 {
			mine = mine || w.ownAddress(first[0].Address)
		}
		return senderAvatar(first, mine)
	}
	s := row.Message
	var first []api.Address
	if len(s.From) > 0 {
		first = s.From[:1]
	}
	mine := ownFolder(w.model.folderRole(folderKey{Account: s.AccountID, Folder: s.FolderID}))
	if len(first) > 0 {
		mine = mine || w.ownAddress(first[0].Address)
	}
	return senderAvatar(first, mine)
}

// watchListPhotos registers the message list and refreshes it when it
// scrolls, grows, shows or hides, and when show-avatars changes.
func (w *Window) watchListPhotos() {
	if w.messageList == nil || w.listScroller == nil {
		return
	}
	w.photoHub().addPhotoView("list", w.listPhotos)
	adj := w.listScroller.VAdjustment()
	adj.ConnectValueChanged(w.photosChanged)
	adj.ConnectChanged(w.photosChanged)
	w.listScroller.ConnectMap(w.photosChanged)
	w.listScroller.ConnectUnmap(w.photosChanged)
	if w.settings != nil {
		// Settings callbacks arrive on the main loop.
		w.settings.OnChanged(settings.KeyShowAvatars, w.photosChanged)
	}
}

// convItemPhoto is the avatar of a conversation's member: its first
// sender, never the user's own (Mine) nor a reply from Sent.
func convItemPhoto(it conversation.Item) contacts.Avatar {
	if it.Kind != conversation.ItemMessage {
		return contacts.Avatar{}
	}
	return senderAvatar(it.Message.From, it.Mine || it.Sent)
}

// photoAvatars is the timeline's provider: the avatars of its cards
// while it shows (a conversation has few senders, all of them are asked).
func (cv *conversationView) photoAvatars() ([]photoAvatar, []contactsctl.ContactRef) {
	if !cv.shown || !cv.scroller.Mapped() {
		return nil, nil
	}
	out := make([]photoAvatar, 0, len(cv.items))
	for _, r := range cv.items {
		if r != nil && r.avatar != nil {
			out = append(out, photoAvatar{av: r.avatar, a: r.photo})
		}
	}
	return out, nil
}

// photoAvatars is Mail's chat's provider: its bubbles' avatars while it
// shows.
func (cv *conversationChat) photoAvatars() ([]photoAvatar, []contactsctl.ContactRef) {
	if !cv.shown || !cv.scroller.Mapped() {
		return nil, nil
	}
	out := make([]photoAvatar, 0, len(cv.order))
	for _, w := range cv.order {
		if r, ok := w.(*chatRow); ok {
			out = append(out, photoAvatar{av: r.avatar, a: r.photo})
		}
	}
	return out, nil
}

// photoAvatars is the Board's chat's provider: its bubbles' avatars while
// it shows.
func (c *boardChat) photoAvatars() ([]photoAvatar, []contactsctl.ContactRef) {
	if c.closed || !c.shown || !c.visible || !c.column.Mapped() {
		return nil, nil
	}
	out := make([]photoAvatar, 0, len(c.rows))
	for _, r := range c.rows {
		out = append(out, photoAvatar{av: r.avatar, a: r.photo})
	}
	return out, nil
}
