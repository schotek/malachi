// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import "fmt"

// ErrorCode is the numeric error enumeration of the contract. Codes are
// stable: never renumber, only append.
//
// Negative codes are reserved by the JSON-RPC 2.0 specification. Application
// codes are positive and grouped by hundreds.
type ErrorCode int

const (
	// JSON-RPC 2.0 reserved codes.
	CodeParseError     ErrorCode = -32700
	CodeInvalidRequest ErrorCode = -32600
	CodeMethodNotFound ErrorCode = -32601
	CodeInvalidParams  ErrorCode = -32602
	CodeInternalError  ErrorCode = -32603

	// 1000–1099: general.
	CodeNotImplemented  ErrorCode = 1000
	CodeInvalidArgument ErrorCode = 1001
	CodeConflict        ErrorCode = 1002 // optimistic-concurrency conflict (drafts)
	CodeCancelled       ErrorCode = 1003
	CodeUnavailable     ErrorCode = 1004 // backend busy / shutting down

	// 1100–1199: not found.
	CodeAccountNotFound ErrorCode = 1100
	CodeFolderNotFound  ErrorCode = 1101
	CodeMessageNotFound ErrorCode = 1102
	CodeThreadNotFound  ErrorCode = 1103
	CodeDraftNotFound   ErrorCode = 1104
	// CodeAttachmentNotFound: unknown id, another account's, or already bound
	// to a different draft.
	CodeAttachmentNotFound ErrorCode = 1105

	// 1200–1299: authentication.
	CodeAuthRequired ErrorCode = 1200 // credentials missing or token expired; see notify.authRequired
	CodeAuthFailed   ErrorCode = 1201 // server rejected the credentials
	CodeKeyringError ErrorCode = 1202 // secret storage unavailable

	// 1300–1399: network and remote servers.
	CodeOffline       ErrorCode = 1300
	CodeNetworkError  ErrorCode = 1301
	CodeServerError   ErrorCode = 1302 // IMAP/SMTP server returned an error
	CodeTLSError      ErrorCode = 1303
	CodeServerTimeout ErrorCode = 1304

	// 1400–1499: local storage.
	CodeStorageError    ErrorCode = 1400
	CodeMigrationFailed ErrorCode = 1401

	// 1500–1599: content.
	CodeMalformedMessage ErrorCode = 1500 // MIME could not be parsed even leniently
	CodeSanitizeFailed   ErrorCode = 1501 // sanitiser refused the input; body withheld
	// CodeAttachmentTooBig: over MaxAttachmentBytes, MaxDraftAttachmentBytes
	// or MaxAttachmentDataBytes; Error.Data = {"limit": n, "size": n}.
	CodeAttachmentTooBig ErrorCode = 1502
)

// String returns the stable symbolic name of the code.
func (c ErrorCode) String() string {
	if s, ok := codeNames[c]; ok {
		return s
	}
	return fmt.Sprintf("unknown(%d)", int(c))
}

var codeNames = map[ErrorCode]string{
	CodeParseError:         "parseError",
	CodeInvalidRequest:     "invalidRequest",
	CodeMethodNotFound:     "methodNotFound",
	CodeInvalidParams:      "invalidParams",
	CodeInternalError:      "internalError",
	CodeNotImplemented:     "notImplemented",
	CodeInvalidArgument:    "invalidArgument",
	CodeConflict:           "conflict",
	CodeCancelled:          "cancelled",
	CodeUnavailable:        "unavailable",
	CodeAccountNotFound:    "accountNotFound",
	CodeFolderNotFound:     "folderNotFound",
	CodeMessageNotFound:    "messageNotFound",
	CodeThreadNotFound:     "threadNotFound",
	CodeDraftNotFound:      "draftNotFound",
	CodeAttachmentNotFound: "attachmentNotFound",
	CodeAuthRequired:       "authRequired",
	CodeAuthFailed:         "authFailed",
	CodeKeyringError:       "keyringError",
	CodeOffline:            "offline",
	CodeNetworkError:       "networkError",
	CodeServerError:        "serverError",
	CodeTLSError:           "tlsError",
	CodeServerTimeout:      "serverTimeout",
	CodeStorageError:       "storageError",
	CodeMigrationFailed:    "migrationFailed",
	CodeMalformedMessage:   "malformedMessage",
	CodeSanitizeFailed:     "sanitizeFailed",
	CodeAttachmentTooBig:   "attachmentTooBig",
}

// Error is the JSON-RPC error object. It implements the Go error interface so
// that service implementations can return it directly.
type Error struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
	Data    any       `json:"data,omitempty"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s (%d): %s", e.Code, int(e.Code), e.Message)
}

// NewError builds an Error with a formatted message.
func NewError(code ErrorCode, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// ErrNotImplemented is returned by every method that is still a stub.
var ErrNotImplemented = &Error{Code: CodeNotImplemented, Message: "not implemented"}
