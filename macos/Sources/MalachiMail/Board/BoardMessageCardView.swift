// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

extension PrefsWrappingLabel {
    /// A wrapping label of the board's detail: selectable, in `font`, at
    /// most `lines` lines (0 for all) with the last one cut. The wrapping
    /// width follows the width the label is given, so inside a stack
    /// pinned at both edges the height is the wrapped one, in a narrow
    /// panel and a wide pane alike. Text is `stringValue` only.
    static func board(_ text: String, font: NSFont = Typo.body, color: NSColor = .labelColor, lines: Int = 0) -> PrefsWrappingLabel {
        let label = PrefsWrappingLabel(text, color: color)
        label.font = font
        label.isSelectable = true
        label.translatesAutoresizingMaskIntoConstraints = false
        if lines > 0 {
            label.maximumNumberOfLines = lines
            label.lineBreakMode = .byTruncatingTail
            label.cell?.wraps = true
            label.cell?.truncatesLastVisibleLine = true
        }
        return label
    }
}

/// What a card needs to show a message's HTML: the locked web view of Mail's
/// conversation cards (`MessageWebView`, sized mode) and where its links,
/// its hovers and its height changes go. Set by the conversation block.
@MainActor
struct BoardCardWeb {
    /// The cache the web view's `malachi-cid:` handler fetches parts from.
    let cache: MessageCache
    /// The text-zoom setting for a web view made now.
    let zoom: () -> Int
    /// A link the user activated, with the body's links as the daemon
    /// listed them (Mail's `openLink`: allow-list, masked-link question).
    let openLink: (ActivatedLink, [Link], NSWindow?) -> Void
    /// The link under the pointer ("" when none), for the detail's one
    /// status box.
    let hover: (String) -> Void
}

/// One message of the detail's conversation, as the prototype draws it: a
/// tinted card with a hairline border (`BoardPalette.messageFill`, the
/// user's own messages `ownMessageFill`), the sender in semibold at the
/// start and the time at the end, then the message.
///
/// The message is the excerpt the daemon gave (`board.get`, at most
/// `API.Limits.maxBoardMessageTextBytes`) with its line breaks, or, while
/// the card is open and `message.body` answered with HTML, that sanitised
/// HTML in the web view of Mail's conversation cards (`MessageWebView` in
/// its sized mode: content JavaScript off, the CSP, no network, links only
/// through `BoardCardWeb.openLink`). The web view spans the card under the
/// header from hairline to hairline on the reader's white page
/// (`WebPaperView`), as Mail's card does in both appearances; its document's
/// compact sheet gives the card's padding. What the card shows is the
/// conversation block's (`Board.ConversationCards.shows`): this view only
/// applies it (`apply`).
///
/// The text is a label as tall as its text and the web view takes the
/// height its document reports (capped, frozen when it grows with the view,
/// `WebHeightGovernor`): the detail's scroll view scrolls the column, wheel
/// events included (the web view hands them on while its document fits).
/// Every change of the card's height that does not come from a click goes
/// through `onHeightChanging`, so the detail can keep what the user sees in
/// place.
///
/// An older message starts folded (`foldable`): a preview of its text
/// (the lines joined, as Mail's folded card shows its snippet) of at most
/// `foldedLines` lines, not selectable (a selectable field shows its whole
/// text while it is being selected), with Mail's fold arrow and its Expand /
/// Collapse before the sender. The arrow shows when the card can show its
/// message formatted, or when the whole text is longer than the preview
/// (`Board.ConversationCards.arrow`); it opens the card in place.
/// The fill and the border are resolved in `updateLayer`, so they follow
/// the appearance.
@MainActor
final class BoardMessageCardView: NSView {
    /// The lines a folded card keeps.
    static let foldedLines = 3

    typealias Shows = Board.ConversationCards.Shows

