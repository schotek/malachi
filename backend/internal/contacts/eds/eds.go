// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package eds reads the system address books of Evolution Data Server over
// the session bus, for recipient completion. It is read-only: it lists the
// address-book sources of the registry (org.gnome.evolution.dataserver.Sources)
// and runs search queries against the books the factory
// (org.gnome.evolution.dataserver.AddressBook) opens for it. Contacts are
// never stored; every search asks the books again.
//
// The client speaks raw D-Bus through godbus like internal/auth/goa: it
// connects lazily and reconnects once when the bus drops. Without a
// session bus, or without Evolution Data Server, every call fails with
// unavailable, which the core turns into "no address-book results".
package eds

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/schotek/malachi/backend/internal/contacts"
	"github.com/schotek/malachi/backend/pkg/api"
)

const (
	// The source registry: an ObjectManager whose objects carry the
	// org.gnome.evolution.dataserver.Source interface with the UID and
	// the keyfile Data of each source.
	sourcesService     = "org.gnome.evolution.dataserver.Sources5"
	sourcesPath        = dbus.ObjectPath("/org/gnome/evolution/dataserver/SourceManager")
	ifaceObjectManager = "org.freedesktop.DBus.ObjectManager"
	ifaceSource        = "org.gnome.evolution.dataserver.Source"

	// The address-book factory opens a book by source uid and answers with
	// the object path and bus name of the book object.
	booksService = "org.gnome.evolution.dataserver.AddressBook10"
	factoryPath  = dbus.ObjectPath("/org/gnome/evolution/dataserver/AddressBookFactory")
	ifaceFactory = "org.gnome.evolution.dataserver.AddressBookFactory"
	ifaceBook    = "org.gnome.evolution.dataserver.AddressBook"

	// CallTimeout bounds a local round trip: listing sources, opening a
	// book. BookTimeout bounds one book's query — a directory book asks
	// its server. SearchBudget bounds a whole search, under the UI's 5 s
	// call timeout, so a slow book costs its own results, not everyone's.
	CallTimeout  = 5 * time.Second
	BookTimeout  = 3 * time.Second
	SearchBudget = 4 * time.Second

	// booksTTL is how long the list of books is reused between searches.
	booksTTL = time.Minute
	// maxCardsPerBook caps how many cards of one reply are parsed.
	maxCardsPerBook = 200
	// defaultSearchLimit applies when Search is called with limit <= 0.
	defaultSearchLimit = 50
)

// Keyfile groups and keys of an ESource (see the Data property).
const (
	groupSource       = "Data Source"
	groupAddressBook  = "Address Book"
	groupAutocomplete = "Autocomplete"
	groupCollection   = "Collection"
	groupGOA          = "GNOME Online Accounts"
)

var (
	// uidPattern is the shape of a source uid: a hash or a name such as
	// "system-address-book". It is passed to the factory as a string, but
	// it also names cache directories, so nothing beyond this alphabet.
	uidPattern = regexp.MustCompile(`^[A-Za-z0-9_.@-]{1,128}$`)
	// busNamePattern is what the factory may name as the book's owner.
	busNamePattern = regexp.MustCompile(`^org\.gnome\.evolution\.dataserver\.[A-Za-z0-9_.]{1,200}$`)
)

// ValidUID reports whether uid can be a source uid.
func ValidUID(uid string) bool { return uidPattern.MatchString(uid) }

// Client talks to Evolution Data Server over the session bus.
type Client struct {
	log  *slog.Logger
	dial func() (bus, error)
	now  func() time.Time

	mu      sync.Mutex
	conn    bus
	books   []contacts.Book // cached list, valid until booksAt + booksTTL
	booksAt time.Time
	open    map[string]bookHandle // by uid; valid for conn only
}

// bookHandle is an opened book: where the factory said it lives.
type bookHandle struct {
	dest string
	path dbus.ObjectPath
}

var _ contacts.Directory = (*Client)(nil)

// New returns a client that connects on first use.
func New(log *slog.Logger) *Client {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Client{log: log.With("component", "eds"), dial: dialSessionBus, now: time.Now, open: map[string]bookHandle{}}
}

