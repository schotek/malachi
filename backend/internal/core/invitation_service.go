// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/calendar/imip"
	"github.com/schotek/malachi/backend/internal/calsync"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Invitations received in mail (docs/calendar.md §3.8, §4.6): detection in
// the Stored hook (D17), the card of message.get, invitation.list,
// invitation.respond (D18) and invitation.applyCancel. Every string of a
// calendar part is hostile: imip.ParseMessage cleans what it keeps, and
// the card carries nothing else of the part.

// maxInvitationParts is how many calendar parts of a message are read
// (D17); the first that reads is the message's invitation.
const maxInvitationParts = 2

// maxInvitationTargets caps the "Add to:" list of a card.
const maxInvitationTargets = 50

// maxInvitationDayBusy caps the busy spans of a card's day strip.
const maxInvitationDayBusy = 100

// invitationsChangedDelay gathers notify.invitationsChanged (§4.7: within
// 1 s); a variable so that tests can shorten it.
var invitationsChangedDelay = time.Second

// invitationProviders, when set, makes the calendar source of an account
// for invitation.respond and invitation.applyCancel instead of the
// running engine's or calendarProvider (tests).
var invitationProviders func(ctx context.Context, a store.Account) (calsync.Provider, error)

type invitationService struct{ b *Backend }

func (b *Backend) Invitations() api.InvitationService { return &invitationService{b: b} }

// --- detection (the Stored hook, D17) ----------------------------------------

// noteInvitation reads the calendar part of a message the mail syncer has
// just stored, synchronously before notify.newMessage: the row of
// cal_invitations (the pill, the card), a received REPLY applied under
// D10, a CANCEL marked on the daemon's own copy, the board told.
func (b *Backend) noteInvitation(ctx context.Context, accountID, messageID string) {
	a, err := b.store.GetAccount(ctx, accountID)
	if err != nil {
		return
	}
	m, err := b.store.GetMessage(ctx, accountID, messageID)
	if err != nil {
		return
	}
	res, err := b.readInvitation(ctx, a, m)
	if err != nil {
		if !isCancelled(err) {
			b.log.Warn("invitation of a message", "account", accountID, "message", messageID, "err", err)
		}
		return
	}
	b.invitationEffects(res)
}

// invitationRead is what readInvitation did.
type invitationRead struct {
	accountID string
	wrote     bool // a row was written (or removed)
	// unanswered: the change may alter the unanswered invitations (a
	// request or a cancel came or went).
	unanswered bool
	// calendars changed by a reply or a cancel, by account.
	calendars map[string][]string
}

// invitationEffects sends what a read changed: notify.invitationsChanged
// (gathered) and notify.calendarChanged for the events a reply or a cancel
// marked.
func (b *Backend) invitationEffects(res invitationRead) {
	if res.wrote {
		b.wakeBoard()
	}
	if res.unanswered {
		b.invitationsChanged(res.accountID)
	}
	for acc, cals := range res.calendars {
		b.invitationCalendarChanged(acc, cals)
	}
}

// invitationMailAccount: a mail account whose messages may carry
// invitations (imap, graph); not an issue tracker nor a calendar.
func invitationMailAccount(a store.Account) bool {
	switch a.Config.Protocol() {
	case api.AccountIMAP, api.AccountGraph:
		return true
	}
	return false
}

// calendarParts are the calendar parts of a message's attachments list,
// at most maxInvitationParts. A calendar part inside an attached message
// (message/rfc822) is not in the list: the MIME walk does not descend into
// an attached message, which is a leaf.
func calendarParts(atts []api.Attachment) []api.Attachment {
	var out []api.Attachment
	for _, att := range atts {
		if strings.Contains(att.PartID, ".") && nestedInMessage(atts, att.PartID) {
			continue
		}
		if store.IsCalendarPart(att.ContentType, att.Filename) {
			out = append(out, att)
			if len(out) == maxInvitationParts {
				break
			}
		}
	}
	return out
}

// nestedInMessage reports whether part id lies under a message/rfc822 part
// of the list (defensive: Parse never lists one).
func nestedInMessage(atts []api.Attachment, id string) bool {
	for _, a := range atts {
		if strings.EqualFold(a.ContentType, "message/rfc822") && strings.HasPrefix(id, a.PartID+".") {
			return true
		}
	}
	return false
}

// readInvitation makes the message's cal_invitations row what its
// calendar parts say now (or removes it when it has none), with the D10
// and cancel effects; it notifies nothing (invitationEffects).
func (b *Backend) readInvitation(ctx context.Context, a store.Account, m store.Message) (invitationRead, error) {
	out := invitationRead{accountID: a.ID}
	if !invitationMailAccount(a) {
		return out, nil
	}
	old, err := b.store.GetInvitation(ctx, m.ID)
	had := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return out, err
	}
	parts := calendarParts(m.Attachments)
	if len(parts) == 0 {
		if had {
			if err := b.store.DeleteInvitation(ctx, m.ID); err != nil {
				return out, err
			}
			out.wrote = true
			out.unanswered = old.Method == api.InvitationMethodRequest || old.Method == api.InvitationMethodCancel
			return out, b.store.MarkBoardMessagesDirty(ctx, []string{m.ID})
		}
		return out, nil
	}
	if m.BodyState != store.BodyFetched {
		return out, nil // the hook runs again once the body is stored
	}
	row := store.Invitation{MessageID: m.ID, AccountID: a.ID, PartID: parts[0].PartID,
		Method: api.InvitationMethodOther, ParserVersion: calendar.ParserVersion}
	me := b.invitationMe(ctx, a)
	from := invitationFrom(m)
	var inv imip.Invitation
	parsed := false
	for _, p := range parts {
		data, err := b.invitationPartData(ctx, m, p)
		if err != nil {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			continue
		}
		got, err := imip.ParseMessage(data, me)
		if err != nil {
			continue
		}
		inv, parsed, row.PartID = got, true, p.PartID
		break
	}
	if parsed {
		role := b.invitationFolderRole(ctx, a.ID, m.FolderID)
		card := inv.Card(row.PartID)
		card.OrganizerMismatch = inv.OrganizerMismatch(from)
		card.OwnEvent = inv.Organizer != nil && slices.Contains(me, strings.ToLower(inv.Organizer.Address))
		quarantined := role == api.RoleJunk || role == api.RoleTrash
		// A row read again (a newer ParserVersion) of the same iTIP message
		// is not applied again: the event may have moved on since. The
		// reply keeps what it said then.
		reread := had && len(old.Card) > 0 && old.UID == inv.UID && old.Method == inv.Method
		switch {
		case quarantined:
		case reread:
			if inv.Method == api.InvitationMethodReply {
				var was api.InvitationCard
				if json.Unmarshal(old.Card, &was) == nil {
					card.ReplyApplied = was.ReplyApplied
				}
			}
		case inv.Method == api.InvitationMethodReply:
			cals, applied, err := b.applyInvitationReply(ctx, &inv, from)
			if err != nil {
				return out, err
			}
			card.ReplyApplied = applied
			out.calendars = cals
		case inv.Method == api.InvitationMethodCancel:
			cals, err := b.applyInvitationCancel(ctx, a, &inv)
			if err != nil {
				return out, err
			}
			out.calendars = cals
		}
		fillInvitationRow(&row, &inv)
		static := staticInvitationCard(card)
		if inv.Method == api.InvitationMethodReply {
			// A reply card's response is the attendee's answer (ReplyFrom).
			static.Response = inv.ReplyResponse
		}
		raw, err := json.Marshal(static)
		if err != nil {
			return out, err
		}
		row.Card = raw
	}
	if err := b.store.PutInvitation(ctx, row); err != nil {
		return out, err
	}
	out.wrote = true
	out.unanswered = row.Method == api.InvitationMethodRequest || row.Method == api.InvitationMethodCancel ||
		had && (old.Method == api.InvitationMethodRequest || old.Method == api.InvitationMethodCancel)
	// No trigger joins cal_invitations to the board (docs/calendar.md
	// §7 Q1): the threads of the message are judged again here.
	return out, b.store.MarkBoardMessagesDirty(ctx, []string{m.ID})
}