    private var mine: Bool
    private(set) var foldable = false
    /// The arrow shows whatever the text's length (the card can show its
    /// message formatted).
    private var arrowAlways = false
    private(set) var folded = false
    /// What the card shows now (`apply`).
    private(set) var shows: Shows = .text
    /// The user opened or folded the card (its arrow).
    var onFold: ((Bool) -> Void)?
    /// The card cannot show HTML (its web view is unavailable): the
    /// conversation block takes it out of the web view count.
    var onUnavailable: (() -> Void)?
    /// Runs a change of the card's height that the user did not make
    /// (the web view's height, HTML in place of the text, a fold by the
    /// limit), so the detail keeps the viewport's content in place; nil
    /// runs it as it is.
    var onHeightChanging: ((_ card: BoardMessageCardView, _ change: () -> Void) -> Void)?

    private let column: FillStackView
    private let body: PrefsWrappingLabel
    private var text: String
    /// The text's lines joined by spaces: what a folded card shows.
    private var preview: String
    private let fromLabel = NSTextField(labelWithString: "")
    private let whenLabel = NSTextField(labelWithString: "")
    private let foldButton = CardFoldButton()
    /// The text is longer than `foldedLines` at the current width; nil
    /// until measured.
    private var long: Bool?
    private var measuredWidth: CGFloat = -1

    // The HTML body: the sanitiser's output from message.body and the
    // body's links as the daemon listed them; the paper under the web view
    // (in the card while it shows HTML) and the web view (while live).
    private(set) var html: String?
    private var links: [Link] = []
    private var web: BoardCardWeb?
    private var paper: WebPaperView?
    private var paperHeight: NSLayoutConstraint?
    private(set) var webView: MessageWebView?
    /// The height the web view last reported (kept while it is let go).
    private(set) var webHeight: CGFloat?

    /// The hairline, inside which the web view's paper keeps.
    private static let border = BoardMetrics.cardBorderWidth
    private static let padV = ConversationMetrics.cardPaddingV
    private static let padH = ConversationMetrics.cardPaddingH
    /// Under the header when the web view follows: the document's compact
    /// sheet adds its own 4 above the text.
    private static let headerBottom: CGFloat = 4
    /// The height the paper starts at before its document reports one,
    /// never less.
    private static let minimumPaper = CGFloat(ConversationLayout.initialWebHeight) / 2

    init(_ message: Board.MessageCard) {
        mine = message.mine
        text = message.text
        preview = message.text.split(whereSeparator: \.isNewline).joined(separator: " ")
        body = PrefsWrappingLabel.board(message.text)
        column = FillStackView()
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        wantsLayer = true
        layer?.cornerRadius = ConversationMetrics.cardRadius
        layer?.cornerCurve = .continuous
        layer?.borderWidth = Self.border
        // The corners clip the paper of an HTML body.
        layer?.masksToBounds = true

        let from = fromLabel
        from.stringValue = message.from
        from.font = .systemFont(ofSize: Typo.bodySize, weight: .semibold)
        from.lineBreakMode = .byTruncatingTail
        from.maximumNumberOfLines = 1
        from.isSelectable = true
        from.setContentCompressionResistancePriority(NSLayoutConstraint.Priority(250), for: .horizontal)
        from.setContentHuggingPriority(.defaultLow, for: .horizontal)
        let when = whenLabel
        when.stringValue = message.when
        when.font = Typo.caption
        when.textColor = Tint.secondary
        when.lineBreakMode = .byClipping
        when.maximumNumberOfLines = 1
        when.isSelectable = false
        when.setContentHuggingPriority(.required, for: .horizontal)
        when.setContentCompressionResistancePriority(.defaultHigh, for: .horizontal)
        foldButton.target = self
        foldButton.action = #selector(foldClicked(_:))
        foldButton.isHidden = true
        foldButton.setContentHuggingPriority(.required, for: .horizontal)
        let head = NSStackView(views: [foldButton, from, when])
        head.orientation = .horizontal
        head.alignment = .firstBaseline
        head.spacing = 8
        head.setCustomSpacing(4, after: foldButton)

        column.addArrangedSubview(head)
        column.addArrangedSubview(body)
        column.spacing = 5
        column.edgeInsets = NSEdgeInsets(top: Self.padV, left: Self.padH, bottom: Self.padV, right: Self.padH)
        addSubview(column)
        NSLayoutConstraint.activate([
            column.leadingAnchor.constraint(equalTo: leadingAnchor),
            column.trailingAnchor.constraint(equalTo: trailingAnchor),
            column.topAnchor.constraint(equalTo: topAnchor),
        ])
        bottomToColumn = column.bottomAnchor.constraint(equalTo: bottomAnchor)
        bottomToColumn.isActive = true
        showFold()
        // A group named after the sender; the text is read once, from the
        // labels inside it.
        setAccessibilityElement(true)
        setAccessibilityRole(.group)
        setAccessibilityLabel(message.from)
    }

