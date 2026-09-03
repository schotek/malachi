// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package widget

import (
	"context"
	"errors"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/client"
)

// RPCTimeout bounds user-triggered calls to the daemon.
const RPCTimeout = 5 * time.Second

// RPCErrorText turns a client error into a short user-facing sentence.
// what is the action in progressive form, e.g. "Saving the draft".
func RPCErrorText(what string, err error) string {
	var e *api.Error
	switch {
	case errors.Is(err, client.ErrDisconnected):
		return what + " needs a running mail backend"
	case errors.Is(err, context.DeadlineExceeded):
		return what + " timed out"
	case errors.As(err, &e):
		switch e.Code {
		case api.CodeNotImplemented:
			return what + " is not available yet"
		case api.CodeConflict:
			return what + " conflicted with another change"
		case api.CodeInvalidArgument:
			return what + " was rejected: " + e.Message
		case api.CodeDraftNotFound:
			return "The draft no longer exists"
		case api.CodeAttachmentNotFound:
			return "The attachment no longer exists"
		case api.CodeAttachmentTooBig:
			return "The attachment is too big"
		case api.CodeSanitizeFailed:
			return what + " failed: formatted text cannot be saved yet"
		case api.CodeAccountNotFound:
			return what + " failed: unknown account"
		}
	}
	return what + " failed"
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
	d := adw.NewAlertDialog(heading, body)
	d.SetHeadingUseMarkup(false)
	d.SetBodyUseMarkup(false)
	d.AddResponse("cancel", "_Cancel")
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
