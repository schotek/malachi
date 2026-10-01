// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The whole conversation in the reading pane (ConversationController,
/// ui/internal/conversation): selecting a conversation row stacks every
/// member the folder holds as Jira shows an issue
/// (`ConversationLayout.displayOrder`, conversation_view.go): what opened
/// the conversation first, folded to its header and a preview while more
/// follows, then the rest newest first, the pane opened at its top; the
/// model keeps the oldest first. The user's replies in Sent that the folder
/// lacks are cards among them by date, folded. Every card folds and opens
/// with its arrow (`Conversation.Folds`, fold.go), and one button above the
/// conversation folds or opens them all (Collapse All / Expand All,
/// `Conversation.foldAllOffer`). A Jira conversation has its issue card
/// once on top; its description and comments are cards
/// (`ConversationCardView`), its status and assignee changes compact rows
/// (`ConversationEventRow`); older members left out by thread.get's cap
/// are one row at the bottom (`ConversationTruncatedRow`).
///
/// The pane is slightly grey and the messages are cards on it, in one
/// column no wider than 900 points. A gutter at the column's leading edge
/// carries the timeline (`ConversationLayout.rails`, `ConversationRow`): a
/// thin line from the first item shown to the last, on it the avatar of
/// each message's sender at the top of its card (tinted with the accent
/// colour for the user's own message) and a dot for each event and for the
/// row of older members. The issue card keeps to the cards' column.
///
/// A stack of native cards in one scroll view, never one composed
/// document: each card's body is its own locked view with one sanitiser
/// output in it (docs/security.md §3.2), so a message's CSS cannot restyle
/// or forge another message's headers, which are plain text. The cards
/// are cheap: a body is fetched only near the viewport and a web view
/// exists only for the nearest few HTML cards (`ConversationLayout.live`);
/// the others keep the height they last had. While heights settle (bodies
/// arriving, web views measuring their documents) the item the user reads
/// stays in place, and until the user scrolls that is the top of the
/// conversation, where an arrival shows (right under the card that opened
/// it, or on top).
/// Links under the pointer show in one status label at the bottom of the
/// pane. The list keeps the keyboard: Space and Shift-Space page through
/// the conversation (`page(up:)`).
@MainActor
final class ConversationViewController: NSViewController, MessageDisplay, ConversationCardHost {
    let state: AppState
    let cache: MessageCache
    let controller: ConversationController

    weak var delegate: (any MessageActionDelegate)? {
        didSet {
            for card in cards.values {
                card.refreshActions()
            }
        }
    }

    var onOpenEmbedded: (@MainActor (_ containing: MessageSummary, _ attachment: Attachment, _ remote: Bool, _ chip: NSView?) -> Void)?

    /// Keeps an item where it is on screen: `offset` is how far the
    /// viewport's top was below the item's top.
    private struct Anchor {
        weak var view: NSView?
        var offset: CGFloat
    }

    private let scroll = ConversationScrollView()
    private let clamp: ClampView
    private let stack = FillStackView()
    /// Collapse All / Expand All, at the trailing end of a strip above the
    /// conversation; hidden while fewer than two cards fold.
    private let foldAllBox = NSView()
    private let foldAllButton = NSButton(title: "", target: nil, action: nil)
    /// What the button offers now.
    private var foldAll = Conversation.FoldAll.none
    private let issueBox = NSView()
    private let issueCard = IssueCardView()
    private let truncatedRow = ConversationTruncatedRow()
    private lazy var truncatedItem = ConversationRow(
        content: truncatedRow, mark: .dot(below: ConversationMetrics.captionMiddle))
    private var cards: [MessageID: ConversationCardView] = [:]
    private var events: [MessageID: ConversationEventRow] = [:]
    /// The rows of the cards and of the events: each with its piece of the
    /// timeline.
    private var rows: [MessageID: ConversationRow] = [:]
    /// The rows of the model's items, in the order shown
    /// (`ConversationLayout.displayOrder`).
    private var itemViews: [NSView] = []
    /// The user's folds and unfolds of the cards of the conversation on
    /// show (`cardSetFolded`, Collapse All / Expand All), which win over
    /// the default (`Conversation.defaultFolds`) until another conversation
    /// is shown.
    private var folds = Conversation.Folds()
    /// The cards' fold state as last applied (`Conversation.Folds.state`).
    private var foldState: [MessageID: Bool] = [:]
    /// The pane is narrow: the items show the short date.
    private var compactDates = false
    private let statusBox = NSView()
    private let statusLabel = NSTextField(labelWithString: "")
    private let loadingSpinner = Spinner(size: 32)
    private var spinnerWork: DispatchWorkItem?
    private var settingsTokens: [Settings.ChangeToken] = []

