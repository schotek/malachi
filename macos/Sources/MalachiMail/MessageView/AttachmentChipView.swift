// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import UniformTypeIdentifiers

/// One attachment under the headers (attachments.go `buildChip`): a
/// two-segment control, the first with the type icon, the name and the size
/// (a click previews the attachment in Quick Look), the second with an
/// arrow that offers View (an attached message only), Open in the default
/// application and Save As…; after it, for a part kept on the mail server,
/// the server symbol with the reason as its tooltip, or a spinner while the
/// message is being downloaded. The actions close over the attachment they
/// were built for, so a chip never acts on a message other than its own.
/// A part out of reach (or while the body is on its way) leaves the chip
/// disabled with the reason as its tooltip; a part on the server stays
/// enabled, and its actions download the message first; an attached message
/// is viewed in its own window on click; an executable is previewed like
/// any file but keeps Open disabled (docs/security.md §4). Names and types
/// are server data and are shown as plain text; the name's middle is elided
/// so the extension stays visible, the whole name is the tooltip.
@MainActor
final class AttachmentChipView: NSView {
    /// The gap between the control and the server symbol or the spinner.
    static let indicatorSpacing: CGFloat = 4
    static let indicatorSize: CGFloat = 16

    let attachment: Attachment
    /// What the chip can do with its part (`partState`).
    let state: PartState
    let nested: Bool
    let executable: Bool
    /// The segments: preview (or view) and the arrow's menu.
    let control = NSSegmentedControl()
    /// The server symbol or the spinner of a part kept on the server; nil
    /// for any other part.
    private(set) var indicator: NSView?

    var onPreview: (@MainActor () -> Void)?
    var onOpen: (@MainActor () -> Void)?
    var onSave: (@MainActor () -> Void)?
    var onView: (@MainActor () -> Void)?

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
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false

        control.translatesAutoresizingMaskIntoConstraints = false
        control.segmentCount = 2
        control.segmentStyle = .rounded
        control.trackingMode = .momentary
        control.controlSize = .small
        control.font = Typo.chip
        control.target = self
        control.action = #selector(clicked(_:))

        let name = chipName(attachment)
        var text = Self.middleEllipsis(name, max: chipNameChars)
        if attachment.size > 0 {
            text += "  " + formatSize(attachment.size)
        }
        control.setImage(Self.icon(for: attachment), forSegment: 0)
        control.setImageScaling(.scaleProportionallyDown, forSegment: 0)
        control.setLabel(text, forSegment: 0)
        control.setImage(Icon.image("pan-down", size: .small), forSegment: 1)
        control.setImageScaling(.scaleProportionallyDown, forSegment: 1)
        control.setToolTip(L10n.T("More Actions"), forSegment: 1)

        switch (available, nested, executable) {
        case (false, _, _):
            control.isEnabled = false
            control.setToolTip(why, forSegment: 0)
            control.setToolTip(why, forSegment: 1)
        case (true, true, _):
            // Rendered by the daemon, read-only: the safest thing to do
            // with it, whatever it is called.
            control.setToolTip(name, forSegment: 0)
        case (true, false, true):
            control.setToolTip(L10n.T("Programs and scripts are not opened directly; save the file and decide yourself."), forSegment: 0)
        default:
            control.setToolTip(name, forSegment: 0)
        }
        addSubview(control)

        var constraints = [
            control.leadingAnchor.constraint(equalTo: leadingAnchor),
            control.topAnchor.constraint(equalTo: topAnchor),
            control.bottomAnchor.constraint(equalTo: bottomAnchor),
        ]
        if state == .remote {
            let mark = downloading ? Self.spinner() : Self.serverSymbol(why)
            mark.translatesAutoresizingMaskIntoConstraints = false
            addSubview(mark)
            indicator = mark
            constraints += [
                mark.leadingAnchor.constraint(equalTo: control.trailingAnchor, constant: Self.indicatorSpacing),
                mark.trailingAnchor.constraint(equalTo: trailingAnchor),
                mark.centerYAnchor.constraint(equalTo: control.centerYAnchor),
                mark.widthAnchor.constraint(equalToConstant: Self.indicatorSize),
                mark.heightAnchor.constraint(equalToConstant: Self.indicatorSize),
            ]
        } else {
            constraints.append(control.trailingAnchor.constraint(equalTo: trailingAnchor))
        }
        NSLayoutConstraint.activate(constraints)
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

    /// The type icon: the system's for the claimed (or guessed) type, 14 px.
    private static func icon(for a: Attachment) -> NSImage {
        let image = NSWorkspace.shared.icon(for: chipIconType(a))
        image.size = NSSize(width: 14, height: 14)
        return image
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

    @objc private func clicked(_ sender: Any?) {
        switch control.selectedSegment {
        case 0:
            primary()
        case 1:
            showMenu()
        default:
            break
        }
    }

    /// The first segment's click: view an attached message in its own
    /// window, preview anything else, executables included (Quick Look
    /// never runs them).
    private func primary() {
        if nested {
            onView?()
        } else {
            onPreview?()
        }
    }

    /// The arrow's menu (attachments.go `chipMenu`).
    private func showMenu() {
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
        let save = NSMenuItem(title: mn(L10n.T("Save _As…")), action: #selector(saveItem(_:)), keyEquivalent: "")
        save.target = self
        menu.addItem(save)
        menu.popUp(positioning: nil, at: NSPoint(x: 0, y: -2), in: control)
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
}

/// The button after the chips that saves all of them into one folder
/// (attachments.go `buildSaveAll`): flat, as dense as the chips beside it.
@MainActor
final class SaveAllChipView: NSButton {
    var onClick: (@MainActor () -> Void)?

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        title = mn(L10n.T("Save _All"))
        image = Icon.image("document-save", size: .small)
        imagePosition = .imageLeading
        imageHugsTitle = true
        isBordered = false
        controlSize = .small
        font = Typo.chip
        target = self
        action = #selector(clicked(_:))
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    @objc private func clicked(_ sender: Any?) {
        onClick?()
    }
}