// Close drops the bus connection, the opened books and the cached list.
// The client can still be used afterwards; it reconnects.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.books, c.booksAt = nil, time.Time{}
	c.open = map[string]bookHandle{}
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

// Books lists the address books that take part in completion: enabled
// sources with an [Address Book] group whose [Autocomplete] IncludeMe is
// not false, with the identity of their collection when they have one.
// The list is cached for a minute. Without a session bus or Evolution
// Data Server the error is unavailable.
func (c *Client) Books(ctx context.Context) ([]contacts.Book, error) {
	c.mu.Lock()
	if c.books != nil && c.now().Sub(c.booksAt) < booksTTL {
		out := append([]contacts.Book(nil), c.books...)
		c.mu.Unlock()
		return out, nil
	}
	c.mu.Unlock()

	var out []contacts.Book
	err := c.withRetry(ctx, func(ctx context.Context, conn bus) error {
		var objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant
		if err := call(ctx, conn, sourcesService, sourcesPath, ifaceObjectManager+".GetManagedObjects", CallTimeout).Store(&objects); err != nil {
			return err
		}
		out = parseSources(objects)
		return nil
	})
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.books, c.booksAt = out, c.now()
	c.mu.Unlock()
	return append([]contacts.Book(nil), out...), nil
}

// Search queries the given books for cards matching query anywhere in any
// field, in parallel, each under BookTimeout and all under SearchBudget.
// A book that fails or times out is logged and skipped; the error is
// returned only when no book answered and the failure was the service
// itself (unavailable). Results keep the books' order, then the reply's.
func (c *Client) Search(ctx context.Context, books []contacts.Book, query string, limit int) ([]contacts.Contact, error) {
	query = strings.TrimSpace(query)
	if query == "" || len(books) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	sexp := containsQuery(query)
	ctx, cancel := context.WithTimeout(ctx, SearchBudget)
	defer cancel()

	type reply struct {
		cards []string
		err   error
	}
	replies := make([]reply, len(books))
	var wg sync.WaitGroup
	for i, bk := range books {
		if !ValidUID(bk.UID) {
			replies[i].err = fmt.Errorf("invalid source uid %q", bk.UID)
			continue
		}
		wg.Add(1)
		go func(i int, bk contacts.Book) {
			defer wg.Done()
			cards, err := c.queryBook(ctx, bk.UID, sexp)
			replies[i] = reply{cards, err}
		}(i, bk)
	}
	wg.Wait()

	var out []contacts.Contact
	answered := 0
	var unavailable error
	for i, r := range replies {
		if r.err != nil {
			c.log.Debug("address book query", "book", books[i].UID, "err", r.err)
			if isUnavailable(r.err) {
				unavailable = r.err
			}
			continue
		}
		answered++
		cards := r.cards
		if len(cards) > maxCardsPerBook {
			cards = cards[:maxCardsPerBook]
		}
		for _, card := range cards {
			name, emails, ok := parseVCard(card)
			if !ok {
				continue
			}
			for _, addr := range emails {
				out = append(out, contacts.Contact{Name: name, Address: addr, Book: books[i].Name})
			}
		}
	}
	if answered == 0 && unavailable != nil {
		return nil, unavailable
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// queryBook opens the book when needed and runs one GetContactList. A
// handle the service no longer knows (it restarted, or closed the book) is
// forgotten and the book opened again, once.
func (c *Client) queryBook(ctx context.Context, uid, sexp string) ([]string, error) {
	var cards []string
	err := c.withRetry(ctx, func(ctx context.Context, conn bus) error {
		for attempt := 0; ; attempt++ {
			h, err := c.handle(ctx, conn, uid)
			if err != nil {
				return err
			}
			cards = cards[:0]
			err = call(ctx, conn, h.dest, h.path, ifaceBook+".GetContactList", BookTimeout, sexp).Store(&cards)
			if err != nil && attempt == 0 && isStaleHandle(err) {
				c.log.Debug("address book handle stale, reopening", "book", uid, "err", err)
				c.forget(uid)
				continue
			}
			return err
		}
	})
	return cards, err
}

// handle returns the opened book, opening it through the factory first
// when this connection has not yet.
func (c *Client) handle(ctx context.Context, conn bus, uid string) (bookHandle, error) {
	c.mu.Lock()
	h, ok := c.open[uid]
	c.mu.Unlock()
	if ok {
		return h, nil
	}
	var path, dest string
	if err := call(ctx, conn, booksService, factoryPath, ifaceFactory+".OpenAddressBook", CallTimeout, uid).Store(&path, &dest); err != nil {
		return bookHandle{}, err
	}
	if !dbus.ObjectPath(path).IsValid() || !busNamePattern.MatchString(dest) {
		// The service answered, so this is its fault, not absence: not
		// unavailable, and never a call to whatever name it pointed at.
		return bookHandle{}, api.NewError(api.CodeServerError, "evolution data server: address book %s: malformed handle %q at %q", uid, path, dest)
	}
	h = bookHandle{dest: dest, path: dbus.ObjectPath(path)}
	var props []string
	if err := call(ctx, conn, h.dest, h.path, ifaceBook+".Open", CallTimeout).Store(&props); err != nil {
		return bookHandle{}, err
	}
	c.mu.Lock()
	c.open[uid] = h
	c.mu.Unlock()
	return h, nil
}

// forget drops an opened book's handle.
func (c *Client) forget(uid string) {
	c.mu.Lock()
	delete(c.open, uid)
	c.mu.Unlock()
}

// --- sources -----------------------------------------------------------------

// source is what completion needs to know about one ESource.
type source struct {
	uid, name, parent, backend  string
	enabled, book, autocomplete bool
	goaID, email                string // set on a collection source
}

// parseSources turns the registry's objects into the books that take part
// in completion, sorted by name then uid. A book inherits the identity of
// its parent collection; a disabled collection hides its books.
func parseSources(objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant) []contacts.Book {
	sources := map[string]source{}
	for _, ifaces := range objects {
		props, ok := ifaces[ifaceSource]
		if !ok {
			continue
		}
		s, ok := parseSource(str(props, "UID"), str(props, "Data"))
		if ok {
			sources[s.uid] = s
		}
	}
	var out []contacts.Book
	for _, s := range sources {
		if !s.book || !s.enabled || !s.autocomplete {
			continue
		}
		b := contacts.Book{UID: s.uid, Name: s.name, Backend: s.backend, Collection: s.parent}
		if parent, ok := sources[s.parent]; ok {
			if !parent.enabled {
				continue
			}
			b.GOAAccountID, b.Email = parent.goaID, parent.email
		}
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].UID < out[j].UID
	})
	return out
}

