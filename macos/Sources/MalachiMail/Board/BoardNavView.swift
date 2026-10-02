// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The List style's navigation column: the board's filters (a state dot,
/// the title, the count) and below them the accounts (name, kind in a small
/// capsule, count). A stack of custom rows; each is a radio button for
/// VoiceOver. A click goes to `controller.setFilter` / `setAccount`; the
/// rows themselves are rebuilt from `controller.view` by `configure`.
@MainActor
final class BoardNavView: NSView {
    /// The second caption.
    private static var accountsCaption: String { Board.Text.accountsCaption }

    private let controller: BoardController
    private let scroll = NSScrollView()
    private let stack = NSStackView()
    private let document = BoardNavDocumentView()

    init(controller: BoardController) {
        self.controller = controller
        super.init(frame: .zero)

        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 1
        stack.translatesAutoresizingMaskIntoConstraints = false
        // The document is sized by constraints: left with its autoresizing
        // mask it keeps the zero frame it was made with (the mask's
        // constraints are required and win over the width below), and the
        // whole column collapses into its corner.
        document.translatesAutoresizingMaskIntoConstraints = false
        document.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.topAnchor.constraint(equalTo: document.topAnchor, constant: 8),
            stack.leadingAnchor.constraint(equalTo: document.leadingAnchor, constant: 8),
            stack.trailingAnchor.constraint(equalTo: document.trailingAnchor, constant: -8),
            document.bottomAnchor.constraint(equalTo: stack.bottomAnchor, constant: 8),
        ])

        // Just below required: folded away (zero wide) the column keeps
        // its rows' minimum width, clipped, instead of breaking a required
        // constraint.
        let documentWidth = document.trailingAnchor.constraint(equalTo: scroll.contentView.trailingAnchor)
        documentWidth.priority = NSLayoutConstraint.Priority(999)
        scroll.drawsBackground = false
        scroll.hasVerticalScroller = true
        scroll.autohidesScrollers = true
        scroll.borderType = .noBorder
        scroll.documentView = document
        scroll.translatesAutoresizingMaskIntoConstraints = false
        addSubview(scroll)
        NSLayoutConstraint.activate([
            scroll.topAnchor.constraint(equalTo: topAnchor),
            scroll.bottomAnchor.constraint(equalTo: bottomAnchor),
            scroll.leadingAnchor.constraint(equalTo: leadingAnchor),
            scroll.trailingAnchor.constraint(equalTo: trailingAnchor),
            document.topAnchor.constraint(equalTo: scroll.contentView.topAnchor),
            document.leadingAnchor.constraint(equalTo: scroll.contentView.leadingAnchor),
            documentWidth,
        ])
        setAccessibilityRole(.group)
        configure(controller.view)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Rebuilds the rows from the view model.
    func configure(_ view: Board.View) {
        for old in stack.arrangedSubviews {
            stack.removeArrangedSubview(old)
            old.removeFromSuperview()
        }
        addCaption(Board.Text.boardName)
        for item in view.nav {
            let row = BoardNavRow(
                title: item.title, count: item.count, dot: item.dot.map { BoardPalette.accent($0) }, badge: "",
                selected: item.selected
            ) { [weak self] in
                self?.controller.setFilter(item.filter)
            }
            add(row)
        }
        addCaption(Self.accountsCaption, spaceAbove: 14)
        for item in view.accounts {
            let row = BoardNavRow(
                title: item.title, count: item.count, dot: nil, badge: item.badge, selected: item.selected
            ) { [weak self] in
                self?.controller.setAccount(item.filter)
            }
            add(row)
        }
    }

    private func add(_ row: BoardNavRow) {
        stack.addArrangedSubview(row)
        row.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
    }

    private func addCaption(_ text: String, spaceAbove: CGFloat = 4) {
        let label = NSTextField(labelWithString: text)
        label.font = Typo.sidebarHeading
        label.textColor = .secondaryLabelColor
        label.translatesAutoresizingMaskIntoConstraints = false
        // The caption lines up with the row titles, inside the row's padding.
        let wrap = NSView()
        wrap.translatesAutoresizingMaskIntoConstraints = false
        wrap.addSubview(label)
        NSLayoutConstraint.activate([
            label.leadingAnchor.constraint(equalTo: wrap.leadingAnchor, constant: BoardNavRow.horizontalPadding),
            label.trailingAnchor.constraint(lessThanOrEqualTo: wrap.trailingAnchor),
            label.topAnchor.constraint(equalTo: wrap.topAnchor),
            wrap.bottomAnchor.constraint(equalTo: label.bottomAnchor, constant: 2),
        ])
        if let last = stack.arrangedSubviews.last {
            stack.setCustomSpacing(spaceAbove, after: last)
        }
        stack.addArrangedSubview(wrap)
        wrap.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
    }
}

