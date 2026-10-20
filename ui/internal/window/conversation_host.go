// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/conversation"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settings"
)

// The conversation in the reading pane (docs/plans/chat-view.md §6.1): the
// one page the pane shows for a selected conversation row
// (conversation_view.blp). Above, a fixed strip that does not scroll, its
// controls in the cards' column at the trailing end: Collapse All / Expand
// All of the timeline and the layout toggle, Show as Chat or Show as
// Thread as the setting stands (conversation.ChatToggleLabel); below it,
// filling the rest, the two layouts of the same conversation, of which one
// shows: the timeline (conversation_view.go, the default) and the chat
// (conversation_chat.go). The setting conversation-chat-layout picks it
// (settings.KeyConversationChatLayout, for every conversation and across
// restarts): the toggle is bound to the setting (settings.Bind) and the
// layout follows the setting's change (OnChanged), not the click, so a
// change from elsewhere switches too. The hidden layout stays alive, so the
// timeline's folds and revealed quoted text outlive a visit to the chat
// (D10).
//
// The host is the conversation controller's only subscriber (it has one
// onChange and one onLoaded): conversation_fanout.go hands every change to
// both layouts and the news of a body to the layout shown, arms the
// mark-read once per selection (takeMarkRead, which the timeline did
// before there were two layouts) and trims the controller's bodies only
// with the near set of the layout shown. The host owns the strip, the
// spinner while the members load and the status label of the link under
// the pointer (only the timeline has links), and routes Space from the
// list to the layout shown (pageBy) and the window's news of a message
// (showLoaded and the window's conversationRefresh*) to it.
//
// The Jira issue card: each layout has its own (convIssueSlot) on top of
// its column, the timeline's beside its gutter, the chat's across the
// column; the transitions' spinner and result go to both (issueSlots,
// conversation_issue.go), Change Status in the menus pops up the card of
// the layout shown (shownIssueCard). A port of the macOS client's
// ConversationHostViewController.swift and ConversationStripView.swift
// (which also chose two cards over moving one between the layouts).
type conversationHost struct {
	convFanout
	w *Window

	timeline *conversationView
	chat     *conversationChat

	page    *gtk.Overlay
	layouts *gtk.Stack
	// foldAllButton is Collapse All / Expand All in the strip, showing
	// while the timeline shows and offers something (foldAll).
	foldAllButton *gtk.Button
	foldAll       conversation.FoldAll
	// toggle is the layout toggle, bound to the setting; toggleContent its
	// icon and label.
	toggle        *gtk.ToggleButton
	toggleContent *adw.ButtonContent
	status        *gtk.Label
	spinner       *adw.Spinner
	spinnerTimer  glib.SourceHandle
}

// The pages of conversation_layouts.
const (
	convTimelinePage = "timeline"
	convChatPage     = "chat"
)

// conversationPane is the conversation host, made the first time a
// conversation is shown: the page joins message_stack, the text zoom and
// Space in the list reach it, and the layout the setting asks for shows.
func (w *Window) conversationPane() *conversationHost {
	if w.conv != nil {
		return w.conv
	}
	installConvCSS()
	b := data.Builder("conversation_view.ui")
	h := &conversationHost{
		w:             w,
		page:          b.GetObject("conversation_page").Cast().(*gtk.Overlay),
		layouts:       b.GetObject("conversation_layouts").Cast().(*gtk.Stack),
		foldAllButton: b.GetObject("conversation_fold_all").Cast().(*gtk.Button),
		toggle:        b.GetObject("conversation_chat_toggle").Cast().(*gtk.ToggleButton),
		toggleContent: b.GetObject("conversation_chat_toggle_content").Cast().(*adw.ButtonContent),
		status:        b.GetObject("conversation_status").Cast().(*gtk.Label),
		spinner:       b.GetObject("conversation_spinner").Cast().(*adw.Spinner),
	}
	h.status.SetUseMarkup(false)
	h.ctrl = newConversationController(convWindowHost{w}, i18n.Tr)
	h.timeline = newConversationView(h, b)
	h.chat = newConversationChat(h, b)
	h.timelineLayout, h.chatLayout = h.timeline, h.chat
	h.markRead = w.scheduleMarkRead
	h.onChange = h.spin
	h.attach()

	h.foldAllButton.ConnectClicked(h.timeline.foldAllClicked)
	// The toggle writes the setting and shows it; the layout follows the
	// setting (applyLayout), never the click itself.
	w.settings.Bind(settings.KeyConversationChatLayout, h.toggle.Object, "active")
	w.settings.OnChanged(settings.KeyConversationChatLayout, h.applyLayout)
	h.applyLayout()

	w.messageStack.AddNamed(h.page, convPageName)
	w.settings.OnChanged(settings.KeyTextZoom, func() {
		h.timeline.setZoom(w.settings.TextZoom())
		h.chat.setZoom(w.settings.TextZoom())
	})
	w.addConversationPaging()
	w.conv = h
	return h
}

