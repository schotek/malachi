// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The chat layout of the conversation in the reading pane
/// (docs/plans/chat-view.md, `Conversation.chatRows`): the conversation as a
/// messaging app shows it, oldest first from the top, open at the end. The
/// others' messages are bubbles on the left, the user's own (and the
/// user's replies in Sent, whichever identity sent them) on the right, the
/// drafts last on the right; an issue's events and the row of older
/// members left out are centred rows; a Jira conversation has its issue
/// card on top across the column. The column is the timeline's (no wider
/// than 900 points, the same insets), without its gutter and line: the
/// avatars stand beside the bubbles (`ConversationChatRow`).
///
/// A bubble is plain text, never a web view (`ConversationBubbleView`): the
/// message's text as the daemon derived it, without its quoted history
/// (`Conversation.bubbleBody`), the summary's snippet dimmed until the body
/// is there. The bodies are asked for only for the bubbles near the
/// viewport (`ConversationLayout.live` over the rows' frames, two heights
/// of the view), from the same controller and cache of bodies as the
/// timeline, so switching layouts asks thread.get nothing. A double click
/// anywhere in a bubble opens the message in its own window, a draft in the
/// compose window (`MessageActionDelegate`); nothing else is interactive.
///
/// The chat opens at its end. While the viewport shows the end (and until
/// the user scrolls away from it), the end stays in view as bodies arrive
/// and members come; otherwise the row under the viewport's top keeps its
/// place. Space and Shift-Space from the list page through it (`page(up:)`,
/// through the host). The host is the controller's only subscriber
/// (ConversationHostViewController): it hands the changes on (`changed`,
/// `loaded`) and trims the controller's bodies with the near set this view
/// reports (`onNear`) only while it is the layout shown.
@MainActor
final class ConversationChatViewController: NSViewController {
    let state: AppState
    let controller: ConversationController

    /// The actions of the messages: opening one, editing a draft (set by
    /// the host from `MessageWindows`).
    weak var delegate: (any MessageActionDelegate)?

    /// The members whose bodies the bubbles near the viewport keep.
    var onNear: (@MainActor (Set<MessageID>) -> Void)?

    /// Keeps a row where it is on screen: `offset` is how far the
    /// viewport's top was below the row's top.
    private struct Anchor {
        weak var view: NSView?
        var offset: CGFloat
    }

    private let scroll = ConversationScrollView()
    private let clamp: ClampView
    private let stack = FillStackView()
    private let issueBox: ConversationIssueBox
    private let truncatedRow = ConversationChatCenterRow()
    private var bubbles: [MessageID: ConversationChatRow] = [:]
    private var events: [MessageID: ConversationChatCenterRow] = [:]
    private var draftRows: [DraftID: ConversationChatRow] = [:]
    /// The rows in the order shown, under the issue card.
    private var rowViews: [NSView] = []
    private var compactDates = false
    private let loadingSpinner = Spinner(size: 32)
    private var spinnerWork: DispatchWorkItem?
    private var settingsTokens: [Settings.ChangeToken] = []

    /// This layout is the one the host shows.
    private(set) var shown = false
    /// The end of the chat stays in view until the user scrolls.
    private var pinnedEnd = false
    /// Where the user was, for a change of the pane's width.
    private var lastAnchor: Anchor?
    private var lastAtEnd = false
    /// The viewport's size as last seen (`clipFrameChanged`).
    private var lastClipSize = NSSize.zero
    /// A change of the viewport's size waits for its layout: the near
    /// update meanwhile keeps the position it is to restore.
    private var resizing = false
    private var anchorDepth = 0
    private var nearScheduled = false

    /// The views of the stack before the rows: the issue card.
    private static let headViews = 1

