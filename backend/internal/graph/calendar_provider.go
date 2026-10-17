// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/calsync"
	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The calendar of a Microsoft Graph account (docs/calendar.md §3.6): a
// calsync.Provider over the account's Client.

// PersonalTenantID is the Entra tenant of every personal Microsoft account
// (outlook.com, hotmail.com, live.com): the "tid" of their tokens. Core
// sets CalendarProviderOptions.Personal from it.
const PersonalTenantID = "9188040d-6c67-4c5b-b112-36a304b66dad"

// IsPersonalTenant reports whether a token's tenant id is the one of
// personal Microsoft accounts.
func IsPersonalTenant(tid string) bool {
	return strings.EqualFold(strings.TrimSpace(tid), PersonalTenantID)
}

// Known is what the engine has stored of one calendar, for incremental
// work: Objects maps an object's remote id (a single event or series
// master) to its stored ETag; Instances maps the remote id of every stored
// occurrence (Graph occurrence or exception id) to its object's remote id.
type Known struct {
	Objects   map[string]string
	Instances map[string]string
}

// CalendarProviderOptions configures NewCalendarProvider.
type CalendarProviderOptions struct {
	// AccountID names the account in calendarConsentRequired data.
	AccountID api.AccountID
	// SignIn: the account signs in through the daemon (GraphConfig source
	// daemon), so account.oauthStart with calendar can grant the scope;
	// false for GOA accounts (the desktop's Online Accounts settings).
	SignIn bool
	// Personal: a personal Microsoft account (token tenant
	// PersonalTenantID). It has no free/busy of other people.
	Personal bool
	// Me are the account's own addresses (case-insensitive): they decide
	// Attendee.Me and whether a calendar's owner is someone else.
	Me []string
	// Known returns the stored state of a calendar (nil: nothing known;
	// a delta pass then cannot tell which series a removed occurrence
	// belonged to, and a list pass upserts everything with Full set).
	Known func(ctx context.Context, cal calsync.StoredCalendar) (Known, error)
	// Window is the account's stored window, used to expand series in a
	// write's result (nil: 30 days back to api.CalendarFutureDays ahead).
	Window func() calsync.Window
	// Now is the clock (nil = time.Now).
	Now func() time.Time
}

// CalendarProvider is the Graph calendar source of one account.
type CalendarProvider struct {
	c    *Client
	opts CalendarProviderOptions
	me   map[string]bool

	mu sync.Mutex
	// onlineMeetings: the default calendar lists an online meeting
	// provider (learnt from Calendars).
	onlineMeetings bool
	// noFreeBusy: getSchedule was refused as a whole (a personal account
	// that Personal did not name).
	noFreeBusy bool
}

var _ calsync.Provider = (*CalendarProvider)(nil)

// graphDataVersion is the version of how Graph events map onto stored
// rows (calsync.Versioned): bump whenever the mapping of Graph events to
// stored rows changes, so that the engine lists every calendar again.
// 2: all-day dates, deduplicated locations, the user's own response in
// calendars of others. 3: an item naming its series is always an
// instance of it (an override when it is an exception), never a single.
const graphDataVersion = 3

// DataVersion implements calsync.Versioned.
func (p *CalendarProvider) DataVersion() int { return graphDataVersion }

// NewCalendarProvider returns the calendar source of the account client
// talks to. The client should share the account's Limiter with the mail
// syncer and the outbox (Options.Limiter); calendar passes then run at
// PriorityLow.
func NewCalendarProvider(client *Client, opts CalendarProviderOptions) calsync.Provider {
	return newCalendarProvider(client, opts)
}

func newCalendarProvider(client *Client, opts CalendarProviderOptions) *CalendarProvider {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	p := &CalendarProvider{c: client, opts: opts, me: map[string]bool{}}
	for _, a := range opts.Me {
		if a = strings.ToLower(strings.TrimSpace(a)); a != "" {
			p.me[a] = true
		}
	}
	return p
}

