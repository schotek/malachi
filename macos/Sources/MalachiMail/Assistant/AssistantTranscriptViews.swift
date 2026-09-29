// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

// The entries of the assistant panel's transcript (ui/internal/assistant,
// the In App target; no Blueprint yet, the GTK widgets follow): the user's
// question, the answer, a tool at work, a draft card, an error and a note.
// Everything shown here comes from the model or from mail it read, so it is
// plain text in labels (`stringValue`) or attributed text built here from
// `Assistant.markdown` with fonts and colours only: never HTML, never RTF,
// never an `NSAttributedString.Key.link` that the text view would follow by
// itself. A link is a private attribute whose click goes to the actions'
// `openLink` with no listed links, so every link is confirmed with its
// destination first.

/// Turns the Markdown subset into attributed text for `AssistantTextView`.
@MainActor
enum AssistantMarkdownRenderer {
    /// The URL of a link run (an http or https string); the view opens it
    /// through the confirmation, never AppKit.
    static let linkKey = NSAttributedString.Key("io.github.schotek.Malachi.assistantLink")
    /// Joins the lines of one block without starting a new paragraph.
    static let lineSeparator = String(Character(Unicode.Scalar(UInt32(0x2028)) ?? "\n"))

    static let bodySize: CGFloat = 13
    /// The indentation of one list level, and of the text after a marker.
    static let listIndent: CGFloat = 14

    static func render(_ markdown: String) -> NSAttributedString {
        let blocks = Assistant.markdown(markdown)
        let out = NSMutableAttributedString()
        for (i, b) in blocks.enumerated() {
            if i > 0 {
                out.append(NSAttributedString(string: "\n", attributes: [.font: NSFont.systemFont(ofSize: bodySize)]))
            }
            out.append(render(b))
        }
        return out
    }

    private static func render(_ b: Assistant.Block) -> NSAttributedString {
        let para = NSMutableParagraphStyle()
        para.paragraphSpacing = 6
        var base = NSFont.systemFont(ofSize: bodySize)
        var prefix = ""
        switch b.kind {
        case .paragraph:
            break
        case .heading:
            base = .systemFont(ofSize: b.level == 1 ? 16 : b.level == 2 ? 14.5 : bodySize, weight: .bold)
            para.paragraphSpacingBefore = 4
            para.paragraphSpacing = 4
        case .bullet, .numbered:
            let indent = CGFloat(b.level) * listIndent
            let marker = b.kind == .bullet ? "•" : "\(b.number)."
            prefix = marker + "\t"
            let textStart = indent + (b.kind == .bullet ? listIndent : listIndent + 8)
            para.firstLineHeadIndent = indent
            para.headIndent = textStart
            para.tabStops = [NSTextTab(textAlignment: .left, location: textStart)]
            para.paragraphSpacing = 3
        case .code:
            base = .monospacedSystemFont(ofSize: bodySize - 1, weight: .regular)
            para.firstLineHeadIndent = 6
            para.headIndent = 6
        }
        let out = NSMutableAttributedString()
        let attrs: [NSAttributedString.Key: Any] = [.font: base, .foregroundColor: NSColor.labelColor, .paragraphStyle: para]
        if !prefix.isEmpty {
            out.append(NSAttributedString(string: prefix, attributes: attrs))
        }
        for span in b.spans {
            out.append(render(span, base: base, attrs: attrs, block: b.kind))
        }
        if b.kind == .code, out.length > 0 {
            out.addAttribute(.backgroundColor, value: NSColor.quaternarySystemFill, range: NSRange(location: 0, length: out.length))
        }
        return out
    }

