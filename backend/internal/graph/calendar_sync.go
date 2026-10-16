// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/calsync"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Delta modes of a Graph calendar (SyncState.DeltaMode, D14).
const (
	DeltaModeDelta = "delta"
	DeltaModeList  = "list"
)

const (
	// maxCalendarPages bounds the pages of one listing (200 items each).
	maxCalendarPages = 2000
	viewTimeLayout   = "2006-01-02T15:04:05Z"
)

func windowQuery(from, to time.Time) string {
	v := url.Values{}
	v.Set("startDateTime", from.UTC().Format(viewTimeLayout))
	v.Set("endDateTime", to.UTC().Format(viewTimeLayout))
	return v.Encode()
}

// sameOrigin reports whether a link the service handed out (nextLink,
// deltaLink) points at the client's own endpoint: a stored token is
// never followed to another host.
func (c *Client) sameOrigin(link string) bool {
	if !strings.HasPrefix(link, "https://") && !strings.HasPrefix(link, "http://") {
		return true
	}
	b, err1 := url.Parse(c.base)
	l, err2 := url.Parse(link)
	return err1 == nil && err2 == nil && strings.EqualFold(b.Scheme, l.Scheme) && strings.EqualFold(b.Host, l.Host) && l.User == nil
}

// pages walks a paged listing from first, calling fn for every item; it
// returns the deltaLink of the last page ("" for a plain listing).
func (p *CalendarProvider) pages(ctx context.Context, first string, prio Priority, fn func(*graphEvent) error) (string, error) {
	next := first
	for n := 0; next != ""; n++ {
		if n >= maxCalendarPages {
			return "", api.NewError(api.CodeServerError, "graph: calendar listing over %d pages", maxCalendarPages)
		}
		if !p.c.sameOrigin(next) {
			return "", api.NewError(api.CodeServerError, "graph: calendar link to another host")
		}
		var pg page[graphEvent]
		if err := p.c.calDo(ctx, calReq{method: http.MethodGet, url: next, out: &pg, prio: prio}); err != nil {
			return "", err
		}
		for i := range pg.Value {
			if err := fn(&pg.Value[i]); err != nil {
				return "", err
			}
		}
		if pg.DeltaLink != "" {
			if !p.c.sameOrigin(pg.DeltaLink) {
				return "", api.NewError(api.CodeServerError, "graph: calendar link to another host")
			}
			return pg.DeltaLink, nil
		}
		next = pg.NextLink
	}
	return "", nil
}

// getEvent fetches one event (a master) by id.
func (p *CalendarProvider) getEvent(ctx context.Context, id string, prio Priority) (*graphEvent, error) {
	var ge graphEvent
	if err := p.c.calDo(ctx, calReq{method: http.MethodGet, url: eventPath(id), out: &ge, prio: prio}); err != nil {
		return nil, err
	}
	if ge.ID == "" {
		ge.ID = id
	}
	return &ge, nil
}

// instances lists a series' occurrences and exceptions in win.
func (p *CalendarProvider) instances(ctx context.Context, id string, win calsync.Window, prio Priority) ([]*graphEvent, error) {
	var out []*graphEvent
	_, err := p.pages(ctx, eventPath(id)+"/instances?"+windowQuery(win.From, win.To)+"&$top=200", prio, func(ge *graphEvent) error {
		if len(out) < api.MaxSeriesOccurrences && ge.ID != "" {
			g := *ge
			out = append(out, &g)
		}
		return nil
	})
	return out, err
}

// seriesObject fetches a series' master and its instances in win and
// builds its object; gone reports that the master no longer exists.
func (p *CalendarProvider) seriesObject(ctx context.Context, shared bool, id string, win calsync.Window, prio Priority) (obj calendar.Object, gone bool, err error) {
	m, err := p.getEvent(ctx, id, prio)
	if err != nil {
		if isItemGone(err) {
			return obj, true, nil
		}
		return obj, false, err
	}
	var insts []*graphEvent
	if m.Type == "seriesMaster" {
		if insts, err = p.instances(ctx, id, win, prio); err != nil {
			if isItemGone(err) {
				return obj, true, nil
			}
			return obj, false, err
		}
	}
	obj, ok := p.objectOf(shared, m, insts)
	if !ok {
		return obj, true, nil
	}
	return obj, false, nil
}

// deltaRefused: the service does not offer delta for this calendar.
func deltaRefused(err error) bool {
	se, ok := statusOf(err)
	if !ok || isAccessDenied(err) {
		return false
	}
	return se.Status == http.StatusBadRequest || se.Status == http.StatusNotFound || se.Status == http.StatusNotImplemented
}

