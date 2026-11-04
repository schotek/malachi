// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package caldav

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/calendar/ics"
	"github.com/schotek/malachi/backend/internal/calendar/imip"
	"github.com/schotek/malachi/backend/internal/calendar/tz"
	"github.com/schotek/malachi/backend/internal/calsync"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The CalDAV implementation of calsync.Provider (docs/calendar.md §3.5,
// §3.7). Every byte from the server is hostile: objects are parsed by
// calendar.Parse (its limits, a recover boundary here as well), errors
// leave as *api.Error through ToAPIError.

// Reasons of a skipped resource (store.Resource.Reason).
const (
	SkipTooBig    = "tooBig"
	SkipMalformed = "malformed"
	SkipNoEvent   = "noEvent"
	SkipLimit     = "limit"
)

// maxSyncRounds bounds the follow-up requests of one truncated
// sync-collection (each answer holds at least one change).
const maxSyncRounds = 200

// ResourceIndex is the provider's view of cal_resources: the hrefs the
// daemon knows of a calendar with their ETags. *store.Store satisfies it.
// The engine's sink writes the rows of objects it receives (stored or
// outside) and forgets removed ones; the provider reads the rows at the
// start of a pass and writes only the skipped ones.
type ResourceIndex interface {
	ListResources(ctx context.Context, calendarID string) ([]store.Resource, error)
	PutResources(ctx context.Context, calendarID string, res []store.Resource) error
}

// IMIPMessage is one iTIP message the daemon must send itself (client
// scheduling, D8): the METHOD (imip.MethodRequest, MethodCancel or
// MethodReply), the UID it is about, the recipients (lower case, never
// one of the user's addresses) and the text/calendar body built by
// internal/calendar/imip.
type IMIPMessage struct {
	Method     string
	UID        string
	Recipients []string
	Data       []byte
}

// IMIPSender queues messages in the outbox of opts.MailAccountID with
// opts.Mail's subject and text (core: smtp.BuildMessage +
// store.EnqueueOutbox + Delivery.Wake). It is called after the server
// accepted the write and must not block on the network.
type IMIPSender func(ctx context.Context, msgs []IMIPMessage, opts calsync.WriteOptions) error

// ProviderOptions configure NewProvider.
type ProviderOptions struct {
	// Addresses are the user's addresses (the account's address and the
	// calendar-user-address-set); "me" for organiser and attendee. Empty:
	// the addresses ListCalendars reports.
	Addresses []string
	// Identity is the ORGANIZER of new events with attendees; empty: the
	// first address.
	Identity api.Address
	// Scheduling is the account's setting ("" = auto).
	Scheduling api.CalDAVScheduling
	// ServerSchedules is the server's calendar-auto-schedule
	// advertisement when the caller knows it; nil: learnt from the first
	// ListCalendars (Calendars, or the first write).
	ServerSchedules *bool
	// MailAccountID is the account iMIP leaves from (CalDAVConfig.MailAccountID);
	// WriteOptions.MailAccountID overrides it per write.
	MailAccountID api.AccountID
	// Resources is cal_resources; nil keeps an in-memory map per calendar
	// id (lost on restart: the first pass then lists and fetches the
	// window again and reports Full).
	Resources ResourceIndex
	// SendIMIP queues the iMIP the provider builds; nil: no mail path
	// (a write that needs mail is invitationsUnavailable).
	SendIMIP IMIPSender
	Now      func() time.Time
	Log      *slog.Logger
}

// Provider is one CalDAV account's calendar source.
type Provider struct {
	c    *Client
	opts ProviderOptions
	now  func() time.Time
	log  *slog.Logger

	mu         sync.Mutex
	advertised *bool
	addrs      []string
	known      map[string]map[string]store.Resource // calendar id → href → row (Resources nil)
}

var _ calsync.Provider = (*Provider)(nil)

// NewProvider builds the provider over client.
func NewProvider(client *Client, opts ProviderOptions) *Provider {
	p := &Provider{c: client, opts: opts, now: opts.Now, log: opts.Log, known: map[string]map[string]store.Resource{}}
	if p.now == nil {
		p.now = time.Now
	}
	if p.log == nil {
		p.log = slog.New(slog.DiscardHandler)
	}
	if opts.ServerSchedules != nil {
		v := *opts.ServerSchedules
		p.advertised = &v
	}
	for _, a := range opts.Addresses {
		p.addAddress(a)
	}
	return p
}

func (p *Provider) addAddress(a string) {
	a = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(a, "mailto:"), "MAILTO:")))
	if a != "" && !slices.Contains(p.addrs, a) && len(p.addrs) < 64 {
		p.addrs = append(p.addrs, a)
	}
}

func (p *Provider) me() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.addrs)
}

func (p *Provider) isMe(addr string) bool {
	return slices.Contains(p.me(), strings.ToLower(strings.TrimSpace(addr)))
}

func (p *Provider) identity() api.Address {
	id := p.opts.Identity
	if id.Address == "" {
		if me := p.me(); len(me) > 0 {
			id.Address = me[0]
		}
	}
	return id
}

// learn records what ListCalendars says about the account.
func (p *Provider) learn(info *AccountInfo) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.opts.ServerSchedules == nil {
		v := info.Scheduling == api.CalDAVSchedulingServer
		p.advertised = &v
	}
	if len(p.opts.Addresses) == 0 {
		for _, a := range info.Addresses {
			p.addAddress(a)
		}
	}
}

// serverSchedules: the server sends invitations and replies (D8). With
// auto and an advertisement not yet known, ctx != nil asks the server.
func (p *Provider) serverSchedules(ctx context.Context) bool {
	switch p.opts.Scheduling {
	case api.CalDAVSchedulingServer:
		return true
	case api.CalDAVSchedulingClient:
		return false
	}
	p.mu.Lock()
	adv := p.advertised
	p.mu.Unlock()
	if adv == nil && ctx != nil {
		if info, err := p.c.ListCalendars(ctx); err == nil {
			p.learn(info)
		}
		p.mu.Lock()
		adv = p.advertised
		p.mu.Unlock()
	}
	// Unknown: assume the server schedules (no promise of mail).
	return adv == nil || *adv
}

func (p *Provider) mailAccount(opts calsync.WriteOptions) api.AccountID {
	if opts.MailAccountID != "" {
		return opts.MailAccountID
	}
	return p.opts.MailAccountID
}

