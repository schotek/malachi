// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

// The parts of a message display that the single-message pane
// (MessageViewController) and the cards of the conversation view
// (ConversationCardView) draw alike: the texts of the remote-image and
// pictures bars, the attachment chips with their actions, the chips'
// "Ask the Assistant…" and the keyboard focus across a rebuild of the chips.

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

extension AttachmentChipView {
    /// Gives the chip's menu "Ask the Assistant…" (attachments.go
    /// `bindAskItem`, ui/internal/assistant): hidden while the Assistant is
    /// not shown (its menu off, or the bridge not registered), disabled
    /// while no app handles the chosen Claude app's links (the file itself
    /// goes without the bridge, and never to the other app); for the panel
    /// (In App), while it cannot run or the bridge does not read this type
    /// (`AssistantController.canAsk`). `ask` runs the item.
    func offerAssistant(_ assistant: AssistantController, ask: @escaping @MainActor () -> Void) {
        let a = attachment
        assistantItem = { [weak assistant] in
            guard let assistant, assistant.shown else { return nil }
            assistant.refreshHandlers()
            return assistant.canAsk(about: a)
        }
        onAskAssistant = ask
    }
}

/// The keyboard focus across a rebuild of a message's chips (attachments.go
/// `renderAttachments`): made before the chips go, it takes the focus out
/// of the chip (or Save All) that holds it, and `restore` puts it on the
/// chip at the same place afterwards, or on the body when there is none.
/// Nothing happens when no chip held the focus.
@MainActor
struct ChipFocus {
    private let at: Int?
    private weak var window: NSWindow?

    /// `chips` are the chips on display, about to be rebuilt, in `window`.
    init(_ chips: [NSView], in window: NSWindow?) {
        self.window = window
        if let focus = window?.firstResponder as? NSView {
            at = chips.firstIndex { focus === $0 || focus.isDescendant(of: $0) }
        } else {
            at = nil
        }
        if at != nil {
            window?.makeFirstResponder(nil)
        }
    }

    /// The focus goes to the chip of `chips` (the new ones) at the place of
    /// the one that held it, else to `body` (nil: it stays with the window).
    func restore(to chips: [NSView], else body: NSView?) {
        guard let at, let window else { return }
        let target = at < chips.count ? ((chips[at] as? AttachmentChipView)?.control ?? chips[at]) : nil
        if target.map({ window.makeFirstResponder($0) }) != true, let body {
            window.makeFirstResponder(body)
        }
    }
}

/// The attachment chips of one message (attachments.go `buildChip`,
/// `buildSaveAll`) from a `ChipPlan`: each chip's actions close over the
/// attachment and the message it belongs to and go to the delegate; View
/// (an attached message) goes to `openEmbedded`, "Ask the Assistant…" is
/// offered as `assistant` allows (`offerAssistant`). `window` is the window
/// the view is in when a chip has none to name (it is being rebuilt).
@MainActor
struct AttachmentChipFactory {
    weak var delegate: (any MessageActionDelegate)?
    let cache: MessageCache
    let assistant: AssistantController
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
        chip.offerAssistant(assistant) { [weak chip] in
            delegate?.askAssistant(about: a, of: s, remote: remote, from: chip?.window ?? window())
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
