// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package caldav

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

// StatusError is a non-2xx answer of the server: its status, a cleaned
// text (never the server's body verbatim, never a credential), the
// precondition named by a DAV:error body (RFC 4918 §16, e.g.
// "valid-sync-token", "no-uid-conflict") and, for 429/503, how long the
// server asked to wait (capped at RetryAfterMax).
type StatusError struct {
	Method       string
	Status       int
	Message      string
	Precondition string
	RetryAfter   time.Duration
}

func (e *StatusError) Error() string {
	s := fmt.Sprintf("caldav: %s: HTTP %d", e.Method, e.Status)
	if e.Precondition != "" {
		s += " (" + e.Precondition + ")"
	}
	if e.Message != "" {
		s += ": " + e.Message
	}
	return s
}

// Errors the client returns besides *StatusError and *api.Error.
var (
	// ErrInvalidSyncToken: sync-collection refused the stored token (403
	// or 409 with DAV:valid-sync-token, or any 403/409 of that report);
	// the caller makes a full pass.
	ErrInvalidSyncToken = errors.New("caldav: sync token no longer valid")
	// ErrTooManyResources: a listing named more than MaxResources hrefs.
	ErrTooManyResources = errors.New("caldav: more resources than the limit")
	// ErrTooLarge: a response body over its cap.
	ErrTooLarge = errors.New("caldav: response over the size limit")
	// ErrOtherOrigin: the server named a URL on another origin where the
	// client would have to send credentials (a principal or home set on
	// another host); see Options.Origins.
	ErrOtherOrigin = errors.New("caldav: the server named another server")
)

// Status is the HTTP status of err when it is a *StatusError, else 0.
func Status(err error) int {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Status
	}
	return 0
}

// IsUnauthorized reports a 401: the password is not accepted.
func IsUnauthorized(err error) bool { return Status(err) == http.StatusUnauthorized }

// IsForbidden reports a 403: the user may not do this to this resource
// (a read-only calendar on PUT/DELETE, a collection the user lost access
// to). The provider maps it to readOnly or skips the calendar.
func IsForbidden(err error) bool { return Status(err) == http.StatusForbidden }

// IsGone reports a 404 or 410: the resource is not there (any more).
func IsGone(err error) bool {
	s := Status(err)
	return s == http.StatusNotFound || s == http.StatusGone
}

// IsConflict reports a 412: If-Match / If-None-Match failed (the event
// changed or exists on the server).
func IsConflict(err error) bool { return Status(err) == http.StatusPreconditionFailed }

// RetryAfter is the wait a throttled answer asked for (≤ RetryAfterMax),
// 0 for anything else.
func RetryAfter(err error) time.Duration {
	var se *StatusError
	if errors.As(err, &se) {
		return se.RetryAfter
	}
	return 0
}

// ToAPIError maps a client failure to the contract: *api.Error passes
// through; a StatusError becomes authFailed (401), readOnly (403),
// eventGone (404, 410), conflict (412), serverTimeout (408, 429, 504) or
// serverError (507, other 5xx, the rest); ErrInvalidSyncToken,
// ErrTooManyResources and ErrTooLarge are serverError, ErrOtherOrigin and a server
// without calendar-access serverError; context cancellation is cancelled, a deadline
// serverTimeout; any other error networkError. Nil for nil. The message
// never carries a credential.
func ToAPIError(err error) *api.Error {
	if err == nil {
		return nil
	}
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae
	}
	var se *StatusError
	if errors.As(err, &se) {
		msg := transport.CleanMessage(se.Error())
		switch se.Status {
		case http.StatusUnauthorized:
			return api.NewError(api.CodeAuthFailed, "%s", msg)
		case http.StatusForbidden:
			return api.NewError(api.CodeReadOnly, "%s", msg)
		case http.StatusNotFound, http.StatusGone:
			return api.NewError(api.CodeEventGone, "%s", msg)
		case http.StatusPreconditionFailed:
			return api.NewError(api.CodeConflict, "%s", msg)
		case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusGatewayTimeout:
			return api.NewError(api.CodeServerTimeout, "%s", msg)
		}
		return api.NewError(api.CodeServerError, "%s", msg)
	}
	switch {
	case errors.Is(err, context.Canceled):
		return api.NewError(api.CodeCancelled, "cancelled")
	case errors.Is(err, context.DeadlineExceeded):
		return api.NewError(api.CodeServerTimeout, "caldav: request timed out")
	case errors.Is(err, ErrInvalidSyncToken), errors.Is(err, ErrTooManyResources),
		errors.Is(err, ErrTooLarge), errors.Is(err, ErrOtherOrigin), errors.Is(err, errNotCalDAV):
		return api.NewError(api.CodeServerError, "%s", transport.CleanMessage(err.Error()))
	}
	return api.NewError(api.CodeNetworkError, "%s", transport.CleanMessage(err.Error()))
}

// malformed is the error of an answer that cannot be read.
func malformed(format string, args ...any) *api.Error {
	return api.NewError(api.CodeMalformedCalendar, "caldav: "+format, args...)
}

// classifyTransport maps a transport failure (unwrapping *url.Error):
// cancelled, serverTimeout, tlsError with TLSErrorData, or networkError.
func classifyTransport(ctx context.Context, err error) error {
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	e := transport.Classify(ctx, transport.StageDial, err)
	if e.Code != api.CodeCancelled && !strings.HasPrefix(e.Message, "caldav: ") {
		e.Message = "caldav: " + e.Message
	}
	return e
}
