// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import UniformTypeIdentifiers

/// One attachment as a tile, the same under the headers of a message and
/// in a compose window: a card of fixed width with the system's 32 pt
/// icon for the file's type, the name on the first line (its middle elided
/// to the width, so the extension stays visible) and the size on the
/// second, and a trailing button (the arrow of the actions' menu, or the ✕
/// of a compose window). A click on the card runs `onClick`, a right-click
/// shows `contextMenu`; with full keyboard access the card takes the focus
/// and Space or Return click it. Names are server data and are shown as
/// plain text; the whole name is the tooltip unless the caller sets one.
@MainActor
class AttachmentTileView: NSView {
    static let width: CGFloat = 216
    static let height: CGFloat = 46
    static let iconSize: CGFloat = 32
    static let cornerRadius: CGFloat = 6

    let iconView = NSImageView()
    let nameLabel = NSTextField(labelWithString: "")
    let sizeLabel = NSTextField(labelWithString: "")
    /// The size and whatever follows it (the server symbol or a spinner).
    let detailRow = NSStackView()
    /// The trailing button, nil for a tile without one.
    let accessory: NSButton?

    var onClick: (@MainActor () -> Void)?
    /// The menu of a right-click, nil for none.
    var contextMenu: (@MainActor () -> NSMenu?)?

    /// A disabled tile is dimmed and ignores clicks; its tooltip says why.
    var isEnabled = true {
        didSet {
            guard isEnabled != oldValue else { return }
            alphaValue = isEnabled ? 1 : 0.55
            accessory?.isEnabled = isEnabled
            needsDisplay = true
        }
    }

    private var hovering = false
    private var pressed = false
    private var tracking: NSTrackingArea?

    init(icon: NSImage, name: String, size: String, accessory: NSButton?) {
        self.accessory = accessory
        super.init(frame: NSRect(x: 0, y: 0, width: Self.width, height: Self.height))
        translatesAutoresizingMaskIntoConstraints = false

        icon.size = NSSize(width: Self.iconSize, height: Self.iconSize)
        iconView.image = icon
        iconView.imageScaling = .scaleProportionallyUpOrDown
        iconView.translatesAutoresizingMaskIntoConstraints = false

        nameLabel.stringValue = name
        nameLabel.font = Typo.body
        nameLabel.lineBreakMode = .byTruncatingMiddle
        nameLabel.maximumNumberOfLines = 1
        nameLabel.cell?.truncatesLastVisibleLine = true
        nameLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)

        sizeLabel.stringValue = size
        sizeLabel.font = Typo.caption
        sizeLabel.textColor = Tint.secondary
        sizeLabel.isHidden = size.isEmpty

        detailRow.orientation = .horizontal
        detailRow.spacing = 4
        detailRow.alignment = .centerY
        detailRow.addArrangedSubview(sizeLabel)

        let texts = NSStackView(views: [nameLabel, detailRow])
        texts.orientation = .vertical
        texts.alignment = .leading
        texts.spacing = 1
        texts.translatesAutoresizingMaskIntoConstraints = false

