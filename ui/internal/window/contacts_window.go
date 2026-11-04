// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/contacts"
	"github.com/schotek/malachi/ui/internal/contactsctl"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The Contacts window (docs/plans/address-book.md W1, W4–W6; the macOS
// ContactsWindowController): contacts_window.blp over a
// contactsctl.WindowController (the sidebar of books, the list, the
// search, the consent banners, the status line) and a
// contactsctl.ContactController (the detail and the form,
// contacts_detail.go and contacts_form.go). One window for the
// application, opened by app.contacts from the main menu, Show Contact of
// an address chip and More Details… of Add to Contacts; it has no
// shortcut of its own (owner's answer 5). The controllers' changes come
// inside the widgets' own signals, so the view renders on the next idle.
// Every string of a book or a contact goes through SetUseMarkup(false)
// (rule 3); every text is ui/internal/contacts'.

// contactsWin is the open Contacts window, nil when closed.
var contactsWin *ContactsWindow

// ContactsWindow is the Contacts window.
type ContactsWindow struct {
	*adw.Window

	main    *Window
	opts    contactsctl.Options
	ctl     *contactsctl.WindowController
	contact *contactsctl.ContactController

	toasts             *adw.ToastOverlay
	outer, inner       *adw.NavigationSplitView
	booksPage          *adw.NavigationPage
	listPage, detailPg *adw.NavigationPage
	bookList           *gtk.ListBox
	newButton          *gtk.Button
	listTitle          *adw.WindowTitle
	searchBar          *gtk.SearchBar
	searchEntry        *gtk.SearchEntry
	banners            *gtk.Box
	listStack          *gtk.Stack
	listScroller       *gtk.ScrolledWindow
	contactList        *gtk.ListBox
	listEmpty          *adw.StatusPage
	status             *gtk.Label

	detailStack  *gtk.Stack
	detailBox    *gtk.Box
	detailEmpty  *adw.StatusPage
	openPrefs    *gtk.Button
	editButton   *gtk.Button
	doneButton   *gtk.Button
	cancelButton *gtk.Button
	formBox      *gtk.Box
	formScroller *gtk.ScrolledWindow

	// bookRows and rowIDs are what each row of the sidebar and of the
	// list stands for ("" a heading); itemsKey, rowsKey and bannersKey
	// what they were built from.
	bookRows []api.AddressBookID
	rowIDs   []api.ContactID
	// listRows are the rows the list shows and listAvatars their avatars
	// (nil for a heading), detailAv and detailPhoto the detail's
	// (contacts_photos.go).
	listRows    []contacts.Row
	listAvatars []*adw.Avatar
	detailAv    *adw.Avatar
	detailPhoto contacts.Avatar
	detailRef   contactsctl.ContactRef
	itemsKey    string
	rowsKey     string
	bannersKey  string
	// detailKey is what the detail was built from; form is the form
	// built into formBox (contacts_form.go).
	detailKey string
	form      *contactForm

	lastErr, lastContactErr string
	syncing                 bool
	pending                 bool
	closed                  bool
	// unlisten ends the notifications (listenContacts).
	unlisten func()
	// closing: the window closes without asking (after Discard).
	closing bool
	// quitAsking: Quit's DiscardQuestion is up (ConfirmContactsQuit).
	quitAsking bool
}

// OpenContacts presents the Contacts window, building it the first time.
func (w *Window) OpenContacts() *ContactsWindow {
	if contactsWin != nil && !contactsWin.closed {
		contactsWin.Present()
		return contactsWin
	}
	contactsWin = newContactsWindow(w)
	contactsWin.Present()
	return contactsWin
}

func newContactsWindow(w *Window) *ContactsWindow {
	b := data.Builder("contacts_window.ui")
	get := func(id string) any { return b.GetObject(id).Cast() }
	cw := &ContactsWindow{
		Window:       get("contacts_window").(*adw.Window),
		main:         w,
		toasts:       get("toasts").(*adw.ToastOverlay),
		outer:        get("outer_split").(*adw.NavigationSplitView),
		inner:        get("inner_split").(*adw.NavigationSplitView),
		booksPage:    get("books_page").(*adw.NavigationPage),
		listPage:     get("list_page").(*adw.NavigationPage),
		detailPg:     get("detail_page").(*adw.NavigationPage),
		bookList:     get("book_list").(*gtk.ListBox),
		newButton:    get("new_button").(*gtk.Button),
		listTitle:    get("list_title").(*adw.WindowTitle),
		searchBar:    get("search_bar").(*gtk.SearchBar),
		searchEntry:  get("search_entry").(*gtk.SearchEntry),
		banners:      get("banners").(*gtk.Box),
		listStack:    get("list_stack").(*gtk.Stack),
		listScroller: get("list_scroller").(*gtk.ScrolledWindow),
		contactList:  get("contact_list").(*gtk.ListBox),
		listEmpty:    get("list_empty").(*adw.StatusPage),
		status:       get("status_label").(*gtk.Label),
		detailStack:  get("detail_stack").(*gtk.Stack),
		detailBox:    get("detail_box").(*gtk.Box),
		detailEmpty:  get("detail_empty").(*adw.StatusPage),
		openPrefs:    get("open_prefs_button").(*gtk.Button),
		editButton:   get("edit_button").(*gtk.Button),
		doneButton:   get("done_button").(*gtk.Button),
		cancelButton: get("cancel_button").(*gtk.Button),
		formBox:      get("form_box").(*gtk.Box),
		formScroller: get("form_scroller").(*gtk.ScrolledWindow),
	}
	if w.app != nil {
		cw.SetApplication(&w.app.Application)
	}
	tr := i18n.Tr
	cw.SetTitle(contacts.WindowTitle(tr))
	cw.booksPage.SetTitle(contacts.WindowTitle(tr))
	cw.listPage.SetTitle(contacts.WindowTitle(tr))
	cw.detailPg.SetTitle(contacts.WindowTitle(tr))
	cw.newButton.SetTooltipText(contacts.NewContact(tr))
	cw.searchEntry.SetPlaceholderText(contacts.SearchContacts(tr))
	get("search_button").(*gtk.ToggleButton).SetTooltipText(contacts.SearchContacts(tr))
	cw.editButton.SetLabel(contacts.Edit(tr))
	cw.doneButton.SetLabel(contacts.Done(tr))
	cw.cancelButton.SetLabel(contacts.Cancel(tr))
	cw.openPrefs.SetLabel(contacts.OpenPreferences(tr))
	cw.status.SetUseMarkup(false)

	cw.opts = w.contactsOptions()
	cw.opts.Log = w.log.With("part", "contacts")
	cw.ctl = contactsctl.NewWindowController(cw.opts)
	cw.contact = contactsctl.NewContactController(cw.opts, cw.ctl.Book)
	cw.ctl.OnChange = cw.schedule
	cw.ctl.OnSelect = func(id api.ContactID) { cw.contact.Open(id) }
	cw.ctl.OnSignIn = cw.signIn
	cw.contact.OnChange = cw.schedule
	cw.contact.OnQuestion = cw.ask
	cw.contact.OnCreated = func(api.ContactID) { cw.ctl.Reload() }
	cw.contact.OnDeleted = func(api.ContactID) {
		cw.ctl.Select("")
		cw.ctl.Reload()
		cw.inner.SetShowContent(false)
	}

	cw.bookList.ConnectRowSelected(cw.onBookSelected)
	cw.contactList.ConnectRowSelected(cw.onContactSelected)
	cw.searchEntry.ConnectSearchChanged(func() { cw.ctl.SetQuery(cw.searchEntry.Text()) })
	cw.searchBar.ConnectEntry(cw.searchEntry)
	cw.searchBar.SetKeyCaptureWidget(cw)
	cw.listScroller.VAdjustment().ConnectValueChanged(cw.nearEnd)
	cw.newButton.ConnectClicked(cw.newContact)
	cw.editButton.ConnectClicked(func() { cw.contact.BeginEdit() })
	cw.doneButton.ConnectClicked(cw.save)
	cw.cancelButton.ConnectClicked(cw.cancelEdit)
	cw.openPrefs.ConnectClicked(func() {
		d := NewPreferences(w.settings, w.client, w.assist, w.log, w.calendarSource)
		d.SetVisiblePageName("contacts")
		d.Present(cw)
	})
	cw.installShortcuts()
	cw.unlisten = listenContacts(cw.handle)
	cw.ConnectCloseRequest(cw.onClose)
	cw.watchPhotos()

	cw.ctl.Start()
	cw.render()
	return cw
}

// installShortcuts: Ctrl+F opens the search (W6), Escape closes it.
func (cw *ContactsWindow) installShortcuts() {
	sc := gtk.NewShortcutController()
	sc.SetScope(gtk.ShortcutScopeManaged)
	sc.AddShortcut(gtk.NewShortcut(gtk.NewShortcutTriggerParseString("<Control>f"),
		gtk.NewCallbackAction(func(gtk.Widgetter, *glib.Variant) bool {
			cw.searchBar.SetSearchMode(true)
			cw.inner.SetShowContent(false)
			cw.outer.SetShowContent(true)
			cw.searchEntry.GrabFocus()
			return true
		})))
	cw.AddController(sc)
}

func (cw *ContactsWindow) toast(text string) {
	if text != "" && !cw.closed {
		cw.toasts.AddToast(widget.PlainToast(text))
	}
}

// handle takes a notification of the daemon (or of the samples).
func (cw *ContactsWindow) handle(method string, params json.RawMessage) {
	if cw.closed {
		return
	}
	cw.ctl.Handle(method, params)
	if n, ok := contactsctl.ContactsChanged(method, params); ok {
		cw.contact.ContactsChanged(n)
	}
}

// onClose asks before an edited form is dropped, then ends the
// controllers.
func (cw *ContactsWindow) onClose() bool {
	if !cw.closing && cw.contact.Dirty() {
		cw.askDiscard(func() {
			cw.closing = true
			cw.Close()
		})
		return true
	}
	cw.closed = true
	cw.unlisten()
	cw.main.photoHub().removePhotoView(contactsListPhotos)
	cw.main.photoHub().removePhotoView(contactsDetailPhotos)
	cw.ctl.Stop()
	cw.contact.Stop()
	if contactsWin == cw {
		contactsWin = nil
	}
	return false
}

// schedule renders on the next idle: a controller's change arrives
// inside a widget's own signal.
func (cw *ContactsWindow) schedule() {
	if cw.pending || cw.closed {
		return
	}
	cw.pending = true
	glib.IdleAdd(func() {
		cw.pending = false
		if !cw.closed {
			cw.render()
		}
	})
}

func (cw *ContactsWindow) render() {
	cw.syncing = true
	defer func() { cw.syncing = false }()
	if e := cw.ctl.Error(); e != cw.lastErr {
		cw.lastErr = e
		cw.toast(e)
	}
	cw.newButton.SetSensitive(cw.ctl.CanCreate())
	cw.renderSidebar()
	cw.renderBanners()
	cw.renderList()
	cw.renderDetail()
}

// ---------------------------------------------------------------------------
// The sidebar
// ---------------------------------------------------------------------------

func itemsKey(items []contacts.Item) string {
	var b strings.Builder
	for _, i := range items {
		fmt.Fprintf(&b, "%d|%s|%s|%s|%s|%d|%v\n", i.Kind, i.Book, i.AccountID, i.Title, i.Badge, i.Count, i.ReadOnly)
	}
	return b.String()
}

func (cw *ContactsWindow) renderSidebar() {
	items := cw.ctl.Items()
	if k := itemsKey(items); k != cw.itemsKey {
		cw.itemsKey = k
		removeListBoxRows(cw.bookList)
		cw.bookRows = cw.bookRows[:0]
		for _, it := range items {
			if !it.Selectable() {
				row, _ := newHeaderRow(it.Title, it.Badge, false, false)
				cw.bookList.Append(row)
				cw.bookRows = append(cw.bookRows, "")
				continue
			}
			cw.bookList.Append(bookRow(it))
			cw.bookRows = append(cw.bookRows, it.Book)
		}
	}
	sel := cw.ctl.SelectedBook()
	for i, b := range cw.bookRows {
		if b != "" && b == sel {
			if r := cw.bookList.RowAtIndex(i); r != nil && !r.IsSelected() {
				cw.bookList.SelectRow(r)
			}
			return
		}
	}
	cw.bookList.UnselectAll()
}

// bookRow is a selectable row of the sidebar: the name and the count.
func bookRow(it contacts.Item) *gtk.ListBoxRow {
	title := gtk.NewLabel(it.Title)
	title.SetUseMarkup(false)
	title.SetXAlign(0)
	title.SetHExpand(true)
	title.SetEllipsize(pango.EllipsizeEnd)
	count := gtk.NewLabel(fmt.Sprint(it.Count))
	count.AddCSSClass("dim-label")
	count.AddCSSClass("numeric")
	box := gtk.NewBox(gtk.OrientationHorizontal, 6)
	box.Append(title)
	box.Append(count)
	row := gtk.NewListBoxRow()
	row.SetChild(box)
	return row
}

// removeListBoxRows empties a list box.
func removeListBoxRows(l *gtk.ListBox) {
	for c := l.FirstChild(); c != nil; c = l.FirstChild() {
		l.Remove(c)
	}
}

func (cw *ContactsWindow) onBookSelected(row *gtk.ListBoxRow) {
	if cw.syncing || row == nil {
		return
	}
	i := row.Index()
	if i < 0 || i >= len(cw.bookRows) || cw.bookRows[i] == "" {
		return
	}
	book := cw.bookRows[i]
	cw.leaveForm(func() {
		cw.ctl.SelectBook(book)
		cw.outer.SetShowContent(true)
		cw.inner.SetShowContent(false)
	}, cw.schedule)
}

// ---------------------------------------------------------------------------
// The consent banners
// ---------------------------------------------------------------------------

func (cw *ContactsWindow) renderBanners() {
	need := cw.ctl.Consent()
	key := fmt.Sprint(need)
	if key == cw.bannersKey {
		return
	}
	cw.bannersKey = key
	for c := cw.banners.FirstChild(); c != nil; c = cw.banners.FirstChild() {
		cw.banners.Remove(c)
	}
	tr := i18n.Tr
	for _, n := range need {
		bn := adw.NewBanner(contacts.ConsentBanner(n.Account, tr))
		bn.SetUseMarkup(false)
		if n.SignIn {
			id := n.AccountID
			bn.SetButtonLabel(contacts.Allow(tr))
			bn.ConnectButtonClicked(func() { cw.ctl.AllowConsent(id) })
		}
		bn.SetRevealed(true)
		cw.banners.Append(bn)
		if !n.SignIn {
			online := adw.NewBanner(contacts.ConsentOnline(tr))
			online.SetUseMarkup(false)
			online.SetRevealed(true)
			cw.banners.Append(online)
		}
	}
}

// signIn is Allow… of a consent banner (account.oauthStart with
// contacts).
func (cw *ContactsWindow) signIn(id api.AccountID) {
	if contactsctl.SamplesOn() {
		cw.toast(contacts.ConsentOnline(i18n.Tr))
		return
	}
	contactsSignIn(cw, cw.main.client, cw.main.log, id, cw.toast)
}

// ---------------------------------------------------------------------------
// The list
// ---------------------------------------------------------------------------

func rowsKey(rows []contacts.Row) string {
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "%s|%s|%s|%s\n", r.Heading, r.Contact.ID, r.Title, r.Detail)
	}
	return b.String()
}