// parseSource reads one source's keyfile. The e-mail of a collection is
// the Online Accounts address when set, else the collection's identity.
func parseSource(uid, data string) (source, bool) {
	if !ValidUID(uid) {
		return source{}, false
	}
	kf := parseKeyFile(data)
	s := source{
		uid:          uid,
		name:         kf.get(groupSource, "DisplayName"),
		parent:       kf.get(groupSource, "Parent"),
		enabled:      !kf.isFalse(groupSource, "Enabled"),
		book:         kf.has(groupAddressBook),
		backend:      kf.get(groupAddressBook, "BackendName"),
		autocomplete: !kf.isFalse(groupAutocomplete, "IncludeMe"),
	}
	if kf.has(groupGOA) {
		s.goaID = kf.get(groupGOA, "AccountId")
		s.email = normalizeEmail(kf.get(groupGOA, "Address"))
	}
	if s.email == "" {
		s.email = normalizeEmail(kf.get(groupCollection, "Identity"))
	}
	return s, true
}

// normalizeEmail lower-cases a collection identity when it is an address;
// anything else (a bare user name, a URL) is "".
func normalizeEmail(s string) string {
	s, ok := validAddress(s)
	if !ok {
		return ""
	}
	return s
}

// containsQuery builds the search expression matching q anywhere in any
// field. EDS queries are S-expressions with double-quoted strings, in
// which backslash and the double quote are escaped.
func containsQuery(q string) string {
	var b strings.Builder
	b.Grow(len(q) + 48)
	b.WriteString(`(contains "x-evolution-any-field" "`)
	for i := 0; i < len(q); i++ {
		if q[i] == '\\' || q[i] == '"' {
			b.WriteByte('\\')
		}
		b.WriteByte(q[i])
	}
	b.WriteString(`")`)
	return b.String()
}

