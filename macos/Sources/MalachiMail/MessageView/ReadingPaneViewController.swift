// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The main window's reading pane: the single-message view
/// (`MessageViewController`, with its "No Message Selected" page and its
/// banners) or, for a selected conversation row, the whole conversation
/// (`ConversationHostViewController`: the timeline or the chat), whichever the list's selection asks for
/// (window.go `onMessageRowSelected`, extended by ui/internal/conversation).
/// A member row and a single-message row keep the single-message view.
///
/// Under both, at the bottom of the pane, the quick-reply bar
/// (`ConversationQuickReplyBar`, docs/plans/chat-view.md §10–§12; wireframe
/// docs/plans/chat-view/w5-quick-reply-message.svg): the two views end at
/// its top, the bar at the pane's bottom, and a hidden bar collapses to no
/// height, so the bar stands in the same place under a single message and
/// under a conversation's chat. The pane owns its controller
/// (`QuickReplyController`, over `QuickReplyHostAdapter`, which reaches
/// the window through `quickReplyDelegate`) and feeds it: a single
/// message's page as a model of one member
/// (`ConversationController.quickReplyInput(single:)`, shown unless the
/// message is in the Outbox or Drafts), a conversation's every change as
/// the host reports it (`ConversationController.quickReplyInput()`, shown
/// with the chat only), and nothing for the empty page and several selected
/// rows. The controller keeps the text per account and thread, so a
/// message and its conversation share it. The bar's Assistant button shows while the
/// Assistant is shown (the toolbar's rule and observer:
/// `AssistantController.shown`, `onChange`). The pane's background follows
/// the view shown, so that the bar sits on the reader's colour under a
/// message and on the conversation's surface under the chat.
@MainActor
final class ReadingPaneViewController: NSViewController {
    let reader: MessageViewController
    let conversation: ConversationHostViewController
    let controller: ConversationController

    /// The conversation view is the one on display.
    private(set) var showsConversation = false

    /// The window's message actions, for the quick-reply bar's host (the
    /// attribution, the download before a reply, the compose window;
    /// Integration+Conversation).
    weak var quickReplyDelegate: (any MessageActionDelegate)? {
        didSet { quickReplyHost.delegate = quickReplyDelegate }
    }

    /// Escape in the quick-reply bar's field: the message list takes the
    /// keyboard (Integration+Conversation).
    var onFocusList: (@MainActor () -> Void)? {
        get { replyBar.onFocusList }
        set { replyBar.onFocusList = newValue }
    }

    /// The quick-reply bar's Assistant button, with the button: the main
    /// window's `showAssistant(from:)`, the toolbar's ✦
    /// (Integration+Conversation).
    var onAssistant: (@MainActor (NSView) -> Void)? {
        get { replyBar.onAssistant }
        set { replyBar.onAssistant = newValue }
    }

    private let state: AppState
    private let quickReplyHost: QuickReplyHostAdapter
    /// The quick-reply bar's state (its host is weak: held here).
    let quickReply: QuickReplyController
    let replyBar: ConversationQuickReplyBar
    /// The Assistant shown or not (the bar's Assistant button), as the main
    /// window's toolbar hears it.
    private var assistantToken: AssistantController.Token?
    private var surface: ReadingPaneSurfaceView?

