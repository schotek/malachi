// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The quick-reply bar of Mail's reading pane, under a conversation shown
/// as chat and under the page of a single message
/// (docs/plans/chat-view.md §10–§12; wireframes
/// docs/plans/chat-view/w4-quick-reply.svg and w5-quick-reply-message.svg):
/// a fixed row at the bottom of the pane (`ReadingPaneViewController`, which
/// owns it), under the bubbles' or the message's scroll view, a separator
/// line above it, its content kept to the cards'
/// column (a clamp of `ConversationLayout.Metrics.maxWidth`, the column's
/// side insets). From the leading end: the mode button, a pull-down with the
/// mode's icon and label (Reply, Reply All; its menu the modes the account
/// offers and, after a separator, Forward…, which opens the compose window
/// at once), on an issue a plain label Comment with no menu; the field of
/// one to five lines (`AssistantInputView` with `returnSends` off: Return
/// is a new line); while the Assistant is shown (`Assistant.shown`, the
/// toolbar's rule, set by the host as `assistantShown`) the Assistant
/// button, the toolbar's ✦ with its icon and tooltip, which does what that
/// one does (`onAssistant`: the main window's `showAssistant(from:)`,
/// `Assistant.buttonOpensPanel`: the assistant panel with its field
/// focused, or the toolbar's Assistant menu under the button); Open in a
/// Window; Send. In a narrow pane
/// (`ConversationLayout.quickReplyIconOnly`) the mode button shows its icon
/// alone. The mode button's tooltip names the sender of the message the
/// reply goes to (`Conversation.quickReplyTargetTooltip`).
///
/// Everything it shows is the controller's (`QuickReplyController.state`,
/// read again on every `onChange`); the bar reports typing, the mode, Send,
/// Open in a Window and Forward… to it and decides nothing. A hidden bar
/// (`QuickReplyState.visible` off) collapses to no height.
///
/// Keys: ⌘↩ is the menu bar's Message ▸ Send (`sendMessage:`), which this
/// controller answers while the keyboard is in its field (a view
/// controller is its view's next responder), as the compose window's pane
/// does; the item is off while there is nothing to send. Escape in the
/// field gives the keyboard to the message list (`onFocusList`, which does
/// nothing while the list is hidden); a bar that hides on its own while
/// its field has the keyboard (the selection moved to a page without a bar,
/// the conversation went to the timeline) gives it to the view the pane
/// shows (`keyboardTarget`: the message's body, the layout's scroll view),
/// as GTK's bar gives it to the shown scroller, never to the window alone
/// and never to the list. The
/// single-key actions of the main
/// window are off while the field has the keyboard (the window's
/// `isTyping`), and Space types.
///
/// Rule 3: the only text from mail here is the target's sender in the
/// tooltip (`Item.sender`, cleaned to one line by the model); what the user
/// types becomes HTML in the controller (`Conversation.quickReplyHTML`,
/// escaped), never here.
@MainActor
final class ConversationQuickReplyBar: NSViewController {
    let controller: QuickReplyController
    /// Escape in the field: the message list takes the keyboard.
    var onFocusList: (@MainActor () -> Void)?
    /// The bar hides while its field has the keyboard: the view that takes
    /// it, the one the pane shows now (`ReadingPaneViewController`).
    var keyboardTarget: (@MainActor () -> NSView?)?
    /// The Assistant button, with itself to show a menu under: the main
    /// window's `showAssistant(from:)` (Integration+Conversation).
    var onAssistant: (@MainActor (NSView) -> Void)?
    /// The Assistant button shows: the Assistant is shown
    /// (`AssistantController.shown`, as the toolbar's ✦).
    var assistantShown = false {
        didSet {
            guard assistantShown != oldValue, isViewLoaded else { return }
            applyAssistantShown()
        }
    }

