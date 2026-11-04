// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import UniformTypeIdentifiers

/// The Security page of the settings (docs/plans/signatures.md §6,
/// ui/internal/signature `SecurityPageTexts`, `KeyRows`): the OpenPGP keys
/// of the people whose signed mail the user receives, which the daemon finds
/// in the mail itself (Autocrypt headers, attached keys) and never looks up
/// online; Import Key… takes a key pasted or from a file, and a key's menu
/// confirms it by its fingerprint, takes the confirmation back or removes
/// it. A secret key is refused by the daemon and never stored. Everything
/// shown is the key's own text: plain, selectable.
///
/// `PGPKeysController` keeps the state; the page follows
/// notify.signaturesChanged (`SignatureState`).
@MainActor
final class SecurityPaneViewController: PreferencesPaneViewController {
    private var controller: PGPKeysController?
    private var token: BoardObserverToken?
    private let texts = SignatureView.securityPageTexts()
    private let importButton = NSButton(title: SignatureView.securityPageTexts().importKey, target: nil, action: nil)
    private lazy var group = PreferencesGroupView(
        title: texts.group, description: texts.description, headerSuffix: importButton)
    /// Shows a transient message in the settings window.
    var toast: (@MainActor (String) -> Void)?

    /// The window closed: nothing is taken any more.
    var closed = false {
        didSet {
            guard closed, !oldValue else { return }
            token?.cancel()
            token = nil
        }
    }

    override func loadView() {
        super.loadView()
        importButton.bezelStyle = .rounded
        importButton.isEnabled = false
        importButton.target = self
        importButton.action = #selector(importClicked(_:))
        addGroup(group)
        showEmpty()
    }

    /// Binds the page to the application (once): the daemon's key list,
    /// the questions as sheets on the settings window.
    func configure(state: AppState) {
        guard controller == nil, !closed else { return }
        _ = view
        let c = PGPKeysController(client: state.client)
        c.onChange = { [weak self] in self?.update() }
        c.toast = { [weak self] text in self?.toast?(text) }
        c.ask = { [weak self] q, fingerprint in
            let window = self?.view.window
            if let fingerprint {
                return await state.alerts.confirmKey(
                    on: window, heading: q.heading, body: q.body, fingerprint: fingerprint,
                    confirmLabel: q.accept, declineLabel: q.cancel)
            }
            return await state.alerts.confirmDestructive(
                on: window, heading: q.heading, body: q.body, confirmLabel: q.accept)
        }
        controller = c
        importButton.isEnabled = true
        // The list follows trust (a key imported from a message's popover,
        // a confirmation there) and the connection (it may come later).
        token = state.signatures.observe { [weak c] in c?.load() }
        c.load()
        update()
    }

    // MARK: Drawing

    private func update() {
        guard let c = controller, !closed else { return }
        importButton.isEnabled = !c.busy
        if !c.loaded {
            return
        }
        if c.loadFailed, c.rows.isEmpty {
            showMessage(texts.loadFailed)
            return
        }
        if c.rows.isEmpty {
            showEmpty()
            return
        }
        group.setRows(c.rows.map { row in
            SecurityKeyRowView(row: row) { [weak self] action in
                self?.controller?.perform(action, fingerprint: row.fingerprint)
            }
        })
    }

    private func showEmpty() {
        showMessage(texts.empty)
    }

    private func showMessage(_ text: String) {
        let label = NSTextField(labelWithString: text)
        label.font = .systemFont(ofSize: 13)
        label.textColor = .secondaryLabelColor
        label.alignment = .center
        label.isSelectable = false
        let row = NSView()
        row.translatesAutoresizingMaskIntoConstraints = false
        label.translatesAutoresizingMaskIntoConstraints = false
        row.addSubview(label)
        NSLayoutConstraint.activate([
            label.centerXAnchor.constraint(equalTo: row.centerXAnchor),
            label.topAnchor.constraint(equalTo: row.topAnchor, constant: 18),
            label.bottomAnchor.constraint(equalTo: row.bottomAnchor, constant: -18),
            label.leadingAnchor.constraint(greaterThanOrEqualTo: row.leadingAnchor, constant: 12),
        ])
        group.setRows([row])
    }

    // MARK: Import Key…

    @objc private func importClicked(_ sender: Any?) {
        guard let window = view.window, let c = controller, window.attachedSheet == nil else { return }
        let sheet = SecurityImportSheet()
        sheet.onImportText = { [weak c] text in c?.importPasted(text) }
        sheet.onImportFile = { [weak c] data in c?.importFile(data) }
        sheet.present(on: window)
    }
}

/// A row of the key list: the key's first user ID, its fingerprint in
/// monospace, where and when it came from, a pill for its trust (and one
/// for a revoked or expired key) and a menu with Confirm Key…, Remove
/// Confirmation and Remove Key….
@MainActor
final class SecurityKeyRowView: NSView {
    private let actions: [SignatureView.Button]
    private let onAction: @MainActor (SignatureView.Action) -> Void
    private let menuButton = NSButton()

