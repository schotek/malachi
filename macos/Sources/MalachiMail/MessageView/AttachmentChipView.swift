// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import UniformTypeIdentifiers

/// One attachment under the headers (attachments.go `buildChip`): a tile
/// (`AttachmentTileView`) with the type icon, the name and the size; a
/// click previews the attachment in Quick Look, the arrow (and a
/// right-click) offers View (an attached message only), Open in the default
/// application and Save As…; after the size, for a part kept on the mail
/// server, the server symbol with the reason as its tooltip, or a spinner
/// while the message is being downloaded. The actions close over the
/// attachment they were built for, so a chip never acts on a message other
/// than its own. A part out of reach (or while the body is on its way)
/// leaves the chip disabled with the reason as its tooltip; a part on the
/// server stays enabled, and its actions download the message first; an
/// attached message is viewed in its own window on click; an executable is
/// previewed like any file but keeps Open disabled (docs/security.md §4).
/// After Open the Assistant's "Ask the Assistant…" (ui/internal/assistant),
/// while the `assistant-menu` setting is on, hands the file to Claude.
/// Names and types are server data and are shown as plain text; the
/// name's middle is elided so the extension stays visible, the whole name
/// is the tooltip.
@MainActor
final class AttachmentChipView: AttachmentTileView {
    static let indicatorSize: CGFloat = 14

    let attachment: Attachment
    /// What the chip can do with its part (`partState`).
    let state: PartState
    let nested: Bool
    let executable: Bool
    /// The server symbol or the spinner of a part kept on the server; nil
    /// for any other part.
    private(set) var indicator: NSView?

    var onPreview: (@MainActor () -> Void)?
    var onOpen: (@MainActor () -> Void)?
    var onSave: (@MainActor () -> Void)?
    var onView: (@MainActor () -> Void)?
    /// "Ask the Assistant…": asked when the menu opens; nil hides the item
    /// (the Assistant menu is off), false disables it (the chosen Claude
    /// app is missing).
    var assistantItem: (@MainActor () -> Bool?)?
    var onAskAssistant: (@MainActor () -> Void)?

    /// Whether the chip's actions can run: the part is stored, or on the
    /// server and downloaded on the way.
    var available: Bool { state == .local || state == .remote }

