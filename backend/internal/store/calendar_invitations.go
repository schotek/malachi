// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Invitation is a row of cal_invitations: the calendar part of one stored
// message row (D17), going with the row (ON DELETE CASCADE). Organizer is
// the lower-case address; StartAt/EndAt are UTC, an all-day one's dates'
// midnights as if UTC. Card is card_json, the api.InvitationCard without
// its derived fields; nil (stored as JSON null) marks a part that could
// not be read (state invalid, no card). ParserVersion is the
// calendar.ParserVersion that read it.
type Invitation struct {
	MessageID     string
	AccountID     string
	PartID        string
	Method        api.InvitationMethod
	UID           string
	RecurrenceID  string
	Sequence      int
	DTStamp       time.Time
	Organizer     string
	StartAt       time.Time
	EndAt         time.Time
	AllDay        bool
	Title         string
	Card          json.RawMessage
	ParserVersion int
	CreatedAt     time.Time
}

// Response is a row of cal_responses: the user's answer to an invitation,
// shared by every copy of its message, keyed by (AccountID, UID,
// RecurrenceID). Sequence is the invitation's SEQUENCE answered; Via is
// graph, caldav, imip or none.
type Response struct {
	AccountID    string
	UID          string
	RecurrenceID string
	Sequence     int
	Response     api.EventResponse
	Via          string
	CalendarID   string
	EventID      string
	OutboxID     string
	RespondedAt  time.Time
}

const invitationColumns = `ci.message_id, ci.account_id, ci.part_id, ci.method, ci.uid, ci.recurrence_id, ci.sequence,
	ci.dtstamp, ci.organizer, ci.start_at, ci.end_at, ci.all_day, ci.title, ci.card_json, ci.parser_version, ci.created_at`

func scanInvitation(row scanner, extra ...any) (Invitation, error) {
	var inv Invitation
	var method, dtstamp, start, end, card, created string
	var allDay int
	if err := row.Scan(append([]any{&inv.MessageID, &inv.AccountID, &inv.PartID, &method, &inv.UID, &inv.RecurrenceID,
		&inv.Sequence, &dtstamp, &inv.Organizer, &start, &end, &allDay, &inv.Title, &card, &inv.ParserVersion,
		&created}, extra...)...); err != nil {
		return Invitation{}, err
	}
	inv.Method, inv.AllDay = api.InvitationMethod(method), allDay != 0
	inv.DTStamp, inv.StartAt, inv.EndAt, inv.CreatedAt = parseStamp(dtstamp), parseStamp(start), parseStamp(end), parseStamp(created)
	if card != "null" {
		inv.Card = json.RawMessage(card)
	}
	return inv, nil
}