    /// What is kept in view until the user scrolls: the top of the
    /// conversation (`topAnchor`), from when it is opened.
    private var pinned: Anchor?
    /// Where the user was, for a change of the pane's width.
    private var lastAnchor: Anchor?
    private var lastClipWidth: CGFloat = 0
    private var anchorDepth = 0
    private var liveUpdateScheduled = false

    init(state: AppState, cache: MessageCache, controller: ConversationController) {
        self.state = state
        self.cache = cache
        self.controller = controller
        let widest = CGFloat(ConversationLayout.Metrics.maxWidth)
        clamp = ClampView(maximum: widest, tight: widest)
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    // MARK: View

    override func loadView() {
        // The rows touch (each has the gap above its item in it), so the
        // pieces of the timeline join.
        stack.spacing = 0
        stack.edgeInsets = NSEdgeInsets(
            top: Self.topInset, left: ConversationMetrics.sideInset, bottom: ConversationMetrics.sideInset,
            right: ConversationMetrics.sideInset)
        // The issue card in the cards' column, the gutter beside it empty.
        issueBox.translatesAutoresizingMaskIntoConstraints = false
        issueBox.addSubview(issueCard)
        NSLayoutConstraint.activate([
            issueCard.topAnchor.constraint(equalTo: issueBox.topAnchor, constant: ConversationMetrics.itemGap),
            issueCard.bottomAnchor.constraint(equalTo: issueBox.bottomAnchor),
            issueCard.leadingAnchor.constraint(equalTo: issueBox.leadingAnchor, constant: ConversationMetrics.gutter),
            issueCard.trailingAnchor.constraint(equalTo: issueBox.trailingAnchor),
        ])
        issueBox.isHidden = true
        issueCard.onOpen = { [weak self] url in self?.openIssueLink(url) }
        issueCard.statusMenu = IssueTransitionMenu(state: state) { [weak self] in self?.transitionSubject }
        // Collapse All / Expand All: a small borderless text button at the
        // trailing end of the cards' column, above everything else.
        foldAllButton.isBordered = false
        foldAllButton.bezelStyle = .inline
        foldAllButton.setButtonType(.momentaryChange)
        foldAllButton.font = Typo.caption
        foldAllButton.contentTintColor = .linkColor
        foldAllButton.target = self
        foldAllButton.action = #selector(foldAllClicked(_:))
        foldAllButton.translatesAutoresizingMaskIntoConstraints = false
        foldAllBox.translatesAutoresizingMaskIntoConstraints = false
        foldAllBox.addSubview(foldAllButton)
        NSLayoutConstraint.activate([
            foldAllButton.topAnchor.constraint(equalTo: foldAllBox.topAnchor, constant: Self.foldAllTop),
            foldAllButton.bottomAnchor.constraint(equalTo: foldAllBox.bottomAnchor),
            foldAllButton.trailingAnchor.constraint(equalTo: foldAllBox.trailingAnchor),
            foldAllButton.leadingAnchor.constraint(
                greaterThanOrEqualTo: foldAllBox.leadingAnchor, constant: ConversationMetrics.gutter),
        ])
        foldAllBox.isHidden = true
        stack.addArrangedSubview(foldAllBox)
        stack.addArrangedSubview(issueBox)
        clamp.setChild(stack)

        scroll.documentView = clamp
        scroll.hasVerticalScroller = true
        scroll.hasHorizontalScroller = false
        scroll.autohidesScrollers = true
        scroll.drawsBackground = false
        scroll.translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            clamp.widthAnchor.constraint(equalTo: scroll.contentView.widthAnchor),
        ])
        // The user scrolled: from now on the item under the viewport's top
        // is what stays in place, no longer the top of the conversation.
        scroll.onUserScroll = { [weak self] in self?.pinned = nil }
        NotificationCenter.default.addObserver(
            self, selector: #selector(liveScrollStarted(_:)), name: NSScrollView.willStartLiveScrollNotification,
            object: scroll)
        let clip = scroll.contentView
        clip.postsBoundsChangedNotifications = true
        clip.postsFrameChangedNotifications = true
        NotificationCenter.default.addObserver(
            self, selector: #selector(clipBoundsChanged(_:)), name: NSView.boundsDidChangeNotification, object: clip)
        NotificationCenter.default.addObserver(
            self, selector: #selector(clipFrameChanged(_:)), name: NSView.frameDidChangeNotification, object: clip)

        // The link under the pointer in any card: plain text, the middle
        // elided, as the single-message view shows it.
        statusLabel.font = Typo.caption
        statusLabel.textColor = .labelColor
        statusLabel.lineBreakMode = .byTruncatingMiddle
        statusLabel.maximumNumberOfLines = 1
        statusLabel.isSelectable = false
        statusLabel.setContentCompressionResistancePriority(NSLayoutConstraint.Priority(240), for: .horizontal)
        statusLabel.setContentHuggingPriority(.defaultLow, for: .horizontal)
        statusLabel.translatesAutoresizingMaskIntoConstraints = false
        statusBox.translatesAutoresizingMaskIntoConstraints = false
        statusBox.wantsLayer = true
        statusBox.layer?.backgroundColor = NSColor.windowBackgroundColor.withAlphaComponent(0.92).cgColor
        statusBox.layer?.cornerRadius = 4
        statusBox.layer?.borderWidth = 1
        statusBox.layer?.borderColor = NSColor.separatorColor.cgColor
        statusBox.isHidden = true
        statusBox.addSubview(statusLabel)

        loadingSpinner.setAccessibilityLabel(L10n.C("accessibility", "Loading the message"))

        let container = ConversationSurfaceView()
        container.translatesAutoresizingMaskIntoConstraints = false
        container.addSubview(scroll)
        container.addSubview(statusBox)
        container.addSubview(loadingSpinner)
        NSLayoutConstraint.activate([
            scroll.topAnchor.constraint(equalTo: container.safeAreaLayoutGuide.topAnchor),
            scroll.bottomAnchor.constraint(equalTo: container.bottomAnchor),
            scroll.leadingAnchor.constraint(equalTo: container.leadingAnchor),
            scroll.trailingAnchor.constraint(equalTo: container.trailingAnchor),
            statusBox.leadingAnchor.constraint(equalTo: container.leadingAnchor, constant: 6),
            statusBox.bottomAnchor.constraint(equalTo: container.bottomAnchor, constant: -6),
            statusBox.widthAnchor.constraint(lessThanOrEqualTo: container.widthAnchor, multiplier: 0.8),
            statusLabel.leadingAnchor.constraint(equalTo: statusBox.leadingAnchor, constant: 6),
            statusBox.trailingAnchor.constraint(equalTo: statusLabel.trailingAnchor, constant: 6),
            statusLabel.topAnchor.constraint(equalTo: statusBox.topAnchor, constant: 2),
            statusBox.bottomAnchor.constraint(equalTo: statusLabel.bottomAnchor, constant: 2),
            loadingSpinner.centerXAnchor.constraint(equalTo: container.centerXAnchor),
            loadingSpinner.centerYAnchor.constraint(equalTo: container.centerYAnchor),
        ])
        view = container

        controller.onChange = { [weak self] change in self?.modelChanged(change) }
        controller.onLoaded = { [weak self] id, lm in self?.cards[id]?.render(lm) }
        let settings = state.settings
        settingsTokens.append(settings.onChange(.textZoom) { [weak self] in self?.applyBodyFont() })
        settingsTokens.append(settings.onChange(.monospacePlainText) { [weak self] in self?.applyBodyFont() })
        settingsTokens.append(settings.onChange(.monochromeAvatars) { [weak self] in self?.applyRails() })
    }

    /// Above the first row's own gap.
    private static let topInset: CGFloat = 4
    /// Above the Collapse All / Expand All button, under the top inset.
    private static let foldAllTop: CGFloat = 4
    /// The views of the stack before the model's items: the strip of the
    /// Collapse All button and the issue card.
    private static let headViews = 2

    // MARK: Model

    private func modelChanged(_ change: ConversationController.Change) {
        _ = view
        switch change {
        case .loading:
            removeAll()
            startSpinner()
        case .opened:
            stopSpinner()
            open()
        case .updated:
            stopSpinner()
            update()
        case .cleared:
            stopSpinner()
            removeAll()
        }
    }

    /// A conversation built anew (conversation_view.go `open`): what
    /// opened it first (folded while more follows), then the rest newest
    /// first, the top of the conversation pinned at the viewport's top, and
    /// the cards near it asked for their bodies. Nothing below can push the
    /// newest message out of view while the heights settle.
    private func open() {
        removeAll()
        guard let model = controller.model else { return }
        compactDates = ConversationLayout.compactDates(pane: Double(scroll.contentView.frame.width))
        anchorDepth += 1
        apply(model)
        anchorDepth -= 1
        view.layoutSubtreeIfNeeded()
        pinned = topAnchor
        restore(pinned)
        updateLive()
    }

    /// The shown conversation changed (conversation_view.go `update`): the
    /// cards are reconciled by id and what the user reads stays in place;
    /// while the pane shows its top, it stays there, so an arrival (under
    /// the opening card, or on top) is in view.
    private func update() {
        guard let model = controller.model else { return }
        if isAtStart() {
            pinned = topAnchor
        }
        preservingAnchor {
            apply(model)
        }
    }

    /// The model's items in the order the pane shows them, and which of
    /// them opened the conversation.
    private func shownOrder(_ model: Conversation.Model) -> ConversationLayout.Display {
        ConversationLayout.displayOrder(model.items.filter { $0.kind == .truncated || $0.id != nil })
    }

    /// Puts `model`'s items into the stack in the order shown
    /// (`ConversationLayout.displayOrder`: what opened the conversation,
    /// then the rest newest first), reusing the views of the members shown
    /// already, each in a row with its piece of the timeline; every card
    /// folds as the user or the default says (`Conversation.Folds.state`,
    /// conversation_view.go `apply`), and the Collapse All / Expand All
    /// button follows.
    private func apply(_ model: Conversation.Model) {
        showIssue(model)
        let display = shownOrder(model)
        let items = display.items
        folds.show(model.thread)
        foldState = folds.state(items, opening: display.opening)
        let rails = ConversationLayout.rails(items)
        let monochrome = state.settings.monochromeAvatars
        var desired: [NSView] = []
        var cardIDs = Set<MessageID>()
        var eventIDs = Set<MessageID>()
        for (item, rail) in zip(items, rails) {
            let row: ConversationRow
            switch item.kind {
            case .truncated:
                truncatedRow.text = item.text
                row = truncatedItem
            case .message:
                guard let id = item.id else { continue }
                cardIDs.insert(id)
                let foldable = Conversation.foldable(item)
                let folded = foldState[id] ?? false
                if let card = cards[id], let shown = rows[id] {
                    card.update(item)
                    card.setFold(foldable, folded: folded)
                    row = shown
                } else {
                    let card = makeCard(item, id)
                    card.setFold(foldable, folded: folded)
                    card.render(controller.loaded[id])
                    row = ConversationRow(content: card, mark: .avatar)
                    replaceRow(id, row)
                    events[id] = nil
                }
            case .event:
                guard let id = item.id else { continue }
                eventIDs.insert(id)
                if let event = events[id], let shown = rows[id] {
                    event.update(item)
                    row = shown
                } else {
                    let event = ConversationEventRow(item)
                    event.compactDate = compactDates
                    events[id] = event
                    row = ConversationRow(content: event, mark: .dot(below: ConversationMetrics.captionMiddle))
                    replaceRow(id, row)
                    if let card = cards.removeValue(forKey: id) {
                        card.setLive(false)
                    }
                }
            }
            row.show(rail, name: item.sender, monochrome: monochrome)
            desired.append(row)
        }
        for (id, card) in cards where !cardIDs.contains(id) {
            card.setLive(false)
            cards[id] = nil
        }
        for id in events.keys where !eventIDs.contains(id) {
            events[id] = nil
        }
        for (id, row) in rows where !cardIDs.contains(id) && !eventIDs.contains(id) {
            detach(row)
            rows[id] = nil
        }
        if !desired.contains(where: { $0 === truncatedItem }) {
            detach(truncatedItem)
        }
        // The stack after the fold strip and the issue box, in the order
        // shown.
        for (i, v) in desired.enumerated() {
            let at = i + Self.headViews
            let arranged = stack.arrangedSubviews
            if at < arranged.count, arranged[at] === v {
                continue
            }
            if v.superview === stack {
                stack.removeArrangedSubview(v)
            }
            stack.insertArrangedSubview(v, at: min(at, stack.arrangedSubviews.count))
        }
        itemViews = desired
        showFoldAll()
    }

    /// The Collapse All / Expand All button for the cards' fold state
    /// (`Conversation.foldAllOffer`); hidden while fewer than two fold.
    private func showFoldAll() {
        foldAll = Conversation.foldAllOffer(foldState)
        let label = foldAll.label
        foldAllButton.title = label
        foldAllButton.setAccessibilityLabel(label)
        foldAllBox.isHidden = foldAll == .none
    }

    /// Collapse All or Expand All: every card of the conversation shown
    /// folds or opens (`Conversation.Folds.setAll`); a card that arrives
    /// later starts as its default. Until the user scrolls the top of the
    /// conversation stays in view; otherwise the item under the viewport's
    /// top keeps its place, its header in view.
    @objc private func foldAllClicked(_ sender: Any?) {
        guard let model = controller.model, foldAll != .none else { return }
        let display = shownOrder(model)
        folds.setAll(display.items, foldAll.folded)
        foldState = folds.state(display.items, opening: display.opening)
        preservingAnchor(at: pinned ?? headerAnchor(currentAnchor())) {
            for (id, card) in cards where card.foldable {
                card.setFolded(foldState[id] ?? false)
            }
        }
        showFoldAll()
    }

    /// `anchor` with the item's top kept in view: a viewport inside the
    /// item (its top above the viewport's) moves to the item's top, so a
    /// card that folds keeps its header where the user can see it.
    private func headerAnchor(_ anchor: Anchor?) -> Anchor? {
        guard var a = anchor else { return nil }
        a.offset = min(a.offset, 0)
        return a
    }

    /// The row of member `id` from now on; one it had before (the member
    /// was a card and is an event now, or the other way round) goes.
    private func replaceRow(_ id: MessageID, _ row: ConversationRow) {
        if let old = rows[id] {
            detach(old)
        }
        rows[id] = row
    }

    /// The timeline again, as the items are (the avatars' setting
    /// changed).
    private func applyRails() {
        guard let model = controller.model else { return }
        let items = shownOrder(model).items
        let monochrome = state.settings.monochromeAvatars
        for (item, rail) in zip(items, ConversationLayout.rails(items)) {
            let row = item.kind == .truncated ? truncatedItem : item.id.flatMap { rows[$0] }
            row?.show(rail, name: item.sender, monochrome: monochrome)
        }
    }

    /// The card of `item`, yet to be folded and rendered (`apply`).
    private func makeCard(_ item: Conversation.Item, _ id: MessageID) -> ConversationCardView {
        let card = ConversationCardView(item)
        card.host = self
        card.compactDate = compactDates
        card.refreshActions()
        cards[id] = card
        return card
    }

    private func detach(_ v: NSView) {
        guard v.superview === stack else { return }
        stack.removeArrangedSubview(v)
        v.removeFromSuperview()
    }

    /// Takes everything out (another conversation, or none).
    private func removeAll() {
        for card in cards.values {
            card.setLive(false)
        }
        for row in rows.values {
            detach(row)
        }
        detach(truncatedItem)
        cards = [:]
        events = [:]
        rows = [:]
        itemViews = []
        folds = Conversation.Folds()
        foldState = [:]
        foldAll = .none
        foldAllBox.isHidden = true
        pinned = nil
        lastAnchor = nil
        issueBox.isHidden = true
        issueCard.show(nil, openable: false)
        hover("")
    }

    /// The issue card of a Jira conversation, once on top (under the fold
    /// strip); its key opens
    /// the issue only on the account's own site, its status pill is the
    /// Change Status menu on an account that changes statuses.
    private func showIssue(_ model: Conversation.Model) {
        guard let card = model.issue, let account = firstMember(model)?.accountId else {
            issueCard.show(nil, openable: false)
            issueBox.isHidden = true
            return
        }
        showIssueCard(card, account: account)
        issueBox.isHidden = false
    }

    private func showIssueCard(_ card: Jira.Card, account: AccountID) {
        let site = controller.issueSite(account)
        let actions = state.hooks.issueActions?()
        issueCard.show(
            card, openable: !card.url.isEmpty && Jira.isIssueURL(card.url, siteURL: site),
            transitions: actions?.canTransition(account) ?? false)
        if let actions {
            issueCard.setBusy(actions.isBusy(account: account, key: card.key), key: card.key)
        }
    }

    private func firstMember(_ model: Conversation.Model) -> MessageSummary? {
        model.items.first { $0.kind != .truncated }?.message
    }

    /// The issue of the conversation on show, for the Change Status menu:
    /// any member names it (the first one here); nil for a mail
    /// conversation or none.
    var transitionSubject: IssueActionsController.Subject? {
        guard let model = controller.model, model.issue != nil else { return nil }
        return issueSubject(of: firstMember(model))
    }

    /// The refreshed issue of a transition (`IssueActionsController.
    /// onIssueChanged`): the card shows it at once when it is the issue
    /// on show; the members follow with the daemon's notifications.
    func applyIssue(_ info: IssueInfo, account: AccountID) {
        guard let model = controller.model, let card = model.issue, let member = firstMember(model),
              member.accountId == account, Jira.clean(info.key) == card.key else { return }
        showIssueCard(Jira.issueCard(info), account: account)
    }

    /// A transition started or ended on the issue `key` of `account`
    /// (`IssueActionsController.onBusy`): the pill's spinner.
    func setIssueBusy(_ busy: Bool, account: AccountID, key: String) {
        guard let model = controller.model, let member = firstMember(model), member.accountId == account else { return }
        issueCard.setBusy(busy, key: key)
    }

    private func openIssueLink(_ url: String) {
        guard let model = controller.model, let account = firstMember(model)?.accountId else { return }
        let window = view.window
        let toasts = state.toasts
        openIssue(url, site: controller.issueSite(account)) { text in
            windowToast(text, in: window, or: toasts)
        }
    }

    // MARK: Scroll position

    /// The top of the conversation (conversation_view.go `topAnchor`): the
    /// issue card of a Jira conversation, else the first item shown (the
    /// card that opened it, or the newest). It is the top of the document,
    /// so the pane's inset above that item stays in view too, and whatever
    /// the model puts on top later is what the pane shows.
    private var topAnchor: Anchor {
        Anchor(view: clamp, offset: 0)
    }

    /// Runs `change` (which alters heights) and brings the item the user
    /// reads back to where it was on screen (the top of the conversation
    /// until the user scrolls). Nested changes are laid out once, by the
    /// outermost.
    private func preservingAnchor(at fixed: Anchor? = nil, _ change: () -> Void) {
        if anchorDepth > 0 {
            change()
            return
        }
        let anchor = fixed ?? pinned ?? currentAnchor()
        anchorDepth += 1
        change()
        anchorDepth -= 1
        view.layoutSubtreeIfNeeded()
        restore(anchor)
        scheduleLiveUpdate()
    }

    /// The first item reaching below the viewport's top, and how far below
    /// its top the viewport's top is.
    private func currentAnchor() -> Anchor? {
        let top = scroll.contentView.bounds.minY
        for v in [foldAllBox, issueBox] + itemViews where !v.isHidden && v.superview != nil {
            let f = frameInDocument(v)
            if f.maxY > top + 0.5 {
                return Anchor(view: v, offset: top - f.minY)
            }
        }
        return nil
    }

    private func restore(_ anchor: Anchor?) {
        guard let anchor, let v = anchor.view, v.superview != nil else { return }
        let clip = scroll.contentView
        let f = frameInDocument(v)
        let top = CGFloat(ConversationLayout.anchoredTop(
            itemTop: Double(f.minY), offset: Double(anchor.offset), documentHeight: Double(clamp.frame.height),
            viewportHeight: Double(clip.bounds.height)))
        guard abs(top - clip.bounds.minY) >= 0.5 else { return }
        clip.scroll(to: NSPoint(x: clip.bounds.minX, y: top))
        scroll.reflectScrolledClipView(clip)
    }

    private func frameInDocument(_ v: NSView) -> NSRect {
        v.convert(v.bounds, to: clamp)
    }

    /// The viewport is at the top of the conversation, where an arrival
    /// shows.
    private func isAtStart() -> Bool {
        scroll.contentView.bounds.minY < 1
    }

    @objc private func clipBoundsChanged(_ note: Notification) {
        scheduleLiveUpdate()
    }

    @objc private func liveScrollStarted(_ note: Notification) {
        pinned = nil
    }

    /// The pane's width changed: the cards reflow, and the item the user
    /// read comes back once they have.
    @objc private func clipFrameChanged(_ note: Notification) {
        let w = scroll.contentView.frame.width
        guard abs(w - lastClipWidth) >= 0.5 else { return }
        lastClipWidth = w
        let anchor = pinned ?? lastAnchor
        let compact = ConversationLayout.compactDates(pane: Double(w))
        if compact != compactDates {
            compactDates = compact
            for card in cards.values {
                card.compactDate = compact
            }
            for event in events.values {
                event.compactDate = compact
            }
        }
        DispatchQueue.main.async { [weak self] in
            guard let self else { return }
            self.view.layoutSubtreeIfNeeded()
            self.restore(anchor)
            self.scheduleLiveUpdate()
        }
    }

    // MARK: Live cards

    private func scheduleLiveUpdate() {
        guard !liveUpdateScheduled else { return }
        liveUpdateScheduled = true
        DispatchQueue.main.async { [weak self] in
            guard let self else { return }
            self.liveUpdateScheduled = false
            self.updateLive()
        }
    }

    /// Asks for the bodies of the cards near the viewport, gives web views
    /// to the nearest HTML cards and takes them from the rest
    /// (`ConversationLayout.live`), and lets go of far entries beyond the
    /// controller's budget. A folded card is none of them: no body, no
    /// view while folded (`ConversationCardView.setFolded`).
    private func updateLive() {
        guard controller.model != nil else { return }
        let clip = scroll.contentView.bounds
        var ordered: [ConversationCardView] = []
        var frames: [ConversationLayout.Span] = []
        var html: [Bool] = []
        for v in itemViews {
            guard let card = (v as? ConversationRow)?.content as? ConversationCardView, !card.folded else { continue }
            let f = frameInDocument(card)
            ordered.append(card)
            frames.append(ConversationLayout.Span(Double(f.minY), Double(f.maxY)))
            html.append(card.isHTML)
        }
        let live = ConversationLayout.live(
            frames: frames, html: html, visible: ConversationLayout.Span(Double(clip.minY), Double(clip.maxY)))
        var near = Set<MessageID>()
        for (i, card) in ordered.enumerated() {
            card.setLive(live.web.contains(i))
            if live.near.contains(i) {
                near.insert(card.id)
                controller.needsBody(card.id, details: card.detailsOpen)
            }
        }
        controller.trim(keeping: near)
        lastAnchor = pinned ?? currentAnchor()
    }

    // MARK: Keyboard

    /// Space (or Shift-Space, `up`) from the list: one page of the
    /// conversation down (up). True: the key was used.
    func page(up: Bool) -> Bool {
        guard controller.model != nil else { return false }
        let clip = scroll.contentView
        let top = CGFloat(ConversationLayout.pageTop(
            from: Double(clip.bounds.minY), up: up, viewportHeight: Double(clip.bounds.height),
            documentHeight: Double(clamp.frame.height), overlap: Double(scroll.verticalPageScroll)))
        pinned = nil
        clip.scroll(to: NSPoint(x: clip.bounds.minX, y: top))
        scroll.reflectScrolledClipView(clip)
        return true
    }

    // MARK: Spinner

    private func startSpinner() {
        stopSpinner()
        let work = DispatchWorkItem { [weak self] in
            guard let self else { return }
            self.spinnerWork = nil
            self.loadingSpinner.start()
        }
        spinnerWork = work
        DispatchQueue.main.asyncAfter(deadline: .now() + MessageViewController.spinnerDelay, execute: work)
    }

    private func stopSpinner() {
        spinnerWork?.cancel()
        spinnerWork = nil
        loadingSpinner.stop()
    }

    // MARK: Settings

    private func applyBodyFont() {
        let font = bodyFont
        let zoom = textZoom
        preservingAnchor {
            for card in cards.values {
                card.applyFont(font, zoom: zoom)
            }
        }
    }

    // MARK: ConversationCardHost

    var textZoom: Int { state.settings.textZoom }

    var bodyFont: NSFont {
        Typo.body(zoom: state.settings.textZoom, monospace: state.settings.monospacePlainText)
    }

    var assistant: AssistantController { state.assistant }

    func actions(for s: MessageSummary) -> Capabilities.Actions {
        controller.actions(for: s)
    }

    func held(_ id: MessageID) -> LoadedMessage? {
        controller.loaded[id]
    }

    func cardNeedsDetails(_ card: ConversationCardView) {
        controller.needsBody(card.id, details: true)
    }

    /// conversation_view.go `setCardFolded`. The card changes its height
    /// through `cardHeightChanging`, which keeps what the user reads in
    /// place and has the live cards looked at again: an opened card is
    /// asked for its body. Until the user scrolls the top of the
    /// conversation stays in view; otherwise the card keeps its header
    /// where it was (in view). The Collapse All / Expand All button
    /// follows.
    func cardSetFolded(_ card: ConversationCardView, _ folded: Bool) {
        folds.set(card.id, folded)
        foldState[card.id] = folded
        let own = rows[card.id].map { row in
            Anchor(view: row, offset: scroll.contentView.bounds.minY - frameInDocument(row).minY)
        }
        preservingAnchor(at: pinned ?? headerAnchor(own)) {
            card.setFolded(folded)
        }
        showFoldAll()
    }

    /// The card's "•••": the controller switches its body to the variant
    /// asked for (`ConversationController.setQuoted`), held or fetched, and
    /// the card shows it when it arrives (`showLoaded`, `onLoaded`).
    func cardSetQuoted(_ card: ConversationCardView, _ on: Bool) {
        controller.setQuoted(card.id, on, details: card.detailsOpen)
    }

    func cardHeightChanging(_ change: () -> Void) {
        preservingAnchor(change)
    }

    func hover(_ href: String) {
        let text = String(href.prefix(MessageWebView.statusMaxChars))
        statusLabel.stringValue = text
        statusBox.isHidden = text.isEmpty
    }

    func openEmbedded(_ containing: MessageSummary, _ attachment: Attachment, _ remote: Bool, _ chip: NSView?) {
        onOpenEmbedded?(containing, attachment, remote, chip)
    }

    func toast(_ text: String) {
        windowToast(text, in: view.window, or: state.toasts)
    }

    // MARK: MessageDisplay

    func displays(_ id: MessageID) -> Bool {
        cards[id] != nil
    }

    func showLoaded(_ id: MessageID, _ lm: LoadedMessage) {
        controller.adopt(id, lm)
        cards[id]?.render(lm)
    }

    func refreshRemoteBar(_ id: MessageID, _ lm: LoadedMessage) {
        cards[id]?.renderBars(lm)
    }

    func refreshBulk(_ id: MessageID, _ lm: LoadedMessage) {
        cards[id]?.renderBulk(lm)
    }

    func refreshChips(_ id: MessageID, _ lm: LoadedMessage?) {
        cards[id]?.renderChips(lm ?? controller.loaded[id])
    }

    func showOutboxState(_ id: MessageID, _ m: Message?) {
        cards[id]?.renderOutbox(m)
    }
}

/// The conversation's scroll view: tells when the user scrolls it with the
/// wheel or the trackpad (a card's web view hands its wheel events on to
/// it while its document fits).
@MainActor
private final class ConversationScrollView: NSScrollView {
    var onUserScroll: (@MainActor () -> Void)?

    override func scrollWheel(with event: NSEvent) {
        onUserScroll?()
        super.scrollWheel(with: event)
    }
}