// fillInvitationRow copies the columns of cal_invitations from a parsed
// part: times UTC, an all-day or floating one as its wall clock as if UTC.
func fillInvitationRow(row *store.Invitation, inv *imip.Invitation) {
	ev := &inv.Event
	row.Method, row.UID, row.RecurrenceID = inv.Method, inv.UID, string(inv.RecurrenceID)
	row.Sequence, row.DTStamp, row.Title, row.AllDay = inv.Sequence, inv.DTStamp, ev.Title, ev.AllDay
	if inv.Organizer != nil {
		row.Organizer = strings.ToLower(inv.Organizer.Address)
	}
	if !ev.Start.IsZero() {
		row.StartAt, row.EndAt = ev.Start.UTC(), ev.End.UTC()
	}
}

// staticInvitationCard is what card_json keeps: the card without the
// fields message.get derives (state, response, conflicts, the day,
// targets, the reply path).
func staticInvitationCard(c api.InvitationCard) api.InvitationCard {
	c.State = ""
	c.Response, c.RespondedAt, c.EventID, c.CalendarID, c.Previous = "", nil, "", "", nil
	c.Conflicts, c.Targets, c.Day = []api.InvitationConflict{}, []api.InvitationTarget{}, api.InvitationDay{Busy: []api.DayBusy{}}
	c.FixedTarget, c.CanRespond, c.CanProposeNewTime = false, false, false
	c.ReplyVia, c.ReplyAccountID, c.ReplyTo = "", "", nil
	return c
}

// invitationPartData reads one calendar part of a stored message, at most
// api.MaxICSBytes (larger is an error: the row is then invalid).
func (b *Backend) invitationPartData(ctx context.Context, m store.Message, p api.Attachment) ([]byte, error) {
	if p.Size > api.MaxICSBytes {
		return nil, api.NewError(api.CodeMalformedCalendar, "calendar part over %d bytes", api.MaxICSBytes)
	}
	part, err := b.extractPart(ctx, m, p.PartID)
	if err != nil {
		return nil, err
	}
	if len(part.Body) > api.MaxICSBytes {
		return nil, api.NewError(api.CodeMalformedCalendar, "calendar part over %d bytes", api.MaxICSBytes)
	}
	return part.Body, nil
}

// invitationMe are the user's addresses for an invitation of account a:
// its own and every enabled account's (userAddresses), lower case.
func (b *Backend) invitationMe(ctx context.Context, a store.Account) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		s = strings.ToLower(strings.TrimSpace(s))
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	add(a.Email)
	add(a.Config.Email)
	for _, s := range b.userAddresses(ctx) {
		add(s)
	}
	return out
}

func invitationFrom(m store.Message) string {
	if len(m.From) == 0 {
		return ""
	}
	return m.From[0].Address
}

func (b *Backend) invitationFolderRole(ctx context.Context, accountID, folderID string) api.FolderRole {
	f, err := b.store.GetFolder(ctx, accountID, folderID)
	if err != nil {
		return ""
	}
	return f.Role
}

// calendarAccounts are the enabled accounts whose calendars are stored.
func (b *Backend) invitationCalendarAccounts(ctx context.Context) ([]store.Account, error) {
	accounts, err := b.store.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	var out []store.Account
	for _, a := range accounts {
		if calsync.HasCalendars(a) {
			out = append(out, a)
		}
	}
	return out, nil
}

// applyInvitationReply is D10: a received REPLY updates the response of
// its attendee in the stored events of the UID, only when the mail's From
// is that attendee, the reply names exactly one attendee, and the event
// has the attendee (none is ever added) and no newer SEQUENCE. The rows
// change in the store only; the next pass brings the server's view.
func (b *Backend) applyInvitationReply(ctx context.Context, inv *imip.Invitation, from string) (map[string][]string, bool, error) {
	if !inv.ReplyMatchesSender(from) || inv.ReplyAttendees != 1 || inv.ReplyResponse == "" || inv.UID == "" {
		return nil, false, nil
	}
	accounts, err := b.invitationCalendarAccounts(ctx)
	if err != nil {
		return nil, false, err
	}
	changed := map[string][]string{}
	applied := false
	for _, acc := range accounts {
		for _, res := range invitationResources(b.store.EventsByUID(ctx, acc.ID, inv.UID)) {
			rows, err := b.store.EventsByRemoteID(ctx, res.calendarID, res.remoteID)
			if err != nil {
				return nil, false, err
			}
			i := invitationTargetRow(rows, inv.RecurrenceID)
			if i < 0 || rows[i].Sequence > inv.Sequence {
				continue
			}
			k := slices.IndexFunc(rows[i].Attendees, func(at api.Attendee) bool {
				return strings.EqualFold(at.Address, inv.ReplyFrom.Address)
			})
			if k < 0 {
				continue
			}
			applied = true
			if rows[i].Attendees[k].Response == inv.ReplyResponse {
				continue
			}
			rows[i].Attendees = slices.Clone(rows[i].Attendees)
			rows[i].Attendees[k].Response = inv.ReplyResponse
			if _, err := b.store.ReplaceResource(ctx, res.calendarID, res.remoteID, rows, nil); err != nil {
				return nil, false, err
			}
			changed[acc.ID] = append(changed[acc.ID], res.calendarID)
		}
	}
	return changed, applied, nil
}

type invitationResource struct{ calendarID, remoteID string }

