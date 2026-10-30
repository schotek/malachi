// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The contacts of a Microsoft Graph account (docs/plans/address-book.md
// A1, A5, A6, A15, §4.2): contact folders, delta per folder with immutable
// ids, and server-first writes guarded by the contact's changeKey.
//
// Every string from the service is hostile input: it is cleaned and
// capped as the calendar's are (calendar.CleanLine / CleanBlock). Ids and
// change keys must be printable ASCII within maxContactIDBytes; an item
// whose id is not is skipped. The raw JSON of each contact is kept for the
// cache (Contact.Raw), never shown, and dropped when it is over
// MaxContactRawBytes.

// DefaultContactFolderID names the account's default contact folder
// ("Contacts", /me/contacts). Graph does not list it in /me/contactFolders
// and gives it no stable id of its own there, so it has this one.
const DefaultContactFolderID = "default"

// Caps of contact data.
const (
	// MaxContactRawBytes caps the raw JSON kept of one contact.
	MaxContactRawBytes = 1 << 20
	// MaxContactFolders caps the folders ContactFolders returns.
	MaxContactFolders = 200
	// MaxContactEmails is Graph's own limit of emailAddresses.
	MaxContactEmails = 3
	// MaxContactPhones caps each phone list read or written.
	MaxContactPhones = 20
	// MaxContactLineBytes caps one single-line field.
	MaxContactLineBytes = 256
	// MaxContactAddressLineBytes caps one part of a postal address
	// (street may hold several lines).
	MaxContactAddressLineBytes = 1024
	// MaxContactURLBytes caps businessHomePage.
	MaxContactURLBytes = calendar.MaxLinkBytes
	// MaxContactNotesBytes caps personalNotes.
	MaxContactNotesBytes = 32 << 10

	maxContactIDBytes = 1024
	// maxContactPages bounds the pages of one delta pass.
	maxContactPages = 2000
	// contactsPrefer asks for immutable ids (a move keeps the id) and full
	// pages.
	contactsPrefer = `IdType="ImmutableId", odata.maxpagesize=200`
	// contactSelect is the subset the provider reads.
	contactSelect = "id,changeKey,parentFolderId,displayName,givenName,surname,middleName,title,generation,nickName," +
		"companyName,jobTitle,emailAddresses,businessPhones,homePhones,mobilePhone,businessAddress,homeAddress," +
		"otherAddress,businessHomePage,birthday,personalNotes"
	// birthdayTime is the time of day a written birthday carries: the
	// middle of the UTC day keeps the date in every zone ±12 h.
	birthdayTime = "T11:59:00Z"
)

var (
	// ErrContactGone: the contact (or folder) is no longer on the server.
	ErrContactGone = errors.New("graph: the contact is no longer on the server")
	// ErrContactConflict: the contact changed on the server since the
	// version the caller edited. errors.As with *ContactConflictError
	// gives the server's version.
	ErrContactConflict = errors.New("graph: the contact changed on the server")
	// ErrContactsAccessDenied: the token lacks the contacts scope (or the
	// consent was withdrawn); core asks for consent.
	ErrContactsAccessDenied = errors.New("graph: contacts access denied")
	// ErrContactsResync: the stored delta link is no longer accepted.
	// DeltaContacts never returns it (it starts over and reports Full);
	// it is exported for the engine's own checks.
	ErrContactsResync = errors.New("graph: contacts delta link expired, full resync needed")
)

// ContactConflictError is ErrContactConflict with the server's version.
type ContactConflictError struct {
	Server Contact
}

func (e *ContactConflictError) Error() string { return ErrContactConflict.Error() }

// Is makes errors.Is(err, ErrContactConflict) true.
func (e *ContactConflictError) Is(target error) bool { return target == ErrContactConflict }

// ContactFolder is one contact folder of the account.
type ContactFolder struct {
	// ID is DefaultContactFolderID for the default folder, else Graph's
	// immutable folder id.
	ID string
	// Name is cleaned; "Contacts" for the default folder (the UI names
	// it from Default).
	Name string
	// ParentID is the folder's parent among these ("" for the default
	// folder and its direct children, whose parent is the default).
	ParentID string
	Default  bool
}

// ContactEmail is one of a contact's e-mail addresses.
type ContactEmail struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address"`
}

