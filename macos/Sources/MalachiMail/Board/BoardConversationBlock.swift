// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// What the board's detail needs of Mail to show a message's HTML: the
/// loaded-message cache (message.body, and the `malachi-cid:` parts of the
/// web view), the text-zoom setting, and the registration with the message
/// windows' fan-out (`MessageWindows.track(display:)`), which tells a
/// display what the cache learns and gives it Mail's message actions (the
/// link policy). Set by the application (Integration+Board) for the
/// daemon's board and the samples alike; the samples' members have no ids,
/// so nothing is ever asked for them.
@MainActor
struct BoardMailBodies {
    let cache: MessageCache
    let settings: Settings
    let track: @MainActor (any MessageDisplay) -> Void
}

/// The conversation of the board's detail (`lower`): the heading with Show
/// in Mail, then the cards (`BoardMessageCardView`), or while the
/// conversation loads a spinner row instead, and the note with Try Again
/// when it could not be loaded.
///
/// It lives as long as the detail and is never rebuilt with it: a refresh
/// of the board (every autosave of the inline reply lists the board again)
/// changes the heading and the note in place and touches the cards only
/// when the case or its members changed (`Board.ConversationCards.apply`,
/// keyed by the case and each member's id and excerpt). Another case
/// starts over and lets every web view go; a new message adds its card
/// and keeps the others, with their web views, as they are.
///
/// Which card shows what is `Board.ConversationCards`'s: the newest open,
/// older ones folded to a preview; an open card of a message with an id
/// asks for its body through the cache (`fetchBody`, the variant the cache
/// entry shows, trimmed of its quoted history unless Mail revealed it) and
/// shows HTML in the web view of Mail's conversation cards while the block
/// is live (in a window and not hidden), at most `maxLiveWebViews` of them;
/// anything else keeps the excerpt. The block is a `MessageDisplay`: a body
/// that changes (remote images loaded in Mail) shows here too, and links go
/// through Mail's `openLink` of the delegate it is given.
@MainActor
final class BoardConversationBlock: NSView, MessageDisplay {
    weak var delegate: (any MessageActionDelegate)?
    var onOpenEmbedded: (@MainActor (_ containing: MessageSummary, _ attachment: Attachment, _ remote: Bool, _ chip: NSView?) -> Void)?

    /// Show in Mail and the conversation's Try Again.
    var onShowInMail: (() -> Void)?
    var onRetry: (() -> Void)?
    /// The link under the pointer in a card ("" when none).
    var onHover: ((String) -> Void)?
    /// Runs a change of a card's height that the user did not make, for
    /// the detail to keep what the user sees in place.
    var onHeightChanging: ((_ card: NSView, _ change: () -> Void) -> Void)?

    private let actions: BoardActions
    private let stack = FillStackView()
    private let heading = NSTextField(labelWithString: "")
    private let showButton = NSButton()
    /// The spinner row or the note, between the heading and the cards.
    private var statusView: NSView?
    private var status: Status?
    private let cardsColumn = FillStackView()
    /// Same indices as `state.members`.
    private(set) var cards: [BoardMessageCardView] = []
    private(set) var state = Board.ConversationCards()
    private var account: AccountID?
    private var registered = false
    private var zoomToken: Settings.ChangeToken?

    /// What the status row shows.
    private struct Status: Equatable {
        var loading: Bool
        var note: String
        var retry: Bool
    }

