// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The formatting bar over the editor (compose.blp `format_toolbar`,
/// compose.go `wireToolbar`/`applyState`): bold, italic, underline; the
/// paragraph style and the alignment; lists and quote; a link popover, the
/// text colour, an image and Clear Formatting. Every control refuses the
/// focus so commands act on the page selection; every command goes to
/// `exec`. The bar mirrors the formatting at the caret through
/// `applyState` without echoing it back as commands (`syncing`).
@MainActor
final class FormatToolbar: NSStackView {
    static let height: CGFloat = 34

    /// editor.Exec.
    var exec: (@MainActor (String, String?) -> Void)?
    /// editor.GrabFocus after a menu choice.
    var focusEditor: (@MainActor () -> Void)?
    /// The image button (`compose.insert-image`).
    var onInsertImage: (@MainActor () -> Void)?

    /// The formatting at the caret as last reported.
    private(set) var state = EditorState()
    /// The bar is being updated from the page, not by the user.
    private var syncing = false

    let boldButton: NSButton
    let italicButton: NSButton
    let underlineButton: NSButton
    let blockButton = NSPopUpButton(frame: .zero, pullsDown: true)
    let alignButton = NSPopUpButton(frame: .zero, pullsDown: false)
    let ulButton: NSButton
    let olButton: NSButton
    let quoteButton: NSButton
    let linkButton: NSButton
    let colorWell = NSColorWell(style: .minimal)
    let imageButton: NSButton
    let clearButton: NSButton

    private let linkPopover = NSPopover()
    private let linkEntry = NSTextField()
    private let linkApply: NSButton

    private static let blocks = ["p", "h1", "h2", "h3"]
    private static let aligns = ["left", "center", "right"]

