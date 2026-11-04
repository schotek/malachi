// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/conversation"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/jira"
	"github.com/schotek/malachi/ui/internal/quickreply"
)

// The quick-reply bar of Mail's reading pane, under a single message and
// under a conversation shown as chat (docs/plans/chat-view.md §10, §11,
// §12; wireframes docs/plans/chat-view/w4-quick-reply.svg and
// w5-quick-reply-message.svg): the widgets of quick_reply_bar.blp, which
// the window owns and appends under message_stack (quick_reply.go
// attachQuickReply), a fixed row at the bottom of the pane, a line above
// it, its content in the column's clamp. From the leading end: the mode button (icon and label, Reply or
// Reply All; its menu the modes the account offers and, after a separator,
// Forward…, which opens the compose window at once), on an issue a plain
// label Comment with no menu; the field of one to five lines (the
// assistant panel's: a TextView in a scrolled window with
// propagate-natural-height and max-content-height 112, the placeholder an
// overlay label; Return is a new line); while the Assistant is shown the
// Assistant button, the toolbar's ✦ bound by the same function
// (Assistant.bindAssistantButton over the win.* actions: with In App it
// opens the assistant panel with its field focused, else its menu pops up
// under this button); Open in a Window; Send. In a narrow pane
// (convQuickReplyIconOnly) the mode button shows its icon alone. The mode
// button's tooltip names the sender of the message the reply goes to
// (conversation.QuickReplyTargetTooltip).
//
// Everything it shows is the controller's (quickreply.Controller.State,
// read again on every OnChange); the bar reports typing, the mode, Send,
// Open in a Window and Forward… to it and decides nothing. A hidden bar is
// a hidden child of the pane's column and takes no room; its background is
// the page's above it (the conversation page's under the chat,
// setConversationSurface). Hidden with the keyboard in its field, the bar
// gives the keyboard to the view the pane shows (Window.focusReadingView:
// the message's body, the conversation's layout), else to nobody, never
// keeping it on a hidden widget and never giving it to the list (only
// Escape does that).
//
// The width (followWidth): the mode button shows its icon alone while the
// bar, which spans the reading pane, is narrow. The field's scrolled
// window reports every change of its width through its horizontal
// adjustment ("changed", from the text view's allocation), and the field
// is as wide as the pane allows, so the bar measures itself on the next
// idle after each such report (queueFollowWidth): one trigger for the
// page of a message and for the chat, and none while the bar is hidden.
//
// Keys, in the field's capture phase (as assistant_panel.go's): Ctrl+Enter
// sends (only while Send is enabled; the key is used either way, so it
// never adds a line), Escape gives the keyboard to the message list while
// the list is on screen. While a Send is on its way the field keeps the
// keyboard and stays editable, so that the window's typing gate
// (typing_gate.go) keeps the single-key shortcuts off, but the buffer
// refuses every insertion and deletion (inputGate) and the buttons are
// insensitive. Space types: paging the conversation is the list's key.
//
// Rule 3: the only text from mail here is the target's sender in the
// tooltip (conversation.Item.Sender, cleaned to one line by the model);
// labels have no markup. What the user types becomes HTML in the
// controller (conversation.QuickReplyHTML, escaped), never here. A port of
// the macOS client's ConversationQuickReplyBar.swift.

// quickReplyBar is the quick-reply bar of the reading pane.
type quickReplyBar struct {
	w    *Window
	ctrl *quickreply.Controller

	root         *gtk.Box
	mode         *gtk.MenuButton
	modeContent  *adw.ButtonContent
	comment      *gtk.Box
	commentLabel *gtk.Label
	text         *gtk.TextView
	scroller     *gtk.ScrolledWindow
	placeholder  *gtk.Label
	assistant    *gtk.MenuButton
	open         *gtk.Button
	send         *gtk.Button

	// modeAction is qr.mode, the menu's radio items (state: the mode
	// shown); forwardAction qr.forward, its Forward….
	modeAction    *gio.SimpleAction
	forwardAction *gio.SimpleAction

	// applying: the bar writes the controller's text into the buffer (no
	// edit to report, nothing to refuse).
	applying bool
	// sending: the state last rendered is sending (inputGate refuses
	// typing).
	sending bool
	// iconOnly: the mode button shows its icon alone (a narrow pane);
	// widthQueued: a measure of the bar's width waits for an idle.
	iconOnly    bool
	widthQueued bool
	// holding: the window feeds the controller several steps at once
	// (hold); the bar renders once at the end, heldChange says whether
	// anything changed meanwhile.
	holding    bool
	heldChange bool
	// shownMode is what the mode button was last built for, so that a
	// change of the text alone does not touch it; shownModes the modes its
	// menu was last built from (the menu is replaced only when they
	// change: a rebuild closes an open popover, even from its own item).
	shownMode  *quickReplyModeKey
	shownModes string
}