    init(row: SignatureView.KeyRow, onAction: @escaping @MainActor (SignatureView.Action) -> Void) {
        actions = row.actions
        self.onAction = onAction
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false

        func label(_ text: String, font: NSFont, color: NSColor = .labelColor) -> NSTextField {
            let l = NSTextField(labelWithString: text)
            l.font = font
            l.textColor = color
            l.lineBreakMode = .byTruncatingTail
            l.isSelectable = true
            l.allowsEditingTextAttributes = false
            return l
        }
        let title = label(row.title, font: .systemFont(ofSize: 13))
        let fingerprint = label(
            row.subtitle, font: .monospacedSystemFont(ofSize: Typo.captionSize, weight: .regular), color: .secondaryLabelColor)
        fingerprint.lineBreakMode = .byTruncatingMiddle
        let origin = label(row.origin, font: Typo.caption, color: .secondaryLabelColor)
        let text = prefsColumn(spacing: 2)
        for l in [title, fingerprint, origin] {
            prefsAddFilling(l, to: text)
            l.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        }

        let trust = PillLabel()
        trust.font = Typo.caption
        trust.stringValue = row.trustPill
        let trustColours: IssuePill.Colours = row.verified
            ? (text: NSColor.systemGreen, fill: NSColor.systemGreen.withAlphaComponent(0.15))
            : (text: NSColor.secondaryLabelColor, fill: Tint.fg(alpha: 0.1))
        IssuePill.paint(trust, trustColours, emphasized: false)
        var pills: [NSView] = [trust]
        if !row.statePill.isEmpty {
            let state = PillLabel()
            state.font = Typo.caption
            state.stringValue = row.statePill
            IssuePill.paint(state, IssuePill.draftColours, emphasized: false)
            pills.append(state)
        }

        menuButton.image = NSImage(systemSymbolName: "ellipsis.circle", accessibilityDescription: nil)
        menuButton.isBordered = false
        menuButton.bezelStyle = .inline
        menuButton.imagePosition = .imageOnly
        menuButton.contentTintColor = .secondaryLabelColor
        menuButton.target = self
        menuButton.action = #selector(menuClicked(_:))
        menuButton.setAccessibilityLabel(row.title)
        menuButton.setContentHuggingPriority(.required, for: .horizontal)
        let end = NSStackView(views: pills + [menuButton])
        end.orientation = .horizontal
        end.alignment = .centerY
        end.spacing = 8
        end.translatesAutoresizingMaskIntoConstraints = false
        end.setContentHuggingPriority(.required, for: .horizontal)
        end.setContentCompressionResistancePriority(.required, for: .horizontal)

        addSubview(text)
        addSubview(end)
        NSLayoutConstraint.activate([
            text.leadingAnchor.constraint(equalTo: leadingAnchor, constant: PreferenceRowView.inset),
            text.topAnchor.constraint(equalTo: topAnchor, constant: PreferenceRowView.verticalInset),
            text.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -PreferenceRowView.verticalInset),
            end.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -PreferenceRowView.inset),
            end.centerYAnchor.constraint(equalTo: centerYAnchor),
            text.trailingAnchor.constraint(equalTo: end.leadingAnchor, constant: -PreferenceRowView.inset),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    @objc private func menuClicked(_ sender: NSButton) {
        let menu = NSMenu()
        for (i, a) in actions.enumerated() {
            let item = NSMenuItem(title: a.label, action: #selector(itemChosen(_:)), keyEquivalent: "")
            item.target = self
            item.tag = i
            item.isEnabled = a.enabled
            menu.addItem(item)
        }
        menu.popUp(positioning: nil, at: NSPoint(x: 0, y: sender.bounds.height + 2), in: sender)
    }

    @objc private func itemChosen(_ sender: NSMenuItem) {
        guard actions.indices.contains(sender.tag) else { return }
        onAction(actions[sender.tag].action)
    }
}

/// The sheet of Import Key…: a text area for a pasted public key (its
/// placeholder is the armour's first line, not translated), Choose File…
/// for a key file (`.asc`, `.gpg`, `.pgp`, `.key`, read up to
/// `maxPGPKeyBytes` + 1 bytes), Cancel and Import (enabled once the text is
/// a key the daemon may be given: `SignatureView.pastedParams`). The text is
/// never shown anywhere else and never leaves the application but to the
/// daemon.
@MainActor
final class SecurityImportSheet: NSObject, NSTextViewDelegate {
    var onImportText: (@MainActor (String) -> Void)?
    var onImportFile: (@MainActor (Data) -> Void)?

    private let texts = SignatureView.importDialogTexts()
    private let panel = NSPanel(
        contentRect: NSRect(x: 0, y: 0, width: 440, height: 330), styleMask: [.titled], backing: .buffered, defer: false)
    private let textView = NSTextView()
    private let placeholder = NSTextField(labelWithString: SignatureView.armorHeader)
    private let importButton: NSButton
    private weak var parent: NSWindow?

    override init() {
        importButton = NSButton(title: mn(SignatureView.importDialogTexts().import), target: nil, action: nil)
        super.init()
    }

    func present(on window: NSWindow) {
        parent = window
        let title = NSTextField(labelWithString: texts.title)
        title.font = .systemFont(ofSize: 13, weight: .bold)
        let body = NSTextField(wrappingLabelWithString: texts.body)
        body.font = Typo.caption
        body.textColor = .secondaryLabelColor
        body.preferredMaxLayoutWidth = 400

        textView.isRichText = false
        textView.importsGraphics = false
        textView.allowsUndo = true
        textView.isAutomaticQuoteSubstitutionEnabled = false
        textView.isAutomaticDashSubstitutionEnabled = false
        textView.isAutomaticSpellingCorrectionEnabled = false
        textView.isAutomaticTextReplacementEnabled = false
        textView.font = .monospacedSystemFont(ofSize: Typo.captionSize, weight: .regular)
        textView.isVerticallyResizable = true
        textView.isHorizontallyResizable = false
        textView.autoresizingMask = [.width]
        textView.textContainer?.widthTracksTextView = true
        textView.delegate = self
        let scroll = NSScrollView()
        scroll.documentView = textView
        scroll.hasVerticalScroller = true
        scroll.borderType = .bezelBorder
        scroll.translatesAutoresizingMaskIntoConstraints = false
        placeholder.font = textView.font
        placeholder.textColor = .tertiaryLabelColor
        placeholder.isSelectable = false
        placeholder.translatesAutoresizingMaskIntoConstraints = false
        scroll.addSubview(placeholder)
        NSLayoutConstraint.activate([
            placeholder.leadingAnchor.constraint(equalTo: scroll.leadingAnchor, constant: 8),
            placeholder.topAnchor.constraint(equalTo: scroll.topAnchor, constant: 6),
        ])

        let choose = NSButton(title: texts.chooseFile, target: self, action: #selector(chooseFile(_:)))
        choose.bezelStyle = .rounded
        let cancel = NSButton(title: mn(texts.cancel), target: self, action: #selector(cancelClicked(_:)))
        cancel.bezelStyle = .rounded
        cancel.keyEquivalent = "\u{1b}"
        importButton.target = self
        importButton.action = #selector(importClicked(_:))
        importButton.bezelStyle = .rounded
        importButton.keyEquivalent = "\r"
        importButton.isEnabled = false
        let spacer = NSView()
        spacer.setContentHuggingPriority(.defaultLow, for: .horizontal)
        let buttons = NSStackView(views: [choose, spacer, cancel, importButton])
        buttons.orientation = .horizontal
        buttons.spacing = 8

        let stack = NSStackView(views: [title, body, scroll, buttons])
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 10
        stack.edgeInsets = NSEdgeInsets(top: 20, left: 20, bottom: 16, right: 20)
        stack.translatesAutoresizingMaskIntoConstraints = false
        let content = NSView()
        content.addSubview(stack)
        panel.contentView = content
        NSLayoutConstraint.activate([
            stack.topAnchor.constraint(equalTo: content.topAnchor),
            stack.bottomAnchor.constraint(equalTo: content.bottomAnchor),
            stack.leadingAnchor.constraint(equalTo: content.leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: content.trailingAnchor),
            scroll.widthAnchor.constraint(equalTo: stack.widthAnchor, constant: -40),
            scroll.heightAnchor.constraint(equalToConstant: 150),
            buttons.widthAnchor.constraint(equalTo: stack.widthAnchor, constant: -40),
        ])
        panel.initialFirstResponder = textView
        window.beginSheet(panel) { [self] _ in
            // Keeps the sheet's owner alive until it ends.
            _ = self
        }
    }

    func textDidChange(_ notification: Foundation.Notification) {
        placeholder.isHidden = !textView.string.isEmpty
        importButton.isEnabled = SignatureView.pastedParams(textView.string) != nil
    }

    private func end() {
        parent?.endSheet(panel)
        panel.orderOut(nil)
    }

    @objc private func cancelClicked(_ sender: Any?) {
        end()
    }

    @objc private func importClicked(_ sender: Any?) {
        let text = textView.string
        end()
        onImportText?(text)
    }

    @objc private func chooseFile(_ sender: Any?) {
        let open = NSOpenPanel()
        open.canChooseDirectories = false
        open.allowsMultipleSelection = false
        open.allowedContentTypes = ["asc", "gpg", "pgp", "key"].compactMap { UTType(filenameExtension: $0) }
        open.message = texts.fileFilter
        Task { @MainActor [weak self] in
            let response = await open.beginSheetModal(for: self?.panel ?? NSWindow())
            guard response == .OK, let url = open.url else { return }
            // At most one byte over the limit: more is "too large", and a
            // huge file is never read whole.
            let data = (try? FileHandle(forReadingFrom: url))
                .flatMap { try? $0.read(upToCount: API.Limits.maxPGPKeyBytes + 1) } ?? Data()
            self?.end()
            self?.onImportFile?(data)
        }
    }
}