// ContactAddress is a postal address.
type ContactAddress struct {
	Street          string `json:"street"`
	City            string `json:"city"`
	State           string `json:"state"`
	PostalCode      string `json:"postalCode"`
	CountryOrRegion string `json:"countryOrRegion"`
}

// IsZero reports an empty address.
func (a ContactAddress) IsZero() bool { return a == ContactAddress{} }

// ContactFields are the writable fields of a contact the address book
// knows (Graph's names; Title is the name prefix, Generation the suffix).
// Birthday is "YYYY-MM-DD" ("" none).
type ContactFields struct {
	DisplayName      string
	GivenName        string
	Surname          string
	MiddleName       string
	Title            string
	Generation       string
	NickName         string
	CompanyName      string
	JobTitle         string
	Emails           []ContactEmail
	BusinessPhones   []string
	HomePhones       []string
	MobilePhone      string
	BusinessAddress  ContactAddress
	HomeAddress      ContactAddress
	OtherAddress     ContactAddress
	BusinessHomePage string
	Birthday         string
	PersonalNotes    string
}

// Contact is one contact as the service sent it, cleaned.
type Contact struct {
	ID             string
	ChangeKey      string
	ParentFolderID string
	ContactFields
	// Raw is the contact's JSON as the service sent it (nil when over
	// MaxContactRawBytes): cache data, never shown or trusted.
	Raw json.RawMessage
}

// ContactPage is one page of a delta pass.
type ContactPage struct {
	Changed []Contact
	Removed []string
	// Full: the pass lists the whole folder (no delta link, or one the
	// service no longer accepted): contacts it does not name are gone.
	Full bool
}

// ContactDelta is the result of a delta pass.
type ContactDelta struct {
	// DeltaLink resumes the next pass.
	DeltaLink string
	// Full: the pass listed the whole folder (see ContactPage.Full).
	Full bool
}

// graphContact is the subset of Graph's contact resource read.
type graphContact struct {
	ID               string          `json:"id"`
	ChangeKey        string          `json:"changeKey"`
	ParentFolderID   string          `json:"parentFolderId"`
	DisplayName      string          `json:"displayName"`
	GivenName        string          `json:"givenName"`
	Surname          string          `json:"surname"`
	MiddleName       string          `json:"middleName"`
	Title            string          `json:"title"`
	Generation       string          `json:"generation"`
	NickName         string          `json:"nickName"`
	CompanyName      string          `json:"companyName"`
	JobTitle         string          `json:"jobTitle"`
	EmailAddresses   []emailAddress  `json:"emailAddresses"`
	BusinessPhones   []string        `json:"businessPhones"`
	HomePhones       []string        `json:"homePhones"`
	MobilePhone      string          `json:"mobilePhone"`
	BusinessAddress  *ContactAddress `json:"businessAddress"`
	HomeAddress      *ContactAddress `json:"homeAddress"`
	OtherAddress     *ContactAddress `json:"otherAddress"`
	BusinessHomePage string          `json:"businessHomePage"`
	Birthday         string          `json:"birthday"`
	PersonalNotes    string          `json:"personalNotes"`
	Removed          *removed        `json:"@removed"`
}

type graphContactFolder struct {
	ID             string `json:"id"`
	DisplayName    string `json:"displayName"`
	ParentFolderID string `json:"parentFolderId"`
}

// ---------------------------------------------------------------------------
// Reading
// ---------------------------------------------------------------------------