// quickReplyModeKey is what the mode button shows.
type quickReplyModeKey struct {
	mode     conversation.QuickReplyMode
	modes    string // the modes offered, as quickReplyModeNames
	comment  bool
	iconOnly bool
	tooltip  string
	enabled  bool
}

// The targets of qr.mode.
const (
	quickReplyModeReply    = "reply"
	quickReplyModeReplyAll = "reply-all"
)

// quickReplyModeName is the qr.mode target of a reply mode; "" for
// QuickForward, which is an action of its own (qr.forward).
func quickReplyModeName(m conversation.QuickReplyMode) string {
	switch m {
	case conversation.QuickReply:
		return quickReplyModeReply
	case conversation.QuickReplyAll:
		return quickReplyModeReplyAll
	}
	return ""
}

// quickReplyModeOf is the reply mode of a qr.mode target; false for
// anything else.
func quickReplyModeOf(name string) (conversation.QuickReplyMode, bool) {
	switch name {
	case quickReplyModeReply:
		return conversation.QuickReply, true
	case quickReplyModeReplyAll:
		return conversation.QuickReplyAll, true
	}
	return conversation.QuickReply, false
}

// quickReplyModeNames is modes as one comparable string (quickReplyModeKey).
func quickReplyModeNames(modes []conversation.QuickReplyMode) string {
	parts := make([]string, len(modes))
	for i, m := range modes {
		if m == conversation.QuickForward {
			parts[i] = "forward"
		} else {
			parts[i] = quickReplyModeName(m)
		}
	}
	return strings.Join(parts, ",")
}

// quickReplyModeIcon is the icon of a mode, as the reading pane's buttons
// have it.
func quickReplyModeIcon(m conversation.QuickReplyMode) string {
	switch m {
	case conversation.QuickReplyAll:
		return "mail-reply-all-symbolic"
	case conversation.QuickForward:
		return "mail-forward-symbolic"
	}
	return "mail-reply-sender-symbolic"
}

// quickReplyModeLabel is the label of a reply mode (the reading pane's
// Reply, Reply All).
func quickReplyModeLabel(m conversation.QuickReplyMode) string {
	if m == conversation.QuickReplyAll {
		return i18n.T("Reply All")
	}
	return i18n.T("Reply")
}