func (cw *ContactsWindow) renderList() {
	tr := i18n.Tr
	if it, ok := contacts.Find(cw.ctl.Items(), cw.ctl.SelectedBook()); ok {
		cw.listTitle.SetTitle(it.Title)
		cw.listPage.SetTitle(it.Title)
	} else {
		cw.listTitle.SetTitle(contacts.WindowTitle(tr))
	}
	cw.listTitle.SetSubtitle(cw.ctl.Subtitle())

	rows := cw.ctl.Rows()
	if k := rowsKey(rows); k != cw.rowsKey {
		cw.rowsKey = k
		removeListBoxRows(cw.contactList)
		cw.rowIDs = cw.rowIDs[:0]
		cw.listAvatars = cw.listAvatars[:0]
		for _, r := range rows {
			if r.IsHeading() {
				cw.contactList.Append(sectionRow(r.Heading))
				cw.rowIDs = append(cw.rowIDs, "")
				cw.listAvatars = append(cw.listAvatars, nil)
				continue
			}
			row, av := contactRow(r)
			cw.contactList.Append(row)
			cw.rowIDs = append(cw.rowIDs, r.Contact.ID)
			cw.listAvatars = append(cw.listAvatars, av)
		}
	}
	// The same rows (the key), maybe with another photo state.
	cw.listRows = rows
	cw.main.photosChanged()
	cw.selectListRow(cw.ctl.Selected())

	switch {
	case !cw.ctl.Loaded() || (len(rows) == 0 && cw.ctl.Loading()):
		cw.listStack.SetVisibleChildName("loading")
	case cw.ctl.ListEmpty() != contacts.EmptyNone:
		title, body := contacts.EmptyText(cw.ctl.ListEmpty(), tr)
		cw.listEmpty.SetTitle(glib.MarkupEscapeText(title))
		cw.listEmpty.SetDescription(glib.MarkupEscapeText(body))
		cw.listStack.SetVisibleChildName("empty")
	default:
		cw.listStack.SetVisibleChildName("list")
	}
	s := cw.ctl.Status()
	cw.status.SetText(s)
	cw.status.SetVisible(s != "")
}