// applyLayout shows the layout the setting asks for (the other one is
// hidden first, convFanout.showLayout), and the strip follows: the
// toggle's label, tooltip and accessible description name what a click
// shows (conversation.ChatToggleLabel, ChatToggleTooltip), Collapse All /
// Expand All only in the timeline.
func (h *conversationHost) applyLayout() {
	on := h.w.settings.ConversationChatLayout()
	label := conversation.ChatToggleLabel(on, i18n.Tr)
	tooltip := conversation.ChatToggleTooltip(on, i18n.Tr)
	h.toggleContent.SetLabel(label)
	h.toggle.SetTooltipText(tooltip)
	h.toggle.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel, gtk.AccessiblePropertyDescription},
		[]coreglib.Value{*coreglib.NewValue(label), *coreglib.NewValue(tooltip)})
	if on == h.chatShown && h.started {
		return
	}
	// The page first, so that the layout shown is the one laid out.
	if on {
		h.layouts.SetVisibleChildName(convChatPage)
	} else {
		h.layouts.SetVisibleChildName(convTimelinePage)
	}
	h.showLayout(on)
	h.showFoldAll()
}

// setFoldAll is what the timeline's Collapse All / Expand All offers now
// (conversationView.showFoldAll).
func (h *conversationHost) setFoldAll(offer conversation.FoldAll) {
	h.foldAll = offer
	h.showFoldAll()
}

// showFoldAll shows Collapse All / Expand All while the timeline shows and
// it offers something; hidden, a focus it had goes to the layout shown.
func (h *conversationHost) showFoldAll() {
	if h.foldAllButton == nil {
		return
	}
	if h.chatShown || h.foldAll == conversation.FoldAllNone {
		if h.foldAllButton.HasFocus() {
			h.shownScroller().GrabFocus()
		}
		h.foldAllButton.SetVisible(false)
		return
	}
	h.foldAllButton.SetLabel(h.foldAll.Label(i18n.Tr))
	h.foldAllButton.SetVisible(true)
}

// shownScroller is the scrolled window of the layout shown.
func (h *conversationHost) shownScroller() *gtk.ScrolledWindow {
	if h.chatShown && h.chat != nil {
		return h.chat.scroller
	}
	return h.timeline.scroller
}

// spin follows a change of the controller with the spinner: shown once the
// members take long enough to notice (bodySpinnerDelay), hidden with
// anything else.
func (h *conversationHost) spin(ch convChange) {
	if ch == convLoading {
		h.startSpinner()
		return
	}
	h.stopSpinner()
}

func (h *conversationHost) startSpinner() {
	h.stopSpinner()
	h.spinnerTimer = glib.TimeoutAdd(bodySpinnerDelay, func() bool {
		h.spinnerTimer = 0
		h.spinner.SetVisible(true)
		return false
	})
}

func (h *conversationHost) stopSpinner() {
	if h.spinnerTimer != 0 {
		glib.SourceRemove(h.spinnerTimer)
		h.spinnerTimer = 0
	}
	h.spinner.SetVisible(false)
}

// hover shows the link under the pointer of any card ("" hides it): plain
// text, capped, the middle elided.
func (h *conversationHost) hover(uri string) {
	text := clampText(uri, convStatusMaxRunes)
	h.status.SetText(text)
	h.status.SetVisible(text != "")
}

// showLoaded: the window has news about message id (its fan-out: the
// remote images, a download). When the layout shown displays the member,
// its entry is lm from now on and its card or bubble shows it.
func (h *conversationHost) showLoaded(id api.MessageID, lm *loadedMessage) {
	if h.chatShown {
		if h.chat.bubbles[id] != nil {
			h.ctrl.adopt(id, lm)
			h.chat.loaded(id, lm)
		}
		return
	}
	if c := h.timeline.cards[id]; c != nil {
		h.ctrl.adopt(id, lm)
		c.render(lm)
	}
}

// issueSlots are the layouts' issue card slots, the timeline's first.
func (h *conversationHost) issueSlots() []*convIssueSlot {
	return []*convIssueSlot{&h.timeline.issue, &h.chat.issue}
}

// shownIssueCard is the issue card of the layout shown (Change Status in
// the menus pops it up); nil for a mail conversation.
func (h *conversationHost) shownIssueCard() *issueCard {
	if h.chatShown {
		return h.chat.issue.card
	}
	return h.timeline.issue.card
}
