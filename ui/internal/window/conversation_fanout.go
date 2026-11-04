// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/schotek/malachi/backend/pkg/api"
)

// The conversation host's rules that need no GTK (docs/plans/chat-view.md
// §6.1): the conversation controller has one onChange and one onLoaded,
// and the reading pane shows the conversation in one of two layouts over
// it, the timeline (conversation_view.go) and the chat
// (conversation_chat.go). The host (conversation_host.go) is the
// controller's only subscriber, and this fan-out is its half without
// widgets, so that the tests drive it with fake layouts: every change goes
// to both layouts (the hidden one forgets a conversation that goes, or
// notes what it has to catch up with once shown), the news of a body only
// to the layout shown; the member that opens the conversation is armed for
// the window's delayed mark-read once per selection, whichever layout
// shows; the controller's bodies are trimmed only with the near set of the
// layout shown; Space pages the layout shown. A port of the host's part of
// the macOS client's ConversationHostViewController.swift; the macOS
// client arms the mark-read in its list controller, the GTK client did it
// in the timeline's modelChanged, which moved here.

// convLayout is one layout of the conversation in the reading pane as the
// host drives it.
type convLayout interface {
	// changed is the controller's change. A hidden layout only forgets
	// what goes, or notes what to catch up with; it builds nothing.
	changed(ch convChange)
	// loaded is the news of a member's entry (a half of it arrived), for
	// the layout shown only.
	loaded(id api.MessageID, lm *loadedMessage)
	// setShown shows the layout (it catches up with the controller and
	// opens at its start: the timeline's top, the chat's end) or hides it
	// (it lets go of its web views and asks for nothing).
	setShown(on bool)
	// pageBy scrolls one page down (up) for Space (Shift+Space) in the
	// list; true when the key was used.
	pageBy(up bool) bool
}

// convFanout is the host's fan-out of the controller to its two layouts.
type convFanout struct {
	ctrl *conversationController
	// timelineLayout and chatLayout are the two layouts.
	timelineLayout, chatLayout convLayout
	// chatShown: the chat is the layout shown; started: showLayout ran
	// once (before, neither is shown).
	chatShown, started bool
	// markRead arms the window's delayed mark-read for a member
	// (Window.scheduleMarkRead).
	markRead func(api.MessageID)
	// onChange is the host's own part of a change (the spinner), before
	// the layouts.
	onChange func(convChange)
	// heldBytes is the controller's budget of held bodies for trim; 0 is
	// convHeldBytes (a test sets a smaller one).
	heldBytes int
}

// attach makes the fan-out the controller's only subscriber.
func (f *convFanout) attach() {
	f.ctrl.onChange = f.changed
	f.ctrl.onLoaded = f.loaded
}

// shown is the layout shown (the timeline before showLayout ran).
func (f *convFanout) shown() convLayout {
	if f.chatShown {
		return f.chatLayout
	}
	return f.timelineLayout
}

// changed hands a change of the controller to both layouts; a conversation
// opened arms the mark-read of its newest member that is not an event,
// after the usual delay and once per selection (takeMarkRead: nothing
// while the members are on their way or after thread.get failed).
func (f *convFanout) changed(ch convChange) {
	if f.onChange != nil {
		f.onChange(ch)
	}
	f.timelineLayout.changed(ch)
	f.chatLayout.changed(ch)
	if ch == convOpened {
		if id := f.ctrl.takeMarkRead(); id != "" && f.markRead != nil {
			f.markRead(id)
		}
	}
}

// loaded hands the news of a member's entry to the layout shown; the
// hidden one renders what the controller holds once it is shown.
func (f *convFanout) loaded(id api.MessageID, lm *loadedMessage) {
	f.shown().loaded(id, lm)
}

// showLayout shows the chat (chat) or the timeline: the other layout is
// hidden first (it lets go of what it holds), then the one asked for shown
// (it catches up with the controller). Nothing is asked of the daemon: the
// layouts share the controller's model and bodies. False when that layout
// shows already.
func (f *convFanout) showLayout(chat bool) bool {
	if f.started && chat == f.chatShown {
		return false
	}
	f.started = true
	f.chatShown = chat
	if chat {
		f.timelineLayout.setShown(false)
		f.chatLayout.setShown(true)
	} else {
		f.chatLayout.setShown(false)
		f.timelineLayout.setShown(true)
	}
	return true
}

// near is the set of members whose bodies layout from keeps near its
// viewport: the controller lets go of the others' entries beyond its
// budget (trim), only for the layout shown.
func (f *convFanout) near(from convLayout, near map[api.MessageID]bool) {
	if !f.started || from != f.shown() {
		return
	}
	budget := f.heldBytes
	if budget <= 0 {
		budget = convHeldBytes
	}
	f.ctrl.trim(near, budget)
}

// pageBy pages the layout shown (Space, Shift+Space in the list).
func (f *convFanout) pageBy(up bool) bool {
	return f.shown().pageBy(up)
}
