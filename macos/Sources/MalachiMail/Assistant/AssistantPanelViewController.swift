// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The assistant panel of the main window (ui/internal/assistant, the In
/// App target; GTK assistant_panel.blp and window/assistant_panel.go follow
/// this port), the inspector of `MainSplitViewController`. It renders an
/// `AssistantPanelController` and sends the clicks back:
///
/// - the header: "Assistant", the subtitle (Claude Code · the model) and
///   New Conversation;
/// - the context chip: the selected message, the selected conversation or
///   all mail, with a button that leaves the selection out until it
///   changes; once the conversation's first question was asked, what the
///   conversation is about (its subject, or the number of its messages),
///   without the button;
/// - the quick actions: Summarize, Draft a Reply…, Tasks and Deadlines;
/// - the bar "Another message is selected" (`AssistantSelectionBar`) while
///   the conversation keeps its context and the list's selection is not
///   part of it: New Conversation, Add to Conversation;
/// - the transcript (`AssistantTranscriptView`);
/// - the waiting action's label over the question field
///   (`AssistantInputView`), Send or Stop beside it, and the line that
///   says where the mail goes.
///
/// Everything the model or mail wrote reaches the screen as plain text
/// (`stringValue`, `NSTextView.string`) or as the attributed text the
/// transcript builds from `Assistant.markdown` with fonts only (CLAUDE.md
/// rule 3). A link in an answer goes to `onLink`, which the application
/// routes through the actions' confirmation.
@MainActor
final class AssistantPanelViewController: NSViewController {
    static let inset: CGFloat = 12

    let controller: AssistantPanelController
    /// A link in an answer was clicked; `window` is the panel's.
    var onLink: (@MainActor (_ href: String, _ window: NSWindow?) -> Void)?

    private let titleLabel = PrefsWrappingLabel("", size: 13, weight: .semibold)
    private let subtitleLabel = PrefsWrappingLabel("", size: 11, color: .secondaryLabelColor)
    private let newButton = NSButton()
    private let chip = AssistantContextChip()
    private let actionsFlow = FlowView(spacing: 6, lineSpacing: 6)
    private var actionButtons: [NSButton] = []
    private let selectionBar = AssistantSelectionBar()
    private let transcript = AssistantTranscriptView()
    private let pendingRow = NSView()
    private let pendingLabel = NSTextField(labelWithString: "")
    private let pendingCancel = NSButton()
    let input = AssistantInputView()
    private let sendButton = NSButton()
    private let footer = assistantLabel(size: 11, color: .secondaryLabelColor)

    /// The quick actions, in the order of the row.
    static let quickActions: [Assistant.Action] = [.summarize, .draftReply, .tasks]