    private let input = AssistantInputView()
    private let modeButton = NSPopUpButton(frame: .zero, pullsDown: true)
    /// On an issue the mode is a comment and nothing else: a label.
    private let commentIcon = NSImageView()
    private let commentLabel = NSTextField(labelWithString: "")
    private let commentBox = NSStackView()
    private let assistantButton = NSButton()
    private let openButton = NSButton()
    private let sendButton = NSButton()
    private let separator = NSBox()
    private var clamp: ClampView!

    /// Active while the bar shows: its height is its content's.
    private var contentBottom: NSLayoutConstraint!
    /// Active while the bar is hidden: no height at all (`isHidden` alone
    /// would leave the room under the chat).
    private var collapsed: NSLayoutConstraint!
    /// What `setVisible` last applied; nil before the first time.
    private var appliedVisible: Bool?
    /// The mode button shows its icon alone (a narrow pane).
    private var iconOnly = false
    /// A change of `iconOnly` is to be rendered on the next turn of the run
    /// loop (never inside `viewDidLayout`).
    private var iconOnlyPending = false
    /// What the mode button was last built for, so that a change of the
    /// text alone does not rebuild its menu.
    private var shownMode: ModeKey?

    private struct ModeKey: Equatable {
        var mode: Conversation.QuickReplyMode
        var modes: [Conversation.QuickReplyMode]
        var comment: Bool
        var iconOnly: Bool
        var tooltip: String
        var enabled: Bool
    }

    /// Above and below the row.
    private static let paddingV: CGFloat = 8
    private static let gap: CGFloat = 8

