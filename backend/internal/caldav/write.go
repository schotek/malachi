// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package caldav

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Get fetches one object (≤ api.MaxICSBytes; more is ErrTooLarge). A web
// page is not an object (serverError); 404/410 are a *StatusError
// (IsGone).
func (c *Client) Get(ctx context.Context, href string) (Object, error) {
	u, err := c.URL(href)
	if err != nil {
		return Object{}, err
	}
	a, err := c.do(ctx, request{method: http.MethodGet, url: u, accept: "text/calendar", limit: MaxObjectBytes})
	if err != nil {
		return Object{}, err
	}
	if ct := strings.ToLower(a.header.Get("Content-Type")); strings.HasPrefix(ct, "text/html") {
		return Object{}, api.NewError(api.CodeServerError, "caldav: the server answered with a web page")
	}
	if len(a.body) > api.MaxICSBytes {
		return Object{}, fmt.Errorf("%w: object over %d bytes", ErrTooLarge, api.MaxICSBytes)
	}
	return Object{Href: u.EscapedPath(), ETag: strongETag(a.header.Get("ETag")), Data: a.body, Status: http.StatusOK}, nil
}

// Put stores data (one iCalendar object, ≤ api.MaxICSBytes) at href.
// With etag "" it creates (If-None-Match: *; an existing object is 412),
// otherwise it replaces the version etag (If-Match; another version is
// 412, IsConflict). It returns the new ETag: the answer's, or, when the
// server sends none (or a weak one, RFC 4791 §5.3.4), the one of a
// following GET; "" when even that has none.
func (c *Client) Put(ctx context.Context, href string, data []byte, etag string) (string, error) {
	if len(data) == 0 || len(data) > api.MaxICSBytes {
		return "", api.NewError(api.CodeInvalidArgument, "caldav: object empty or over %d bytes", api.MaxICSBytes)
	}
	u, err := c.URL(href)
	if err != nil {
		return "", err
	}
	h := map[string]string{"If-None-Match": "*"}
	if etag != "" {
		if cleanToken(etag, maxETagBytes) != etag {
			return "", api.NewError(api.CodeInvalidArgument, "caldav: unusable etag")
		}
		h = map[string]string{"If-Match": etag}
	}
	a, err := c.do(ctx, request{
		method: http.MethodPut, url: u, body: data, headers: h,
		contentType: "text/calendar; charset=utf-8",
	})
	if err != nil {
		return "", err
	}
	if e := strongETag(a.header.Get("ETag")); e != "" {
		return e, nil
	}
	o, err := c.Get(ctx, u.EscapedPath())
	if err != nil {
		return "", err
	}
	return o.ETag, nil
}

// Delete removes the object at href; with etag only that version
// (If-Match; another is 412). A missing object is a *StatusError
// (IsGone), which callers usually take as done.
func (c *Client) Delete(ctx context.Context, href, etag string) error {
	u, err := c.URL(href)
	if err != nil {
		return err
	}
	var h map[string]string
	if etag != "" {
		if cleanToken(etag, maxETagBytes) != etag {
			return api.NewError(api.CodeInvalidArgument, "caldav: unusable etag")
		}
		h = map[string]string{"If-Match": etag}
	}
	_, err = c.do(ctx, request{method: http.MethodDelete, url: u, headers: h})
	return err
}

// strongETag is a strong ETag header value, "" for none or a weak one.
func strongETag(v string) string {
	v = cleanToken(v, maxETagBytes)
	if strings.HasPrefix(v, "W/") {
		return ""
	}
	return v
}

// NewUID is the UID of a new event: a random UUID v4 in upper case,
// nothing derived from the user or the host.
func NewUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return strings.ToUpper(fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]))
}

// ObjectHref is the href of a new object of collection: <collection>/<uid>.ics,
// the UID path-escaped.
func ObjectHref(collection, uid string) string {
	if !strings.HasSuffix(collection, "/") {
		collection += "/"
	}
	return collection + url.PathEscape(uid) + ".ics"
}