    init(actions: BoardActions) {
        self.actions = actions
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        heading.font = .systemFont(ofSize: Typo.bodySize, weight: .bold)
        heading.lineBreakMode = .byTruncatingTail
        heading.isSelectable = false
        heading.setAccessibilityRole(.staticText)
        heading.setContentHuggingPriority(.defaultLow, for: .horizontal)
        heading.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        showButton.title = Board.Text.showInMail
        showButton.bezelStyle = .rounded
        showButton.controlSize = .small
        showButton.font = .systemFont(ofSize: NSFont.systemFontSize(for: .small))
        showButton.target = self
        showButton.action = #selector(showInMailClicked(_:))
        showButton.setContentHuggingPriority(.required, for: .horizontal)
        showButton.setContentCompressionResistancePriority(NSLayoutConstraint.Priority(450), for: .horizontal)
        showButton.setAccessibilityLabel(Board.Text.showInMail)
        let top = NSStackView(views: [heading, showButton])
        top.orientation = .horizontal
        top.alignment = .centerY
        top.distribution = .fill
        top.spacing = 8
        cardsColumn.spacing = 8
        stack.spacing = 8
        stack.addArrangedSubview(top)
        stack.addArrangedSubview(cardsColumn)
        addSubview(stack)
        NSLayoutConstraint.activate([
            stack.topAnchor.constraint(equalTo: topAnchor),
            stack.bottomAnchor.constraint(equalTo: bottomAnchor),
            stack.leadingAnchor.constraint(equalTo: leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: trailingAnchor),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    // MARK: Applying the detail

    /// Brings the block up to `d` (nil: no case; every card goes).
    func apply(_ d: Board.Detail?) {
        guard let d else {
            removeAllCards()
            state = Board.ConversationCards()
            account = nil
            return
        }
        account = d.accountID
        if heading.stringValue != d.conversationTitle {
            heading.stringValue = d.conversationTitle
        }
        let canShow = actions.canShowInMail(d.id)
        if showButton.isEnabled != canShow {
            showButton.isEnabled = canShow
        }
        let s = Status(loading: d.messagesLoading, note: d.messagesNote, retry: d.messagesRetry)
        applyStatus(s.loading || !s.note.isEmpty ? s : nil)
        if s.loading || !s.note.isEmpty {
            // The cards of the same case wait hidden for the conversation;
            // another case's go.
            if state.caseID != d.id {
                removeAllCards()
                state = Board.ConversationCards()
            }
            cardsColumn.isHidden = true
            return
        }
        cardsColumn.isHidden = false
        var reloads: [Int] = []
        switch state.apply(Board.ConversationCards.key(d)) {
        case .none:
            break
        case .reset:
            removeAllCards()
            cards = d.messages.map { makeCard($0) }
            for card in cards {
                cardsColumn.addArrangedSubview(card)
            }
        case .members(let kept, let reload):
            var next: [BoardMessageCardView] = []
            var stays = Set<ObjectIdentifier>()
            for (i, m) in d.messages.enumerated() {
                if let old = kept[i] {
                    // The same card, whatever the excerpt now says.
                    cards[old].update(m)
                    next.append(cards[old])
                    stays.insert(ObjectIdentifier(cards[old]))
                } else {
                    next.append(makeCard(m))
                }
            }
            var hadRemoved = false
            for card in cards where !stays.contains(ObjectIdentifier(card)) {
                hadRemoved = true
                card.onHeightChanging = nil
                card.apply(.text, folded: false, web: nil) // lets its web view go
                cardsColumn.removeArrangedSubview(card)
                card.removeFromSuperview()
            }
            // In place: a card that stays is never taken out of the
            // window, so its web view keeps its document.
            for (i, card) in next.enumerated() {
                let arranged = cardsColumn.arrangedSubviews
                if i < arranged.count, arranged[i] === card {
                    continue
                }
                if arranged.contains(where: { $0 === card }) {
                    cardsColumn.removeArrangedSubview(card)
                }
                cardsColumn.insertArrangedSubview(card, at: i)
            }
            cards = next
            if hadRemoved {
                onHover?("")
            }
            reloads = reload
        }
        refreshCards()
        fetchBodies()
        for i in reloads {
            fetchBody(i)
        }
    }

    private func applyStatus(_ s: Status?) {
        guard s != status else { return }
        status = s
        if let v = statusView {
            stack.removeArrangedSubview(v)
            v.removeFromSuperview()
            statusView = nil
        }
        guard let s else { return }
        let v: NSView
        if s.loading {
            let spinner = Spinner(size: 16)
            spinner.start()
            let note = NSTextField(labelWithString: s.note)
            note.font = Typo.caption
            note.textColor = Tint.secondary
            note.isSelectable = false
            let row = NSStackView(views: [spinner, note])
            row.orientation = .horizontal
            row.alignment = .centerY
            row.spacing = 6
            v = row
        } else {
            let note = PrefsWrappingLabel.board(s.note, font: Typo.caption, color: Tint.secondary)
            if s.retry {
                let retry = NSButton(title: Board.Text.tryAgain, target: self, action: #selector(retryClicked(_:)))
                retry.isBordered = false
                retry.bezelStyle = .inline
                retry.setButtonType(.momentaryChange)
                retry.font = Typo.caption
                retry.contentTintColor = .linkColor
                retry.setAccessibilityLabel(Board.Text.tryAgain)
                // The note as wide as the column, the link at its own width
                // under it (as the deadline's quote under its chip).
                let column = NSStackView(views: [note, retry])
                column.orientation = .vertical
                column.alignment = .leading
                column.spacing = 2
                column.translatesAutoresizingMaskIntoConstraints = false
                note.widthAnchor.constraint(equalTo: column.widthAnchor).isActive = true
                v = column
            } else {
                v = note
            }
        }
        stack.insertArrangedSubview(v, at: 1)
        statusView = v
    }

    private func makeCard(_ m: Board.MessageCard) -> BoardMessageCardView {
        let card = BoardMessageCardView(m)
        card.onFold = { [weak self, weak card] folded in
            guard let self, let card else { return }
            self.userFolded(card, folded)
        }
        card.onUnavailable = { [weak self, weak card] in
            guard let self, let card, let i = self.cards.firstIndex(where: { $0 === card }) else { return }
            _ = self.state.answered(i, html: false)
            self.refreshCards()
        }
        card.onHeightChanging = { [weak self] card, change in
            if let self, let host = self.onHeightChanging {
                host(card, change)
            } else {
                change()
            }
        }
        return card
    }

    private func removeAllCards() {
        for card in cards {
            card.onHeightChanging = nil
            card.apply(.text, folded: false, web: nil) // lets its web view go
            cardsColumn.removeArrangedSubview(card)
            card.removeFromSuperview()
        }
        cards = []
        onHover?("")
    }

    // MARK: What the cards show

    /// The block may hold web views now: in a window, not hidden, with
    /// Mail's cache at hand.
    private var live: Bool {
        actions.mailBodies != nil && window != nil && !isHiddenOrHasHiddenAncestor
    }

    private var web: BoardCardWeb? {
        guard let env = actions.mailBodies else { return nil }
        let settings = env.settings
        return BoardCardWeb(
            cache: env.cache, zoom: { settings.textZoom },
            openLink: { [weak self] link, links, window in
                self?.delegate?.openLink(link.href, links: links, from: window)
            },
            hover: { [weak self] href in self?.onHover?(href) })
    }

    /// Applies the state to every card: its arrow, its fold, the text or
    /// the web view.
    private func refreshCards() {
        let live = live
        let canFetch = actions.mailBodies != nil
        let web = web
        if web != nil, zoomToken == nil, let env = actions.mailBodies {
            zoomToken = env.settings.onChange(.textZoom) { [weak self] in
                guard let self, let env = self.actions.mailBodies else { return }
                for card in self.cards {
                    card.setZoom(env.settings.textZoom)
                }
            }
        }
        for (i, card) in cards.enumerated() {
            card.configure(foldable: state.foldable(i), arrowAlways: state.arrow(i, long: false, canFetch: canFetch))
            card.apply(state.shows(i, live: live), folded: state.isFolded(i), web: web)
        }
    }

    /// Asks for the bodies the open cards need, while the block is live.
    private func fetchBodies() {
        guard live, let env = actions.mailBodies, account != nil else { return }
        register(env)
        for i in state.members.indices where state.needsBody(i) {
            state.asked(i)
            fetchBody(i)
        }
    }

    /// message.body for card `i` through the cache; the answer shows in
    /// place (`loaded`). Also the reload of an open card whose message the
    /// daemon rebuilt (the cache forgot it then, so this asks again).
    private func fetchBody(_ i: Int) {
        guard live, let env = actions.mailBodies, let account, state.members.indices.contains(i),
              let id = state.members[i].id
        else { return }
        register(env)
        let member = state.members[i]
        env.cache.fetchBody(Self.summary(id, account: account)) { [weak self] lm in
            self?.loaded(member, lm)
        }
    }

    /// The summary message.body needs: the ids. Nothing else of it is read
    /// (`MessageCache.fetchBody`).
    private static func summary(_ id: MessageID, account: AccountID) -> MessageSummary {
        MessageSummary(
            id: id, accountId: account, folderId: FolderID(""), from: [], subject: "", date: .goZero, snippet: "",
            flags: [], hasAttachments: false, size: 0)
    }

    private func register(_ env: BoardMailBodies) {
        guard !registered else { return }
        registered = true
        env.track(self)
    }

    /// What the cache holds for `member` now: HTML for the web view, or
    /// anything else for the excerpt.
    private func loaded(_ member: Board.ConversationCards.Member, _ lm: LoadedMessage) {
        guard lm.bodySettled, !lm.fetching, let i = state.members.firstIndex(of: member), i < cards.count else { return }
        let html: String? = lm.err == nil && showsHTML(lm.body) ? lm.body?.html : nil
        bodyArrived(i, html: html, links: lm.body?.links ?? [])
    }

    /// The body of card `i` is `html` (nil: none to show, the excerpt
    /// stays). The one way a card gets HTML: message.body's answer, and the
    /// development hook's invented document.
    private func bodyArrived(_ i: Int, html: String?, links: [Link]) {
        let card = cards[i]
        card.setHTML(html, links: links)
        _ = state.answered(i, html: html != nil)
        refreshCards()
    }

    /// The card's arrow: the state decides, the limit may fold another.
    private func userFolded(_ card: BoardMessageCardView, _ folded: Bool) {
        guard let i = cards.firstIndex(where: { $0 === card }) else { return }
        _ = state.setFolded(i, folded)
        refreshCards()
        fetchBodies()
    }

    // MARK: Live

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        liveChanged()
    }

    override func viewDidHide() {
        super.viewDidHide()
        liveChanged()
    }

    override func viewDidUnhide() {
        super.viewDidUnhide()
        liveChanged()
    }

    /// Whether the block is in a window and shown changes while AppKit
    /// lays the window out (a panel hiding, a style switch), and
    /// `refreshCards` can change a card's height, which the detail measures
    /// with `layoutSubtreeIfNeeded`: never from inside a layout pass, so the
    /// refresh waits for the next turn of the main queue (what `live` says
    /// then is what counts).
    private func liveChanged() {
        guard !liveChangePending else { return }
        liveChangePending = true
        Task { @MainActor [weak self] in
            guard let self else { return }
            self.liveChangePending = false
            self.refreshCards()
            self.fetchBodies()
        }
    }

    private var liveChangePending = false

    // MARK: MessageDisplay

    func displays(_ id: MessageID) -> Bool {
        state.index(of: id) != nil
    }

    func showLoaded(_ id: MessageID, _ lm: LoadedMessage) {
        guard let i = state.index(of: id), state.body(i) != .unknown else { return }
        loaded(state.members[i], lm)
    }

    func refreshRemoteBar(_ id: MessageID, _ lm: LoadedMessage) {}

    func refreshBulk(_ id: MessageID, _ lm: LoadedMessage) {}

    func refreshChips(_ id: MessageID, _ lm: LoadedMessage?) {}

    func showOutboxState(_ id: MessageID, _ m: Message?) {}

    // MARK: Actions

    @objc private func showInMailClicked(_ sender: Any?) {
        onShowInMail?()
    }

    @objc private func retryClicked(_ sender: Any?) {
        onRetry?()
    }

    // MARK: Development aid

    /// DEVELOPMENT AID (`MALACHI_START`, samples only): card `i` gets
    /// `html` as message.body's HTML answer would give it (`bodyArrived`).
    func developmentFeed(_ i: Int, html: String) {
        guard actions.samples, cards.indices.contains(i) else { return }
        bodyArrived(i, html: html, links: [])
    }

    /// DEVELOPMENT AID (samples only): card `i` opened as by its arrow.
    func developmentOpen(_ i: Int) {
        guard actions.samples, cards.indices.contains(i) else { return }
        userFolded(cards[i], false)
    }

    /// DEVELOPMENT AID: the cards holding a web view now.
    var developmentLiveWebViews: Int {
        cards.filter { $0.webView != nil }.count
    }
}