// Features: free/busy of others unless the account is personal,
// proposing a new time, invitations sent by Exchange, online meetings
// when the default calendar allows a provider.
func (p *CalendarProvider) Features() api.CalendarFeatures {
	p.mu.Lock()
	defer p.mu.Unlock()
	return api.CalendarFeatures{
		FreeBusy:       !p.opts.Personal && !p.noFreeBusy,
		ProposeNewTime: true,
		Invitations:    api.InvitationsViaServer,
		OnlineMeetings: p.onlineMeetings,
	}
}

func (p *CalendarProvider) isMe(addr string) bool {
	return addr != "" && p.me[strings.ToLower(strings.TrimSpace(addr))]
}

func (p *CalendarProvider) window() calsync.Window {
	if p.opts.Window != nil {
		if w := p.opts.Window(); w.From.Before(w.To) {
			return w
		}
	}
	now := p.opts.Now().UTC()
	return calsync.Window{From: now.AddDate(0, 0, -30), To: now.AddDate(0, 0, api.CalendarFutureDays)}
}

func (p *CalendarProvider) known(ctx context.Context, cal calsync.StoredCalendar) (Known, bool) {
	if p.opts.Known == nil {
		return Known{}, false
	}
	k, err := p.opts.Known(ctx, cal)
	if err != nil {
		return Known{}, false
	}
	return k, true
}

// ---------------------------------------------------------------------------
// Requests
// ---------------------------------------------------------------------------

const (
	// calendarPrefer asks for immutable ids, UTC times (all-day events
	// arrive as midnight-to-midnight UTC) and full pages.
	calendarPrefer = `IdType="ImmutableId", outlook.timezone="UTC", odata.maxpagesize=200`
	// maxCalendarResponse bounds one decoded calendar response.
	maxCalendarResponse = 32 << 20
)

type calReq struct {
	method  string
	url     string
	body    any // JSON; nil = none
	ifMatch string
	out     any // nil = discard
	prio    Priority
}

// calDo runs one calendar request with the client's policy (token, one
// refresh on 401, bounded Retry-After) and the calendar's own headers
// (Prefer with outlook.timezone, If-Match); the answer is decoded into
// out with a size cap. Errors are *StatusError, *api.Error or ctx's.
func (c *Client) calDo(ctx context.Context, r calReq) error {
	var payload []byte
	if r.body != nil {
		b, err := json.Marshal(r.body)
		if err != nil {
			return api.NewError(api.CodeInvalidArgument, "graph: encode request: %s", transport.CleanMessage(err.Error()))
		}
		payload = b
	}
	release, err := c.acquire(ctx, r.prio)
	if err != nil {
		return err
	}
	defer release()
	refreshed, throttled := false, 0
	for {
		token, err := c.token(ctx)
		if err != nil {
			return err
		}
		resp, err := c.calOnce(ctx, r, payload, token)
		if err != nil {
			return err
		}
		switch {
		case resp.StatusCode == http.StatusUnauthorized && !refreshed:
			drain(resp)
			refreshed = true
			c.invalidate()
			continue
		case (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable) && throttled < throttleRetries:
			se := statusError(resp)
			throttled++
			wait := se.RetryAfter
			if wait <= 0 {
				wait = 5 * time.Second
			}
			if wait > retryAfterMax {
				return se
			}
			if err := c.sleep(ctx, wait); err != nil {
				return err
			}
			continue
		case resp.StatusCode < 200 || resp.StatusCode >= 300:
			se := statusError(resp)
			se.Message = strings.ReplaceAll(se.Message, token, "***")
			return se
		}
		defer resp.Body.Close()
		if r.out == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBody))
			return nil
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, maxCalendarResponse+1))
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return classifyTransport(err)
		}
		if len(raw) > maxCalendarResponse {
			return api.NewError(api.CodeServerError, "graph: calendar response over %d bytes", maxCalendarResponse)
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			return nil
		}
		if err := json.Unmarshal(raw, r.out); err != nil {
			return api.NewError(api.CodeMalformedCalendar, "graph: malformed calendar response: %s", transport.CleanMessage(err.Error()))
		}
		return nil
	}
}