    /// The message was rebuilt in place (same id, other excerpt): the new
    /// text, sender and time, in the same card, with its fold and its web
    /// view. The height change goes through `onHeightChanging`.
    func update(_ message: Board.MessageCard) {
        guard message.text != text || message.from != fromLabel.stringValue || message.when != whenLabel.stringValue
            || message.mine != mine
        else { return }
        changingHeight {
            mine = message.mine
            text = message.text
            preview = message.text.split(whereSeparator: \.isNewline).joined(separator: " ")
            fromLabel.stringValue = message.from
            whenLabel.stringValue = message.when
            setAccessibilityLabel(message.from)
            // Measured again at the next layout.
            long = nil
            measuredWidth = -1
            if shows != .web {
                showFold()
            }
            needsLayout = true
            needsDisplay = true
        }
    }

    /// The card's bottom: the column's while the text shows, the paper's
    /// while the HTML does.
    private var bottomToColumn: NSLayoutConstraint!

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    // MARK: Fold

    /// Makes the card one that folds (an older message) or not; with
    /// `arrowAlways` the arrow shows whatever the text's length. Whether it
    /// is folded is `apply`'s.
    func configure(foldable: Bool, arrowAlways: Bool) {
        guard foldable != self.foldable || arrowAlways != self.arrowAlways else { return }
        self.foldable = foldable
        self.arrowAlways = arrowAlways
        if !foldable {
            folded = false
        }
        if shows != .web {
            showFold()
        } else {
            showArrow()
        }
    }

    /// The height `text` needs in the body's font at `width` (0: one line).
    private func height(of text: String, width: CGFloat) -> CGFloat {
        let probe = NSTextField(wrappingLabelWithString: text)
        probe.font = body.font
        let w = width > 0 ? width : .greatestFiniteMagnitude
        return probe.cell?.cellSize(forBounds: NSRect(x: 0, y: 0, width: w, height: .greatestFiniteMagnitude)).height ?? 0
    }

    /// The whole text or the preview, the arrow shown or not, as `folded`
    /// and the measured length say. The preview is one paragraph, so the
    /// line limit ends it with an ellipsis on its last whole line; the
    /// label keeps its full vertical compression resistance either way,
    /// so nothing squeezes a line in half.
    private func showFold() {
        let cut = folded && long != false
        if cut {
            if body.stringValue != preview {
                body.stringValue = preview
            }
            body.maximumNumberOfLines = Self.foldedLines
            body.lineBreakMode = .byTruncatingTail
            body.cell?.truncatesLastVisibleLine = true
            body.isSelectable = false
        } else {
            if body.stringValue != text {
                body.stringValue = text
            }
            body.maximumNumberOfLines = 0
            body.lineBreakMode = .byWordWrapping
            body.cell?.truncatesLastVisibleLine = false
            body.isSelectable = true
        }
        body.invalidateIntrinsicContentSize()
        showArrow()
    }

