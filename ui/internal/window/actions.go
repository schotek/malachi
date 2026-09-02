package window

import (
	"context"
	"errors"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/settings"
)

// rpcTimeout bounds user-triggered calls to the daemon.
const rpcTimeout = 5 * time.Second

// confirmTrash asks before moving a message to Trash when the setting is on,
// then runs proceed. subject is hostile input and is shown as plain text.
func confirmTrash(parent gtk.Widgetter, s *settings.Store, subject string, proceed func()) {
	if !s.ConfirmDelete() {
		proceed()
		return
	}
	d := adw.NewAlertDialog("Move to Trash?", subject)
	d.SetBodyUseMarkup(false)
	d.AddResponse("cancel", "_Cancel")
	d.AddResponse("trash", "Move to _Trash")
	d.SetResponseAppearance("trash", adw.ResponseDestructive)
	d.SetDefaultResponse("cancel")
	d.SetCloseResponse("cancel")
	d.ConnectResponse(func(response string) {
		if response == "trash" {
			proceed()
		}
	})
	d.Present(parent)
}

// trashMessage moves message idx to Trash through message.delete, after the
// optional confirmation. Errors surface as a toast on toasts.
func (w *Window) trashMessage(idx int, parent gtk.Widgetter, toasts *adw.ToastOverlay) {
	if idx < 0 || idx >= len(dummyMessages) {
		return
	}
	confirmTrash(parent, w.settings, dummyMessages[idx].Subject, func() {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
			defer cancel()
			// TODO(phase-1): real account and message IDs.
			err := w.client.Call(ctx, api.MethodMessageDelete, api.MessageDeleteParams{}, &api.MessageDeleteResult{})
			glib.IdleAdd(func() {
				if err != nil {
					w.log.Debug("message.delete", "err", err)
					toasts.AddToast(adw.NewToast(rpcErrorText("Deleting", err)))
					return
				}
				// TODO(phase-1): drop the row once the backend really deletes.
			})
		}()
	})
}

// markRead flags message idx as seen: immediately in the list (optimistic)
// and through message.flag in the background.
func (w *Window) markRead(idx int) {
	if idx < 0 || idx >= len(dummyMessages) || !dummyMessages[idx].Unread {
		return
	}
	dummyMessages[idx].Unread = false
	w.rows[idx].SetMessage(messageOf(dummyMessages[idx]))
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		// TODO(phase-1): real account and message IDs.
		p := api.MessageFlagParams{Set: []api.Flag{api.FlagSeen}}
		if err := w.client.Call(ctx, api.MethodMessageFlag, p, &api.MessageFlagResult{}); err != nil {
			w.log.Debug("message.flag", "err", err)
		}
	}()
}

// scheduleMarkRead arms the mark-as-read timer for the newly selected
// message idx, cancelling any pending one.
func (w *Window) scheduleMarkRead(idx int) {
	if w.markReadSource != 0 {
		glib.SourceRemove(w.markReadSource)
		w.markReadSource = 0
	}
	if idx < 0 || idx >= len(dummyMessages) || !dummyMessages[idx].Unread {
		return
	}
	delay := w.settings.MarkReadDelay()
	if delay <= 0 {
		w.markRead(idx)
		return
	}
	w.markReadSource = glib.TimeoutSecondsAdd(uint(delay), func() bool {
		w.markReadSource = 0
		w.markRead(idx)
		return false
	})
}

// rpcErrorText turns a client error into a short user-facing sentence.
func rpcErrorText(what string, err error) string {
	var e *api.Error
	switch {
	case errors.Is(err, client.ErrDisconnected):
		return what + " needs a running mail backend"
	case errors.As(err, &e) && e.Code == api.CodeNotImplemented:
		return what + " is not available yet"
	default:
		return what + " failed"
	}
}