// invitationResources groups event rows by their resource, in order.
func invitationResources(rows []store.Event, err error) []invitationResource {
	if err != nil {
		return nil
	}
	var out []invitationResource
	for _, r := range rows {
		k := invitationResource{r.CalendarID, r.RemoteID}
		if !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	return out
}

// invitationTargetRow is the row of a resource an iTIP message names: the
// override of its recurrence id, else (no recurrence id) the master or
// single row; -1 when there is none (an instance without an override is
// not marked: its occurrence belongs to the master).
func invitationTargetRow(rows []store.Event, rid api.DateOrTime) int {
	if rid != "" {
		key := calsync.StoredKey(rid)
		return slices.IndexFunc(rows, func(r store.Event) bool { return r.Kind == store.EventOverride && r.RecurrenceID == key })
	}
	return slices.IndexFunc(rows, func(r store.Event) bool { return r.Kind != store.EventOverride })
}

// applyInvitationCancel marks cancelled the stored event a CANCEL names
// when it came from this invitation chain (the copy the daemon stored for
// the invitation, or an event of the same organiser on a CalDAV account
// the daemon schedules for) and its server does not process cancellations
// itself (Graph, CalDAV with server scheduling). It never deletes
// (invitation.applyCancel does, on the user's word).
func (b *Backend) applyInvitationCancel(ctx context.Context, a store.Account, inv *imip.Invitation) (map[string][]string, error) {
	if inv.UID == "" || inv.Organizer == nil {
		return nil, nil
	}
	type cand struct {
		acc store.Account
		res invitationResource
	}
	var cands []cand
	for _, rid := range []string{string(inv.RecurrenceID), ""} {
		r, err := b.store.GetResponse(ctx, a.ID, inv.UID, rid)
		if err != nil || r.EventID == "" || (r.Via != "imip" && r.Via != "none") {
			continue
		}
		ev, err := b.store.GetEvent(ctx, r.EventID)
		if err != nil {
			continue
		}
		acc, err := b.store.GetAccount(ctx, ev.AccountID)
		if err != nil {
			continue
		}
		cands = append(cands, cand{acc, invitationResource{ev.CalendarID, ev.RemoteID}})
		if rid == "" {
			break
		}
	}
	accounts, err := b.invitationCalendarAccounts(ctx)
	if err != nil {
		return nil, err
	}
	for _, acc := range accounts {
		if acc.Config.Protocol() != api.AccountCalDAV || b.invitationServerSchedules(acc) {
			continue
		}
		rows, err := b.store.EventsByUID(ctx, acc.ID, inv.UID)
		if err != nil {
			return nil, err
		}
		for _, res := range invitationResources(rows, nil) {
			i := slices.IndexFunc(rows, func(r store.Event) bool {
				return r.CalendarID == res.calendarID && r.RemoteID == res.remoteID && r.Organizer != nil &&
					strings.EqualFold(r.Organizer.Address, inv.Organizer.Address) && !r.IAmOrganizer
			})
			if i >= 0 && !slices.Contains(cands, cand{acc, res}) {
				cands = append(cands, cand{acc, res})
			}
		}
	}
	changed := map[string][]string{}
	for _, c := range cands {
		rows, err := b.store.EventsByRemoteID(ctx, c.res.calendarID, c.res.remoteID)
		if err != nil {
			return nil, err
		}
		var idx []int
		if inv.RecurrenceID == "" {
			for i := range rows {
				idx = append(idx, i)
			}
		} else if i := invitationTargetRow(rows, inv.RecurrenceID); i >= 0 {
			idx = []int{i}
		}
		touched := false
		for _, i := range idx {
			if rows[i].Sequence > inv.Sequence || rows[i].Status == api.EventStatusCancelled {
				continue
			}
			rows[i].Status, touched = api.EventStatusCancelled, true
		}
		if !touched {
			continue
		}
		if _, err := b.store.ReplaceResource(ctx, c.res.calendarID, c.res.remoteID, rows, nil); err != nil {
			return nil, err
		}
		changed[c.acc.ID] = append(changed[c.acc.ID], c.res.calendarID)
	}
	return changed, nil
}

// invitationServerSchedules reports whether a CalDAV account's server
// sends invitations and replies itself (D8): the account's override, else
// what its running provider found; unknown counts as the server.
func (b *Backend) invitationServerSchedules(a store.Account) bool {
	if a.Config.CalDAV != nil {
		switch a.Config.CalDAV.Scheduling {
		case api.CalDAVSchedulingServer:
			return true
		case api.CalDAVSchedulingClient:
			return false
		}
	}
	if b.calSync != nil {
		if p, ok := b.calSync.Provider(a.ID); ok {
			return p.Features().Invitations == api.InvitationsViaServer
		}
	}
	return true
}

// --- notifications ----------------------------------------------------------

// invitationBatches gathers notify.invitationsChanged per backend.
var invitationBatches sync.Map // *Backend → *invitationBatch

type invitationBatch struct {
	mu       sync.Mutex
	accounts []api.AccountID
	all      bool
	timer    *time.Timer
}

// invitationsChanged announces, gathered within invitationsChangedDelay,
// that the unanswered invitations of the account changed ("" = any).
func (b *Backend) invitationsChanged(accountID string) {
	v, _ := invitationBatches.LoadOrStore(b, &invitationBatch{})
	batch := v.(*invitationBatch)
	batch.mu.Lock()
	defer batch.mu.Unlock()
	if accountID == "" {
		batch.all = true
	} else if !slices.Contains(batch.accounts, api.AccountID(accountID)) {
		batch.accounts = append(batch.accounts, api.AccountID(accountID))
	}
	if batch.timer != nil {
		return
	}
	batch.timer = time.AfterFunc(invitationsChangedDelay, func() {
		batch.mu.Lock()
		n := api.InvitationsChangedNotification{AccountIDs: batch.accounts}
		if batch.all {
			n.AccountIDs = nil
		}
		batch.accounts, batch.all, batch.timer = nil, false, nil
		batch.mu.Unlock()
		if cn, ok := b.getNotifier().(api.CalendarNotifier); ok {
			cn.InvitationsChanged(n)
		}
	})
}

// invitationCalendarChanged sends notify.calendarChanged for calendars
// whose stored events an invitation changed outside a provider write.
func (b *Backend) invitationCalendarChanged(accountID string, calendarIDs []string) {
	cn, ok := b.getNotifier().(api.CalendarNotifier)
	if !ok {
		return
	}
	n := api.CalendarChangedNotification{AccountIDs: []api.AccountID{api.AccountID(accountID)}}
	for _, c := range calendarIDs {
		if !slices.Contains(n.CalendarIDs, api.CalendarID(c)) {
			n.CalendarIDs = append(n.CalendarIDs, api.CalendarID(c))
		}
	}
	cn.CalendarChanged(n)
}

// --- the card (message.get, message.download) -------------------------------

// invitationLink is the stored event an invitation is answered through
// (D18), or the copy the daemon stored for it (copy).
type invitationLink struct {
	acc  store.Account
	cal  store.Calendar
	ev   store.Event // the row the invitation names (an override, else the master or single)
	copy bool
}

// findInvitationLink finds the event of an invitation: the copy the
// daemon stored (cal_responses), else an event of the UID in the
// account's calendars, else in the user's other calendar accounts — one
// the user does not organise, of the invitation's organiser, so that an
// invitation reusing the UID of the user's own event answers nothing.
func (b *Backend) findInvitationLink(ctx context.Context, a store.Account, row store.Invitation, resp *store.Response) *invitationLink {
	if row.UID == "" {
		return nil
	}
	var own struct {
		OwnEvent bool `json:"ownEvent"`
	}
	_ = json.Unmarshal(row.Card, &own) // an unreadable card links as not own
	if resp != nil && resp.EventID != "" && (resp.Via == "imip" || resp.Via == "none") {
		if ev, err := b.store.GetEvent(ctx, resp.EventID); err == nil {
			if l := b.invitationLinkOf(ctx, ev, true); l != nil {
				return l
			}
		}
	}
	// The same rule as the summaries' answered-elsewhere lookup
	// (store.InvitationLinkedEvent), so that card, pill and list agree.
	found, ok, err := b.store.InvitationLinkedEvent(ctx, a.ID, row.UID, store.InvitationRecurrenceKey(row.RecurrenceID), row.Organizer, own.OwnEvent)
	if err != nil || !ok {
		return nil
	}
	ev, err := b.store.GetEvent(ctx, found.ID) // with Raw, for a write through it
	if err != nil {
		return nil
	}
	if l := b.invitationLinkOf(ctx, ev, false); l != nil {
		return l
	}
	return nil
}

func (b *Backend) invitationLinkOf(ctx context.Context, ev store.Event, copy bool) *invitationLink {
	cal, err := b.store.GetCalendar(ctx, ev.CalendarID)
	if err != nil || calsync.Disabled(cal) {
		return nil // a calendar in disabledCalendars takes no answer and no copy
	}
	acc, err := b.store.GetAccount(ctx, ev.AccountID)
	if err != nil || !acc.Enabled {
		return nil
	}
	return &invitationLink{acc: acc, cal: cal, ev: ev, copy: copy}
}

// invitationCard is the card of message m of account a: nil when the
// message has no calendar part. It also returns the linked event.
func (b *Backend) invitationCard(ctx context.Context, a store.Account, m store.Message) (*api.InvitationCard, *invitationLink, *store.Invitation, error) {
	row, err := b.store.GetInvitation(ctx, m.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, nil, nil, nil
	case err != nil:
		return nil, nil, nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	now := time.Now()
	var sum api.InvitationSummary
	if m.Invitation != nil {
		sum = *m.Invitation
	} else {
		sum = store.InvitationSummaryOf(row, -1, false, false, false, now)
	}
	card := api.InvitationCard{}
	if len(row.Card) > 0 && json.Unmarshal(row.Card, &card) != nil {
		sum.State = api.InvitationInvalid
	}
	card.InvitationSummary = sum
	card.PartID, card.UID, card.Sequence = row.PartID, row.UID, row.Sequence
	if card.RecurrenceID == "" {
		card.RecurrenceID = api.DateOrTime(row.RecurrenceID)
	}
	card.Conflicts, card.Targets = []api.InvitationConflict{}, []api.InvitationTarget{}
	card.Day = api.InvitationDay{Busy: []api.DayBusy{}}
	card.ReplyVia = api.InvitationsViaNone
	if sum.State == api.InvitationInvalid {
		// No card: the client shows the message as usual.
		return &api.InvitationCard{InvitationSummary: sum, PartID: row.PartID, Conflicts: card.Conflicts,
			Targets: card.Targets, Day: card.Day, ReplyVia: api.InvitationsViaNone}, nil, &row, nil
	}
	var respPtr *store.Response
	if row.Method == api.InvitationMethodReply {
		// The attendee's answer as stored; the user's own answer means
		// nothing on a reply the organiser receives.
		if link := b.findInvitationLink(ctx, a, row, nil); link != nil {
			card.EventID, card.CalendarID = api.EventID(link.ev.ID), api.CalendarID(link.cal.ID)
		}
		return &card, nil, &row, nil
	}
	if r, err := b.store.GetResponse(ctx, a.ID, row.UID, row.RecurrenceID); err == nil {
		respPtr = &r
		card.Response = r.Response
		at := r.RespondedAt
		card.RespondedAt = &at
	}
	link := b.findInvitationLink(ctx, a, row, respPtr)
	instance := row.RecurrenceID != "" && link != nil && link.ev.Kind != store.EventMaster
	if respPtr == nil && link != nil && !link.copy && (row.RecurrenceID == "" || instance) {
		// An answer given through the linked event (event.respond on a
		// Graph instance stored as a single row keys it by that row).
		if r, err := b.store.ResponseByEvent(ctx, a.ID, row.UID, link.ev.ID); err == nil {
			respPtr = &r
			card.Response = r.Response
			at := r.RespondedAt
			card.RespondedAt = &at
		}
	}
	if link != nil && !link.copy && (row.RecurrenceID == "" || instance) &&
		(respPtr == nil || respPtr.Sequence < row.Sequence) && store.AnsweredResponse(link.ev.MyResponse) {
		// Answered in another client (Outlook, a phone): the event's
		// answer, when is unknown. A local answer of this SEQUENCE wins.
		card.Response, card.RespondedAt = link.ev.MyResponse, nil
	}
	if link != nil {
		card.EventID, card.CalendarID = api.EventID(link.ev.ID), api.CalendarID(link.cal.ID)
	}
	if row.Method != api.InvitationMethodRequest {
		return &card, link, &row, nil
	}
	if link != nil && !link.copy && link.ev.Sequence <= row.Sequence {
		card.Previous = invitationPrevious(&card, link.ev)
	}
	loc := time.Local
	if b.calSync != nil {
		loc = b.calSync.Location()
	}
	if start, end, ok := invitationSpan(&card, loc); ok {
		if err := b.invitationDay(ctx, &card, link, start, end, loc); err != nil {
			return nil, nil, nil, err
		}
	}
	role := b.invitationFolderRole(ctx, a.ID, m.FolderID)
	b.invitationReplyPath(ctx, a, &card, link, role)
	return &card, link, &row, nil
}

// invitationPrevious is what the stored event had before an update,
// when its time, location or recurrence differs from the invitation's:
// start and end only when the time changed, the location when it changed
// and was not empty, the rule when it differs (also one now gone; a new
// rule where there was none reports nothing). nil when nothing is to
// report.
func invitationPrevious(card *api.InvitationCard, ev store.Event) *api.InvitationPrevious {
	var p api.InvitationPrevious
	if ev.AllDay {
		start, end := api.DateOrTime(ev.StartDate), api.DateOrTime(ev.EndDate)
		if start != "" && (start != api.DateOrTime(card.StartDate) || end != api.DateOrTime(card.EndDate) || !card.AllDay) {
			p.Start, p.End = start, end
		}
	} else if !ev.StartAt.IsZero() {
		same := !card.AllDay && card.Start != nil && card.End != nil && card.Start.Equal(ev.StartAt) && card.End.Equal(ev.EndAt)
		if !same {
			p.Start, p.End = api.DateOrTime(ev.StartAt.UTC().Format(time.RFC3339)), api.DateOrTime(ev.EndAt.UTC().Format(time.RFC3339))
		}
	}
	if loc := calendar.CleanLine(ev.Location, api.MaxEventLocationBytes); loc != "" && loc != card.Location {
		p.Location = loc
	}
	if ev.Kind != store.EventOverride && card.RecurrenceID == "" && ev.Recurrence != nil &&
		canonicalRecurrence(ev.Recurrence) != canonicalRecurrence(card.Recurrence) {
		r := *ev.Recurrence
		r.Exceptions = 0
		p.Recurrence = &r
	}
	if p == (api.InvitationPrevious{}) {
		return nil
	}
	return &p
}

// canonicalRecurrence is a rule's canonical form for comparison (the
// read-only count of exceptions in the window left out); "" for none.
func canonicalRecurrence(r *api.Recurrence) string {
	if r == nil {
		return ""
	}
	c := *r
	c.Exceptions = 0
	if c.Interval == 0 {
		c.Interval = 1
	}
	if c.WeekStart == "" {
		c.WeekStart = "MO"
	}
	c.ByDay = slices.Sorted(slices.Values(c.ByDay))
	c.ByMonthDay = slices.Sorted(slices.Values(c.ByMonthDay))
	c.ByMonth = slices.Sorted(slices.Values(c.ByMonth))
	data, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	return string(data)
}

// invitationSpan is the invitation's span as instants (an all-day one as
// its days in loc).
func invitationSpan(card *api.InvitationCard, loc *time.Location) (time.Time, time.Time, bool) {
	if card.AllDay {
		s, ok := calendar.ParseDate(card.StartDate)
		if !ok {
			return time.Time{}, time.Time{}, false
		}
		e, ok := calendar.ParseDate(card.EndDate)
		if !ok || !e.After(s) {
			e = s.AddDate(0, 0, 1)
		}
		return inZone(s, loc), inZone(e, loc), true
	}
	if card.Start == nil {
		return time.Time{}, time.Time{}, false
	}
	end := *card.Start
	if card.End != nil && card.End.After(end) {
		end = *card.End
	}
	return card.Start.UTC(), end.UTC(), true
}

// inZone is the wall clock of t (as if UTC) in loc.
func inZone(t time.Time, loc *time.Location) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, loc).UTC()
}

