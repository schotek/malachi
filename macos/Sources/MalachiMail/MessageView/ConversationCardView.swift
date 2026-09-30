// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// What a card needs from the conversation view it sits in.
@MainActor
protocol ConversationCardHost: AnyObject {
    var delegate: (any MessageActionDelegate)? { get }
    var cache: MessageCache { get }
    /// The text-zoom setting, for a web view made now.
    var textZoom: Int { get }
    /// The plain-text body's font (text zoom, monospace).
    var bodyFont: NSFont { get }
    /// The buttons the card of `s` offers (`Conversation.cardActions`).
    func actions(for s: MessageSummary) -> Capabilities.Actions
    /// The recipients' disclosure opened: the full message (Cc) is asked
    /// for.
    func cardNeedsDetails(_ card: ConversationCardView)
    /// Runs `change`, which alters the card's height, keeping what the user
    /// reads in place.
    func cardHeightChanging(_ change: () -> Void)
    /// The link under the pointer in a card's web view ("" when none).
    func hover(_ href: String)
    /// A chip's View: the attached message in its own window.
    func openEmbedded(_ containing: MessageSummary, _ attachment: Attachment, _ remote: Bool, _ chip: NSView?)
    /// A transient message in the view's window.
    func toast(_ text: String)
}

/// One message of the conversation view (`Conversation.ItemKind.message`),
/// a card: the background of text with a hairline border and rounded
/// corners on the pane's grey, no shadow. In it a header
/// (`ConversationCardHeader`: an unread dot, the sender, a disclosure for
/// the recipients, the Jira badges via, Internal and Edited, the date, and
/// on hover Reply (Comment on an issue), Reply All and Forward as the
/// account allows them), the recipients, the attachment chips, the delivery
/// state of a queued message, the remote-image and pictures bars, and the
/// body: plain text in a text view, HTML in a web view of its own in the
/// sized mode (never one document for the conversation: a message's CSS
/// must not reach another's headers). Headers are plain text
/// (`stringValue`); the body's HTML is the sanitiser's output only
/// (`MessageWebView`). The sender's avatar is not the card's: it sits on
/// the timeline beside it (`ConversationRow`).
///
/// The native parts keep the card's padding. An HTML body does not: its
/// document is the reader's in its compact form (`viewerDocument(body:
/// compact:)`), a white page whatever the appearance with the card's
/// padding at the sides and a little above and below, so it spans the card
/// from edge to edge under the header and the card's corners clip it.
///
/// A card is cheap: the bars, the chips, the recipients and the text view
/// are made when first needed, and the web view exists only while the pane
/// keeps the card live (`setLive`, near the viewport); otherwise the body
/// keeps the height it last had. The card is an accessibility group named
/// by its sender and date.
@MainActor
final class ConversationCardView: NSView {
    let id: MessageID
    private(set) var item: Conversation.Item
    private(set) var summary: MessageSummary
    weak var host: (any ConversationCardHost)?

    /// The body is HTML (known once the body arrived): the card wants a web
    /// view while it is live.
    private(set) var isHTML = false
    /// The card holds a web view (set by the pane's live window).
    private(set) var live = false
    /// The recipients' disclosure is open.
    private(set) var detailsOpen = false

    /// The short date of the list instead of the full date and time (a
    /// narrow pane, `ConversationLayout.compactDates`).
    var compactDate = false {
        didSet {
            if compactDate != oldValue {
                renderDate()
            }
        }
    }

    // Header.
    private let header = ConversationCardHeader()
    private let top = FillStackView()
    private let root = FillStackView()
    private var hovering = false

    // Made when first needed, in the order of `TopSlot` and `Slot`.
    private var details: AddressHeaderView?
    private var chips: FlowView?
    private var hintLabel: NSTextField?
    private var banner: BannerView?
    private var remoteBar: RemoteBarView?
    private var picturesBar: RemoteBarView?
    private var textView: MessageBodyTextView?
    private var chipViews: [NSView] = []

    /// The body area for HTML and for the wait: the web view while live,
    /// else blank at `bodyHeight`.
    private let bodyHost = CardBodyHost()
    private let loadingLabel = NSTextField(labelWithString: "")
    private var bodyHeight: NSLayoutConstraint!
    private var webView: MessageWebView?
    /// The height the HTML body last had, kept while no web view is live.
    private var webHeight = ConversationLayout.initialWebHeight

