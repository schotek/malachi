// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package contacts

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The window's sidebar and list (W1, W5): the sidebar is All Contacts,
// then the visible books grouped by account in the order of account.list
// (each account a heading with its name and kind capsule), then On This
// Computer with Recently Used last; hidden books are not shown (they are
// chosen in Preferences → Contacts). The list is the books' contacts as
// contact.list orders them, under index headings (ContactSummary.Section).

// The kind capsules: brand and protocol names, never translated.
const (
	BadgeGraph   = "M365"
	BadgeCardDAV = "CARDDAV"
	BadgeEway    = "EWAY"
)

// Badge is a book's kind capsule ("" for Recently Used and an unknown
// kind).
func Badge(k api.AddressBookKind) string {
	switch k {
	case api.AddressBookKindGraph:
		return BadgeGraph
	case api.AddressBookKindCardDAV:
		return BadgeCardDAV
	case "eway":
		return BadgeEway
	}
	return ""
}

// AccountKindBadge is an account's capsule in the sidebar and the
// preferences: M365 for graph, CARDDAV for caldav, EWAY for eway.
func AccountKindBadge(a api.Account) string {
	switch a.Config.Protocol() {
	case api.AccountGraph:
		return BadgeGraph
	case api.AccountCalDAV:
		return BadgeCardDAV
	case api.AccountEway:
		return BadgeEway
	}
	return ""
}

// AccountName is an account's heading in the sidebar and the preferences:
// its address, else its name.
func AccountName(a api.Account) string {
	if e := Clean(a.Config.Email, api.MaxContactFieldBytes); e != "" {
		return e
	}
	return Clean(a.Config.Name, api.MaxContactFieldBytes)
}

// AccountLabel is an account in a sentence and the detail's source line:
// "Microsoft 365 (jana@firma.cz)" for graph, the name (else the address)
// of a CardDAV server; "" for nil.
func AccountLabel(a *api.Account, tr Translator) string {
	if a == nil {
		return ""
	}
	if a.Config.Protocol() == api.AccountGraph {
		if e := AccountName(*a); e != "" {
			return BookChoiceText(microsoft365, e, tr)
		}
		return microsoft365
	}
	if n := Clean(a.Config.Name, api.MaxContactFieldBytes); n != "" {
		return n
	}
	return AccountName(*a)
}

// microsoft365 is the brand's name, never translated.
const microsoft365 = "Microsoft 365"

// BookChoiceText is a book in a choice of books: "Contacts
// (jana@firma.cz)".
func BookChoiceText(book, account string, tr Translator) string {
	if account == "" {
		return book
	}
	// TRANSLATORS: an address book with its account, e.g. "Contacts
	// (jana@example.org)"; also a brand with an address.
	return strings.TrimSpace(fmt.Sprintf(tr.T("%s (%s)"), book, account))
}

// BookName is a book's name as shown (Recently Used translated, an empty
// name as the kind's default).
func BookName(b api.AddressBook, tr Translator) string {
	if b.ID == api.AddressBookRecent || b.Kind == api.AddressBookKindRecent {
		return RecentlyUsed(tr)
	}
	if n := Clean(b.Name, api.MaxContactFieldBytes); n != "" {
		return n
	}
	return WindowTitle(tr)
}

// ItemKind is a sidebar row's kind.
type ItemKind int

// The sidebar rows.
const (
	ItemAll ItemKind = iota
	// ItemAccount is an account's heading (not selectable).
	ItemAccount
	ItemBook
	// ItemLocal is the heading On This Computer (not selectable).
	ItemLocal
	ItemRecent
)

// Item is a sidebar row.
type Item struct {
	Kind ItemKind
	// Book is what the row lists (api.AddressBookAll for All Contacts);
	// "" for a heading.
	Book      api.AddressBookID
	AccountID api.AccountID
	Title     string
	// Badge is an account heading's kind capsule.
	Badge string
	// Count is the row's number of contacts (-1 for a heading).
	Count int
	// ReadOnly: the book cannot be changed (Recently Used, a read-only
	// book; All Contacts never).
	ReadOnly bool
}

