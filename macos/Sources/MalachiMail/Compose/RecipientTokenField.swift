// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// One finished recipient of a To, Cc or Bcc row: the address capsule of a
/// message's header (`CapsuleStyle`) with a small × on its right. A selected
/// badge is drawn in the selection colours, an invalid entry (no address in
/// it) as a red-tinted capsule. The text is the token's label, plain, cut in
/// the middle when long; the whole address is the tooltip. Names and
/// addresses are user and server data, never markup.
@MainActor
final class RecipientBadgeView: NSView {
    static let maxTextWidth: CGFloat = 240
    private static let crossSide: CGFloat = 14
    private static let crossGap: CGFloat = 3
    private static let trailingPadding: CGFloat = 5

    /// What a badge draws and measures of a long entry: the model keeps the
    /// whole text, the view never lays out more than this.
    static let maxLabelCharacters = 200
    static let maxTooltipCharacters = 1000

    let token: RecipientTokens.Token
    private let displayLabel: String
    /// Measured once, with the font that never changes.
    private let textWidth: CGFloat

    /// `s` cut to `limit` Unicode scalars, with an ellipsis when it was longer.
    static func shortened(_ s: String, _ limit: Int) -> String {
        let head = s.unicodeScalars.prefix(limit + 1)
        if head.count <= limit {
            return s
        }
        var view = String.UnicodeScalarView()
        view.append(contentsOf: head.prefix(limit))
        return String(view) + "\u{2026}"
    }

    var isSelected = false {
        didSet {
            if isSelected != oldValue {
                needsDisplay = true
            }
        }
    }

    /// A click on the capsule, a double click on it, and a click on the ×.
    var onSelect: (@MainActor () -> Void)?
    var onEdit: (@MainActor () -> Void)?
    var onRemove: (@MainActor () -> Void)?

    private var tracking: NSTrackingArea?
    private var crossHovered = false {
        didSet {
            if crossHovered != oldValue {
                needsDisplay = true
            }
        }
    }

    private var attributes: [NSAttributedString.Key: Any] {
        let style = NSMutableParagraphStyle()
        style.lineBreakMode = .byTruncatingMiddle
        return [.font: CapsuleStyle.font, .paragraphStyle: style]
    }