    private static func render(
        _ s: Assistant.Span, base: NSFont, attrs: [NSAttributedString.Key: Any], block: Assistant.BlockKind
    ) -> NSAttributedString {
        var a = attrs
        var font = base
        if s.code, block != .code {
            font = .monospacedSystemFont(ofSize: base.pointSize - 1, weight: s.bold ? .bold : .regular)
            a[.backgroundColor] = NSColor.quaternarySystemFill
        }
        if s.bold {
            font = NSFontManager.shared.convert(font, toHaveTrait: .boldFontMask)
        }
        if s.italic {
            font = NSFontManager.shared.convert(font, toHaveTrait: .italicFontMask)
        }
        a[.font] = font
        if !s.link.isEmpty {
            a[linkKey] = s.link
            a[.foregroundColor] = NSColor.linkColor
            a[.underlineStyle] = NSUnderlineStyle.single.rawValue
            a[.cursor] = NSCursor.pointingHand
            // The destination on hover, before any click.
            a[.toolTip] = s.link
        }
        let text = s.text.replacingOccurrences(of: "\n", with: lineSeparator)
        return NSAttributedString(string: text, attributes: a)
    }
}

/// An answer: attributed text, selectable and inert like the plain-text
/// body of a message (`MessageBodyTextView`: no editing, no rich paste, no
/// data detectors, Copy and Select All only, no Services, no Look Up),
/// sized to its text. A click on a link run reports its URL.
@MainActor
final class AssistantTextView: NSTextView {
    /// A link run was clicked.
    var onLink: ((String) -> Void)?

    init() {
        let storage = NSTextStorage()
        let layout = NSLayoutManager()
        let container = NSTextContainer(size: NSSize(width: 0, height: CGFloat.greatestFiniteMagnitude))
        container.widthTracksTextView = true
        container.heightTracksTextView = false
        container.lineFragmentPadding = 0
        layout.addTextContainer(container)
        storage.addLayoutManager(layout)
        super.init(frame: .zero, textContainer: container)
        translatesAutoresizingMaskIntoConstraints = false
        isEditable = false
        isSelectable = true
        isRichText = false
        importsGraphics = false
        allowsUndo = false
        usesFontPanel = false
        usesFindBar = false
        isAutomaticLinkDetectionEnabled = false
        isAutomaticDataDetectionEnabled = false
        isAutomaticQuoteSubstitutionEnabled = false
        isAutomaticDashSubstitutionEnabled = false
        isAutomaticTextReplacementEnabled = false
        isAutomaticSpellingCorrectionEnabled = false
        isContinuousSpellCheckingEnabled = false
        isGrammarCheckingEnabled = false
        drawsBackground = false
        textContainerInset = .zero
        isVerticallyResizable = false
        isHorizontallyResizable = false
        minSize = .zero
        maxSize = NSSize(width: CGFloat.greatestFiniteMagnitude, height: CGFloat.greatestFiniteMagnitude)
        setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        setContentHuggingPriority(.defaultLow, for: .horizontal)
        setContentCompressionResistancePriority(.required, for: .vertical)
        setContentHuggingPriority(.required, for: .vertical)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Shows `markdown`, rendered by `AssistantMarkdownRenderer`.
    func setMarkdown(_ markdown: String) {
        textStorage?.setAttributedString(AssistantMarkdownRenderer.render(markdown))
        invalidateIntrinsicContentSize()
    }

    override var intrinsicContentSize: NSSize {
        guard let layoutManager, let textContainer else {
            return super.intrinsicContentSize
        }
        // Before the first layout there is no width to wrap at: a
        // zero-wide container would stack every character on a line.
        guard bounds.width > 0 else {
            return NSSize(width: NSView.noIntrinsicMetric, height: 0)
        }
        layoutManager.ensureLayout(for: textContainer)
        let used = layoutManager.usedRect(for: textContainer)
        return NSSize(width: NSView.noIntrinsicMetric, height: ceil(used.height))
    }

    override func setFrameSize(_ newSize: NSSize) {
        let changed = newSize.width != frame.width
        super.setFrameSize(newSize)
        if changed {
            invalidateIntrinsicContentSize()
        }
    }

    // MARK: Links

    /// The URL of the link run under `point` (view coordinates), if any.
    private func link(at point: NSPoint) -> String? {
        guard let layoutManager, let textContainer, let storage = textStorage, storage.length > 0 else { return nil }
        let p = NSPoint(x: point.x - textContainerOrigin.x, y: point.y - textContainerOrigin.y)
        let glyph = layoutManager.glyphIndex(for: p, in: textContainer)
        let rect = layoutManager.boundingRect(forGlyphRange: NSRange(location: glyph, length: 1), in: textContainer)
        guard rect.contains(p) else { return nil }
        let index = layoutManager.characterIndexForGlyph(at: glyph)
        guard index < storage.length else { return nil }
        return storage.attribute(AssistantMarkdownRenderer.linkKey, at: index, effectiveRange: nil) as? String
    }

    override func mouseDown(with event: NSEvent) {
        if event.clickCount == 1, let href = link(at: convert(event.locationInWindow, from: nil)) {
            onLink?(href)
            return
        }
        super.mouseDown(with: event)
    }

    // MARK: Text services (as MessageBodyTextView)

    override func menu(for event: NSEvent) -> NSMenu? {
        plainTextContextMenu()
    }

    override func validRequestor(forSendType sendType: NSPasteboard.PasteboardType?, returnType: NSPasteboard.PasteboardType?) -> Any? {
        nil
    }

    override func quickLook(with event: NSEvent) {}
}

/// A transcript entry that follows its item.
@MainActor
protocol AssistantItemView: NSView {
    /// Whether this view can show `content` (same kind).
    func accepts(_ content: AssistantPanelController.Content) -> Bool
    func update(_ content: AssistantPanelController.Content)
}

/// A wrapping, non-selectable label for model or mail text: plain text
/// (`stringValue`), its height following the width it gets.
@MainActor
func assistantLabel(_ text: String = "", size: CGFloat = 13, color: NSColor = .labelColor) -> NSTextField {
    let l = PrefsWrappingLabel(text, size: size, color: color)
    l.allowsEditingTextAttributes = false
    l.translatesAutoresizingMaskIntoConstraints = false
    return l
}

/// The user's question: a tinted bubble with the action's label over the
/// typed text, indented from the leading edge. The labels span the bubble:
/// a wrapping label that sized itself to its text would keep an old
/// wrapping width when the panel widens.
@MainActor
final class AssistantUserView: NSView, AssistantItemView {
    private let bubble = CalloutCard()
    private let label = assistantLabel(size: 11, color: .secondaryLabelColor)
    private let text = assistantLabel()