func (p *Provider) canMail(opts calsync.WriteOptions) bool {
	return p.opts.SendIMIP != nil && p.mailAccount(opts) != ""
}

// Features of a CalDAV account.
func (p *Provider) Features() api.CalendarFeatures {
	f := api.CalendarFeatures{Invitations: api.InvitationsViaServer}
	if !p.serverSchedules(nil) {
		f.Invitations = api.InvitationsViaNone
		if id := p.opts.MailAccountID; id != "" && p.opts.SendIMIP != nil {
			f.Invitations, f.MailAccountID = api.InvitationsViaMail, id
		}
	}
	return f
}

// FreeBusy of other people is not used for CalDAV (D22).
func (p *Provider) FreeBusy(context.Context, []string, time.Time, time.Time, time.Duration) ([]calsync.Availability, error) {
	return nil, calsync.ErrUnsupported
}

// ---------------------------------------------------------------------------
// Calendars
// ---------------------------------------------------------------------------

// Calendars lists the VEVENT collections of the home set, in the server's
// order (the slice order is the position).
func (p *Provider) Calendars(ctx context.Context) ([]calsync.RemoteCalendar, error) {
	info, err := p.c.ListCalendars(ctx)
	if err != nil {
		return nil, ToAPIError(err)
	}
	p.learn(info)
	out := make([]calsync.RemoteCalendar, 0, len(info.Calendars))
	for _, c := range info.Calendars {
		out = append(out, calsync.RemoteCalendar{
			RemoteID: c.Href, Name: c.Name, Color: c.Color, Access: c.Access,
			Kind: api.CalendarKindEvents, IsDefault: c.IsDefault,
			TimeZone: timeZoneOf(c.Timezone), CTag: c.CTag,
		})
	}
	return out, nil
}