func (cw *ContactsWindow) selectListRow(id api.ContactID) {
	if id != "" {
		for i, r := range cw.rowIDs {
			if r == id {
				if row := cw.contactList.RowAtIndex(i); row != nil && !row.IsSelected() {
					cw.contactList.SelectRow(row)
				}
				return
			}
		}
	}
	cw.contactList.UnselectAll()
}

// sectionRow is an index heading of the list ("N").
func sectionRow(letter string) *gtk.ListBoxRow {
	l := gtk.NewLabel(letter)
	l.SetUseMarkup(false)
	l.SetXAlign(0)
	l.AddCSSClass("caption-heading")
	l.AddCSSClass("dim-label")
	l.SetMarginTop(6)
	row := gtk.NewListBoxRow()
	row.SetActivatable(false)
	row.SetSelectable(false)
	row.SetChild(l)
	return row
}

// contactRow is a contact of the list: initials (or the photo,
// contacts_photos.go), name and company or address; av is its avatar.
func contactRow(r contacts.Row) (row *gtk.ListBoxRow, av *adw.Avatar) {
	av = adw.NewAvatar(32, r.Title, true)
	title := gtk.NewLabel(r.Title)
	title.SetUseMarkup(false)
	title.SetXAlign(0)
	title.SetEllipsize(pango.EllipsizeEnd)
	texts := gtk.NewBox(gtk.OrientationVertical, 0)
	texts.SetVAlign(gtk.AlignCenter)
	texts.SetHExpand(true)
	texts.Append(title)
	if r.Detail != "" {
		d := gtk.NewLabel(r.Detail)
		d.SetUseMarkup(false)
		d.SetXAlign(0)
		d.SetEllipsize(pango.EllipsizeEnd)
		d.AddCSSClass("dim-label")
		d.AddCSSClass("caption")
		texts.Append(d)
	}
	box := gtk.NewBox(gtk.OrientationHorizontal, 10)
	box.SetMarginTop(3)
	box.SetMarginBottom(3)
	box.Append(av)
	box.Append(texts)
	row = gtk.NewListBoxRow()
	row.SetChild(box)
	return row, av
}

