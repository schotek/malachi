// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// What a bubble showing HTML needs from the chat it sits in
/// (`ConversationBubbleView.setLive`): what a timeline card asks of its
/// `ConversationCardHost` for its web view.
@MainActor
protocol ConversationBubbleWebHost: AnyObject {
    /// The cache the web view's `malachi-cid:` scheme handler reads.
    var cache: MessageCache { get }
    /// The text-zoom setting, for a web view made now.
    var textZoom: Int { get }
    /// A link the user activated in the bubble's document, with the
    /// body's links (the card's "Open this link?").
    func openLink(_ href: String, links: [Link], from window: NSWindow?)
    /// The link under the pointer ("" when none).
    func hover(_ href: String)
    /// Runs `change`, which alters the bubble's height, keeping the
    /// chat's end or the row the user reads in place.
    func bubbleHeightChanging(_ change: () -> Void)
}

/// A bubble of the chat layout (docs/plans/chat-view.md D3, D6, D14, D15):
/// a message of the conversation, or the user's draft, as plain text only.
/// The header has the unread dot (only of someone else's message), the
/// sender, a paperclip pill (`ConversationClipPill`: from the first moment
/// when the summary has attachments, with the number of the timeline's
/// chips once message.get told it, `Conversation.bubbleAttachmentBadge`),
/// the Draft and Internal badges of a
/// draft, the Internal badge, who relayed it ("via …") and Edited of an
/// issue's comment, and at the trailing end the date (the list's short form
/// in a narrow pane, the full one in the tooltip) or, for a draft, when it
/// was saved. Under it the text: the summary's snippet, dimmed, until the
/// body is there (`Conversation.bubbleBody`), then the message's text
/// without its quoted history, or the draft's.
///
/// With the preference Render HTML in Chat (`conversation-chat-html`) a
/// message whose body has HTML (`Conversation.BubbleBody.html`) shows the
/// same body as its timeline card, in a transparent `MessageWebView` of its
/// own in the sized mode where the text is (the chat's sheet: no
/// backgrounds of the message, the bubble's padding, its colours in the
/// dark appearance), while the chat keeps the bubble live
/// (`setLive`: near the viewport, within the timeline's cap); otherwise,
/// and until the web view exists, the bubble reads its text. Such a bubble
/// takes its full width. The web view takes the clicks on the document, so
/// a double click opens the window from the header and the padding only.
///
/// With the preference of its side, Shorten My / Others' Messages in
/// Chat, the bubble shows about
/// three lines (`setShortened`, `Conversation.chatPreviewLines`): the text
/// at most that many lines with an ellipsis, the HTML slot clipped to that
/// many lines of the chat font (the web view keeps loading and measuring
/// the whole document under the clip) behind a fade into the bubble's
/// colour, which also takes the clicks off the document; a "Show More"
/// hint under the body only when there is more. A single click anywhere
/// expands it (`onExpand`, the chat records it); a double click still opens
/// the window. A draft's bubble is always shortened, light red, without
/// the hint, and a single click opens the draft (`onDoubleClick`).
///
/// Every text is plain (`stringValue`) and none can be selected: a double
/// click anywhere in the bubble opens the message in its own window, a
/// draft in the compose window (`onDoubleClick`), where everything else is.
/// The bubble is a light grey card on the pane (`ConversationTint.theirsFill`,
/// a hairline, rounded corners);
/// the user's own is tinted with the accent colour and has a border of it
/// (`ConversationTint.mineFill`). It hugs its text, never wider than the
/// row allows (`setMaxWidth`, `ConversationLayout.chatBubbleMax`) and never
/// narrower than its header, whose sender is truncated first when the row
/// is too narrow for it. The bubble is one accessibility group named by its
/// sender and date, as a card is.
@MainActor
final class ConversationBubbleView: NSView {
    /// The user's own message or draft.
    let mine: Bool
    /// A double click anywhere in the bubble.
    var onDoubleClick: (@MainActor () -> Void)?
    /// A single click on a shortened bubble: the whole message, please.
    var onExpand: (@MainActor () -> Void)?

    /// The short date of the list instead of the full date and time (a
    /// narrow pane, `ConversationLayout.compactDates`).
    var compactDate = false {
        didSet {
            if compactDate != oldValue {
                renderDate()
            }
        }
    }