    private func showArrow() {
        let arrow = foldable && (arrowAlways || long == true)
        if foldButton.isHidden == arrow {
            foldButton.isHidden = !arrow
        }
        let tip = folded ? L10n.T("Expand") : L10n.T("Collapse")
        foldButton.image = Icon.image(folded ? "pan-end" : "pan-down", size: .small)
        foldButton.toolTip = tip
        foldButton.setAccessibilityLabel(tip)
    }

    /// The text's width follows the card's (the column's insets inside
    /// it), so the length is measured whenever the card's width changes,
    /// whatever lays the text out.
    override func setFrameSize(_ newSize: NSSize) {
        super.setFrameSize(newSize)
        measure()
    }

    override func layout() {
        super.layout()
        measure()
    }

    /// Whether the whole text is longer than `foldedLines` at the card's
    /// text width; shows the arrow (and the preview) when that changes.
    private func measure() {
        guard foldable else { return }
        let width = bounds.width - 2 * Self.padH
        guard width > 0, width != measuredWidth else { return }
        measuredWidth = width
        let isLong = height(of: text, width: width) > height(of: "X", width: 0) * CGFloat(Self.foldedLines) + 1
        if isLong != long {
            long = isLong
            if shows != .web {
                showFold()
            } else {
                showArrow()
            }
        }
    }

    @objc private func foldClicked(_ sender: Any?) {
        onFold?(!folded)
    }

    // MARK: The body

    /// The sanitised HTML that message.body gave for the card's message,
    /// and its links; nil drops it (the excerpt stays). A web view showing
    /// the card loads the new document.
    func setHTML(_ html: String?, links: [Link]) {
        self.html = html
        self.links = html == nil ? [] : links
        if html != nil, shows == .web {
            loadWebView()
        }
    }

    /// Shows what the block decided (`Board.ConversationCards.shows`): the
    /// preview, the whole excerpt, or the HTML in a web view (`web` says
    /// how to make one). `.web` without HTML shows the excerpt.
    func apply(_ shows: Shows, folded: Bool, web: BoardCardWeb?) {
        self.web = web
        let target: Shows = shows == .web && (html == nil || web == nil) ? .text : shows
        let foldChanged = folded != self.folded
        guard target != self.shows || foldChanged else { return }
        changingHeight {
            self.folded = foldable && folded
            self.shows = target
            if target == .web {
                showWeb()
            } else {
                showText()
            }
        }
    }

    /// The excerpt (whole or the preview): the web view goes, the label
    /// comes back.
    private func showText() {
        releaseWebView()
        if let paper {
            paper.removeFromSuperview()
            self.paper = nil
            paperHeight = nil
        }
        bottomToColumn.isActive = true
        body.isHidden = false
        column.edgeInsets.bottom = Self.padV
        showFold()
    }