    /// What the body shows, so a render with the same body leaves it alone.
    private var renderedBody: MessageBodyResult?
    private var renderedErr = false
    private var html: String?
    private var htmlReload = false
    private var links: [Link] = []

    private enum TopSlot: Int, CaseIterable {
        case header, details, chips, hint
    }

    private enum Slot: Int, CaseIterable {
        case top, banner, remote, pictures, text, body
    }

    /// The hairline around the card. What the card holds keeps inside it:
    /// the white page of an HTML body would hide it otherwise, and look a
    /// hair wider than the header above it in the dark appearance.
    private static let border: CGFloat = 1
    /// The padding inside the card, from the hairline on.
    private static let paddingH = ConversationMetrics.cardPaddingH - border
    private static let paddingV = ConversationMetrics.cardPaddingV - border
    /// Under the header's parts, above the bars and the body.
    private static let headerBottom: CGFloat = 8
    /// Above and below the plain text, inside the text view.
    private static let textInset: CGFloat = 2

    private var topSlots: [TopSlot: NSView] = [:]
    private var slots: [Slot: NSView] = [:]

    init(_ item: Conversation.Item) {
        id = item.id ?? MessageID("")
        self.item = item
        summary = item.message ?? MessageSummary(
            id: MessageID(""), accountId: AccountID(""), folderId: FolderID(""), from: [], subject: "", date: .goZero,
            snippet: "", flags: [], hasAttachments: false, size: 0)
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        build()
        update(item)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    // MARK: Building

    private func build() {
        wantsLayer = true
        layer?.cornerRadius = ConversationMetrics.cardRadius
        layer?.cornerCurve = .continuous
        layer?.borderWidth = Self.border
        // The corners clip the body (an HTML body reaches the edges).
        clipsToBounds = true
        layer?.masksToBounds = true

        header.disclosure.target = self
        header.disclosure.action = #selector(toggleDetails(_:))
        for (b, image, action) in [
            (header.replyButton, Icon.reply, #selector(replyClicked(_:))),
            (header.replyAllButton, Icon.replyAll, #selector(replyAllClicked(_:))),
            (header.forwardButton, Icon.forward, #selector(forwardClicked(_:))),
        ] {
            b.image = image
            b.target = self
            b.action = action
            b.onFocus = { [weak self] focused in
                if focused {
                    self?.showButtons()
                } else {
                    self?.hideButtonsUnlessHovering()
                }
            }
        }
        header.replyAllButton.toolTip = L10n.T("Reply All")
        header.replyAllButton.setAccessibilityLabel(L10n.T("Reply All"))
        header.forwardButton.toolTip = L10n.T("Forward")
        header.forwardButton.setAccessibilityLabel(L10n.T("Forward"))

        top.spacing = 8
        top.edgeInsets = NSEdgeInsets(
            top: Self.paddingV, left: Self.paddingH, bottom: Self.headerBottom, right: Self.paddingH)
        install(header, .header)

        loadingLabel.stringValue = L10n.T("Loading…")
        loadingLabel.font = Typo.caption
        loadingLabel.textColor = Tint.secondary
        loadingLabel.isSelectable = false
        loadingLabel.translatesAutoresizingMaskIntoConstraints = false
        bodyHost.addSubview(loadingLabel)
        bodyHeight = bodyHost.heightAnchor.constraint(equalToConstant: ConversationLayout.estimatedBodyHeight)
        NSLayoutConstraint.activate([
            bodyHeight,
            loadingLabel.topAnchor.constraint(equalTo: bodyHost.topAnchor, constant: Self.textInset),
            loadingLabel.leadingAnchor.constraint(equalTo: bodyHost.leadingAnchor, constant: Self.paddingH),
        ])

        root.spacing = 0
        root.edgeInsets = NSEdgeInsets(top: Self.border, left: Self.border, bottom: Self.border, right: Self.border)
        root.translatesAutoresizingMaskIntoConstraints = false
        install(top, .top)
        install(bodyHost, .body)
        addSubview(root)
        NSLayoutConstraint.activate([
            root.topAnchor.constraint(equalTo: topAnchor),
            root.bottomAnchor.constraint(equalTo: bottomAnchor),
            root.leadingAnchor.constraint(equalTo: leadingAnchor),
            root.trailingAnchor.constraint(equalTo: trailingAnchor),
        ])

        setAccessibilityElement(true)
        setAccessibilityRole(.group)
    }

    override var wantsUpdateLayer: Bool { true }

    override func updateLayer() {
        layer?.backgroundColor = ConversationTint.cardFill.cgColor
        layer?.borderColor = ConversationTint.cardBorder(in: self).cgColor
    }

    /// Puts `v` into the header stack at its place.
    private func install(_ v: NSView, _ slot: TopSlot) {
        let at = topSlots.keys.filter { $0.rawValue < slot.rawValue }.count
        top.insertArrangedSubview(v, at: at)
        topSlots[slot] = v
    }

    /// Puts `v` into the card's stack at its place.
    private func install(_ v: NSView, _ slot: Slot) {
        let at = slots.keys.filter { $0.rawValue < slot.rawValue }.count
        root.insertArrangedSubview(v, at: at)
        slots[slot] = v
    }

    private func uninstall(_ slot: Slot) {
        guard let v = slots.removeValue(forKey: slot) else { return }
        root.removeArrangedSubview(v)
        v.removeFromSuperview()
    }

    // MARK: Header

    /// Shows `item` (the same member, as the model has it now: flags, the
    /// delivery state, the issue's badges).
    func update(_ item: Conversation.Item) {
        self.item = item
        if let s = item.message {
            summary = s
        }
        header.senderLabel.stringValue = item.sender
        header.senderLabel.toolTip = item.sender.isEmpty ? nil : item.sender
        header.disclosure.setAccessibilityLabel(item.sender)
        header.unread.isHidden = !item.unread
        header.internalPill.stringValue = item.internalLabel
        header.internalPill.isHidden = !item.internal
        header.viaLabel.stringValue = item.via
        header.viaLabel.toolTip = item.via.isEmpty ? nil : item.via
        header.viaLabel.isHidden = item.via.isEmpty
        header.editedLabel.stringValue = item.edited
        header.editedLabel.isHidden = item.edited.isEmpty
        renderDate()
        updateButtons()
    }

    /// The date at the header's end: in full, or the list's short form in
    /// a narrow pane, with the full one in the tooltip. Accessibility hears
    /// the full one with the sender.
    private func renderDate() {
        let full = summary.date.isGoZero ? "" : formatDateTime(summary.date)
        header.dateLabel.stringValue = compactDate && !full.isEmpty ? formatDate(summary.date, now: Date()) : full
        header.dateLabel.toolTip = compactDate && !full.isEmpty ? full : nil
        header.dateLabel.isHidden = full.isEmpty
        setAccessibilityLabel([item.sender, full].filter { !$0.isEmpty }.joined(separator: ", "))
        header.contentChanged()
    }

    /// The hover buttons the account allows for this member; Reply is
    /// Comment on an issue (`Jira.replyLabel`).
    private func updateButtons() {
        defer { header.contentChanged() }
        guard let a = host?.actions(for: summary) else {
            for b in [header.replyButton, header.replyAllButton, header.forwardButton] {
                b.isHidden = true
            }
            return
        }
        let reply = Jira.replyLabel(comment: a.comment)
        header.replyButton.toolTip = reply
        header.replyButton.setAccessibilityLabel(reply)
        header.replyButton.image = a.comment ? Icon.symbol("text.bubble", size: .toolbar) : Icon.reply
        header.replyButton.isHidden = !a.reply
        header.replyAllButton.isHidden = !a.replyAll
        header.forwardButton.isHidden = !a.forward
    }

    /// Re-reads the buttons (the host was set, or the accounts changed).
    func refreshActions() {
        updateButtons()
    }

    @objc private func toggleDetails(_ sender: Any?) {
        detailsOpen = header.disclosure.state == .on
        if detailsOpen {
            let d = details ?? makeDetails()
            d.isHidden = false
            renderDetails(host?.cache.loaded(id)?.msg)
            host?.cardNeedsDetails(self)
        } else {
            details?.isHidden = true
        }
    }

    private func makeDetails() -> AddressHeaderView {
        let d = AddressHeaderView()
        d.onCopy = { [weak self] address in self?.copyAddress(address) }
        d.onWrite = { [weak self] address, account in
            self?.host?.delegate?.newMessage(to: address, account: account)
        }
        details = d
        install(d, .details)
        return d
    }

    /// The From, To and Cc rows while the disclosure is open: the summary's,
    /// then the full message's once message.get answered.
    private func renderDetails(_ m: Message?) {
        guard detailsOpen, let d = details else { return }
        d.show(
            id, account: summary.accountId, from: m?.summary.from ?? summary.from, to: m?.summary.to ?? summary.to,
            cc: m?.cc)
    }

    private func copyAddress(_ a: Address) {
        let addr = a.address.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !addr.isEmpty else { return }
        let pasteboard = NSPasteboard.general
        pasteboard.clearContents()
        pasteboard.setString(addr, forType: .string)
        host?.toast(L10n.T("Address copied"))
    }

    // MARK: Hover buttons

    override func updateTrackingAreas() {
        super.updateTrackingAreas()
        for t in trackingAreas {
            removeTrackingArea(t)
        }
        addTrackingArea(NSTrackingArea(
            rect: .zero, options: [.mouseEnteredAndExited, .activeInKeyWindow, .inVisibleRect], owner: self))
    }

    override func mouseEntered(with event: NSEvent) {
        hovering = true
        showButtons()
    }

    override func mouseExited(with event: NSEvent) {
        hovering = false
        hideButtonsUnlessFocused()
    }

    private func showButtons() {
        header.buttonsShown = true
    }

    private func hideButtonsUnlessFocused() {
        guard !hovering, !header.buttonsFocused else { return }
        header.buttonsShown = false
    }

    private func hideButtonsUnlessHovering() {
        if !hovering {
            header.buttonsShown = false
        }
    }

    @objc private func replyClicked(_ sender: Any?) {
        host?.delegate?.reply(id)
    }

    @objc private func replyAllClicked(_ sender: Any?) {
        host?.delegate?.replyAll(id)
    }

    @objc private func forwardClicked(_ sender: Any?) {
        host?.delegate?.forward(id, from: window)
    }

    // MARK: Rendering

    /// Shows whatever `lm` holds (nil: nothing asked for yet, or let go by
    /// the pane): the body, the bars, the chips, the recipients and the
    /// delivery state. A body already shown stays when `lm` has none.
    func render(_ lm: LoadedMessage?) {
        renderDetails(lm?.msg)
        renderOutbox(lm?.msg)
        renderBody(lm)
        renderBars(lm)
        renderChips(lm)
    }

    /// Redraws the bars and leaves the body alone.
    func renderBars(_ lm: LoadedMessage?) {
        guard isHTML, let lm else {
            remoteBar?.isHidden = true
            picturesBar?.isHidden = true
            return
        }
        let remote = remoteBarState(for: lm)
        if remote.visible || remoteBar != nil {
            let bar = remoteBar ?? makeRemoteBar()
            bar.setRemoteText(remote)
            bar.setLoading(remote.loading)
            bar.isHidden = !remote.visible
        }
        let pictures = picturesBarState(for: lm)
        if pictures.visible || picturesBar != nil {
            let bar = picturesBar ?? makePicturesBar()
            bar.setPicturesText(pictures)
            bar.setLoading(pictures.loading)
            bar.isHidden = !pictures.visible
        }
    }

    private func makeRemoteBar() -> RemoteBarView {
        let bar = RemoteBarView(showsTrust: true)
        bar.onLoad = { [weak self] in
            guard let self else { return }
            self.host?.delegate?.loadImages(self.id)
        }
        bar.onTrust = { [weak self] in
            guard let self else { return }
            self.host?.delegate?.trustSender(self.id)
        }
        remoteBar = bar
        install(bar, .remote)
        return bar
    }

    private func makePicturesBar() -> RemoteBarView {
        let bar = RemoteBarView.pictures()
        bar.onLoad = { [weak self] in
            guard let self else { return }
            self.host?.delegate?.downloadPictures(self.id, from: self.window)
        }
        picturesBar = bar
        install(bar, .pictures)
        return bar
    }

    /// The chips for what `lm` holds (`ChipPlan`); nothing until
    /// message.get answered.
    func renderChips(_ lm: LoadedMessage?) {
        let plan = ChipPlan(lm?.msg, lm?.body)
        if plan.isEmpty {
            chips?.removeAllViews()
            chipViews = []
            chips?.isHidden = true
            return
        }
        guard let host else { return }
        let flow = chips ?? makeChips()
        flow.removeAllViews()
        let factory = AttachmentChipFactory(
            delegate: host.delegate, cache: host.cache,
            openEmbedded: { [weak self] s, a, remote, chip in self?.host?.openEmbedded(s, a, remote, chip) },
            window: { [weak self] in self?.window },
            chipForPart: { [weak self] part in
                self?.chipViews.first { ($0 as? AttachmentChipView)?.attachment.partId == part }
            })
        chipViews = factory.views(plan, of: summary)
        for v in chipViews {
            flow.addView(v)
        }
        flow.isHidden = false
    }

    private func makeChips() -> FlowView {
        let flow = FlowView(spacing: 6, lineSpacing: 6)
        chips = flow
        install(flow, .chips)
        return flow
    }

    /// The delivery state of a queued message (outbox.go
    /// `renderOutboxBanner`): the full message's once known, else the
    /// summary's.
    func renderOutbox(_ m: Message?) {
        let text = outboxBannerText(m?.summary.outbox ?? summary.outbox)
        guard text.shown || banner != nil else { return }
        let b = banner ?? makeBanner()
        if (m?.summary.outbox ?? summary.outbox)?.state == .failed {
            b.setSymbol("exclamationmark.triangle.fill", .warning)
        } else {
            b.setSymbol("paperplane", .info)
        }
        b.title = text.title
        b.buttonTitle = text.button
        b.reveal(text.shown, animated: false)
    }

    private func makeBanner() -> BannerView {
        let b = BannerView()
        b.onButton = { [weak self] in
            guard let self else { return }
            self.host?.delegate?.retryOutbox(self.id)
        }
        banner = b
        install(b, .banner)
        return b
    }

    /// The body, its error, or the wait; the same body again changes
    /// nothing (a re-render must not reload the web view or lay a long
    /// text out anew).
    private func renderBody(_ lm: LoadedMessage?) {
        guard let lm, lm.bodySettled else {
            if renderedBody == nil, !renderedErr {
                showWaiting()
            }
            return
        }
        let before = renderedBody
        if lm.body == renderedBody, (lm.err != nil) == renderedErr, before != nil || renderedErr {
            return
        }
        renderedBody = lm.body
        renderedErr = lm.err != nil
        changingHeight {
            if let err = lm.err {
                isHTML = false
                links = []
                setHint(false)
                showText(rpcErrorText(L10n.T("Loading the message"), err))
                return
            }
            let b = lm.body
            if showsHTML(b), let b, let html = b.html {
                links = b.links
                setHint(false)
                showHTML(html, reload: picturesArrived(before, b))
                return
            }
            isHTML = false
            links = []
            setHint(b?.htmlWithheld == true)
            showText(bodyText(b))
        }
    }

    /// The body shown again after its pictures kept on the mail server were
    /// downloaded: the same HTML, whose `malachi-cid:` pictures load now.
    private func picturesArrived(_ before: MessageBodyResult?, _ now: MessageBodyResult) -> Bool {
        guard let before, before.messageId == now.messageId else { return false }
        return before.remotePictureCount > 0 && before != now
    }

    /// Runs `change` through the host, which keeps what the user reads in
    /// place.
    private func changingHeight(_ change: () -> Void) {
        if let host {
            host.cardHeightChanging(change)
        } else {
            change()
        }
    }

    /// Nothing yet: the blank body at its estimate, "Loading…" in it.
    private func showWaiting() {
        guard slots[.body] != nil, !isHTML else { return }
        loadingLabel.isHidden = false
        bodyHeight.constant = ConversationLayout.estimatedBodyHeight
    }

    private func showText(_ text: String) {
        releaseWebView()
        html = nil
        isHTML = false
        bodyHost.paper = false
        uninstall(.body)
        let tv = textView ?? makeTextView()
        tv.setText(text)
    }

    /// The plain text at the card's padding: the text view's own margins
    /// are the single-message pane's, so its insets are the card's here
    /// and the room it keeps under the text is cut to the card's padding.
    private func makeTextView() -> MessageBodyTextView {
        let tv = MessageBodyTextView()
        let padding = tv.textContainer?.lineFragmentPadding ?? 0
        tv.textContainerInset = NSSize(width: max(Self.paddingH - padding, 0), height: Self.textInset)
        if let font = host?.bodyFont {
            tv.applyFont(font)
        }
        textView = tv
        let box = NSView()
        box.translatesAutoresizingMaskIntoConstraints = false
        box.addSubview(tv)
        let below = Self.textInset + MessageBodyTextView.extraBottom
        NSLayoutConstraint.activate([
            tv.topAnchor.constraint(equalTo: box.topAnchor),
            tv.leadingAnchor.constraint(equalTo: box.leadingAnchor),
            tv.trailingAnchor.constraint(equalTo: box.trailingAnchor),
            tv.bottomAnchor.constraint(equalTo: box.bottomAnchor, constant: max(below - Self.paddingV, 0)),
        ])
        install(box, .text)
        return tv
    }

    private func showHTML(_ body: String, reload: Bool) {
        uninstall(.text)
        textView = nil
        if slots[.body] == nil {
            install(bodyHost, .body)
        }
        loadingLabel.isHidden = true
        bodyHost.paper = true
        isHTML = true
        html = body
        htmlReload = htmlReload || reload
        bodyHeight.constant = webHeight
        if live {
            loadWebView()
        }
    }

    private func setHint(_ on: Bool) {
        if on, hintLabel == nil {
            let l = NSTextField(wrappingLabelWithString: L10n.T(
                "The formatted version of this message could not be shown safely; this is its plain text."))
            l.font = Typo.caption
            l.textColor = Tint.secondary
            l.isSelectable = false
            hintLabel = l
            install(l, .hint)
        }
        hintLabel?.isHidden = !on
    }

    // MARK: Web view

    /// Whether the card holds a web view (`ConversationLayout.live`): made
    /// and loaded when it becomes live, let go when it does not stay; the
    /// body keeps its last height meanwhile.
    func setLive(_ on: Bool) {
        guard on != live else { return }
        live = on
        if on {
            if isHTML {
                loadWebView()
            }
        } else {
            releaseWebView()
        }
    }

    private func loadWebView() {
        guard let html, let host else { return }
        let wv = webView ?? makeWebView(host)
        wv.load(body: html, reload: htmlReload)
        htmlReload = false
    }

    private func makeWebView(_ host: any ConversationCardHost) -> MessageWebView {
        let wv = MessageWebView(cache: host.cache, zoom: host.textZoom, sized: true)
        wv.onLink = { [weak self] link in
            guard let self else { return }
            self.host?.delegate?.openLink(link.href, links: self.links, from: self.window)
        }
        wv.onUnavailable = { [weak self] in self?.htmlUnavailable() }
        wv.onHover = { [weak self] href in self?.host?.hover(href) }
        wv.onHeight = { [weak self] h in self?.webHeightChanged(h) }
        bodyHost.addSubview(wv)
        NSLayoutConstraint.activate([
            wv.topAnchor.constraint(equalTo: bodyHost.topAnchor),
            wv.bottomAnchor.constraint(equalTo: bodyHost.bottomAnchor),
            wv.leadingAnchor.constraint(equalTo: bodyHost.leadingAnchor),
            wv.trailingAnchor.constraint(equalTo: bodyHost.trailingAnchor),
        ])
        webView = wv
        return wv
    }

    private func releaseWebView() {
        guard let wv = webView else { return }
        webView = nil
        wv.onHeight = nil
        wv.onHover = nil
        wv.removeFromSuperview()
        host?.hover("")
    }

    private func webHeightChanged(_ h: CGFloat) {
        guard h != webHeight || bodyHeight.constant != h else { return }
        changingHeight {
            webHeight = h
            bodyHeight.constant = h
        }
    }

    /// The web view cannot show HTML at all (its content rule list did not
    /// compile): the plain text with the hint instead, as for HTML the
    /// daemon withheld.
    private func htmlUnavailable() {
        guard let b = renderedBody, isHTML else { return }
        changingHeight {
            links = []
            setHint(true)
            showText(bodyText(b))
        }
    }

    // MARK: Settings

    /// The text-zoom and monospace settings changed.
    func applyFont(_ font: NSFont, zoom: Int) {
        textView?.applyFont(font)
        webView?.setZoom(zoom)
    }
}

/// The body area of a card for HTML and for the wait. Under an HTML body
/// it is the white of the reader's page (`viewerDocument` paints a light
/// canvas whatever the appearance), so a card that scrolls into view, whose
/// web view is yet to be made or to paint, does not flash in the card's own
/// background in the dark appearance.
@MainActor
private final class CardBodyHost: NSView {
    /// The area holds an HTML body.
    var paper = false {
        didSet {
            if paper != oldValue {
                needsDisplay = true
            }
        }
    }

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        wantsLayer = true
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var wantsUpdateLayer: Bool { true }

    override func updateLayer() {
        layer?.backgroundColor = paper ? NSColor.white.cgColor : nil
    }
}
