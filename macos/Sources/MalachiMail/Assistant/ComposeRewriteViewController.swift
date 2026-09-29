// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The compose window's rewrite (ui/internal/assistant rewrite.go, the In
/// App target; there is no Blueprint yet, the GTK widgets follow this
/// port): the popover of the toolbar's Assistant button. It renders a
/// `ComposeRewriteController` for one passage (`RewriteTarget`):
///
/// - the title: "Rewrite Selection" or "Rewrite Your Text";
/// - the presets (`Assistant.rewrites`, `Assistant.rewriteLabel`) and a
///   field for the user's own instruction, which Return sends;
/// - while the answer arrives, a spinner with "Rewriting…" and the text so
///   far; then the answer, or what went wrong;
/// - Discard (closes), Insert Below and Replace (the default button, Return
///   unless the instruction field holds words to send), both only with an
///   answer.
///
/// The passage is never shown here; the answer is plain text in a
/// non-editable text view (`string`, CLAUDE.md rule 3). What the buttons
/// do with it is the window's (`onApply`, `onClose`).
@MainActor
final class ComposeRewriteViewController: NSViewController, NSTextFieldDelegate {
    static let width: CGFloat = 380

    let controller: ComposeRewriteController
    let target: RewriteTarget
    /// Replace (`below` false) or Insert Below with the answer.
    var onApply: (@MainActor (_ text: String, _ below: Bool) -> Void)?
    /// Discard.
    var onClose: (@MainActor () -> Void)?

    private let titleLabel = PrefsWrappingLabel("", size: 13, weight: .semibold)
    private let presets = FlowView(spacing: 6, lineSpacing: 6)
    private var presetButtons: [NSButton] = []
    private let customField = NSTextField()
    private let spinner = NSProgressIndicator()
    private let statusLabel = NSTextField(labelWithString: "")
    private let statusRow = NSStackView()
    private let resultScroll = NSScrollView()
    private let resultView = RewriteResultTextView()
    private let errorLabel = PrefsWrappingLabel("", size: 12, color: .systemRed)
    private let discardButton = NSButton()
    private let insertButton = NSButton()
    private let replaceButton = RewriteDefaultButton()