// timeZoneOf is the IANA zone of a calendar-timezone VCALENDAR, "".
func timeZoneOf(vcal string) api.TimeZone {
	if vcal == "" || len(vcal) > 256<<10 {
		return ""
	}
	obj, err := ics.Parse([]byte(vcal))
	if err != nil {
		return ""
	}
	for _, z := range obj.Calendar().Children("VTIMEZONE") {
		id := z.Property("TZID").Text()
		loc, kind, err := tz.Resolve(id)
		if err != nil || loc == nil || kind == tz.KindFixed || kind == tz.KindMatched {
			continue
		}
		if name := loc.String(); name != "" && name != "Local" {
			return api.TimeZone(name)
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Sync
// ---------------------------------------------------------------------------

func dateOf(t time.Time) string { return t.UTC().Format(time.DateOnly) }

// tooManyEvents is the error of a collection over MaxResources.
func tooManyEvents() *api.Error {
	e := api.NewError(api.CodeServerError, "caldav: more than %d events in the calendar", MaxResources)
	e.Data = map[string]string{"reason": "tooManyEvents"}
	return e
}

func syncErr(err error) error {
	if errors.Is(err, ErrTooManyResources) {
		return tooManyEvents()
	}
	return ToAPIError(err)
}

// Sync is one pass over a collection (§3.5): the depth-0 state, then
// sync-collection with the stored token, else a calendar-query of the
// window; changed hrefs are fetched by multiget in batches of 50 and
// handed to sink in order. A pass whose window differs from the window
// of cal (WindowFrom/WindowTo) lists the window again.
func (p *Provider) Sync(ctx context.Context, cal calsync.StoredCalendar, win calsync.Window, sink calsync.Sink) (calsync.SyncState, error) {
	coll := cal.RemoteID
	st, err := p.c.State(ctx, coll)
	if err != nil {
		return calsync.SyncState{}, syncErr(err)
	}
	moved := cal.WindowFrom != dateOf(win.From) || cal.WindowTo != dateOf(win.To)
	unchanged := (st.CTag != "" || st.SyncToken != "") && st.CTag == cal.CTag && st.SyncToken == cal.SyncToken
	if unchanged && !moved {
		return calsync.SyncState{Token: cal.SyncToken, CTag: cal.CTag}, nil
	}
	known, err := p.loadKnown(ctx, cal.ID)
	if err != nil {
		return calsync.SyncState{}, api.NewError(api.CodeStorageError, "caldav: read resources: %v", err)
	}
	ps := &pass{p: p, cal: cal, win: win, sink: sink, known: known, moved: moved}
	if cal.SyncToken != "" && st.SupportsSync && !moved {
		tok, err := ps.incremental(ctx, cal.SyncToken)
		switch {
		case err == nil:
			return calsync.SyncState{Token: tok, CTag: st.CTag}, nil
		case errors.Is(err, ErrInvalidSyncToken):
			p.log.Debug("caldav: sync token refused, listing the window", "calendar", cal.ID)
		default:
			return calsync.SyncState{}, syncErr(err)
		}
	}
	full, err := ps.listing(ctx)
	if err != nil {
		return calsync.SyncState{}, syncErr(err)
	}
	tok := ""
	if st.SupportsSync {
		tok = st.SyncToken
	}
	return calsync.SyncState{Token: tok, CTag: st.CTag, Full: full}, nil
}

// loadKnown returns the known hrefs of a calendar (a copy).
func (p *Provider) loadKnown(ctx context.Context, calendarID string) (map[string]store.Resource, error) {
	out := map[string]store.Resource{}
	if p.opts.Resources != nil {
		rows, err := p.opts.Resources.ListResources(ctx, calendarID)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if h := NormaliseHref(r.Href); h != "" {
				out[h] = r
			}
		}
		return out, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for h, r := range p.known[calendarID] {
		out[h] = r
	}
	return out, nil
}

// pass is the state of one Sync.
type pass struct {
	p     *Provider
	cal   calsync.StoredCalendar
	win   calsync.Window
	sink  calsync.Sink
	known map[string]store.Resource
	moved bool
}

// note records an href in the in-memory index (Resources nil).
func (ps *pass) note(r store.Resource) {
	ps.known[r.Href] = r
	if ps.p.opts.Resources != nil {
		return
	}
	ps.p.mu.Lock()
	defer ps.p.mu.Unlock()
	m := ps.p.known[ps.cal.ID]
	if m == nil {
		m = map[string]store.Resource{}
		ps.p.known[ps.cal.ID] = m
	}
	m[r.Href] = r
}

func (ps *pass) forget(href string) {
	delete(ps.known, href)
	if ps.p.opts.Resources != nil {
		return
	}
	ps.p.mu.Lock()
	defer ps.p.mu.Unlock()
	delete(ps.p.known[ps.cal.ID], href)
}

// fresh reports whether a listed resource need not be fetched.
func (ps *pass) fresh(r Resource) bool {
	k, ok := ps.known[r.Href]
	if !ok || r.ETag == "" || k.ETag != r.ETag {
		return false
	}
	return !(ps.moved && k.State == store.ResourceOutside)
}

func (ps *pass) remove(href string) error {
	if err := ps.sink.Remove(href); err != nil {
		return err
	}
	ps.forget(href)
	return nil
}

// incremental follows sync-collection from token; it returns the new
// token.
func (ps *pass) incremental(ctx context.Context, token string) (string, error) {
	var changed []string
	seen := map[string]bool{}
	for round := 0; ; round++ {
		if round >= maxSyncRounds {
			return "", malformed("sync-collection truncation does not end")
		}
		res, err := ps.p.c.SyncCollection(ctx, ps.cal.RemoteID, token)
		if err != nil {
			return "", err
		}
		for _, h := range res.Removed {
			if h = NormaliseHref(h); h == "" {
				continue
			}
			if err := ps.remove(h); err != nil {
				return "", err
			}
		}
		for _, r := range res.Changed {
			r.Href = NormaliseHref(r.Href)
			if r.Href == "" || seen[r.Href] || ps.fresh(r) {
				continue
			}
			seen[r.Href] = true
			changed = append(changed, r.Href)
		}
		if len(changed) > MaxResources {
			return "", ErrTooManyResources
		}
		if res.Token == "" || res.Token == token || !res.Truncated {
			token = res.Token
			break
		}
		token = res.Token
	}
	if err := ps.fetch(ctx, changed); err != nil {
		return "", err
	}
	return token, nil
}

// listing lists the window (calendar-query, else every member), fetches
// what is new or changed and removes what is no longer listed. It
// reports whether every object of the window went to the sink (Full).
func (ps *pass) listing(ctx context.Context) (bool, error) {
	res, _, err := ps.p.c.QueryETags(ctx, ps.cal.RemoteID, ps.win.From, ps.win.To)
	if err != nil {
		return false, err
	}
	listed := map[string]bool{}
	var fetch []string
	skippedFresh := 0
	for _, r := range res {
		if r.Href = NormaliseHref(r.Href); r.Href == "" || listed[r.Href] {
			continue
		}
		listed[r.Href] = true
		if ps.fresh(r) {
			skippedFresh++
			continue
		}
		fetch = append(fetch, r.Href)
	}
	gone := make([]string, 0)
	for h := range ps.known {
		if !listed[h] {
			gone = append(gone, h)
		}
	}
	slices.Sort(gone)
	for _, h := range gone {
		if err := ps.remove(h); err != nil {
			return false, err
		}
	}
	if err := ps.fetch(ctx, fetch); err != nil {
		return false, err
	}
	return skippedFresh == 0, nil
}

// fetch reads hrefs in batches and hands each object to the sink.
func (ps *pass) fetch(ctx context.Context, hrefs []string) error {
	for start := 0; start < len(hrefs); start += MultigetBatch {
		batch := hrefs[start:min(start+MultigetBatch, len(hrefs))]
		objs, err := ps.p.c.Multiget(ctx, ps.cal.RemoteID, batch)
		if err != nil {
			return err
		}
		for _, o := range objs {
			if err := ps.take(ctx, o); err != nil {
				return err
			}
		}
	}
	return nil
}

// take stores, removes or skips one fetched object.
func (ps *pass) take(ctx context.Context, o Object) error {
	href := NormaliseHref(o.Href)
	if href == "" {
		return nil
	}
	if o.Status == http.StatusNotFound {
		return ps.remove(href)
	}
	if o.TooLarge {
		return ps.skip(ctx, href, o.ETag, SkipTooBig)
	}
	obj, err := parseObject(o.Data, ps.p.me(), "")
	if err != nil {
		reason := SkipMalformed
		if errors.Is(err, calendar.ErrNoEvent) && hasCalendar(o.Data) {
			reason = SkipNoEvent // a VTODO, VJOURNAL or VFREEBUSY only
		}
		return ps.skip(ctx, href, o.ETag, reason)
	}
	obj.RemoteID, obj.ETag = href, o.ETag
	if err := ps.sink.Upsert(obj); err != nil {
		return err
	}
	state := store.ResourceStored
	if !inWindow(obj, ps.win) {
		state = store.ResourceOutside
	}
	ps.note(store.Resource{CalendarID: ps.cal.ID, Href: href, ETag: o.ETag, State: state})
	return nil
}

func (ps *pass) skip(ctx context.Context, href, etag, reason string) error {
	if k, ok := ps.known[href]; ok && k.State != store.ResourceSkipped {
		if err := ps.sink.Remove(href); err != nil {
			return err
		}
	}
	r := store.Resource{CalendarID: ps.cal.ID, Href: href, ETag: etag, State: store.ResourceSkipped, Reason: reason}
	if ps.p.opts.Resources != nil {
		if err := ps.p.opts.Resources.PutResources(ctx, ps.cal.ID, []store.Resource{r}); err != nil {
			return api.NewError(api.CodeStorageError, "caldav: write resource: %v", err)
		}
	}
	ps.p.log.Debug("caldav: object skipped", "calendar", ps.cal.ID, "reason", reason)
	ps.note(r)
	return nil
}

// hasCalendar reports whether data holds a VCALENDAR at all.
func hasCalendar(data []byte) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	parsed, err := ics.Parse(data)
	return err == nil && parsed.Calendar() != nil
}

// parseObject is calendar.Parse behind a recover boundary.
func parseObject(data []byte, me []string, uid string) (obj calendar.Object, err error) {
	defer func() {
		if r := recover(); r != nil {
			obj, err = calendar.Object{}, malformed("object does not read")
		}
	}()
	return calendar.Parse(data, calendar.FromOptions{Me: me, UID: uid})
}

// inWindow reports whether some instance of obj (cancelled ones too)
// overlaps the window, padded by a day for all-day and floating times.
func inWindow(obj calendar.Object, win calsync.Window) bool {
	o := obj
	o.Occurrences = nil
	if obj.Master != nil {
		m := *obj.Master
		m.Status = api.EventStatusConfirmed
		o.Master = &m
	}
	o.Overrides = slices.Clone(obj.Overrides)
	for i := range o.Overrides {
		o.Overrides[i].Status = api.EventStatusConfirmed
	}
	occs, _, err := calendar.Occurrences(o, calendar.Window{From: win.From.AddDate(0, 0, -1), To: win.To.AddDate(0, 0, 1)}, time.UTC, 1)
	return err != nil || len(occs) > 0
}

// ---------------------------------------------------------------------------
// Range
// ---------------------------------------------------------------------------

// Range fetches the occurrences of [from, to) without storing anything.
// Occurrence.RemoteID is the href of the object each comes from.
func (p *Provider) Range(ctx context.Context, cal calsync.StoredCalendar, from, to time.Time) ([]calsync.Occurrence, error) {
	objs, err := p.RangeObjects(ctx, cal, from, to)
	if err != nil {
		return nil, err
	}
	var out []calsync.Occurrence
	for _, o := range objs {
		out = append(out, o.Occurrences...)
	}
	slices.SortStableFunc(out, func(a, b calsync.Occurrence) int { return a.Start.Compare(b.Start) })
	return out, nil
}

// RangeObjects lists [from, to) as objects (as graph's RangeObjects does
// for core's event.list outside the window): a calendar-query with that
// time-range, the objects fetched by multiget, each parsed with its
// Occurrences set to its instances in [from, to) (calendar.Occurrences,
// Occurrence.RemoteID = the href; Override names the event whose fields
// an instance shows). Objects without an instance in the range, too big
// or not readable are left out; nothing is stored. At most
// api.MaxEventListResults objects, in the server's order.
func (p *Provider) RangeObjects(ctx context.Context, cal calsync.StoredCalendar, from, to time.Time) ([]calendar.Object, error) {
	if !from.Before(to) {
		return nil, nil
	}
	res, _, err := p.c.QueryETags(ctx, cal.RemoteID, from, to)
	if err != nil {
		return nil, syncErr(err)
	}
	hrefs := make([]string, 0, len(res))
	for _, r := range res {
		if h := NormaliseHref(r.Href); h != "" && !slices.Contains(hrefs, h) {
			hrefs = append(hrefs, h)
		}
	}
	me := p.me()
	var out []calendar.Object
	for start := 0; start < len(hrefs) && len(out) < api.MaxEventListResults; start += MultigetBatch {
		objs, err := p.c.Multiget(ctx, cal.RemoteID, hrefs[start:min(start+MultigetBatch, len(hrefs))])
		if err != nil {
			return nil, syncErr(err)
		}
		for _, o := range objs {
			if o.Status != http.StatusOK || o.TooLarge || len(out) >= api.MaxEventListResults {
				continue
			}
			obj, err := parseObject(o.Data, me, "")
			if err != nil {
				continue
			}
			occs, _, err := calendar.Occurrences(obj, calendar.Window{From: from, To: to}, time.UTC, 0)
			if err != nil || len(occs) == 0 {
				continue
			}
			href := NormaliseHref(o.Href)
			for i := range occs {
				occs[i].RemoteID = href
			}
			obj.RemoteID, obj.ETag, obj.Occurrences = href, o.ETag, occs
			out = append(out, obj)
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Writes
// ---------------------------------------------------------------------------

// agent says what a write does to the SCHEDULE-AGENT parameter.
type agent int

const (
	agentKeep   agent = iota
	agentClient       // SCHEDULE-AGENT=CLIENT
	agentServer       // the parameter removed: the server schedules
)

// marks are the scheduling parameters of a PUT.
type marks struct {
	attendees agent
	organizer agent
}

func setAgent(c *ics.Component, name string, a agent) {
	if a == agentKeep {
		return
	}
	for i := range c.Props {
		pr := &c.Props[i]
		if !strings.EqualFold(pr.Name, name) {
			continue
		}
		if a == agentClient {
			pr.SetParam("SCHEDULE-AGENT", "CLIENT")
			continue
		}
		pr.Params = slices.DeleteFunc(pr.Params, func(x ics.Param) bool { return strings.EqualFold(x.Name, "SCHEDULE-AGENT") })
	}
}

// encode writes obj (with its Raw preserved, calendar.ToICS) and sets the
// scheduling parameters.
func (p *Provider) encode(obj calendar.Object, m marks) ([]byte, error) {
	c, err := calendar.ToComponent(obj, calendar.WriteOptions{Now: p.now().UTC()})
	if err != nil {
		return nil, api.NewError(api.CodeInvalidArgument, "caldav: event does not write: %v", err)
	}
	for _, ev := range c.Children("VEVENT") {
		setAgent(ev, "ATTENDEE", m.attendees)
		setAgent(ev, "ORGANIZER", m.organizer)
	}
	data, err := ics.EncodeComponent(c)
	if err != nil {
		return nil, api.NewError(api.CodeInvalidArgument, "caldav: event does not encode: %v", err)
	}
	if len(data) > api.MaxICSBytes {
		return nil, api.NewError(api.CodeInvalidArgument, "caldav: event over %d bytes", api.MaxICSBytes)
	}
	return data, nil
}

// put writes obj at href (etag "" = create) and returns the object as
// stored: the written bytes parsed again, with the new ETag.
func (p *Provider) put(ctx context.Context, href string, obj calendar.Object, m marks, etag string) (calendar.Object, error) {
	data, err := p.encode(obj, m)
	if err != nil {
		return calendar.Object{}, err
	}
	newTag, err := p.c.Put(ctx, href, data, etag)
	if err != nil {
		return calendar.Object{}, p.writeErr(ctx, href, err)
	}
	out, err := parseObject(data, p.me(), obj.UID())
	if err != nil {
		return calendar.Object{}, ToAPIError(err)
	}
	out.RemoteID, out.ETag = NormaliseHref(href), newTag
	return out, nil
}

// writeErr maps a failed PUT/DELETE: 412 is conflict with the current
// object (re-fetched) as *calendar.Object in Error.Data, which core
// turns into api.ConflictEventData; 403 readOnly; 404/410 eventGone.
func (p *Provider) writeErr(ctx context.Context, href string, err error) error {
	if !IsConflict(err) {
		return ToAPIError(err)
	}
	e := api.NewError(api.CodeConflict, "caldav: the event changed on the server")
	if o, gerr := p.c.Get(ctx, href); gerr == nil {
		if obj, perr := parseObject(o.Data, p.me(), ""); perr == nil {
			obj.RemoteID, obj.ETag = NormaliseHref(href), o.ETag
			e.Data = &obj
		}
	} else if IsGone(gerr) {
		return api.NewError(api.CodeEventGone, "caldav: the event is gone from the server")
	}
	return e
}

// load reads the resource of a stored event: its Raw when the row has it
// (master or single), else a GET (an override row). With a GET whose
// ETag is not ifMatch the answer is conflict at once.
func (p *Provider) load(ctx context.Context, ev calsync.StoredEvent, ifMatch string) (calendar.Object, error) {
	href := NormaliseHref(ev.RemoteID)
	if href == "" {
		return calendar.Object{}, api.NewError(api.CodeInvalidArgument, "caldav: event without a usable href")
	}
	data, etag := ev.Raw, ev.ETag
	if len(data) == 0 || ev.Kind == store.EventOverride {
		o, err := p.c.Get(ctx, href)
		if err != nil {
			return calendar.Object{}, ToAPIError(err)
		}
		data, etag = o.Data, o.ETag
		if ifMatch != "" && etag != "" && etag != ifMatch {
			e := api.NewError(api.CodeConflict, "caldav: the event changed on the server")
			if obj, err := parseObject(data, p.me(), ""); err == nil {
				obj.RemoteID, obj.ETag = href, etag
				e.Data = &obj
			}
			return calendar.Object{}, e
		}
	}
	obj, err := parseObject(data, p.me(), ev.UID)
	if err != nil {
		return calendar.Object{}, api.NewError(api.CodeMalformedCalendar, "caldav: stored event does not read")
	}
	obj.RemoteID, obj.ETag = href, etag
	return obj, nil
}

func ifMatchOf(ev calsync.StoredEvent, opts calsync.WriteOptions) string {
	if opts.IfMatch != "" {
		return opts.IfMatch
	}
	return ev.ETag
}

// instanceOf is the master's instance rid as an override would hold it.
func instanceOf(m *calendar.Event, rid api.DateOrTime) (calendar.Event, bool) {
	t, ok := calendar.ParseKey(rid)
	if m == nil || !ok || !calendar.HasInstance(m, rid) {
		return calendar.Event{}, false
	}
	ev := *m
	ev.Attendees = slices.Clone(m.Attendees)
	length := ev.End.Sub(ev.Start)
	ev.Kind, ev.RecurrenceID = calendar.KindOverride, rid
	ev.RRule, ev.RDates, ev.EXDates, ev.Recurrence = "", nil, nil, nil
	ev.Start, ev.End = t, t.Add(length)
	if ev.AllDay {
		ev.End = t.AddDate(0, 0, int(length.Hours()+12)/24)
		ev.StartDate, ev.EndDate = api.Date(ev.Start.Format(time.DateOnly)), api.Date(ev.End.Format(time.DateOnly))
	}
	return ev, true
}

// counterpart is the event of before that after replaces.
func counterpart(before calendar.Object, after *calendar.Event) *calendar.Event {
	if after.Kind == calendar.KindOverride {
		if o := before.Override(after.RecurrenceID); o != nil {
			return o
		}
		if inst, ok := instanceOf(before.Master, after.RecurrenceID); ok {
			return &inst
		}
		return nil
	}
	return before.Master
}

// bumpSequences sets each event of after to its predecessor's SEQUENCE,
// incremented when the change is significant (calendar.Significant).
func bumpSequences(before calendar.Object, after *calendar.Object) {
	for _, e := range after.Events() {
		prev := counterpart(before, e)
		if prev == nil {
			continue
		}
		seq := max(prev.Sequence, e.Sequence)
		if calendar.Significant(prev, e) {
			seq = prev.Sequence + 1
		}
		e.Sequence = seq
	}
}

// recipients are the iMIP recipients of method over the object's events.
func (p *Provider) recipients(method string, obj calendar.Object) []string {
	var out []string
	for _, e := range obj.Events() {
		for _, r := range imip.Recipients(method, nil, e, p.me()) {
			if !slices.Contains(out, r) {
				out = append(out, r)
			}
		}
	}
	return out
}

func (p *Provider) cancelRecipients(before calendar.Object) []string {
	var out []string
	for _, e := range before.Events() {
		for _, r := range imip.Recipients(imip.MethodCancel, e, nil, p.me()) {
			if !slices.Contains(out, r) {
				out = append(out, r)
			}
		}
	}
	return out
}

// organizes: the user organises the event and it has attendees.
func (p *Provider) organizes(obj calendar.Object) bool {
	for _, e := range obj.Events() {
		if e.Organizer != nil && (e.IAmOrganizer || p.isMe(e.Organizer.Address)) && len(e.Attendees) > 0 {
			return true
		}
	}
	return false
}

// plan is what a write sends.
type plan struct {
	server bool // the server schedules
	notify bool
	mail   bool // the daemon sends iMIP
}

func (p *Provider) planFor(ctx context.Context, opts calsync.WriteOptions) (plan, error) {
	pl := plan{server: p.serverSchedules(ctx), notify: opts.Notify}
	if !pl.server && pl.notify {
		if !p.canMail(opts) {
			return pl, api.NewError(api.CodeInvitationsUnavailable, "caldav: no mail account sends the invitations of this calendar")
		}
		pl.mail = true
	}
	return pl, nil
}

// organizerMarks are the marks of an organiser's PUT.
func (pl plan) organizerMarks() marks {
	switch {
	case !pl.server:
		return marks{attendees: agentClient}
	case pl.notify:
		return marks{attendees: agentServer}
	}
	return marks{attendees: agentClient}
}

func (pl plan) via(n int) api.InvitationsSent {
	switch {
	case n == 0 || !pl.notify:
		return api.InvitationsSent{Via: api.InvitationsViaNone}
	case pl.mail:
		return api.InvitationsSent{Via: api.InvitationsViaMail, Count: n}
	}
	return api.InvitationsSent{Via: api.InvitationsViaServer, Count: n}
}

// send hands the built messages to SendIMIP. A failure leaves the
// server's write in place: the result is returned together with the
// error (storageError), so that the engine can still store it.
func (p *Provider) send(ctx context.Context, msgs []IMIPMessage, opts calsync.WriteOptions) error {
	var keep []IMIPMessage
	for _, m := range msgs {
		if len(m.Recipients) > 0 && len(m.Data) > 0 {
			keep = append(keep, m)
		}
	}
	if len(keep) == 0 {
		return nil
	}
	if p.opts.SendIMIP == nil {
		return api.NewError(api.CodeInvitationsUnavailable, "caldav: no mail path for invitations")
	}
	if opts.MailAccountID == "" {
		opts.MailAccountID = p.mailAccount(opts)
	}
	if err := p.opts.SendIMIP(ctx, keep, opts); err != nil {
		p.log.Warn("caldav: iMIP not queued", "err", err)
		var ae *api.Error
		if errors.As(err, &ae) {
			return ae
		}
		return api.NewError(api.CodeStorageError, "caldav: iMIP not queued: %v", err)
	}
	return nil
}

func union(lists ...[]string) []string {
	var out []string
	for _, l := range lists {
		for _, x := range l {
			if !slices.Contains(out, x) {
				out = append(out, x)
			}
		}
	}
	return out
}

func (p *Provider) checkWritable(cal calsync.StoredCalendar) error {
	if cal.Access == api.CalendarAccessRead {
		return api.NewError(api.CodeReadOnly, "caldav: the calendar is read-only")
	}
	return nil
}

// Create PUTs a new object at <collection>/<UUID>.ics with
// If-None-Match: *. An event organised by someone else (an invitation
// stored in the calendar, D18) gets SCHEDULE-AGENT=CLIENT on its
// ORGANIZER and no mail: the daemon replies itself.
func (p *Provider) Create(ctx context.Context, cal calsync.StoredCalendar, ev calsync.Event, opts calsync.WriteOptions) (calsync.WriteResult, error) {
	if err := p.checkWritable(cal); err != nil {
		return calsync.WriteResult{}, err
	}
	id := NewUID()
	href := ObjectHref(cal.RemoteID, id)
	if ev.UID == "" {
		ev.UID = id
	}
	ev.RecurrenceID, ev.Sequence = "", 0
	if ev.RRule != "" || len(ev.RDates) > 0 {
		ev.Kind = calendar.KindMaster
	} else {
		ev.Kind = calendar.KindSingle
	}
	foreign := ev.Organizer != nil && ev.Organizer.Address != "" && !ev.IAmOrganizer && !p.isMe(ev.Organizer.Address)
	obj := calendar.Object{Master: &ev}
	if foreign {
		written, err := p.put(ctx, href, obj, marks{organizer: agentClient}, "")
		if err != nil {
			return calsync.WriteResult{}, err
		}
		return calsync.WriteResult{Objects: []calendar.Object{written}, Invitations: api.InvitationsSent{Via: api.InvitationsViaNone}}, nil
	}
	org := p.identity()
	if len(ev.Attendees) > 0 {
		if ev.Organizer == nil || ev.Organizer.Address == "" {
			ev.Organizer = &org
		} else {
			org = *ev.Organizer
		}
		ev.IAmOrganizer = true
	}
	rcpt := p.recipients(imip.MethodRequest, obj)
	var pl plan
	if len(rcpt) > 0 {
		var err error
		if pl, err = p.planFor(ctx, opts); err != nil {
			return calsync.WriteResult{}, err
		}
	}
	m := marks{}
	if len(rcpt) > 0 {
		m = pl.organizerMarks()
	}
	written, err := p.put(ctx, href, obj, m, "")
	if err != nil {
		return calsync.WriteResult{}, err
	}
	res := calsync.WriteResult{Objects: []calendar.Object{written}, Invitations: pl.via(len(rcpt))}
	if pl.mail && len(rcpt) > 0 {
		data, berr := imip.BuildRequest(written, org, p.now())
		if berr != nil {
			return res, api.NewError(api.CodeInvalidArgument, "caldav: invitation does not build: %v", berr)
		}
		return res, p.send(ctx, []IMIPMessage{{Method: imip.MethodRequest, UID: written.UID(), Recipients: rcpt, Data: data}}, opts)
	}
	return res, nil
}

func scopeErr(err error) error {
	if errors.Is(err, calendar.ErrScope) {
		return api.NewError(api.CodeInvalidArgument, "caldav: scope not possible for this event")
	}
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae
	}
	return api.NewError(api.CodeInvalidArgument, "caldav: %v", err)
}

// Update rewrites the resource of ev from its stored bytes (unknown
// properties survive) with the change applied in its scope (D19):
// this = an override, thisAndFuture = the series cut with UNTIL before
// the instance and a new series (new UID, new href) from it, all = the
// master. SEQUENCE is incremented on a significant change.
func (p *Provider) Update(ctx context.Context, cal calsync.StoredCalendar, ev calsync.StoredEvent, change calsync.Change, opts calsync.WriteOptions) (calsync.WriteResult, error) {
	if err := p.checkWritable(cal); err != nil {
		return calsync.WriteResult{}, err
	}
	etag := ifMatchOf(ev, opts)
	obj, err := p.load(ctx, ev, etag)
	if err != nil {
		return calsync.WriteResult{}, err
	}
	if change.RecurrenceID == "" {
		change.RecurrenceID = opts.RecurrenceID
	}
	newUID := NewUID()
	updated, split, dropped, err := calendar.ApplyChange(obj, change, newUID)
	if err != nil {
		return calsync.WriteResult{}, scopeErr(err)
	}
	bumpSequences(obj, &updated)
	organizes := p.organizes(obj) || p.organizes(updated)
	var pl plan
	m := marks{}
	if organizes {
		if pl, err = p.planFor(ctx, opts); err != nil {
			return calsync.WriteResult{}, err
		}
		m = pl.organizerMarks()
	}

	res := calsync.WriteResult{DroppedExceptions: dropped}
	var splitWritten *calendar.Object
	if split != nil {
		so := *split
		if so.Master != nil && so.Master.Organizer == nil && obj.Master != nil {
			so.Master.Organizer, so.Master.IAmOrganizer = obj.Master.Organizer, obj.Master.IAmOrganizer
		}
		w, err := p.put(ctx, ObjectHref(cal.RemoteID, newUID), so, m, "")
		if err != nil {
			return calsync.WriteResult{}, err
		}
		splitWritten = &w
	}
	written, err := p.put(ctx, obj.RemoteID, updated, m, etag)
	if err != nil {
		if splitWritten != nil {
			if derr := p.c.Delete(ctx, splitWritten.RemoteID, splitWritten.ETag); derr != nil {
				p.log.Warn("caldav: split series left behind", "err", ToAPIError(derr))
			}
		}
		return calsync.WriteResult{}, err
	}
	res.Objects = append(res.Objects, written)
	if splitWritten != nil {
		res.Objects = append(res.Objects, *splitWritten)
	}
	if !organizes {
		res.Invitations = api.InvitationsSent{Via: api.InvitationsViaNone}
		return res, nil
	}

	org := p.organizerOf(obj)
	var msgs []IMIPMessage
	rcpt := p.recipients(imip.MethodRequest, written)
	if len(rcpt) > 0 && pl.mail {
		if data, err := imip.BuildRequest(written, org, p.now()); err == nil {
			msgs = append(msgs, IMIPMessage{Method: imip.MethodRequest, UID: written.UID(), Recipients: rcpt, Data: data})
		}
	}
	var splitRcpt []string
	if splitWritten != nil {
		splitRcpt = p.recipients(imip.MethodRequest, *splitWritten)
		if len(splitRcpt) > 0 && pl.mail {
			if data, err := imip.BuildRequest(*splitWritten, org, p.now()); err == nil {
				msgs = append(msgs, IMIPMessage{Method: imip.MethodRequest, UID: splitWritten.UID(), Recipients: splitRcpt, Data: data})
			}
		}
	}
	// Attendees removed from the series get a CANCEL.
	var removed []string
	if obj.Master != nil && written.Master != nil {
		if gone := imip.RemovedAttendees(obj.Master, written.Master); len(gone) > 0 {
			before := *obj.Master
			before.Attendees = gone
			removed = imip.Recipients(imip.MethodCancel, &before, nil, p.me())
			if len(removed) > 0 && pl.mail {
				c := calendar.Object{Master: &before}
				if data, err := imip.BuildCancel(c, org, api.EditScopeAll, "", p.now()); err == nil {
					msgs = append(msgs, IMIPMessage{Method: imip.MethodCancel, UID: written.UID(), Recipients: removed, Data: data})
				}
			}
		}
	}
	res.Invitations = pl.via(len(union(rcpt, splitRcpt, removed)))
	if pl.mail {
		return res, p.send(ctx, msgs, opts)
	}
	return res, nil
}

func (p *Provider) organizerOf(obj calendar.Object) api.Address {
	for _, e := range obj.Events() {
		if e.Organizer != nil && e.Organizer.Address != "" {
			return *e.Organizer
		}
	}
	return p.identity()
}

// Delete removes the resource (scope all, or thisAndFuture at the first
// instance) with If-Match, or rewrites it with an EXDATE (this) or the
// series cut (thisAndFuture). An organiser's delete with attendees and
// Notify cancels for them: the server, or a CANCEL (a REQUEST of the cut
// series for thisAndFuture) by iMIP. An attendee's delete sends nothing
// (core declines first through Respond when asked).
func (p *Provider) Delete(ctx context.Context, cal calsync.StoredCalendar, ev calsync.StoredEvent, scope calsync.Scope, opts calsync.WriteOptions) (calsync.WriteResult, error) {
	if err := p.checkWritable(cal); err != nil {
		return calsync.WriteResult{}, err
	}
	etag := ifMatchOf(ev, opts)
	obj, err := p.load(ctx, ev, etag)
	if err != nil {
		return calsync.WriteResult{}, err
	}
	rid := opts.RecurrenceID
	if rid == "" && ev.Kind == store.EventOverride {
		rid = api.DateOrTime(ev.RecurrenceID)
	}
	updated, err := calendar.ApplyDelete(obj, scope, rid)
	if err != nil {
		return calsync.WriteResult{}, scopeErr(err)
	}
	organizes := p.organizes(obj)
	var pl plan
	if organizes {
		if pl, err = p.planFor(ctx, opts); err != nil {
			return calsync.WriteResult{}, err
		}
	}
	org := p.organizerOf(obj)
	var res calsync.WriteResult
	var msgs []IMIPMessage
	var rcpt []string
	if updated == nil {
		if err := p.c.Delete(ctx, obj.RemoteID, etag); err != nil {
			return calsync.WriteResult{}, p.writeErr(ctx, obj.RemoteID, err)
		}
		res.Removed = []string{obj.RemoteID}
		if organizes {
			rcpt = p.cancelRecipients(obj)
			if pl.mail && len(rcpt) > 0 {
				if data, err := imip.BuildCancel(obj, org, api.EditScopeAll, "", p.now()); err == nil {
					msgs = append(msgs, IMIPMessage{Method: imip.MethodCancel, UID: obj.UID(), Recipients: rcpt, Data: data})
				}
			}
		}
	} else {
		bumpSequences(obj, updated)
		if updated.Master != nil && obj.Master != nil && updated.Master.Sequence == obj.Master.Sequence {
			updated.Master.Sequence++
		}
		m := marks{}
		if organizes {
			m = pl.organizerMarks()
		}
		written, err := p.put(ctx, obj.RemoteID, *updated, m, etag)
		if err != nil {
			return calsync.WriteResult{}, err
		}
		res.Objects = []calendar.Object{written}
		if organizes {
			if scope == api.EditScopeThisAndFuture {
				rcpt = p.recipients(imip.MethodRequest, written)
				if pl.mail && len(rcpt) > 0 {
					if data, err := imip.BuildRequest(written, org, p.now()); err == nil {
						msgs = append(msgs, IMIPMessage{Method: imip.MethodRequest, UID: written.UID(), Recipients: rcpt, Data: data})
					}
				}
			} else {
				inst := obj.Override(rid)
				if inst == nil {
					if i, ok := instanceOf(obj.Master, rid); ok {
						inst = &i
					}
				}
				if inst != nil {
					rcpt = imip.Recipients(imip.MethodCancel, inst, nil, p.me())
				}
				if pl.mail && len(rcpt) > 0 {
					if data, err := imip.BuildCancel(obj, org, api.EditScopeThis, rid, p.now()); err == nil {
						msgs = append(msgs, IMIPMessage{Method: imip.MethodCancel, UID: obj.UID(), Recipients: rcpt, Data: data})
					}
				}
			}
		}
	}
	if !organizes {
		res.Invitations = api.InvitationsSent{Via: api.InvitationsViaNone}
		return res, nil
	}
	res.Invitations = pl.via(len(rcpt))
	if pl.mail {
		return res, p.send(ctx, msgs, opts)
	}
	return res, nil
}

// hasClientOrganizer reports an ORGANIZER with SCHEDULE-AGENT=CLIENT in
// the raw resource (an invitation stored by the daemon, D18).
func hasClientOrganizer(raw []byte) bool {
	parsed, err := ics.Parse(raw)
	if err != nil {
		return false
	}
	for _, c := range parsed.Calendar().Children("VEVENT") {
		if o := c.Property("ORGANIZER"); o != nil && strings.EqualFold(o.Param("SCHEDULE-AGENT"), "CLIENT") {
			return true
		}
	}
	return false
}

func setMine(e *calendar.Event, r api.EventResponse) bool {
	found := false
	for i := range e.Attendees {
		if e.Attendees[i].Me {
			e.Attendees[i].Response, found = r, true
		}
	}
	if found {
		e.MyResponse = r
	}
	return found
}

// Respond writes the user's PARTSTAT (scope all: the series and its
// overrides; this: the instance, as an override when it has none). The
// reply goes by the server when it schedules and the organiser has no
// SCHEDULE-AGENT=CLIENT; otherwise the daemon sends an iMIP REPLY to the
// ORGANIZER and the PUT marks the ORGANIZER SCHEDULE-AGENT=CLIENT (also
// without Notify, so that the server sends nothing).
func (p *Provider) Respond(ctx context.Context, cal calsync.StoredCalendar, ev calsync.StoredEvent, r calsync.Response, opts calsync.WriteOptions) (calsync.WriteResult, error) {
	switch r.Response {
	case api.ResponseAccepted, api.ResponseTentative, api.ResponseDeclined:
	default:
		return calsync.WriteResult{}, api.NewError(api.CodeInvalidArgument, "caldav: bad response")
	}
	if r.ProposedStart != nil || r.ProposedEnd != nil {
		return calsync.WriteResult{}, calsync.ErrUnsupported
	}
	etag := ifMatchOf(ev, opts)
	obj, err := p.load(ctx, ev, etag)
	if err != nil {
		return calsync.WriteResult{}, err
	}
	raw := obj.Raw
	rid := opts.RecurrenceID
	if rid == "" && ev.Kind == store.EventOverride {
		rid = api.DateOrTime(ev.RecurrenceID)
	}
	updated := obj
	updated.Overrides = slices.Clone(obj.Overrides)
	for i := range updated.Overrides {
		updated.Overrides[i].Attendees = slices.Clone(updated.Overrides[i].Attendees)
	}
	if obj.Master != nil {
		m := *obj.Master
		m.Attendees = slices.Clone(m.Attendees)
		updated.Master = &m
	}
	var target *calendar.Event
	if r.Scope == api.EditScopeThis && rid != "" {
		if o := updated.Override(rid); o != nil {
			target = o
		} else if inst, ok := instanceOf(updated.Master, rid); ok {
			updated.Overrides = append(updated.Overrides, inst)
			target = &updated.Overrides[len(updated.Overrides)-1]
		} else {
			return calsync.WriteResult{}, api.NewError(api.CodeEventGone, "caldav: the instance is not in the series")
		}
		if !setMine(target, r.Response) {
			return calsync.WriteResult{}, api.NewError(api.CodeInvalidArgument, "caldav: the user is not an attendee")
		}
	} else {
		any := false
		for _, e := range updated.Events() {
			if setMine(e, r.Response) {
				any = true
				if target == nil {
					target = e
				}
			}
		}
		if !any {
			return calsync.WriteResult{}, api.NewError(api.CodeInvalidArgument, "caldav: the user is not an attendee")
		}
	}
	server := p.serverSchedules(ctx) && !hasClientOrganizer(raw)
	pl := plan{server: server, notify: opts.Notify}
	m := marks{}
	if !server || !opts.Notify {
		m.organizer = agentClient
	}
	if !server && opts.Notify {
		if !p.canMail(opts) {
			return calsync.WriteResult{}, api.NewError(api.CodeInvitationsUnavailable, "caldav: no mail account sends the reply")
		}
		if target.Organizer == nil || target.Organizer.Address == "" {
			return calsync.WriteResult{}, api.NewError(api.CodeInvalidArgument, "caldav: the event has no organiser to reply to")
		}
		pl.mail = true
	}
	written, err := p.put(ctx, obj.RemoteID, updated, m, etag)
	if err != nil {
		return calsync.WriteResult{}, err
	}
	res := calsync.WriteResult{Objects: []calendar.Object{written}, Invitations: pl.via(1)}
	if !pl.mail {
		return res, nil
	}
	data, err := p.reply(*target, r.Response, opts.Comment)
	if err != nil {
		return res, api.NewError(api.CodeInvalidArgument, "caldav: reply does not build: %v", err)
	}
	rcpt := imip.Recipients(imip.MethodReply, nil, target, p.me())
	return res, p.send(ctx, []IMIPMessage{{Method: imip.MethodReply, UID: written.UID(), Recipients: rcpt, Data: data}}, opts)
}

// reply builds the REPLY of one event (the series or an instance).
func (p *Provider) reply(ev calendar.Event, r api.EventResponse, comment string) ([]byte, error) {
	obj := calendar.Object{Master: &ev}
	if ev.Kind == calendar.KindOverride {
		obj = calendar.Object{Overrides: []calendar.Event{ev}}
	}
	data, err := calendar.ToICS(obj, calendar.WriteOptions{Now: p.now().UTC(), IgnoreRaw: true})
	if err != nil {
		return nil, err
	}
	inv, err := imip.ParseMessage(data, p.me())
	if err != nil {
		return nil, err
	}
	if inv.Me == nil {
		return nil, fmt.Errorf("caldav: the user is not an attendee")
	}
	return imip.BuildReply(inv, "", r, comment, p.now())
}
