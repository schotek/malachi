// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package caldav

import (
	"context"
	"encoding/xml"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

const (
	// MaxResources bounds the hrefs of one collection a listing returns
	// (api.MaxEventsPerCalendar); more is ErrTooManyResources.
	MaxResources = api.MaxEventsPerCalendar
	// MultigetBatch is how many hrefs one calendar-multiget asks for.
	MultigetBatch = 50
)

// CollectionState is what a depth-0 PROPFIND says about a collection: the
// values a pass compares to decide whether anything changed.
type CollectionState struct {
	CTag      string
	SyncToken string
	// SupportsSync: supported-report-set names sync-collection.
	SupportsSync bool
}

// State reads the ctag, sync-token and supported reports of collection.
func (c *Client) State(ctx context.Context, collection string) (CollectionState, error) {
	u, err := c.URL(collection)
	if err != nil {
		return CollectionState{}, err
	}
	ms, err := c.propfind(ctx, u, 0, []xml.Name{PropGetCTag, PropSyncToken, PropSupportedReportSet})
	if err != nil {
		return CollectionState{}, err
	}
	r := findResponse(ms, u.EscapedPath())
	if r == nil {
		return CollectionState{}, malformed("PROPFIND did not describe the collection")
	}
	return CollectionState{
		CTag:         r.Props.CTag,
		SyncToken:    r.Props.SyncToken,
		SupportsSync: r.Props.SupportsReport(ReportSyncCollection),
	}, nil
}

// findResponse is the response for path (with or without a trailing
// slash); with one response only, that one (servers answer depth 0 with
// the canonical href).
func findResponse(ms *multistatus, path string) *Response {
	want := strings.TrimSuffix(path, "/")
	for i := range ms.Responses {
		if strings.TrimSuffix(ms.Responses[i].Href, "/") == want {
			return &ms.Responses[i]
		}
	}
	if len(ms.Responses) == 1 {
		return &ms.Responses[0]
	}
	return nil
}

func samePath(a, b string) bool { return strings.TrimSuffix(a, "/") == strings.TrimSuffix(b, "/") }

// Resource is a member of a collection: its href and ETag ("" when the
// server named none; the caller fetches it).
type Resource struct {
	Href string
	ETag string
}

// SyncResult is one sync-collection answer (RFC 6578).
type SyncResult struct {
	Changed []Resource
	Removed []string
	// Token is the new sync token; with Truncated the caller asks again
	// with it for the rest.
	Token     string
	Truncated bool
}

// SyncCollection asks collection for the changes since token ("" = an
// initial sync listing every member). A token the server no longer
// accepts (403/409, DAV:valid-sync-token) is ErrInvalidSyncToken; a 507
// for the collection inside the multistatus marks a truncated answer.
func (c *Client) SyncCollection(ctx context.Context, collection, token string) (*SyncResult, error) {
	u, err := c.URL(collection)
	if err != nil {
		return nil, err
	}
	ms, err := c.report(ctx, u, "", syncCollectionBody(token))
	if err != nil {
		var se *StatusError
		if errors.As(err, &se) && (se.Status == http.StatusForbidden || se.Status == http.StatusConflict) &&
			(se.Precondition == "valid-sync-token" || token != "") {
			return nil, ErrInvalidSyncToken
		}
		return nil, err
	}
	if ms.SyncToken == "" {
		return nil, malformed("sync-collection answer without a sync token")
	}
	res := &SyncResult{Token: ms.SyncToken}
	self := u.EscapedPath()
	for _, r := range ms.Responses {
		switch {
		case samePath(r.Href, self):
			if r.Status == http.StatusInsufficientStorage {
				res.Truncated = true
			}
			continue
		case r.Status == http.StatusNotFound || r.Status == http.StatusGone:
			res.Removed = append(res.Removed, r.Href)
		case r.Status != 0 && r.Status != http.StatusOK:
			continue
		case r.Props.Is(TypeCollection):
			continue
		default:
			res.Changed = append(res.Changed, Resource{Href: r.Href, ETag: r.Props.ETag})
		}
		if len(res.Changed)+len(res.Removed) > MaxResources {
			return nil, ErrTooManyResources
		}
	}
	return res, nil
}

// QueryETags lists the VEVENT objects of collection overlapping [from,
// to) with a calendar-query time-range (RFC 4791 §7.8). A server that
// refuses the query (400, 403, 405, 415, 422, 501) is asked with a depth-1
// PROPFIND for every member instead; filtered reports which of the two
// answered.
func (c *Client) QueryETags(ctx context.Context, collection string, from, to time.Time) (res []Resource, filtered bool, err error) {
	u, err := c.URL(collection)
	if err != nil {
		return nil, false, err
	}
	const f = "20060102T150405Z"
	ms, err := c.report(ctx, u, "1", calendarQueryBody(from.UTC().Format(f), to.UTC().Format(f)))
	if err != nil {
		if refused(err) {
			res, err := c.listETags(ctx, u)
			return res, false, err
		}
		return nil, false, err
	}
	res, err = members(ms, u.EscapedPath())
	return res, true, err
}

// ListETags lists every member of collection with a depth-1 PROPFIND.
func (c *Client) ListETags(ctx context.Context, collection string) ([]Resource, error) {
	u, err := c.URL(collection)
	if err != nil {
		return nil, err
	}
	return c.listETags(ctx, u)
}

func (c *Client) listETags(ctx context.Context, u *url.URL) ([]Resource, error) {
	ms, err := c.propfind(ctx, u, 1, []xml.Name{PropResourceType, PropGetETag})
	if err != nil {
		return nil, err
	}
	return members(ms, u.EscapedPath())
}

// members are the non-collection responses other than the collection.
func members(ms *multistatus, self string) ([]Resource, error) {
	var out []Resource
	for _, r := range ms.Responses {
		if samePath(r.Href, self) || r.Props.Is(TypeCollection) || (r.Status != 0 && r.Status != http.StatusOK) {
			continue
		}
		if len(out) >= MaxResources {
			return nil, ErrTooManyResources
		}
		out = append(out, Resource{Href: r.Href, ETag: r.Props.ETag})
	}
	return out, nil
}

// refused reports a status by which a server says it does not do a
// report (or a filter of it).
func refused(err error) bool {
	switch Status(err) {
	case http.StatusBadRequest, http.StatusForbidden, http.StatusMethodNotAllowed,
		http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity, http.StatusNotImplemented:
		return true
	}
	return false
}

// Object is one calendar object as the server has it.
type Object struct {
	Href string
	ETag string
	Data []byte
	// Status is 200 for an object read, 404 when the server does not
	// have it (any more).
	Status int
	// TooLarge: the object exceeded api.MaxICSBytes and was not read.
	TooLarge bool
}

// Multiget fetches hrefs of collection with calendar-multiget, in batches
// of MultigetBatch. Each href asked for gets one Object in the order
// asked; answers for hrefs not asked for are ignored, an href the server
// leaves out is Status 404, an answer without data is fetched with GET. A
// server that refuses the report (400, 403, 405, 415, 422, 501) is asked
// with a GET per object instead.
func (c *Client) Multiget(ctx context.Context, collection string, hrefs []string) ([]Object, error) {
	return c.multiget(ctx, collection, hrefs, calendarObjects)
}

// objectKind is what a multiget fetches: calendar objects or vCards.
type objectKind struct {
	name  string
	batch int
	body  func(hrefs []string) []byte
	// data is the object of a response and whether it was over the cap.
	data func(p *Props) ([]byte, bool)
	get  func(c *Client, ctx context.Context, href string) (Object, error)
}

var calendarObjects = objectKind{
	name: "multiget", batch: MultigetBatch, body: multigetBody,
	data: func(p *Props) ([]byte, bool) { return p.CalendarData, p.DataTooLarge },
	get:  (*Client).Get,
}

func (c *Client) multiget(ctx context.Context, collection string, hrefs []string, kind objectKind) ([]Object, error) {
	u, err := c.URL(collection)
	if err != nil {
		return nil, err
	}
	out := make([]Object, 0, len(hrefs))
	useGet := false
	for start := 0; start < len(hrefs); start += kind.batch {
		batch := hrefs[start:min(start+kind.batch, len(hrefs))]
		norm := make([]string, len(batch))
		for i, h := range batch {
			if norm[i] = NormaliseHref(h); norm[i] == "" {
				return nil, api.NewError(api.CodeInvalidArgument, "caldav: unusable href")
			}
		}
		if !useGet {
			objs, err := c.multigetBatch(ctx, u, norm, kind)
			if err == nil {
				out = append(out, objs...)
				continue
			}
			if !refused(err) {
				return nil, err
			}
			c.log.Debug("caldav: " + kind.name + " refused, fetching objects one by one")
			useGet = true
		}
		for _, h := range norm {
			o, err := c.getOrGone(ctx, h, kind)
			if err != nil {
				return nil, err
			}
			out = append(out, o)
		}
	}
	return out, nil
}

func (c *Client) multigetBatch(ctx context.Context, u *url.URL, hrefs []string, kind objectKind) ([]Object, error) {
	ms, err := c.report(ctx, u, "1", kind.body(hrefs))
	if err != nil {
		return nil, err
	}
	got := make(map[string]*Response, len(ms.Responses))
	for i := range ms.Responses {
		r := &ms.Responses[i]
		if _, dup := got[r.Href]; !dup {
			got[r.Href] = r
		}
	}
	out := make([]Object, 0, len(hrefs))
	for _, h := range hrefs {
		r := got[h]
		var data []byte
		tooLarge := false
		if r != nil {
			data, tooLarge = kind.data(&r.Props)
		}
		switch {
		case r == nil, r.Status == http.StatusNotFound, r.Status == http.StatusGone:
			out = append(out, Object{Href: h, Status: http.StatusNotFound})
		case tooLarge:
			out = append(out, Object{Href: h, ETag: r.Props.ETag, Status: http.StatusOK, TooLarge: true})
		case data == nil || r.Props.ETag == "":
			o, err := c.getOrGone(ctx, h, kind)
			if err != nil {
				return nil, err
			}
			out = append(out, o)
		default:
			out = append(out, Object{Href: h, ETag: r.Props.ETag, Data: data, Status: http.StatusOK})
		}
	}
	return out, nil
}

// getOrGone is Get with a 404/410 turned into Status 404 and an object
// over the limit into TooLarge.
func (c *Client) getOrGone(ctx context.Context, href string, kind objectKind) (Object, error) {
	o, err := kind.get(c, ctx, href)
	switch {
	case err == nil:
		return o, nil
	case IsGone(err):
		return Object{Href: href, Status: http.StatusNotFound}, nil
	case errors.Is(err, ErrTooLarge):
		return Object{Href: href, Status: http.StatusOK, TooLarge: true}, nil
	}
	return Object{}, err
}