    init(_ content: AssistantPanelController.Content) {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        let column = FillStackView(fillingViews: [label, text])
        column.spacing = 2
        bubble.addSubview(column)
        addSubview(bubble)
        NSLayoutConstraint.activate([
            bubble.topAnchor.constraint(equalTo: topAnchor),
            bubble.bottomAnchor.constraint(equalTo: bottomAnchor),
            bubble.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 28),
            bubble.trailingAnchor.constraint(equalTo: trailingAnchor),
            column.topAnchor.constraint(equalTo: bubble.topAnchor, constant: 6),
            column.bottomAnchor.constraint(equalTo: bubble.bottomAnchor, constant: -6),
            column.leadingAnchor.constraint(equalTo: bubble.leadingAnchor, constant: 10),
            column.trailingAnchor.constraint(equalTo: bubble.trailingAnchor, constant: -10),
        ])
        update(content)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    func accepts(_ content: AssistantPanelController.Content) -> Bool {
        if case .user = content { return true }
        return false
    }

    func update(_ content: AssistantPanelController.Content) {
        guard case .user(let l, let t) = content else { return }
        label.stringValue = l
        label.isHidden = l.isEmpty
        text.stringValue = t
        text.isHidden = t.isEmpty
    }
}

/// An answer, rendered from its Markdown.
@MainActor
final class AssistantAnswerView: NSView, AssistantItemView {
    let textView = AssistantTextView()
    private var shown = ""

