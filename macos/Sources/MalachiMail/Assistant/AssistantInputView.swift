// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The question field of the assistant panel (ui/internal/assistant, the
/// In App target; GTK assistant_panel.blp `assistant_input`): plain text in
/// a rounded box that grows from one line to five and scrolls beyond.
/// Return sends (`onSubmit`), Shift-Return (and Option-Return) starts a new
/// line, Escape drops a waiting message action (`onCancel`). The
/// placeholder is a label over the empty field. Nothing typed here is
/// formatted: no rich text, no pictures, no data detectors.
@MainActor
final class AssistantInputView: NSView, NSTextViewDelegate {
    static let maxLines = 5
    static var fontSize: CGFloat { Typo.bodySize }
    static let textInset = NSSize(width: 4, height: 5)
    static let cornerRadius: CGFloat = 8

    let textView = NSTextView()
    private let scroll = NSScrollView()
    private let placeholder = NSTextField(labelWithString: "")
    private var heightConstraint: NSLayoutConstraint?
    private let lineHeight: CGFloat

    /// Return in the field.
    var onSubmit: (@MainActor () -> Void)?
    /// Escape in the field.
    var onCancel: (@MainActor () -> Void)?
    /// The text changed (typing, pasting, `text` set).
    var onTextChange: (@MainActor () -> Void)?

    /// The field's text.
    var text: String {
        get { textView.string }
        set {
            textView.string = newValue
            textChanged()
        }
    }

    /// The field's placeholder.
    var placeholderText: String {
        get { placeholder.stringValue }
        set { placeholder.stringValue = newValue }
    }

    init() {
        let font = NSFont.systemFont(ofSize: Self.fontSize)
        lineHeight = ceil(NSLayoutManager().defaultLineHeight(for: font))
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        wantsLayer = true
        layer?.cornerRadius = Self.cornerRadius
        layer?.cornerCurve = .continuous
        layer?.borderWidth = 1

        textView.font = font
        textView.textColor = .labelColor
        textView.isRichText = false
        textView.importsGraphics = false
        textView.allowsUndo = true
        textView.usesFontPanel = false
        textView.usesFindBar = false
        textView.isAutomaticLinkDetectionEnabled = false
        textView.isAutomaticDataDetectionEnabled = false
        textView.isAutomaticQuoteSubstitutionEnabled = false
        textView.isAutomaticDashSubstitutionEnabled = false
        textView.drawsBackground = false
        textView.textContainerInset = Self.textInset
        textView.isVerticallyResizable = true
        textView.isHorizontallyResizable = false
        textView.autoresizingMask = [.width]
        textView.minSize = .zero
        textView.maxSize = NSSize(width: CGFloat.greatestFiniteMagnitude, height: CGFloat.greatestFiniteMagnitude)
        textView.textContainer?.widthTracksTextView = true
        textView.textContainer?.lineFragmentPadding = 2
        textView.delegate = self

        scroll.translatesAutoresizingMaskIntoConstraints = false
        scroll.hasVerticalScroller = true
        scroll.hasHorizontalScroller = false
        scroll.autohidesScrollers = true
        scroll.drawsBackground = false
        scroll.borderType = .noBorder
        scroll.documentView = textView

        placeholder.translatesAutoresizingMaskIntoConstraints = false
        placeholder.font = font
        placeholder.textColor = .placeholderTextColor
        placeholder.lineBreakMode = .byTruncatingTail
        placeholder.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        // The field itself carries the placeholder for VoiceOver.
        placeholder.setAccessibilityElement(false)

        addSubview(scroll)
        addSubview(placeholder)
        let height = scroll.heightAnchor.constraint(equalToConstant: minimumHeight)
        heightConstraint = height
        NSLayoutConstraint.activate([
            scroll.topAnchor.constraint(equalTo: topAnchor, constant: 1),
            scroll.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -1),
            scroll.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 1),
            scroll.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -1),
            height,
            placeholder.leadingAnchor.constraint(
                equalTo: leadingAnchor, constant: 1 + Self.textInset.width + (textView.textContainer?.lineFragmentPadding ?? 0)),
            placeholder.trailingAnchor.constraint(lessThanOrEqualTo: trailingAnchor, constant: -8),
            placeholder.topAnchor.constraint(equalTo: topAnchor, constant: 1 + Self.textInset.height),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var wantsUpdateLayer: Bool { true }

    override func updateLayer() {
        layer?.backgroundColor = NSColor.textBackgroundColor.cgColor
        layer?.borderColor = NSColor.separatorColor.cgColor
    }

    /// Gives the field the keyboard.
    func focus() {
        window?.makeFirstResponder(textView)
    }

    /// A click anywhere in the box (its padding included) goes to the text.
    override func mouseDown(with event: NSEvent) {
        focus()
    }

    // MARK: Height

    private var minimumHeight: CGFloat {
        lineHeight + 2 * Self.textInset.height
    }

    private var maximumHeight: CGFloat {
        CGFloat(Self.maxLines) * lineHeight + 2 * Self.textInset.height
    }

    /// One to five lines of the text, then it scrolls.
    private func updateHeight() {
        guard let layout = textView.layoutManager, let container = textView.textContainer else { return }
        layout.ensureLayout(for: container)
        let used = layout.usedRect(for: container).height + 2 * Self.textInset.height
        let h = min(max(ceil(used), minimumHeight), maximumHeight)
        if heightConstraint?.constant != h {
            heightConstraint?.constant = h
        }
    }

    override func layout() {
        super.layout()
        updateHeight()
    }

    private func textChanged() {
        placeholder.isHidden = !textView.string.isEmpty
        updateHeight()
        onTextChange?()
    }

    // MARK: NSTextViewDelegate

    func textDidChange(_ notification: Foundation.Notification) {
        textChanged()
    }

    /// Return sends; Shift-Return is a new line (Option-Return is one
    /// already, `insertNewlineIgnoringFieldEditor:`); Escape drops the
    /// waiting action. A composition in progress (marked text) keeps its
    /// Return: the input method commits it before any command arrives.
    func textView(_ textView: NSTextView, doCommandBy commandSelector: Selector) -> Bool {
        switch commandSelector {
        case #selector(NSResponder.insertNewline(_:)):
            if NSApp.currentEvent?.modifierFlags.contains(.shift) == true {
                textView.insertNewlineIgnoringFieldEditor(nil)
            } else {
                onSubmit?()
            }
            return true
        case #selector(NSResponder.cancelOperation(_:)):
            onCancel?()
            return true
        default:
            return false
        }
    }
}