    private let header = NSStackView()
    private let unread = UnreadDot()
    private let senderLabel = NSTextField(labelWithString: "")
    private let clipPill = ConversationClipPill()
    private let draftPill = PillLabel()
    private let internalPill = PillLabel()
    private let viaLabel = NSTextField(labelWithString: "")
    private let editedLabel = NSTextField(labelWithString: "")
    private let dateLabel = NSTextField(labelWithString: "")
    private let bodyLabel = NSTextField(wrappingLabelWithString: "")
    private var widthLimit: NSLayoutConstraint!
    /// The bubble takes its whole width (`widthLimit`'s): its body is HTML.
    private var fullWidth: NSLayoutConstraint!

    /// The chat, for the web view of an HTML body.
    weak var webHost: (any ConversationBubbleWebHost)?
    /// The body area of HTML: the web view while live.
    private let htmlSlot = WebPaperView()
    private var htmlHeight: NSLayoutConstraint!
    private var webView: MessageWebView?
    /// The height the document last reported, kept while no web view is
    /// live.
    private var webHeight = CGFloat(ConversationLayout.initialWebHeight)
    /// The document reported its height (`webHeight` is measured, not the
    /// estimate).
    private var webHeightReported = false
    /// While an HTML bubble holds no web view after it had one, its text
    /// keeps the document's last height (`webHeight`), as a timeline card
    /// keeps its paper at its last height (`ConversationCardView.setLive`):
    /// the row's frame does not change when the chat's live window lets go
    /// of the view, so `ConversationLayout.live` cannot take it back and
    /// forth. Off with the HTML (`leaveHTML`: the setting off, a text body,
    /// `dropHTML`).
    private var frozenHeight: NSLayoutConstraint!
    /// The web view's own height, the document's whole height even while
    /// the slot clips it (`htmlHeight`).
    private var webViewHeight: NSLayoutConstraint?

    /// The bubble shows the shortened preview (`setShortened`).
    private(set) var shortened = false
    /// The bubble is a draft's (`showDraft`): always shortened, light red,
    /// no "Show More", and a single click opens the draft (`onDoubleClick`).
    private(set) var isDraft = false
    /// "Show More" under a shortened body that has more.
    private let hintLabel = NSTextField(labelWithString: "")
    /// The fade over the bottom of a clipped document, which also takes the
    /// clicks the web view would.
    private let fade = BubbleFadeView()
    /// The HTML body shown (`Conversation.BubbleBody.html`), and the text
    /// the bubble reads while it holds no web view.
    private var htmlBody: MessageBodyResult?
    private var htmlText = ""
    private var htmlReload = false
    /// The bubble holds a web view (set by the chat's live window).
    private(set) var live = false
    /// The bubble holds a web view now.
    var holdsWebView: Bool { webView != nil }
    /// The body is HTML: the bubble wants a web view while near.
    var rendersHTML: Bool { htmlBody != nil }

    /// What the header names for accessibility and the date it shows.
    private var sender = ""
    private var date: Date?
    /// A draft's "Draft saved …" in the date's place.
    private var saved: String?
    /// The text shows a body (not the placeholder): a let-go entry keeps
    /// it (`showBody`).
    private(set) var showsText = false
    private var shownBody: Conversation.BubbleBody?
    private var fonts: (normal: NSFont, monospace: NSFont)
    /// The text shows in the monospaced font.
    private var monospace = false

    private static let border: CGFloat = 1
    /// How strongly the bubble keeps to the width of its content: below
    /// every part's resistance to compression (the sender's is the
    /// lowest), so it shrinks to the text and the whole header, and only
    /// the row's limit (`setMaxWidth`) truncates the sender.
    private static let hugging = NSLayoutConstraint.Priority(200)
    /// The padding inside the bubble, from its edge.
    static let paddingH: CGFloat = 13
    static let paddingV: CGFloat = 9

