// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

// MaxContactPhotoBytes caps the picture ContactPhoto reads
// (docs/plans/address-book.md F3); a larger answer is refused.
const MaxContactPhotoBytes = 4 << 20

var (
	// ErrNoPhoto: the contact has no picture (the service answered 404).
	ErrNoPhoto = errors.New("graph: the contact has no photo")
	// ErrPhotoTooLarge: the picture is over MaxContactPhotoBytes.
	ErrPhotoTooLarge = errors.New("graph: the contact's photo is too large")
)

// contactPhotoPath is the picture of a contact of a folder
// (DefaultContactFolderID: /me/contacts).
func contactPhotoPath(folderID, id string) string {
	base := contactPath(id)
	if folderID != "" && folderID != DefaultContactFolderID {
		base = contactsCollection(folderID) + "/" + url.PathEscape(id)
	}
	return base + "/photo/$value"
}

// ContactPhoto downloads the picture of a contact of a folder: its bytes
// as the service sends them and the media type it names ("" when none
// usable). The bytes are a third party's image data, unchecked: the caller
// decodes them only through internal/contactphoto. The request takes the
// client's limiter slot like any other and its retry policy applies.
//
// Errors: ErrNoPhoto (404: no picture, or no such contact),
// ErrPhotoTooLarge, ErrContactsAccessDenied, a cancelled context, else
// the *api.Error of the request (ToAPIError).
func (c *Client) ContactPhoto(ctx context.Context, folderID, id string) ([]byte, string, error) {
	if !validID(id) || (folderID != "" && !validID(folderID)) {
		return nil, "", fmt.Errorf("%w: bad contact id", ErrNoPhoto)
	}
	var data []byte
	var mediaType string
	err := c.request(ctx, http.MethodGet, contactPhotoPath(folderID, id), nil, "", 0, nil, bodyTimeout, func(resp *http.Response) error {
		defer resp.Body.Close()
		if resp.ContentLength > MaxContactPhotoBytes {
			return ErrPhotoTooLarge
		}
		if mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err == nil {
			mediaType = strings.ToLower(mt)
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxContactPhotoBytes+1))
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return classifyTransport(err)
		}
		if len(raw) > MaxContactPhotoBytes {
			return ErrPhotoTooLarge
		}
		data = raw
		return nil
	})
	switch {
	case err == nil:
		if len(data) == 0 {
			return nil, "", ErrNoPhoto
		}
		return data, mediaType, nil
	case errors.Is(err, ErrPhotoTooLarge):
		return nil, "", err
	case IsNotFound(err):
		return nil, "", fmt.Errorf("%w: the service answered 404", ErrNoPhoto)
	}
	return nil, "", contactsError(err)
}