func (cw *ContactsWindow) onContactSelected(row *gtk.ListBoxRow) {
	if cw.syncing || row == nil {
		return
	}
	i := row.Index()
	if i < 0 || i >= len(cw.rowIDs) || cw.rowIDs[i] == "" {
		return
	}
	id := cw.rowIDs[i]
	cw.leaveForm(func() {
		cw.ctl.Select(id)
		if cw.contact.ID() != id {
			// Selected again after Show Contact showed it without a row.
			cw.contact.Open(id)
		}
		cw.inner.SetShowContent(true)
	}, cw.schedule)
}

// nearEnd asks for the next page when the list is scrolled near its end.
func (cw *ContactsWindow) nearEnd() {
	adj := cw.listScroller.VAdjustment()
	if adj.Value()+adj.PageSize() >= adj.Upper()-240 {
		cw.ctl.LoadMore()
	}
}

// ---------------------------------------------------------------------------
// Entry points
// ---------------------------------------------------------------------------

// leaveForm runs next, asking first when a form with changes would be
// dropped; refused runs instead when the user keeps editing.
func (cw *ContactsWindow) leaveForm(next, refused func()) {
	if !cw.contact.Dirty() {
		cw.contact.CancelEdit()
		next()
		return
	}
	cw.askDiscard(func() {
		cw.contact.CancelEdit()
		next()
	}, refused)
}