// Selectable reports a row that lists contacts.
func (i Item) Selectable() bool {
	return i.Kind == ItemAll || i.Kind == ItemBook || i.Kind == ItemRecent
}

// Sidebar is the sidebar of books (addressBook.list) and accounts
// (account.list, for the order and the headings). All Contacts is there
// only with a visible book besides Recently Used.
func Sidebar(books []api.AddressBook, accounts []api.Account, tr Translator) []Item {
	byAccount := map[api.AccountID][]api.AddressBook{}
	var order []api.AccountID
	var recent *api.AddressBook
	total := 0
	for i := range books {
		b := books[i]
		if b.ID == api.AddressBookRecent || b.Kind == api.AddressBookKindRecent {
			recent = &books[i]
			continue
		}
		if !b.Visible {
			continue
		}
		if _, ok := byAccount[b.AccountID]; !ok {
			order = append(order, b.AccountID)
		}
		byAccount[b.AccountID] = append(byAccount[b.AccountID], b)
		total += max(b.Count, 0)
	}
	var out []Item
	if len(order) > 0 {
		out = append(out, Item{Kind: ItemAll, Book: api.AddressBookAll, Title: AllContacts(tr), Count: total})
	}
	known := map[api.AccountID]api.Account{}
	var accountOrder []api.AccountID
	for _, a := range accounts {
		known[a.ID] = a
		if _, ok := byAccount[a.ID]; ok {
			accountOrder = append(accountOrder, a.ID)
		}
	}
	for _, id := range order {
		if _, ok := known[id]; !ok {
			accountOrder = append(accountOrder, id)
		}
	}
	for _, id := range accountOrder {
		h := Item{Kind: ItemAccount, AccountID: id, Count: -1}
		if a, ok := known[id]; ok {
			h.Title, h.Badge = AccountName(a), AccountKindBadge(a)
		} else if bs := byAccount[id]; len(bs) > 0 {
			h.Badge = Badge(bs[0].Kind)
		}
		out = append(out, h)
		for _, b := range byAccount[id] {
			out = append(out, Item{Kind: ItemBook, Book: b.ID, AccountID: id, Title: BookName(b, tr),
				Count: max(b.Count, 0), ReadOnly: b.ReadOnly || b.SearchOnly || !knownKind(b.Kind)})
		}
	}
	if recent != nil {
		out = append(out, Item{Kind: ItemLocal, Title: OnThisComputer(tr), Count: -1},
			Item{Kind: ItemRecent, Book: api.AddressBookRecent, Title: RecentlyUsed(tr), Count: max(recent.Count, 0), ReadOnly: true})
	}
	return out
}

func knownKind(k api.AddressBookKind) bool {
	return k == api.AddressBookKindGraph || k == api.AddressBookKindCardDAV
}

// HasBooks reports a sidebar with a book besides Recently Used.
func HasBooks(items []Item) bool {
	for _, i := range items {
		if i.Kind == ItemBook {
			return true
		}
	}
	return false
}

// DefaultSelection is the row selected when the window opens or the
// selected row is gone: All Contacts, else Recently Used, else nothing.
func DefaultSelection(items []Item) api.AddressBookID {
	for _, i := range items {
		if i.Kind == ItemAll {
			return i.Book
		}
	}
	for _, i := range items {
		if i.Selectable() {
			return i.Book
		}
	}
	return ""
}

// Find is the selectable row of a book.
func Find(items []Item, book api.AddressBookID) (Item, bool) {
	for _, i := range items {
		if i.Selectable() && i.Book == book {
			return i, true
		}
	}
	return Item{}, false
}

// ListSubtitle is the list's subtitle: "All Contacts · 412", "Recently
// Used · 318 addresses", for a book "Family · 82 contacts"; total is
// contact.list's total.
func ListSubtitle(item Item, total int, tr Translator) string {
	switch item.Kind {
	case ItemAll:
		return TwoParts(item.Title, strconv.Itoa(total), tr)
	case ItemRecent:
		return TwoParts(item.Title, AddressCount(total, tr), tr)
	}
	return TwoParts(item.Title, ContactCount(total, tr), tr)
}

