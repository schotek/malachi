// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import UniformTypeIdentifiers

/// One attachment under the headers (attachments.go `buildChip`): a
/// two-segment control, the first with the type icon, the name and the size
/// (a click opens the attachment), the second with an arrow that offers
/// View (an attached message only), Open and Save As…. The actions close
/// over the attachment they were built for, so a chip never acts on a
/// message other than its own. An unavailable part leaves the chip
/// disabled with the reason as its tooltip; an attached message is viewed
/// in its own window on click; an executable keeps Open disabled and saves
/// on click instead (docs/security.md §4). Names and types are server data
/// and are shown as plain text; the name's middle is elided so the
/// extension stays visible, the whole name is the tooltip.
@MainActor
final class AttachmentChipView: NSSegmentedControl {
    let attachment: Attachment
    let available: Bool
    let nested: Bool
    let executable: Bool

    var onOpen: (@MainActor () -> Void)?
    var onSave: (@MainActor () -> Void)?
    var onView: (@MainActor () -> Void)?

    /// - Parameters:
    ///   - attachment: what the chip stands for.
    ///   - available: whether message.part can deliver it (`partAvailable`).
    ///   - why: the reason when it cannot, as the tooltip.
    init(attachment: Attachment, available: Bool, why: String) {
        self.attachment = attachment
        self.available = available
        nested = attachedMessage(attachment)
        executable = executableAttachment(filename: attachment.filename, contentType: attachment.contentType)
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        segmentCount = 2
        segmentStyle = .rounded
        trackingMode = .momentary
        controlSize = .small
        font = Typo.chip
        target = self
        action = #selector(clicked(_:))

        let name = chipName(attachment)
        var text = Self.middleEllipsis(name, max: chipNameChars)
        if attachment.size > 0 {
            text += "  " + formatSize(attachment.size)
        }
        setImage(Self.icon(for: attachment), forSegment: 0)
        setImageScaling(.scaleProportionallyDown, forSegment: 0)
        setLabel(text, forSegment: 0)
        setImage(Icon.image("pan-down", size: .small), forSegment: 1)
        setImageScaling(.scaleProportionallyDown, forSegment: 1)
        setToolTip(L10n.T("More Actions"), forSegment: 1)

        switch (available, nested, executable) {
        case (false, _, _):
            isEnabled = false
            setToolTip(why, forSegment: 0)
            setToolTip(why, forSegment: 1)
        case (true, true, _):
            // Rendered by the daemon, read-only: the safest thing to do
            // with it, whatever it is called.
            setToolTip(name, forSegment: 0)
        case (true, false, true):
            setToolTip(L10n.T("Programs and scripts are not opened directly; save the file and decide yourself."), forSegment: 0)
        default:
            setToolTip(name, forSegment: 0)
        }
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
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
        switch selectedSegment {
        case 0:
            primary()
        case 1:
            showMenu()
        default:
            break
        }
    }

    /// The first segment's click: view an attached message, save an
    /// executable, open anything else.
    private func primary() {
        if nested {
            onView?()
        } else if executable {
            onSave?()
        } else {
            onOpen?()
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
        menu.popUp(positioning: nil, at: NSPoint(x: 0, y: -2), in: self)
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
