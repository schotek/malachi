// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"math"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/conversation"
)

// The chat layout of the conversation view (docs/plans/chat-view.md D2,
// D3, D4, D13, D17), the rules that need no GTK: how wide a bubble gets in
// the column and whether its header shows the short date, whether the
// viewport shows the end of the chat and where it goes to show it, what a
// change of the viewport's size asks for, which variant of a loaded body a
// bubble reads and its text in every state of that body, whether that text
// is plain text (the monospace setting reaches it), what the bubble's
// header shows of the attachments, which bubbles are near enough to the
// viewport to ask for their bodies, and what a double click on a bubble
// opens. The view (conversation_chat.go, conversation_chat_row.go) applies
// them.
//
// A port of the macOS client's Swift-first rules, which
// ui/internal/conversation/chat.go leaves to the clients:
// MalachiCore/Model/ConversationChatBody.swift (`chatBody(of:quotedRevealed:)`,
// `bubbleBody`, `bubbleMonospace`, `bubbleAttachmentBadge`, `chatAtEnd`,
// `chatEndTop`, `chatColumn`, `chatCompactDates`, `chatResize`) and
// MalachiCore/Model/ConversationLayout.swift (`chatGap`, `chatBubbleMax`),
// with their tests (ConversationChatTests.swift, ConversationLayoutTests.swift)
// in conversation_chat_layout_test.go. No gotk4 here. No new msgid either:
// every text is the timeline's (bodyText, loadingMessageError).

// The chat's empty side of a row (convChatGap): at least
// convChatGapMin, else convChatGapRatio of the column.
const (
	convChatGapMin   = 24.0
	convChatGapRatio = 0.2
)

// convChatEndSlack is how close to the end of the chat the viewport's
// bottom may be and still count as at the end (D2: an arrival keeps a chat
// at its end in view).
const convChatEndSlack = 4.0

// convChatGap is the chat's empty side of a row (D4): the larger of
// convChatGapMin and convChatGapRatio of the column, continuous (the two
// meet at a column of 120). column is the clamp's inner width: the column
// at most convMaxWidth, less the two convSideInset (convChatColumn).
func convChatGap(column float64) float64 {
	return math.Max(convChatGapMin, convChatGapRatio*column)
}

// convChatBubbleMax is the widest a chat bubble gets in a column column
// wide (as convChatGap): a row of the chat is the avatar's gutter on the
// bubble's side (convAvatar + convGutterGap), the bubble, and the empty
// side of at least convChatGap; never negative. A short bubble shrinks to
// its text, not below its header.
func convChatBubbleMax(column float64) float64 {
	return math.Max(0, column-convAvatar-convGutterGap-convChatGap(column))
}

// convChatColumn is the chat's column in a pane pane wide: the clamp's
// inner width (at most convMaxWidth, less the two convSideInset), the
// column of convChatBubbleMax; never negative.
func convChatColumn(pane float64) float64 {
	return math.Max(0, math.Min(pane, convMaxWidth)-2*convSideInset)
}

// convChatCompactDates reports whether the chat's bubbles in a column
// column wide (convChatColumn) show the list's short date: their widest
// header, convChatBubbleMax less the padding, is narrower than
// convCompactHeader (the cards' rule, convCompactDates, measured on the
// bubble, which is narrower than a card).
func convChatCompactDates(column float64) bool {
	return convChatBubbleMax(column)-2*convCardPaddingH < convCompactHeader
}

// convChatAtEnd reports whether a viewport viewport tall whose top is at
// top shows the end of a document document tall (within slack); a document
// no taller than the viewport is always at its end.
func convChatAtEnd(top, viewport, document, slack float64) bool {
	return top+viewport >= document-slack
}

// convChatEndTop is where the viewport's top goes to show the end of the
// document (the chat opens there, conversation.ChatScrollTo): never above
// its top.
func convChatEndTop(viewport, document float64) float64 {
	return math.Max(0, document-viewport)
}

// convSize is a width and a height, in pixels.
type convSize struct {
	W, H float64
}

// convChatResizing is what a change of the chat's viewport size asks for
// (convChatResize).
type convChatResizing struct {
	// Width: the width changed; the bubbles reflow and the short dates may
	// switch.
	Width bool
	// ToEnd: the viewport ends at the end of the chat again, as it did
	// before the change (or the end was pinned); else the row the user
	// read keeps its place.
	ToEnd bool
}

