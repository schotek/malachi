// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The conversation in the reading pane (docs/plans/chat-view.md §6.1): the
/// one view the pane shows for a selected conversation row. Above, a fixed
/// strip that does not scroll (`ConversationStripView`: Collapse All /
/// Expand All of the timeline, the layout toggle, Show as Chat or Show as
/// Thread as the setting stands); below it, filling
/// the rest, the two layouts of the same conversation, of which one shows:
/// the timeline (`ConversationViewController`, the default) and the chat
/// (`ConversationChatViewController`). The setting `conversation-chat-layout`
/// picks it (`Settings.conversationChatLayout`, for every conversation and
/// across restarts); the toggle only writes the setting, and the layout
/// follows the setting's change, so a change from elsewhere switches too.
/// The hidden layout stays alive, so the timeline's folds and revealed
/// quoted text outlive a visit to the chat (D10).
///
/// The host is the conversation controller's only subscriber (it has one
/// `onChange` and one `onLoaded`): every change goes to both layouts, which
/// act on it at once while shown and catch up when shown again; the news of
/// a body goes to the layout shown, which renders every body held when it
/// is shown. The host alone trims the controller's bodies
/// (`ConversationController.trim`), with the near set of the layout shown;
/// a hidden layout asks for no body and the hidden timeline lets go of its
/// web views. Marking the conversation read is not the views' business: the
/// list controller arms its timer when the members are known
/// (`ConversationController`, `ListController.announceSelection`), whichever
/// layout shows.
///
/// The Jira issue card: each layout has its own (`ConversationIssueBox`) at
/// the top of its column, the timeline's beside its gutter, the chat's
/// across the column; the host hands the refreshed issue of a transition
/// and its busy state to both (`applyIssue`, `setIssueBusy`, from
/// Integration+Jira), and each card's status pill asks the controller for
/// the issue, so the Change Status menus (the menu bar's and the toolbar's
/// ask the list's selection, ChangeStatusMenus) need nothing from here. Two
/// cards are simpler than moving one between the layouts' stacks, whose
/// anchors and scroll positions it belongs to.
///
/// The fan-out of the message cache (`MessageDisplay`, set up by
/// `MessageWindows.track(display:)`) reaches the host, which keeps the
/// controller's entry (`ConversationController.adopt`) and passes the news
/// to the layout shown. Space and Shift-Space from the list page the layout
/// shown (`page(up:)`). Each layout has its own loading spinner; the
/// timeline has the status label of the link under the pointer (the chat
/// has no links).
///
/// Under the chat, and only there, the quick-reply bar
/// (`ConversationQuickReplyBar`, docs/plans/chat-view.md §10, §11): the
/// chat ends at its top, the bar at the container's bottom, and a hidden
/// bar collapses to no height (the timeline ends at the bottom whatever
/// it does). The host owns its controller (`QuickReplyController`, over
/// `QuickReplyHostAdapter`, which reaches the window through the
/// delegate), hands it every change of the conversation
/// (`ConversationController.quickReplyInput`: the model, the actions of
/// the card the reply goes to, its account) and the layout shown, and
/// shows its Assistant button while the Assistant is shown (the toolbar's
/// rule and observer: `AssistantController.shown`, `onChange`).
@MainActor
final class ConversationHostViewController: NSViewController, MessageDisplay {
    let state: AppState
    let controller: ConversationController
    let timeline: ConversationViewController
    let chat: ConversationChatViewController

