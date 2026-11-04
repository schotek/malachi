// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package caldav

import (
	"context"
	"encoding/xml"
	"net/url"
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
)

// CardDAV (RFC 6352, docs/plans/address-book.md A8): the same client,
// hostile-input rules and origin policy as CalDAV. Synchronising an
// address book uses the generic State, SyncCollection and ListETags;
// Delete works for a card as for an event.

const (
	// MaxAddressBooks bounds the address books ListAddressBooks returns.
	MaxAddressBooks = 100
	// CardMultigetBatch is how many hrefs one addressbook-multiget asks for.
	CardMultigetBatch = 100
	// CardContentType is the Content-Type of a PUT of a vCard.
	CardContentType = "text/vcard; charset=utf-8"
)

// HomeSets is what the principal of the server URL names.
type HomeSets struct {
	PrincipalURL string // absolute, on a trusted origin
	DisplayName  string
	// CalendarHomeSet and AddressBookHomeSet are absolute URLs, "" when
	// the principal names none. Either may be on an origin the client
	// does not trust (iCloud's contacts host): Trusts tells, and the
	// caller decides whether to pass it in Options.Origins.
	CalendarHomeSet    string
	AddressBookHomeSet string
}

// HomeSets signs in and reads current-user-principal of the server URL,
// then the principal's displayname, calendar-home-set and
// addressbook-home-set. A server that names no principal but home sets
// on the server URL itself is taken as its own principal. A principal on
// an origin the client does not trust is ErrOtherOrigin; neither home
// set is serverError.
func (c *Client) HomeSets(ctx context.Context) (*HomeSets, error) {
	root := c.rootURL()
	ms, err := c.propfind(ctx, root, 0, []xml.Name{PropCurrentUserPrincipal, PropCalendarHomeSet, PropAddressbookHomeSet})
	if err != nil {
		return nil, err
	}
	var principal string
	var cal, book []string
	if r := findResponse(ms, root.EscapedPath()); r != nil {
		principal = r.Props.CurrentUserPrincipal
		cal, book = r.Props.CalendarHomeSet, r.Props.AddressbookHomeSet
	}
	if principal == "" {
		if len(cal) == 0 && len(book) == 0 {
			return nil, api.NewError(api.CodeServerError, "caldav: the server names no current-user-principal")
		}
		principal = root.String()
	}
	pu, err := c.trustedURL(principal)
	if err != nil {
		return nil, err
	}
	hs := &HomeSets{PrincipalURL: pu.String()}
	ms, err = c.propfind(ctx, pu, 0, []xml.Name{PropDisplayName, PropCalendarHomeSet, PropAddressbookHomeSet})
	if err != nil {
		return nil, err
	}
	if r := findResponse(ms, pu.EscapedPath()); r != nil {
		hs.DisplayName = r.Props.DisplayName
		if len(r.Props.CalendarHomeSet) > 0 {
			cal = r.Props.CalendarHomeSet
		}
		if len(r.Props.AddressbookHomeSet) > 0 {
			book = r.Props.AddressbookHomeSet
		}
	}
	if len(cal) == 0 && len(book) == 0 {
		return nil, api.NewError(api.CodeServerError, "caldav: the principal names no home set")
	}
	if len(cal) > 0 {
		hs.CalendarHomeSet = collectionURL(cal[0])
	}
	if len(book) > 0 {
		hs.AddressBookHomeSet = collectionURL(book[0])
	}
	return hs, nil
}

// collectionURL is an absolute URL (resolved by readProps) with a
// trailing slash.
func collectionURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	return u.String()
}

// Trusts reports whether the client sends its credentials to raw (an
// absolute URL or a path): the server's origin or one of Options.Origins.
func (c *Client) Trusts(raw string) bool {
	u, ok := resolveURL(c.base, raw)
	return ok && c.trusted(u)
}

// AddressBook is a vCard collection of an address book home set.
type AddressBook struct {
	Href        string // path on the home set's origin
	Name        string // displayname, else the last segment of Href
	Description string
	CTag        string
	SyncToken   string
	// ReadOnly: current-user-privilege-set grants neither write,
	// write-content nor all (an absent set counts as writable).
	ReadOnly bool
	// SupportedVersions are the vCard versions of supported-address-data;
	// ["3.0"] when the server does not say (RFC 6352 §6.2.2).
	SupportedVersions []string
	SupportsSync      bool
}

