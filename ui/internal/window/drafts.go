// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"errors"
	"fmt"

	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/compose"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Drafts: the daemon keeps a copy of every saved draft in the account's
// Drafts folder. A message of that folder opens in the compose window:
// draft.open answers with the saved draft it is the copy of, or with a
// draft built from it (another client's), and the window edits that.

// openDraft opens message id of a Drafts folder in the compose window, or
// raises the window already editing it. A second request while the first
// is on its way does nothing. A daemon without draft.open shows the
// message instead.
func (w *Window) openDraft(id api.MessageID) {
	s, ok := w.summary(id)
	if !ok || w.composing[id] {
		return
	}
	w.composing[id] = true
	params := api.DraftOpenParams{AccountID: s.AccountID, MessageID: id}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), composeTimeout)
		defer cancel()
		var res api.DraftOpenResult
		err := w.client.Call(ctx, api.MethodDraftOpen, params, &res)
		glib.IdleAdd(func() {
			delete(w.composing, id)
			if err != nil {
				w.log.Warn("draft.open", "err", err)
				if draftOpenUnsupported(err) {
					w.openMessageWindow(id)
					return
				}
				w.Toast(draftOpenErrorText(err))
				return
			}
			if cw := w.compose.FindDraft(res.Draft); cw != nil {
				cw.Present()
				return
			}
			w.compose.Open(compose.FromDraft(compose.KindEdit, res.Draft, res.Blocked))
			if n := len(res.Skipped); n > 0 {
				w.Toast(skippedText(n))
			}
		})
	}()
}

// draftOpenUnsupported reports a daemon that does not offer draft.open.
func draftOpenUnsupported(err error) bool {
	var e *api.Error
	return errors.As(err, &e) && (e.Code == api.CodeMethodNotFound || e.Code == api.CodeNotImplemented)
}

// draftOpenErrorText is the toast for a failed draft.open: a body the
// syncer has not downloaded yet is a matter of a moment, anything else the
// usual sentence.
func draftOpenErrorText(err error) string {
	var e *api.Error
	if errors.As(err, &e) && e.Code == api.CodeUnavailable {
		return i18n.T("The draft has not been downloaded yet; try again in a moment")
	}
	return widget.RPCErrorText(i18n.T("Opening the draft"), err)
}

// skippedText says how many parts of a draft could not be taken along.
func skippedText(n int) string {
	return fmt.Sprintf(i18n.N("%d attachment of the draft could not be opened",
		"%d attachments of the draft could not be opened", n), n)
}