func str(props map[string]dbus.Variant, key string) string {
	if v, ok := props[key]; ok {
		if s, ok := v.Value().(string); ok {
			return s
		}
	}
	return ""
}

// --- connection management -------------------------------------------------

func call(ctx context.Context, conn bus, dest string, path dbus.ObjectPath, method string, timeout time.Duration, args ...any) *dbus.Call {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return conn.Object(dest, path).CallWithContext(ctx, method, 0, args...)
}

// withRetry runs fn on the current connection, reconnecting once when the
// bus went away underneath. Errors leave here mapped to *api.Error.
func (c *Client) withRetry(ctx context.Context, fn func(context.Context, bus) error) error {
	for attempt := 0; ; attempt++ {
		conn, err := c.ensure()
		if err != nil {
			return mapErr(err)
		}
		err = fn(ctx, conn)
		if err != nil && attempt == 0 && isTransient(err) {
			c.log.Debug("session bus connection lost, reconnecting", "err", err)
			c.reset(conn)
			continue
		}
		return mapErr(err)
	}
}

func (c *Client) ensure() (bus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return c.conn, nil
	}
	conn, err := c.dial()
	if err != nil {
		return nil, fmt.Errorf("connect to session bus: %w", err)
	}
	c.conn = conn
	return conn, nil
}

// reset forgets conn if it is still the current connection, and with it
// the books opened on it.
func (c *Client) reset(conn bus) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == conn {
		c.conn.Close()
		c.conn = nil
		c.open = map[string]bookHandle{}
	}
}

// --- errors ------------------------------------------------------------------

func dbusErrorName(err error) string {
	var perr *dbus.Error
	if errors.As(err, &perr) {
		return perr.Name
	}
	var verr dbus.Error
	if errors.As(err, &verr) {
		return verr.Name
	}
	return ""
}

// isTransient reports whether the bus connection is gone and a reconnect
// is worth one retry.
func isTransient(err error) bool {
	if errors.Is(err, dbus.ErrClosed) {
		return true
	}
	switch dbusErrorName(err) {
	case "org.freedesktop.DBus.Error.Disconnected", "org.freedesktop.DBus.Error.NoReply":
		return true
	}
	return false
}

// isStaleHandle recognises a book object the service no longer exports:
// it restarted, or closed the book behind our back.
func isStaleHandle(err error) bool {
	switch dbusErrorName(err) {
	case "org.freedesktop.DBus.Error.UnknownObject",
		"org.freedesktop.DBus.Error.UnknownMethod",
		"org.freedesktop.DBus.Error.UnknownInterface",
		"org.freedesktop.DBus.Error.ServiceUnknown",
		"org.freedesktop.DBus.Error.NameHasNoOwner":
		return true
	}
	return false
}

func isUnavailable(err error) bool {
	var apiErr *api.Error
	return errors.As(err, &apiErr) && apiErr.Code == api.CodeUnavailable
}

// mapErr turns raw failures into *api.Error: context errors and *api.Error
// pass through; no bus or no service is unavailable; a timeout and
// anything else is serverError naming the D-Bus error.
func mapErr(err error) error {
	if err == nil || errors.Is(err, context.Canceled) {
		return err
	}
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		return apiErr
	}
	name := dbusErrorName(err)
	switch name {
	case "":
		if errors.Is(err, context.DeadlineExceeded) {
			return api.NewError(api.CodeServerError, "evolution data server: timed out")
		}
		return api.NewError(api.CodeUnavailable, "evolution data server: %v", err)
	case "org.freedesktop.DBus.Error.ServiceUnknown",
		"org.freedesktop.DBus.Error.NameHasNoOwner",
		"org.freedesktop.DBus.Error.Spawn.ExecFailed",
		"org.freedesktop.DBus.Error.Spawn.FileNotFound":
		return api.NewError(api.CodeUnavailable, "evolution data server not available: %s", name)
	}
	return api.NewError(api.CodeServerError, "evolution data server: %s", name)
}