// validID: an id or change key the daemon may store and put in a URL.
func validID(s string) bool {
	if s == "" || len(s) > maxContactIDBytes {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

func contactLine(s string) string {
	return calendar.CleanLine(capString(s, 4*MaxContactLineBytes), MaxContactLineBytes)
}

func addressOf(a *ContactAddress) ContactAddress {
	if a == nil {
		return ContactAddress{}
	}
	return ContactAddress{
		Street:          calendar.CleanBlock(capString(a.Street, 4*MaxContactAddressLineBytes), MaxContactAddressLineBytes),
		City:            contactLine(a.City),
		State:           contactLine(a.State),
		PostalCode:      contactLine(a.PostalCode),
		CountryOrRegion: contactLine(a.CountryOrRegion),
	}
}

func phonesOf(in []string) []string {
	var out []string
	for i, p := range in {
		if i >= 4*MaxContactPhones || len(out) >= MaxContactPhones {
			break
		}
		if p = contactLine(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// birthdayOf reads Graph's birthday (a DateTimeOffset): its date part as
// written, never shifted to a zone ("" when it is not a date).
func birthdayOf(s string) string {
	s = strings.TrimSpace(s)
	if len(s) < 10 || len(s) > 40 {
		return ""
	}
	d, err := time.Parse(time.DateOnly, s[:10])
	if err != nil || d.Year() < 1 || d.Year() > 9999 {
		return ""
	}
	return d.Format(time.DateOnly)
}

// contactOf turns one item into a Contact; false when its id is unusable.
func contactOf(raw json.RawMessage) (Contact, bool, error) {
	var gc graphContact
	if err := json.Unmarshal(raw, &gc); err != nil {
		return Contact{}, false, api.NewError(api.CodeServerError, "graph: malformed contact: %s", transport.CleanMessage(err.Error()))
	}
	if !validID(gc.ID) {
		return Contact{}, false, nil
	}
	c := Contact{ID: gc.ID}
	if validID(gc.ChangeKey) {
		c.ChangeKey = gc.ChangeKey
	}
	if validID(gc.ParentFolderID) {
		c.ParentFolderID = gc.ParentFolderID
	}
	c.DisplayName = contactLine(gc.DisplayName)
	c.GivenName = contactLine(gc.GivenName)
	c.Surname = contactLine(gc.Surname)
	c.MiddleName = contactLine(gc.MiddleName)
	c.Title = contactLine(gc.Title)
	c.Generation = contactLine(gc.Generation)
	c.NickName = contactLine(gc.NickName)
	c.CompanyName = contactLine(gc.CompanyName)
	c.JobTitle = contactLine(gc.JobTitle)
	for i, e := range gc.EmailAddresses {
		if i >= 4*MaxContactPhones || len(c.Emails) >= MaxContactPhones {
			break
		}
		addr := contactLine(e.Address)
		if addr == "" {
			continue
		}
		c.Emails = append(c.Emails, ContactEmail{Name: contactLine(e.Name), Address: addr})
	}
	c.BusinessPhones = phonesOf(gc.BusinessPhones)
	c.HomePhones = phonesOf(gc.HomePhones)
	c.MobilePhone = contactLine(gc.MobilePhone)
	c.BusinessAddress = addressOf(gc.BusinessAddress)
	c.HomeAddress = addressOf(gc.HomeAddress)
	c.OtherAddress = addressOf(gc.OtherAddress)
	c.BusinessHomePage = calendar.CleanLine(capString(gc.BusinessHomePage, 4*MaxContactURLBytes), MaxContactURLBytes)
	c.Birthday = birthdayOf(gc.Birthday)
	c.PersonalNotes = calendar.CleanBlock(capString(gc.PersonalNotes, 4*MaxContactNotesBytes), MaxContactNotesBytes)
	if len(raw) <= MaxContactRawBytes {
		c.Raw = append(json.RawMessage(nil), raw...)
	}
	return c, true, nil
}

func contactFolderPath(id string) string { return "me/contactFolders/" + url.PathEscape(id) }

// contactsCollection is the contacts of a folder.
func contactsCollection(folderID string) string {
	if folderID == DefaultContactFolderID {
		return "me/contacts"
	}
	return contactFolderPath(folderID) + "/contacts"
}

func contactPath(id string) string { return "me/contacts/" + url.PathEscape(id) }

func (c *Client) contactsDo(ctx context.Context, r calReq) error {
	r.prefer, r.what = contactsPrefer, "contacts"
	return c.calDo(ctx, r)
}

// ContactFolders lists the account's contact folders: the default folder
// first (DefaultContactFolderID), then /me/contactFolders (the default
// folder's children) each followed by its own child folders (one level).
// At most MaxContactFolders.
func (c *Client) ContactFolders(ctx context.Context) ([]ContactFolder, error) {
	out := []ContactFolder{{ID: DefaultContactFolderID, Name: "Contacts", Default: true}}
	seen := map[string]bool{DefaultContactFolderID: true}
	top, err := c.contactFolderList(ctx, "me/contactFolders?$top=100", "", seen, MaxContactFolders-len(out))
	if err != nil {
		return nil, err
	}
	for _, f := range top {
		out = append(out, f)
		if len(out) >= MaxContactFolders {
			break
		}
		kids, err := c.contactFolderList(ctx, contactFolderPath(f.ID)+"/childFolders?$top=100", f.ID, seen, MaxContactFolders-len(out))
		if err != nil {
			if errors.Is(err, ErrContactGone) {
				continue // removed between the two calls
			}
			return nil, err
		}
		out = append(out, kids...)
	}
	return out, nil
}

func (c *Client) contactFolderList(ctx context.Context, first, parent string, seen map[string]bool, room int) ([]ContactFolder, error) {
	var out []ContactFolder
	next := first
	for pages := 0; next != "" && pages < 50 && len(out) < room; pages++ {
		if !c.sameOrigin(next) {
			return nil, api.NewError(api.CodeServerError, "graph: contacts link to another host")
		}
		var pg page[graphContactFolder]
		if err := c.contactsDo(ctx, calReq{method: http.MethodGet, url: next, out: &pg, prio: PriorityLow}); err != nil {
			return nil, contactsError(err)
		}
		for _, gf := range pg.Value {
			if len(out) >= room || !validID(gf.ID) || seen[gf.ID] {
				continue
			}
			seen[gf.ID] = true
			out = append(out, ContactFolder{ID: gf.ID, Name: contactLine(gf.DisplayName), ParentID: parent})
		}
		next = pg.NextLink
	}
	return out, nil
}

// DeltaContacts runs one delta pass over a folder's contacts, resumed
// from deltaLink ("" = a full listing). Each page goes to fn as it
// arrives: changed contacts (cleaned) and removed ids ("@removed"). A
// delta link the service no longer accepts (410 Gone, syncStateNotFound
// and the like) starts over from scratch once; the result and the pages
// of that pass then say Full. Pages are walked with immutable ids and
// odata.maxpagesize; a link to another host is refused.
func (c *Client) DeltaContacts(ctx context.Context, folderID, deltaLink string, fn func(ContactPage) error) (ContactDelta, error) {
	if folderID != DefaultContactFolderID && !validID(folderID) {
		return ContactDelta{}, api.NewError(api.CodeInvalidArgument, "graph: bad contact folder id")
	}
	d, err := c.deltaContacts(ctx, folderID, deltaLink, fn)
	if err != nil && deltaLink != "" && errors.Is(err, ErrContactsResync) {
		d, err = c.deltaContacts(ctx, folderID, "", fn)
	}
	return d, err
}

func (c *Client) deltaContacts(ctx context.Context, folderID, deltaLink string, fn func(ContactPage) error) (ContactDelta, error) {
	full := deltaLink == ""
	next := deltaLink
	if full {
		next = contactsCollection(folderID) + "/delta?$select=" + contactSelect
	}
	for n := 0; ; n++ {
		if n >= maxContactPages {
			return ContactDelta{}, api.NewError(api.CodeServerError, "graph: contacts delta over %d pages", maxContactPages)
		}
		if !c.sameOrigin(next) {
			return ContactDelta{}, api.NewError(api.CodeServerError, "graph: contacts link to another host")
		}
		var pg page[json.RawMessage]
		if err := c.contactsDo(ctx, calReq{method: http.MethodGet, url: next, out: &pg, prio: PriorityLow}); err != nil {
			if isSyncStateError(err) {
				return ContactDelta{}, fmt.Errorf("%w: %s", ErrContactsResync, transport.CleanMessage(err.Error()))
			}
			return ContactDelta{}, contactsError(err)
		}
		cp := ContactPage{Full: full}
		for _, raw := range pg.Value {
			var tomb struct {
				ID      string   `json:"id"`
				Removed *removed `json:"@removed"`
			}
			if err := json.Unmarshal(raw, &tomb); err != nil {
				return ContactDelta{}, api.NewError(api.CodeServerError, "graph: malformed contact: %s", transport.CleanMessage(err.Error()))
			}
			if tomb.Removed != nil {
				if validID(tomb.ID) {
					cp.Removed = append(cp.Removed, tomb.ID)
				}
				continue
			}
			ct, ok, err := contactOf(raw)
			if err != nil {
				return ContactDelta{}, err
			}
			if ok {
				cp.Changed = append(cp.Changed, ct)
			}
		}
		if err := fn(cp); err != nil {
			return ContactDelta{}, err
		}
		if pg.DeltaLink != "" {
			if !c.sameOrigin(pg.DeltaLink) {
				return ContactDelta{}, api.NewError(api.CodeServerError, "graph: contacts link to another host")
			}
			return ContactDelta{DeltaLink: pg.DeltaLink, Full: full}, nil
		}
		if pg.NextLink == "" {
			return ContactDelta{}, api.NewError(api.CodeServerError, "graph: contacts delta ended without a delta link")
		}
		next = pg.NextLink
	}
}

// GetContact fetches one contact; 404 is ErrContactGone.
func (c *Client) GetContact(ctx context.Context, id string) (Contact, error) {
	if !validID(id) {
		return Contact{}, api.NewError(api.CodeInvalidArgument, "graph: bad contact id")
	}
	var raw json.RawMessage
	if err := c.contactsDo(ctx, calReq{method: http.MethodGet, url: contactPath(id) + "?$select=" + contactSelect, out: &raw, prio: PriorityHigh}); err != nil {
		return Contact{}, contactsError(err)
	}
	ct, ok, err := contactOf(raw)
	if err != nil {
		return Contact{}, err
	}
	if !ok {
		return Contact{}, api.NewError(api.CodeServerError, "graph: contact without a usable id")
	}
	return ct, nil
}

// ---------------------------------------------------------------------------
// Writing
// ---------------------------------------------------------------------------

// contactKeys are the JSON keys a patch may carry.
var contactKeys = map[string]bool{
	"displayName": true, "givenName": true, "surname": true, "middleName": true, "title": true, "generation": true,
	"nickName": true, "companyName": true, "jobTitle": true, "emailAddresses": true, "businessPhones": true,
	"homePhones": true, "mobilePhone": true, "businessAddress": true, "homeAddress": true, "otherAddress": true,
	"businessHomePage": true, "birthday": true, "personalNotes": true,
}

// wireFields is every field of f under its Graph key, as a write sends
// it: lists never null, an empty birthday null.
func wireFields(f ContactFields) map[string]any {
	emails := make([]ContactEmail, 0, len(f.Emails))
	emails = append(emails, f.Emails...)
	var bday any
	if f.Birthday != "" {
		bday = f.Birthday + birthdayTime
	}
	return map[string]any{
		"displayName": f.DisplayName, "givenName": f.GivenName, "surname": f.Surname, "middleName": f.MiddleName,
		"title": f.Title, "generation": f.Generation, "nickName": f.NickName, "companyName": f.CompanyName,
		"jobTitle": f.JobTitle, "emailAddresses": emails, "businessPhones": nonNil(f.BusinessPhones),
		"homePhones": nonNil(f.HomePhones), "mobilePhone": f.MobilePhone, "businessAddress": f.BusinessAddress,
		"homeAddress": f.HomeAddress, "otherAddress": f.OtherAddress, "businessHomePage": f.BusinessHomePage,
		"birthday": bday, "personalNotes": f.PersonalNotes,
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ContactPatch is the patch that turns before into after: only the keys
// whose values differ (a list or an address is sent whole). Both are
// checked as a write checks them.
func ContactPatch(before, after ContactFields) (map[string]any, error) {
	if err := checkFields(&before); err != nil {
		return nil, err
	}
	if err := checkFields(&after); err != nil {
		return nil, err
	}
	b, a := wireFields(before), wireFields(after)
	out := map[string]any{}
	for k, v := range a {
		if !reflect.DeepEqual(b[k], v) {
			out[k] = v
		}
	}
	return out, nil
}

func checkLine(name, s string, max int) error {
	if !utf8.ValidString(s) {
		return api.NewError(api.CodeInvalidArgument, "graph: contact %s is not UTF-8", name)
	}
	if len(s) > max {
		return api.NewError(api.CodeInvalidArgument, "graph: contact %s over %d bytes", name, max)
	}
	return nil
}

// checkFields validates and cleans what a write sends: UTF-8 within the
// caps (over is invalidArgument, never cut), control and invisible
// characters removed as on reading, at most MaxContactEmails addresses,
// a birthday that is a date.
func checkFields(f *ContactFields) error {
	lines := []struct {
		name string
		p    *string
	}{
		{"displayName", &f.DisplayName}, {"givenName", &f.GivenName}, {"surname", &f.Surname},
		{"middleName", &f.MiddleName}, {"title", &f.Title}, {"generation", &f.Generation},
		{"nickName", &f.NickName}, {"companyName", &f.CompanyName}, {"jobTitle", &f.JobTitle},
		{"mobilePhone", &f.MobilePhone},
	}
	for _, l := range lines {
		if err := checkLine(l.name, *l.p, MaxContactLineBytes); err != nil {
			return err
		}
		*l.p = calendar.CleanLine(*l.p, MaxContactLineBytes)
	}
	if err := checkLine("businessHomePage", f.BusinessHomePage, MaxContactURLBytes); err != nil {
		return err
	}
	f.BusinessHomePage = calendar.CleanLine(f.BusinessHomePage, MaxContactURLBytes)
	if err := checkLine("personalNotes", f.PersonalNotes, MaxContactNotesBytes); err != nil {
		return err
	}
	f.PersonalNotes = calendar.CleanBlock(f.PersonalNotes, MaxContactNotesBytes)
	if len(f.Emails) > MaxContactEmails {
		return api.NewError(api.CodeInvalidArgument, "graph: a contact has at most %d e-mail addresses", MaxContactEmails)
	}
	var emails []ContactEmail
	for _, e := range f.Emails {
		if err := checkLine("e-mail name", e.Name, MaxContactLineBytes); err != nil {
			return err
		}
		if err := checkLine("e-mail address", e.Address, MaxContactLineBytes); err != nil {
			return err
		}
		e.Name, e.Address = calendar.CleanLine(e.Name, MaxContactLineBytes), calendar.CleanLine(e.Address, MaxContactLineBytes)
		if e.Address != "" {
			emails = append(emails, e)
		}
	}
	f.Emails = emails
	for _, ph := range []*[]string{&f.BusinessPhones, &f.HomePhones} {
		if len(*ph) > MaxContactPhones {
			return api.NewError(api.CodeInvalidArgument, "graph: a contact phone list has at most %d numbers", MaxContactPhones)
		}
		var out []string
		for _, p := range *ph {
			if err := checkLine("phone", p, MaxContactLineBytes); err != nil {
				return err
			}
			if p = calendar.CleanLine(p, MaxContactLineBytes); p != "" {
				out = append(out, p)
			}
		}
		*ph = out
	}
	for _, a := range []*ContactAddress{&f.BusinessAddress, &f.HomeAddress, &f.OtherAddress} {
		if err := checkLine("street", a.Street, MaxContactAddressLineBytes); err != nil {
			return err
		}
		a.Street = calendar.CleanBlock(a.Street, MaxContactAddressLineBytes)
		for _, p := range []*string{&a.City, &a.State, &a.PostalCode, &a.CountryOrRegion} {
			if err := checkLine("address", *p, MaxContactLineBytes); err != nil {
				return err
			}
			*p = calendar.CleanLine(*p, MaxContactLineBytes)
		}
	}
	if f.Birthday != "" && birthdayOf(f.Birthday) != f.Birthday {
		return api.NewError(api.CodeInvalidArgument, "graph: contact birthday is not a date")
	}
	return nil
}

// CreateContact creates a contact in a folder (DefaultContactFolderID:
// /me/contacts) and returns it as the service stored it.
func (c *Client) CreateContact(ctx context.Context, folderID string, f ContactFields) (Contact, error) {
	if folderID != DefaultContactFolderID && !validID(folderID) {
		return Contact{}, api.NewError(api.CodeInvalidArgument, "graph: bad contact folder id")
	}
	if err := checkFields(&f); err != nil {
		return Contact{}, err
	}
	body := wireFields(f)
	for k, v := range body {
		// A new contact sends only what it has.
		if s, ok := v.(string); ok && s == "" {
			delete(body, k)
		}
		if v == nil {
			delete(body, k)
		}
		switch x := v.(type) {
		case ContactAddress:
			if x.IsZero() {
				delete(body, k)
			}
		case []string:
			if len(x) == 0 {
				delete(body, k)
			}
		case []ContactEmail:
			if len(x) == 0 {
				delete(body, k)
			}
		}
	}
	var raw json.RawMessage
	if err := c.contactsDo(ctx, calReq{method: http.MethodPost, url: contactsCollection(folderID), body: body, out: &raw, prio: PriorityHigh}); err != nil {
		return Contact{}, contactsError(err)
	}
	ct, ok, err := contactOf(raw)
	if err != nil {
		return Contact{}, err
	}
	if !ok {
		return Contact{}, api.NewError(api.CodeServerError, "graph: created contact without a usable id")
	}
	return ct, nil
}

// checkVersion fetches the contact and compares its changeKey with the
// one the caller edited (Graph does not promise to honour If-Match on
// contacts, so the comparison is the guard; If-Match is sent as well).
func (c *Client) checkVersion(ctx context.Context, id, changeKey string) error {
	if !validID(id) {
		return api.NewError(api.CodeInvalidArgument, "graph: bad contact id")
	}
	if !validID(changeKey) {
		return api.NewError(api.CodeInvalidArgument, "graph: bad contact change key")
	}
	cur, err := c.GetContact(ctx, id)
	if err != nil {
		return err
	}
	if cur.ChangeKey != changeKey {
		return &ContactConflictError{Server: cur}
	}
	return nil
}

// conflictOf is the conflict after a refused precondition, with the
// server's version (gone when it no longer exists).
func (c *Client) conflictOf(ctx context.Context, id string) error {
	cur, err := c.GetContact(ctx, id)
	if err != nil {
		return err
	}
	return &ContactConflictError{Server: cur}
}

// UpdateContact applies patch (Graph keys, as ContactPatch makes it; only
// the changed fields) to the contact at version changeKey. The contact is
// fetched first: another changeKey is a *ContactConflictError with the
// server's version; the PATCH carries If-Match W/"<changeKey>" and a 412
// is the same conflict. 404 is ErrContactGone. An empty patch changes
// nothing and returns the current contact.
func (c *Client) UpdateContact(ctx context.Context, id, changeKey string, patch map[string]any) (Contact, error) {
	for k := range patch {
		if !contactKeys[k] {
			return Contact{}, api.NewError(api.CodeInvalidArgument, "graph: unknown contact field %q", transport.CleanMessage(k))
		}
	}
	if err := c.checkVersion(ctx, id, changeKey); err != nil {
		return Contact{}, err
	}
	if len(patch) == 0 {
		return c.GetContact(ctx, id)
	}
	var raw json.RawMessage
	err := c.contactsDo(ctx, calReq{method: http.MethodPatch, url: contactPath(id), body: patch, ifMatch: `W/"` + changeKey + `"`, out: &raw, prio: PriorityHigh})
	if err != nil {
		if isPrecondition(err) {
			return Contact{}, c.conflictOf(ctx, id)
		}
		return Contact{}, contactsError(err)
	}
	ct, ok, err := contactOf(raw)
	if err != nil {
		return Contact{}, err
	}
	if !ok {
		return c.GetContact(ctx, id)
	}
	return ct, nil
}

// DeleteContact deletes the contact at version changeKey, guarded as
// UpdateContact is.
func (c *Client) DeleteContact(ctx context.Context, id, changeKey string) error {
	if err := c.checkVersion(ctx, id, changeKey); err != nil {
		return err
	}
	err := c.contactsDo(ctx, calReq{method: http.MethodDelete, url: contactPath(id), ifMatch: `W/"` + changeKey + `"`, prio: PriorityHigh})
	if err != nil {
		if isPrecondition(err) {
			return c.conflictOf(ctx, id)
		}
		return contactsError(err)
	}
	return nil
}

// contactsError maps a failure: cancelled, access denied
// (ErrContactsAccessDenied), a missing item (ErrContactGone), a refused
// request (invalidArgument), else ToAPIError.
func contactsError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return api.NewError(api.CodeCancelled, "cancelled")
	}
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae
	}
	msg := transport.CleanMessage(err.Error())
	switch {
	case isAccessDenied(err):
		return fmt.Errorf("%w: %s", ErrContactsAccessDenied, msg)
	case isItemGone(err):
		return fmt.Errorf("%w: %s", ErrContactGone, msg)
	}
	if se, ok := statusOf(err); ok && se.Status == http.StatusBadRequest {
		return api.NewError(api.CodeInvalidArgument, "graph: %s", msg)
	}
	return ToAPIError(err)
}