    init(controller: QuickReplyController) {
        self.controller = controller
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    // MARK: View

    override func loadView() {
        let root = NSView()
        root.translatesAutoresizingMaskIntoConstraints = false
        root.clipsToBounds = true

        separator.boxType = .separator
        separator.translatesAutoresizingMaskIntoConstraints = false
        root.addSubview(separator)

        let widest = CGFloat(ConversationLayout.Metrics.maxWidth)
        clamp = ClampView(maximum: widest, tight: widest)
        let column = NSView()
        column.translatesAutoresizingMaskIntoConstraints = false
        buildRow(in: column)
        clamp.setChild(column)
        root.addSubview(clamp)

        contentBottom = clamp.bottomAnchor.constraint(equalTo: root.bottomAnchor)
        collapsed = root.heightAnchor.constraint(equalToConstant: 0)
        NSLayoutConstraint.activate([
            separator.topAnchor.constraint(equalTo: root.topAnchor),
            separator.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            separator.trailingAnchor.constraint(equalTo: root.trailingAnchor),
            clamp.topAnchor.constraint(equalTo: separator.bottomAnchor),
            clamp.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            clamp.trailingAnchor.constraint(equalTo: root.trailingAnchor),
        ])
        view = root

        controller.onChange = { [weak self] in self?.render() }
        render()
    }

    private func buildRow(in column: NSView) {
        // The mode button: a pull-down whose first item is its face.
        modeButton.translatesAutoresizingMaskIntoConstraints = false
        modeButton.bezelStyle = .push
        modeButton.autoenablesItems = false
        modeButton.setContentHuggingPriority(.required, for: .horizontal)
        modeButton.setContentCompressionResistancePriority(.required, for: .horizontal)

        commentIcon.image = Self.modeIcon(.reply)
        commentIcon.contentTintColor = .secondaryLabelColor
        commentIcon.setAccessibilityElement(false)
        commentLabel.font = Typo.body
        commentLabel.textColor = .labelColor
        commentBox.orientation = .horizontal
        commentBox.spacing = 4
        commentBox.alignment = .centerY
        commentBox.translatesAutoresizingMaskIntoConstraints = false
        commentBox.setViews([commentIcon, commentLabel], in: .leading)
        commentBox.setContentHuggingPriority(.required, for: .horizontal)
        commentBox.setContentCompressionResistancePriority(.required, for: .horizontal)
        commentBox.isHidden = true

        input.returnSends = false
        input.onTextChange = { [weak self] in
            guard let self else { return }
            self.controller.setText(self.input.text)
        }
        input.onCancel = { [weak self] in self?.onFocusList?() }
        input.setContentHuggingPriority(.defaultLow, for: .horizontal)
        input.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)

        // The toolbar's ✦ (MainToolbar `ID.assistant`): its icon, its
        // tooltip and accessibility label.
        let assistantLabel = Assistant.texts().assistant
        configure(assistantButton, Icon.symbol("sparkles", size: .toolbar, description: assistantLabel),
                  assistantLabel, #selector(assistantClicked(_:)))

        let openLabel = Conversation.quickReplyOpenWindowLabel()
        configure(openButton, Icon.symbol("arrow.up.forward.square", size: .toolbar, description: openLabel), openLabel,
                  #selector(openClicked(_:)))
        let sendLabel = mn(L10n.T("_Send"))
        configure(sendButton, Icon.symbol("paperplane.fill", size: .toolbar, description: sendLabel), sendLabel,
                  Action.sendMessage)
        // As the compose window's Send: tinted with the accent colour, its
        // key the menu bar's ⌘↩ (no key equivalent of its own, which would
        // send from anywhere in the window).
        sendButton.bezelColor = .controlAccentColor
        sendButton.keyEquivalent = ""
        sendButton.target = self
        sendButton.toolTip = sendLabel + " (⌘↩)"

        for v in [modeButton, commentBox, input, assistantButton, openButton, sendButton] as [NSView] {
            column.addSubview(v)
        }
        let inset = ConversationMetrics.sideInset
        // The buttons sit on the field's last line: centred on a line's
        // height above its bottom, so that a field grown to five lines
        // keeps them by the caret, as messaging apps do.
        let line = input.bottomAnchor
        let half = (input.minimumHeight + 2) / 2
        let modeLead = input.leadingAnchor.constraint(equalTo: modeButton.trailingAnchor, constant: Self.gap)
        let commentLead = input.leadingAnchor.constraint(equalTo: commentBox.trailingAnchor, constant: Self.gap)
        NSLayoutConstraint.activate([
            modeButton.leadingAnchor.constraint(equalTo: column.leadingAnchor, constant: inset),
            commentBox.leadingAnchor.constraint(equalTo: column.leadingAnchor, constant: inset),
            modeLead,
            sendButton.leadingAnchor.constraint(equalTo: openButton.trailingAnchor, constant: 6),
            sendButton.trailingAnchor.constraint(equalTo: column.trailingAnchor, constant: -inset),
            input.topAnchor.constraint(equalTo: column.topAnchor, constant: Self.paddingV),
            input.bottomAnchor.constraint(equalTo: column.bottomAnchor, constant: -Self.paddingV),
            modeButton.centerYAnchor.constraint(equalTo: line, constant: -half),
            commentBox.centerYAnchor.constraint(equalTo: line, constant: -half),
            assistantButton.centerYAnchor.constraint(equalTo: line, constant: -half),
            openButton.centerYAnchor.constraint(equalTo: line, constant: -half),
            sendButton.centerYAnchor.constraint(equalTo: line, constant: -half),
        ])
        modeLeading = modeLead
        commentLeading = commentLead
        openAfterInput = openButton.leadingAnchor.constraint(equalTo: input.trailingAnchor, constant: Self.gap)
        assistantAfterInput = assistantButton.leadingAnchor.constraint(
            equalTo: input.trailingAnchor, constant: Self.gap)
        openAfterAssistant = openButton.leadingAnchor.constraint(equalTo: assistantButton.trailingAnchor, constant: 6)
        applyAssistantShown()
    }

    /// Open in a Window follows the field, or the Assistant button that
    /// follows it while it shows.
    private var openAfterInput: NSLayoutConstraint!
    private var assistantAfterInput: NSLayoutConstraint!
    private var openAfterAssistant: NSLayoutConstraint!

    /// Shows or hides the Assistant button and closes the gap it leaves.
    private func applyAssistantShown() {
        assistantButton.isHidden = !assistantShown
        if assistantShown {
            openAfterInput.isActive = false
            assistantAfterInput.isActive = true
            openAfterAssistant.isActive = true
        } else {
            assistantAfterInput.isActive = false
            openAfterAssistant.isActive = false
            openAfterInput.isActive = true
        }
    }

    /// The field's leading edge after the mode button, or after the
    /// comment label on an issue: one of them is active.
    private var modeLeading: NSLayoutConstraint?
    private var commentLeading: NSLayoutConstraint?

    private func configure(_ b: NSButton, _ image: NSImage, _ label: String, _ action: Selector) {
        b.translatesAutoresizingMaskIntoConstraints = false
        b.bezelStyle = .push
        b.image = image
        b.imagePosition = .imageOnly
        b.imageScaling = .scaleProportionallyDown
        b.title = ""
        b.toolTip = label
        b.setAccessibilityLabel(label)
        b.target = self
        b.action = action
        b.setContentHuggingPriority(.required, for: .horizontal)
        b.setContentCompressionResistancePriority(.required, for: .horizontal)
    }

    /// The bar spans the reading pane, so its width is the pane's
    /// (`quickReplyIconOnly(pane:)`). A change of width that flips the mode
    /// button to its icon alone (or back) is rendered on the next turn of the run loop: changing the
    /// pull-down's menu and title inside layout would ask for layout again
    /// while it runs.
    override func viewDidLayout() {
        super.viewDidLayout()
        let narrow = ConversationLayout.quickReplyIconOnly(pane: Double(view.bounds.width))
        guard narrow != iconOnly, !iconOnlyPending else { return }
        iconOnlyPending = true
        // A run loop timer, not the main queue: it also fires in a nested
        // run of the run loop.
        perform(#selector(applyIconOnly), with: nil, afterDelay: 0)
    }

    @objc private func applyIconOnly() {
        iconOnlyPending = false
        let narrow = ConversationLayout.quickReplyIconOnly(pane: Double(view.bounds.width))
        if narrow != iconOnly {
            iconOnly = narrow
            render()
        }
    }

    // MARK: Rendering

    /// Shows the controller's state.
    private func render() {
        guard isViewLoaded else { return }
        let s = controller.state
        setVisible(s.visible)

        // The text first: setting it reports a change, which the controller
        // drops as the same text.
        if input.text != s.text {
            input.text = s.text
        }
        let placeholder = Conversation.quickReplyPlaceholder(comment: s.comment)
        input.placeholderText = placeholder
        input.textView.setAccessibilityLabel(placeholder)
        input.isEnabled = !s.sending
        openButton.isEnabled = !s.sending
        sendButton.isEnabled = !s.blank && !s.sending

        let tooltip = Conversation.quickReplyTargetTooltip(s.mode, sender: s.target?.sender ?? "")
        let key = ModeKey(mode: s.mode, modes: s.modes, comment: s.comment, iconOnly: iconOnly, tooltip: tooltip,
                          enabled: !s.sending)
        if key != shownMode {
            shownMode = key
            renderMode(key)
        }
    }

    /// The mode button (or, on an issue, the comment label).
    private func renderMode(_ k: ModeKey) {
        let tip: String? = k.tooltip.isEmpty ? nil : k.tooltip
        modeButton.isHidden = k.comment
        commentBox.isHidden = !k.comment
        if k.comment {
            modeLeading?.isActive = false
            commentLeading?.isActive = true
            let label = Jira.replyLabel(comment: true)
            commentLabel.stringValue = label
            commentLabel.isHidden = k.iconOnly
            commentBox.toolTip = tip
            commentBox.setAccessibilityElement(true)
            commentBox.setAccessibilityRole(.staticText)
            commentBox.setAccessibilityLabel(label)
            return
        }
        commentLeading?.isActive = false
        modeLeading?.isActive = true

        let label = Self.modeLabel(k.mode)
        let menu = NSMenu()
        // A pull-down shows its first item on its face.
        let face = NSMenuItem(title: k.iconOnly ? "" : label, action: nil, keyEquivalent: "")
        face.image = Self.modeIcon(k.mode)
        menu.addItem(face)
        let replies = k.modes.filter { $0 != .forward }
        for m in replies {
            let item = NSMenuItem(title: Self.modeLabel(m), action: #selector(modeChosen(_:)), keyEquivalent: "")
            item.target = self
            item.image = Self.modeIcon(m)
            item.state = m == k.mode ? .on : .off
            item.tag = m == .replyAll ? 1 : 0
            menu.addItem(item)
        }
        if k.modes.contains(.forward) {
            if !replies.isEmpty {
                menu.addItem(.separator())
            }
            let item = NSMenuItem(
                title: Conversation.quickReplyForwardLabel(), action: #selector(forwardChosen(_:)), keyEquivalent: "")
            item.target = self
            item.image = Self.modeIcon(.forward)
            menu.addItem(item)
        }
        modeButton.menu = menu
        modeButton.toolTip = tip
        modeButton.setAccessibilityLabel(label)
        modeButton.isEnabled = k.enabled
    }

    private static func modeLabel(_ m: Conversation.QuickReplyMode) -> String {
        switch m {
        case .reply: return L10n.T("Reply")
        case .replyAll: return L10n.T("Reply All")
        case .forward: return Conversation.quickReplyForwardLabel()
        }
    }

    private static func modeIcon(_ m: Conversation.QuickReplyMode) -> NSImage {
        switch m {
        case .reply: return Icon.image("mail-reply-sender")
        case .replyAll: return Icon.image("mail-reply-all")
        case .forward: return Icon.image("mail-forward")
        }
    }

    /// Shows or collapses the bar, whenever that differs from what it last
    /// applied (`appliedVisible`; the view's `isHidden` is no state). A
    /// field that had the keyboard lets it go as the bar hides on its own:
    /// the window's first responder is cleared (no beep, no other view
    /// picked); only Escape gives it to the list.
    private func setVisible(_ on: Bool) {
        guard appliedVisible != on else { return }
        appliedVisible = on
        if !on, let window = view.window, window.firstResponder === input.textView {
            // The view shown takes the keyboard. Only when it refuses does
            // the window take it: a hidden field must never keep it (what
            // is typed would land in the bar unseen).
            if keyboardTarget?().map({ window.makeFirstResponder($0) }) != true {
                window.makeFirstResponder(nil)
            }
        }
        view.isHidden = !on
        if on {
            collapsed.isActive = false
            contentBottom.isActive = true
        } else {
            contentBottom.isActive = false
            collapsed.isActive = true
        }
    }

    // MARK: Actions

    @objc private func modeChosen(_ sender: NSMenuItem) {
        controller.setMode(sender.tag == 1 ? .replyAll : .reply)
    }

    @objc private func forwardChosen(_ sender: Any?) {
        controller.forward()
    }

    @objc private func openClicked(_ sender: Any?) {
        controller.openWindow()
    }

    @objc private func assistantClicked(_ sender: Any?) {
        onAssistant?(assistantButton)
    }
}

// MARK: - Message ▸ Send (⌘↩) while the field has the keyboard

extension ConversationQuickReplyBar: MalachiActions {
    @objc func sendMessage(_ sender: Any?) {
        controller.send()
    }
}

extension ConversationQuickReplyBar: NSUserInterfaceValidations {
    /// Send only with something to send and nothing on its way; the bar's
    /// other actions always.
    func validateUserInterfaceItem(_ item: any NSValidatedUserInterfaceItem) -> Bool {
        guard let action = item.action else { return false }
        if action == Action.sendMessage {
            let s = controller.state
            return s.visible && !s.blank && !s.sending
        }
        return true
    }
}