// invitationWorkingDay is the day strip's span: the working hours of the
// invitation's day (calendar.preferences), widened to the invitation
// within that day.
func invitationWorkingDay(start, end time.Time, wh api.WorkingHours, loc *time.Location) (time.Time, time.Time) {
	clock := func(s string, def int) int {
		h, m, ok := strings.Cut(s, ":")
		hh, e1 := strconv.Atoi(h)
		mm, e2 := strconv.Atoi(m)
		if !ok || e1 != nil || e2 != nil || hh < 0 || hh > 24 || mm < 0 || mm > 59 {
			return def
		}
		return hh*60 + mm
	}
	l := start.In(loc)
	midnight := time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
	next := midnight.AddDate(0, 0, 1)
	from := midnight.Add(time.Duration(clock(wh.Start, 8*60)) * time.Minute)
	to := midnight.Add(time.Duration(clock(wh.End, 17*60)) * time.Minute)
	if !to.After(from) {
		from, to = midnight.Add(8*time.Hour), midnight.Add(17*time.Hour)
	}
	if start.Before(from) {
		from = start
	}
	if end.After(to) {
		to = end
	}
	if from.Before(midnight) {
		from = midnight
	}
	if to.After(next) {
		to = next
	}
	return from.UTC(), to.UTC()
}

