// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package style applies application-wide appearance settings: the libadwaita
// color scheme and a CSS provider for everything that cannot be expressed
// per widget (message body zoom and font, list decorations).
//
// Because the provider is installed on the display, reloading it restyles
// every open window at once; windows need no subscriptions of their own.
package style

import (
	"fmt"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/settings"
)

// Apply installs the color scheme and CSS from s and keeps them in sync with
// later changes. Call it once from the application's startup handler, after
// GTK and libadwaita are initialised.
func Apply(s *settings.Store) {
	sm := adw.StyleManagerGetDefault()
	applyScheme := func() { sm.SetColorScheme(adwColorScheme(s.ColorScheme())) }
	applyScheme()
	s.OnChanged(settings.KeyColorScheme, applyScheme)

	provider := gtk.NewCSSProvider()
	gtk.StyleContextAddProviderForDisplay(gdk.DisplayGetDefault(), provider,
		uint(gtk.STYLE_PROVIDER_PRIORITY_APPLICATION))
	reload := func() {
		provider.LoadFromString(CSS(s.TextZoom(), s.MonospacePlainText(), s.MonochromeAvatars()))
	}
	reload()
	for _, key := range []string{settings.KeyTextZoom, settings.KeyMonospacePlainText, settings.KeyMonochromeAvatars} {
		s.OnChanged(key, reload)
	}
}

func adwColorScheme(c settings.ColorScheme) adw.ColorScheme {
	switch c {
	case settings.ColorSchemeLight:
		return adw.ColorSchemeForceLight
	case settings.ColorSchemeDark:
		return adw.ColorSchemeForceDark
	default:
		return adw.ColorSchemeDefault
	}
}