// askDiscard asks DiscardQuestion; yes runs on Discard, the optional
// no on Cancel.
func (cw *ContactsWindow) askDiscard(yes func(), no ...func()) {
	cw.ask(contacts.DiscardQuestion(i18n.Tr), func(ok bool) {
		if ok {
			yes()
			return
		}
		for _, f := range no {
			f()
		}
	})
}

// newContact is "+": the form of a new contact in NewContactBook.
func (cw *ContactsWindow) newContact() {
	book := cw.ctl.NewContactBook()
	if book == "" {
		return
	}
	cw.leaveForm(func() { cw.beginNew(book, nil) }, cw.schedule)
}

// beginNew opens the form of a new contact (prefilled when f is not nil).
func (cw *ContactsWindow) beginNew(book api.AddressBookID, f *contacts.Form) {
	cw.ctl.Select("")
	cw.contact.BeginNew(book, f)
	cw.outer.SetShowContent(true)
	cw.inner.SetShowContent(true)
}

// ShowContact shows a contact in the detail, whichever book is listed.
func (cw *ContactsWindow) ShowContact(id api.ContactID) {
	cw.leaveForm(func() {
		cw.ctl.Select("")
		cw.contact.Open(id)
		cw.outer.SetShowContent(true)
		cw.inner.SetShowContent(true)
	}, nil)
}