// convChatResize is what the viewport going from from to to asks for;
// false when neither the width nor the height changed (by half a pixel or
// more). A change of the height alone moves nothing but the end: a chat
// that showed its end before (wasAtEnd, measured before the change, or
// pinned) shows it after.
func convChatResize(from, to convSize, pinned, wasAtEnd bool) (convChatResizing, bool) {
	width := math.Abs(to.W-from.W) >= 0.5
	height := math.Abs(to.H-from.H) >= 0.5
	if !width && !height {
		return convChatResizing{}, false
	}
	return convChatResizing{Width: width, ToEnd: pinned || wasAtEnd}, true
}

// chatBody is the variant of lm's body a bubble reads: the one without the
// quoted history (D13), even when the user revealed it in the timeline.
// While lm.body is the whole body (lm.quotedShown), the trimmed variant
// kept aside (otherBody) when there is one, else the whole body, which is
// all there is; otherwise lm.body. nil: no body yet (or no entry).
func chatBody(lm *loadedMessage) *api.MessageBodyResult {
	if lm == nil {
		return nil
	}
	if lm.quotedShown && lm.otherBody != nil {
		return lm.otherBody
	}
	return lm.body
}

// chatBodyAside reports whether the bubble of lm reads the trimmed variant
// kept aside (otherBody) rather than lm.body, whose error then does not
// count.
func chatBodyAside(lm *loadedMessage) bool {
	return lm.quotedShown && lm.otherBody != nil
}

// bubbleBody is the text of the bubble of a message whose cache entry is lm
// (nil: nothing asked for yet, or let go) and whose summary's snippet is
// snippet, and whether it is the placeholder (the snippet, dimmed, D17):
// the placeholder until a body or its error is there; the error of
// message.body as the cards say it (loadingMessageError); a body in a
// state other than fetched as the cards say it (bodyText); else the body's
// text cleaned (conversation.BubbleText), "(Empty message)" when nothing
// is left. The error belongs to lm.body, so it does not hide the trimmed
// variant kept aside.
func bubbleBody(lm *loadedMessage, snippet string) (text string, placeholder bool) {
	if lm == nil {
		return conversation.BubbleText(snippet), true
	}
	if !chatBodyAside(lm) && lm.err != nil {
		return loadingMessageError(lm.err), false
	}
	b := chatBody(lm)
	if b == nil {
		return conversation.BubbleText(snippet), true
	}
	switch b.BodyState {
	case api.BodyPending, api.BodyTooBig, api.BodyFailed:
		return bodyText(b), false
	}
	if t := conversation.BubbleText(b.Text); t != "" {
		return t, false
	}
	return bodyText(nil), false
}

// bubbleHTML is the body whose sanitised HTML the bubble of a message
// whose cache entry is lm shows in a locked web view (the preference
// conversation-chat-html, setting; conversation.BubbleRendersHTML), the
// document the timeline's card of the message would show for the variant
// the chat reads (chatBody: without the quoted history); nil when the
// bubble reads as text (bubbleBody): the preference off, nothing loaded
// yet, an error of message.body, a body in a state other than fetched, a
// text-only message or withheld HTML. The view gives a web view only to
// the bubbles near the viewport with such a body, at most
// convMaxLiveWebViews (chatLive); the others show bubbleBody's text.
func bubbleHTML(lm *loadedMessage, setting bool) *api.MessageBodyResult {
	if !setting || lm == nil {
		return nil
	}
	if !chatBodyAside(lm) && lm.err != nil {
		return nil
	}
	b := chatBody(lm)
	if b == nil || b.BodyState != api.BodyFetched {
		return nil
	}
	if !conversation.BubbleRendersHTML(setting, b.HTML != "", b.HTMLWithheld) {
		return nil
	}
	return b
}