// invitationDay fills conflicts (≤ 3) and the day strip of a card from
// the stored occurrences of visible calendars, without the invitation's
// own event; a private event of a shared calendar comes without a title.
func (b *Backend) invitationDay(ctx context.Context, card *api.InvitationCard, link *invitationLink, start, end time.Time, loc *time.Location) error {
	prefs, err := b.store.CalendarPreferences(ctx)
	if err != nil {
		prefs = api.DefaultCalendarPreferences()
	}
	from, to := invitationWorkingDay(start, end, prefs.WorkingHours, loc)
	qFrom, qTo := from, to
	if start.Before(qFrom) {
		qFrom = start
	}
	if end.After(qTo) {
		qTo = end
	}
	if !qTo.After(qFrom) {
		qTo = qFrom.Add(time.Minute)
	}
	list, err := b.store.EventRange(ctx, store.OccurrenceQuery{From: qFrom, To: qTo, Zone: loc, Limit: 500})
	if err != nil {
		return api.NewError(api.CodeStorageError, "%v", err)
	}
	cals := map[string]store.Calendar{}
	calOf := func(id string) store.Calendar {
		c, ok := cals[id]
		if !ok {
			c, _ = b.store.GetCalendar(ctx, id)
			cals[id] = c
		}
		return c
	}
	var occs []api.Occurrence
	for _, o := range list.Occurrences {
		ev := list.Events[o.EventID]
		if ev.UID == card.UID && ev.UID != "" {
			continue // the invitation's own event (or the daemon's copy)
		}
		if link != nil && (o.EventID == link.ev.ID || o.SeriesID == link.ev.ID || (link.ev.MasterID != "" && o.SeriesID == link.ev.MasterID)) {
			continue
		}
		ao := api.Occurrence{EventID: api.EventID(o.EventID), CalendarID: api.CalendarID(o.CalendarID),
			AccountID: api.AccountID(o.AccountID), AllDay: o.AllDay, Status: ev.Status, ShowAs: ev.ShowAs, MyResponse: ev.MyResponse}
		if !(ev.Private && calOf(o.CalendarID).OwnerAddress != "") {
			ao.Title = ev.Title
		}
		switch {
		case o.AllDay:
			ao.StartDate, ao.EndDate = api.Date(o.StartAt.Format(time.DateOnly)), api.Date(o.EndAt.Format(time.DateOnly))
		case o.Floating:
			s, e := inZone(o.StartAt, loc), inZone(o.EndAt, loc)
			ao.Start, ao.End = &s, &e
		default:
			s, e := o.StartAt, o.EndAt
			ao.Start, ao.End = &s, &e
		}
		occs = append(occs, ao)
	}
	for _, c := range calendar.Conflicts(start, end, occs, loc, calendar.MaxConflicts) {
		s, e, ok := occurrenceSpan(c, loc)
		if !ok {
			continue
		}
		card.Conflicts = append(card.Conflicts, api.InvitationConflict{Title: c.Title, Start: s, End: e,
			CalendarID: c.CalendarID, EventID: c.EventID})
	}
	card.Day = api.InvitationDay{From: from, To: to, Busy: []api.DayBusy{}}
	for _, o := range occs {
		if !calendar.Busy(o) || len(card.Day.Busy) == maxInvitationDayBusy {
			continue
		}
		s, e, ok := occurrenceSpan(o, loc)
		if !ok || !s.Before(to) || !e.After(from) {
			continue
		}
		if s.Before(from) {
			s = from
		}
		if e.After(to) {
			e = to
		}
		card.Day.Busy = append(card.Day.Busy, api.DayBusy{Start: s, End: e, CalendarID: o.CalendarID})
	}
	return nil
}

// occurrenceSpan is an occurrence's instants (all-day: its days in loc).
func occurrenceSpan(o api.Occurrence, loc *time.Location) (time.Time, time.Time, bool) {
	if o.AllDay {
		s, ok1 := calendar.ParseDate(o.StartDate)
		e, ok2 := calendar.ParseDate(o.EndDate)
		if !ok1 || !ok2 {
			return time.Time{}, time.Time{}, false
		}
		return inZone(s, loc), inZone(e, loc), true
	}
	if o.Start == nil || o.End == nil {
		return time.Time{}, time.Time{}, false
	}
	return o.Start.UTC(), o.End.UTC(), true
}

// invitationReplyPath fills targets, fixedTarget, canRespond,
// canProposeNewTime, replyVia, replyAccountId and replyTo of a request's
// card.
func (b *Backend) invitationReplyPath(ctx context.Context, a store.Account, card *api.InvitationCard, link *invitationLink, role api.FolderRole) {
	me := b.invitationMe(ctx, a)
	ownOrganizer := card.OwnEvent || card.Organizer != nil && slices.Contains(me, strings.ToLower(card.Organizer.Address))
	switch {
	case ownOrganizer:
		// The user's own invitation: nothing to answer or add; the link
		// (Show in Calendar) stays.
		card.ReplyVia = api.InvitationsViaNone
		return
	case link != nil && !link.copy:
		card.Targets = []api.InvitationTarget{{CalendarID: api.CalendarID(link.cal.ID), IsDefault: invitationIsDefault(link.acc, link.cal)}}
		card.FixedTarget = true
		card.CanProposeNewTime = link.acc.Config.Protocol() == api.AccountGraph && link.ev.NewTimeOK
		card.ReplyVia, card.ReplyAccountID = api.InvitationsViaServer, api.AccountID(link.acc.ID)
		if link.acc.Config.Protocol() == api.AccountCalDAV && !b.invitationServerSchedules(link.acc) {
			card.ReplyVia, card.ReplyAccountID = api.InvitationsViaMail, api.AccountID(a.ID)
		}
	default:
		if a.Config.Protocol() != api.AccountGraph {
			// A Graph mailbox's event is made by Exchange: until it is
			// stored there is nowhere to choose (empty: the answer only).
			card.Targets = b.invitationTargets(ctx, a, link)
		}
		if card.Organizer != nil && can(a.Config, api.CapabilityCompose) {
			card.ReplyVia, card.ReplyAccountID = api.InvitationsViaMail, api.AccountID(a.ID)
		}
	}
	if card.Organizer != nil {
		org := *card.Organizer
		card.ReplyTo = &org
	}
	switch {
	case role == api.RoleJunk || role == api.RoleTrash || role == api.RoleSent || role == api.RoleDrafts || role == api.RoleOutbox:
	case card.State != api.InvitationUnanswered && card.State != api.InvitationAnswered:
	case ownOrganizer || card.Organizer == nil:
	case link != nil && !link.copy:
		card.CanRespond = true
	case card.Me != nil && card.ReplyVia != api.InvitationsViaNone:
		card.CanRespond = true
	}
	if !card.CanRespond {
		card.CanProposeNewTime = false
	}
}

// invitationIsDefault: the calendar is its account's default for new
// events (CalendarConfig.defaultCalendar, else the server's).
func invitationIsDefault(acc store.Account, cal store.Calendar) bool {
	if acc.Config.Calendar != nil && acc.Config.Calendar.DefaultCalendar != "" {
		return acc.Config.Calendar.DefaultCalendar == cal.RemoteID
	}
	return cal.ServerDefault
}

