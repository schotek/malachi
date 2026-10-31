// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"sync"

	"github.com/schotek/malachi/backend/internal/contactphoto"
	"github.com/schotek/malachi/backend/internal/contactsync"
	"github.com/schotek/malachi/backend/internal/graph"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/internal/vcard"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Contact photos, read only (docs/api.md §4.19 contact.photo,
// docs/plans/address-book.md §9). A CardDAV contact's photo is embedded
// in its card and stored by the engine (contactsync); a Microsoft 365
// contact's photo is downloaded here, when a client asks, once per
// revision of the contact (F3). Every picture goes through
// internal/contactphoto before it is stored; the server's bytes never
// leave the daemon. The log gets ids and error classes only.

// maxPhotoFetchesPerAccount bounds the concurrent downloads of an
// account's photos (F3); the account's Graph limiter applies as well.
const maxPhotoFetchesPerAccount = 4

// contactPhotoSource is a provider that can download a contact's photo
// (the Graph one). errNoPhoto: the contact has none.
type contactPhotoSource interface {
	ContactPhoto(ctx context.Context, book store.AddressBook, remoteID string) ([]byte, string, error)
}

var (
	errNoPhoto      = errors.New("the contact has no photo")
	errPhotoRefused = errors.New("the contact's photo was refused")
)

// contactPhotoFetches joins the concurrent requests for one contact's
// photo and bounds the downloads of each account.
type contactPhotoFetches struct {
	mu       sync.Mutex
	inflight map[string]*photoFetch   // contact id + revision
	slots    map[string]chan struct{} // account id
}

type photoFetch struct {
	done chan struct{}
	err  error
}

func (f *contactPhotoFetches) slot(accountID string) chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.slots == nil {
		f.slots = map[string]chan struct{}{}
	}
	ch := f.slots[accountID]
	if ch == nil {
		ch = make(chan struct{}, maxPhotoFetchesPerAccount)
		f.slots[accountID] = ch
	}
	return ch
}

// join runs fn once for key: a second caller while it runs waits for the
// first one's result.
func (f *contactPhotoFetches) join(ctx context.Context, key string, fn func() error) error {
	f.mu.Lock()
	if f.inflight == nil {
		f.inflight = map[string]*photoFetch{}
	}
	if pf, ok := f.inflight[key]; ok {
		f.mu.Unlock()
		select {
		case <-pf.done:
			return pf.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	pf := &photoFetch{done: make(chan struct{})}
	f.inflight[key] = pf
	f.mu.Unlock()
	pf.err = fn()
	f.mu.Lock()
	delete(f.inflight, key)
	f.mu.Unlock()
	close(pf.done)
	return pf.err
}

// photoResult is the API form of a stored photo row.
func photoResult(p store.ContactPhoto) *api.ContactPhotoResult {
	if p.State == store.PhotoOK {
		return &api.ContactPhotoResult{State: api.ContactPhotoStateReady, MIME: p.MIME, Data: p.Data, Revision: p.Revision}
	}
	return &api.ContactPhotoResult{State: api.ContactPhotoStateNone, Revision: p.Revision}
}

func (s *addressBookService) Photo(ctx context.Context, p api.ContactPhotoParams) (*api.ContactPhotoResult, error) {
	b := s.b
	if addr, ok := recentAddress(p.ContactID); ok {
		// The recent book never has photos.
		if _, err := b.store.GetCollectedAddress(ctx, addr); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, api.NewError(api.CodeContactNotFound, "unknown contact %q", p.ContactID)
			}
			return nil, api.NewError(api.CodeStorageError, "%v", err)
		}
		return &api.ContactPhotoResult{State: api.ContactPhotoStateNone}, nil
	}
	stored, bk, a, err := b.storedContact(ctx, p.ContactID)
	if err != nil {
		return nil, err
	}
	if row, ok, err := b.storedPhoto(ctx, stored); err != nil || ok {
		return row, err
	}
	key := stored.ID + "\x00" + stored.ETag
	err = b.photoFetch.join(ctx, key, func() error { return b.makePhoto(ctx, stored, bk, a) })
	if err != nil {
		return nil, err
	}
	row, ok, err := b.storedPhoto(ctx, stored)
	if err == nil && !ok {
		// Stored meanwhile under another revision: what the server has now
		// is for the next request.
		return &api.ContactPhotoResult{State: api.ContactPhotoStateNone, Revision: stored.ETag}, nil
	}
	return row, err
}