// bubbleFrozenHeight is the minimum height of the text of a bubble that
// wants its HTML (wantsHTML: bubbleHTML gave it a body) but shows no web
// view now (web false: far from the viewport or beyond the cap): the
// document's height as last reported (reported, height), as a released
// card keeps its paper's height, so that the row keeps its allocation and
// the live window (chatLive) cannot flip the bubble between the view and
// the text. 0 (no minimum) while the view shows, before the document
// reported a height, and once the bubble wants no HTML (the preference
// off, a body that cannot render it).
func bubbleFrozenHeight(wantsHTML, web, reported bool, height int) int {
	if !wantsHTML || web || !reported || height <= 0 {
		return 0
	}
	return height
}

// bubbleMonospace reports whether the bubble of a message whose cache
// entry is lm shows its text in the monospaced font: only with the setting
// for plain text (monospace) and only for a body that is plain text, not
// the text the daemon derived from HTML (HasHTML), as the timeline's cards
// keep the setting to plain-text bodies. A placeholder, an error and a
// body in another state than fetched use the normal font.
func bubbleMonospace(lm *loadedMessage, monospace bool) bool {
	if !monospace || lm == nil {
		return false
	}
	if !chatBodyAside(lm) && lm.err != nil {
		return false
	}
	b := chatBody(lm)
	return b != nil && b.BodyState == api.BodyFetched && !b.HasHTML
}

// bubbleAttachments is what the header of a message bubble shows of the
// message's attachments (D3): a paperclip pill, with how many once known.
type bubbleAttachments struct {
	// Shown: the pill shows.
	Shown bool
	// Count is the number of chips the timeline's card shows for the
	// message (more than zero), 0 for the pill without a number: the
	// summary says the message has attachments, message.get has not told
	// which yet.
	Count int
}

// bubbleAttachmentBadge is the attachments pill of the bubble of a message
// whose summary is s and whose cache entry is lm (nil: nothing asked for
// yet, or let go). Once message.get answered (lm.msg), the number of chips
// the timeline's card shows (chipAttachments of the message and the body
// held), whatever the summary says: the summary's flag counts only the
// attachments the daemon calls not inline (a part with a Content-ID is
// inline to IMAP and the MIME parser, Microsoft Graph's hasAttachments
// leaves out the inline ones), while a chip shows every part the body does
// not show itself; none when there are no chips. Before that, the pill
// alone when the summary has HasAttachments; else none. A failed
// message.get leaves the summary's word.
func bubbleAttachmentBadge(s api.MessageSummary, lm *loadedMessage) bubbleAttachments {
	if lm != nil && lm.msg != nil {
		if n := len(chipAttachments(lm.msg.Attachments, lm.body)); n > 0 {
			return bubbleAttachments{Shown: true, Count: n}
		}
		return bubbleAttachments{}
	}
	return bubbleAttachments{Shown: s.HasAttachments}
}

// convChatScroll is where the chat keeps the viewport (D2): open at the
// end, and while the viewport shows the end (and until the user scrolls
// away from it) the end stays in view as bodies arrive and members come;
// otherwise the row under the viewport's top keeps its place.
type convChatScroll struct {
	// pinned: the end stays in view until the user scrolls.
	pinned bool
	// atEnd: the viewport showed the end when last measured at rest
	// (measure), before the change that is being laid out.
	atEnd bool
}

// open: a conversation shown anew (or the chat shown again) opens at the
// end.
func (s *convChatScroll) open() {
	s.pinned = true
	s.atEnd = true
}

// clear forgets the conversation.
func (s *convChatScroll) clear() {
	*s = convChatScroll{}
}

// scrolled: the user scrolled (the wheel, a drag, Space); the end is no
// longer pinned.
func (s *convChatScroll) scrolled() {
	s.pinned = false
}

// measure records whether the viewport, at rest, shows the end of the
// document.
func (s *convChatScroll) measure(top, viewport, document float64) {
	s.atEnd = convChatAtEnd(top, viewport, document, convChatEndSlack)
}

// toEnd reports whether a change being laid out (a body arrived, a member
// came, the pane changed its size) leaves the viewport at the end: it was
// there before, or the end is pinned.
func (s *convChatScroll) toEnd() bool {
	return s.pinned || s.atEnd
}

// chatNear asks for the bodies of the bubbles near the viewport: the
// messages ids whose rows are at frames (same indices) within
// convLiveScreens heights of visible, each with message.get as well when
// the summary has attachments, as the timeline's card asks for its chips,
// so that the paperclip gets its number (conversationController.needsBody).
// The near set is returned for the host's trim. chatLive without web
// views.
func chatNear(ctrl *conversationController, ids []api.MessageID, frames []convSpan, visible convSpan) map[api.MessageID]bool {
	return chatLive(ctrl, ids, frames, nil, visible).near
}