// CSS renders the application stylesheet for the given settings. Percent
// font sizes are relative to the parent, so zoom composes with the user's
// system font. The provider has application priority, so its rules win over
// the libadwaita stylesheet (e.g. the per-sender avatar colour classes).
func CSS(textZoomPercent int, monospace, monochromeAvatars bool) string {
	var b strings.Builder
	b.WriteString(".unread-dot { min-width: 8px; min-height: 8px; border-radius: 4px; background-color: @accent_bg_color; }\n")
	// The folder sidebar is navigation, not content: it lists many one-line
	// entries and is therefore denser than libadwaita's action rows. Most of
	// the height a row gets is not the label but the 6px libadwaita puts
	// above and below the title box, so that is what comes down first; the
	// row's own floor follows, and the title goes to 88% so the shorter rows
	// do not look crowded. The unread badge keeps the .caption size it has,
	// being outside the title box. Only this list is affected: the message
	// list shares the navigation-sidebar class (window.blp).
	b.WriteString("list.folder-list { padding: 3px 0; }\n")
	b.WriteString("list.folder-list row.folder-row { min-height: 24px; padding-top: 1px; padding-bottom: 1px; }\n")
	b.WriteString("row.folder-row > box.header { min-height: 0; padding-top: 0; padding-bottom: 0; }\n")
	b.WriteString("row.folder-row > box.header > box.title { margin-top: 2px; margin-bottom: 2px; font-size: 88%; }\n")
	// Every icon of the sidebar at once: the folder icons, the fold arrows of
	// rows and headings alike, and the stars.
	b.WriteString("list.folder-list image { -gtk-icon-size: 14px; }\n")
	// The unread count of a sidebar row is a disc in the accent colour, 16px
	// across at one digit (8px of text box and 4px either side) and a
	// capsule with more; bold, so the small caption still reads at a glance.
	b.WriteString("label.unread-badge { min-width: 8px; min-height: 16px; padding: 0 4px; border-radius: 99px; font-weight: bold; background-color: @accent_bg_color; color: @accent_fg_color; }\n")
	// The status line at the bottom of the sidebar (window.blp) is a menu
	// button that opens the accounts' states, but it reads as a line of
	// text: the button loses the height and padding a stand-alone one
	// claims and the bold weight Adwaita gives buttons, and its label keeps
	// to the left over the full width (hexpand and xalign in window.blp).
	b.WriteString("menubutton.status-line > button { min-height: 0; padding: 4px 6px; font-weight: normal; }\n")
	// The fold arrow and the pin star of a sidebar row. A default button
	// would push the row past the height above, so both are squeezed to the
	// size of their icon.
	b.WriteString("button.folder-twisty, button.folder-star { min-width: 18px; min-height: 18px; padding: 0; margin: 0; }\n")
	// The star waits for the pointer or the keyboard focus. Opacity rather
	// than visibility, so nothing moves under the pointer.
	b.WriteString("row.folder-row button.folder-star { opacity: 0; transition: opacity 150ms; }\n")
	b.WriteString("row.folder-row:hover button.folder-star, row.folder-row:focus-within button.folder-star { opacity: 1; }\n")
	// In the tree a filled star stays visible: it is what says the folder is
	// pinned. In the Favourites section every row is pinned by definition, so
	// a column of stars would say nothing and the star waits there too.
	b.WriteString("row.folder-row:not(.favourite) button.folder-star.starred { opacity: 1; }\n")
	// The remote-image bar above a message: a neutral tint that reads as a
	// notice in light and dark alike (Adw.Banner has room for one button).
	b.WriteString("box.remote-bar { background-color: alpha(@window_fg_color, 0.06); padding: 6px 12px; }\n")
	// The strip above a bulk message (window/bulk.go): the remote-bar tint,
	// a warm one in the junk folder.
	b.WriteString("box.bulk-bar.bulk-warning { background-color: alpha(@orange_3, 0.25); }\n")
	// Compose header fields (compose.blp): one line each, so the entries
	// and the account drop-down lose the height a stand-alone input would
	// claim and the card supplies the padding the rows no longer have.
	b.WriteString("box.compose-headers > box { padding: 0 12px; }\n")
	b.WriteString("box.compose-headers entry, box.compose-headers dropdown > button { min-height: 30px; }\n")
	b.WriteString("box.compose-headers dropdown > button { padding-left: 0; }\n")
	// The card is one surface: the fields sit on it rather than each being
	// a control of its own. .flat on the drop-down does not reach the
	// button GtkDropDown builds inside itself, which is what put a grey
	// slab behind the sender, so the background comes off here; hover and
	// the open popup keep theirs, or the row would stop looking clickable.
	b.WriteString("box.compose-headers dropdown > button { background: none; box-shadow: none; }\n")
	b.WriteString("box.compose-headers dropdown > button:hover { background: alpha(@window_fg_color, 0.05); }\n")
	b.WriteString("box.compose-headers dropdown > button:active, box.compose-headers dropdown > button:checked { background: alpha(@window_fg_color, 0.1); }\n")
	// No focus ring inside the card either: moving between From, To and
	// Subject would otherwise draw an outline around each in turn. The
	// text caret still says which field takes typing.
	b.WriteString("box.compose-headers entry, box.compose-headers entry:focus-within { outline: none; box-shadow: none; }\n")
	b.WriteString("box.compose-headers button:focus-visible, box.compose-headers dropdown > button:focus-visible { outline: none; }\n")
	// Recipient suggestions (compose): a popover the width of the row, its
	// list flush with the popover's edges rather than inset like a menu,
	// and compact two-line rows — it is a list to pick from, not a dialog.
	b.WriteString("popover.recipient-suggestions > contents { padding: 0; }\n")
	b.WriteString("popover.recipient-suggestions list { background: none; }\n")
	b.WriteString("popover.recipient-suggestions row { padding: 4px 10px; }\n")
	b.WriteString("popover.recipient-suggestions image { -gtk-icon-size: 16px; }\n")
	// Attachment chips under the message headers: two have to fit side by
	// side in a narrow pane, so the buttons drop libadwaita's roomy
	// padding, the labels go down a size and the menu arrow is barely
	// wider than its icon. The name itself is capped in Go (chipNameChars).
	b.WriteString("box.attachment-chip button, button.chip-action { min-height: 0; padding: 3px 8px; }\n")
	b.WriteString("box.attachment-chip image, button.chip-action image { -gtk-icon-size: 14px; }\n")
	b.WriteString("box.attachment-chip label.chip-name, button.chip-action label { font-size: 90%; }\n")
	b.WriteString("box.attachment-chip label.chip-size { font-size: 80%; }\n")
	b.WriteString("box.attachment-chip menubutton.chip-arrow > button { min-width: 16px; padding-left: 2px; padding-right: 2px; }\n")
	// The assistant panel (window/assistant_panel.go): the context chip is a
	// pill with its remove button inside it; the user's questions a tinted
	// bubble, the cards (the draft, the bar "Another message is selected")
	// a faint one; the answers are text views on the panel's own
	// background; the question field is a rounded box that grows to five
	// lines.
	b.WriteString("box.assistant-chip { background-color: alpha(@window_fg_color, 0.07); border-radius: 99px; padding: 2px 3px 2px 10px; }\n")
	b.WriteString("box.assistant-chip label { font-size: 90%; }\n")
	b.WriteString("box.assistant-chip image { -gtk-icon-size: 14px; }\n")
	b.WriteString("box.assistant-chip button { min-width: 20px; min-height: 20px; padding: 0; }\n")
	b.WriteString("box.assistant-user { background-color: alpha(@accent_bg_color, 0.14); border-radius: 12px; padding: 6px 10px; }\n")
	b.WriteString("box.assistant-card { background-color: alpha(@window_fg_color, 0.05); border-radius: 12px; padding: 8px 10px; }\n")
	b.WriteString("textview.assistant-answer, textview.assistant-answer > text { background: none; }\n")
	b.WriteString("scrolledwindow.assistant-input { border-radius: 8px; box-shadow: inset 0 0 0 1px alpha(@window_fg_color, 0.15); }\n")
	b.WriteString("scrolledwindow.assistant-input textview, scrolledwindow.assistant-input textview > text { background: none; }\n")
	// Sender and recipients above a message (window/addresses.go): pills on
	// a faint tint, a size down and without the bold weight Adwaita gives
	// buttons, since a row of bold names reads as shouting. "+N more" is a
	// flat pill that only tints under the pointer. The row labels share the
	// chips' height, so From, To and Cc sit level with the first line of
	// chips however many lines follow.
	b.WriteString("menubutton.address-chip > button, button.address-more { min-height: 24px; padding: 0 10px; border-radius: 99px; font-weight: normal; font-size: 90%; }\n")
	b.WriteString("menubutton.address-chip > button { background-color: alpha(@window_fg_color, 0.07); }\n")
	b.WriteString("menubutton.address-chip > button:hover { background-color: alpha(@window_fg_color, 0.12); }\n")
	b.WriteString("menubutton.address-chip > button:active, menubutton.address-chip > button:checked { background-color: alpha(@window_fg_color, 0.18); }\n")
	b.WriteString("button.address-more { color: alpha(@window_fg_color, 0.7); }\n")
	b.WriteString("label.address-label { min-height: 24px; font-size: 90%; }\n")
	// The recipient fields of the compose window (compose/recipient_field.go):
	// the same capsule and tint as an address chip, with a small × inside.
	// A selected badge takes the accent, an entry that is not an address the
	// error colours. The entry beside them stays as it was.
	b.WriteString("box.recipient-token { min-height: 24px; padding: 0 2px 0 10px; border-radius: 99px; font-size: 90%; background-color: alpha(@window_fg_color, 0.07); }\n")
	b.WriteString("box.recipient-token:hover { background-color: alpha(@window_fg_color, 0.12); }\n")
	b.WriteString("box.recipient-token button { min-width: 18px; min-height: 18px; padding: 0; margin: 3px 0 3px 2px; }\n")
	b.WriteString("box.recipient-token button image { -gtk-icon-size: 12px; }\n")
	b.WriteString("box.recipient-token.invalid { background-color: alpha(@error_bg_color, 0.2); color: @error_color; }\n")
	b.WriteString("box.recipient-token.selected { background-color: @accent_bg_color; color: @accent_fg_color; }\n")
	b.WriteString("box.recipient-token.selected.invalid { background-color: @error_bg_color; color: @error_fg_color; }\n")
	// The name and address on top of a chip's menu, inset like its items.
	b.WriteString("box.address-card { padding: 6px 12px; }\n")
	// The All / Unread / Flagged switch above the message list: it is a
	// filter, not the column's heading, so it stays out of the way. The
	// toggles lose the height and the roomy padding a stand-alone button
	// claims, the label goes down a size, and the bold weight Adwaita
	// gives buttons comes off — three short words in bold read as a title
	// bar. CSS node names come from AdwToggleGroup (toggle-group > toggle).
	b.WriteString("toggle-group.message-filter toggle { min-height: 0; padding: 3px 12px; }\n")
	b.WriteString("toggle-group.message-filter toggle label { font-size: 90%; font-weight: normal; }\n")
	// Separators between messages (show-separators on the list in window.blp).
	// The sidebar style rounds its rows and insets them, which would bend the
	// separator into a shallow arc and leave a gap under it, so the message
	// rows are squared and flush; selection stays the sidebar's subtle tint,
	// now spanning the full width. The last row keeps no trailing line.
	b.WriteString("list.message-list > row { border-radius: 0; margin: 0; }\n")
	b.WriteString("list.message-list > row:last-child { border-bottom: none; }\n")
	// Conversation rows of the grouped list (message_row.blp): a small fold
	// arrow, the member count in a disc 18px across (a capsule with more
	// digits), and the members of an expanded conversation on a faint tint
	// so they read as one group. The tint is for the resting state only: the rule outranks the sidebar style's
	// hover and selection tints and would hide them.
	b.WriteString("button.thread-twisty { min-width: 20px; min-height: 20px; padding: 0; margin: 0; }\n")
	b.WriteString("label.thread-count { min-width: 8px; min-height: 18px; padding: 0 5px; border-radius: 99px; background-color: alpha(@window_fg_color, 0.1); }\n")
	b.WriteString("list.message-list > row.thread-member:not(:selected):not(:hover):not(:active) { background-color: alpha(@window_fg_color, 0.03); }\n")
	// Layer the section tint over native hover, without affecting selection.
	b.WriteString("list.message-list > row.flagged-section:not(:selected) { background-image: linear-gradient(alpha(@accent_bg_color, 0.06), alpha(@accent_bg_color, 0.06)); }\n")
	// The pills of a Jira account (widget/pill.go): an issue's status by
	// its category (grey to do or unknown, blue in progress, green done),
	// the orange Internal badge of a service-desk comment, and the JIRA
	// capsule after the account's name in the sidebar. Tints of the palette
	// under the default text colour, so they read in light and dark alike.
	b.WriteString("label.issue-pill { padding: 0 7px; border-radius: 99px; font-size: 80%; background-color: alpha(@window_fg_color, 0.1); }\n")
	b.WriteString("label.issue-pill.bulk-pill { color: alpha(@window_fg_color, 0.75); }\n")
	b.WriteString("label.issue-pill.status-in-progress { background-color: alpha(@blue_3, 0.3); }\n")
	b.WriteString("label.issue-pill.status-done { background-color: alpha(@green_4, 0.3); }\n")
	b.WriteString("label.issue-pill.internal-pill { background-color: alpha(@orange_3, 0.35); }\n")
	// The issue card over a Jira message (window/issue_card.go): the
	// card's own padding, the key as a link without a button's bulk, and
	// the status pill as a menu button in the pill's shape and colours.
	b.WriteString("box.issue-card { padding: 10px 12px; }\n")
	b.WriteString("button.issue-key { min-height: 0; padding: 0 4px; }\n")
	b.WriteString("menubutton.issue-status-button > button { min-height: 0; padding: 1px 8px; border-radius: 99px; font-size: 80%; font-weight: normal; background-color: alpha(@window_fg_color, 0.1); }\n")
	b.WriteString("menubutton.issue-status-button.status-in-progress > button { background-color: alpha(@blue_3, 0.3); }\n")
	b.WriteString("menubutton.issue-status-button.status-done > button { background-color: alpha(@green_4, 0.3); }\n")
	b.WriteString("menubutton.issue-status-button image { -gtk-icon-size: 12px; }\n")
	b.WriteString("label.kind-badge { padding: 0 4px; border-radius: 4px; font-size: 70%; font-weight: bold; background-color: alpha(@window_fg_color, 0.1); color: alpha(@window_fg_color, 0.75); }\n")
	// The "•••" under a body whose quoted history the daemon cut
	// (window/quoted.go): a small grey capsule as Gmail has it, a size
	// down. The Collapse All / Expand All button above a conversation
	// (conversation_view.blp): flat text in the accent colour, a link
	// without a button's bulk.
	b.WriteString("button.quoted-text { min-height: 0; min-width: 0; padding: 0 8px; border-radius: 99px; font-size: 80%; font-weight: bold; background-color: alpha(@window_fg_color, 0.08); }\n")
	b.WriteString("button.quoted-text:hover { background-color: alpha(@window_fg_color, 0.14); }\n")
	b.WriteString("button.fold-all { min-height: 0; padding: 2px 4px; font-size: 90%; font-weight: normal; color: @accent_color; }\n")
	// Account reordering in preferences: the insertion line is a box-shadow
	// rather than a border so the row does not change height, and therefore
	// does not twitch, while the pointer moves over it.
	b.WriteString(".drag-handle { color: alpha(@window_fg_color, 0.55); }\n")
	b.WriteString("row.account-row.dragging { opacity: 0.4; }\n")
	b.WriteString("row.account-row.drop-above { box-shadow: inset 0 2px 0 0 @accent_bg_color; }\n")
	b.WriteString("row.account-row.drop-below { box-shadow: inset 0 -2px 0 0 @accent_bg_color; }\n")
	// The board (window/board*.go, widget/board_row.go): a case's state as
	// a colour dot in the navigation column, the list and the detail's
	// state pill, and the pill itself as a menu button in the issue-status
	// pill's shape. hot is the destructive red, you the accent blue, them a
	// warm orange (libadwaita's named palette has no brown, the colour
	// macOS uses for "waiting for them"), info the plain dim label colour
	// (no dot, so a dot with none of the classes below stays invisible on
	// purpose where the state is not shown).
	b.WriteString(".board-state-dot { min-width: 9px; min-height: 9px; border-radius: 5px; background-color: alpha(@window_fg_color, 0.25); }\n")
	b.WriteString(".board-state-dot-hot { background-color: @destructive_bg_color; }\n")
	b.WriteString(".board-state-dot-you { background-color: @accent_bg_color; }\n")
	b.WriteString(".board-state-dot-them { background-color: @orange_4; }\n")
	b.WriteString(".board-state-dot-info { background-color: alpha(@window_fg_color, 0.35); }\n")
	b.WriteString("menubutton.board-state-pill > button { min-height: 0; padding: 3px 10px; border-radius: 99px; font-weight: bold; font-size: 85%; background-color: alpha(@window_fg_color, 0.1); }\n")
	b.WriteString("menubutton.board-state-pill.board-state-hot > button { background-color: alpha(@destructive_bg_color, 0.2); color: @destructive_color; }\n")
	b.WriteString("menubutton.board-state-pill.board-state-you > button { background-color: alpha(@accent_bg_color, 0.2); color: @accent_color; }\n")
	b.WriteString("menubutton.board-state-pill.board-state-them > button { background-color: alpha(@orange_4, 0.25); }\n")
	// A case's badges (Reminded, New contact; widget.NewBadgePill): the
	// issue pill's shape, smaller and tinted with the accent, so that they
	// never read as an issue's status.
	b.WriteString("label.issue-pill.board-badge { padding: 0 5px; font-size: 72%; background-color: alpha(@accent_bg_color, 0.15); color: @accent_color; }\n")
	// The board's nav column rows (window/board_list.go): the filter rows'
	// dot and count badge, the account rows' kind capsule, same shape as
	// the folder sidebar's unread badge.
	b.WriteString("row.board-nav-row { min-height: 28px; }\n")
	b.WriteString("label.board-nav-count { min-width: 8px; min-height: 16px; padding: 0 4px; border-radius: 99px; font-size: 80%; background-color: alpha(@window_fg_color, 0.1); }\n")
	// A case's message cards in the detail (window/board_detail.go): the
	// user's own on a faint accent tint, the other party's neutral, plain
	// text only (CLAUDE.md rule 3 — never HTML on the board).
	b.WriteString("box.board-card { padding: 8px 10px; border-radius: 10px; background-color: alpha(@window_fg_color, 0.045); }\n")
	b.WriteString("box.board-card.board-card-mine { background-color: alpha(@accent_bg_color, 0.1); }\n")
	b.WriteString("box.board-conversation-card { background-color: alpha(@window_fg_color, 0.045); }\n")
	b.WriteString("box.board-conversation-card.board-card-mine { background-color: alpha(@accent_bg_color, 0.1); }\n")
	// The assistant's summary, tasks and commitments: the same indigo-ish
	// note the assistant panel gives its own cards, so the board reads as
	// the same voice.
	b.WriteString("box.board-assistant-box { border-radius: 10px; padding: 8px 10px; background-color: alpha(@window_fg_color, 0.05); }\n")
	b.WriteString("label.board-assistant-mark { color: @accent_color; }\n")
	// Board headings keep their text readable; only the small state dot
	// carries the state colour. Cards distinguish title, sender and summary.
	b.WriteString("scrolledwindow.board-surface { background-color: alpha(@window_fg_color, 0.02); }\n")
	b.WriteString("list.board-columns-list row.board-card-row { background-color: alpha(@window_fg_color, 0.045); border-radius: 10px; margin: 4px 10px; padding: 8px 10px; }\n")
	b.WriteString("list.board-columns-list row.board-card-row.board-card-row-hot { background-color: alpha(@destructive_bg_color, 0.08); }\n")
	b.WriteString("list.board-columns-list row.board-card-row:selected { box-shadow: inset 0 0 0 2px @accent_color; }\n")
	b.WriteString("label.board-section-title { color: @window_fg_color; }\n")
	b.WriteString("label.board-section-count { min-width: 14px; padding: 2px 6px; border-radius: 99px; background-color: alpha(@window_fg_color, 0.08); color: alpha(@window_fg_color, 0.75); }\n")
	b.WriteString("label.board-row-title { font-weight: 600; }\n")
	b.WriteString("label.board-row-snippet { color: alpha(@window_fg_color, 0.75); }\n")
	b.WriteString("label.board-due-label { padding: 3px 8px; border-radius: 99px; background-color: alpha(@accent_bg_color, 0.12); color: @accent_color; font-weight: 600; }\n")
	b.WriteString("label.board-due-label.board-due-overdue, button.board-due-overdue label.board-due-label { background-color: alpha(@destructive_bg_color, 0.15); color: @destructive_color; }\n")
	b.WriteString("button.board-more-button { color: @accent_color; font-weight: 600; }\n")
	b.WriteString("box.board-column-placeholder { border: 1px dashed alpha(@window_fg_color, 0.3); border-radius: 10px; }\n")
	b.WriteString("box.board-today-card { border: 1px solid alpha(@window_fg_color, 0.12); border-radius: 10px; padding: 12px 14px; }\n")
	b.WriteString("box.board-today-card-dashed { border-style: dashed; }\n")
	b.WriteString("button.board-due-item { padding: 6px 8px; border-radius: 8px; }\n")
	b.WriteString(".board-today-tile { border: 1px solid alpha(@window_fg_color, 0.12); border-radius: 10px; padding: 10px; }\n")
	b.WriteString(".board-today-tile.board-today-tile-hot { background-color: alpha(@destructive_bg_color, 0.06); }\n")
	b.WriteString("label.board-today-tile-count { font-size: 170%; font-weight: bold; }\n")
	fmt.Fprintf(&b, ".message-body { font-size: %d%%; }\n", textZoomPercent)
	if monospace {
		b.WriteString(".message-body { font-family: monospace; }\n")
	}
	if monochromeAvatars {
		// Neutral tint of the foreground colour: readable in light and dark.
		b.WriteString("avatar { background-image: none; background-color: alpha(@window_fg_color, 0.12); color: alpha(@window_fg_color, 0.8); }\n")
	}
	return b.String()
}
