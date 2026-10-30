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
	// CodeConflict: optimistic-concurrency conflict (drafts); for an event
	// write whose etag is not the server's current one, Error.Data is a
	// ConflictEventData with the current event.
	CodeConflict    ErrorCode = 1002
	CodeCancelled   ErrorCode = 1003
	CodeUnavailable ErrorCode = 1004 // backend busy / shutting down
	// CodeUnauthenticated: the RPC connection has not completed the
	// handshake (docs/api.md §1.4), or the handshake failed; the daemon
	// closes the connection after this answer. It is not about mail
	// accounts, whose sign-in problems are the 1200s.
	CodeUnauthenticated ErrorCode = 1005
	// CodeReadOnly: the calendar or event cannot be changed by the user (a
	// read-only or shared calendar, a birthdays/holidays calendar, a change
	// only the organiser may make).
	CodeReadOnly ErrorCode = 1006
	// CodeInvitationsUnavailable: the event has attendees, but neither the
	// server schedules nor a mail account can send the invitations
	// (CalDAVConfig.MailAccountID empty or not a compose account).
	CodeInvitationsUnavailable ErrorCode = 1007

	// 1100–1199: not found.
	CodeAccountNotFound ErrorCode = 1100
	CodeFolderNotFound  ErrorCode = 1101
	CodeMessageNotFound ErrorCode = 1102
	CodeThreadNotFound  ErrorCode = 1103
	CodeDraftNotFound   ErrorCode = 1104
	// CodeAttachmentNotFound: unknown id, another account's, or already bound
	// to a different draft.
	CodeAttachmentNotFound ErrorCode = 1105
	// CodeCaseNotFound: no board case (or, for board.setCommitment, no
	// commitment) with that id; the thread left the board or the account
	// is gone (docs/api.md §4.13).
	CodeCaseNotFound ErrorCode = 1106
	// CodeCalendarNotFound: no calendar with that id (removed, its account
	// gone or its sync switched off).
	CodeCalendarNotFound ErrorCode = 1107
	// CodeEventNotFound: no stored event with that id, or the recurrence id
	// names no occurrence of it.
	CodeEventNotFound ErrorCode = 1108
	// CodeCrmItemNotFound: a CRM item named in crm.save (link, superior)
	// is not an item of that CRM account, or the saved message's item is
	// gone from the CRM (crm.remove).
	CodeCrmItemNotFound ErrorCode = 1109
	// CodeContactNotFound: no stored contact with that id (deleted, its
	// book gone or its account's contacts switched off).
	CodeContactNotFound ErrorCode = 1110
	// CodeAddressBookNotFound: no address book with that id.
	CodeAddressBookNotFound ErrorCode = 1111

	// 1200–1299: authentication.
	CodeAuthRequired ErrorCode = 1200 // credentials missing or token expired; see notify.authRequired
	CodeAuthFailed   ErrorCode = 1201 // server rejected the credentials
	CodeKeyringError ErrorCode = 1202 // secret storage unavailable
	// CodeOAuthClientMissing: the backend's own sign-in needs an OAuth
	// client id for the provider and none is configured (config.toml
	// [oauth2.*], the account's clientId, or a built-in one).
	CodeOAuthClientMissing ErrorCode = 1203
	// CodeCalendarConsentRequired: the account's sign-in does not allow
	// calendar access; Error.Data is a CalendarConsentRequiredData.
	CodeCalendarConsentRequired ErrorCode = 1204
	// CodeCrmSignInUnsupported: the CRM instance cannot be signed in to by
	// this version; Error.Data is a CrmSignInUnsupportedData.
	CodeCrmSignInUnsupported ErrorCode = 1205
	// CodeContactsConsentRequired: the account's sign-in does not allow
	// contacts access; Error.Data is a ContactsConsentRequiredData.
	CodeContactsConsentRequired ErrorCode = 1206

	// 1300–1399: network and remote servers.
	CodeOffline      ErrorCode = 1300
	CodeNetworkError ErrorCode = 1301
	CodeServerError  ErrorCode = 1302 // IMAP/SMTP server returned an error
	// CodeTLSError: certificate, handshake or STARTTLS problem; for IMAP/SMTP
	// endpoints Error.Data is a TLSErrorData (reason, the certificate).
	CodeTLSError      ErrorCode = 1303
	CodeServerTimeout ErrorCode = 1304
	// CodeMessageGone: the mail server no longer has the message (another
	// client deleted or moved it); the local row and its text still exist
	// until the next sync removes them.
	CodeMessageGone ErrorCode = 1305
	// CodeEventGone: the server no longer has the event (deleted or moved
	// elsewhere); the next pass removes the local copy.
	CodeEventGone ErrorCode = 1306
	// CodeCrmDuplicate: the CRM refused the message as a duplicate of an
	// item the daemon cannot find (someone's private item, or one saved
	// without a Message-ID).
	CodeCrmDuplicate ErrorCode = 1307
	// CodeContactConflict: the contact changed on the server since the
	// revision the write names; Error.Data is a ContactConflictData.
	CodeContactConflict ErrorCode = 1308

	// 1400–1499: local storage.
	CodeStorageError    ErrorCode = 1400
	CodeMigrationFailed ErrorCode = 1401

	// 1500–1599: content.
	CodeMalformedMessage ErrorCode = 1500 // MIME could not be parsed even leniently
	CodeSanitizeFailed   ErrorCode = 1501 // sanitiser refused the input; body withheld
	// CodeAttachmentTooBig: over MaxAttachmentBytes, MaxDraftAttachmentBytes
	// or MaxAttachmentDataBytes; Error.Data = {"limit": n, "size": n}.
	CodeAttachmentTooBig ErrorCode = 1502
	// CodePartNotFound: message.part named a MIME part the message does not
	// have, or the message's content is no longer stored.
	CodePartNotFound ErrorCode = 1503
	// CodePartNotDownloaded: the part's data is not stored on this device
	// (Attachment.Remote); message.download fetches it from the server.
	CodePartNotDownloaded ErrorCode = 1504
	// CodeUnsubscribeFailed: message.unsubscribe reached the sender's
	// server, which refused or did not answer the one-click request
	// (Error.Message carries the status, never shown verbatim).
	CodeUnsubscribeFailed ErrorCode = 1505
	// CodeQuoteNotFound: board.annotate or board.commit quoted text the
	// daemon did not find verbatim where it must be; Error.Data is a
	// QuoteNotFoundData naming the field.
	CodeQuoteNotFound ErrorCode = 1506
	// CodeMalformedCalendar: an iCalendar object or a server's calendar
	// answer could not be read even leniently; the data is withheld.
	CodeMalformedCalendar ErrorCode = 1507
	// CodeRecurrenceUnsupported: the event's recurrence rule is outside
	// what the daemon expands or edits; the event shows its first instance
	// only and may be edited with scope all only.
	CodeRecurrenceUnsupported ErrorCode = 1508
	// CodeAddressBookReadOnly: the address book (or the contact) cannot be
	// changed by the user: the recent book, a server's read-only
	// collection or folder.
	CodeAddressBookReadOnly ErrorCode = 1509
)

