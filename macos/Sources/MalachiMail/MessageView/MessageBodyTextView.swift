// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// The plain-text body of a message (window.blp `message_body`): a
/// non-editable, selectable text view that wraps at the width it is given
/// and reports the height of its text as its intrinsic size, so it lives in
/// a clamp inside a scroll view like the GTK label does. Plain text only:
/// the daemon's text is shown as it is, nothing in it is interpreted, and
/// the system's text services stay out of it (`plainTextContextMenu`,
/// `validRequestor`, `quickLook`): a selection of mail text is never
/// handed to another program or a web lookup by a path that bypasses the
/// reader's own link handling (`allowedLink`, the masked-link alert).
@MainActor
final class MessageBodyTextView: NSTextView {
    /// The GTK margins: 24 at the sides, 12 above, 24 below.
    static let horizontalInset: CGFloat = 24
    static let verticalInset: CGFloat = 12
    static let extraBottom: CGFloat = 12

    init() {
        // TextKit 1 explicitly: the intrinsic height comes from the layout
        // manager's used rect.
        let storage = NSTextStorage()
        let layout = NSLayoutManager()
        let container = NSTextContainer(size: NSSize(width: 0, height: CGFloat.greatestFiniteMagnitude))
        container.widthTracksTextView = true
        container.heightTracksTextView = false
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
        textColor = .labelColor
        textContainerInset = NSSize(width: Self.horizontalInset, height: Self.verticalInset)
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

    /// Replaces the text. Nothing in `text` is interpreted.
    func setText(_ text: String) {
        string = text
        if let font {
            applyFont(font)
        }
        invalidateIntrinsicContentSize()
    }

    /// Sets the font of the whole text (the text-zoom and monospace settings).
    func applyFont(_ f: NSFont) {
        font = f
        if let storage = textStorage, storage.length > 0 {
            storage.addAttribute(.font, value: f, range: NSRange(location: 0, length: storage.length))
        }
        invalidateIntrinsicContentSize()
    }

    override var intrinsicContentSize: NSSize {
        guard let layoutManager, let textContainer else {
            return super.intrinsicContentSize
        }
        layoutManager.ensureLayout(for: textContainer)
        let used = layoutManager.usedRect(for: textContainer)
        return NSSize(
            width: NSView.noIntrinsicMetric,
            height: ceil(used.height) + textContainerInset.height * 2 + Self.extraBottom)
    }

    override func setFrameSize(_ newSize: NSSize) {
        let changed = newSize.width != frame.width
        super.setFrameSize(newSize)
        if changed {
            invalidateIntrinsicContentSize()
        }
    }

    // MARK: Text services

    /// Copy and Select All only: not Look Up, Search With…, Translate,
    /// Share or Services, which would act on mail text outside the
    /// reader's control.
    override func menu(for event: NSEvent) -> NSMenu? {
        plainTextContextMenu()
    }

    /// No Services on the selection (the Services menu would offer "Open
    /// URL" and the like for a selected address).
    override func validRequestor(forSendType sendType: NSPasteboard.PasteboardType?, returnType: NSPasteboard.PasteboardType?) -> Any? {
        nil
    }

    /// No Look Up preview (force click, three-finger tap): it renders web
    /// content for the selection.
    override func quickLook(with event: NSEvent) {}
}

/// The context menu of inert mail text: Copy and Select All, and nothing
/// that hands the text to another program. The two titles are AppKit's
/// own words for the standard actions; the main menu carries the same
/// items with their key equivalents.
@MainActor
func plainTextContextMenu() -> NSMenu {
    let menu = NSMenu()
    menu.addItem(NSMenuItem(title: "Copy", action: #selector(NSText.copy(_:)), keyEquivalent: "")) // macOS-only string
    menu.addItem(NSMenuItem(title: "Select All", action: #selector(NSText.selectAll(_:)), keyEquivalent: "")) // macOS-only string
    return menu
}

/// A field editor for selectable labels showing mail text
/// (`MessageHeaderView`): the same three refusals as
/// `MessageBodyTextView`, since a label's selection lives in its field
/// editor, which is otherwise the window's default one with the full
/// text-services menu.
@MainActor
final class PlainFieldEditor: NSTextView {
    convenience init() {
        // nil lets NSTextView build its own text storage, as init(frame:) does.
        self.init(frame: .zero, textContainer: nil)
    }

    /// NSTextView's designated initializer; `init(frame:)` funnels here.
    override init(frame frameRect: NSRect, textContainer container: NSTextContainer?) {
        super.init(frame: frameRect, textContainer: container)
        isFieldEditor = true
        isRichText = false
        importsGraphics = false
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
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func menu(for event: NSEvent) -> NSMenu? {
        plainTextContextMenu()
    }

    override func validRequestor(forSendType sendType: NSPasteboard.PasteboardType?, returnType: NSPasteboard.PasteboardType?) -> Any? {
        nil
    }

    override func quickLook(with event: NSEvent) {}
}