    init(_ content: AssistantPanelController.Content, onLink: @escaping (String) -> Void) {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        textView.onLink = onLink
        addSubview(textView)
        NSLayoutConstraint.activate([
            textView.topAnchor.constraint(equalTo: topAnchor),
            textView.bottomAnchor.constraint(equalTo: bottomAnchor),
            textView.leadingAnchor.constraint(equalTo: leadingAnchor),
            textView.trailingAnchor.constraint(equalTo: trailingAnchor),
        ])
        update(content)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    func accepts(_ content: AssistantPanelController.Content) -> Bool {
        if case .assistant = content { return true }
        return false
    }

    func update(_ content: AssistantPanelController.Content) {
        guard case .assistant(let text, _) = content, text != shown else { return }
        shown = text
        textView.setMarkdown(text)
    }
}

/// A tool at work: a spinner, then a check mark, beside its label.
@MainActor
final class AssistantActivityView: NSView, AssistantItemView {
    private let spinner = Spinner(size: 16)
    private let check = NSImageView()
    private let label = assistantLabel(size: 12, color: .secondaryLabelColor)

    init(_ content: AssistantPanelController.Content) {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        check.image = NSImage(systemSymbolName: "checkmark.circle", accessibilityDescription: nil)?
            .withSymbolConfiguration(NSImage.SymbolConfiguration(pointSize: 12, weight: .regular))
        check.contentTintColor = .tertiaryLabelColor
        check.translatesAutoresizingMaskIntoConstraints = false
        check.setAccessibilityElement(false)
        for v in [spinner, check, label] as [NSView] {
            addSubview(v)
        }
        NSLayoutConstraint.activate([
            spinner.leadingAnchor.constraint(equalTo: leadingAnchor),
            spinner.centerYAnchor.constraint(equalTo: label.centerYAnchor),
            check.centerXAnchor.constraint(equalTo: spinner.centerXAnchor),
            check.centerYAnchor.constraint(equalTo: spinner.centerYAnchor),
            label.leadingAnchor.constraint(equalTo: spinner.trailingAnchor, constant: 6),
            label.trailingAnchor.constraint(equalTo: trailingAnchor),
            label.topAnchor.constraint(equalTo: topAnchor),
            label.bottomAnchor.constraint(equalTo: bottomAnchor),
        ])
        update(content)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    func accepts(_ content: AssistantPanelController.Content) -> Bool {
        if case .activity = content { return true }
        return false
    }

    func update(_ content: AssistantPanelController.Content) {
        guard case .activity(let text, let done) = content else { return }
        label.stringValue = text
        check.isHidden = !done
        if done {
            spinner.stop()
        } else {
            spinner.start()
        }
    }
}

/// A draft the bridge saved: "A draft is ready" with Open Draft.
@MainActor
final class AssistantDraftView: NSView, AssistantItemView {
    private let symbol = CalloutCard.symbolView()
    private let label = assistantLabel()
    let button: NSButton

    init(_ content: AssistantPanelController.Content, open: @escaping () -> Void) {
        let t = Assistant.panelTexts()
        button = NSButton(title: t.openDraft, target: nil, action: nil)
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        CalloutCard.show("doc.text", .info, in: symbol)
        label.stringValue = t.draftReady
        button.bezelStyle = .push
        button.controlSize = .small
        button.target = self
        button.action = #selector(clicked(_:))
        self.open = open
        let row = NSStackView(views: [symbol, label, button])
        row.orientation = .horizontal
        row.alignment = .centerY
        row.distribution = .fill
        row.spacing = CalloutCard.spacing
        button.setContentHuggingPriority(.required, for: .horizontal)
        button.setContentCompressionResistancePriority(.required, for: .horizontal)
        CalloutCard.install(row, in: self, margins: NSEdgeInsets())
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    private var open: (() -> Void)?

    @objc private func clicked(_ sender: Any?) {
        open?()
    }

    func accepts(_ content: AssistantPanelController.Content) -> Bool {
        if case .draft = content { return true }
        return false
    }

    func update(_ content: AssistantPanelController.Content) {}
}

/// An error line (red, with Try Again when the question can be sent once
/// more) or a note (secondary).
@MainActor
final class AssistantMessageLineView: NSView, AssistantItemView {
    private let symbol = CalloutCard.symbolView()
    private let label = assistantLabel(size: 12)
    private let retryButton = NSButton(title: Assistant.panelTexts().tryAgain, target: nil, action: nil)
    private let retry: () -> Void