// invitationTargets are the writable event calendars the invitation's
// event may be stored in (D18): the copy's calendar first, then the
// defaults of the CalDAV accounts whose mail account is a, then the other
// defaults, then the rest, in account and server order.
func (b *Backend) invitationTargets(ctx context.Context, a store.Account, link *invitationLink) []api.InvitationTarget {
	accounts, err := b.invitationCalendarAccounts(ctx)
	if err != nil {
		return []api.InvitationTarget{}
	}
	type ranked struct {
		t    api.InvitationTarget
		rank int
	}
	var all []ranked
	for _, acc := range accounts {
		cals, err := b.store.ListCalendars(ctx, acc.ID)
		if err != nil {
			continue
		}
		mine := acc.Config.CalDAV != nil && string(acc.Config.CalDAV.MailAccountID) == a.ID
		for _, c := range cals {
			if c.Access == api.CalendarAccessRead || (c.Kind != "" && c.Kind != api.CalendarKindEvents) || calsync.Disabled(c) {
				continue
			}
			def := invitationIsDefault(acc, c)
			rank := 4
			switch {
			case link != nil && link.cal.ID == c.ID:
				rank = 0
			case def && mine:
				rank = 1
			case def:
				rank = 2
			case mine:
				rank = 3
			}
			all = append(all, ranked{api.InvitationTarget{CalendarID: api.CalendarID(c.ID), IsDefault: def}, rank})
		}
	}
	slices.SortStableFunc(all, func(x, y ranked) int { return x.rank - y.rank })
	out := make([]api.InvitationTarget, 0, min(len(all), maxInvitationTargets))
	for i := 0; i < len(all) && i < maxInvitationTargets; i++ {
		out = append(out, all[i].t)
	}
	return out
}

// attachInvitationCard sets Message.Invitation of message.get /
// message.download.
func (b *Backend) attachInvitationCard(ctx context.Context, a store.Account, m store.Message, out *api.Message) error {
	if m.Invitation == nil {
		return nil
	}
	card, _, _, err := b.invitationCard(ctx, a, m)
	if err != nil {
		return err
	}
	out.Invitation = card
	return nil
}

// --- invitation.list ----------------------------------------------------------

func (s *invitationService) List(ctx context.Context, p api.InvitationListParams) (*api.InvitationListResult, error) {
	b := s.b
	if p.Limit < 0 || p.Limit > api.MaxInvitationList {
		return nil, api.NewError(api.CodeInvalidArgument, "limit must be 0–%d", api.MaxInvitationList)
	}
	switch p.State {
	case "", api.InvitationUnanswered, api.InvitationAnswered, api.InvitationOutdated, api.InvitationCancelled,
		api.InvitationProcessed:
	default:
		return nil, api.NewError(api.CodeInvalidArgument, "unknown state %q", p.State)
	}
	accounts, err := b.store.ListAccounts(ctx)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	q := store.InvitationQuery{State: p.State, Limit: p.Limit}
	if q.Limit == 0 {
		q.Limit = 200
	}
	want := q.Limit
	q.Limit = min(api.MaxInvitationList, want*2+20) // room for the rows left out below
	known := map[string]bool{}
	for _, a := range accounts {
		known[a.ID] = true
		if !a.Enabled {
			q.ExcludeAccountIDs = append(q.ExcludeAccountIDs, a.ID)
		}
	}
	for _, id := range p.AccountIDs {
		if !known[string(id)] {
			return nil, api.NewError(api.CodeAccountNotFound, "unknown account %q", id)
		}
		q.AccountIDs = append(q.AccountIDs, string(id))
	}
	rows, err := b.store.ListInvitations(ctx, q, time.Now())
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	out := &api.InvitationListResult{Invitations: []api.InvitationListItem{}}
	for _, r := range rows {
		switch r.FolderRole {
		case api.RoleJunk, api.RoleTrash, api.RoleSent, api.RoleDrafts, api.RoleOutbox:
			continue
		}
		if r.Summary.State == api.InvitationInvalid || len(out.Invitations) == want {
			continue
		}
		out.Invitations = append(out.Invitations, api.InvitationListItem{AccountID: api.AccountID(r.AccountID),
			MessageID: api.MessageID(r.MessageID), Summary: r.Summary})
	}
	return out, nil
}

// --- invitation.respond ---------------------------------------------------------

func (s *invitationService) Respond(ctx context.Context, p api.InvitationRespondParams) (*api.InvitationRespondResult, error) {
	b := s.b
	switch p.Response {
	case api.ResponseAccepted, api.ResponseTentative, api.ResponseDeclined:
	default:
		return nil, api.NewError(api.CodeInvalidArgument, "response must be accepted, tentative or declined")
	}
	if p.AccountID == "" || p.MessageID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "accountId and messageId are required")
	}
	if len(p.Comment) > api.MaxCommentBytes {
		return nil, api.NewError(api.CodeInvalidArgument, "comment over %d bytes", api.MaxCommentBytes)
	}
	if p.Mail != nil && (len(p.Mail.Subject) > api.MaxMailTextBytes || len(p.Mail.Text) > api.MaxMailTextBytes) {
		return nil, api.NewError(api.CodeInvalidArgument, "mail text over %d bytes", api.MaxMailTextBytes)
	}
	if (p.ProposedStart == nil) != (p.ProposedEnd == nil) || p.ProposedStart != nil && !p.ProposedEnd.After(*p.ProposedStart) {
		return nil, api.NewError(api.CodeInvalidArgument, "a proposed time needs a start before its end")
	}
	a, err := b.requireAccount(ctx, string(p.AccountID))
	if err != nil {
		return nil, err
	}
	m, err := b.getMessage(ctx, a.ID, string(p.MessageID))
	if err != nil {
		return nil, err
	}
	card, link, row, err := b.invitationCard(ctx, a, m)
	switch {
	case err != nil:
		return nil, err
	case card == nil:
		return nil, api.NewError(api.CodeInvalidArgument, "the message has no invitation")
	case card.State == api.InvitationInvalid:
		return nil, api.NewError(api.CodeMalformedCalendar, "the invitation could not be read")
	case row.Method != api.InvitationMethodRequest:
		return nil, api.NewError(api.CodeInvalidArgument, "the message is not an invitation to answer")
	case !card.CanRespond:
		return nil, api.NewError(api.CodeInvalidArgument, "the invitation cannot be answered")
	case p.ProposedStart != nil && !card.CanProposeNewTime:
		return nil, api.NewError(api.CodeInvalidArgument, "the organiser allows no new time")
	}
	if p.CalendarID != "" && !slices.ContainsFunc(card.Targets, func(t api.InvitationTarget) bool { return t.CalendarID == p.CalendarID }) {
		return nil, api.NewError(api.CodeInvalidArgument, "calendar %q is not a target of the invitation", p.CalendarID)
	}
	send := p.SendResponse == nil || *p.SendResponse
	resp := store.Response{AccountID: a.ID, UID: row.UID, RecurrenceID: row.RecurrenceID, Sequence: row.Sequence,
		Response: p.Response, RespondedAt: time.Now()}
	changed := map[string][]string{}
	var calAccount *store.Account
	if link != nil && !link.copy {
		if err := b.respondThroughEvent(ctx, a, link, row, p, send); err != nil {
			return nil, err
		}
		resp.Via = string(link.acc.Config.Protocol())
		resp.CalendarID, resp.EventID = link.cal.ID, link.ev.ID
		calAccount = &link.acc
	} else {
		parsed, err := b.invitationParsed(ctx, a, m, *row)
		if err != nil {
			return nil, err
		}
		copyCal, copyID, copyAcc, err := b.respondCopy(ctx, a, link, parsed, p)
		if err != nil {
			return nil, err
		}
		if copyAcc != nil {
			calAccount = copyAcc
			changed[copyAcc.ID] = append(changed[copyAcc.ID], copyCal)
		}
		if link != nil && copyCal != link.cal.ID {
			changed[link.acc.ID] = append(changed[link.acc.ID], link.cal.ID)
		}
		resp.CalendarID, resp.EventID, resp.Via = copyCal, copyID, "none"
		if send {
			if err := b.queueInvitationReply(ctx, a, m, parsed, card, p); err != nil {
				return nil, err
			}
			resp.Via = "imip"
		}
	}
	if err := b.store.PutResponse(ctx, resp); err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	moved := b.invitationAfterAnswer(ctx, a, m, calAccount)
	b.invitationsChanged(a.ID)
	for acc, cals := range changed {
		b.invitationCalendarChanged(acc, cals)
	}
	if m2, err := b.store.GetMessage(ctx, a.ID, m.ID); err == nil {
		m = m2 // its folder after the move, its summary after the answer
	}
	after, _, _, err := b.invitationCard(ctx, a, m)
	if err != nil {
		return nil, err
	}
	out := &api.InvitationRespondResult{MovedTo: moved, EventID: api.EventID(resp.EventID)}
	if after != nil {
		out.Invitation = *after
	}
	return out, nil
}