    init() {
        // macOS-only strings for the tooltips: the GTK msgids name Ctrl.
        boldButton = Self.toggle("format-text-bold", tooltip: "Bold (⌘B)") // macOS-only string
        italicButton = Self.toggle("format-text-italic", tooltip: "Italic (⌘I)") // macOS-only string
        underlineButton = Self.toggle("format-text-underline", tooltip: "Underline (⌘U)") // macOS-only string
        ulButton = Self.toggle("view-list-bullet", tooltip: L10n.T("Bulleted List"))
        olButton = Self.toggle("view-list-ordered", tooltip: L10n.T("Numbered List"))
        quoteButton = Self.toggle("format-indent-more", tooltip: L10n.T("Quote"))
        linkButton = Self.push("insert-link", tooltip: L10n.T("Insert Link"))
        imageButton = Self.push("insert-image", tooltip: L10n.T("Insert Image"))
        clearButton = Self.push("edit-clear", tooltip: L10n.T("Clear Formatting"))
        linkApply = NSButton(title: L10n.T("Insert"), target: nil, action: nil)
        super.init(frame: .zero)

        translatesAutoresizingMaskIntoConstraints = false
        orientation = .horizontal
        alignment = .centerY
        spacing = 2
        edgeInsets = NSEdgeInsets(top: 0, left: 6, bottom: 0, right: 6)
        heightAnchor.constraint(equalToConstant: Self.height).isActive = true

        // Paragraph style: a pull-down whose first item is the label.
        blockButton.isBordered = true
        blockButton.bezelStyle = .toolbar
        blockButton.refusesFirstResponder = true
        blockButton.toolTip = L10n.T("Paragraph Style")
        blockButton.font = Typo.body
        blockButton.addItem(withTitle: L10n.T("Paragraph"))
        for (i, title) in [L10n.T("Paragraph"), L10n.T("Heading 1"), L10n.T("Heading 2"), L10n.T("Heading 3")].enumerated() {
            let item = NSMenuItem(title: title, action: #selector(blockChosen(_:)), keyEquivalent: "")
            item.target = self
            item.representedObject = Self.blocks[i]
            blockButton.menu?.addItem(item)
        }
        blockButton.setContentHuggingPriority(.required, for: .horizontal)

        // Alignment: a pop-up showing the icon of the current alignment.
        alignButton.isBordered = true
        alignButton.bezelStyle = .toolbar
        alignButton.refusesFirstResponder = true
        alignButton.toolTip = L10n.T("Alignment")
        alignButton.imagePosition = .imageOnly
        for (i, title) in [L10n.T("Left"), L10n.T("Center"), L10n.T("Right")].enumerated() {
            let item = NSMenuItem(title: title, action: nil, keyEquivalent: "")
            item.image = Icon.image("format-justify-" + Self.aligns[i], size: .regular)
            item.representedObject = Self.aligns[i]
            alignButton.menu?.addItem(item)
        }
        alignButton.target = self
        alignButton.action = #selector(alignChosen(_:))
        alignButton.selectItem(at: 0)
        alignButton.setContentHuggingPriority(.required, for: .horizontal)

        // The link popover: an entry and Insert.
        linkEntry.placeholderString = "https://"
        linkEntry.font = Typo.body
        linkEntry.translatesAutoresizingMaskIntoConstraints = false
        linkEntry.widthAnchor.constraint(equalToConstant: 240).isActive = true
        linkEntry.setAccessibilityLabel(L10n.T("Insert Link"))
        linkApply.bezelStyle = .rounded
        linkApply.bezelColor = .controlAccentColor
        linkApply.keyEquivalent = "\r"
        linkApply.target = self
        linkApply.action = #selector(insertLink(_:))
        let linkRow = NSStackView(views: [linkEntry, linkApply])
        linkRow.orientation = .horizontal
        linkRow.distribution = .fill
        linkApply.setContentHuggingPriority(.required, for: .horizontal)
        linkEntry.setContentHuggingPriority(.defaultLow, for: .horizontal)
        linkRow.spacing = 6
        linkRow.edgeInsets = NSEdgeInsets(top: 6, left: 6, bottom: 6, right: 6)
        linkRow.translatesAutoresizingMaskIntoConstraints = false
        let linkController = NSViewController()
        let linkContent = NSView()
        linkContent.translatesAutoresizingMaskIntoConstraints = false
        linkContent.addSubview(linkRow)
        NSLayoutConstraint.activate([
            linkRow.topAnchor.constraint(equalTo: linkContent.topAnchor),
            linkRow.bottomAnchor.constraint(equalTo: linkContent.bottomAnchor),
            linkRow.leadingAnchor.constraint(equalTo: linkContent.leadingAnchor),
            linkRow.trailingAnchor.constraint(equalTo: linkContent.trailingAnchor),
        ])
        linkController.view = linkContent
        linkPopover.contentViewController = linkController
        linkPopover.behavior = .transient

        colorWell.controlSize = .small
        colorWell.supportsAlpha = false
        colorWell.refusesFirstResponder = true
        colorWell.toolTip = L10n.T("Text Colour")
        colorWell.target = self
        colorWell.action = #selector(colorChanged(_:))

        for b in [boldButton, italicButton, underlineButton, ulButton, olButton] {
            b.target = self
            b.action = #selector(toggled(_:))
        }
        quoteButton.target = self
        quoteButton.action = #selector(quoteToggled(_:))
        linkButton.target = self
        linkButton.action = #selector(showLink(_:))
        imageButton.target = self
        imageButton.action = #selector(imageClicked(_:))
        clearButton.target = self
        clearButton.action = #selector(clearClicked(_:))

        for v in [
            boldButton, italicButton, underlineButton, Self.separator(),
            blockButton, alignButton, Self.separator(),
            ulButton, olButton, quoteButton, Self.separator(),
            linkButton, colorWell, imageButton, clearButton,
        ] {
            addArrangedSubview(v)
        }
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    // MARK: State from the page

    /// applyState mirrors the formatting at the caret onto the toolbar.
    func applyState(_ st: EditorState) {
        syncing = true
        defer { syncing = false }
        state = st
        boldButton.state = st.bold ? .on : .off
        italicButton.state = st.italic ? .on : .off
        underlineButton.state = st.underline ? .on : .off
        ulButton.state = st.ul ? .on : .off
        olButton.state = st.ol ? .on : .off
        quoteButton.state = st.block == "blockquote" ? .on : .off

        var block = st.block
        var label = L10n.T("Paragraph")
        switch block {
        case "h1", "h2", "h3":
            label = L10n.T("Heading %s", String(block.dropFirst()))
        default:
            block = "p"
        }
        blockButton.item(at: 0)?.title = label
        for item in blockButton.itemArray.dropFirst() {
            item.state = (item.representedObject as? String) == block ? .on : .off
        }

        var align = st.align
        if align != "center", align != "right" {
            align = "left"
        }
        if let i = Self.aligns.firstIndex(of: align) {
            alignButton.selectItem(at: i)
        }
    }

    // MARK: Commands (the Format menu reaches these too)

    /// `compose.block`: a paragraph style by name (`p`, `h1`, `h2`, `h3`).
    func setBlock(_ block: String) {
        exec?("formatBlock", block)
        focusEditor?()
    }

    /// `compose.align`: `left`, `center` or `right`.
    func setAlign(_ align: String) {
        switch align {
        case "center":
            exec?("justifyCenter", nil)
        case "right":
            exec?("justifyRight", nil)
        default:
            exec?("justifyLeft", nil)
        }
        focusEditor?()
    }

    /// The quote toggle: into a blockquote, or out of one.
    func toggleQuote() {
        if state.block == "blockquote" {
            exec?("outdent", nil)
        } else {
            exec?("formatBlock", "blockquote")
        }
    }

    /// Clear Formatting: formatting and links.
    func clearFormatting() {
        exec?("removeFormat", nil)
        exec?("unlink", nil)
    }

    /// Opens the link popover under its button.
    func showLinkPopover() {
        guard !linkPopover.isShown else { return }
        linkEntry.textColor = .labelColor
        linkPopover.show(relativeTo: linkButton.bounds, of: linkButton, preferredEdge: .maxY)
        linkEntry.window?.makeFirstResponder(linkEntry)
    }

    /// Whether the link popover is up (Escape closes it first).
    var isLinkPopoverShown: Bool { linkPopover.isShown }

    // MARK: Actions

    @objc private func toggled(_ sender: NSButton) {
        guard !syncing else { return }
        switch sender {
        case boldButton: exec?("bold", nil)
        case italicButton: exec?("italic", nil)
        case underlineButton: exec?("underline", nil)
        case ulButton: exec?("insertUnorderedList", nil)
        case olButton: exec?("insertOrderedList", nil)
        default: break
        }
    }

    @objc private func quoteToggled(_ sender: NSButton) {
        guard !syncing else { return }
        if sender.state == .on {
            exec?("formatBlock", "blockquote")
        } else {
            exec?("outdent", nil)
        }
    }

    @objc private func blockChosen(_ sender: NSMenuItem) {
        guard !syncing, let block = sender.representedObject as? String else { return }
        setBlock(block)
    }

    @objc private func alignChosen(_ sender: NSPopUpButton) {
        guard !syncing, let align = sender.selectedItem?.representedObject as? String else { return }
        setAlign(align)
    }

    @objc private func showLink(_ sender: Any?) {
        showLinkPopover()
    }

    /// insertLink: an http, https or mailto URL goes to the page; anything
    /// else turns the entry red.
    @objc private func insertLink(_ sender: Any?) {
        guard let url = composeLinkURL(linkEntry.stringValue) else {
            linkEntry.textColor = .systemRed
            return
        }
        linkEntry.textColor = .labelColor
        exec?("createLink", url)
        linkEntry.stringValue = ""
        linkPopover.performClose(nil)
    }

    @objc private func colorChanged(_ sender: NSColorWell) {
        exec?("foreColor", Self.cssColor(sender.color))
    }

    @objc private func imageClicked(_ sender: Any?) {
        onInsertImage?()
    }

    @objc private func clearClicked(_ sender: Any?) {
        clearFormatting()
    }

    // MARK: Helpers

    /// The colour as `#rrggbb` (the GTK button hands over `rgb(r,g,b)`).
    static func cssColor(_ color: NSColor) -> String {
        let c = color.usingColorSpace(.sRGB) ?? color
        let r = Int((c.redComponent * 255).rounded())
        let g = Int((c.greenComponent * 255).rounded())
        let b = Int((c.blueComponent * 255).rounded())
        return String(format: "#%02x%02x%02x", r, g, b)
    }

    private static func toggle(_ icon: String, tooltip: String) -> NSButton {
        let b = NSButton(image: Icon.image(icon, size: .regular), target: nil, action: nil)
        b.setButtonType(.pushOnPushOff)
        b.bezelStyle = .toolbar
        b.imagePosition = .imageOnly
        b.imageScaling = .scaleProportionallyDown
        b.refusesFirstResponder = true
        b.toolTip = tooltip
        b.setAccessibilityLabel(tooltip)
        return b
    }

    private static func push(_ icon: String, tooltip: String) -> NSButton {
        let b = NSButton(image: Icon.image(icon, size: .regular), target: nil, action: nil)
        b.setButtonType(.momentaryPushIn)
        b.bezelStyle = .toolbar
        b.imagePosition = .imageOnly
        b.imageScaling = .scaleProportionallyDown
        b.refusesFirstResponder = true
        b.toolTip = tooltip
        b.setAccessibilityLabel(tooltip)
        return b
    }

    private static func separator() -> NSBox {
        let s = NSBox()
        s.boxType = .separator
        s.translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            s.widthAnchor.constraint(equalToConstant: 1),
            s.heightAnchor.constraint(equalToConstant: 18),
        ])
        return s
    }
}