    init(_ content: AssistantPanelController.Content, retry: @escaping () -> Void) {
        self.retry = retry
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        retryButton.bezelStyle = .push
        retryButton.controlSize = .small
        retryButton.target = self
        retryButton.action = #selector(retryClicked(_:))
        let text = NSStackView(views: [label, retryButton])
        text.orientation = .vertical
        text.alignment = .leading
        text.spacing = 4
        text.setHuggingPriority(.defaultLow, for: .horizontal)
        // The text column takes the row's width beside the symbol.
        let row = NSStackView(views: [symbol, text])
        row.orientation = .horizontal
        row.alignment = .top
        row.distribution = .fill
        row.spacing = 6
        row.translatesAutoresizingMaskIntoConstraints = false
        addSubview(row)
        NSLayoutConstraint.activate([
            row.topAnchor.constraint(equalTo: topAnchor),
            row.bottomAnchor.constraint(equalTo: bottomAnchor),
            row.leadingAnchor.constraint(equalTo: leadingAnchor),
            row.trailingAnchor.constraint(equalTo: trailingAnchor),
            label.widthAnchor.constraint(equalTo: text.widthAnchor),
        ])
        update(content)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    @objc private func retryClicked(_ sender: Any?) {
        retry()
    }

    func accepts(_ content: AssistantPanelController.Content) -> Bool {
        switch content {
        case .error, .note: return true
        default: return false
        }
    }

    func update(_ content: AssistantPanelController.Content) {
        switch content {
        case .error(let text, let canRetry):
            label.stringValue = text
            label.textColor = .systemRed
            symbol.image = NSImage(systemSymbolName: "exclamationmark.triangle", accessibilityDescription: nil)?
                .withSymbolConfiguration(NSImage.SymbolConfiguration(pointSize: 12, weight: .medium))
            symbol.contentTintColor = .systemRed
            symbol.isHidden = false
            retryButton.isHidden = !canRetry
        case .note(let text):
            label.stringValue = text
            label.textColor = .secondaryLabelColor
            symbol.isHidden = true
            retryButton.isHidden = true
        default:
            break
        }
    }
}

/// The transcript: one view per item of `AssistantPanelController`, top to
/// bottom in a scroll view. While the view is scrolled to the end it stays
/// there as items arrive and an answer streams in; a user who scrolled up
/// to read is left where they are until they scroll back down (or the
/// conversation starts over).
@MainActor
final class AssistantTranscriptView: NSView {
    /// How near the end still counts as at the end.
    static let stickDistance: CGFloat = 24
    static let spacing: CGFloat = 10
    static let insets = NSEdgeInsets(top: 12, left: 12, bottom: 12, right: 12)

    let scrollView = NSScrollView()
    private let document = AssistantDocumentView()
    private let stack = FillStackView()
    /// The item views, in the order of the controller's items.
    private var views: [any AssistantItemView] = []
    /// Whether the view follows the end.
    private var followsEnd = true
    /// The view scrolls itself: its own bounds change says nothing about
    /// where the user wants to be.
    private var scrolling = false