/// The scroll view's document: flipped, so the rows start at the top.
private final class BoardNavDocumentView: NSView {
    override var isFlipped: Bool { true }
}

/// One row of the column: radio button, hover and selected look like a
/// sidebar row. The texts are plain strings of the view model.
@MainActor
private final class BoardNavRow: NSView {
    static let horizontalPadding: CGFloat = 8
    static let height: CGFloat = 28

    private let onSelect: () -> Void
    private let selected: Bool
    private var tracking: NSTrackingArea?
    private var hovered = false {
        didSet {
            if hovered != oldValue {
                needsDisplay = true
            }
        }
    }

    init(title: String, count: Int, dot: NSColor?, badge: String, selected: Bool, onSelect: @escaping () -> Void) {
        self.selected = selected
        self.onSelect = onSelect
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false

        let label = NSTextField(labelWithString: title)
        label.font = selected ? .systemFont(ofSize: Typo.bodySize, weight: .semibold) : Typo.body
        label.lineBreakMode = .byTruncatingTail
        label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        label.setContentHuggingPriority(.defaultLow, for: .horizontal)

        var items: [NSView] = []
        if let dot {
            let d = DotView()
            d.color = dot
            items.append(d)
        }
        items.append(label)
        if !badge.isEmpty {
            let pill = PillLabel()
            pill.font = Typo.sidebarKindBadge
            pill.textColor = .secondaryLabelColor
            pill.setText(badge, maxCharacters: 12)
            pill.toolTip = badge
            items.append(pill)
            // The kind stays beside the name; the gap goes before the count.
            let spacer = NSView()
            spacer.setContentHuggingPriority(NSLayoutConstraint.Priority(1), for: .horizontal)
            items.append(spacer)
        }
        if count > 0 {
            let countBadge = CountBadgeView(padding: 4)
            countBadge.text = String(count)
            countBadge.textColor = .secondaryLabelColor
            countBadge.fill = Tint.fg(alpha: 0.1)
            items.append(countBadge)
        }
        let line = NSStackView(views: items)
        line.orientation = .horizontal
        line.alignment = .centerY
        line.spacing = 6
        line.translatesAutoresizingMaskIntoConstraints = false
        addSubview(line)
        // A row is 28 pt tall whatever its content; the count sits at the
        // trailing edge and the title takes the slack (the stack fills the
        // row, the title hugs least).
        line.distribution = .fill
        NSLayoutConstraint.activate([
            line.leadingAnchor.constraint(equalTo: leadingAnchor, constant: Self.horizontalPadding),
            line.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -Self.horizontalPadding),
            line.centerYAnchor.constraint(equalTo: centerYAnchor),
            heightAnchor.constraint(equalToConstant: Self.height),
        ])

        setAccessibilityElement(true)
        setAccessibilityRole(.radioButton)
        setAccessibilityLabel(count > 0 ? "\(title), \(count)" : title)
        setAccessibilityValue(selected ? 1 : 0)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func draw(_ dirtyRect: NSRect) {
        let fill: NSColor?
        if selected {
            fill = Tint.fg(alpha: 0.1)
        } else if hovered {
            fill = Tint.fg(alpha: 0.05)
        } else {
            fill = nil
        }
        guard let fill else { return }
        fill.setFill()
        NSBezierPath(roundedRect: bounds, xRadius: 6, yRadius: 6).fill()
    }

    override func updateTrackingAreas() {
        super.updateTrackingAreas()
        if let tracking {
            removeTrackingArea(tracking)
        }
        let area = NSTrackingArea(
            rect: bounds, options: [.mouseEnteredAndExited, .activeInKeyWindow, .inVisibleRect], owner: self, userInfo: nil
        )
        addTrackingArea(area)
        tracking = area
    }

    override func mouseEntered(with event: NSEvent) {
        hovered = true
    }

    override func mouseExited(with event: NSEvent) {
        hovered = false
    }

    override func mouseDown(with event: NSEvent) {
        onSelect()
    }

    override func accessibilityPerformPress() -> Bool {
        onSelect()
        return true
    }
}
