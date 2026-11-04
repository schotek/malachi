// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/signature"
)

// The signature badge of a board's conversation card (plan §6, the board's
// counterpart of convCard.sig): the same badge as Mail's (signature.go)
// from board.get's verdict (board.MessageCard.Signature), on its own row
// under the card's header, shown folded too; never in a chat bubble. The
// verdict needs no message.get: board.get carries the signer and notes.
// A click opens the same details dialog as Mail's (message.signature with
// the member's id; a sample's card shows only the verdict). A verdict
// that changed alone is a change of the block's cards
// (board.ConversationKey.Signatures: same cards, same bodies) and reaches
// the card through update (board.SameCard compares it by value); board.go
// asks board.get again when verdicts change without the case.

// bindSignature adds the card's hidden badge under its header.
func (c *boardConversationCard) bindSignature() {
	c.sig = newSignatureBadge(c.openSignature)
	c.sig.button.SetMarginStart(convCardPaddingH)
	c.sig.button.SetMarginEnd(convCardPaddingH)
	c.sig.button.SetMarginBottom(4)
	c.root.Append(c.sig.button)
}

// showSignature shows the badge of the card's verdict (hidden without
// one). A badge that goes away while it has the keyboard hands it to the
// detail's scroller.
func (c *boardConversationCard) showSignature() {
	if c.sig == nil {
		return
	}
	b := signature.BadgeFor(c.m.Signature, i18n.Tr)
	if !b.Visible && c.sig.button.HasFocus() {
		if scroller := c.block.p.boardReplyScroller(); scroller != nil {
			scroller.GrabFocus()
		}
	}
	c.sig.show(b)
}

// openSignature opens the details of the card's verdict over the main
// window.
func (c *boardConversationCard) openSignature() {
	w := c.block.p.w
	w.openSignatureDetails(&w.ApplicationWindow.Window, c.m.ID, c.m.Address, c.m.Signature)
}