    init(controller: AssistantPanelController) {
        self.controller = controller
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    // MARK: View

    override func loadView() {
        let texts = Assistant.panelTexts()
        let root = NSView()

        // The header: the title and the subtitle, New Conversation at the
        // end. The titles take all the width beside the button and wrap
        // rather than truncate when the panel is narrow.
        titleLabel.stringValue = Assistant.texts().assistant
        let titles = FillStackView(fillingViews: [titleLabel, subtitleLabel])
        titles.spacing = 1

        newButton.image = Icon.symbol("square.and.pencil", size: .regular, description: texts.newConversation)
        newButton.imagePosition = .imageOnly
        newButton.bezelStyle = .accessoryBarAction
        newButton.isBordered = false
        newButton.toolTip = texts.newConversation
        newButton.setAccessibilityLabel(texts.newConversation)
        newButton.target = self
        newButton.action = #selector(newConversation(_:))
        newButton.translatesAutoresizingMaskIntoConstraints = false
        newButton.setContentHuggingPriority(.required, for: .horizontal)
        newButton.setContentCompressionResistancePriority(.required, for: .horizontal)

        let header = NSView()
        header.translatesAutoresizingMaskIntoConstraints = false
        header.addSubview(titles)
        header.addSubview(newButton)
        NSLayoutConstraint.activate([
            titles.topAnchor.constraint(equalTo: header.topAnchor),
            titles.bottomAnchor.constraint(equalTo: header.bottomAnchor),
            titles.leadingAnchor.constraint(equalTo: header.leadingAnchor),
            titles.trailingAnchor.constraint(equalTo: newButton.leadingAnchor, constant: -8),
            newButton.trailingAnchor.constraint(equalTo: header.trailingAnchor),
            newButton.centerYAnchor.constraint(equalTo: header.centerYAnchor),
            newButton.topAnchor.constraint(greaterThanOrEqualTo: header.topAnchor),
            header.bottomAnchor.constraint(greaterThanOrEqualTo: newButton.bottomAnchor),
        ])

        // The context chip, at its own width.
        let chipRow = NSView()
        chipRow.translatesAutoresizingMaskIntoConstraints = false
        chipRow.addSubview(chip)
        NSLayoutConstraint.activate([
            chip.topAnchor.constraint(equalTo: chipRow.topAnchor),
            chip.bottomAnchor.constraint(equalTo: chipRow.bottomAnchor),
            chip.leadingAnchor.constraint(equalTo: chipRow.leadingAnchor),
            chip.trailingAnchor.constraint(lessThanOrEqualTo: chipRow.trailingAnchor),
        ])
        chip.onRemove = { [weak self] in
            self?.controller.removeContext()
        }

        // The quick actions, wrapping when the panel is narrow.
        for a in Self.quickActions {
            let b = NSButton(title: Assistant.label(a), target: self, action: #selector(quickAction(_:)))
            b.bezelStyle = .push
            b.controlSize = .small
            b.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
            b.tag = Assistant.messageActions.firstIndex(of: a) ?? -1
            actionButtons.append(b)
            actionsFlow.addView(b)
        }

        // The bar while the selection is not what the conversation is about.
        selectionBar.onNewConversation = { [weak self] in
            self?.controller.newConversation()
        }
        selectionBar.onAdd = { [weak self] in
            self?.controller.addSelection()
        }
        selectionBar.isHidden = true

        let top = FillStackView(fillingViews: [header, chipRow, actionsFlow, selectionBar])
        top.spacing = 8
        top.edgeInsets = NSEdgeInsets(top: 10, left: Self.inset, bottom: 10, right: Self.inset)

        // The transcript.
        transcript.makeView = { [weak self] item in
            self?.makeItemView(item) ?? AssistantMessageLineView(item.content, retry: {})
        }
        transcript.setContentHuggingPriority(.defaultLow, for: .vertical)
        transcript.setContentCompressionResistancePriority(.defaultLow, for: .vertical)

        // The waiting action over the field, with a button that drops it.
        pendingLabel.font = .systemFont(ofSize: 11, weight: .medium)
        pendingLabel.textColor = .secondaryLabelColor
        pendingLabel.lineBreakMode = .byTruncatingTail
        pendingLabel.translatesAutoresizingMaskIntoConstraints = false
        pendingLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        let cancel = mn(texts.cancel)
        pendingCancel.image = Icon.symbol("xmark.circle.fill", size: .small, description: cancel)
        pendingCancel.imagePosition = .imageOnly
        pendingCancel.isBordered = false
        pendingCancel.contentTintColor = .tertiaryLabelColor
        pendingCancel.toolTip = cancel
        pendingCancel.setAccessibilityLabel(cancel)
        pendingCancel.target = self
        pendingCancel.action = #selector(cancelPending(_:))
        pendingCancel.translatesAutoresizingMaskIntoConstraints = false
        pendingRow.translatesAutoresizingMaskIntoConstraints = false
        pendingRow.addSubview(pendingLabel)
        pendingRow.addSubview(pendingCancel)
        NSLayoutConstraint.activate([
            pendingLabel.leadingAnchor.constraint(equalTo: pendingRow.leadingAnchor, constant: 2),
            pendingLabel.topAnchor.constraint(equalTo: pendingRow.topAnchor),
            pendingLabel.bottomAnchor.constraint(equalTo: pendingRow.bottomAnchor),
            pendingCancel.leadingAnchor.constraint(equalTo: pendingLabel.trailingAnchor, constant: 4),
            pendingCancel.trailingAnchor.constraint(lessThanOrEqualTo: pendingRow.trailingAnchor),
            pendingCancel.centerYAnchor.constraint(equalTo: pendingLabel.centerYAnchor),
        ])

        // The field and Send or Stop.
        input.onSubmit = { [weak self] in self?.send() }
        input.onCancel = { [weak self] in self?.controller.cancelPending() }
        input.onTextChange = { [weak self] in self?.updateSendButton() }
        sendButton.bezelStyle = .push
        sendButton.target = self
        sendButton.action = #selector(sendOrStop(_:))
        sendButton.translatesAutoresizingMaskIntoConstraints = false
        sendButton.setContentHuggingPriority(.required, for: .horizontal)
        sendButton.setContentCompressionResistancePriority(.required, for: .horizontal)
        let inputRow = NSView()
        inputRow.translatesAutoresizingMaskIntoConstraints = false
        inputRow.addSubview(input)
        inputRow.addSubview(sendButton)
        NSLayoutConstraint.activate([
            input.topAnchor.constraint(equalTo: inputRow.topAnchor),
            input.bottomAnchor.constraint(equalTo: inputRow.bottomAnchor),
            input.leadingAnchor.constraint(equalTo: inputRow.leadingAnchor),
            sendButton.leadingAnchor.constraint(equalTo: input.trailingAnchor, constant: 6),
            sendButton.trailingAnchor.constraint(equalTo: inputRow.trailingAnchor),
            sendButton.bottomAnchor.constraint(equalTo: input.bottomAnchor),
        ])

        footer.stringValue = texts.footer

        let bottom = FillStackView(fillingViews: [pendingRow, inputRow, footer])
        bottom.spacing = 6
        bottom.edgeInsets = NSEdgeInsets(top: 8, left: Self.inset, bottom: 10, right: Self.inset)

        let topRule = NSBox()
        topRule.boxType = .separator
        let bottomRule = NSBox()
        bottomRule.boxType = .separator
        for v in [top, topRule, transcript, bottomRule, bottom] as [NSView] {
            v.translatesAutoresizingMaskIntoConstraints = false
            root.addSubview(v)
            NSLayoutConstraint.activate([
                v.leadingAnchor.constraint(equalTo: root.leadingAnchor),
                v.trailingAnchor.constraint(equalTo: root.trailingAnchor),
            ])
        }
        // Below the unified toolbar, whether AppKit lays the inspector out
        // under it or not.
        NSLayoutConstraint.activate([
            top.topAnchor.constraint(equalTo: root.safeAreaLayoutGuide.topAnchor),
            topRule.topAnchor.constraint(equalTo: top.bottomAnchor),
            transcript.topAnchor.constraint(equalTo: topRule.bottomAnchor),
            bottomRule.topAnchor.constraint(equalTo: transcript.bottomAnchor),
            bottom.topAnchor.constraint(equalTo: bottomRule.bottomAnchor),
            bottom.bottomAnchor.constraint(equalTo: root.bottomAnchor),
        ])
        view = root
    }

    override func viewDidLoad() {
        super.viewDidLoad()
        controller.onChange = { [weak self] change in
            guard let self else { return }
            self.transcript.apply(change, items: self.controller.items)
            self.updateState()
        }
        controller.onState = { [weak self] in
            self?.updateState()
        }
        controller.onFocusInput = { [weak self] in
            // After the panel has unfolded (a message action opened it).
            DispatchQueue.main.async {
                self?.input.focus()
            }
        }
        controller.onRestoreInput = { [weak self] text in
            guard let self, self.input.text.isEmpty else { return }
            self.input.text = text
        }
        transcript.apply(.reset, items: controller.items)
        updateState()
    }

    // MARK: Items

    private func makeItemView(_ item: AssistantPanelController.Item) -> any AssistantItemView {
        let id = item.id
        switch item.content {
        case .user:
            return AssistantUserView(item.content)
        case .assistant:
            return AssistantAnswerView(item.content) { [weak self] href in
                guard let self else { return }
                self.onLink?(href, self.view.window)
            }
        case .activity:
            return AssistantActivityView(item.content)
        case .draft:
            return AssistantDraftView(item.content) { [weak self] in
                self?.controller.openDraft(id)
            }
        case .error, .note:
            return AssistantMessageLineView(item.content) { [weak self] in
                self?.controller.retry(id)
            }
        }
    }

    // MARK: State

    /// Everything but the transcript follows the controller's state.
    private func updateState() {
        let c = controller
        subtitleLabel.stringValue = c.subtitle
        if c.isPinned {
            let symbol = c.pinned.count > 1
                ? AssistantContextChip.severalSymbol : AssistantContextChip.symbol(for: c.pinned[0].context)
            chip.show(label: c.contextLabel, symbol: symbol, removable: false)
        } else {
            chip.show(
                label: c.contextLabel, symbol: AssistantContextChip.symbol(for: c.effectiveContext),
                removable: c.effectiveContext != nil)
        }
        selectionBar.isHidden = !c.anotherSelected
        for b in actionButtons {
            b.isEnabled = c.canRunActions
        }
        let pending = c.pendingLabel
        pendingLabel.stringValue = pending
        pendingRow.isHidden = pending.isEmpty
        input.placeholderText = c.placeholder
        input.textView.setAccessibilityLabel(c.placeholder)
        newButton.isEnabled = !c.closed && (!c.items.isEmpty || c.running || c.pending != nil || c.isPinned)
        updateSendButton()
    }

    private func updateSendButton() {
        let texts = Assistant.panelTexts()
        let c = controller
        if c.running {
            sendButton.title = texts.stop
            sendButton.isEnabled = !c.closed
        } else {
            sendButton.title = mn(texts.send)
            sendButton.isEnabled = !c.closed && (!Self.blank(input.text) || c.pending == .action(.draftReply))
        }
    }

    private static func blank(_ s: String) -> Bool {
        s.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    // MARK: Actions

    /// Return in the field, or Send: the controller takes the text (the
    /// field empties) or leaves it (a question under way, nothing typed).
    private func send() {
        if controller.submit(input.text) {
            input.text = ""
        }
    }

    @objc private func sendOrStop(_ sender: Any?) {
        if controller.running {
            controller.stop()
        } else {
            send()
        }
    }

    @objc private func newConversation(_ sender: Any?) {
        controller.newConversation()
    }

    @objc private func quickAction(_ sender: NSButton) {
        guard Assistant.messageActions.indices.contains(sender.tag) else { return }
        controller.run(Assistant.messageActions[sender.tag])
    }

    @objc private func cancelPending(_ sender: Any?) {
        controller.cancelPending()
    }
}

/// The panel's context: a capsule with a symbol (a message, a
/// conversation, several, all mail), the text, and, while the chip follows
/// a selection, a button that leaves it out. The text may be a subject
/// (mail text): plain `stringValue`, one line, cut at the end, whole in
/// the tooltip.
@MainActor
final class AssistantContextChip: NSView {
    private let symbol = NSImageView()
    private let label = NSTextField(labelWithString: "")
    private let remove = NSButton()
    /// The remove button.
    var onRemove: (@MainActor () -> Void)?

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        wantsLayer = true
        layer?.cornerCurve = .continuous

        symbol.translatesAutoresizingMaskIntoConstraints = false
        symbol.imageScaling = .scaleNone
        symbol.contentTintColor = .secondaryLabelColor
        symbol.setAccessibilityElement(false)
        symbol.setContentHuggingPriority(.required, for: .horizontal)
        symbol.setContentCompressionResistancePriority(.required, for: .horizontal)

        label.translatesAutoresizingMaskIntoConstraints = false
        label.font = .systemFont(ofSize: 11)
        label.lineBreakMode = .byTruncatingTail
        label.maximumNumberOfLines = 1
        // Above the stack's hugging, below the row's edge: a subject is cut
        // only where the panel ends, never at a width the layout guessed.
        label.setContentCompressionResistancePriority(.defaultHigh, for: .horizontal)

        let removeText = L10n.T("Remove")
        remove.translatesAutoresizingMaskIntoConstraints = false
        remove.image = Icon.symbol("xmark.circle.fill", size: .small, description: removeText)
        remove.imagePosition = .imageOnly
        remove.isBordered = false
        remove.contentTintColor = .tertiaryLabelColor
        remove.toolTip = removeText
        remove.setAccessibilityLabel(removeText)
        remove.target = self
        remove.action = #selector(removeClicked(_:))
        remove.setContentHuggingPriority(.required, for: .horizontal)
        remove.setContentCompressionResistancePriority(.required, for: .horizontal)

        let row = NSStackView(views: [symbol, label, remove])
        row.orientation = .horizontal
        row.alignment = .centerY
        row.spacing = 4
        row.translatesAutoresizingMaskIntoConstraints = false
        addSubview(row)
        NSLayoutConstraint.activate([
            row.topAnchor.constraint(equalTo: topAnchor, constant: 3),
            row.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -3),
            row.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 8),
            row.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -6),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var wantsUpdateLayer: Bool { true }

    override func updateLayer() {
        layer?.backgroundColor = NSColor.tertiarySystemFill.cgColor
    }

    override func layout() {
        super.layout()
        layer?.cornerRadius = bounds.height / 2
    }

    /// The symbol of several contexts.
    static let severalSymbol = "square.stack"

    /// The symbol of one context: all mail, a conversation, a message.
    static func symbol(for context: AssistantPanelController.Context?) -> String {
        switch context {
        case nil: return "tray.2"
        case let c? where c.conversation: return "bubble.left.and.bubble.right"
        case _?: return "envelope"
        }
    }

    /// Shows the text and the symbol, and the remove button when asked.
    func show(label text: String, symbol name: String, removable: Bool) {
        label.stringValue = text
        label.toolTip = text
        symbol.image = Icon.symbol(name, size: .small)
        remove.isHidden = !removable
    }

    @objc private func removeClicked(_ sender: Any?) {
        onRemove?()
    }
}

/// The bar over the transcript while the conversation keeps its context and
/// the list's selection is not part of it: "Another message is selected",
/// New Conversation (ends the conversation; the chip follows the selection
/// again) and Add to Conversation (the selection joins what the
/// conversation is about). A card like the transcript's draft card; the
/// buttons wrap under the text when the panel is narrow.
@MainActor
final class AssistantSelectionBar: NSView {
    var onNewConversation: (@MainActor () -> Void)?
    var onAdd: (@MainActor () -> Void)?

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        let t = Assistant.panelTexts()
        let symbol = CalloutCard.symbolView()
        CalloutCard.show("envelope.badge", .info, in: symbol)
        symbol.translatesAutoresizingMaskIntoConstraints = false
        let label = assistantLabel(t.anotherSelected, size: 12)
        let buttons = FlowView(spacing: 6, lineSpacing: 6)
        for (title, action) in [
            (t.newConversation, #selector(newConversation(_:))), (t.addToConversation, #selector(add(_:))),
        ] {
            let b = NSButton(title: title, target: self, action: action)
            b.bezelStyle = .push
            b.controlSize = .small
            b.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
            buttons.addView(b)
        }
        let column = FillStackView(fillingViews: [label, buttons])
        column.spacing = 6
        let content = NSView()
        content.addSubview(symbol)
        content.addSubview(column)
        NSLayoutConstraint.activate([
            symbol.leadingAnchor.constraint(equalTo: content.leadingAnchor),
            symbol.centerYAnchor.constraint(equalTo: label.centerYAnchor),
            column.leadingAnchor.constraint(equalTo: symbol.trailingAnchor, constant: CalloutCard.spacing),
            column.trailingAnchor.constraint(equalTo: content.trailingAnchor),
            column.topAnchor.constraint(equalTo: content.topAnchor),
            column.bottomAnchor.constraint(equalTo: content.bottomAnchor),
        ])
        CalloutCard.install(content, in: self, margins: NSEdgeInsets())
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    @objc private func newConversation(_ sender: Any?) {
        onNewConversation?()
    }

    @objc private func add(_ sender: Any?) {
        onAdd?()
    }
}
