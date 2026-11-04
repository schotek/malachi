// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"context"
	"errors"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/client"
)

// WhyOf is why a write or a load of the board failed, as far as err says
// (macOS Board.Text's failureReason): board.setCommitment's caseNotFound
// names the promise, not the case (docs/api.md §2); no connection is a
// mail backend that is not running; a call that ran out of time or was
// cancelled did not answer in time; the daemon's error codes map to their
// reasons; anything else is WhyNone. Never the daemon's own message (it is
// not for the user).
func WhyOf(err error, a Action) Why {
	var e *api.Error
	isRPC := errors.As(err, &e)
	switch {
	case a == ActionCommitment && isRPC && e.Code == api.CodeCaseNotFound:
		return WhyPromiseGone
	case errors.Is(err, client.ErrDisconnected):
		return WhyBackendDown
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return WhyTimeout
	case !isRPC:
		return WhyNone
	}
	switch e.Code {
	case api.CodeCaseNotFound:
		return WhyCaseGone
	case api.CodeInvalidArgument:
		return WhyNotAccepted
	case api.CodeMethodNotFound, api.CodeNotImplemented:
		return WhyNoBoard
	case api.CodeStorageError:
		return WhyNotSaved
	case api.CodeDraftNotFound:
		return WhyDraftGone
	}
	return WhyNone
}

// FailedText is the toast of a failed write or load: Failed with the
// reason WhyOf finds in err.
func FailedText(a Action, err error, tr Translator) string {
	return Failed(a, WhyOf(err, a), tr)
}

// isDisconnected reports an error of a call made without a connection, or
// whose connection dropped.
func isDisconnected(err error) bool { return errors.Is(err, client.ErrDisconnected) }

// isUnsupported reports a daemon without the method: an older backend.
func isUnsupported(err error) bool {
	var e *api.Error
	return errors.As(err, &e) && (e.Code == api.CodeMethodNotFound || e.Code == api.CodeNotImplemented)
}