    init(state: AppState, controller: ConversationController) {
        self.state = state
        self.controller = controller
        let widest = CGFloat(ConversationLayout.Metrics.maxWidth)
        clamp = ClampView(maximum: widest, tight: widest)
        issueBox = ConversationIssueBox(state: state, controller: controller, leading: 0)
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    // MARK: View

    override func loadView() {
        stack.spacing = 0
        stack.edgeInsets = NSEdgeInsets(
            top: Self.topInset, left: ConversationMetrics.sideInset, bottom: ConversationMetrics.sideInset,
            right: ConversationMetrics.sideInset)
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
        scroll.onUserScroll = { [weak self] in self?.pinnedEnd = false }
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

        loadingSpinner.setAccessibilityLabel(L10n.C("accessibility", "Loading the message"))

        let container = ConversationSurfaceView()
        container.translatesAutoresizingMaskIntoConstraints = false
        container.addSubview(scroll)
        container.addSubview(loadingSpinner)
        NSLayoutConstraint.activate([
            scroll.topAnchor.constraint(equalTo: container.topAnchor),
            scroll.bottomAnchor.constraint(equalTo: container.bottomAnchor),
            scroll.leadingAnchor.constraint(equalTo: container.leadingAnchor),
            scroll.trailingAnchor.constraint(equalTo: container.trailingAnchor),
            loadingSpinner.centerXAnchor.constraint(equalTo: container.centerXAnchor),
            loadingSpinner.centerYAnchor.constraint(equalTo: container.centerYAnchor),
        ])
        view = container

        let settings = state.settings
        settingsTokens.append(settings.onChange(.textZoom) { [weak self] in self?.applyBodyFont() })
        settingsTokens.append(settings.onChange(.monospacePlainText) { [weak self] in self?.applyBodyFont() })
        settingsTokens.append(settings.onChange(.monochromeAvatars) { [weak self] in self?.applyAvatars() })
    }

    /// Above the first row's own gap.
    private static let topInset: CGFloat = 4

    // MARK: Model

    /// A change of the controller, from the host. While hidden the rows
    /// are only taken out when the conversation goes; `setShown(true)`
    /// builds the rest.
    func changed(_ change: ConversationController.Change) {
        _ = view
        switch change {
        case .loading:
            removeAll()
            startSpinner()
        case .opened:
            stopSpinner()
            if shown {
                open()
            } else {
                removeAll()
            }
        case .updated:
            stopSpinner()
            if shown {
                update()
            }
        case .cleared:
            stopSpinner()
            removeAll()
        }
    }

    /// A bubble's entry has news (`ConversationController.onLoaded`, from
    /// the host while this layout is shown): its text and its paperclip.
    func loaded(_ id: MessageID, _ lm: LoadedMessage) {
        guard let row = bubbles[id] else { return }
        preserving {
            render(row, lm)
        }
    }

    /// The host shows this layout (`on`) or the timeline. Shown, the chat
    /// catches up with the controller (the rows reconciled with the model,
    /// every bubble rendered from the bodies held) and opens at its end.
    func setShown(_ on: Bool) {
        guard on != shown else { return }
        _ = view
        shown = on
        if on {
            open()
        }
    }

    /// The conversation shown anew (a new one, or this layout shown): the
    /// rows as the model has them, the viewport at the end, the bubbles
    /// near it asked for their bodies.
    private func open() {
        guard let model = controller.model else {
            removeAll()
            return
        }
        compactDates = chatCompactDates(scroll.contentView.frame.width)
        anchorDepth += 1
        apply(model)
        for (id, row) in bubbles {
            render(row, controller.loaded[id])
        }
        anchorDepth -= 1
        view.layoutSubtreeIfNeeded()
        pinnedEnd = true
        scrollToEnd()
        updateNear()
    }

    /// The shown conversation changed: the rows are reconciled by id, and
    /// the end stays in view when it was in view, else the row the user
    /// reads keeps its place.
    private func update() {
        guard let model = controller.model else { return }
        preserving {
            apply(model)
        }
    }

    /// Puts the rows of `model` (`Conversation.chatRows`) into the stack,
    /// reusing the views of the members, events and drafts shown already.
    private func apply(_ model: Conversation.Model) {
        issueBox.show(model)
        let rows = Conversation.chatRows(model, drafts: controller.drafts)
        let monochrome = state.settings.monochromeAvatars
        let column = columnWidth
        var desired: [NSView] = []
        var bubbleIDs = Set<MessageID>()
        var eventIDs = Set<MessageID>()
        var draftIDs = Set<DraftID>()
        for row in rows {
            switch row.kind {
            case .truncated:
                truncatedRow.showText(row.item?.text ?? "")
                truncatedRow.setColumn(column)
                desired.append(truncatedRow)
            case .bubble:
                guard let item = row.item, let id = item.id else { continue }
                bubbleIDs.insert(id)
                let right = row.side == .right
                let view: ConversationChatRow
                if let shown = bubbles[id], shown.right == right {
                    view = shown
                } else {
                    if let old = bubbles[id] {
                        detach(old)
                    }
                    view = makeRow(id: id, draft: nil, right: right)
                    view.bubble.onDoubleClick = { [weak self, weak view] in
                        guard let self, let id = view?.id, let s = self.controller.member(id) else { return }
                        self.delegate?.openMessage(s)
                    }
                    bubbles[id] = view
                }
                view.bubble.compactDate = compactDates
                view.bubble.showMessage(item, attachments: attachmentCount(id))
                if !view.bubble.showsText {
                    let lm = controller.loaded[id]
                    view.bubble.showBody(
                        Conversation.bubbleBody(lm, snippet: item.message?.snippet ?? ""), monospace: monospace(lm))
                }
                view.showAvatar(name: item.sender, monochrome: monochrome)
                view.setColumn(column)
                desired.append(view)
            case .event:
                guard let item = row.item, let id = item.id else { continue }
                eventIDs.insert(id)
                let view = events[id] ?? ConversationChatCenterRow()
                events[id] = view
                view.compactDate = compactDates
                view.showEvent(item)
                view.setColumn(column)
                desired.append(view)
            case .draft:
                guard let card = row.draft, let id = card.draft.id else { continue }
                draftIDs.insert(id)
                let view: ConversationChatRow
                if let shown = draftRows[id] {
                    view = shown
                } else {
                    view = makeRow(id: nil, draft: id, right: true)
                    draftRows[id] = view
                }
                view.bubble.onDoubleClick = { [weak self] in self?.delegate?.editThreadDraft(card.draft) }
                view.bubble.compactDate = compactDates
                view.bubble.draftMonospace = state.settings.monospacePlainText
                view.bubble.showDraft(card)
                view.showAvatar(name: card.sender, monochrome: monochrome)
                view.setColumn(column)
                desired.append(view)
            }
        }
        for (id, view) in bubbles where !bubbleIDs.contains(id) {
            detach(view)
            bubbles[id] = nil
        }
        for (id, view) in events where !eventIDs.contains(id) {
            detach(view)
            events[id] = nil
        }
        for (id, view) in draftRows where !draftIDs.contains(id) {
            detach(view)
            draftRows[id] = nil
        }
        if !desired.contains(where: { $0 === truncatedRow }) {
            detach(truncatedRow)
        }
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
        rowViews = desired
    }

    private func makeRow(id: MessageID?, draft: DraftID?, right: Bool) -> ConversationChatRow {
        let row = ConversationChatRow(id: id, draftID: draft, right: right, fonts: fonts)
        row.bubble.compactDate = compactDates
        return row
    }

    /// The bubble of a member shows what its entry `lm` holds: the text
    /// (`Conversation.bubbleBody`) and how many attachments it has.
    private func render(_ row: ConversationChatRow, _ lm: LoadedMessage?) {
        guard let id = row.id, let model = controller.model else { return }
        let i = model.index(id)
        guard i >= 0 else { return }
        let item = model.items[i]
        row.bubble.showMessage(item, attachments: attachmentCount(id))
        row.bubble.showBody(Conversation.bubbleBody(lm, snippet: item.message?.snippet ?? ""), monospace: monospace(lm))
    }

    /// How many attachments member `id` shows as chips, once message.get
    /// told it (the timeline's chips, `ChipPlan`); nil before.
    private func attachmentCount(_ id: MessageID) -> Int? {
        guard let lm = controller.loaded[id], let m = lm.msg else { return nil }
        return ChipPlan(m, lm.body).chips.count
    }

    private func detach(_ v: NSView) {
        guard v.superview === stack else { return }
        stack.removeArrangedSubview(v)
        v.removeFromSuperview()
    }

    /// Takes everything out (another conversation, or none).
    private func removeAll() {
        for v in rowViews {
            detach(v)
        }
        for v in Array(bubbles.values) as [NSView] + Array(events.values) + Array(draftRows.values) {
            detach(v)
        }
        detach(truncatedRow)
        bubbles = [:]
        events = [:]
        draftRows = [:]
        rowViews = []
        pinnedEnd = false
        lastAnchor = nil
        lastAtEnd = false
        issueBox.show(nil)
    }

    /// The refreshed issue of a transition (through the host).
    func applyIssue(_ info: IssueInfo, account: AccountID) {
        issueBox.applyIssue(info, account: account)
    }

    /// A transition started or ended on the issue (through the host).
    func setIssueBusy(_ busy: Bool, account: AccountID, key: String) {
        issueBox.setBusy(busy, account: account, key: key)
    }

    /// Whether member `id` has a bubble here.
    func displays(_ id: MessageID) -> Bool {
        bubbles[id] != nil
    }

    // MARK: Scroll position

    /// The column's inner width (`ConversationLayout.chatColumn`): the
    /// clamp's child less its side insets.
    private var columnWidth: CGFloat {
        let width = clamp.frame.width > 0 ? clamp.frame.width : scroll.contentView.frame.width
        return CGFloat(ConversationLayout.chatColumn(pane: Double(width)))
    }

    /// Runs `change` (which alters heights) and keeps the end in view when
    /// it was (or until the user scrolls), else the row the user reads in
    /// its place. Nested changes are laid out once, by the outermost.
    private func preserving(_ change: () -> Void) {
        if anchorDepth > 0 {
            change()
            return
        }
        let atEnd = pinnedEnd || isAtEnd()
        let anchor = atEnd ? nil : currentAnchor()
        anchorDepth += 1
        change()
        anchorDepth -= 1
        view.layoutSubtreeIfNeeded()
        if atEnd {
            scrollToEnd()
        } else {
            restore(anchor)
        }
        scheduleNear()
    }

    private func isAtEnd() -> Bool {
        let clip = scroll.contentView.bounds
        return ConversationLayout.chatAtEnd(
            top: Double(clip.minY), viewportHeight: Double(clip.height), documentHeight: Double(clamp.frame.height))
    }

    private func scrollToEnd() {
        let clip = scroll.contentView
        let top = CGFloat(ConversationLayout.chatEndTop(
            viewportHeight: Double(clip.bounds.height), documentHeight: Double(clamp.frame.height)))
        guard abs(top - clip.bounds.minY) >= 0.5 else { return }
        clip.scroll(to: NSPoint(x: clip.bounds.minX, y: top))
        scroll.reflectScrolledClipView(clip)
    }

    /// The first row reaching below the viewport's top, and how far below
    /// its top the viewport's top is.
    private func currentAnchor() -> Anchor? {
        let top = scroll.contentView.bounds.minY
        for v in [issueBox as NSView] + rowViews where !v.isHidden && v.superview != nil {
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

    @objc private func clipBoundsChanged(_ note: Notification) {
        scheduleNear()
    }

    @objc private func liveScrollStarted(_ note: Notification) {
        pinnedEnd = false
    }

    /// Whether the bubbles of a pane `width` wide show the short date
    /// (`ConversationLayout.chatCompactDates` of its column).
    private func chatCompactDates(_ width: CGFloat) -> Bool {
        ConversationLayout.chatCompactDates(column: ConversationLayout.chatColumn(pane: Double(width)))
    }

    /// The pane's size changed (`ConversationLayout.chatResize`): a chat
    /// that showed its end (or had it pinned) before shows it after,
    /// whether the pane got narrower, wider, shorter or taller; otherwise
    /// the row the user read comes back once the bubbles reflowed. Only a
    /// change of the width reflows and may switch the short dates. The
    /// position is the one measured before the change (`lastAtEnd`,
    /// `lastAnchor`), not what the clipped viewport says after it.
    @objc private func clipFrameChanged(_ note: Notification) {
        let size = scroll.contentView.frame.size
        let old = lastClipSize
        guard let resize = ConversationLayout.chatResize(
            from: (Double(old.width), Double(old.height)), to: (Double(size.width), Double(size.height)),
            pinned: pinnedEnd, wasAtEnd: lastAtEnd)
        else { return }
        lastClipSize = size
        let atEnd = resize.toEnd
        let anchor = lastAnchor
        resizing = true
        let compact = chatCompactDates(size.width)
        if resize.width, compact != compactDates {
            compactDates = compact
            for row in Array(bubbles.values) + Array(draftRows.values) {
                row.bubble.compactDate = compact
            }
            for event in events.values {
                event.compactDate = compact
            }
        }
        DispatchQueue.main.async { [weak self] in
            guard let self else { return }
            self.resizing = false
            guard self.shown else { return }
            self.view.layoutSubtreeIfNeeded()
            if atEnd {
                self.scrollToEnd()
            } else {
                self.restore(anchor)
            }
            self.scheduleNear()
        }
    }

    // MARK: Bodies

    private func scheduleNear() {
        guard !nearScheduled else { return }
        nearScheduled = true
        DispatchQueue.main.async { [weak self] in
            guard let self else { return }
            self.nearScheduled = false
            self.updateNear()
        }
    }

    /// Asks for the bodies of the bubbles near the viewport (within two of
    /// its heights, `ConversationLayout.live`) and tells the host which
    /// they are. Only while this layout is shown.
    private func updateNear() {
        guard shown, controller.model != nil else { return }
        let clip = scroll.contentView.bounds
        var ids: [MessageID] = []
        var frames: [ConversationLayout.Span] = []
        for v in rowViews {
            guard let row = v as? ConversationChatRow, let id = row.id else { continue }
            let f = frameInDocument(row)
            ids.append(id)
            frames.append(ConversationLayout.Span(Double(f.minY), Double(f.maxY)))
        }
        let live = ConversationLayout.live(
            frames: frames, html: [], visible: ConversationLayout.Span(Double(clip.minY), Double(clip.maxY)))
        var near = Set<MessageID>()
        for i in live.near.sorted() {
            near.insert(ids[i])
            controller.needsBody(ids[i], details: false)
        }
        onNear?(near)
        guard !resizing else { return }
        lastAtEnd = isAtEnd()
        lastAnchor = currentAnchor()
    }

    // MARK: Keyboard

    /// Space (or Shift-Space, `up`) from the list: one page of the chat
    /// down (up). True: the key was used.
    func page(up: Bool) -> Bool {
        guard controller.model != nil else { return false }
        let clip = scroll.contentView
        let top = CGFloat(ConversationLayout.pageTop(
            from: Double(clip.bounds.minY), up: up, viewportHeight: Double(clip.bounds.height),
            documentHeight: Double(clamp.frame.height), overlap: Double(scroll.verticalPageScroll)))
        pinnedEnd = false
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

    /// The bubbles' fonts at the text zoom: the normal one and the
    /// monospaced one for plain-text bodies.
    private var fonts: (normal: NSFont, monospace: NSFont) {
        let zoom = state.settings.textZoom
        return (Typo.body(zoom: zoom, monospace: false), Typo.body(zoom: zoom, monospace: true))
    }

    /// Whether a bubble whose entry is `lm` shows its text monospaced
    /// (`Conversation.bubbleMonospace`: the setting, a plain-text body).
    private func monospace(_ lm: LoadedMessage?) -> Bool {
        Conversation.bubbleMonospace(lm, setting: state.settings.monospacePlainText)
    }

    /// The text zoom or the monospace setting changed: every bubble's
    /// fonts, and the font each text shows in (a bubble whose entry was
    /// let go keeps its choice while the setting is on).
    private func applyBodyFont() {
        let f = fonts
        let setting = state.settings.monospacePlainText
        preserving {
            for row in bubbles.values {
                row.bubble.setFonts(normal: f.normal, monospace: f.monospace)
                let lm = row.id.flatMap { controller.loaded[$0] }
                row.bubble.setMonospace(lm.map { monospace($0) } ?? (setting && row.bubble.isMonospace))
            }
            for row in draftRows.values {
                row.bubble.setFonts(normal: f.normal, monospace: f.monospace)
                row.bubble.draftMonospace = setting
                row.bubble.setMonospace(setting)
            }
        }
    }

    private func applyAvatars() {
        guard let model = controller.model else { return }
        let monochrome = state.settings.monochromeAvatars
        for (id, row) in bubbles {
            let i = model.index(id)
            if i >= 0 {
                row.showAvatar(name: model.items[i].sender, monochrome: monochrome)
            }
        }
        for card in controller.drafts {
            if let id = card.draft.id {
                draftRows[id]?.showAvatar(name: card.sender, monochrome: monochrome)
            }
        }
    }
}