// newQuickReplyBar binds the bar of quick_reply_bar.ui (b) to ctrl.
func newQuickReplyBar(w *Window, b *gtk.Builder, ctrl *quickreply.Controller) *quickReplyBar {
	get := func(id string) any { return b.GetObject(id).Cast() }
	bar := &quickReplyBar{
		w:            w,
		root:         get("conversation_reply_bar").(*gtk.Box),
		mode:         get("conversation_reply_mode").(*gtk.MenuButton),
		modeContent:  get("conversation_reply_mode_content").(*adw.ButtonContent),
		comment:      get("conversation_reply_comment").(*gtk.Box),
		commentLabel: get("conversation_reply_comment_label").(*gtk.Label),
		text:         get("conversation_reply_text").(*gtk.TextView),
		scroller:     get("conversation_reply_scroller").(*gtk.ScrolledWindow),
		placeholder:  get("conversation_reply_placeholder").(*gtk.Label),
		assistant:    get("conversation_reply_assistant").(*gtk.MenuButton),
		open:         get("conversation_reply_open").(*gtk.Button),
		send:         get("conversation_reply_send").(*gtk.Button),
	}
	bar.commentLabel.SetUseMarkup(false)
	bar.placeholder.SetUseMarkup(false)
	// A click on a button leaves the keyboard in the field: Send and Open
	// turn insensitive while a Send is on its way, which would drop the
	// focus and let the window's typing gate switch the single-key
	// shortcuts back on under the user's fingers.
	for _, b := range []*gtk.Widget{&bar.mode.Widget, &bar.assistant.Widget, &bar.open.Widget, &bar.send.Widget} {
		b.SetFocusOnClick(false)
	}

	group := gio.NewSimpleActionGroup()
	bar.modeAction = gio.NewSimpleActionStateful("mode", glib.NewVariantType("s"), glib.NewVariantString(quickReplyModeReply))
	bar.modeAction.ConnectActivate(func(v *glib.Variant) {
		if v == nil {
			return
		}
		if m, ok := quickReplyModeOf(v.String()); ok {
			bar.ctrl.SetMode(m)
		}
	})
	group.AddAction(bar.modeAction)
	bar.forwardAction = gio.NewSimpleAction("forward", nil)
	bar.forwardAction.ConnectActivate(func(*glib.Variant) { bar.ctrl.Forward() })
	group.AddAction(bar.forwardAction)
	bar.root.InsertActionGroup("qr", group)

	openLabel := conversation.QuickReplyOpenWindowLabel(i18n.Tr)
	bar.open.SetTooltipText(openLabel)
	bar.open.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel}, []coreglib.Value{*coreglib.NewValue(openLabel)})
	bar.open.ConnectClicked(func() { bar.ctrl.OpenWindow() })
	// The tooltip "Send (Ctrl+Enter)" is the Blueprint's.
	sendLabel := strings.ReplaceAll(i18n.T("_Send"), "_", "")
	bar.send.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel}, []coreglib.Value{*coreglib.NewValue(sendLabel)})
	bar.send.ConnectClicked(func() { bar.ctrl.Send() })

	// The toolbar's ✦ (window.go): the same binding over the win.*
	// actions, so the same menu, the same panel rule and the same
	// visibility (Assistant.shown and its observer). The main window lives
	// as long as the application: no unbinding.
	if w.assist != nil {
		w.assist.bindAssistantButton(bar.assistant, "win", true, w.syncAssistantActions, w.openAssistantPanel,
			func() int { return len(w.selectedRows()) })
	}

	bar.bindInput()
	bar.scroller.HAdjustment().ConnectChanged(bar.queueFollowWidth)
	bar.setController(ctrl)
	return bar
}

// setController makes ctrl the bar's controller (newQuickReplyBar; the
// smoke test gives it one over a fake host) and shows its state.
func (bar *quickReplyBar) setController(ctrl *quickreply.Controller) {
	bar.ctrl = ctrl
	bar.shownMode = nil
	bar.shownModes = ""
	ctrl.OnChange = bar.render
	bar.render()
}

// textOf is the field's text.
func (bar *quickReplyBar) textOf() string {
	buf := bar.text.Buffer()
	return buf.Text(buf.StartIter(), buf.EndIter(), false)
}

// bindInput: an edit goes to the controller, the placeholder shows while
// the field is empty, the buffer refuses edits while a Send is on its way
// (inputGate), Ctrl+Enter sends and Escape gives the keyboard to the list.
func (bar *quickReplyBar) bindInput() {
	buf := bar.text.Buffer()
	buf.ConnectChanged(func() {
		bar.placeholder.SetVisible(buf.CharCount() == 0)
		if bar.applying {
			return
		}
		bar.ctrl.SetText(bar.textOf())
	})
	// inputGate: while sending, nothing the user does changes the text;
	// stopping the signals before their default handler drops the edit.
	buf.ConnectInsertText(func(*gtk.TextIter, string, int) {
		if bar.sending && !bar.applying {
			buf.StopEmission("insert-text")
		}
	})
	buf.ConnectDeleteRange(func(_, _ *gtk.TextIter) {
		if bar.sending && !bar.applying {
			buf.StopEmission("delete-range")
		}
	})
	keys := gtk.NewEventControllerKey()
	keys.SetPropagationPhase(gtk.PhaseCapture)
	keys.ConnectKeyPressed(func(keyval, _ uint, state gdk.ModifierType) bool {
		return bar.key(keyval, state)
	})
	bar.text.AddController(keys)
}