        addSubview(iconView)
        addSubview(texts)
        let width = widthAnchor.constraint(equalToConstant: Self.width)
        width.priority = .init(999)
        let height = heightAnchor.constraint(equalToConstant: Self.height)
        height.priority = .init(999)
        var constraints = [
            width, height,
            iconView.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 8),
            iconView.centerYAnchor.constraint(equalTo: centerYAnchor),
            iconView.widthAnchor.constraint(equalToConstant: Self.iconSize),
            iconView.heightAnchor.constraint(equalToConstant: Self.iconSize),
            texts.leadingAnchor.constraint(equalTo: iconView.trailingAnchor, constant: 8),
            texts.centerYAnchor.constraint(equalTo: centerYAnchor),
        ]
        if let accessory {
            accessory.translatesAutoresizingMaskIntoConstraints = false
            accessory.isBordered = false
            accessory.imagePosition = .imageOnly
            accessory.refusesFirstResponder = !NSApp.isFullKeyboardAccessEnabled
            accessory.contentTintColor = Tint.secondary
            addSubview(accessory)
            constraints += [
                accessory.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -6),
                accessory.centerYAnchor.constraint(equalTo: centerYAnchor),
                accessory.widthAnchor.constraint(equalToConstant: 20),
                accessory.heightAnchor.constraint(equalToConstant: 20),
                texts.trailingAnchor.constraint(equalTo: accessory.leadingAnchor, constant: -4),
            ]
        } else {
            constraints.append(texts.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -8))
        }
        NSLayoutConstraint.activate(constraints)

        toolTip = name
        setAccessibilityElement(true)
        setAccessibilityRole(.button)
        setAccessibilityLabel(size.isEmpty ? name : name + ", " + size)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// The system's icon for the type of `filename` (or `contentType` when
    /// the name has no known extension).
    static func icon(filename: String, contentType: String) -> NSImage {
        let ext = (filename as NSString).pathExtension
        let type = UTType(filenameExtension: ext) ?? UTType(mimeType: contentType) ?? .data
        return NSWorkspace.shared.icon(for: type)
    }

    // MARK: Drawing

    override var isFlipped: Bool { true }

    private var card: NSBezierPath {
        NSBezierPath(roundedRect: bounds.insetBy(dx: 0.5, dy: 0.5), xRadius: Self.cornerRadius, yRadius: Self.cornerRadius)
    }

    override func draw(_ dirtyRect: NSRect) {
        let path = card
        Tint.cardFill.setFill()
        path.fill()
        if isEnabled, onClick != nil, pressed || hovering {
            Tint.fg(alpha: pressed ? 0.1 : 0.05).setFill()
            path.fill()
        }
        Tint.cardBorder.setStroke()
        path.lineWidth = 1
        path.stroke()
    }

    override func drawFocusRingMask() {
        card.fill()
    }

    override var focusRingMaskBounds: NSRect { bounds }

    // MARK: Mouse

    override func updateTrackingAreas() {
        super.updateTrackingAreas()
        if let tracking {
            removeTrackingArea(tracking)
        }
        let area = NSTrackingArea(rect: .zero, options: [.mouseEnteredAndExited, .activeInActiveApp, .inVisibleRect], owner: self, userInfo: nil)
        addTrackingArea(area)
        tracking = area
    }

    override func mouseEntered(with event: NSEvent) {
        hovering = true
        needsDisplay = true
    }

    override func mouseExited(with event: NSEvent) {
        hovering = false
        needsDisplay = true
    }

    override func mouseDown(with event: NSEvent) {
        guard isEnabled else { return }
        pressed = true
        needsDisplay = true
    }

    override func mouseDragged(with event: NSEvent) {
        guard isEnabled else { return }
        let inside = bounds.contains(convert(event.locationInWindow, from: nil))
        if inside != pressed {
            pressed = inside
            needsDisplay = true
        }
    }

    override func mouseUp(with event: NSEvent) {
        guard isEnabled else { return }
        let fire = pressed && bounds.contains(convert(event.locationInWindow, from: nil))
        pressed = false
        needsDisplay = true
        if fire {
            onClick?()
        }
    }

    override func menu(for event: NSEvent) -> NSMenu? {
        guard isEnabled else { return nil }
        return contextMenu?()
    }

    // MARK: Keyboard

    override var acceptsFirstResponder: Bool { isEnabled }

    override var canBecomeKeyView: Bool { isEnabled && NSApp.isFullKeyboardAccessEnabled }

    override func keyDown(with event: NSEvent) {
        switch event.charactersIgnoringModifiers {
        case " ", "\r":
            onClick?()
        default:
            super.keyDown(with: event)
        }
    }

    override func accessibilityPerformPress() -> Bool {
        guard isEnabled else { return false }
        onClick?()
        return true
    }
}
