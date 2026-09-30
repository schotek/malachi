// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The status pill of the issue card (`IssueCardView`): the status as
/// plain text on a rounded fill in the category's colours (`IssuePill`),
/// like the list's `PillLabel`, and, when the account can change the
/// status (`Capability.transition`), a menu button: a chevron after the
/// text, the pointing hand, and a click (or Space and Return while it has
/// the keyboard) calls `onClick`, which pops the Change Status menu up
/// under it (`IssueTransitionMenu`). While a transition runs `busy` shows
/// a small spinner in place of the chevron and the pill stops taking
/// clicks. The text is `stringValue` on a label, never markup.
@MainActor
final class IssueStatusPill: NSView {
    /// The pill was clicked (only while `menuIndicator` is on and not
    /// `busy`).
    var onClick: (@MainActor () -> Void)?

    private let label = NSTextField(labelWithString: "")
    private let chevron = NSImageView()
    /// A mini spinner (a `Spinner` is 16 pt, taller than the pill).
    private let spinner = NSProgressIndicator()
    private var fill: NSColor = Tint.fg(alpha: 0.1) {
        didSet { needsDisplay = true }
    }
    private var trackingArea: NSTrackingArea?

    /// The horizontal padding inside the fill (`RowMetrics.badgePadding`).
    private static let padding: CGFloat = RowMetrics.badgePadding
    private static let verticalPadding: CGFloat = 1

    var text: String {
        get { label.stringValue }
        set { label.stringValue = newValue }
    }

    /// Shows the chevron and takes clicks.
    var menuIndicator = false {
        didSet {
            guard menuIndicator != oldValue else { return }
            applyState()
        }
    }

    /// A transition runs: the spinner instead of the chevron, no clicks.
    var busy = false {
        didSet {
            guard busy != oldValue else { return }
            applyState()
        }
    }

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        label.font = Typo.caption
        label.isSelectable = false
        label.lineBreakMode = .byClipping
        label.maximumNumberOfLines = 1
        label.alignment = .center
        label.setContentHuggingPriority(.required, for: .horizontal)
        label.setContentCompressionResistancePriority(.required, for: .horizontal)
        chevron.image = Icon.symbol("chevron.down", size: .small)
        chevron.imageScaling = .scaleProportionallyDown
        chevron.isHidden = true
        chevron.setContentHuggingPriority(.required, for: .horizontal)
        spinner.style = .spinning
        spinner.controlSize = .mini
        spinner.isIndeterminate = true
        spinner.isDisplayedWhenStopped = false
        spinner.translatesAutoresizingMaskIntoConstraints = false
        spinner.isHidden = true
        let stack = NSStackView(views: [label, chevron, spinner])
        stack.orientation = .horizontal
        stack.alignment = .centerY
        stack.spacing = 3
        stack.translatesAutoresizingMaskIntoConstraints = false
        addSubview(stack)
        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo: leadingAnchor, constant: Self.padding),
            stack.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -Self.padding),
            stack.topAnchor.constraint(equalTo: topAnchor, constant: Self.verticalPadding),
            stack.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -Self.verticalPadding),
            spinner.widthAnchor.constraint(equalToConstant: 12),
            spinner.heightAnchor.constraint(equalToConstant: 12),
        ])
        setContentHuggingPriority(.required, for: .horizontal)
        setContentCompressionResistancePriority(.required, for: .horizontal)
        setAccessibilityElement(true)
        applyState()
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// The colours of the category (`IssuePill.colours`).
    func paint(_ c: IssuePill.Colours) {
        label.textColor = c.text
        chevron.contentTintColor = c.text
        fill = c.fill
    }

    /// The accessible name ("Status"); the status itself is the value.
    func setLabel(_ name: String) {
        setAccessibilityLabel(name)
    }

    private func applyState() {
        let clickable = menuIndicator && !busy
        chevron.isHidden = !menuIndicator || busy
        spinner.isHidden = !busy
        if busy {
            spinner.startAnimation(nil)
        } else {
            spinner.stopAnimation(nil)
        }
        setAccessibilityRole(menuIndicator ? .popUpButton : .staticText)
        setAccessibilityEnabled(clickable)
        window?.invalidateCursorRects(for: self)
    }

    override var acceptsFirstResponder: Bool { menuIndicator }

    override func draw(_ dirtyRect: NSRect) {
        let radius = bounds.height / 2
        fill.setFill()
        NSBezierPath(roundedRect: bounds, xRadius: radius, yRadius: radius).fill()
        if window?.firstResponder === self, menuIndicator {
            NSColor.keyboardFocusIndicatorColor.setStroke()
            let path = NSBezierPath(roundedRect: bounds.insetBy(dx: 0.5, dy: 0.5), xRadius: radius, yRadius: radius)
            path.lineWidth = 1
            path.stroke()
        }
    }

    override func resetCursorRects() {
        if menuIndicator, !busy {
            addCursorRect(bounds, cursor: .pointingHand)
        }
    }

    override func mouseDown(with event: NSEvent) {
        guard menuIndicator, !busy else {
            super.mouseDown(with: event)
            return
        }
        onClick?()
    }

    override func keyDown(with event: NSEvent) {
        // Space and Return open the menu, as on a pop-up button.
        if menuIndicator, !busy, event.charactersIgnoringModifiers == " " || event.keyCode == 36 {
            onClick?()
            return
        }
        super.keyDown(with: event)
    }

    override func becomeFirstResponder() -> Bool {
        needsDisplay = true
        return super.becomeFirstResponder()
    }

    override func resignFirstResponder() -> Bool {
        needsDisplay = true
        return super.resignFirstResponder()
    }

    override func accessibilityValue() -> Any? {
        label.stringValue
    }

    override func accessibilityPerformPress() -> Bool {
        guard menuIndicator, !busy else { return false }
        onClick?()
        return true
    }
}