// key is a key pressed in the field; true when the bar used it.
func (bar *quickReplyBar) key(keyval uint, state gdk.ModifierType) bool {
	switch keyval {
	case gdk.KEY_Return, gdk.KEY_KP_Enter, gdk.KEY_ISO_Enter:
		if state&gdk.ControlMask == 0 {
			return false // Return is a new line
		}
		if s := bar.ctrl.State(); s.Visible && !s.Blank && !s.Sending {
			bar.ctrl.Send()
		}
		return true
	case gdk.KEY_Escape:
		return bar.focusList()
	}
	return false
}

// focusList gives the keyboard to the message list when it is on screen
// (a narrow window that shows the reading pane alone has it hidden);
// false when it is not.
func (bar *quickReplyBar) focusList() bool {
	list := bar.w.messageList
	if list == nil || !list.Mapped() {
		return false
	}
	list.GrabFocus()
	return true
}

// queueFollowWidth measures the bar on the next idle (followWidth): the
// field's adjustment reports from inside the allocation, which must not
// change the bar's content.
func (bar *quickReplyBar) queueFollowWidth() {
	if bar.widthQueued {
		return
	}
	bar.widthQueued = true
	glib.IdleAdd(func() {
		bar.widthQueued = false
		bar.followWidth(bar.root.Width())
	})
}

// followWidth follows the pane's width pane (the bar's own: it spans the
// reading pane): the mode button shows its icon alone in a narrow one
// (convQuickReplyIconOnly). A bar without a width yet (hidden, not laid
// out) changes nothing.
func (bar *quickReplyBar) followWidth(pane int) {
	if pane <= 0 {
		return
	}
	narrow := convQuickReplyIconOnly(float64(pane))
	if narrow == bar.iconOnly {
		return
	}
	bar.iconOnly = narrow
	bar.render()
}

// hold runs f, which feeds the controller several steps
// (Window.updateQuickReply), and renders once afterwards if any of them
// changed the state: a step in between must not hide the bar.
func (bar *quickReplyBar) hold(f func()) {
	if bar.holding {
		f()
		return
	}
	bar.holding, bar.heldChange = true, false
	defer func() {
		bar.holding = false
		if bar.heldChange {
			bar.heldChange = false
			bar.render()
		}
	}()
	f()
}

// setConversationSurface gives the bar the conversation page's background
// (on: under the chat) or none, so that it shows the pane's own under a
// message (convCSS, .conversation-pane).
func (bar *quickReplyBar) setConversationSurface(on bool) {
	if on {
		bar.root.AddCSSClass("conversation-pane")
	} else {
		bar.root.RemoveCSSClass("conversation-pane")
	}
}

// render shows the controller's state.
func (bar *quickReplyBar) render() {
	if bar.holding {
		bar.heldChange = true
		return
	}
	s := bar.ctrl.State()
	bar.setVisible(s.Visible)
	bar.sending = s.Sending

	// The text first: writing it reports a change, which is not an edit.
	if bar.textOf() != s.Text {
		bar.applying = true
		bar.text.Buffer().SetText(s.Text)
		bar.applying = false
	}
	placeholder := conversation.QuickReplyPlaceholder(s.Comment, i18n.Tr)
	bar.placeholder.SetText(placeholder)
	bar.placeholder.SetVisible(bar.text.Buffer().CharCount() == 0)
	bar.text.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel}, []coreglib.Value{*coreglib.NewValue(placeholder)})
	// The field keeps the keyboard while sending (see inputGate), dimmed;
	// undo is off meanwhile, so that a refused edit leaves no history out
	// of step with the text.
	bar.text.Buffer().SetEnableUndo(!s.Sending)
	if s.Sending {
		bar.text.SetOpacity(0.55)
	} else {
		bar.text.SetOpacity(1)
	}
	bar.open.SetSensitive(!s.Sending)
	bar.send.SetSensitive(!s.Blank && !s.Sending)

	var sender string
	if s.Target.Kind == conversation.ItemMessage {
		sender = s.Target.Sender
	}
	key := quickReplyModeKey{
		mode:     s.Mode,
		modes:    quickReplyModeNames(s.Modes),
		comment:  s.Comment,
		iconOnly: bar.iconOnly,
		tooltip:  conversation.QuickReplyTargetTooltip(s.Mode, sender, i18n.Tr),
		enabled:  !s.Sending,
	}
	if bar.shownMode == nil || *bar.shownMode != key {
		bar.shownMode = &key
		bar.renderMode(key, s.Modes)
	}
}