    init(token: RecipientTokens.Token) {
        self.token = token
        let label = Self.shortened(token.label, Self.maxLabelCharacters)
        displayLabel = label
        let style = NSMutableParagraphStyle()
        style.lineBreakMode = .byTruncatingMiddle
        let attrs: [NSAttributedString.Key: Any] = [.font: CapsuleStyle.font, .paragraphStyle: style]
        textWidth = min(ceil(NSString(string: label).size(withAttributes: attrs).width), Self.maxTextWidth)
        super.init(frame: .zero)
        let tip = Self.shortened(token.tooltip, Self.maxTooltipCharacters)
        toolTip = tip
        setAccessibilityLabel(tip)
        setAccessibilityRole(.button)
        setAccessibilityCustomActions([
            NSAccessibilityCustomAction(name: L10n.T("Remove"), target: self, selector: #selector(accessibilityRemove)),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var isFlipped: Bool { true }
    override var mouseDownCanMoveWindow: Bool { false }
    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

    override func isAccessibilityElement() -> Bool { true }

    override func accessibilityPerformPress() -> Bool {
        onSelect?()
        return true
    }

    @objc private func accessibilityRemove() -> Bool {
        onRemove?()
        return true
    }

    override var intrinsicContentSize: NSSize {
        NSSize(
            width: CapsuleStyle.padding + textWidth + Self.crossGap + Self.crossSide + Self.trailingPadding,
            height: CapsuleStyle.height)
    }

    private var crossRect: NSRect {
        NSRect(
            x: bounds.maxX - Self.trailingPadding - Self.crossSide,
            y: (bounds.height - Self.crossSide) / 2,
            width: Self.crossSide, height: Self.crossSide)
    }

    override func draw(_ dirtyRect: NSRect) {
        let text: NSColor
        let secondary: NSColor
        if isSelected {
            NSColor.selectedContentBackgroundColor.setFill()
            text = .alternateSelectedControlTextColor
            secondary = text.withAlphaComponent(0.8)
        } else if !token.isValid {
            NSColor.systemRed.withAlphaComponent(0.16).setFill()
            text = .systemRed
            secondary = .systemRed
        } else {
            Tint.fg(alpha: CapsuleStyle.restingAlpha).setFill()
            text = .labelColor
            secondary = Tint.secondary
        }
        CapsuleStyle.path(in: bounds).fill()

        var attrs = attributes
        attrs[.foregroundColor] = text
        let height = ceil(CapsuleStyle.font.ascender - CapsuleStyle.font.descender)
        let rect = NSRect(
            x: CapsuleStyle.padding, y: (bounds.height - height) / 2,
            width: textWidth, height: height)
        NSString(string: displayLabel).draw(in: rect, withAttributes: attrs)

        let cross = crossRect
        if crossHovered {
            (isSelected ? NSColor.white.withAlphaComponent(0.25) : Tint.fg(alpha: 0.15)).setFill()
            NSBezierPath(ovalIn: cross).fill()
        }
        let c = cross.insetBy(dx: 4.5, dy: 4.5)
        let path = NSBezierPath()
        path.move(to: NSPoint(x: c.minX, y: c.minY))
        path.line(to: NSPoint(x: c.maxX, y: c.maxY))
        path.move(to: NSPoint(x: c.minX, y: c.maxY))
        path.line(to: NSPoint(x: c.maxX, y: c.minY))
        path.lineWidth = 1.4
        path.lineCapStyle = .round
        secondary.setStroke()
        path.stroke()
    }

    override func updateTrackingAreas() {
        super.updateTrackingAreas()
        if let tracking {
            removeTrackingArea(tracking)
        }
        let area = NSTrackingArea(
            rect: .zero, options: [.mouseEnteredAndExited, .mouseMoved, .activeInActiveApp, .inVisibleRect],
            owner: self, userInfo: nil)
        addTrackingArea(area)
        tracking = area
    }

    override func mouseMoved(with event: NSEvent) {
        crossHovered = crossRect.contains(convert(event.locationInWindow, from: nil))
    }

    override func mouseExited(with event: NSEvent) {
        crossHovered = false
    }

    override func mouseDown(with event: NSEvent) {
        let p = convert(event.locationInWindow, from: nil)
        if crossRect.insetBy(dx: -2, dy: -2).contains(p) {
            onRemove?()
        } else if event.clickCount >= 2 {
            onEdit?()
        } else {
            onSelect?()
        }
    }
}

/// The inline editor of a recipient row: a borderless, single-line, plain
/// text view. Keys that the row reads (Return, Tab, Backspace, the arrows)
/// come through `onCommand`; copy, cut and paste are the row's too, because
/// a selected badge is not a text selection. It lives in a `RecipientEditorHost`
/// that scrolls it sideways, so long text and the caret never leave the box.
@MainActor
final class RecipientEditorView: NSTextView {
    var onCommand: (@MainActor (Selector) -> Bool)?
    /// The user is about to type, select or click: a selected badge is
    /// deselected first.
    var onWillType: (@MainActor () -> Void)?
    var onTextChanged: (@MainActor () -> Void)?
    var onPaste: (@MainActor (String) -> Void)?
    /// Copy and cut of a selected badge; false when none is selected.
    var onCopy: (@MainActor (_ cut: Bool) -> Bool)?
    var canCopyBadge: (@MainActor () -> Bool)?
    var onResign: (@MainActor () -> Void)?

    private let storage = NSTextStorage()
    /// Of its own, not the window's: the row clears it whenever the model
    /// resets the text, and must not clear anybody else's.
    private let ownUndo = UndoManager()

    init() {
        let layout = NSLayoutManager()
        let container = NSTextContainer(size: NSSize(width: 10_000_000, height: CapsuleStyle.height))
        layout.addTextContainer(container)
        storage.addLayoutManager(layout)
        super.init(frame: NSRect(x: 0, y: 0, width: 80, height: CapsuleStyle.height), textContainer: container)
        container.widthTracksTextView = false
        container.heightTracksTextView = false
        container.lineFragmentPadding = 0
        isRichText = false
        importsGraphics = false
        allowsUndo = true
        drawsBackground = false
        isHorizontallyResizable = true
        isVerticallyResizable = false
        minSize = NSSize(width: 0, height: CapsuleStyle.height)
        maxSize = NSSize(width: 10_000_000, height: CapsuleStyle.height)
        autoresizingMask = [.width]
        focusRingType = .none
        font = Typo.body
        textColor = .labelColor
        isAutomaticQuoteSubstitutionEnabled = false
        isAutomaticDashSubstitutionEnabled = false
        isAutomaticTextReplacementEnabled = false
        isAutomaticSpellingCorrectionEnabled = false
        isAutomaticLinkDetectionEnabled = false
        isAutomaticDataDetectionEnabled = false
        isAutomaticTextCompletionEnabled = false
        isContinuousSpellCheckingEnabled = false
        isGrammarCheckingEnabled = false
        let lineHeight = layout.defaultLineHeight(for: Typo.body)
        textContainerInset = NSSize(width: 0, height: max(0, (CapsuleStyle.height - lineHeight) / 2))
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var undoManager: UndoManager? { ownUndo }

    /// What was typed can no longer be undone: the model has turned it into
    /// badges or replaced it.
    func clearUndo() {
        ownUndo.removeAllActions()
    }

    /// The width the typed text needs.
    var textWidth: CGFloat {
        guard let layoutManager, let textContainer else { return 0 }
        layoutManager.ensureLayout(for: textContainer)
        return ceil(layoutManager.usedRect(for: textContainer).width)
    }

    override func doCommand(by selector: Selector) {
        if onCommand?(selector) == true {
            return
        }
        super.doCommand(by: selector)
    }

    override func insertText(_ string: Any, replacementRange: NSRange) {
        onWillType?()
        super.insertText(string, replacementRange: replacementRange)
    }

    override func setMarkedText(_ string: Any, selectedRange: NSRange, replacementRange: NSRange) {
        onWillType?()
        super.setMarkedText(string, selectedRange: selectedRange, replacementRange: replacementRange)
    }

    override func selectAll(_ sender: Any?) {
        onWillType?()
        super.selectAll(sender)
    }

    override func mouseDown(with event: NSEvent) {
        onWillType?()
        super.mouseDown(with: event)
    }

    override func didChangeText() {
        super.didChangeText()
        onTextChanged?()
    }

    override func paste(_ sender: Any?) {
        guard let text = NSPasteboard.general.string(forType: .string) else { return }
        onPaste?(text)
    }

    override func pasteAsPlainText(_ sender: Any?) {
        paste(sender)
    }

    override func copy(_ sender: Any?) {
        if onCopy?(false) != true {
            super.copy(sender)
        }
    }

    override func cut(_ sender: Any?) {
        if onCopy?(true) != true {
            super.cut(sender)
        }
    }

    override func validateUserInterfaceItem(_ item: any NSValidatedUserInterfaceItem) -> Bool {
        if item.action == #selector(NSText.copy(_:)) || item.action == #selector(NSText.cut(_:)),
           canCopyBadge?() == true {
            return true
        }
        return super.validateUserInterfaceItem(item)
    }

    override func resignFirstResponder() -> Bool {
        let ok = super.resignFirstResponder()
        if ok {
            onResign?()
        }
        return ok
    }
}

/// The box of the inline editor: scrolls it sideways like an NSTextField
/// does, with no scrollers, and hands the wheel to the row.
@MainActor
final class RecipientEditorHost: NSScrollView {
    init(editor: RecipientEditorView) {
        super.init(frame: .zero)
        drawsBackground = false
        contentView.drawsBackground = false
        borderType = .noBorder
        hasHorizontalScroller = false
        hasVerticalScroller = false
        horizontalScrollElasticity = .none
        verticalScrollElasticity = .none
        automaticallyAdjustsContentInsets = false
        documentView = editor
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func scrollWheel(with event: NSEvent) {
        if let outer = superview?.enclosingScrollView {
            outer.scrollWheel(with: event)
        } else {
            super.scrollWheel(with: event)
        }
    }
}

/// The flipped sheet the badges and the editor sit on inside the row's
/// scroll view.
@MainActor
final class RecipientCanvas: NSView {
    override var isFlipped: Bool { true }
}

/// A To, Cc or Bcc row (compose.blp's recipient entry, Outlook-style): the
/// finished addresses as badges with an ×, then an inline editor on the same
/// line; a full row wraps onto the next and the row grows in height, up to
/// four lines, after which it scrolls inside itself. All decisions on when
/// text becomes a badge are `RecipientTokens`'; this view only shows the
/// value and forwards the keys. `stringValue` is the field's value as the
/// row had it as text; `resolved()` is what is sent.
@MainActor
final class RecipientTokenField: NSView {
    static let rowHeight: CGFloat = 30
    /// Lines of badges the row shows before it scrolls.
    static let maxLines = 4
    private static let spacing: CGFloat = 4
    private static let lineSpacing: CGFloat = 4
    private static let minEditorWidth: CGFloat = 80

    /// The typing area, for focus and for the suggestions' anchor.
    let editor = RecipientEditorView()

    /// The text or the badges changed by the user (not by `stringValue`);
    /// only when `stringValue` actually differs from before.
    var onChange: (@MainActor () -> Void)?
    /// A key about to be read by the row: the suggestions panel answers it
    /// first (true = handled).
    var commandInterceptor: (@MainActor (Selector) -> Bool)?
    /// The editor lost the keyboard.
    var onEditingEnded: (@MainActor () -> Void)?

    private let scroller = NSScrollView()
    private let canvas = RecipientCanvas()
    private let editorHost: RecipientEditorHost

    private var model = RecipientTokens()
    private var badges: [RecipientBadgeView] = []
    private var shown: [RecipientTokens.Token] = []
    private var selected: Int?
    private var lastWidth: CGFloat = -1
    /// `model.text` as last set or reported, to tell a real change.
    private var lastText = ""
    private var keepEditorVisible = false

    private enum Caret {
        case keep, start, end
        case at(Int)
    }

    init() {
        editorHost = RecipientEditorHost(editor: editor)
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        setContentHuggingPriority(.defaultLow, for: .horizontal)
        setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        setAccessibilityRole(.group)
        scroller.drawsBackground = false
        scroller.contentView.drawsBackground = false
        scroller.borderType = .noBorder
        scroller.hasVerticalScroller = true
        scroller.hasHorizontalScroller = false
        scroller.autohidesScrollers = true
        scroller.scrollerStyle = .overlay
        scroller.verticalScrollElasticity = .none
        scroller.horizontalScrollElasticity = .none
        scroller.automaticallyAdjustsContentInsets = false
        scroller.documentView = canvas
        addSubview(scroller)
        canvas.addSubview(editorHost)
        editor.onCommand = { [weak self] selector in self?.handle(selector) ?? false }
        editor.onWillType = { [weak self] in self?.select(nil) }
        editor.onTextChanged = { [weak self] in self?.typed() }
        editor.onPaste = { [weak self] text in self?.pasted(text) }
        editor.onCopy = { [weak self] cut in self?.copySelected(cut: cut) ?? false }
        editor.canCopyBadge = { [weak self] in self?.selected != nil }
        editor.onResign = { [weak self] in self?.editingEnded() }
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    // MARK: Value

    /// The row's value as text (`AddressList.format` plus the unfinished
    /// text); setting it makes badges of every address in it, silently.
    var stringValue: String {
        get { model.text }
        set {
            model = RecipientTokens(text: newValue)
            selected = nil
            lastText = model.text
            sync(tokensChanged: true)
        }
    }

    /// The recipients as sent: what the badges show, plus what is still
    /// typed, read the way a commit would. Validity here is the validity the
    /// badges show.
    func resolved() -> (addresses: [Address], invalid: [String]) {
        model.resolved()
    }

    /// The text being typed after the last badge.
    var pending: String { model.pending }

    var hasInvalid: Bool { model.hasInvalid }

    /// A picked suggestion becomes a badge and the typed text goes.
    func add(_ address: Address) {
        model.add(address)
        select(nil)
        sync(tokensChanged: true)
        report()
    }

    /// The area under the editor's line, in the row's coordinates: where the
    /// suggestions panel goes.
    var anchorRect: NSRect {
        let r = convert(editorHost.bounds, from: editorHost)
        return NSRect(x: 0, y: r.minY, width: bounds.width, height: r.height)
    }

    /// The keyboard to the editor.
    func focus() {
        window?.makeFirstResponder(editor)
    }

    override func setAccessibilityLabel(_ label: String?) {
        super.setAccessibilityLabel(label)
        editor.setAccessibilityLabel(label)
    }

    // MARK: Sync

    /// Shows `model`: the badges when the tokens changed (never when only
    /// the pending text did), the editor text when it differs.
    private func sync(tokensChanged: Bool, caret: Caret = .end) {
        if tokensChanged, model.tokens != shown {
            rebuildBadges()
        }
        let text = model.pending
        let length = (text as NSString).length
        if editor.string != text {
            editor.string = text
            editor.clearUndo()
            switch caret {
            case .start: editor.setSelectedRange(NSRange(location: 0, length: 0))
            case .at(let n): editor.setSelectedRange(NSRange(location: min(max(n, 0), length), length: 0))
            case .keep, .end: editor.setSelectedRange(NSRange(location: length, length: 0))
            }
        } else {
            switch caret {
            case .start: editor.setSelectedRange(NSRange(location: 0, length: 0))
            case .at(let n): editor.setSelectedRange(NSRange(location: min(max(n, 0), length), length: 0))
            case .keep, .end: break
            }
        }
        editor.scrollRangeToVisible(editor.selectedRange())
        select(selected.flatMap { $0 < badges.count ? $0 : nil })
        keepEditorVisible = true
        relayout()
    }

    private func rebuildBadges() {
        for b in badges {
            b.removeFromSuperview()
        }
        shown = model.tokens
        badges = shown.enumerated().map { i, token in
            let b = RecipientBadgeView(token: token)
            b.onSelect = { [weak self] in
                self?.focus()
                self?.select(i)
            }
            b.onEdit = { [weak self] in self?.edit(at: i) }
            b.onRemove = { [weak self] in self?.remove(at: i) }
            canvas.addSubview(b, positioned: .below, relativeTo: editorHost)
            return b
        }
    }

    private func relayout() {
        needsLayout = true
        invalidateIntrinsicContentSize()
    }

    /// Tells the owner of a change, but only a change of the value: a
    /// commit that turns `a@b.cz` into a badge leaves the text as it was.
    private func report() {
        let now = model.text
        if now != lastText {
            lastText = now
            onChange?()
        }
    }

    private func select(_ index: Int?) {
        selected = index
        for (i, b) in badges.enumerated() {
            b.isSelected = i == index
        }
    }

    // MARK: Editing

    /// The user changed the text.
    private func typed() {
        guard !editor.hasMarkedText() else { return }
        var text = editor.string
        if text.contains(where: \.isNewline) {
            // A line break is a separator, never part of an address.
            text = String(text.map { $0.isNewline ? "," : $0 })
            editor.string = text
        }
        select(nil)
        let changed = model.setPending(text)
        // What follows a separator typed in the middle is what is left: the
        // caret goes to its start.
        sync(tokensChanged: changed, caret: changed ? .start : .keep)
        report()
    }

    /// Pasted text goes where the selection or the caret is: the text in
    /// front of it, the paste and the text behind it are one input, and the
    /// caret ends up after the paste.
    private func pasted(_ text: String) {
        select(nil)
        let whole = editor.string as NSString
        let range = editor.selectedRange()
        let safe = NSIntersectionRange(range, NSRange(location: 0, length: whole.length))
        let prefix = whole.substring(to: safe.location)
        let suffix = whole.substring(from: safe.location + safe.length)
        model.setPending(prefix)
        model.paste(text)
        var caret = (model.pending as NSString).length
        if !suffix.isEmpty {
            if model.setPending(model.pending + suffix) {
                caret = 0
            }
        }
        sync(tokensChanged: true, caret: .at(caret))
        report()
    }

    private func commit() {
        let changed = model.commit()
        sync(tokensChanged: changed)
        report()
    }

    private func editingEnded() {
        select(nil)
        commit()
        onEditingEnded?()
    }

    private func remove(at index: Int) {
        guard index < model.tokens.count else { return }
        model.remove(at: index)
        selected = nil
        focus()
        sync(tokensChanged: true)
        report()
    }

    private func edit(at index: Int) {
        guard index < model.tokens.count else { return }
        _ = model.edit(at: index)
        selected = nil
        focus()
        sync(tokensChanged: true)
        report()
    }

    /// ⌘C and ⌘X of a selected badge: its full text.
    private func copySelected(cut: Bool) -> Bool {
        guard let i = selected, i < model.tokens.count else { return false }
        let pb = NSPasteboard.general
        pb.clearContents()
        pb.setString(model.tokens[i].tooltip, forType: .string)
        if cut {
            remove(at: i)
        }
        return true
    }
    /// The keys of the row (the suggestions panel has had its say).
    private func handle(_ selector: Selector) -> Bool {
        if commandInterceptor?(selector) == true {
            return true
        }
        let last = model.tokens.count - 1
        switch selector {
        case #selector(NSResponder.insertNewline(_:)):
            commit()
            return true
        case #selector(NSResponder.insertTab(_:)):
            commit()
            window?.selectNextKeyView(editor)
            return true
        case #selector(NSResponder.insertBacktab(_:)):
            commit()
            window?.selectPreviousKeyView(editor)
            return true
        case #selector(NSResponder.deleteBackward(_:)), #selector(NSResponder.deleteForward(_:)):
            if let i = selected {
                remove(at: i)
                return true
            }
            if selector == #selector(NSResponder.deleteBackward(_:)), editor.string.isEmpty, last >= 0 {
                select(last)
                return true
            }
            return false
        case #selector(NSResponder.moveLeft(_:)):
            if let i = selected {
                select(max(i - 1, 0))
                return true
            }
            let r = editor.selectedRange()
            if r.location == 0, r.length == 0, last >= 0 {
                select(last)
                return true
            }
            return false
        case #selector(NSResponder.moveRight(_:)):
            guard let i = selected else { return false }
            if i < last {
                select(i + 1)
            } else {
                select(nil)
                editor.setSelectedRange(NSRange(location: 0, length: 0))
            }
            return true
        default:
            return false
        }
    }

    // MARK: Layout

    override var isFlipped: Bool { true }

    /// The tallest the row gets: `maxLines` lines of badges.
    private static var maxHeight: CGFloat {
        let line = CapsuleStyle.height
        let inset = (rowHeight - line) / 2
        return 2 * inset + CGFloat(maxLines) * line + CGFloat(maxLines - 1) * lineSpacing
    }

    override var intrinsicContentSize: NSSize {
        let content = arrange(width: bounds.width, place: false)
        return NSSize(width: NSView.noIntrinsicMetric, height: min(content, Self.maxHeight))
    }

    override func setFrameSize(_ newSize: NSSize) {
        super.setFrameSize(newSize)
        if newSize.width != lastWidth {
            lastWidth = newSize.width
            relayout()
        }
    }

    override func layout() {
        super.layout()
        let content = arrange(width: bounds.width, place: true)
        scroller.frame = bounds
        canvas.frame = NSRect(x: 0, y: 0, width: bounds.width, height: content)
        if keepEditorVisible {
            keepEditorVisible = false
            canvas.scrollToVisible(editorHost.frame)
        }
    }

    /// Lays the badges and the editor out in lines for `width`, moving them
    /// when `place` is set; the editor takes the rest of the last line, or a
    /// line of its own when less than a usable width is left. Returns the
    /// height of everything, not the capped one.
    @discardableResult
    private func arrange(width: CGFloat, place: Bool) -> CGFloat {
        let line = CapsuleStyle.height
        let inset = (Self.rowHeight - line) / 2
        guard width > 0 else { return Self.rowHeight }
        var x: CGFloat = 0
        var y = inset
        for b in badges {
            let w = min(b.intrinsicContentSize.width, width)
            if x > 0, x + w > width {
                x = 0
                y += line + Self.lineSpacing
            }
            if place {
                b.frame = NSRect(x: x, y: y, width: w, height: line)
            }
            x += w + Self.spacing
        }
        let need = max(Self.minEditorWidth, editor.textWidth + 12)
        if x > 0, x + need > width {
            x = 0
            y += line + Self.lineSpacing
        }
        if place {
            editorHost.frame = NSRect(x: x, y: y, width: max(width - x, 0), height: line)
        }
        return y + line + inset
    }

    // MARK: Mouse

    /// A click on the empty part of the row puts the keyboard in the editor.
    override func mouseDown(with event: NSEvent) {
        select(nil)
        focus()
    }
}