// ListAddressBooks lists the address books of homeSet (an absolute URL
// or a path, on a trusted origin, else ErrOtherOrigin) with a depth-1
// PROPFIND: collections of resource type addressbook, at most
// MaxAddressBooks.
func (c *Client) ListAddressBooks(ctx context.Context, homeSet string) ([]AddressBook, error) {
	hu, err := c.trustedURL(homeSet)
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(hu.Path, "/") {
		hu.Path += "/"
	}
	ms, err := c.propfind(ctx, hu, 1, []xml.Name{
		PropResourceType, PropDisplayName, PropAddressbookDescription, PropCurrentUserPrivileges,
		PropGetCTag, PropSyncToken, PropSupportedReportSet, PropSupportedAddressData,
	})
	if err != nil {
		return nil, err
	}
	out := []AddressBook{}
	for _, r := range ms.Responses {
		if samePath(r.Href, hu.EscapedPath()) || !r.Props.Is(TypeAddressBook) || (r.Status != 0 && r.Status != 200) {
			continue
		}
		if len(out) >= MaxAddressBooks {
			c.log.Debug("caldav: address books over the limit left out")
			break
		}
		b := AddressBook{
			Href: r.Href, Name: r.Props.DisplayName, Description: r.Props.Description,
			CTag: r.Props.CTag, SyncToken: r.Props.SyncToken, ReadOnly: !r.Props.CanWrite(),
			SupportedVersions: r.Props.AddressDataVersions,
			SupportsSync:      r.Props.SupportsReport(ReportSyncCollection),
		}
		if !r.Props.AddressDataSet {
			b.SupportedVersions = []string{"3.0"}
		}
		if b.Name == "" {
			b.Name = hrefName(r.Href)
		}
		out = append(out, b)
	}
	return out, nil
}

var cardObjects = objectKind{
	name: "addressbook-multiget", batch: CardMultigetBatch, body: addressbookMultigetBody,
	data: func(p *Props) ([]byte, bool) { return p.AddressData, p.AddressDataTooLarge },
	get:  (*Client).GetCard,
}

// MultigetCards fetches hrefs of the address book collection with
// addressbook-multiget, in batches of CardMultigetBatch, as Multiget does
// for events: one Object per href in the order asked, Status 404 for one
// the server does not have, TooLarge for a card over MaxCardBytes, a GET
// for an answer without data, and a GET per card when the server refuses
// the report.
func (c *Client) MultigetCards(ctx context.Context, collection string, hrefs []string) ([]Object, error) {
	return c.multiget(ctx, collection, hrefs, cardObjects)
}

// GetCard fetches one vCard (≤ MaxCardBytes; more is ErrTooLarge).
func (c *Client) GetCard(ctx context.Context, href string) (Object, error) {
	return c.get(ctx, href, "text/vcard", MaxCardBytes)
}

// PutCard stores one vCard (≤ MaxCardBytes) at href as text/vcard, with
// Put's preconditions: etag "" creates (If-None-Match: *), otherwise it
// replaces that version (If-Match; 412 is IsConflict). It returns the new
// ETag (from a following GET when the answer has no strong one).
func (c *Client) PutCard(ctx context.Context, href string, data []byte, etag string) (string, error) {
	return c.put(ctx, href, data, etag, CardContentType, MaxCardBytes, (*Client).GetCard)
}

// CreateCard stores a new vCard in collection under a fresh href
// (CardHref with NewUID) and returns the href and ETag. An href that
// happens to exist (412) is tried once more with another UID.
func (c *Client) CreateCard(ctx context.Context, collection string, data []byte) (href, etag string, err error) {
	for attempt := 0; ; attempt++ {
		u, err := c.URL(CardHref(collection, NewUID()))
		if err != nil {
			return "", "", err
		}
		etag, err = c.PutCard(ctx, u.String(), data, "")
		if err == nil {
			return u.EscapedPath(), etag, nil
		}
		if !IsConflict(err) || attempt > 0 {
			return "", "", err
		}
	}
}

// CardHref is the href of a new card of collection: <collection>/<uid>.vcf,
// the UID path-escaped.
func CardHref(collection, uid string) string {
	if !strings.HasSuffix(collection, "/") {
		collection += "/"
	}
	return collection + url.PathEscape(uid) + ".vcf"
}
