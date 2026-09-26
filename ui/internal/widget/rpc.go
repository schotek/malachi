// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package widget

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/certtrust"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// RPCTimeout bounds user-triggered calls to the daemon.
const RPCTimeout = 5 * time.Second

// RPCErrorText turns a client error into a short user-facing sentence.
// what is the (already translated) action in progressive form, e.g.
// i18n.T("Saving the draft").
func RPCErrorText(what string, err error) string {
	var e *api.Error
	// Whole sentences with the action as %s: translators reorder freely.
	// The backend's e.Message is technical English and only ever shown as
	// a trailing detail; the backend itself stays language-neutral.
	switch {
	case errors.Is(err, client.ErrDisconnected):
		return fmt.Sprintf(i18n.T("%s needs a running mail backend"), what)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Sprintf(i18n.T("%s timed out"), what)
	case errors.As(err, &e):
		switch e.Code {
		case api.CodeNotImplemented:
			return fmt.Sprintf(i18n.T("%s is not available yet"), what)
		case api.CodeConflict:
			return fmt.Sprintf(i18n.T("%s conflicted with another change"), what)
		case api.CodeInvalidArgument:
			return fmt.Sprintf(i18n.T("%s was rejected: %s"), what, e.Message)
		case api.CodeDraftNotFound:
			return i18n.T("The draft no longer exists")
		case api.CodeAttachmentNotFound:
			return i18n.T("The attachment no longer exists")
		case api.CodeAttachmentTooBig:
			return i18n.T("The attachment is too big")
		case api.CodeSanitizeFailed:
			return fmt.Sprintf(i18n.T("%s failed: formatted text cannot be saved yet"), what)
		case api.CodeAccountNotFound:
			return fmt.Sprintf(i18n.T("%s failed: unknown account"), what)
		case api.CodeKeyringError:
			return fmt.Sprintf(i18n.T("%s failed: the system keyring is unavailable"), what)
		case api.CodeAuthFailed:
			return fmt.Sprintf(i18n.T("%s failed: the server rejected the user name or password"), what)
		case api.CodeAuthRequired:
			// TRANSLATORS: %s is an action such as "Testing the connection".
			return fmt.Sprintf(i18n.T("%s failed: sign-in required"), what)
		case api.CodeNetworkError:
			return fmt.Sprintf(i18n.T("%s failed: the server could not be reached"), what)
		case api.CodeServerError:
			return fmt.Sprintf(i18n.T("%s failed: the server returned an error"), what)
		case api.CodeTLSError:
			// A refused certificate says why on its own (the outbox
			// banner of a failed message shows it).
			if text := tlsReasonText(e); text != "" {
				return text
			}
			return fmt.Sprintf(i18n.T("%s failed: the secure connection could not be established"), what)
		case api.CodeServerTimeout:
			return fmt.Sprintf(i18n.T("%s failed: the server did not respond in time"), what)
		}
	}
	return fmt.Sprintf(i18n.T("%s failed"), what)
}

// EndpointErrorText is the sentence for one endpoint of account.test, and
// for why an account is offline or in error (the status line's tooltip,
// Settings → Accounts). The backend's message is technical English and
// only ever a trailing detail.
func EndpointErrorText(e *api.Error) string {
	if e == nil {
		return i18n.T("Failed")
	}
	switch e.Code {
	case api.CodeAuthFailed:
		return i18n.T("The server rejected the user name or password")
	case api.CodeNetworkError:
		return i18n.T("The server could not be reached")
	case api.CodeServerError:
		return i18n.T("The server returned an error")
	case api.CodeTLSError:
		if text := tlsReasonText(e); text != "" {
			return text
		}
		return i18n.T("The secure connection could not be established")
	case api.CodeServerTimeout:
		return i18n.T("The server did not respond in time")
	case api.CodeNotImplemented:
		return i18n.T("Not supported yet")
	case api.CodeAuthRequired:
		return i18n.T("Sign in to this account again")
	case api.CodeUnavailable:
		return i18n.T("The sign-in service is not available")
	case api.CodeKeyringError:
		// TRANSLATORS: why an endpoint or an account failed
		return i18n.T("The system keyring is unavailable")
	case api.CodeOffline:
		// TRANSLATORS: why an endpoint or an account failed
		return i18n.T("No network connection")
	case api.CodeInvalidArgument:
		// TRANSLATORS: %s is a technical message from the mail backend.
		return fmt.Sprintf(i18n.T("Rejected: %s"), e.Message)
	}
	// TRANSLATORS: %s is a technical message from the mail backend.
	return fmt.Sprintf(i18n.T("Failed: %s"), e.Message)
}

// tlsReasonText is the sentence for the reason of a tlsError's details
// (docs/api.md §2); "" when the error carries none, or for a handshake
// failure, which the callers' general sentence describes. certtrust
// reports a reason this client does not know as "other".
func tlsReasonText(e *api.Error) string {
	p, ok := certtrust.Details(e)
	if !ok {
		return ""
	}
	switch p.Reason {
	case api.TLSUntrusted:
		return i18n.T("The server's certificate is not from a trusted authority")
	case api.TLSHostnameMismatch:
		return i18n.T("The server's certificate is for another name")
	case api.TLSExpired:
		return i18n.T("The server's certificate has expired")
	case api.TLSNotYetValid:
		return i18n.T("The server's certificate is not valid yet")
	case api.TLSInvalid:
		return i18n.T("The server's certificate is not valid")
	case api.TLSPinMismatch:
		return i18n.T("The server presented a different certificate than the one you trust")
	case api.TLSStartTLSUnavail:
		return i18n.T("The server does not offer STARTTLS")
	case api.TLSRequired:
		return i18n.T("The server requires TLS before signing in")
	case api.TLSHandshake:
		return ""
	}
	// TRANSLATORS: the system's certificate check refused the server's
	// certificate for a reason it does not name (e.g. "not standards
	// compliant" on macOS).
	return i18n.T("The system does not accept the server's certificate")
}

// PlainToast builds a toast whose title is plain text. Toast titles are
// Pango markup by default; backend messages are not mail content, but
// nothing shown to the user is interpreted as markup on principle.
func PlainToast(text string) *adw.Toast {
	t := adw.NewToast(text)
	t.SetUseMarkup(false)
	return t
}

// ConfirmDestructive asks before a destructive action. heading and body are
// shown as plain text (body is often hostile input such as a subject);
// label is the destructive button's label with a mnemonic. proceed runs
// only when the user confirms.
func ConfirmDestructive(parent gtk.Widgetter, heading, body, label string, proceed func()) {
	ConfirmDestructiveExtra(parent, heading, body, label, nil, proceed)
}

// ConfirmDestructiveExtra is ConfirmDestructive with an extra child widget
// (typically a check button) shown between the body and the buttons.
func ConfirmDestructiveExtra(parent gtk.Widgetter, heading, body, label string, extra gtk.Widgetter, proceed func()) {
	d := adw.NewAlertDialog(heading, body)
	d.SetHeadingUseMarkup(false)
	d.SetBodyUseMarkup(false)
	if extra != nil {
		d.SetExtraChild(extra)
	}
	d.AddResponse("cancel", i18n.T("_Cancel"))
	d.AddResponse("confirm", label)
	d.SetResponseAppearance("confirm", adw.ResponseDestructive)
	d.SetDefaultResponse("cancel")
	d.SetCloseResponse("cancel")
	d.ConnectResponse(func(response string) {
		if response == "confirm" {
			proceed()
		}
	})
	d.Present(parent)
}