// storedPhoto is the stored photo of the contact's current revision, ok
// false when there is none.
func (b *Backend) storedPhoto(ctx context.Context, c store.StoredContact) (*api.ContactPhotoResult, bool, error) {
	row, err := b.store.GetContactPhoto(ctx, c.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, false, nil
	case err != nil:
		return nil, false, api.NewError(api.CodeStorageError, "%v", err)
	case row.Revision != c.ETag:
		return nil, false, nil
	}
	return photoResult(row), true, nil
}

// makePhoto reads the photo of a contact's current revision and stores
// it: from its card (vCard), or downloaded (Graph). A missing or refused
// photo is stored as such; a failed download is the error and nothing is
// stored. The contact's book is notified after a stored picture or a
// download.
func (b *Backend) makePhoto(ctx context.Context, c store.StoredContact, bk store.AddressBook, a store.Account) error {
	var row store.ContactPhoto
	downloaded := false
	switch c.RawKind {
	case store.ContactRawVCard:
		card, err := vcard.Parse(c.Raw)
		if err != nil {
			card = nil
		}
		var class string
		row, class = contactsync.PhotoRow(c.ID, c.ETag, card)
		if class != "" {
			b.log.Info("contact photo refused", "contact", c.ID, "class", class)
		}
	case store.ContactRawGraph:
		var err error
		if row, err = b.downloadPhoto(ctx, c, bk, a); err != nil {
			return err
		}
		downloaded = true
	default:
		row = store.ContactPhoto{ContactID: c.ID, Revision: c.ETag, State: store.PhotoNone}
	}
	err := b.store.SetContactPhoto(ctx, row)
	if errors.Is(err, store.ErrNotFound) {
		return api.NewError(api.CodeContactNotFound, "the contact is gone")
	}
	if err != nil {
		return api.NewError(api.CodeStorageError, "%v", err)
	}
	if downloaded || row.State == store.PhotoOK {
		b.contactsChanged(a.ID, bk.ID)
	}
	return nil
}

// downloadPhoto fetches and normalises a Graph contact's photo within
// ContactPhotoTimeout and the account's download slots.
func (b *Backend) downloadPhoto(ctx context.Context, c store.StoredContact, bk store.AddressBook, a store.Account) (store.ContactPhoto, error) {
	row := store.ContactPhoto{ContactID: c.ID, Revision: c.ETag, State: store.PhotoNone}
	ctx, cancel := context.WithTimeout(ctx, api.ContactPhotoTimeout)
	defer cancel()
	slot := b.photoFetch.slot(a.ID)
	select {
	case slot <- struct{}{}:
		defer func() { <-slot }()
	case <-ctx.Done():
		return row, photoContextError(ctx)
	}
	prov, err := b.contactsProviderFor(ctx, a)
	if err != nil {
		return row, err
	}
	src, ok := prov.(contactPhotoSource)
	if !ok {
		return row, nil // a source without photos: none
	}
	data, mime, err := src.ContactPhoto(ctx, bk, c.RemoteID)
	switch {
	case errors.Is(err, errNoPhoto):
		return row, nil
	case errors.Is(err, errPhotoRefused):
		b.log.Info("contact photo refused", "contact", c.ID, "class", "tooLarge")
		row.State = store.PhotoRefused
		return row, nil
	case err != nil:
		if ctx.Err() != nil {
			return row, photoContextError(ctx)
		}
		return row, err
	}
	out, outMIME, nerr := contactphoto.Normalize(data, mime)
	if nerr != nil {
		b.log.Info("contact photo refused", "contact", c.ID, "class", contactphoto.Class(nerr))
		row.State = store.PhotoRefused
		return row, nil
	}
	row.State, row.MIME, row.Data = store.PhotoOK, outMIME, out
	return row, nil
}

// photoContextError is the error of a download whose context ended.
func photoContextError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return api.NewError(api.CodeServerTimeout, "the photo did not arrive in time")
	}
	return api.NewError(api.CodeCancelled, "cancelled")
}

// ContactPhoto is contactPhotoSource of the Graph provider.
func (p *graphContacts) ContactPhoto(ctx context.Context, book store.AddressBook, remoteID string) ([]byte, string, error) {
	data, mime, err := p.client.ContactPhoto(ctx, book.RemoteID, remoteID)
	switch {
	case errors.Is(err, graph.ErrNoPhoto):
		return nil, "", errNoPhoto
	case errors.Is(err, graph.ErrPhotoTooLarge):
		return nil, "", errPhotoRefused
	case err != nil:
		return nil, "", p.mapError(err)
	}
	return data, mime, nil
}

var _ contactPhotoSource = (*graphContacts)(nil)