// Sync brings one calendar up to date in win (D14): calendarView/delta
// (the default calendar through /me/calendarView/delta), resumed from the
// stored deltaLink; a token the service no longer accepts starts over; a
// calendar whose delta is refused (400/404/501) falls back for good to a
// paged calendarView listing (DeltaMode list).
func (p *CalendarProvider) Sync(ctx context.Context, cal calsync.StoredCalendar, win calsync.Window, sink calsync.Sink) (calsync.SyncState, error) {
	if !win.From.Before(win.To) {
		return calsync.SyncState{}, api.NewError(api.CodeInvalidArgument, "graph: empty sync window")
	}
	if cal.SyncMode != DeltaModeList {
		st, err := p.syncDelta(ctx, cal, win, sink, cal.SyncToken)
		if err != nil && cal.SyncToken != "" && (isSyncStateError(err) || deltaRefused(err)) {
			// A stale or refused token: once more from scratch.
			st, err = p.syncDelta(ctx, cal, win, sink, "")
		}
		if err == nil {
			return st, nil
		}
		if !deltaRefused(err) {
			return calsync.SyncState{}, p.readError(err)
		}
	}
	st, err := p.syncList(ctx, cal, win, sink)
	if err != nil {
		return calsync.SyncState{}, p.readError(err)
	}
	return st, nil
}

func (p *CalendarProvider) syncDelta(ctx context.Context, cal calsync.StoredCalendar, win calsync.Window, sink calsync.Sink, token string) (calsync.SyncState, error) {
	if !p.c.sameOrigin(token) {
		token = "" // a stored link to another host is never followed
	}
	full := token == ""
	first := token
	if full {
		path := calendarPath(cal.RemoteID) + "/calendarView/delta"
		if cal.ServerDefault {
			path = "me/calendarView/delta"
		}
		first = path + "?" + windowQuery(win.From, win.To)
	}
	known, _ := p.known(ctx, cal)
	shared := p.sharedCalendar(cal)
	masters := map[string]*graphEvent{}
	insts := map[string][]*graphEvent{}
	var order []string
	touched := map[string]bool{}
	touch := func(id string) {
		if id != "" && !touched[id] {
			touched[id] = true
			order = append(order, id)
		}
	}
	objects := 0
	deltaLink, err := p.pages(ctx, first, PriorityLow, func(ge *graphEvent) error {
		if ge.ID == "" {
			return nil
		}
		if ge.Removed != nil {
			if s, ok := known.Instances[ge.ID]; ok && s != ge.ID {
				touch(s)
				return nil
			}
			delete(masters, ge.ID)
			return sink.Remove(ge.ID)
		}
		switch ge.Type {
		case "seriesMaster":
			g := *ge
			masters[ge.ID] = &g
			touch(ge.ID)
		case "occurrence", "exception":
			touch(ge.SeriesMasterID)
			if full && len(insts[ge.SeriesMasterID]) < api.MaxSeriesOccurrences {
				g := *ge
				insts[ge.SeriesMasterID] = append(insts[ge.SeriesMasterID], &g)
			}
		default:
			if objects++; objects > api.MaxEventsPerCalendar {
				return api.NewError(api.CodeServerError, "graph: calendar over %d events", api.MaxEventsPerCalendar)
			}
			obj, ok := p.objectOf(shared, ge, nil)
			if !ok {
				return nil
			}
			return sink.Upsert(obj)
		}
		return nil
	})
	if err != nil {
		return calsync.SyncState{}, err
	}
	if deltaLink == "" {
		return calsync.SyncState{}, api.NewError(api.CodeServerError, "graph: calendar delta without a deltaLink")
	}
	for _, id := range order {
		if objects++; objects > api.MaxEventsPerCalendar {
			return calsync.SyncState{}, api.NewError(api.CodeServerError, "graph: calendar over %d events", api.MaxEventsPerCalendar)
		}
		var obj calendar.Object
		gone := false
		if full {
			// Everything in the window came with the delta; the master
			// is fetched when the delta's copy lacks its pattern or its
			// version is not the stored one.
			m := masters[id]
			if m == nil || m.Recurrence == nil || known.Objects[id] == "" || known.Objects[id] != m.version() {
				fetched, err := p.getEvent(ctx, id, PriorityLow)
				switch {
				case err == nil:
					m = fetched
				case isItemGone(err):
					gone = true
				default:
					return calsync.SyncState{}, err
				}
			}
			if !gone {
				var ok bool
				if obj, ok = p.objectOf(shared, m, insts[id]); !ok {
					gone = true
				}
			}
		} else {
			var err error
			if obj, gone, err = p.seriesObject(ctx, shared, id, win, PriorityLow); err != nil {
				return calsync.SyncState{}, err
			}
		}
		if gone {
			if err := sink.Remove(id); err != nil {
				return calsync.SyncState{}, err
			}
			continue
		}
		if err := sink.Upsert(obj); err != nil {
			return calsync.SyncState{}, err
		}
	}
	return calsync.SyncState{Token: deltaLink, DeltaMode: DeltaModeDelta, Full: full}, nil
}

