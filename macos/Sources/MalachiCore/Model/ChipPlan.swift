// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// What the attachment chips of a message show (attachments.go
/// `renderAttachments` without the widgets): a chip per attachment except
/// the pictures the HTML body on display already shows, each with what it
/// can do (`partState`), and Save All after them when there are two or
/// more and all can be saved, now or after a download (`saveAllRemote`:
/// some of them are on the mail server). Shared by the views that draw
/// chips (the conversation view's cards; the single-message pane draws the
/// same rules itself).
public struct ChipPlan: Sendable, Equatable {
    public struct Chip: Sendable, Equatable {
        public var attachment: Attachment
        public var state: PartState
        /// The reason that goes with the state: the tooltip of a disabled
        /// chip or of the server symbol.
        public var why: String

        public init(attachment: Attachment, state: PartState, why: String) {
            self.attachment = attachment
            self.state = state
            self.why = why
        }
    }

    public var chips: [Chip] = []
    public var saveAll = false
    public var saveAllRemote = false

    public init(chips: [Chip] = [], saveAll: Bool = false, saveAllRemote: Bool = false) {
        self.chips = chips
        self.saveAll = saveAll
        self.saveAllRemote = saveAllRemote
    }

    /// The plan for message `m` (nil while message.get has not answered:
    /// no chips) with body `body`. `embedded`: the parts of an attached
    /// message, which nothing can fetch (message.embedded in docs/api.md).
    public init(_ m: Message?, _ body: MessageBodyResult?, embedded: Bool = false) {
        guard let m else { return }
        let atts = chipAttachments(m.attachments, body)
        var allOK = true
        for a in atts {
            var (state, why) = partState(a, body)
            if embedded {
                state = .unavailable
                why = L10n.T("Files inside an attached message cannot be opened or saved yet.")
            }
            allOK = allOK && (state == .local || state == .remote)
            chips.append(Chip(attachment: a, state: state, why: why))
        }
        if atts.count >= 2, allOK {
            saveAll = true
            saveAllRemote = anyRemote(atts, body)
        }
    }

    /// Nothing to show.
    public var isEmpty: Bool { chips.isEmpty }
}
