// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package llm

import (
	"context"
	"errors"
	"fmt"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Error is a failed call: its class (what a client words), the HTTP
// status when the provider answered, RetryAfter in seconds when it said
// so (429, 529), and a technical message that never carries the key,
// the request or the answer's text beyond the provider's own error
// message, cleaned and cut.
type Error struct {
	Class      api.AssistantFailureClass
	Status     int
	RetryAfter int
	Message    string
}

func (e *Error) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("llm: %s (HTTP %d): %s", e.Class, e.Status, e.Message)
	}
	return fmt.Sprintf("llm: %s: %s", e.Class, e.Message)
}

// ClassOf returns the class of err: an *Error's own, FailureStopped for
// a cancelled context, FailureTimeout for a deadline, FailureNetwork
// otherwise.
func ClassOf(err error) api.AssistantFailureClass {
	var e *Error
	if errors.As(err, &e) {
		return e.Class
	}
	switch {
	case errors.Is(err, context.Canceled):
		return api.FailureStopped
	case errors.Is(err, context.DeadlineExceeded):
		return api.FailureTimeout
	}
	return api.FailureNetwork
}

// ToAPIError maps err to the contract's codes for account.test and
// assistant.providerModels: a refused key is authFailed, a key without
// the permission or a refused request serverError (with the status in
// the message), a rate limit or an overloaded provider serverTimeout, a
// TLS failure tlsError with its data, no connection networkError, a
// deadline serverTimeout, a cancelled context cancelled. An *api.Error
// passes through.
func ToAPIError(err error) *api.Error {
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae
	}
	var e *Error
	if errors.As(err, &e) {
		switch e.Class {
		case api.FailureAuth:
			return api.NewError(api.CodeAuthFailed, "the provider refused the key")
		case api.FailureRateLimit, api.FailureOverloaded:
			return api.NewError(api.CodeServerTimeout, "the provider is busy (HTTP %d)", e.Status)
		case api.FailureNetwork:
			return api.NewError(api.CodeNetworkError, "%s", e.Message)
		case api.FailureTimeout:
			return api.NewError(api.CodeServerTimeout, "%s", e.Message)
		case api.FailureStopped:
			return api.NewError(api.CodeCancelled, "cancelled")
		}
		if e.Status != 0 {
			return api.NewError(api.CodeServerError, "the provider answered HTTP %d", e.Status)
		}
		return api.NewError(api.CodeServerError, "%s", e.Message)
	}
	switch {
	case errors.Is(err, context.Canceled):
		return api.NewError(api.CodeCancelled, "cancelled")
	case errors.Is(err, context.DeadlineExceeded):
		return api.NewError(api.CodeServerTimeout, "the provider did not answer in time")
	}
	return api.NewError(api.CodeNetworkError, "%s", err.Error())
}