// SyncedAt is the oldest lastSyncAt of the visible books of the accounts
// (Recently Used has none); false when no book has one.
func SyncedAt(books []api.AddressBook) (time.Time, bool) {
	var oldest time.Time
	ok := false
	for _, b := range books {
		if !b.Visible || b.LastSyncAt == nil || b.ID == api.AddressBookRecent {
			continue
		}
		if !ok || b.LastSyncAt.Before(oldest) {
			oldest, ok = *b.LastSyncAt, true
		}
	}
	return oldest, ok
}

// StatusLine is the status bar's text: SyncedLine with the oldest
// synchronisation, "" without one.
func StatusLine(books []api.AddressBook, now time.Time, tr Translator) string {
	if t, ok := SyncedAt(books); ok {
		return SyncedLine(t, now, tr)
	}
	return ""
}

// Writable reports a book a contact can be created in: visible or not,
// not Recently Used, not read only, not search only, of a known kind.
func Writable(b api.AddressBook) bool {
	return b.ID != api.AddressBookRecent && !b.ReadOnly && !b.SearchOnly && knownKind(b.Kind)
}

// ---------------------------------------------------------------------------
// The list
// ---------------------------------------------------------------------------

// Row is a row of the list: a section heading or a contact.
type Row struct {
	// Heading is a section's letter ("" for a contact's row).
	Heading string
	Contact api.ContactSummary
	// Title is the contact's name (SummaryTitle); Detail its company,
	// else its address.
	Title, Detail string
	Initials      string
}

// IsHeading reports a section heading.
func (r Row) IsHeading() bool { return r.Heading != "" }

// Rows are the list's rows: a heading before the first contact of each
// section (consecutive contacts with the same Section, in the order
// contact.list returned them; an empty section is "#"). The rows of a
// search have no headings (flat, as W5 c).
func Rows(list []api.ContactSummary, headings bool, tr Translator) []Row {
	var out []Row
	last := "\x00"
	for _, c := range list {
		sec := SectionOf(c)
		if headings && sec != last {
			out = append(out, Row{Heading: sec})
			last = sec
		}
		title := SummaryTitle(c, tr)
		detail := Clean(c.Org, api.MaxContactFieldBytes)
		if detail == "" || detail == title {
			detail = Clean(c.Email, api.MaxContactFieldBytes)
		}
		if detail == title {
			detail = ""
		}
		out = append(out, Row{Contact: c, Title: title, Detail: detail, Initials: Initials(title, c.Email)})
	}
	return out
}

// SectionOf is a contact's index heading: Section cleaned to one upper
// case character, "#" when empty.
func SectionOf(c api.ContactSummary) string {
	s := Clean(c.Section, 16)
	for _, r := range s {
		return strings.ToUpper(string(r))
	}
	return "#"
}

// ---------------------------------------------------------------------------
// Layout
// ---------------------------------------------------------------------------

// Breakpoints are the window widths at which its columns fold: below
// Sidebar the sidebar collapses (a toggle shows it over the list), below
// Detail the detail covers the list with a back button (‹ Contacts).
type Breakpoints struct{ Sidebar, Detail float64 }

// MacBreakpoints are the macOS window's in points (W5 d); GTK folds at
// 900/600 sp like the main window (W6), Windows like its main window.
var (
	MacBreakpoints = Breakpoints{Sidebar: 720, Detail: 520}
	GTKBreakpoints = Breakpoints{Sidebar: 900, Detail: 600}
)

// Fold is how a window of width is laid out.
type Fold struct {
	// SidebarCollapsed: the sidebar is hidden (shown over the list on
	// demand).
	SidebarCollapsed bool
	// DetailOverList: one column; a selected contact's detail covers the
	// list.
	DetailOverList bool
}

// FoldFor is the layout of a window of width.
func FoldFor(width float64, bp Breakpoints) Fold {
	return Fold{SidebarCollapsed: width < bp.Sidebar, DetailOverList: width < bp.Detail}
}