// String returns the stable symbolic name of the code.
func (c ErrorCode) String() string {
	if s, ok := codeNames[c]; ok {
		return s
	}
	return fmt.Sprintf("unknown(%d)", int(c))
}

var codeNames = map[ErrorCode]string{
	CodeParseError:              "parseError",
	CodeInvalidRequest:          "invalidRequest",
	CodeMethodNotFound:          "methodNotFound",
	CodeInvalidParams:           "invalidParams",
	CodeInternalError:           "internalError",
	CodeNotImplemented:          "notImplemented",
	CodeInvalidArgument:         "invalidArgument",
	CodeConflict:                "conflict",
	CodeCancelled:               "cancelled",
	CodeUnavailable:             "unavailable",
	CodeUnauthenticated:         "unauthenticated",
	CodeReadOnly:                "readOnly",
	CodeInvitationsUnavailable:  "invitationsUnavailable",
	CodeAccountNotFound:         "accountNotFound",
	CodeFolderNotFound:          "folderNotFound",
	CodeMessageNotFound:         "messageNotFound",
	CodeThreadNotFound:          "threadNotFound",
	CodeDraftNotFound:           "draftNotFound",
	CodeAttachmentNotFound:      "attachmentNotFound",
	CodeCaseNotFound:            "caseNotFound",
	CodeCalendarNotFound:        "calendarNotFound",
	CodeEventNotFound:           "eventNotFound",
	CodeCrmItemNotFound:         "crmItemNotFound",
	CodeContactNotFound:         "contactNotFound",
	CodeAddressBookNotFound:     "addressBookNotFound",
	CodeAuthRequired:            "authRequired",
	CodeAuthFailed:              "authFailed",
	CodeKeyringError:            "keyringError",
	CodeOAuthClientMissing:      "oauthClientMissing",
	CodeCalendarConsentRequired: "calendarConsentRequired",
	CodeCrmSignInUnsupported:    "crmSignInUnsupported",
	CodeContactsConsentRequired: "contactsConsentRequired",
	CodeOffline:                 "offline",
	CodeNetworkError:            "networkError",
	CodeServerError:             "serverError",
	CodeTLSError:                "tlsError",
	CodeServerTimeout:           "serverTimeout",
	CodeMessageGone:             "messageGone",
	CodeEventGone:               "eventGone",
	CodeCrmDuplicate:            "crmDuplicate",
	CodeContactConflict:         "contactConflict",
	CodeStorageError:            "storageError",
	CodeMigrationFailed:         "migrationFailed",
	CodeMalformedMessage:        "malformedMessage",
	CodeSanitizeFailed:          "sanitizeFailed",
	CodeAttachmentTooBig:        "attachmentTooBig",
	CodePartNotFound:            "partNotFound",
	CodePartNotDownloaded:       "partNotDownloaded",
	CodeUnsubscribeFailed:       "unsubscribeFailed",
	CodeQuoteNotFound:           "quoteNotFound",
	CodeMalformedCalendar:       "malformedCalendar",
	CodeRecurrenceUnsupported:   "recurrenceUnsupported",
	CodeAddressBookReadOnly:     "addressBookReadOnly",
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