// chatLiveSet is what chatLive decided: the bubbles near the viewport
// (their bodies are asked for) and those of them that show their HTML in a
// web view.
type chatLiveSet struct {
	near, web map[api.MessageID]bool
}

// chatLive is chatNear and, of the bubbles near the viewport whose body
// renders as HTML (html, same indices as ids; bubbleHTML), the ones that
// get a web view: the timeline's rule (liveCards, convMaxLiveWebViews at
// most, the nearest to the visible part first) measured from the end, so
// that of bubbles equally near (in view) the newest win, as the chat opens
// at its end. The others show their text.
func chatLive(ctrl *conversationController, ids []api.MessageID, frames []convSpan, html []bool, visible convSpan) chatLiveSet {
	live := chatLiveRows(frames, html, visible, convMaxLiveWebViews)
	out := chatLiveSet{near: map[api.MessageID]bool{}, web: map[api.MessageID]bool{}}
	for i, id := range ids {
		if !live.Near[i] {
			continue
		}
		out.near[id] = true
		if live.Web[i] {
			out.web[id] = true
		}
		s, _ := ctrl.member(id)
		ctrl.needsBody(id, s.HasAttachments)
	}
	return out
}

// chatLiveRows is liveCards for the rows of a chat (indices as frames),
// measured from the end: of rows equally near the visible part the
// newest (the last) get the limit's web views first. Mail's chat
// (chatLive, convMaxLiveWebViews) and the board's (boardChat,
// board.ConversationMaxLiveWebViews) share it.
func chatLiveRows(frames []convSpan, html []bool, visible convSpan, limit int) convLive {
	n := len(frames)
	revFrames := make([]convSpan, n)
	revHTML := make([]bool, n)
	for i := 0; i < n; i++ {
		revFrames[n-1-i] = frames[i]
		revHTML[n-1-i] = i < len(html) && html[i]
	}
	rev := liveCards(revFrames, revHTML, visible, convLiveScreens, limit)
	out := convLive{Near: map[int]bool{}, Web: map[int]bool{}}
	for i := 0; i < n; i++ {
		out.Near[i] = rev.Near[n-1-i]
		out.Web[i] = rev.Web[n-1-i]
	}
	return out
}

// chatActivate is a double click on the bubble of row (D3, D14): a
// message opens as a double click on its row in the list does (in a window
// of its own, a message of a Drafts folder in the compose window;
// convHost.convOpenMessage), a draft in the compose window, or the comment
// window of a comment draft (convHost.convEditDraft). A member no longer
// shown, and the centred rows, open nothing.
func chatActivate(ctrl *conversationController, row conversation.ChatRow) {
	switch row.Kind {
	case conversation.ChatBubble:
		if s, ok := ctrl.member(row.Item.Message.ID); ok {
			ctrl.host.convOpenMessage(s)
		}
	case conversation.ChatDraft:
		if row.Draft.Draft.ID != "" {
			ctrl.host.convEditDraft(row.Draft.Draft)
		}
	}
}

// chatAccessibleName is the accessible name of a bubble: its sender and the
// full date (or a draft's badge and when it was saved), as a card's is;
// parts that are empty are left out.
func chatAccessibleName(parts ...string) string {
	out := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out != "" {
			out += ", "
		}
		out += p
	}
	return out
}

// chatDateText is the date of a bubble's or a centred row's header and its
// tooltip: in full, or in a narrow column the list's short form with the
// full one as the tooltip; "" for no date.
func chatDateText(full, short string, compact bool) (text, tooltip string) {
	if full == "" {
		return "", ""
	}
	if compact {
		return short, full
	}
	return full, ""
}

// chatEventMeta is the second line of a centred event row: who made the
// change and when, joined by a middle dot (no translatable text: a name
// and a date), as the macOS client's ConversationChatCenterRow has it.
func chatEventMeta(sender, when string) string {
	switch {
	case sender == "":
		return when
	case when == "":
		return sender
	}
	return sender + " · " + when
}
