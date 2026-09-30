// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

// The parts of a message display that the single-message pane
// (MessageViewController) and the cards of the conversation view
// (ConversationCardView) draw alike: the texts of the remote-image and
// pictures bars, and the attachment chips with their actions.

extension RemoteBarView {
    /// The remote-image bar's text for state `st` (remote.go
    /// `showRemoteBar`); whether the bar shows, and its spinner, are the
    /// caller's (they move the keyboard focus first).
    func setRemoteText(_ st: RemoteBarState) {
        if st.loading {
            text = L10n.T("Loading remote images…")
        } else if st.blocked > 0 {
            // TRANSLATORS: %d is the number of remote images the message tried to load.
            text = L10n.N("%d remote image was blocked", "%d remote images were blocked", st.blocked)
        }
    }

    /// The pictures bar's text for state `st` (remote.go
    /// `renderPicturesBar`).
    func setPicturesText(_ st: PicturesBarState) {
        if st.loading {
            text = L10n.T("Downloading pictures…")
        } else if st.remote > 0 {
            // TRANSLATORS: %d is the number of pictures of the message kept on the mail server only.
            text = L10n.N(
                "%d picture of this message is on the server only", "%d pictures of this message are on the server only",
                st.remote)
        }
    }
}

/// The attachment chips of one message (attachments.go `buildChip`,
/// `buildSaveAll`) from a `ChipPlan`: each chip's actions close over the
/// attachment and the message it belongs to and go to the delegate; View
/// (an attached message) goes to `openEmbedded`. `window` is the window
/// the view is in when a chip has none to name (it is being rebuilt).
@MainActor
struct AttachmentChipFactory {
    weak var delegate: (any MessageActionDelegate)?
    let cache: MessageCache
    let openEmbedded: (@MainActor (_ containing: MessageSummary, _ attachment: Attachment, _ remote: Bool, _ chip: NSView?) -> Void)?
    let window: @MainActor () -> NSWindow?
    /// The chip on display for a part, for Quick Look to zoom out of.
    let chipForPart: @MainActor (_ partId: String) -> NSView?

    /// The chips of `plan` for message `s`, Save All last.
    func views(_ plan: ChipPlan, of s: MessageSummary) -> [NSView] {
        let downloading = cache.showsDownload(s.id)
        var out = plan.chips.map { chip(s, $0, downloading: downloading) }
        if plan.saveAll {
            out.append(saveAll(s, plan.chips.map(\.attachment), remote: plan.saveAllRemote))
        }
        return out
    }

    private func chip(_ s: MessageSummary, _ c: ChipPlan.Chip, downloading: Bool) -> NSView {
        let a = c.attachment
        let chip = AttachmentChipView(attachment: a, state: c.state, why: c.why, downloading: downloading)
        let remote = c.state == .remote
        let delegate = delegate
        let window = window
        let chipForPart = chipForPart
        let openEmbedded = openEmbedded
        chip.onPreview = { [weak chip] in
            delegate?.previewAttachment(a, of: s, remote: remote, from: chip?.window ?? window()) { part in
                chipForPart(part)
            }
        }
        chip.onOpen = { [weak chip] in
            delegate?.openAttachment(a, of: s, remote: remote, from: chip?.window ?? window())
        }
        chip.onSave = { [weak chip] in
            delegate?.saveAttachment(a, of: s, remote: remote, from: chip?.window ?? window())
        }
        chip.onView = { [weak chip] in
            openEmbedded?(s, a, remote, chip)
        }
        return chip
    }

    /// Save All stays disabled while one of the message runs, wherever the
    /// message is shown (`MessageCache.isSavingAll`).
    private func saveAll(_ s: MessageSummary, _ atts: [Attachment], remote: Bool) -> NSView {
        let button = SaveAllChipView()
        button.isEnabled = !cache.isSavingAll(s.id)
        let delegate = delegate
        let window = window
        button.onClick = { [weak button] in
            delegate?.saveAllAttachments(atts, of: s, remote: remote, from: button?.window ?? window())
        }
        return button
    }
}
