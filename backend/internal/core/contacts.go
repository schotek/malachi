// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/internal/contacts"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Recipient completion (docs/api.md §4.11): addresses the user wrote to,
// merged with the system address books of the sending account.

// metaContactsBackfilled marks the one-off seeding of collected_addresses
// from the Sent folders that were in the store before migration 0009.
const metaContactsBackfilled = "contacts.backfilled"

const (
	// collectedFetch and bookFetch are how many candidates each source
	// contributes to the merge; the limit applies after ranking.
	collectedFetch = 50
	bookFetch      = 100
	// recentUse is how long a collected address counts as recently used.
	recentUse = 30 * 24 * time.Hour
	// maxUsesBoost caps how much sheer frequency can lift an address.
	maxUsesBoost = 20
	// bothSourcesBoost lifts an address the user both wrote to and keeps
	// in an address book above one that is only in either.
	bothSourcesBoost = 5
	// backfillPage is the message.list page size the backfill walks with.
	backfillPage = 500
)

type contactService struct{ b *Backend }

func (s *contactService) Search(ctx context.Context, p api.ContactSearchParams) (*api.ContactSearchResult, error) {
	q := strings.TrimSpace(p.Query)
	switch {
	case q == "":
		return nil, api.NewError(api.CodeInvalidArgument, "query is required")
	case len(q) > api.MaxContactQueryBytes:
		return nil, api.NewError(api.CodeInvalidArgument, "query longer than %d bytes", api.MaxContactQueryBytes)
	case !utf8.ValidString(q) || hasControl(q):
		return nil, api.NewError(api.CodeInvalidArgument, "query contains control or invalid characters")
	}
	limit := p.Limit
	if limit <= 0 {
		limit = api.DefaultContactLimit
	}
	if limit > api.MaxContactLimit {
		limit = api.MaxContactLimit
	}
	a, err := s.b.requireAccount(ctx, string(p.AccountID))
	if err != nil {
		return nil, err
	}
	collected, err := s.b.store.SearchCollectedAddresses(ctx, q, collectedFetch)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	found := s.b.searchAddressBooks(ctx, a, q, bookFetch)
	return &api.ContactSearchResult{Contacts: mergeContacts(q, collected, found, time.Now(), limit)}, nil
}

// searchAddressBooks queries the address books that belong to the account.
// Without a directory, without a session bus, or when the account has no
// books, the answer is empty and the reason goes to the log: completion
// then rests on the collected addresses alone, never on an error.
func (b *Backend) searchAddressBooks(ctx context.Context, a store.Account, q string, limit int) []contacts.Contact {
	if b.Directory == nil {
		return nil
	}
	books, err := b.Directory.Books(ctx)
	if err != nil {
		b.log.Debug("address books unavailable", "err", err)
		return nil
	}
	mine := accountBooks(a, books)
	if len(mine) == 0 {
		return nil
	}
	found, err := b.Directory.Search(ctx, mine, q, limit)
	if err != nil {
		b.log.Debug("address book search", "err", err)
		return nil
	}
	return found
}

// accountBooks picks the books of the account's own collection: matched on
// the GNOME Online Accounts id of a Graph account, else on the collection's
// e-mail identity (an IMAP account that is also set up in Online
// Accounts). Stand-alone books and other accounts' collections are left
// out; that is the user's decision, not a limitation.
func accountBooks(a store.Account, books []contacts.Book) []contacts.Book {
	goaID := ""
	if a.Config.Graph != nil {
		goaID = a.Config.Graph.GOAAccountID
	}
	email := store.NormalizeAddress(a.Config.Email)
	var out []contacts.Book
	for _, bk := range books {
		switch {
		case goaID != "" && bk.GOAAccountID == goaID:
		case email != "" && bk.Email == email:
		default:
			continue
		}
		out = append(out, bk)
	}
	return out
}

// candidate is one address on its way through mergeContacts.
type candidate struct {
	api.Contact
	score int
}