// invitationProvider is the calendar source of an account: the running
// engine's, else a new one (calendarProvider).
func (b *Backend) invitationProvider(ctx context.Context, a store.Account) (calsync.Provider, error) {
	if invitationProviders != nil {
		return invitationProviders(ctx, a)
	}
	if b.calSync != nil {
		if p, ok := b.calSync.Provider(a.ID); ok {
			return p, nil
		}
	}
	return b.calendarProvider(ctx, a)
}

// invitationWriteError maps a provider failure.
func invitationWriteError(err error) error {
	if errors.Is(err, calsync.ErrUnsupported) {
		return api.NewError(api.CodeInvalidArgument, "the calendar cannot do this")
	}
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae
	}
	return api.NewError(api.CodeServerError, "%v", err)
}

// respondThroughEvent answers through the stored event (D18), as
// event.respond does: the instance of an instance invitation, else the
// series; the server's answer stored by the engine.
func (b *Backend) respondThroughEvent(ctx context.Context, a store.Account, link *invitationLink, row *store.Invitation, p api.InvitationRespondParams, send bool) error {
	prov, err := b.invitationProvider(ctx, link.acc)
	if err != nil {
		return invitationWriteError(err)
	}
	r := calendar.Response{Scope: api.EditScopeAll, Response: p.Response, ProposedStart: p.ProposedStart, ProposedEnd: p.ProposedEnd}
	opts := calsync.WriteOptions{IfMatch: link.ev.ETag, Notify: send, Mail: p.Mail, Comment: p.Comment}
	if row.RecurrenceID != "" {
		r.Scope, opts.RecurrenceID = api.EditScopeThis, api.DateOrTime(row.RecurrenceID)
	}
	if link.acc.ID != a.ID && can(a.Config, api.CapabilityCompose) {
		opts.MailAccountID = api.AccountID(a.ID)
	}
	res, werr := prov.Respond(ctx, link.cal, link.ev, r, opts)
	if werr != nil && len(res.Objects) == 0 && len(res.Removed) == 0 {
		return invitationWriteError(werr)
	}
	if err := b.calSync.ApplyWrite(ctx, link.acc, link.cal.ID, res); err != nil {
		return api.NewError(api.CodeStorageError, "%v", err)
	}
	if werr != nil {
		return invitationWriteError(werr)
	}
	return nil
}

// invitationParsed reads the invitation's part again (the card keeps
// only what it shows; a reply and a copy need the object).
func (b *Backend) invitationParsed(ctx context.Context, a store.Account, m store.Message, row store.Invitation) (imip.Invitation, error) {
	att, ok := rowAttachment(m, row.PartID)
	if !ok {
		return imip.Invitation{}, api.NewError(api.CodeMalformedCalendar, "the calendar part is gone")
	}
	data, err := b.invitationPartData(ctx, m, att)
	if err != nil {
		var ae *api.Error
		if errors.As(err, &ae) {
			return imip.Invitation{}, ae
		}
		return imip.Invitation{}, api.NewError(api.CodeMalformedCalendar, "%v", err)
	}
	inv, err := imip.ParseMessage(data, b.invitationMe(ctx, a))
	if err != nil {
		return imip.Invitation{}, api.NewError(api.CodeMalformedCalendar, "the invitation could not be read")
	}
	return inv, nil
}

// respondCopy keeps the copy of the invitation's event the daemon stores
// for an answer by mail (D18): decline removes one stored earlier; an
// acceptance with a calendar stores one there (a CalDAV calendar: the
// organiser's object with the user's PARTSTAT and SCHEDULE-AGENT=CLIENT on
// the ORGANIZER; a Graph calendar: a plain copy without attendees), or
// updates the PARTSTAT of the one already in that calendar. Returns the
// copy's calendar, event and account ("" and nil when there is none).
func (b *Backend) respondCopy(ctx context.Context, a store.Account, existing *invitationLink, inv imip.Invitation, p api.InvitationRespondParams) (string, string, *store.Account, error) {
	target := string(p.CalendarID)
	if existing != nil && (p.Response == api.ResponseDeclined || target != "" && target != existing.cal.ID) {
		if err := b.removeInvitationCopy(ctx, existing); err != nil {
			return "", "", nil, err
		}
		existing = nil
	}
	if p.Response == api.ResponseDeclined {
		return "", "", nil, nil
	}
	if existing != nil {
		if existing.acc.Config.Protocol() == api.AccountCalDAV {
			prov, err := b.invitationProvider(ctx, existing.acc)
			if err != nil {
				return "", "", nil, invitationWriteError(err)
			}
			res, err := prov.Respond(ctx, existing.cal, existing.ev, calendar.Response{Scope: api.EditScopeAll, Response: p.Response},
				calsync.WriteOptions{IfMatch: existing.ev.ETag})
			if err != nil {
				return "", "", nil, invitationWriteError(err)
			}
			if err := b.calSync.ApplyWrite(ctx, existing.acc, existing.cal.ID, res); err != nil {
				return "", "", nil, api.NewError(api.CodeStorageError, "%v", err)
			}
		}
		acc := existing.acc
		return existing.cal.ID, existing.ev.ID, &acc, nil
	}
	if target == "" {
		return "", "", nil, nil
	}
	cal, err := b.store.GetCalendar(ctx, target)
	if err != nil {
		return "", "", nil, api.NewError(api.CodeCalendarNotFound, "unknown calendar %q", target)
	}
	acc, err := b.store.GetAccount(ctx, cal.AccountID)
	if err != nil {
		return "", "", nil, api.NewError(api.CodeCalendarNotFound, "unknown calendar %q", target)
	}
	if cal.Access == api.CalendarAccessRead {
		return "", "", nil, api.NewError(api.CodeReadOnly, "the calendar is read-only")
	}
	if calsync.Disabled(cal) {
		return "", "", nil, api.NewError(api.CodeInvalidArgument, "calendar %q is not synchronised", target)
	}
	ev := invitationCopyEvent(inv, p.Response, acc.Config.Protocol() == api.AccountGraph)
	prov, err := b.invitationProvider(ctx, acc)
	if err != nil {
		return "", "", nil, invitationWriteError(err)
	}
	res, err := prov.Create(ctx, cal, ev, calsync.WriteOptions{})
	if err != nil && len(res.Objects) == 0 {
		return "", "", nil, invitationWriteError(err)
	}
	if aerr := b.calSync.ApplyWrite(ctx, acc, cal.ID, res); aerr != nil {
		return "", "", nil, api.NewError(api.CodeStorageError, "%v", aerr)
	}
	if err != nil {
		return "", "", nil, invitationWriteError(err)
	}
	eventID := ""
	if len(res.Objects) > 0 {
		if rows, err := b.store.EventsByRemoteID(ctx, cal.ID, res.Objects[0].RemoteID); err == nil && len(rows) > 0 {
			eventID = rows[0].ID
		}
	}
	return cal.ID, eventID, &acc, nil
}