    /// The HTML: the paper under the header at the height last known (the
    /// text's height before the document reports one), the web view on it.
    private func showWeb() {
        let start = webHeight ?? max(body.isHidden ? Self.minimumPaper : body.frame.height, Self.minimumPaper)
        body.isHidden = true
        column.edgeInsets.bottom = Self.headerBottom
        showArrow()
        if paper == nil {
            let p = WebPaperView()
            p.paper = true
            addSubview(p)
            let h = p.heightAnchor.constraint(equalToConstant: start)
            paperHeight = h
            bottomToColumn.isActive = false
            NSLayoutConstraint.activate([
                p.topAnchor.constraint(equalTo: column.bottomAnchor),
                p.leadingAnchor.constraint(equalTo: leadingAnchor, constant: Self.border),
                p.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -Self.border),
                bottomAnchor.constraint(equalTo: p.bottomAnchor, constant: Self.border),
                h,
            ])
            paper = p
        }
        loadWebView()
    }

    private func loadWebView() {
        guard let html, let web, let paper else { return }
        let wv = webView ?? makeWebView(web, in: paper)
        wv.load(body: html)
    }

    private func makeWebView(_ web: BoardCardWeb, in paper: WebPaperView) -> MessageWebView {
        let wv = MessageWebView(cache: web.cache, zoom: web.zoom(), sized: true)
        wv.onLink = { [weak self] link in
            guard let self else { return }
            self.web?.openLink(link, self.links, self.window)
        }
        wv.onUnavailable = { [weak self] in self?.htmlUnavailable() }
        wv.onHover = { [weak self] href in self?.web?.hover(href) }
        wv.onHeight = { [weak self] h in self?.webHeightChanged(h) }
        paper.addSubview(wv)
        NSLayoutConstraint.activate([
            wv.topAnchor.constraint(equalTo: paper.topAnchor),
            wv.bottomAnchor.constraint(equalTo: paper.bottomAnchor),
            wv.leadingAnchor.constraint(equalTo: paper.leadingAnchor),
            wv.trailingAnchor.constraint(equalTo: paper.trailingAnchor),
        ])
        webView = wv
        Self.webViewsMade += 1
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
        web?.hover("")
    }

    private func webHeightChanged(_ h: CGFloat) {
        guard let paperHeight, h != paperHeight.constant else {
            webHeight = h
            return
        }
        changingHeight {
            webHeight = h
            paperHeight.constant = h
        }
    }

    /// The web view cannot show HTML at all (its content rule list did not
    /// compile): the excerpt, as for HTML the daemon withheld.
    private func htmlUnavailable() {
        html = nil
        links = []
        if shows == .web {
            changingHeight {
                shows = .text
                showText()
            }
        }
        onUnavailable?()
    }

    /// The text-zoom setting changed.
    func setZoom(_ zoom: Int) {
        webView?.setZoom(zoom)
    }

    private func changingHeight(_ change: () -> Void) {
        if let onHeightChanging {
            onHeightChanging(self, change)
        } else {
            change()
        }
    }

    // MARK: Development aid

    /// DEVELOPMENT AID (`MALACHI_START`): web views made by all cards
    /// since the start, to tell a refresh that recreated one.
    static var webViewsMade = 0

    /// DEVELOPMENT AID (`MALACHI_START`): the card's frame, the text's
    /// frame and the height the whole text needs, or the web view's.
    var developmentMetrics: String {
        if shows == .web {
            return String(format: "card %.0fx%.0f web: paper %.0fx%.0f reported %@ live view %d foldable %d folded %d arrow %d",
                          frame.width, frame.height, paper?.frame.width ?? -1, paper?.frame.height ?? -1,
                          webHeight.map { String(format: "%.0f", $0) } ?? "none", webView != nil ? 1 : 0,
                          foldable ? 1 : 0, folded ? 1 : 0, foldButton.isHidden ? 0 : 1)
        }
        let need = height(of: body.stringValue, width: body.bounds.width)
        return String(format: "card %.0fx%.0f text %.0fx%.0f needs %.0f (whole text %.0f, a line %.0f) cut %.0f foldable %d folded %d arrow %d",
                      frame.width, frame.height, body.frame.width, body.frame.height, need, height(of: text, width: body.bounds.width),
                      height(of: "X", width: 0), max(0, need - body.frame.height),
                      foldable ? 1 : 0, folded ? 1 : 0, foldButton.isHidden ? 0 : 1)
    }

    /// DEVELOPMENT AID: the body label, for the wheel check.
    var developmentBody: NSTextField { body }

    override var wantsUpdateLayer: Bool { true }

    override func updateLayer() {
        effectiveAppearance.performAsCurrentDrawingAppearance {
            layer?.backgroundColor = (mine ? BoardPalette.ownMessageFill : BoardPalette.messageFill).cgColor
            layer?.borderColor = BoardPalette.cardBorder(in: self).cgColor
        }
    }
}