// mergeContacts ranks and de-duplicates the two sources. The score says
// how the query matches (whole address, address prefix, a word of the
// name, anywhere in the name, anywhere in the address), and a collected
// address is lifted by how often and how recently the user wrote to it.
// The same address from both sources is one row, lifted a little, reported
// as the address book's with the book's name unless the card has none.
// Names are cleaned like any other untrusted display text — before
// scoring, so that a control character cannot make a word.
func mergeContacts(q string, collected []store.CollectedAddress, found []contacts.Contact, now time.Time, limit int) []api.Contact {
	q = strings.ToLower(strings.TrimSpace(q))
	byAddr := map[string]*candidate{}
	var order []string
	add := func(c api.Contact, score int) {
		key := store.NormalizeAddress(c.Address)
		if key == "" {
			return
		}
		c.Address = key
		cur, ok := byAddr[key]
		if !ok {
			byAddr[key] = &candidate{Contact: c, score: score}
			order = append(order, key)
			return
		}
		if cur.Source != c.Source {
			cur.score += bothSourcesBoost
		}
		cur.score = max(cur.score, score)
		if c.Source == api.ContactSourceAddressBook {
			cur.Source, cur.Book = c.Source, c.Book
			if c.Name != "" {
				cur.Name = c.Name
			}
		} else if cur.Name == "" {
			cur.Name = c.Name
		}
	}
	for _, c := range collected {
		name := cleanDisplayName(c.Name)
		add(api.Contact{Name: name, Address: c.Address, Source: api.ContactSourceSent},
			matchScore(q, name, c.Address)+usageBoost(c, now))
	}
	for _, f := range found {
		name := cleanDisplayName(f.Name)
		add(api.Contact{Name: name, Address: f.Address, Source: api.ContactSourceAddressBook, Book: cleanDisplayName(f.Book)},
			matchScore(q, name, f.Address))
	}
	all := make([]*candidate, 0, len(order))
	for _, k := range order {
		all = append(all, byAddr[k])
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		return contactLabel(all[i].Contact) < contactLabel(all[j].Contact)
	})
	if len(all) > limit {
		all = all[:limit]
	}
	out := make([]api.Contact, 0, len(all))
	for _, c := range all {
		out = append(out, c.Contact)
	}
	return out
}

// matchScore grades how q (already lower-cased) matches a card. The floor
// is not zero: an address book may have matched server-side on a field
// this function does not see (a nickname, a company), and that hit still
// belongs in the list, just below the ones we can explain.
func matchScore(q, name, address string) int {
	name, address = strings.ToLower(name), strings.ToLower(address)
	switch {
	case address == q:
		return 100
	case strings.HasPrefix(address, q):
		return 80
	case wordHasPrefix(name, q):
		return 70
	case strings.Contains(name, q):
		return 50
	case strings.Contains(address, q):
		return 40
	}
	return 30
}

// wordHasPrefix reports whether any word of s starts with q. Words are
// split on space and the punctuation that separates name parts.
func wordHasPrefix(s, q string) bool {
	if q == "" {
		return false
	}
	for _, w := range strings.FieldsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || r == ',' || r == '.' || r == '-' || r == '(' || r == '"'
	}) {
		if strings.HasPrefix(w, q) {
			return true
		}
	}
	return false
}

// usageBoost lifts a collected address by how often (capped) and how
// recently the user wrote to it.
func usageBoost(c store.CollectedAddress, now time.Time) int {
	boost := min(c.Uses, maxUsesBoost)
	if !c.LastUsed.IsZero() && now.Sub(c.LastUsed) < recentUse {
		boost += 10
	}
	return boost
}

// contactLabel is the tie-break order: by name when there is one, else by
// address.
func contactLabel(c api.Contact) string {
	if c.Name != "" {
		return strings.ToLower(c.Name)
	}
	return c.Address
}

// backfillCollectedAddresses seeds collected_addresses once from the Sent
// folders already in the store, so completion has something to offer
// before the first message is sent through this daemon. Runs from
// Maintain; the meta key is set only after a complete pass, so a failure
// retries at the next start. Only messages within the offlineDays window
// exist locally, which bounds the work.
func (b *Backend) backfillCollectedAddresses(ctx context.Context) error {
	if _, done, err := b.store.GetMeta(ctx, metaContactsBackfilled); err != nil || done {
		return err
	}
	accounts, err := b.store.ListAccounts(ctx)
	if err != nil {
		return err
	}
	var messages int
	for _, a := range accounts {
		sent, err := b.store.FolderByRole(ctx, a.ID, api.RoleSent)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		cursor := ""
		for {
			items, next, _, err := b.store.ListMessages(ctx, a.ID, sent.ID, cursor, backfillPage, api.SortDateDesc, api.FilterAll)
			if err != nil {
				return err
			}
			for _, m := range items {
				addrs := make([]api.Address, 0, len(m.To)+len(m.CC)+len(m.BCC))
				addrs = append(append(append(addrs, m.To...), m.CC...), m.BCC...)
				if err := b.store.TouchCollectedAddresses(ctx, addrs, m.Date); err != nil {
					return err
				}
				messages++
			}
			if next == "" {
				break
			}
			cursor = next
		}
	}
	if err := b.store.SetMeta(ctx, metaContactsBackfilled, "1"); err != nil {
		return err
	}
	b.log.Info("collected addresses backfilled from sent mail", "messages", messages)
	return nil
}