// invitationCopyEvent is the event stored for an invitation: the
// organiser's with the user's answer, or for Graph (which cannot store an
// event organised by someone else) a plain copy without attendees.
func invitationCopyEvent(inv imip.Invitation, r api.EventResponse, plain bool) calendar.Event {
	ev := inv.Event
	ev.Attendees = slices.Clone(ev.Attendees)
	ev.Alarms = nil
	if plain {
		ev.Organizer, ev.Attendees, ev.AttendeeCount = nil, nil, 0
		ev.IAmOrganizer, ev.MyResponse, ev.ResponseRequested = false, "", false
		return ev
	}
	mine := ""
	if inv.Me != nil {
		mine = inv.Me.Address
	}
	for i := range ev.Attendees {
		if ev.Attendees[i].Me || mine != "" && strings.EqualFold(ev.Attendees[i].Address, mine) {
			ev.Attendees[i].Response = r
		}
	}
	ev.MyResponse = r
	return ev
}

// removeInvitationCopy deletes the copy the daemon stored, notifying
// nobody; a copy already gone is not an error.
func (b *Backend) removeInvitationCopy(ctx context.Context, l *invitationLink) error {
	prov, err := b.invitationProvider(ctx, l.acc)
	if err != nil {
		return invitationWriteError(err)
	}
	res, err := prov.Delete(ctx, l.cal, l.ev, api.EditScopeAll, calsync.WriteOptions{IfMatch: l.ev.ETag})
	if err != nil {
		var ae *api.Error
		if errors.As(err, &ae) && ae.Code == api.CodeEventGone {
			res = calsync.WriteResult{Removed: []string{l.ev.RemoteID}}
		} else {
			return invitationWriteError(err)
		}
	}
	if err := b.calSync.ApplyWrite(ctx, l.acc, l.cal.ID, res); err != nil {
		return api.NewError(api.CodeStorageError, "%v", err)
	}
	return nil
}

// queueInvitationReply queues the iMIP REPLY from the invitation's
// account to the stored ORGANIZER only (D10), threaded under the
// invitation message.
func (b *Backend) queueInvitationReply(ctx context.Context, a store.Account, m store.Message, inv imip.Invitation, card *api.InvitationCard, p api.InvitationRespondParams) error {
	if inv.Organizer == nil || inv.Organizer.Address == "" {
		return api.NewError(api.CodeInvalidArgument, "the invitation has no organiser to reply to")
	}
	me := ""
	if card.Me != nil {
		me = card.Me.Address
	}
	data, err := imip.BuildReply(inv, me, p.Response, p.Comment, time.Now())
	if err != nil {
		return api.NewError(api.CodeMalformedCalendar, "the reply does not build: %v", err)
	}
	id := strings.Trim(strings.TrimSpace(m.RFCMessageID), "<>")
	var refs []string
	for _, r := range m.References {
		if r = strings.Trim(strings.TrimSpace(r), "<>"); r != "" {
			refs = append(refs, r)
		}
	}
	if id != "" {
		refs = append(refs, id)
	}
	return b.queueIMIP(ctx, a.ID, IMIPMail{Method: imip.MethodReply, Data: data, Recipients: []string{inv.Organizer.Address},
		Mail: p.Mail, InReplyTo: id, References: refs})
}

// invitationAfterAnswer moves the answered message to the account's
// archive folder when afterAnswer is archive (the invitation's account's
// calendar settings, else those of the calendar's account); the folder it
// went to, "" when it stayed.
func (b *Backend) invitationAfterAnswer(ctx context.Context, a store.Account, m store.Message, calAccount *store.Account) api.FolderID {
	after := api.AfterAnswerKeep
	switch {
	case a.Config.Calendar != nil && a.Config.Calendar.AfterAnswer != "":
		after = a.Config.Calendar.AfterAnswer
	case calAccount != nil && calAccount.Config.Calendar != nil:
		after = calAccount.Config.Calendar.AfterAnswer
	}
	if after != api.AfterAnswerArchive || !can(a.Config, api.CapabilityMove) {
		return ""
	}
	f, err := b.store.FolderByRole(ctx, a.ID, api.RoleArchive)
	if err != nil || f.ID == m.FolderID || !f.Selectable {
		return ""
	}
	if _, err := (&messageService{b: b}).Move(ctx, api.MessageMoveParams{AccountID: api.AccountID(a.ID),
		MessageIDs: []api.MessageID{api.MessageID(m.ID)}, TargetFolderID: api.FolderID(f.ID)}); err != nil {
		b.log.Warn("archive an answered invitation", "account", a.ID, "message", m.ID, "err", err)
		return ""
	}
	return api.FolderID(f.ID)
}

// --- invitation.applyCancel -----------------------------------------------------

func (s *invitationService) ApplyCancel(ctx context.Context, p api.InvitationApplyCancelParams) (*api.InvitationApplyCancelResult, error) {
	b := s.b
	if p.AccountID == "" || p.MessageID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "accountId and messageId are required")
	}
	a, err := b.requireAccount(ctx, string(p.AccountID))
	if err != nil {
		return nil, err
	}
	m, err := b.getMessage(ctx, a.ID, string(p.MessageID))
	if err != nil {
		return nil, err
	}
	row, err := b.store.GetInvitation(ctx, m.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, api.NewError(api.CodeInvalidArgument, "the message has no invitation")
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	case row.Method != api.InvitationMethodCancel || len(row.Card) == 0:
		return nil, api.NewError(api.CodeInvalidArgument, "the message is not a cancellation")
	}
	var resp *store.Response
	for _, rid := range []string{row.RecurrenceID, ""} {
		if r, err := b.store.GetResponse(ctx, a.ID, row.UID, rid); err == nil {
			resp = &r
			break
		}
	}
	link := b.findInvitationLink(ctx, a, row, resp)
	if link == nil {
		return &api.InvitationApplyCancelResult{Removed: false}, nil
	}
	prov, err := b.invitationProvider(ctx, link.acc)
	if err != nil {
		return nil, invitationWriteError(err)
	}
	scope, opts := api.EditScopeAll, calsync.WriteOptions{IfMatch: link.ev.ETag}
	if row.RecurrenceID != "" && !link.copy {
		scope, opts.RecurrenceID = api.EditScopeThis, api.DateOrTime(row.RecurrenceID)
	}
	res, err := prov.Delete(ctx, link.cal, link.ev, scope, opts)
	if err != nil {
		var ae *api.Error
		if errors.As(err, &ae) && ae.Code == api.CodeEventGone {
			res = calsync.WriteResult{Removed: []string{link.ev.RemoteID}}
			if scope == api.EditScopeAll {
				_ = b.calSync.ApplyWrite(ctx, link.acc, link.cal.ID, res)
			}
			return &api.InvitationApplyCancelResult{Removed: false}, nil
		}
		return nil, invitationWriteError(err)
	}
	if err := b.calSync.ApplyWrite(ctx, link.acc, link.cal.ID, res); err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	b.invitationsChanged(a.ID)
	return &api.InvitationApplyCancelResult{Removed: true}, nil
}