// NewContactFrom opens the form prefilled (More Details…); an empty book
// is New Contact's.
func (cw *ContactsWindow) NewContactFrom(f *contacts.Form) {
	cw.leaveForm(func() {
		book := f.Book
		if book == "" {
			book = cw.ctl.NewContactBook()
			f.Book = book
		}
		cw.beginNew(book, f)
	}, nil)
}

// ---------------------------------------------------------------------------
// Questions
// ---------------------------------------------------------------------------

// ask shows a question of the controllers as an Adw.AlertDialog; answer
// gets true for Confirm, false for Cancel and closing.
func (cw *ContactsWindow) ask(q contacts.Question, answer func(bool)) {
	tr := i18n.Tr
	d := adw.NewAlertDialog(q.Heading, q.Body)
	d.SetHeadingUseMarkup(false)
	d.SetBodyUseMarkup(false)
	d.AddResponse("cancel", contacts.Cancel(tr))
	d.AddResponse("confirm", q.Confirm)
	if q.Destructive {
		d.SetResponseAppearance("confirm", adw.ResponseDestructive)
	} else {
		d.SetResponseAppearance("confirm", adw.ResponseSuggested)
		d.SetDefaultResponse("confirm")
	}
	d.SetCloseResponse("cancel")
	d.ConnectResponse(func(response string) { answer(response == "confirm") })
	d.Present(cw)
}

// boardLoop keeps glibLoop a board.Loop here as well.
var _ board.Loop = glibLoop{}