    /// `fonts`: the body's font, and the monospaced one for a plain-text
    /// body (`Conversation.bubbleMonospace`).
    init(mine: Bool, fonts: (normal: NSFont, monospace: NSFont)) {
        self.mine = mine
        self.fonts = fonts
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        build(fonts.normal)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    private func build(_ bodyFont: NSFont) {
        wantsLayer = true
        layer?.cornerRadius = ConversationMetrics.cardRadius
        layer?.cornerCurve = .continuous
        layer?.borderWidth = Self.border

        senderLabel.font = Typo.bodyMedium
        senderLabel.textColor = .labelColor
        dateLabel.font = Typo.caption
        dateLabel.textColor = Tint.secondary
        for l in [viaLabel, editedLabel] {
            l.font = Typo.caption
            l.textColor = Tint.secondary
        }
        for l in [senderLabel, viaLabel, editedLabel, dateLabel] {
            l.isSelectable = false
            l.lineBreakMode = .byTruncatingTail
            l.maximumNumberOfLines = 1
        }
        // The sender gives way first, then the badges; the date last.
        senderLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        for l in [viaLabel, editedLabel] {
            l.setContentCompressionResistancePriority(NSLayoutConstraint.Priority(300), for: .horizontal)
        }
        dateLabel.setContentCompressionResistancePriority(NSLayoutConstraint.Priority(749), for: .horizontal)
        dateLabel.setContentHuggingPriority(.required, for: .horizontal)

        clipPill.isHidden = true

        draftPill.font = Typo.caption
        draftPill.stringValue = Conversation.draftBadge()
        IssuePill.paint(draftPill, IssuePill.draftColours, emphasized: false)
        draftPill.isHidden = true
        internalPill.font = Typo.caption
        IssuePill.paint(internalPill, IssuePill.internalColours, emphasized: false)
        internalPill.isHidden = true
        for p in [draftPill, internalPill, clipPill] as [NSView] {
            p.setContentCompressionResistancePriority(NSLayoutConstraint.Priority(600), for: .horizontal)
        }
        unread.isHidden = true

        let spacer = NSView()
        spacer.translatesAutoresizingMaskIntoConstraints = false
        spacer.setContentHuggingPriority(.init(1), for: .horizontal)
        spacer.setContentCompressionResistancePriority(.init(1), for: .horizontal)
        for v in [
            unread, senderLabel, clipPill, draftPill, internalPill, viaLabel, editedLabel, spacer, dateLabel,
        ] as [NSView] {
            header.addArrangedSubview(v)
        }
        header.orientation = .horizontal
        header.alignment = .centerY
        header.spacing = 6
        header.setCustomSpacing(12, after: spacer)
        header.setHuggingPriority(Self.hugging, for: .horizontal)

        bodyLabel.font = bodyFont
        bodyLabel.textColor = .labelColor
        bodyLabel.isSelectable = false
        bodyLabel.maximumNumberOfLines = 0
        bodyLabel.lineBreakMode = .byWordWrapping
        bodyLabel.isHidden = true

        // No paper: the transparent web view shows the bubble's own
        // background, as the text does.
        htmlSlot.isHidden = true
        htmlSlot.paper = false

        htmlSlot.layer?.masksToBounds = true
        fade.translatesAutoresizingMaskIntoConstraints = false
        fade.isHidden = true

        hintLabel.stringValue = Conversation.chatShowMore()
        hintLabel.font = Typo.caption
        hintLabel.textColor = .linkColor
        hintLabel.isSelectable = false
        hintLabel.isHidden = true

        let column = NSStackView(views: [header, bodyLabel, htmlSlot, hintLabel])
        column.orientation = .vertical
        column.alignment = .leading
        column.spacing = 6
        column.edgeInsets = NSEdgeInsets(
            top: Self.paddingV, left: Self.paddingH, bottom: Self.paddingV, right: Self.paddingH)
        column.setHuggingPriority(Self.hugging, for: .horizontal)
        column.translatesAutoresizingMaskIntoConstraints = false
        addSubview(column)
        widthLimit = widthAnchor.constraint(lessThanOrEqualToConstant: 10_000)
        fullWidth = widthAnchor.constraint(equalToConstant: 10_000)
        fullWidth.priority = .defaultHigh
        htmlHeight = htmlSlot.heightAnchor.constraint(equalToConstant: webHeight)
        frozenHeight = bodyLabel.heightAnchor.constraint(equalToConstant: webHeight)
        NSLayoutConstraint.activate([
            column.topAnchor.constraint(equalTo: topAnchor),
            column.bottomAnchor.constraint(equalTo: bottomAnchor),
            column.leadingAnchor.constraint(equalTo: leadingAnchor),
            column.trailingAnchor.constraint(equalTo: trailingAnchor),
            header.widthAnchor.constraint(equalTo: column.widthAnchor, constant: -2 * Self.paddingH),
            htmlSlot.widthAnchor.constraint(equalTo: column.widthAnchor, constant: -2 * Self.paddingH),
            htmlHeight,
            widthLimit,
        ])

        // A double click anywhere opens; a single click does nothing and
        // is not held back.
        let doubleClick = NSClickGestureRecognizer(target: self, action: #selector(doubleClicked(_:)))
        doubleClick.numberOfClicksRequired = 2
        doubleClick.delaysPrimaryMouseButtonEvents = false
        doubleClick.delegate = self
        addGestureRecognizer(doubleClick)
        // A single click expands a shortened bubble at once (the double
        // click's first click may expand it first: accepted).
        let click = NSClickGestureRecognizer(target: self, action: #selector(clicked(_:)))
        click.numberOfClicksRequired = 1
        click.delaysPrimaryMouseButtonEvents = false
        click.delegate = self
        addGestureRecognizer(click)

        setAccessibilityElement(true)
        setAccessibilityRole(.group)
    }

    override var wantsUpdateLayer: Bool { true }

    override func updateLayer() {
        if isDraft {
            layer?.backgroundColor = ConversationTint.draftFill(in: self).cgColor
            var border = ConversationTint.draftBorder.cgColor
            effectiveAppearance.performAsCurrentDrawingAppearance {
                border = ConversationTint.draftBorder.cgColor
            }
            layer?.borderColor = border
            fade.colour = ConversationTint.draftFill(in: self)
            return
        }
        if mine {
            layer?.backgroundColor = ConversationTint.mineFill(in: self).cgColor
            var border = ConversationTint.mineBorder.cgColor
            effectiveAppearance.performAsCurrentDrawingAppearance {
                border = ConversationTint.mineBorder.cgColor
            }
            layer?.borderColor = border
        } else {
            layer?.backgroundColor = ConversationTint.theirsFill(in: self).cgColor
            layer?.borderColor = ConversationTint.cardBorder(in: self).cgColor
        }
        fade.colour = mine ? ConversationTint.mineFill(in: self) : ConversationTint.theirsFill(in: self)
    }

    /// The widest the bubble may be (`ConversationLayout.chatBubbleMax`):
    /// the text wraps inside it.
    func setMaxWidth(_ width: CGFloat) {
        let w = max(width, 2 * Self.paddingH + 1)
        guard widthLimit.constant != w else { return }
        widthLimit.constant = w
        fullWidth.constant = w
        bodyLabel.preferredMaxLayoutWidth = w - 2 * Self.paddingH
        bodyLabel.invalidateIntrinsicContentSize()
        applyShortening()
    }

    // MARK: Shortened

    /// Shows the shortened preview (`on`, `Conversation.bubbleShortened`)
    /// or the whole message.
    func setShortened(_ on: Bool) {
        let on = on || isDraft
        guard on != shortened else { return }
        shortened = on
        applyShortening()
    }

    /// The preview's height of an HTML body: `chatPreviewLines` lines of
    /// the chat document's font (the text bubbles' size at the text zoom,
    /// line height 1.3, `viewerChatFontCSS`).
    private var previewHeight: CGFloat {
        (CGFloat(Conversation.chatPreviewLines) * 1.3 * fonts.normal.pointSize).rounded(.up)
    }

    /// The HTML slot's height: the document's, clipped to the preview while
    /// shortened.
    private var shownHTMLHeight: CGFloat {
        shortened ? min(webHeight, previewHeight) : webHeight
    }

    /// The text, the slot, the fade and the hint as `shortened` asks.
    private func applyShortening() {
        bodyLabel.maximumNumberOfLines = shortened ? Conversation.chatPreviewLines : 0
        bodyLabel.lineBreakMode = shortened ? .byTruncatingTail : .byWordWrapping
        bodyLabel.cell?.truncatesLastVisibleLine = shortened
        bodyLabel.cell?.wraps = true
        bodyLabel.invalidateIntrinsicContentSize()
        htmlHeight.constant = shownHTMLHeight
        if frozenHeight.isActive {
            frozenHeight.constant = shownHTMLHeight
        }
        let clipped = shortened && webView != nil && !htmlSlot.isHidden && webHeight > previewHeight + 0.5
        fade.isHidden = !clipped
        let more: Bool
        if !shortened {
            more = false
        } else if rendersHTML, webHeightReported {
            more = webHeight > previewHeight + 0.5
        } else {
            more = textExceeds()
        }
        hintLabel.isHidden = !more || isDraft
    }

    /// Whether the bubble is a draft's (`showDraft` sets it, the others
    /// clear it).
    private func setDraft(_ on: Bool) {
        guard on != isDraft else { return }
        isDraft = on
        needsDisplay = true
        if on {
            shortened = true
        }
        applyShortening()
    }

    /// The text needs more than `chatPreviewLines` lines at the width it
    /// wraps at.
    private func textExceeds() -> Bool {
        let text = bodyLabel.stringValue
        let width = bodyLabel.preferredMaxLayoutWidth
        guard !text.isEmpty, !bodyLabel.isHidden || frozenHeight.isActive, width > 0, let font = bodyLabel.font else {
            return false
        }
        let full = (text as NSString).boundingRect(
            with: NSSize(width: width, height: .greatestFiniteMagnitude),
            options: [.usesLineFragmentOrigin, .usesFontLeading], attributes: [.font: font]).height
        let line = NSLayoutManager().defaultLineHeight(for: font)
        return full > line * CGFloat(Conversation.chatPreviewLines) + 0.5
    }

    @objc private func clicked(_ g: NSClickGestureRecognizer) {
        guard g.state == .ended else { return }
        if isDraft {
            // A draft opens in the compose window at once.
            onDoubleClick?()
        } else if shortened {
            onExpand?()
        }
    }

    // MARK: Content

    /// The header of a message (`item`, as the model has it now);
    /// `attachments` is its paperclip pill
    /// (`Conversation.bubbleAttachmentBadge`).
    func showMessage(_ item: Conversation.Item, attachments: Conversation.BubbleAttachments) {
        setDraft(false)
        sender = item.sender
        senderLabel.stringValue = item.sender
        senderLabel.toolTip = item.sender.isEmpty ? nil : item.sender
        unread.isHidden = !item.unread || mine
        clipPill.show(attachments)
        draftPill.isHidden = true
        internalPill.stringValue = item.internalLabel
        internalPill.isHidden = !item.internal
        viaLabel.stringValue = item.via
        viaLabel.toolTip = item.via.isEmpty ? nil : item.via
        viaLabel.isHidden = item.via.isEmpty
        editedLabel.stringValue = item.edited
        editedLabel.isHidden = item.edited.isEmpty
        date = item.message.map(\.date)
        saved = nil
        renderDate()
    }

    /// The header of a message known only by its sender and date, as a
    /// member of a case's conversation on the board is
    /// (`Board.MessageCard`): no unread dot, paperclip, badges, via or
    /// Edited; `date` nil hides the date.
    func showPlain(sender name: String, date when: Date?) {
        setDraft(false)
        sender = name
        senderLabel.stringValue = name
        senderLabel.toolTip = name.isEmpty ? nil : name
        unread.isHidden = true
        clipPill.show(.none)
        draftPill.isHidden = true
        internalPill.isHidden = true
        viaLabel.isHidden = true
        editedLabel.isHidden = true
        date = when
        saved = nil
        renderDate()
    }

    /// A draft of the user's (D14): its header with the Draft badge, the
    /// Internal one of an internal comment and when it was saved, its text.
    func showDraft(_ card: Conversation.DraftCard) {
        setDraft(true)
        sender = card.sender
        senderLabel.stringValue = card.sender
        senderLabel.toolTip = card.sender.isEmpty ? nil : card.sender
        unread.isHidden = true
        clipPill.show(.none)
        draftPill.isHidden = false
        internalPill.stringValue = card.internalLabel
        internalPill.isHidden = !card.internal
        viaLabel.isHidden = true
        editedLabel.isHidden = true
        date = nil
        saved = card.draft.updatedAt <= .goZero ? "" : Conversation.draftSaved(formatTime(card.draft.updatedAt))
        renderDate()
        showBody(.text(card.text), monospace: draftMonospace)
    }

    /// A draft's text follows the setting for plain text, as the
    /// timeline's draft card does.
    var draftMonospace = false

    /// The text under the header: a placeholder (dimmed), or the text. An
    /// empty one hides the label. A placeholder does not replace a text
    /// shown already (the controller let go of the body, or fetches it
    /// again: the bubble keeps what it read). `monospace` picks the
    /// monospaced font for the text (never for a placeholder).
    func showBody(_ body: Conversation.BubbleBody, monospace: Bool) {
        if case .placeholder = body, showsText {
            return
        }
        var mono = monospace
        if case .placeholder = body {
            mono = false
        }
        if mono != self.monospace {
            self.monospace = mono
            bodyLabel.font = mono ? fonts.monospace : fonts.normal
            bodyLabel.invalidateIntrinsicContentSize()
        }
        guard body != shownBody else { return }
        shownBody = body
        switch body {
        case .placeholder(let s):
            showsText = false
            leaveHTML()
            showLabel(s, color: Tint.secondary)
        case .text(let s):
            showsText = true
            leaveHTML()
            showLabel(s, color: .labelColor)
        case .html(let b, let text):
            showsText = true
            let before = htmlBody
            htmlBody = b
            htmlText = text
            htmlReload = htmlReload || picturesArrived(before, b)
            fullWidth.isActive = true
            if live {
                loadWebView()
            } else {
                showLabel(text, color: .labelColor)
            }
        }
    }

    /// The HTML body shown goes back to its text: the preference was
    /// switched off while the entry was let go (nothing to render anew).
    func dropHTML() {
        guard rendersHTML else { return }
        let text = htmlText
        shownBody = .text(text)
        leaveHTML()
        showLabel(text, color: .labelColor)
    }

    private func showLabel(_ s: String, color: NSColor) {
        htmlSlot.isHidden = true
        let freeze = rendersHTML && webHeightReported
        frozenHeight.constant = shownHTMLHeight
        frozenHeight.isActive = freeze
        bodyLabel.stringValue = s
        bodyLabel.textColor = color
        bodyLabel.isHidden = s.isEmpty && !freeze
        applyShortening()
    }

    /// The body is no longer HTML: no web view, the bubble hugs its text.
    private func leaveHTML() {
        releaseWebView()
        htmlBody = nil
        htmlText = ""
        htmlReload = false
        fullWidth.isActive = false
        frozenHeight.isActive = false
        webHeightReported = false
        webHeight = CGFloat(ConversationLayout.initialWebHeight)
    }

    /// The same body again after its pictures kept on the mail server were
    /// downloaded: the same HTML, whose `malachi-cid:` pictures load now
    /// (ConversationCardView `picturesArrived`).
    private func picturesArrived(_ before: MessageBodyResult?, _ now: MessageBodyResult) -> Bool {
        guard let before, before.messageId == now.messageId else { return false }
        return before.remotePictureCount > 0 && before != now
    }

    // MARK: Web view

    /// Whether the bubble holds a web view (the chat's live window,
    /// `ConversationLayout.live`): made and loaded when it becomes live
    /// with an HTML body, let go when it does not stay, the text shown
    /// again.
    func setLive(_ on: Bool) {
        guard on != live else { return }
        live = on
        if on {
            if rendersHTML {
                loadWebView()
            }
        } else {
            releaseWebView()
            if rendersHTML {
                showLabel(htmlText, color: .labelColor)
            }
        }
    }

    private func loadWebView() {
        guard let b = htmlBody, let html = b.html, let host = webHost else { return }
        let wv = webView ?? makeWebView(host)
        frozenHeight.isActive = false
        bodyLabel.isHidden = true
        htmlSlot.isHidden = false
        applyShortening()
        wv.load(body: html, reload: htmlReload)
        htmlReload = false
    }

    private func makeWebView(_ host: any ConversationBubbleWebHost) -> MessageWebView {
        let wv = MessageWebView(cache: host.cache, zoom: host.textZoom, chat: true)
        wv.onLink = { [weak self] link in
            guard let self else { return }
            self.webHost?.openLink(link.href, links: self.htmlBody?.links ?? [], from: self.window)
        }
        wv.onUnavailable = { [weak self] in self?.htmlUnavailable() }
        wv.onHover = { [weak self] href in self?.webHost?.hover(href) }
        wv.onHeight = { [weak self] h in self?.webHeightChanged(h) }
        htmlSlot.addSubview(wv)
        htmlSlot.addSubview(fade)
        // The view as tall as the document; the slot clips it while the
        // bubble is shortened.
        let height = wv.heightAnchor.constraint(equalToConstant: webHeight)
        webViewHeight = height
        NSLayoutConstraint.activate([
            wv.topAnchor.constraint(equalTo: htmlSlot.topAnchor),
            height,
            wv.leadingAnchor.constraint(equalTo: htmlSlot.leadingAnchor),
            wv.trailingAnchor.constraint(equalTo: htmlSlot.trailingAnchor),
            fade.topAnchor.constraint(equalTo: htmlSlot.topAnchor),
            fade.bottomAnchor.constraint(equalTo: htmlSlot.bottomAnchor),
            fade.leadingAnchor.constraint(equalTo: htmlSlot.leadingAnchor),
            fade.trailingAnchor.constraint(equalTo: htmlSlot.trailingAnchor),
        ])
        webView = wv
        return wv
    }

    private func releaseWebView() {
        guard let wv = webView else { return }
        webView = nil
        wv.onHeight = nil
        wv.onHover = nil
        wv.onLink = nil
        wv.onUnavailable = nil
        wv.removeFromSuperview()
        fade.removeFromSuperview()
        webViewHeight = nil
        htmlSlot.isHidden = true
        webHost?.hover("")
    }

    private func webHeightChanged(_ h: CGFloat) {
        webHeightReported = true
        guard h != webHeight || webViewHeight?.constant != h else {
            applyShortening()
            return
        }
        let apply = {
            self.webHeight = h
            self.webViewHeight?.constant = h
            self.applyShortening()
        }
        if let host = webHost {
            host.bubbleHeightChanging(apply)
        } else {
            apply()
        }
    }

    /// The web view cannot show HTML at all (its content rule list did not
    /// compile): the text instead, as the card does.
    private func htmlUnavailable() {
        guard rendersHTML else { return }
        let change = { self.dropHTML() }
        if let host = webHost {
            host.bubbleHeightChanging(change)
        } else {
            change()
        }
    }

    /// The point `p` (the bubble's coordinates) is on the document of a
    /// live web view, which takes its clicks itself.
    private func onDocument(_ p: NSPoint) -> Bool {
        // A shortened document is under the fade, which is the bubble's.
        guard webView != nil, !htmlSlot.isHidden, !shortened else { return false }
        return htmlSlot.convert(htmlSlot.bounds, to: self).contains(p)
    }

    /// The text shows in the monospaced font.
    var isMonospace: Bool { monospace }

    /// The monospace setting changed: the text in the font it asks for
    /// (never a placeholder's).
    func setMonospace(_ on: Bool) {
        let mono = on && showsText
        guard mono != monospace else { return }
        monospace = mono
        bodyLabel.font = mono ? fonts.monospace : fonts.normal
        bodyLabel.invalidateIntrinsicContentSize()
    }

    /// The text-zoom setting changed: both fonts anew, the text in the one
    /// it shows in.
    func setFonts(normal: NSFont, monospace: NSFont, zoom: Int? = nil) {
        if let zoom {
            webView?.setZoom(zoom)
        }
        fonts = (normal, monospace)
        bodyLabel.font = self.monospace ? monospace : normal
        bodyLabel.invalidateIntrinsicContentSize()
        applyShortening()
    }

    /// The date (or a draft's saved time) at the header's end, and the
    /// accessibility name: the sender and the full date.
    private func renderDate() {
        if let saved {
            dateLabel.stringValue = saved
            dateLabel.toolTip = nil
            dateLabel.isHidden = saved.isEmpty
            setAccessibilityLabel([sender, Conversation.draftBadge(), saved].filter { !$0.isEmpty }
                .joined(separator: ", "))
            return
        }
        let full = date.map { $0.isGoZero ? "" : formatDateTime($0) } ?? ""
        dateLabel.stringValue = compactDate && !full.isEmpty ? date.map { formatDate($0, now: Date()) } ?? "" : full
        dateLabel.toolTip = compactDate && !full.isEmpty ? full : nil
        dateLabel.isHidden = full.isEmpty
        setAccessibilityLabel([sender, full].filter { !$0.isEmpty }.joined(separator: ", "))
    }

    @objc private func doubleClicked(_ g: NSClickGestureRecognizer) {
        guard g.state == .ended else { return }
        onDoubleClick?()
    }
}

/// The paperclip pill of a bubble's header (docs/plans/chat-view.md D3): a
/// rounded badge drawn as the list's and the issue's pills are
/// (`PillLabel`, in the colours of the bulk tag, `IssuePill.bulkColours`),
/// so that it reads at a glance on someone else's bubble and on the user's
/// tinted one: the paperclip alone while only the summary says the message
/// has attachments, the paperclip and the number once message.get told it
/// (`Conversation.BubbleAttachments`). One accessibility element, "Has
/// attachments" (also its tooltip), with the number as its value.
@MainActor
final class ConversationClipPill: NSView {
    private let icon = NSImageView()
    private let count = NSTextField(labelWithString: "")
    private let row = NSStackView()

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        let colours = IssuePill.bulkColours
        icon.image = NSImage(systemSymbolName: "paperclip", accessibilityDescription: nil)?
            .withSymbolConfiguration(NSImage.SymbolConfiguration(pointSize: Typo.captionSize, weight: .semibold))
        icon.contentTintColor = colours.text
        icon.setAccessibilityElement(false)
        count.font = Typo.captionNumeric
        count.textColor = colours.text
        count.isSelectable = false
        count.maximumNumberOfLines = 1
        count.setAccessibilityElement(false)
        count.isHidden = true
        for v in [icon, count] as [NSView] {
            v.setContentHuggingPriority(.required, for: .horizontal)
            v.setContentCompressionResistancePriority(.required, for: .horizontal)
            row.addArrangedSubview(v)
        }
        row.orientation = .horizontal
        row.alignment = .centerY
        row.spacing = 2
        row.edgeInsets = NSEdgeInsets(top: 1, left: Self.padding, bottom: 1, right: Self.padding)
        row.translatesAutoresizingMaskIntoConstraints = false
        addSubview(row)
        NSLayoutConstraint.activate([
            row.topAnchor.constraint(equalTo: topAnchor),
            row.bottomAnchor.constraint(equalTo: bottomAnchor),
            row.leadingAnchor.constraint(equalTo: leadingAnchor),
            row.trailingAnchor.constraint(equalTo: trailingAnchor),
        ])
        setContentHuggingPriority(.required, for: .horizontal)
        let label = L10n.T("Has attachments")
        toolTip = label
        setAccessibilityElement(true)
        setAccessibilityRole(.image)
        setAccessibilityLabel(label)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// The pill's side padding, a little tighter than a text pill's: the
    /// paperclip has room of its own.
    private static let padding: CGFloat = RowMetrics.badgePadding - 1

    /// Shows `badge`: hidden for `.none`, the paperclip for `.some`, the
    /// paperclip and the number for `.count`.
    func show(_ badge: Conversation.BubbleAttachments) {
        switch badge {
        case .none:
            isHidden = true
        case .some:
            count.isHidden = true
            setAccessibilityValue(nil)
            isHidden = false
        case .count(let n):
            let text = String(n)
            if count.stringValue != text {
                count.stringValue = text
            }
            count.isHidden = false
            setAccessibilityValue(text)
            isHidden = false
        }
    }

    override func draw(_ dirtyRect: NSRect) {
        let radius = bounds.height / 2
        IssuePill.bulkColours.fill.setFill()
        NSBezierPath(roundedRect: bounds, xRadius: radius, yRadius: radius).fill()
    }
}

extension ConversationBubbleView: NSGestureRecognizerDelegate {
    /// The double click reads every click on the bubble but those on the
    /// document of its live web view (word selection, links): the header
    /// and the padding still open the window.
    func gestureRecognizer(_ gestureRecognizer: NSGestureRecognizer, shouldAttemptToRecognizeWith event: NSEvent) -> Bool {
        !onDocument(convert(event.locationInWindow, from: nil))
    }
}

/// The fade over the bottom of a shortened bubble's clipped document: from
/// clear to the bubble's colour, over the lower half; it covers the whole
/// slot so that a click lands on the bubble, not on the document.
@MainActor
final class BubbleFadeView: NSView {
    var colour: NSColor = .clear {
        didSet { needsDisplay = true }
    }

    override func draw(_ dirtyRect: NSRect) {
        guard let gradient = NSGradient(colors: [colour.withAlphaComponent(0), colour]) else { return }
        let half = NSRect(x: 0, y: 0, width: bounds.width, height: bounds.height / 2)
        // Flipped or not, the lower half (y up: the bottom is minY).
        let rect = isFlipped ? half.offsetBy(dx: 0, dy: bounds.height / 2) : half
        gradient.draw(in: rect, angle: isFlipped ? 90 : -90)
    }

    override func hitTest(_ point: NSPoint) -> NSView? {
        isHidden ? nil : super.hitTest(point)
    }
}