    weak var delegate: (any MessageActionDelegate)? {
        didSet {
            timeline.delegate = delegate
            chat.delegate = delegate
            quickReplyHost.delegate = delegate
        }
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

    private let quickReplyHost: QuickReplyHostAdapter
    /// The quick-reply bar's state (its host is weak: held here).
    let quickReply: QuickReplyController
    private let replyBar: ConversationQuickReplyBar

    var onOpenEmbedded: (@MainActor (_ containing: MessageSummary, _ attachment: Attachment, _ remote: Bool, _ chip: NSView?) -> Void)? {
        didSet {
            timeline.onOpenEmbedded = onOpenEmbedded
        }
    }

    private let strip = ConversationStripView()
    /// The chat is the layout shown.
    private(set) var chatShown = false
    private var settingsToken: Settings.ChangeToken?
    /// The Assistant shown or not (the bar's Assistant button), as the main
    /// window's toolbar hears it.
    private var assistantToken: AssistantController.Token?

    init(state: AppState, cache: MessageCache, controller: ConversationController) {
        self.state = state
        self.controller = controller
        timeline = ConversationViewController(state: state, cache: cache, controller: controller)
        chat = ConversationChatViewController(state: state, controller: controller)
        quickReplyHost = QuickReplyHostAdapter(state: state)
        quickReply = QuickReplyController(host: quickReplyHost)
        replyBar = ConversationQuickReplyBar(controller: quickReply)
        super.init(nibName: nil, bundle: nil)
        quickReplyHost.window = { [weak self] in self?.view.window }
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    // MARK: View

    override func loadView() {
        let container = ConversationSurfaceView()
        container.translatesAutoresizingMaskIntoConstraints = false
        container.addSubview(strip)
        NSLayoutConstraint.activate([
            strip.topAnchor.constraint(equalTo: container.safeAreaLayoutGuide.topAnchor),
            strip.leadingAnchor.constraint(equalTo: container.leadingAnchor),
            strip.trailingAnchor.constraint(equalTo: container.trailingAnchor),
        ])
        for vc in [timeline, chat, replyBar] as [NSViewController] {
            addChild(vc)
            let v = vc.view
            v.translatesAutoresizingMaskIntoConstraints = false
            container.addSubview(v)
            NSLayoutConstraint.activate([
                v.leadingAnchor.constraint(equalTo: container.leadingAnchor),
                v.trailingAnchor.constraint(equalTo: container.trailingAnchor),
            ])
        }
        // The timeline fills the rest; the chat ends at the quick-reply
        // bar, which ends at the bottom (no height while hidden).
        NSLayoutConstraint.activate([
            timeline.view.topAnchor.constraint(equalTo: strip.bottomAnchor),
            timeline.view.bottomAnchor.constraint(equalTo: container.bottomAnchor),
            chat.view.topAnchor.constraint(equalTo: strip.bottomAnchor),
            chat.view.bottomAnchor.constraint(equalTo: replyBar.view.topAnchor),
            replyBar.view.bottomAnchor.constraint(equalTo: container.bottomAnchor),
        ])
        view = container

        strip.onFoldAll = { [weak self] in self?.timeline.foldAllClicked() }
        strip.onToggle = { [weak self] on in
            // The setting switches the layout (`settingsToken`).
            self?.state.settings.conversationChatLayout = on
        }
        timeline.onFoldAllChanged = { [weak self] offer in
            guard let self, !self.chatShown else { return }
            self.strip.setFoldAll(offer)
        }
        timeline.onNear = { [weak self] near in
            guard let self, !self.chatShown else { return }
            self.controller.trim(keeping: near)
        }
        chat.onNear = { [weak self] near in
            guard let self, self.chatShown else { return }
            self.controller.trim(keeping: near)
        }
        controller.onChange = { [weak self] change in self?.changed(change) }
        controller.onLoaded = { [weak self] id, lm in self?.loaded(id, lm) }
        settingsToken = state.settings.onChange(.conversationChatLayout) { [weak self] in self?.applyLayout() }
        replyBar.assistantShown = state.assistant.shown
        assistantToken = state.assistant.onChange { [weak self] in
            guard let self else { return }
            self.replyBar.assistantShown = self.state.assistant.shown
        }
        applyLayout(initial: true)
    }

    /// Shows the layout the setting asks for: the other one is hidden first
    /// (it lets go of what it holds), then the one asked for shown (it
    /// catches up with the controller), and the strip follows.
    private func applyLayout(initial: Bool = false) {
        _ = view
        let on = state.settings.conversationChatLayout
        strip.setChat(on)
        guard on != chatShown || initial else { return }
        chatShown = on
        let (shown, hidden): (NSViewController, NSViewController) = on ? (chat, timeline) : (timeline, chat)
        if on {
            timeline.setShown(false)
        } else {
            chat.setShown(false)
        }
        hidden.view.isHidden = true
        shown.view.isHidden = false
        quickReply.setChatShown(on)
        if on {
            strip.setFoldAll(.none)
            chat.setShown(true)
        } else {
            timeline.setShown(true)
            strip.setFoldAll(timeline.foldAll)
        }
    }

    // MARK: The controller's changes

    private func changed(_ change: ConversationController.Change) {
        _ = view
        timeline.changed(change)
        chat.changed(change)
        updateQuickReply()
    }

    /// The quick-reply bar follows the conversation shown: its id first
    /// (a new one has no target until its model is built), then its model,
    /// the actions of the card the reply goes to and its account.
    private func updateQuickReply() {
        quickReply.show(controller.thread ?? "")
        if let input = controller.quickReplyInput() {
            quickReply.update(input.model, actions: input.actions, account: input.account)
        }
    }

    private func loaded(_ id: MessageID, _ lm: LoadedMessage) {
        if chatShown {
            chat.loaded(id, lm)
        } else {
            timeline.loaded(id, lm)
        }
    }

    // MARK: Keyboard

    /// Space (or Shift-Space, `up`) from the list: one page of the layout
    /// shown. True: the key was used.
    func page(up: Bool) -> Bool {
        chatShown ? chat.page(up: up) : timeline.page(up: up)
    }

    // MARK: Issue

    /// The refreshed issue of a transition (`IssueActionsController.
    /// onIssueChanged`): both layouts' cards show it.
    func applyIssue(_ info: IssueInfo, account: AccountID) {
        timeline.applyIssue(info, account: account)
        chat.applyIssue(info, account: account)
    }

    /// A transition started or ended on the issue `key` of `account`: both
    /// layouts' pills.
    func setIssueBusy(_ busy: Bool, account: AccountID, key: String) {
        timeline.setIssueBusy(busy, account: account, key: key)
        chat.setIssueBusy(busy, account: account, key: key)
    }

    // MARK: MessageDisplay

    func displays(_ id: MessageID) -> Bool {
        timeline.displays(id) || chat.displays(id)
    }

    func showLoaded(_ id: MessageID, _ lm: LoadedMessage) {
        controller.adopt(id, lm)
        if chatShown {
            chat.loaded(id, lm)
        } else {
            timeline.showLoaded(id, lm)
        }
    }

    func refreshRemoteBar(_ id: MessageID, _ lm: LoadedMessage) {
        if !chatShown {
            timeline.refreshRemoteBar(id, lm)
        }
    }

    func refreshBulk(_ id: MessageID, _ lm: LoadedMessage) {
        if !chatShown {
            timeline.refreshBulk(id, lm)
        }
    }

    func refreshChips(_ id: MessageID, _ lm: LoadedMessage?) {
        if !chatShown {
            timeline.refreshChips(id, lm)
        } else if let lm {
            chat.loaded(id, lm)
        }
    }

    func showOutboxState(_ id: MessageID, _ m: Message?) {
        if !chatShown {
            timeline.showOutboxState(id, m)
        }
    }
}