    init(controller: ComposeRewriteController, target: RewriteTarget) {
        self.controller = controller
        self.target = target
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Whether there is anything to rewrite.
    private var hasPassage: Bool {
        !target.text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    // MARK: View

    override func loadView() {
        let t = Assistant.composeTexts()
        titleLabel.stringValue = target.selected ? t.rewriteSelection : t.rewriteText

        for r in Assistant.rewrites {
            let b = NSButton(title: Assistant.rewriteLabel(r), target: self, action: #selector(preset(_:)))
            b.bezelStyle = .push
            b.controlSize = .small
            b.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
            b.tag = Assistant.rewrites.firstIndex(of: r) ?? -1
            presetButtons.append(b)
            presets.addView(b)
        }

        customField.placeholderString = t.custom
        customField.translatesAutoresizingMaskIntoConstraints = false
        customField.lineBreakMode = .byTruncatingTail
        customField.usesSingleLineMode = true
        customField.cell?.isScrollable = true
        customField.delegate = self
        customField.setAccessibilityLabel(t.custom)

        spinner.style = .spinning
        spinner.controlSize = .small
        spinner.isDisplayedWhenStopped = false
        statusLabel.stringValue = t.rewriting
        statusLabel.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
        statusLabel.textColor = .secondaryLabelColor
        statusRow.orientation = .horizontal
        statusRow.spacing = 6
        statusRow.addArrangedSubview(spinner)
        statusRow.addArrangedSubview(statusLabel)

        resultScroll.translatesAutoresizingMaskIntoConstraints = false
        resultScroll.hasVerticalScroller = true
        resultScroll.autohidesScrollers = true
        resultScroll.borderType = .bezelBorder
        resultScroll.drawsBackground = true
        // The text view follows the clip view's width from here on
        // (autoresizing), so it starts at the scroll view's content size.
        resultView.frame = NSRect(origin: .zero, size: resultScroll.contentSize)
        resultScroll.documentView = resultView
        resultScroll.backgroundColor = .textBackgroundColor

        for (b, title, action) in [
            (discardButton, mn(t.discard), #selector(discard(_:))),
            (insertButton, t.insertBelow, #selector(insertBelow(_:))),
            (replaceButton as NSButton, t.replace, #selector(replace(_:))),
        ] {
            b.title = title
            b.bezelStyle = .push
            b.target = self
            b.action = action
        }
        discardButton.keyEquivalent = "\u{1b}"
        replaceButton.keyEquivalent = "\r"
        replaceButton.yields = { [weak self] in
            // Return in an instruction field with words sends them.
            guard let self, let editor = self.customField.currentEditor() else { return false }
            return editor.window?.firstResponder === editor && !self.customField.stringValue.isEmpty
        }
        let buttons = NSStackView()
        buttons.orientation = .horizontal
        buttons.spacing = 8
        buttons.addView(discardButton, in: .leading)
        buttons.addView(insertButton, in: .trailing)
        buttons.addView(replaceButton, in: .trailing)
        buttons.setHuggingPriority(.defaultLow, for: .horizontal)

        let stack = FillStackView(fillingViews: [
            titleLabel, presets, customField, statusRow, resultScroll, errorLabel, buttons,
        ])
        stack.spacing = 10
        stack.edgeInsets = NSEdgeInsets(top: 14, left: 14, bottom: 14, right: 14)
        stack.setCustomSpacing(8, after: presets)

        let root = NSView()
        root.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.topAnchor.constraint(equalTo: root.topAnchor),
            stack.bottomAnchor.constraint(equalTo: root.bottomAnchor),
            stack.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: root.trailingAnchor),
            root.widthAnchor.constraint(equalToConstant: Self.width),
            resultScroll.heightAnchor.constraint(equalToConstant: 140),
        ])
        view = root
        controller.onState = { [weak self] _ in
            self?.render()
        }
        render()
    }

    override func viewDidAppear() {
        super.viewDidAppear()
        if hasPassage {
            view.window?.makeFirstResponder(customField)
        }
    }

    // MARK: State

    private func render() {
        let state = controller.state
        let running = controller.running
        for b in presetButtons {
            b.isEnabled = hasPassage && !running
        }
        customField.isEnabled = hasPassage && !running
        statusRow.isHidden = !running
        if running {
            spinner.startAnimation(nil)
        } else {
            spinner.stopAnimation(nil)
        }
        var text = ""
        var answer = false
        errorLabel.stringValue = ""
        switch state {
        case .idle:
            break
        case .running(let preview):
            text = preview
        case .done(let t):
            text = t
            answer = true
        case .failed(let message):
            errorLabel.stringValue = message
        }
        resultScroll.isHidden = text.isEmpty
        if resultView.string != text {
            resultView.string = text
            resultView.scrollToEndOfDocument(nil)
            if answer {
                resultView.scrollToBeginningOfDocument(nil)
            }
        }
        errorLabel.isHidden = errorLabel.stringValue.isEmpty
        insertButton.isEnabled = answer
        replaceButton.isEnabled = answer
    }

    // MARK: Actions

    @objc private func preset(_ sender: NSButton) {
        guard Assistant.rewrites.indices.contains(sender.tag) else { return }
        controller.start(rewrite: Assistant.rewrites[sender.tag], custom: "", passage: target.text)
    }

    /// Return in the instruction field sends it.
    func control(_ control: NSControl, textView: NSTextView, doCommandBy commandSelector: Selector) -> Bool {
        guard control === customField, commandSelector == #selector(NSResponder.insertNewline(_:)) else { return false }
        let words = customField.stringValue
        guard !controller.running, !words.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return true }
        controller.start(rewrite: .custom, custom: words, passage: target.text)
        return true
    }

    @objc private func discard(_ sender: Any?) {
        onClose?()
    }

    @objc private func insertBelow(_ sender: Any?) {
        guard case .done(let text) = controller.state else { return }
        onApply?(text, true)
    }

    @objc private func replace(_ sender: Any?) {
        guard case .done(let text) = controller.state else { return }
        onApply?(text, false)
    }
}

/// Replace: the popover's default button, which leaves Return to the
/// instruction field while that holds words to send (`yields`).
@MainActor
final class RewriteDefaultButton: NSButton {
    var yields: (@MainActor () -> Bool)?

    override func performKeyEquivalent(with event: NSEvent) -> Bool {
        if yields?() == true {
            return false
        }
        return super.performKeyEquivalent(with: event)
    }
}

/// The answer: plain text, selectable and inert like the plain-text body
/// of a message (`MessageBodyTextView`: no editing, no rich paste, no data
/// detectors, Copy and Select All only, no Services, no Look Up), in a
/// scroll view.
@MainActor
final class RewriteResultTextView: NSTextView {
    init() {
        let storage = NSTextStorage()
        let layout = NSLayoutManager()
        let container = NSTextContainer(size: NSSize(width: 0, height: CGFloat.greatestFiniteMagnitude))
        container.widthTracksTextView = true
        container.heightTracksTextView = false
        layout.addTextContainer(container)
        storage.addLayoutManager(layout)
        super.init(frame: .zero, textContainer: container)
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
        font = .systemFont(ofSize: NSFont.systemFontSize)
        textColor = .labelColor
        drawsBackground = true
        backgroundColor = .textBackgroundColor
        textContainerInset = NSSize(width: 4, height: 6)
        isVerticallyResizable = true
        isHorizontallyResizable = false
        autoresizingMask = [.width]
        minSize = .zero
        maxSize = NSSize(width: CGFloat.greatestFiniteMagnitude, height: CGFloat.greatestFiniteMagnitude)
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