func (c *Client) calOnce(ctx context.Context, r calReq, payload []byte, token string) (*http.Response, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	rctx, cancel := context.WithTimeout(ctx, requestTimeout)
	req, err := http.NewRequestWithContext(rctx, r.method, c.URL(r.url), body)
	if err != nil {
		cancel()
		return nil, api.NewError(api.CodeInvalidArgument, "graph: bad request: %s", transport.CleanMessage(err.Error()))
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Prefer", calendarPrefer)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if r.ifMatch != "" {
		req.Header.Set("If-Match", r.ifMatch)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		cancel()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, classifyTransport(err)
	}
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

func statusOf(err error) (*StatusError, bool) {
	var se *StatusError
	if errors.As(err, &se) {
		return se, true
	}
	return nil, false
}

// isAccessDenied reports Graph's answer to a token without the calendar
// scope (or with consent withdrawn).
func isAccessDenied(err error) bool {
	se, ok := statusOf(err)
	return ok && se.Status == http.StatusForbidden &&
		(strings.EqualFold(se.Code, "ErrorAccessDenied") || strings.EqualFold(se.Code, "Authorization_RequestDenied") ||
			strings.EqualFold(se.Code, "AccessDenied"))
}

func isRecurrenceRefused(err error) bool {
	se, ok := statusOf(err)
	return ok && (strings.EqualFold(se.Code, "ErrorInvalidRecurrencePattern") ||
		strings.EqualFold(se.Code, "ErrorRecurrenceHasNoOccurrence") ||
		strings.EqualFold(se.Code, "ErrorOccurrenceCrossingBoundary"))
}

func isItemGone(err error) bool {
	se, ok := statusOf(err)
	return ok && (se.Status == http.StatusNotFound || se.Status == http.StatusGone || strings.EqualFold(se.Code, "ErrorItemNotFound"))
}

func isPrecondition(err error) bool {
	se, ok := statusOf(err)
	return ok && (se.Status == http.StatusPreconditionFailed ||
		(se.Status == http.StatusConflict && strings.EqualFold(se.Code, "ErrorIrresolvableConflict")))
}

func (p *CalendarProvider) consentError(err error) *api.Error {
	e := api.NewError(api.CodeCalendarConsentRequired, "graph: calendar access denied: %s", transport.CleanMessage(err.Error()))
	e.Data = api.CalendarConsentRequiredData{AccountID: p.opts.AccountID, SignIn: p.opts.SignIn}
	return e
}

// readError maps a failure of a reading call (calendars, sync, range).
func (p *CalendarProvider) readError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return api.NewError(api.CodeCancelled, "cancelled")
	}
	if isAccessDenied(err) {
		return p.consentError(err)
	}
	return ToAPIError(err)
}

// writeError maps a failure of a write: access denied is consent (the
// calendar's own access was checked before: a read-only calendar never
// gets here), a missing item eventGone, a failed precondition conflict,
// a refused recurrence recurrenceUnsupported.
func (p *CalendarProvider) writeError(err error) error {
	if err == nil {
		return nil
	}
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae
	}
	if errors.Is(err, context.Canceled) {
		return api.NewError(api.CodeCancelled, "cancelled")
	}
	msg := transport.CleanMessage(err.Error())
	switch {
	case isAccessDenied(err):
		return p.consentError(err)
	case isPrecondition(err):
		return api.NewError(api.CodeConflict, "graph: %s", msg)
	case isRecurrenceRefused(err):
		return api.NewError(api.CodeRecurrenceUnsupported, "graph: %s", msg)
	case isItemGone(err):
		return api.NewError(api.CodeEventGone, "graph: %s", msg)
	}
	if se, ok := statusOf(err); ok && se.Status == http.StatusBadRequest {
		return api.NewError(api.CodeInvalidArgument, "graph: %s", msg)
	}
	return ToAPIError(err)
}