// syncList is the fallback: the whole window listed, compared with what
// is stored by version. With Known it upserts only what changed and
// removes what is gone itself (Full false); without, it upserts
// everything and sets Full.
func (p *CalendarProvider) syncList(ctx context.Context, cal calsync.StoredCalendar, win calsync.Window, sink calsync.Sink) (calsync.SyncState, error) {
	known, haveKnown := p.known(ctx, cal)
	shared := p.sharedCalendar(cal)
	seen := map[string]bool{}
	insts := map[string][]*graphEvent{}
	var order []string
	objects := 0
	_, err := p.pages(ctx, calendarPath(cal.RemoteID)+"/calendarView?"+windowQuery(win.From, win.To)+"&$top=200", PriorityLow, func(ge *graphEvent) error {
		if ge.ID == "" {
			return nil
		}
		switch ge.Type {
		case "occurrence", "exception":
			if ge.SeriesMasterID == "" {
				return nil
			}
			if _, ok := insts[ge.SeriesMasterID]; !ok {
				order = append(order, ge.SeriesMasterID)
			}
			if len(insts[ge.SeriesMasterID]) < api.MaxSeriesOccurrences {
				g := *ge
				insts[ge.SeriesMasterID] = append(insts[ge.SeriesMasterID], &g)
			}
			return nil
		case "seriesMaster":
			return nil
		}
		if objects++; objects > api.MaxEventsPerCalendar {
			return api.NewError(api.CodeServerError, "graph: calendar over %d events", api.MaxEventsPerCalendar)
		}
		seen[ge.ID] = true
		if haveKnown && known.Objects[ge.ID] != "" && known.Objects[ge.ID] == ge.version() {
			return nil
		}
		obj, ok := p.objectOf(shared, ge, nil)
		if !ok {
			return nil
		}
		return sink.Upsert(obj)
	})
	if err != nil {
		return calsync.SyncState{}, err
	}
	for _, id := range order {
		m, err := p.getEvent(ctx, id, PriorityLow)
		if err != nil {
			if isItemGone(err) {
				continue
			}
			return calsync.SyncState{}, err
		}
		seen[id] = true
		if haveKnown && known.Objects[id] != "" && known.Objects[id] == m.version() && sameInstances(known, id, insts[id]) {
			continue
		}
		obj, ok := p.objectOf(shared, m, insts[id])
		if !ok {
			continue
		}
		if err := sink.Upsert(obj); err != nil {
			return calsync.SyncState{}, err
		}
	}
	if haveKnown {
		var gone []string
		for id := range known.Objects {
			if !seen[id] {
				gone = append(gone, id)
			}
		}
		sort.Strings(gone)
		for _, id := range gone {
			if err := sink.Remove(id); err != nil {
				return calsync.SyncState{}, err
			}
		}
	}
	return calsync.SyncState{DeltaMode: DeltaModeList, Full: !haveKnown}, nil
}

// sameInstances: the stored occurrences of series id are exactly these.
func sameInstances(k Known, id string, insts []*graphEvent) bool {
	n := 0
	for _, s := range k.Instances {
		if s == id {
			n++
		}
	}
	if n != len(insts) {
		return false
	}
	for _, gi := range insts {
		if k.Instances[gi.ID] != id {
			return false
		}
	}
	return true
}

// Range fetches the occurrences of [from, to) without storing anything
// (event.list outside the window). The frozen Provider signature carries
// only occurrences; RangeObjects returns them with their events.
func (p *CalendarProvider) Range(ctx context.Context, cal calsync.StoredCalendar, from, to time.Time) ([]calsync.Occurrence, error) {
	objs, err := p.RangeObjects(ctx, cal, from, to)
	if err != nil {
		return nil, err
	}
	var out []calsync.Occurrence
	for _, o := range objs {
		out = append(out, o.Occurrences...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out, nil
}

// RangeObjects lists [from, to) with calendarView as objects: a single
// event as itself, a series without its master (no extra request) whose
// every instance is an override, so each Occurrence's Override names the
// event whose fields it shows.
func (p *CalendarProvider) RangeObjects(ctx context.Context, cal calsync.StoredCalendar, from, to time.Time) ([]calendar.Object, error) {
	if !from.Before(to) {
		return nil, nil
	}
	var out []calendar.Object
	shared := p.sharedCalendar(cal)
	insts := map[string][]*graphEvent{}
	var order []string
	total := 0
	_, err := p.pages(ctx, calendarPath(cal.RemoteID)+"/calendarView?"+windowQuery(from, to)+"&$top=200", PriorityHigh, func(ge *graphEvent) error {
		if ge.ID == "" {
			return nil
		}
		if total++; total > api.MaxEventListResults {
			return nil
		}
		switch ge.Type {
		case "occurrence", "exception":
			if ge.SeriesMasterID == "" {
				return nil
			}
			if _, ok := insts[ge.SeriesMasterID]; !ok {
				order = append(order, ge.SeriesMasterID)
			}
			g := *ge
			insts[ge.SeriesMasterID] = append(insts[ge.SeriesMasterID], &g)
		case "seriesMaster":
		default:
			if obj, ok := p.objectOf(shared, ge, nil); ok {
				out = append(out, obj)
			}
		}
		return nil
	})
	if err != nil {
		return nil, p.readError(err)
	}
	for _, id := range order {
		if obj, ok := p.objectOf(shared, nil, insts[id]); ok {
			out = append(out, obj)
		}
	}
	return out, nil
}