// PutInvitation stores the invitation of a message row, replacing what it
// had (a re-read with a newer ParserVersion). The message must exist.
func (s *Store) PutInvitation(ctx context.Context, inv Invitation) error {
	card := "null"
	if len(inv.Card) > 0 {
		if !json.Valid(inv.Card) {
			return fmt.Errorf("put invitation: card is not JSON")
		}
		card = string(inv.Card)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO cal_invitations (message_id, account_id, part_id, method, uid,
			recurrence_id, sequence, dtstamp, organizer, start_at, end_at, all_day, title, card_json, parser_version, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (message_id) DO UPDATE SET account_id = excluded.account_id, part_id = excluded.part_id,
			method = excluded.method, uid = excluded.uid, recurrence_id = excluded.recurrence_id,
			sequence = excluded.sequence, dtstamp = excluded.dtstamp, organizer = excluded.organizer,
			start_at = excluded.start_at, end_at = excluded.end_at, all_day = excluded.all_day,
			title = excluded.title, card_json = excluded.card_json, parser_version = excluded.parser_version`,
		inv.MessageID, inv.AccountID, inv.PartID, string(inv.Method), inv.UID, inv.RecurrenceID, inv.Sequence,
		fmtStamp(inv.DTStamp), inv.Organizer, fmtStamp(inv.StartAt), fmtStamp(inv.EndAt), boolInt(inv.AllDay),
		inv.Title, card, inv.ParserVersion, nowStamp())
	if err != nil {
		return fmt.Errorf("put invitation: %w", err)
	}
	return nil
}

// GetInvitation returns the invitation of a message row; ErrNotFound when
// it has none.
func (s *Store) GetInvitation(ctx context.Context, messageID string) (Invitation, error) {
	inv, err := scanInvitation(s.db.QueryRowContext(ctx, `SELECT `+invitationColumns+` FROM cal_invitations ci WHERE ci.message_id = ?`, messageID))
	if errors.Is(err, sql.ErrNoRows) {
		return Invitation{}, ErrNotFound
	}
	if err != nil {
		return Invitation{}, fmt.Errorf("get invitation: %w", err)
	}
	return inv, nil
}

// InvitationsByUID returns the invitations of a UID in an account (every
// recurrence id and copy), by sequence, then DTStamp, then message id.
func (s *Store) InvitationsByUID(ctx context.Context, accountID, uid string) ([]Invitation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+invitationColumns+` FROM cal_invitations ci
		WHERE ci.account_id = ? AND ci.uid = ? ORDER BY ci.sequence, ci.dtstamp, ci.message_id`, accountID, uid)
	if err != nil {
		return nil, fmt.Errorf("invitations by uid: %w", err)
	}
	defer rows.Close()
	var out []Invitation
	for rows.Next() {
		inv, err := scanInvitation(rows)
		if err != nil {
			return nil, fmt.Errorf("scan invitation: %w", err)
		}
		out = append(out, inv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("invitations by uid: %w", err)
	}
	return out, nil
}

// DeleteInvitation removes the invitation of a message row (its part is no
// longer a calendar part); no error when it has none.
func (s *Store) DeleteInvitation(ctx context.Context, messageID string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM cal_invitations WHERE message_id = ?`, messageID); err != nil {
		return fmt.Errorf("delete invitation: %w", err)
	}
	return nil
}

// InvitationsBelowVersion returns up to limit message ids whose invitation
// was read by a parser older than version, by message id.
func (s *Store) InvitationsBelowVersion(ctx context.Context, version, limit int) ([]string, error) {
	return s.listIDs(ctx, `SELECT message_id FROM cal_invitations WHERE parser_version < ? ORDER BY message_id LIMIT ?`, version, limit)
}

func (s *Store) listIDs(ctx context.Context, q string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list ids: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// InvitationScanRow is a stored message the invitation backfill looks at
// (MetaCalendarInvitesScan): its rowid is the cursor.
type InvitationScanRow struct {
	RowID        int64
	MessageID    string
	AccountID    string
	FolderID     string
	Date         time.Time
	InternalDate time.Time
	Attachments  []api.Attachment
	HasInvite    bool // it already has a cal_invitations row
}

// InvitationScanBatch returns up to limit message rows after rowid after,
// by rowid, whose attachments list names a calendar part (a text/calendar
// or application/ics type, or a .ics file name); next is the last rowid
// looked at (the cursor for the next call), 0 when the table is done.
func (s *Store) InvitationScanBatch(ctx context.Context, after int64, limit int) (rows []InvitationScanRow, next int64, err error) {
	if limit <= 0 {
		limit = 2000
	}
	r, err := s.db.QueryContext(ctx, `SELECT m.rowid, m.id, m.account_id, m.folder_id, m.date, m.internal_date,
			m.attachments_json, m.attachments_json LIKE '%calendar%' OR m.attachments_json LIKE '%ics%',
			EXISTS (SELECT 1 FROM cal_invitations ci WHERE ci.message_id = m.id)
		FROM messages m WHERE m.rowid > ? ORDER BY m.rowid LIMIT ?`, after, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("invitation scan: %w", err)
	}
	defer r.Close()
	n := 0
	for r.Next() {
		var row InvitationScanRow
		var date, internal, attachments string
		var maybe, has int
		if err := r.Scan(&row.RowID, &row.MessageID, &row.AccountID, &row.FolderID, &date, &internal, &attachments, &maybe, &has); err != nil {
			return nil, 0, fmt.Errorf("invitation scan: %w", err)
		}
		n++
		next = row.RowID
		if maybe == 0 {
			continue
		}
		if err := json.Unmarshal([]byte(attachments), &row.Attachments); err != nil {
			continue // a damaged list is no calendar part
		}
		if !hasCalendarPart(row.Attachments) {
			continue
		}
		row.Date, row.InternalDate, row.HasInvite = parseStamp(date), parseStamp(internal), has != 0
		rows = append(rows, row)
	}
	if err := r.Err(); err != nil {
		return nil, 0, fmt.Errorf("invitation scan: %w", err)
	}
	if n < limit {
		next = 0
	}
	return rows, next, nil
}

func hasCalendarPart(atts []api.Attachment) bool {
	for _, a := range atts {
		if IsCalendarPart(a.ContentType, a.Filename) {
			return true
		}
	}
	return false
}

// IsCalendarPart reports whether a part of this content type or file name
// is a calendar part (D17): text/calendar, application/ics, or a name
// ending in .ics.
func IsCalendarPart(contentType, filename string) bool {
	ct, _, _ := strings.Cut(strings.ToLower(contentType), ";")
	if ct = strings.TrimSpace(ct); ct == "text/calendar" || ct == "application/ics" {
		return true
	}
	fn := strings.ToLower(filename)
	return len(fn) > len(".ics") && strings.HasSuffix(fn, ".ics")
}

// PutResponse stores the user's answer to an invitation, replacing an
// earlier one of the same key.
func (s *Store) PutResponse(ctx context.Context, r Response) error {
	at := r.RespondedAt
	if at.IsZero() {
		at = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO cal_responses (account_id, uid, recurrence_id, sequence, response, via,
			calendar_id, event_id, outbox_id, responded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (account_id, uid, recurrence_id) DO UPDATE SET sequence = excluded.sequence,
			response = excluded.response, via = excluded.via, calendar_id = excluded.calendar_id,
			event_id = excluded.event_id, outbox_id = excluded.outbox_id, responded_at = excluded.responded_at`,
		r.AccountID, r.UID, r.RecurrenceID, r.Sequence, string(r.Response), r.Via, r.CalendarID, r.EventID, r.OutboxID, fmtStamp(at))
	if err != nil {
		return fmt.Errorf("put response: %w", err)
	}
	return nil
}

// GetResponse returns the user's answer of a key; ErrNotFound without one.
func (s *Store) GetResponse(ctx context.Context, accountID, uid, recurrenceID string) (Response, error) {
	var r Response
	var resp, at string
	err := s.db.QueryRowContext(ctx, `SELECT account_id, uid, recurrence_id, sequence, response, via, calendar_id,
			event_id, outbox_id, responded_at FROM cal_responses WHERE account_id = ? AND uid = ? AND recurrence_id = ?`,
		accountID, uid, recurrenceID).Scan(&r.AccountID, &r.UID, &r.RecurrenceID, &r.Sequence, &resp, &r.Via,
		&r.CalendarID, &r.EventID, &r.OutboxID, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return Response{}, ErrNotFound
	}
	if err != nil {
		return Response{}, fmt.Errorf("get response: %w", err)
	}
	r.Response, r.RespondedAt = api.EventResponse(resp), parseStamp(at)
	return r, nil
}

// DeleteResponse forgets the user's answer of a key; no error without one.
func (s *Store) DeleteResponse(ctx context.Context, accountID, uid, recurrenceID string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM cal_responses WHERE account_id = ? AND uid = ? AND recurrence_id = ?`,
		accountID, uid, recurrenceID); err != nil {
		return fmt.Errorf("delete response: %w", err)
	}
	return nil
}

// invitationFacts are the columns derived next to an invitation row for
// its summary: the answered SEQUENCE (-1 = none) and its answer, whether a
// later cancel or a newer request of the UID is stored, and whether the
// UID is known (an event of the account, or an earlier invitation).
const invitationFacts = `,
	coalesce((SELECT r.sequence FROM cal_responses r
	           WHERE r.account_id = ci.account_id AND r.uid = ci.uid AND r.recurrence_id = ci.recurrence_id), -1),
	EXISTS (SELECT 1 FROM cal_invitations o
	         WHERE o.account_id = ci.account_id AND o.uid = ci.uid AND o.method = 'cancel'
	           AND o.sequence >= ci.sequence AND (o.recurrence_id = '' OR o.recurrence_id = ci.recurrence_id)),
	EXISTS (SELECT 1 FROM cal_invitations o
	         WHERE o.account_id = ci.account_id AND o.uid = ci.uid AND o.recurrence_id = ci.recurrence_id
	           AND o.method = 'request' AND o.sequence > ci.sequence),
	(EXISTS (SELECT 1 FROM cal_events e WHERE e.account_id = ci.account_id AND e.uid = ci.uid)
	 OR EXISTS (SELECT 1 FROM cal_invitations o
	             WHERE o.account_id = ci.account_id AND o.uid = ci.uid AND o.sequence < ci.sequence))`

type invitationFact struct {
	answeredSeq             int
	cancelled, newer, known bool
}

func scanInvitationWithFacts(row scanner, extra ...any) (Invitation, invitationFact, error) {
	var f invitationFact
	var cancelled, newer, known int
	inv, err := scanInvitation(row, append([]any{&f.answeredSeq, &cancelled, &newer, &known}, extra...)...)
	f.cancelled, f.newer, f.known = cancelled != 0, newer != 0, known != 0
	return inv, f, err
}

// InvitationSummaryOf derives the list summary of an invitation
// (MessageSummary.Invitation, docs/calendar.md §4.6) from its row and
// what else is stored: answeredSeq is the SEQUENCE the user answered (-1
// = none); cancelled: a cancel of the UID with at least its SEQUENCE is
// stored; newer: a request with a higher SEQUENCE is; known: the UID is
// known (an event, or an earlier invitation) — a request with SEQUENCE
// > 0 of a known UID is an update. now decides whether its event ended.
func InvitationSummaryOf(inv Invitation, answeredSeq int, cancelled, newer, known bool, now time.Time) api.InvitationSummary {
	var sum api.InvitationSummary
	if len(inv.Card) > 0 {
		_ = json.Unmarshal(inv.Card, &sum) // the card's static fields; a damaged one reads as invalid below
	}
	sum.Method = inv.Method
	if sum.Title == "" {
		sum.Title = inv.Title
	}
	if sum.Start == nil && !inv.AllDay && !inv.StartAt.IsZero() {
		t := inv.StartAt
		sum.Start = &t
	}
	if sum.End == nil && !inv.AllDay && !inv.EndAt.IsZero() {
		t := inv.EndAt
		sum.End = &t
	}
	if inv.AllDay {
		sum.AllDay = true
		if sum.StartDate == "" && !inv.StartAt.IsZero() {
			sum.StartDate = api.Date(inv.StartAt.Format("2006-01-02"))
		}
	}
	if sum.Organizer == nil && inv.Organizer != "" {
		sum.Organizer = &api.Address{Address: inv.Organizer}
	}
	switch inv.Method {
	case api.InvitationMethodRequest:
		sum.Kind = api.InvitationKindInvitation
		if inv.Sequence > 0 && known {
			sum.Kind = api.InvitationKindUpdate
		}
	case api.InvitationMethodCancel:
		sum.Kind = api.InvitationKindCancelled
	case api.InvitationMethodReply:
		sum.Kind = api.InvitationKindReply
	default:
		sum.Kind = api.InvitationKindOther
	}
	end := inv.EndAt
	if end.IsZero() {
		end = inv.StartAt
	}
	switch {
	case len(inv.Card) == 0 || !json.Valid(inv.Card):
		sum.State = api.InvitationInvalid
	case inv.Method == api.InvitationMethodCancel:
		sum.State = api.InvitationCancelled
	case inv.Method != api.InvitationMethodRequest:
		sum.State = api.InvitationProcessed
	case cancelled:
		sum.State = api.InvitationCancelled
	case sum.OwnEvent:
		// The user's own invitation: nothing to answer, never outdated.
		sum.State = api.InvitationProcessed
	case newer:
		sum.State = api.InvitationOutdated
	case answeredSeq >= inv.Sequence:
		sum.State = api.InvitationAnswered
	case !end.IsZero() && !end.After(now):
		sum.State = api.InvitationOutdated
	default:
		sum.State = api.InvitationUnanswered
	}
	return sum
}

// attachInvitations fills Message.Invitation of the listed rows that have
// an invitation: one primary-key lookup per row on cal_invitations, the
// LEFT JOIN of the listings (docs/calendar.md §2.6) done once per page.
func attachInvitations(ctx context.Context, q querier, msgs []Message) error {
	ptrs := make([]*Message, len(msgs))
	for i := range msgs {
		ptrs[i] = &msgs[i]
	}
	return attachInvitationsTo(ctx, q, ptrs)
}

// attachInvitationHeads is attachInvitations over the newest members of
// a thread listing, by id.
func attachInvitationHeads(ctx context.Context, q querier, heads map[string]Message) error {
	ptrs := make([]*Message, 0, len(heads))
	ms := make([]Message, 0, len(heads))
	for _, m := range heads {
		ms = append(ms, m)
	}
	for i := range ms {
		ptrs = append(ptrs, &ms[i])
	}
	if err := attachInvitationsTo(ctx, q, ptrs); err != nil {
		return err
	}
	for _, m := range ms {
		heads[m.ID] = m
	}
	return nil
}

// attachInvitationsTo is attachInvitations over pointers (a search page,
// the heads of a thread list).
func attachInvitationsTo(ctx context.Context, q querier, msgs []*Message) error {
	if len(msgs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(msgs))
	at := make(map[string][]*Message, len(msgs))
	for _, m := range msgs {
		if _, ok := at[m.ID]; !ok {
			ids = append(ids, m.ID)
		}
		at[m.ID] = append(at[m.ID], m)
	}
	now := time.Now()
	var found []foundInvitation
	for _, chunk := range chunkStrings(ids, 500) {
		rows, err := q.QueryContext(ctx, `SELECT `+invitationColumns+invitationFacts+`
			FROM cal_invitations ci WHERE ci.message_id IN (`+inPlaceholders(len(chunk))+`)`, toAny(chunk)...)
		if err != nil {
			return fmt.Errorf("list invitations: %w", err)
		}
		for rows.Next() {
			inv, f, err := scanInvitationWithFacts(rows)
			if err != nil {
				rows.Close()
				return fmt.Errorf("scan invitation: %w", err)
			}
			found = append(found, foundInvitation{inv, f})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("list invitations: %w", err)
		}
		for _, fi := range found {
			if err := answeredElsewhere(ctx, q, fi.inv, &fi.f); err != nil {
				return err
			}
			sum := InvitationSummaryOf(fi.inv, fi.f.answeredSeq, fi.f.cancelled, fi.f.newer, fi.f.known, now)
			for _, m := range at[fi.inv.MessageID] {
				s := sum
				m.Invitation = &s
			}
		}
		found = found[:0]
	}
	return nil
}

// ListedInvitation is one invitation of ListInvitations: the newest
// message of its UID with its summary, the message's date and the role of
// its folder ("none" for a folder without one; junk and trash never
// answer).
type ListedInvitation struct {
	Invitation
	Summary     api.InvitationSummary
	MessageDate time.Time
	FolderRole  api.FolderRole
}

// InvitationQuery selects ListInvitations' accounts: AccountIDs restrict
// (empty = all), ExcludeAccountIDs leave out (paused or disabled ones).
// State keeps only the invitations whose summary has it ("" = any).
// Hidden messages are never listed. Limit 0 = 200.
type InvitationQuery struct {
	AccountIDs        []string
	ExcludeAccountIDs []string
	State             api.InvitationState
	Limit             int
}

// ListInvitations returns one invitation per (account, UID): the newest
// stored message of it (by message date, then rowid), ordered by start
// (an invitation without a start last), each with its summary
// (InvitationSummaryOf at now), at most q.Limit of those in q.State.
func (s *Store) ListInvitations(ctx context.Context, q InvitationQuery, now time.Time) ([]ListedInvitation, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 200
	}
	where := ` WHERE m.hidden = 0`
	var args []any
	if len(q.AccountIDs) > 0 {
		where += ` AND ci.account_id IN (` + inPlaceholders(len(q.AccountIDs)) + `)`
		args = append(args, toAny(q.AccountIDs)...)
	}
	if len(q.ExcludeAccountIDs) > 0 {
		where += ` AND ci.account_id NOT IN (` + inPlaceholders(len(q.ExcludeAccountIDs)) + `)`
		args = append(args, toAny(q.ExcludeAccountIDs)...)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+invitationColumns+invitationFacts+`, m.date, coalesce(f.role, '')
		FROM (SELECT ci.message_id AS id, ROW_NUMBER() OVER (PARTITION BY ci.account_id, ci.uid
		             ORDER BY m.date DESC, m.rowid DESC) AS rn
		        FROM cal_invitations ci JOIN messages m ON m.id = ci.message_id`+where+`) p
		JOIN cal_invitations ci ON ci.message_id = p.id
		JOIN messages m ON m.id = ci.message_id
		LEFT JOIN folders f ON f.id = m.folder_id
		WHERE p.rn = 1
		ORDER BY ci.start_at = '', ci.start_at, ci.message_id`, args...)
	if err != nil {
		return nil, fmt.Errorf("list invitations: %w", err)
	}
	defer rows.Close()
	var out []ListedInvitation
	var all []listedRow
	for rows.Next() {
		var date, role string
		inv, f, err := scanInvitationWithFacts(rows, &date, &role)
		if err != nil {
			return nil, fmt.Errorf("scan invitation: %w", err)
		}
		all = append(all, listedRow{foundInvitation{inv, f}, date, role})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list invitations: %w", err)
	}
	rows.Close()
	for _, r := range all {
		if err := answeredElsewhere(ctx, s.db, r.inv, &r.f); err != nil {
			return nil, err
		}
		sum := InvitationSummaryOf(r.inv, r.f.answeredSeq, r.f.cancelled, r.f.newer, r.f.known, now)
		if q.State != "" && sum.State != q.State {
			continue
		}
		out = append(out, ListedInvitation{Invitation: r.inv, Summary: sum,
			MessageDate: parseStamp(r.date), FolderRole: api.FolderRole(r.role)})
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

type foundInvitation struct {
	inv Invitation
	f   invitationFact
}

type listedRow struct {
	foundInvitation
	date, role string
}

// answeredElsewhere is the answer given in another client (Outlook, a
// phone): a request without a local answer for its SEQUENCE whose linked
// event (InvitationLinkedEvent) has the user's response accepted,
// tentative or declined counts as answered at its SEQUENCE. A local
// answer wins (it is checked first); needsAction or none stays
// unanswered. Only a summary that could still be unanswered is looked up.
func answeredElsewhere(ctx context.Context, q querier, inv Invitation, f *invitationFact) error {
	if inv.Method != api.InvitationMethodRequest || len(inv.Card) == 0 || f.cancelled || f.newer || f.answeredSeq >= inv.Sequence {
		return nil
	}
	var own struct {
		OwnEvent bool `json:"ownEvent"`
	}
	if json.Unmarshal(inv.Card, &own) == nil && own.OwnEvent {
		return nil
	}
	ev, ok, err := linkedEvent(ctx, q, inv.AccountID, inv.UID, InvitationRecurrenceKey(inv.RecurrenceID), inv.Organizer, false)
	if err != nil || !ok {
		return err
	}
	if inv.RecurrenceID != "" && ev.Kind == EventMaster {
		return nil // the series' answer does not answer one instance's invitation
	}
	if AnsweredResponse(ev.MyResponse) {
		f.answeredSeq = inv.Sequence
	}
	return nil
}

// AnsweredResponse reports a response that answers an invitation.
func AnsweredResponse(r api.EventResponse) bool {
	return r == api.ResponseAccepted || r == api.ResponseTentative || r == api.ResponseDeclined
}

// InvitationRecurrenceKey is the stored form (FormatRecurrenceID) of an
// invitation's recurrence id ("" = the series); a value that does not
// read is kept as it is.
func InvitationRecurrenceKey(rid string) string {
	if rid == "" {
		return ""
	}
	if t, err := time.Parse("2006-01-02", rid); err == nil {
		return FormatRecurrenceID(t, true)
	}
	if t, err := time.Parse(time.RFC3339, rid); err == nil {
		return FormatRecurrenceID(t, false)
	}
	return rid
}

// InvitationLinkedEvent is the stored event an invitation is answered
// through (docs/calendar.md §4.6, D18), the one rule of the card, the
// summaries and invitation.list: the events of the UID in every account,
// firstAccount's first, then by account order; in each account the row
// the invitation names (the override of key, else the first master or
// single row, else the first row, by calendar, remote id and recurrence
// id). It counts only when the user does not organise it, its ORGANIZER
// is organizer (lower case), its account is enabled and its calendar not
// disabled (sync_mode "disabled"); otherwise the next account is tried.
// The event comes without Raw.
//
// own (the invitation is the user's own, InvitationSummary.ownEvent)
// accepts the event the user organises instead: its Sent copy links to it.
func (s *Store) InvitationLinkedEvent(ctx context.Context, firstAccount, uid, key, organizer string, own bool) (Event, bool, error) {
	return linkedEvent(ctx, s.db, firstAccount, uid, key, organizer, own)
}

func linkedEvent(ctx context.Context, q querier, firstAccount, uid, key, organizer string, own bool) (Event, bool, error) {
	if uid == "" || organizer == "" {
		return Event{}, false, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT e.id, e.account_id, e.kind, e.recurrence_id, e.i_am_organizer, e.organizer_json,
			a.enabled, c.sync_mode, e.start_at, e.start_date, e.all_day
		FROM cal_events e JOIN cal_calendars c ON c.id = e.calendar_id JOIN accounts a ON a.id = e.account_id
		WHERE e.uid = ?
		ORDER BY e.account_id != ?, a.position, a.created_at, a.id,
			e.calendar_id, e.remote_id, e.kind = 'override', e.recurrence_id, e.id`, uid, firstAccount)
	if err != nil {
		return Event{}, false, fmt.Errorf("linked event: %w", err)
	}
	type cand struct {
		id, account, kind, rid, organizer, mode string
		start, startDate                        string
		mine, enabled, allDay                   bool
	}
	var cands []cand
	for rows.Next() {
		var c cand
		var mine, enabled, allDay int
		if err := rows.Scan(&c.id, &c.account, &c.kind, &c.rid, &mine, &c.organizer, &enabled, &c.mode,
			&c.start, &c.startDate, &allDay); err != nil {
			rows.Close()
			return Event{}, false, fmt.Errorf("linked event: %w", err)
		}
		c.mine, c.enabled, c.allDay = mine != 0, enabled != 0, allDay != 0
		cands = append(cands, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Event{}, false, fmt.Errorf("linked event: %w", err)
	}
	for i := 0; i < len(cands); {
		j := i
		for j < len(cands) && cands[j].account == cands[i].account {
			j++
		}
		group := cands[i:j]
		i = j
		pick := -1
		if key != "" {
			pick = instanceRow(group, func(c cand) bool { return c.kind == string(EventOverride) && c.rid == key })
			if pick < 0 {
				// A Graph instance or exception is stored as a single row
				// whose start is the instance (the RECURRENCE-ID): compared
				// as an instant, an all-day one by its date.
				pick = instanceRow(group, func(c cand) bool {
					if c.kind == string(EventMaster) {
						return false
					}
					if len(key) == len("2006-01-02") {
						return c.allDay && c.startDate == key
					}
					return !c.allDay && c.start != "" && c.start == key
				})
			}
		}
		if pick < 0 {
			// The series (Graph stores its instances as single rows
			// beside the master).
			pick = instanceRow(group, func(c cand) bool { return c.kind == string(EventMaster) })
		}
		if pick < 0 {
			for k, c := range group {
				if c.kind != string(EventOverride) {
					pick = k
					break
				}
			}
		}
		if pick < 0 {
			pick = 0
		}
		c := group[pick]
		var org api.Address
		if c.mine != own || !c.enabled || c.mode == "disabled" || c.organizer == "" || c.organizer == "null" ||
			json.Unmarshal([]byte(c.organizer), &org) != nil || !strings.EqualFold(strings.TrimSpace(org.Address), organizer) {
			continue
		}
		evs, err := eventsByIDs(ctx, q, []string{c.id})
		if err != nil {
			return Event{}, false, err
		}
		if ev, ok := evs[c.id]; ok {
			return ev, true, nil
		}
	}
	return Event{}, false, nil
}

// instanceRow is the index of the first element ok accepts, -1 for none.
func instanceRow[T any](list []T, ok func(T) bool) int {
	for i, x := range list {
		if ok(x) {
			return i
		}
	}
	return -1
}

// ResponseByEvent returns the user's answer recorded for an invitation's
// UID through a stored event (cal_responses.event_id): event.respond keys
// an answer through a Graph instance stored as a single row by the
// instance's row, not by its recurrence id. ErrNotFound without one.
func (s *Store) ResponseByEvent(ctx context.Context, accountID, uid, eventID string) (Response, error) {
	var rid string
	err := s.db.QueryRowContext(ctx, `SELECT recurrence_id FROM cal_responses WHERE account_id = ? AND uid = ? AND event_id = ?
		ORDER BY responded_at DESC LIMIT 1`, accountID, uid, eventID).Scan(&rid)
	if errors.Is(err, sql.ErrNoRows) {
		return Response{}, ErrNotFound
	}
	if err != nil {
		return Response{}, fmt.Errorf("response by event: %w", err)
	}
	return s.GetResponse(ctx, accountID, uid, rid)
}
