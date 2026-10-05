// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// A compact, collapsible assistant at the bottom of the compose window.
/// Generated text is previewed and applied only by an explicit button press.
@MainActor
final class ComposeAssistantBar: NSView {
    var onRequest: ((String) -> Void)?
    var onCancel: (() -> Void)?
    var onApply: ((String) -> Void)?
    private let disclosure = NSButton()
    private let contents = FillStackView()
    private let instruction = NSTextField()
    private let write = NSButton()
    private let spinner = NSProgressIndicator()
    private let preview = NSTextView()
    private let previewScroll = NSScrollView()
    private let resultButtons = NSStackView()
    private let apply = NSButton()
    private let discard = NSButton()
    private let error = NSTextField(wrappingLabelWithString: "")
    private var expanded = true
    private var answer = ""

    override init(frame: NSRect) {
        super.init(frame: frame)
        translatesAutoresizingMaskIntoConstraints = false
        wantsLayer = true
        disclosure.title = L10n.T("Assistant") + " · Claude (Anthropic)"
        disclosure.isBordered = false
        disclosure.font = .systemFont(ofSize: 13, weight: .semibold)
        disclosure.imagePosition = .imageLeading
        disclosure.target = self
        disclosure.action = #selector(toggle(_:))
        instruction.placeholderString = "What should the message say? (optional)" // macOS-only string
        instruction.setAccessibilityLabel(instruction.placeholderString)
        instruction.usesSingleLineMode = true
        instruction.cell?.isScrollable = true
        instruction.target = self
        instruction.action = #selector(request(_:))
        instruction.setContentHuggingPriority(.defaultLow, for: .horizontal)
        instruction.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        write.title = "Write a Reply" // macOS-only string
        write.bezelStyle = .rounded
        write.target = self
        write.action = #selector(request(_:))
        spinner.style = .spinning
        spinner.controlSize = .small
        spinner.isDisplayedWhenStopped = false
        let entry = NSStackView(views: [write, instruction, spinner])
        entry.spacing = 8
        contents.spacing = 8
        contents.addArrangedSubview(entry)
        preview.isEditable = false
        preview.isRichText = false
        preview.font = .systemFont(ofSize: 13)
        preview.textColor = .labelColor
        preview.backgroundColor = .textBackgroundColor
        preview.isVerticallyResizable = true
        preview.isHorizontallyResizable = false
        preview.autoresizingMask = [.width]
        preview.textContainer?.widthTracksTextView = true
        preview.textContainerInset = NSSize(width: 6, height: 6)
        previewScroll.documentView = preview
        previewScroll.hasVerticalScroller = true
        previewScroll.autohidesScrollers = true
        previewScroll.borderType = .bezelBorder
        previewScroll.heightAnchor.constraint(equalToConstant: 100).isActive = true
        contents.addArrangedSubview(previewScroll)
        error.font = .systemFont(ofSize: 12)
        error.textColor = .systemRed
        contents.addArrangedSubview(error)
        apply.title = L10n.T("Replace")
        apply.bezelStyle = .rounded
        apply.target = self
        apply.action = #selector(replace(_:))
        discard.title = mn(L10n.T("_Discard"))
        discard.bezelStyle = .rounded
        discard.target = self
        discard.action = #selector(cancel(_:))
        resultButtons.addArrangedSubview(discard)
        resultButtons.addArrangedSubview(apply)
        contents.addArrangedSubview(resultButtons)
        let stack = FillStackView(fillingViews: [disclosure, contents])
        stack.spacing = 8
        stack.edgeInsets = NSEdgeInsets(top: 10, left: 12, bottom: 10, right: 12)
        addSubview(stack)
        NSLayoutConstraint.activate([
            stack.topAnchor.constraint(equalTo: topAnchor), stack.bottomAnchor.constraint(equalTo: bottomAnchor),
            stack.leadingAnchor.constraint(equalTo: leadingAnchor), stack.trailingAnchor.constraint(equalTo: trailingAnchor),
        ])
        renderDisclosure()
        show(text: "", running: false)
    }

    convenience init() { self.init(frame: .zero) }
    @available(*, unavailable)
    required init?(coder: NSCoder) { fatalError("not used") }

    override var wantsUpdateLayer: Bool { true }
    override func updateLayer() { layer?.backgroundColor = NSColor.controlAccentColor.withAlphaComponent(0.06).cgColor }
    override func viewDidChangeEffectiveAppearance() { super.viewDidChangeEffectiveAppearance(); needsDisplay = true }

    func setMode(_ kind: ComposeKind) {
        if kind == .forward { write.title = "Write an Introduction" } // macOS-only string
        if kind == .new || kind == .edit { write.title = "Write a Message" } // macOS-only string
    }

    func show(text: String, running: Bool, failure: String = "") {
        answer = running ? "" : text
        preview.string = text
        previewScroll.isHidden = text.isEmpty
        error.stringValue = failure
        error.isHidden = failure.isEmpty
        resultButtons.isHidden = !running && text.isEmpty
        apply.isEnabled = !running && !text.isEmpty
        discard.title = running ? L10n.T("Cancel") : mn(L10n.T("_Discard"))
        instruction.isEnabled = !running
        write.isEnabled = !running
        if running { spinner.startAnimation(nil) } else { spinner.stopAnimation(nil) }
    }

    @objc private func toggle(_ sender: Any?) { expanded.toggle(); renderDisclosure() }
    private func renderDisclosure() {
        contents.isHidden = !expanded
        disclosure.image = NSImage(systemSymbolName: expanded ? "chevron.down" : "chevron.right", accessibilityDescription: nil)
        disclosure.setAccessibilityValue(expanded ? L10n.T("Expanded") : L10n.T("Collapsed"))
    }
    @objc private func request(_ sender: Any?) { if write.isEnabled { onRequest?(instruction.stringValue) } }
    @objc private func cancel(_ sender: Any?) { onCancel?(); show(text: "", running: false) }
    @objc private func replace(_ sender: Any?) { if !answer.isEmpty { onApply?(answer); show(text: "", running: false) } }
}