    /// - Parameters:
    ///   - attachment: what the chip stands for.
    ///   - state: whether message.part can deliver it, now or after a
    ///     download (`partState`).
    ///   - why: the reason that goes with the state, as the tooltip of the
    ///     disabled chip or of the server symbol.
    ///   - downloading: the message is being downloaded: a spinner instead
    ///     of the server symbol (`MessageCache.showsDownload`).
    init(attachment: Attachment, state: PartState, why: String, downloading: Bool) {
        self.attachment = attachment
        self.state = state
        nested = attachedMessage(attachment)
        executable = executableAttachment(filename: attachment.filename, contentType: attachment.contentType)

        let arrow = NSButton(image: Icon.image("pan-down", size: .small), target: nil, action: nil)
        arrow.toolTip = L10n.T("More Actions")
        arrow.setAccessibilityLabel(L10n.T("More Actions"))
        let name = chipName(attachment)
        let icon = NSWorkspace.shared.icon(for: chipIconType(attachment))
        super.init(icon: icon, name: name, size: attachment.size > 0 ? formatSize(attachment.size) : "", accessory: arrow)
        arrow.target = self
        arrow.action = #selector(arrowClicked(_:))
        onClick = { [weak self] in self?.primary() }
        contextMenu = { [weak self] in self?.makeMenu() }

        switch (available, nested, executable) {
        case (false, _, _):
            isEnabled = false
            toolTip = why
            arrow.toolTip = why
        case (true, false, true):
            toolTip = L10n.T("Programs and scripts are not opened directly; save the file and decide yourself.")
        default:
            // An attached message is rendered by the daemon, read-only:
            // the safest thing to do with it, whatever it is called.
            toolTip = name
        }

        if state == .remote {
            let mark = downloading ? Self.spinner() : Self.serverSymbol(why)
            mark.translatesAutoresizingMaskIntoConstraints = false
            NSLayoutConstraint.activate([
                mark.widthAnchor.constraint(equalToConstant: Self.indicatorSize),
                mark.heightAnchor.constraint(equalToConstant: Self.indicatorSize),
            ])
            detailRow.addArrangedSubview(mark)
            indicator = mark
        }
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// The server symbol (GTK `network-server-symbolic`), secondary like a
    /// dim label, with the reason as tooltip and accessibility label.
    private static func serverSymbol(_ why: String) -> NSView {
        let view = NSImageView(image: Icon.image("network-server", size: .small))
        view.contentTintColor = .secondaryLabelColor
        view.imageScaling = .scaleProportionallyDown
        view.toolTip = why
        view.setAccessibilityLabel(why)
        return view
    }

    /// The spinner of a running download (`adw.NewSpinner()`).
    private static func spinner() -> NSView {
        let spinner = Spinner(size: indicatorSize)
        spinner.setAccessibilityLabel(L10n.T("Downloading…"))
        spinner.start()
        return spinner
    }

    /// Elides the middle of `s` to at most `max` characters, keeping the
    /// end (the extension) whole (Pango's EllipsizeMiddle, by characters).
    static func middleEllipsis(_ s: String, max: Int) -> String {
        let chars = Array(s)
        guard max >= 2, chars.count > max else { return s }
        let keep = max - 1
        let head = (keep + 1) / 2
        let tail = keep - head
        return String(chars[..<head]) + "\u{2026}" + String(chars[(chars.count - tail)...])
    }

    @objc private func arrowClicked(_ sender: Any?) {
        guard let arrow = accessory else { return }
        makeMenu().popUp(positioning: nil, at: NSPoint(x: 0, y: arrow.bounds.height + 2), in: arrow)
    }

    /// The tile's click: view an attached message in its own
    /// window, preview anything else, executables included (Quick Look
    /// never runs them).
    private func primary() {
        if nested {
            onView?()
        } else {
            onPreview?()
        }
    }

    /// The menu of the arrow and of a right-click (attachments.go
    /// `chipMenu`).
    private func makeMenu() -> NSMenu {
        let menu = NSMenu()
        menu.autoenablesItems = false
        if nested {
            let view = NSMenuItem(title: mn(L10n.T("_View")), action: #selector(viewItem(_:)), keyEquivalent: "")
            view.target = self
            menu.addItem(view)
        }
        let open = NSMenuItem(title: mn(L10n.T("_Open")), action: #selector(openItem(_:)), keyEquivalent: "")
        open.target = self
        open.isEnabled = !executable
        menu.addItem(open)
        if let enabled = assistantItem?() {
            let ask = NSMenuItem(title: Assistant.texts().askFile, action: #selector(askItem(_:)), keyEquivalent: "")
            ask.target = self
            ask.isEnabled = enabled
            menu.addItem(ask)
        }
        let save = NSMenuItem(title: mn(L10n.T("Save _As…")), action: #selector(saveItem(_:)), keyEquivalent: "")
        save.target = self
        menu.addItem(save)
        return menu
    }

    @objc private func viewItem(_ sender: Any?) {
        onView?()
    }

    @objc private func openItem(_ sender: Any?) {
        onOpen?()
    }

    @objc private func saveItem(_ sender: Any?) {
        onSave?()
    }

    @objc private func askItem(_ sender: Any?) {
        onAskAssistant?()
    }
}

/// The line over the tiles of two or more attachments that can all be
/// saved (attachments.go `buildSaveAll`): their number and total size in
/// a dim caption, and at its end the quiet Save All button that saves all
/// of them into one folder.
@MainActor
final class AttachmentSummaryView: NSView, FlowFullLine {
    var onClick: (@MainActor () -> Void)?

    /// Save All, disabled while one of the message runs.
    var isEnabled: Bool {
        get { button.isEnabled }
        set { button.isEnabled = newValue }
    }

    private let button = NSButton()

    init(count: Int, total: Int) {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false

        let label = NSTextField(labelWithString: attachmentSummary(count: count, total: total))
        label.font = Typo.caption
        label.textColor = Tint.secondary
        label.lineBreakMode = .byTruncatingTail
        label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        label.translatesAutoresizingMaskIntoConstraints = false

        button.title = mn(L10n.T("Save _All"))
        button.image = Icon.image("document-save", size: .small)
        button.imagePosition = .imageLeading
        button.imageHugsTitle = true
        button.isBordered = false
        button.controlSize = .small
        button.font = Typo.caption
        button.contentTintColor = Tint.secondary
        button.target = self
        button.action = #selector(clicked(_:))
        button.translatesAutoresizingMaskIntoConstraints = false

        addSubview(label)
        addSubview(button)
        NSLayoutConstraint.activate([
            label.leadingAnchor.constraint(equalTo: leadingAnchor),
            label.firstBaselineAnchor.constraint(equalTo: button.firstBaselineAnchor),
            button.leadingAnchor.constraint(greaterThanOrEqualTo: label.trailingAnchor, constant: 12),
            button.trailingAnchor.constraint(equalTo: trailingAnchor),
            button.topAnchor.constraint(equalTo: topAnchor),
            button.bottomAnchor.constraint(equalTo: bottomAnchor),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    @objc private func clicked(_ sender: Any?) {
        onClick?()
    }
}