// renderMode builds the mode button (or, on an issue, the comment label).
func (bar *quickReplyBar) renderMode(k quickReplyModeKey, modes []conversation.QuickReplyMode) {
	bar.mode.SetVisible(!k.comment)
	bar.comment.SetVisible(k.comment)
	if k.comment {
		label := jira.ReplyLabel(true, i18n.Tr)
		bar.commentLabel.SetText(label)
		bar.commentLabel.SetVisible(!k.iconOnly)
		setQuickReplyTooltip(&bar.comment.Widget, k.tooltip)
		bar.comment.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel}, []coreglib.Value{*coreglib.NewValue(label)})
		return
	}

	label := quickReplyModeLabel(k.mode)
	bar.modeContent.SetIconName(quickReplyModeIcon(k.mode))
	if k.iconOnly {
		bar.modeContent.SetLabel("")
	} else {
		bar.modeContent.SetLabel(label)
	}
	setQuickReplyTooltip(&bar.mode.Widget, k.tooltip)
	bar.mode.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel}, []coreglib.Value{*coreglib.NewValue(label)})
	bar.mode.SetSensitive(k.enabled)
	bar.modeAction.SetState(glib.NewVariantString(quickReplyModeName(k.mode)))
	if k.modes == bar.shownModes {
		return
	}
	bar.shownModes = k.modes

	menu := gio.NewMenu()
	replies := gio.NewMenu()
	forward := false
	for _, m := range modes {
		if m == conversation.QuickForward {
			forward = true
			continue
		}
		item := gio.NewMenuItem(quickReplyModeLabel(m), "")
		item.SetActionAndTargetValue("qr.mode", glib.NewVariantString(quickReplyModeName(m)))
		replies.AppendItem(item)
	}
	menu.AppendSection("", replies)
	if forward {
		section := gio.NewMenu()
		section.Append(conversation.QuickReplyForwardLabel(i18n.Tr), "qr.forward")
		menu.AppendSection("", section)
	}
	bar.forwardAction.SetEnabled(forward)
	bar.mode.SetMenuModel(menu)
}

// setQuickReplyTooltip sets w's tooltip to text, none for "" (a target
// without a sender).
func setQuickReplyTooltip(w *gtk.Widget, text string) {
	if text == "" {
		w.SetHasTooltip(false)
		return
	}
	w.SetTooltipText(text)
}

// setVisible shows or hides the bar; a field that had the keyboard as the
// bar hides on its own (another page, the timeline, a reply that made the
// message a conversation) lets it go to the view the pane shows
// (Window.focusReadingView), else to nobody, never to the list (only
// Escape does that) and never kept on a hidden widget.
func (bar *quickReplyBar) setVisible(on bool) {
	if bar.root.Visible() == on {
		return
	}
	if !on && bar.holdsKeyboard() && !bar.w.focusReadingView() {
		bar.w.SetFocus(nil)
	}
	bar.root.SetVisible(on)
}

// leaveIfOther hands the keyboard off (as setVisible does on hiding) when
// the bar holds it and the pane is about to show another conversation
// (account, t) than the one whose text the field holds: hold renders
// once after the switch, with the bar still visible, so the field would
// otherwise keep the keyboard while its text silently becomes the other
// conversation's (a message that left the list under the user's hands:
// moved elsewhere, trashed, a neighbour selected instead). The macOS bar
// renders every step and lets the keyboard go the same way.
func (bar *quickReplyBar) leaveIfOther(account api.AccountID, t api.ThreadID) {
	st := bar.ctrl.State()
	if st.Thread == "" || (st.Account == account && st.Thread == t) || !bar.holdsKeyboard() {
		return
	}
	if !bar.w.focusReadingView() {
		bar.w.SetFocus(nil)
	}
}

// holdsKeyboard reports whether the window's keyboard is in the bar (the
// field or one of its buttons); the window's focus widget, not HasFocus,
// so that it holds for a window that is not active as well.
func (bar *quickReplyBar) holdsKeyboard() bool {
	f := bar.w.Focus()
	if f == nil {
		return false
	}
	return coreglib.BaseObject(f).Eq(bar.root) || gtk.BaseWidget(f).IsAncestor(bar.root)
}