    init(reader: MessageViewController, conversation: ConversationHostViewController) {
        self.reader = reader
        self.conversation = conversation
        controller = conversation.controller
        state = conversation.state
        quickReplyHost = QuickReplyHostAdapter(state: conversation.state)
        quickReply = QuickReplyController(host: quickReplyHost)
        replyBar = ConversationQuickReplyBar(controller: quickReply)
        super.init(nibName: nil, bundle: nil)
        quickReplyHost.window = { [weak self] in self?.view.window }
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func loadView() {
        let container = ReadingPaneSurfaceView()
        container.translatesAutoresizingMaskIntoConstraints = false
        surface = container
        view = container
        for vc in [reader, conversation, replyBar] as [NSViewController] {
            addChild(vc)
            let v = vc.view
            v.translatesAutoresizingMaskIntoConstraints = false
            container.addSubview(v)
            NSLayoutConstraint.activate([
                v.leadingAnchor.constraint(equalTo: container.leadingAnchor),
                v.trailingAnchor.constraint(equalTo: container.trailingAnchor),
            ])
        }
        // Both views end at the quick-reply bar, which ends at the bottom
        // (no height while hidden).
        for v in [reader.view, conversation.view] {
            NSLayoutConstraint.activate([
                v.topAnchor.constraint(equalTo: container.topAnchor),
                v.bottomAnchor.constraint(equalTo: replyBar.view.topAnchor),
            ])
        }
        replyBar.view.bottomAnchor.constraint(equalTo: container.bottomAnchor).isActive = true
        conversation.view.isHidden = true

        conversation.onQuickReplyChange = { [weak self] in self?.conversationChanged() }
        replyBar.keyboardTarget = { [weak self] in self?.keyboardTarget }
        replyBar.assistantShown = state.assistant.shown
        assistantToken = state.assistant.onChange { [weak self] in
            guard let self else { return }
            self.replyBar.assistantShown = self.state.assistant.shown
        }
    }

    /// The list's selection (`ListController.onSelectedRowChanged`): a
    /// conversation row shows the conversation, another row its message,
    /// none the empty page.
    func show(_ row: ListRow?) {
        _ = view
        if let row, row.showsConversation {
            // Visible before the controller builds (at once when the members
            // are known), so the stack is laid out at the pane's size.
            setConversation(true)
            // The single-message view drops the message it showed.
            reader.clear()
            controller.show(row)
            conversationChanged()
            return
        }
        // The single-message view first: clearing the controller reports a
        // change, which `conversationChanged` then ignores instead of
        // hiding the bar under a message of the same thread.
        setConversation(false)
        controller.clear()
        if let row {
            reader.show(row.message)
            let input = controller.quickReplyInput(single: row.message)
            showSingleReply(input.model, actions: input.actions, account: input.account, shown: input.shown)
        } else {
            reader.clear()
            hideReply()
        }
    }

    /// Several rows are selected (`ListController.onSeveralSelected`):
    /// their count, no conversation, no quick-reply bar.
    func showSelection(count n: Int) {
        _ = view
        setConversation(false)
        controller.clear()
        reader.showSelection(count: n)
        hideReply()
    }

    /// Space and Shift-Space from the list: a page of the conversation;
    /// false when no conversation is shown (the key goes on as before).
    func page(up: Bool) -> Bool {
        guard showsConversation else { return false }
        return conversation.page(up: up)
    }

    /// What takes the keyboard when the quick-reply bar hides while its
    /// field has it (the bar's `keyboardTarget`): the view on display —
    /// the conversation's layout or the message's body.
    var keyboardTarget: NSView {
        showsConversation ? conversation.keyboardTarget : reader.keyboardTarget
    }

    private func setConversation(_ on: Bool) {
        guard on != showsConversation else { return }
        showsConversation = on
        conversation.view.isHidden = !on
        reader.view.isHidden = on
        surface?.conversation = on
    }

    // MARK: The quick-reply bar

    /// The quick-reply bar under the page of a single message: model `m` of
    /// the message (`Conversation.singleModel`), its card's `actions`, its
    /// account, and whether the page is a place of the bar (`shown`).
    /// Internal for the development aid (`MALACHI_START` `quick-reply-message`).
    func showSingleReply(_ m: Conversation.Model, actions: Capabilities.Actions, account: Account?, shown: Bool) {
        let acc = m.items.first?.message?.accountId ?? account?.id
        quickReply.show(acc, m.thread)
        quickReply.update(m, actions: actions, account: account)
        quickReply.setShown(shown)
    }

    /// The conversation shown changed, or its layout (the host's
    /// `onQuickReplyChange`): the bar follows it while the conversation is
    /// on display — its account and id first (a new one has no target until
    /// its model is built), then its model, the actions of the card the
    /// reply goes to and its account, and the chat (the timeline has no
    /// bar). Without a model (the members are on their way, or thread.get
    /// failed with nothing to build from) the bar gets the empty model and
    /// hides until the real one: the same thread shown a moment ago as a
    /// single message must not lend it its target. Ignored while the
    /// single-message view is shown: clearing the controller reports a
    /// change too, and `show(_:)` hides the conversation first.
    private func conversationChanged() {
        guard showsConversation else { return }
        quickReply.show(controller.account, controller.thread ?? "")
        if let input = controller.quickReplyInput() {
            quickReply.update(input.model, actions: input.actions, account: input.account)
        } else {
            quickReply.update(Conversation.Model(thread: ""), actions: .empty, account: nil)
        }
        quickReply.setShown(conversation.chatShown)
    }

    /// No place of the bar: the empty page, several rows.
    private func hideReply() {
        quickReply.show(nil, "")
        quickReply.setShown(false)
    }
}

/// The reading pane's background: the reader's colour (as
/// `ContentBackgroundView`) while it shows a message, the conversation's
/// surface (as `ConversationSurfaceView`) while it shows a conversation.
/// Only the quick-reply bar lets it show through.
@MainActor
private final class ReadingPaneSurfaceView: NSView {
    var conversation = false {
        didSet {
            if conversation != oldValue {
                needsDisplay = true
            }
        }
    }

    override init(frame: NSRect) {
        super.init(frame: frame)
        wantsLayer = true
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var wantsUpdateLayer: Bool { true }

    override func updateLayer() {
        layer?.backgroundColor = (conversation ? ConversationTint.surface : NSColor.controlBackgroundColor).cgColor
    }
}