    /// Makes the view of an item; the panel installs it.
    var makeView: (@MainActor (AssistantPanelController.Item) -> any AssistantItemView)?

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        stack.spacing = Self.spacing
        stack.edgeInsets = Self.insets
        document.translatesAutoresizingMaskIntoConstraints = false
        document.addSubview(stack)
        scrollView.translatesAutoresizingMaskIntoConstraints = false
        scrollView.hasVerticalScroller = true
        scrollView.hasHorizontalScroller = false
        scrollView.autohidesScrollers = true
        scrollView.drawsBackground = false
        scrollView.borderType = .noBorder
        scrollView.documentView = document
        addSubview(scrollView)
        let clip = scrollView.contentView
        NSLayoutConstraint.activate([
            scrollView.topAnchor.constraint(equalTo: topAnchor),
            scrollView.bottomAnchor.constraint(equalTo: bottomAnchor),
            scrollView.leadingAnchor.constraint(equalTo: leadingAnchor),
            scrollView.trailingAnchor.constraint(equalTo: trailingAnchor),
            stack.topAnchor.constraint(equalTo: document.topAnchor),
            stack.bottomAnchor.constraint(equalTo: document.bottomAnchor),
            stack.leadingAnchor.constraint(equalTo: document.leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: document.trailingAnchor),
            document.leadingAnchor.constraint(equalTo: clip.leadingAnchor),
            document.trailingAnchor.constraint(equalTo: clip.trailingAnchor),
            document.topAnchor.constraint(equalTo: clip.topAnchor),
        ])
        clip.postsBoundsChangedNotifications = true
        // A selector observer unregisters itself when the view goes.
        NotificationCenter.default.addObserver(
            self, selector: #selector(clipBoundsChanged(_:)), name: NSView.boundsDidChangeNotification, object: clip)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    // MARK: Following the controller

    /// Brings the views in line with `items` after `change`.
    func apply(_ change: AssistantPanelController.Change, items: [AssistantPanelController.Item]) {
        switch change {
        case .reset:
            rebuild(items)
            followsEnd = true
        case .appended(let i):
            if i == views.count, items.indices.contains(i) {
                append(items[i])
            } else {
                rebuild(items)
            }
        case .updated(let i):
            guard items.indices.contains(i), views.indices.contains(i), views.count == items.count else {
                rebuild(items)
                break
            }
            if views[i].accepts(items[i].content) {
                views[i].update(items[i].content)
            } else {
                replace(at: i, with: items[i])
            }
        }
        stickToEnd()
    }

    private func rebuild(_ items: [AssistantPanelController.Item]) {
        for v in views {
            stack.removeArrangedSubview(v)
            v.removeFromSuperview()
        }
        views = []
        for item in items {
            append(item)
        }
    }

    private func append(_ item: AssistantPanelController.Item) {
        guard let v = makeView?(item) else { return }
        views.append(v)
        stack.addArrangedSubview(v)
    }

    private func replace(at i: Int, with item: AssistantPanelController.Item) {
        guard let v = makeView?(item) else { return }
        let old = views[i]
        stack.removeArrangedSubview(old)
        old.removeFromSuperview()
        views[i] = v
        stack.insertArrangedSubview(v, at: i)
    }

    // MARK: Scrolling

    /// The clip view moved: at the end (or near it) the view follows the
    /// end again, anywhere else it stays.
    @objc private func clipBoundsChanged(_ note: Foundation.Notification) {
        guard !scrolling else { return }
        followsEnd = distanceToEnd() <= Self.stickDistance
    }

    private func distanceToEnd() -> CGFloat {
        let clip = scrollView.contentView
        return document.frame.height - clip.bounds.maxY
    }

    /// Scrolls to the end after the layout, while following it.
    private func stickToEnd() {
        guard followsEnd else { return }
        layoutSubtreeIfNeeded()
        let clip = scrollView.contentView
        let y = max(0, document.frame.height - clip.bounds.height)
        guard abs(clip.bounds.origin.y - y) > 0.5 else { return }
        scrolling = true
        clip.scroll(to: NSPoint(x: 0, y: y))
        scrollView.reflectScrolledClipView(clip)
        scrolling = false
    }

    override func layout() {
        super.layout()
        // A new width rewraps the answers; the end stays in sight.
        if followsEnd, distanceToEnd() > 0.5 {
            DispatchQueue.main.async { [weak self] in
                self?.stickToEnd()
            }
        }
    }
}

/// The transcript's document: flipped, so a short conversation sits at the
/// top, as wide as the clip view.
@MainActor
private final class AssistantDocumentView: NSView {
    override var isFlipped: Bool { true }
}
