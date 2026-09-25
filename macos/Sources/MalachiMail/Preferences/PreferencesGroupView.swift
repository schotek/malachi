// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// A member of a group's boxed list that follows the group's sensitivity
/// (a GTK child follows its parent's; its own stays separate).
@MainActor
protocol PrefsGroupMember: AnyObject {
    func setGroupEnabled(_ enabled: Bool)
}

/// A label that wraps and reports the height its width needs, for stack
/// views (AppKit needs `preferredMaxLayoutWidth` for that).
@MainActor
final class PrefsWrappingLabel: NSTextField {
    init(_ text: String, size: CGFloat = 13, weight: NSFont.Weight = .regular, color: NSColor = .labelColor, alignment: NSTextAlignment = .left) {
        super.init(frame: .zero)
        stringValue = text
        isEditable = false
        isSelectable = false
        isBezeled = false
        drawsBackground = false
        lineBreakMode = .byWordWrapping
        maximumNumberOfLines = 0
        usesSingleLineMode = false
        cell?.wraps = true
        cell?.isScrollable = false
        font = .systemFont(ofSize: size, weight: weight)
        textColor = color
        self.alignment = alignment
        setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        setContentHuggingPriority(.defaultLow, for: .horizontal)
        setContentCompressionResistancePriority(.required, for: .vertical)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func setFrameSize(_ newSize: NSSize) {
        super.setFrameSize(newSize)
        followWidth(newSize.width)
    }

    override func layout() {
        super.layout()
        followWidth(bounds.width)
    }

    /// The wrapping width follows the width the layout gave the label, so
    /// its intrinsic height is the height of the wrapped text.
    private func followWidth(_ width: CGFloat) {
        guard width > 0, preferredMaxLayoutWidth != width else { return }
        preferredMaxLayoutWidth = width
        invalidateIntrinsicContentSize()
    }
}

/// A 1 px line in `.separatorColor`, the divider between rows of a boxed
/// list.
@MainActor
final class PrefsDivider: NSView {
    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        wantsLayer = true
        heightAnchor.constraint(equalToConstant: 1).isActive = true
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func updateLayer() {
        layer?.backgroundColor = NSColor.separatorColor.cgColor
    }
}

/// An `AdwPreferencesGroup`: a bold title, an optional description, an
/// optional view at the header's trailing end, and a "boxed list" body: a
/// rounded box with 1 px dividers between its rows.
@MainActor
final class PreferencesGroupView: NSView {
    private let titleLabel: NSTextField
    private let descriptionLabel: PrefsWrappingLabel
    private let header = NSView()
    private let hasSuffix: Bool
    private let box = NSBox()
    private let rowsStack: NSStackView
    private var members: [PrefsGroupMember] = []

    /// The text under the title; empty hides it.
    var descriptionText: String {
        didSet { applyDescription() }
    }

    /// `set_sensitive` of the group: every row follows.
    var isEnabled = true {
        didSet {
            guard isEnabled != oldValue else { return }
            alphaValue = isEnabled ? 1 : 0.5
            for m in members {
                m.setGroupEnabled(isEnabled)
            }
        }
    }

    init(title: String? = nil, description: String? = nil, headerSuffix: NSView? = nil) {
        titleLabel = NSTextField(labelWithString: title ?? "")
        titleLabel.font = .systemFont(ofSize: 13, weight: .bold)
        titleLabel.lineBreakMode = .byTruncatingTail
        titleLabel.setContentHuggingPriority(.defaultLow, for: .horizontal)
        titleLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        descriptionLabel = PrefsWrappingLabel(description ?? "", color: .secondaryLabelColor)
        descriptionText = description ?? ""

        // The header: the text column flush left, the suffix at the end,
        // the text taking the width between them.
        let textStack = prefsColumn(spacing: 4)
        prefsAddFilling(titleLabel, to: textStack)
        prefsAddFilling(descriptionLabel, to: textStack)
        header.translatesAutoresizingMaskIntoConstraints = false
        header.addSubview(textStack)
        var headerConstraints = [
            textStack.leadingAnchor.constraint(equalTo: header.leadingAnchor),
            textStack.topAnchor.constraint(greaterThanOrEqualTo: header.topAnchor),
            textStack.bottomAnchor.constraint(lessThanOrEqualTo: header.bottomAnchor),
            textStack.centerYAnchor.constraint(equalTo: header.centerYAnchor),
        ]
        if let headerSuffix {
            headerSuffix.translatesAutoresizingMaskIntoConstraints = false
            headerSuffix.setContentHuggingPriority(.required, for: .horizontal)
            headerSuffix.setContentCompressionResistancePriority(.required, for: .horizontal)
            header.addSubview(headerSuffix)
            headerConstraints += [
                headerSuffix.trailingAnchor.constraint(equalTo: header.trailingAnchor),
                headerSuffix.centerYAnchor.constraint(equalTo: header.centerYAnchor),
                headerSuffix.topAnchor.constraint(greaterThanOrEqualTo: header.topAnchor),
                headerSuffix.bottomAnchor.constraint(lessThanOrEqualTo: header.bottomAnchor),
                textStack.trailingAnchor.constraint(equalTo: headerSuffix.leadingAnchor, constant: -12),
            ]
        } else {
            headerConstraints.append(textStack.trailingAnchor.constraint(equalTo: header.trailingAnchor))
        }
        NSLayoutConstraint.activate(headerConstraints)
        hasSuffix = headerSuffix != nil
        header.isHidden = (title ?? "").isEmpty && (description ?? "").isEmpty && headerSuffix == nil

        rowsStack = prefsColumn(spacing: 0)

        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false

        box.boxType = .custom
        box.titlePosition = .noTitle
        box.cornerRadius = 12
        box.borderWidth = 1
        box.borderColor = .separatorColor
        box.fillColor = .controlBackgroundColor
        box.contentViewMargins = .zero
        box.translatesAutoresizingMaskIntoConstraints = false
        if let content = box.contentView {
            content.wantsLayer = true
            content.layer?.cornerRadius = 12
            content.layer?.masksToBounds = true
            rowsStack.translatesAutoresizingMaskIntoConstraints = false
            content.addSubview(rowsStack)
            NSLayoutConstraint.activate([
                rowsStack.topAnchor.constraint(equalTo: content.topAnchor),
                rowsStack.bottomAnchor.constraint(equalTo: content.bottomAnchor),
                rowsStack.leadingAnchor.constraint(equalTo: content.leadingAnchor),
                rowsStack.trailingAnchor.constraint(equalTo: content.trailingAnchor),
            ])
        }

        let stack = prefsColumn(spacing: 12)
        prefsAddFilling(header, to: stack)
        prefsAddFilling(box, to: stack)
        addSubview(stack)
        NSLayoutConstraint.activate([
            stack.topAnchor.constraint(equalTo: topAnchor),
            stack.bottomAnchor.constraint(equalTo: bottomAnchor),
            stack.leadingAnchor.constraint(equalTo: leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: trailingAnchor),
        ])
        applyDescription()
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Replaces the rows of the boxed list; dividers go between them. A
    /// hidden row and its divider are taken out of the layout.
    func setRows(_ rows: [NSView]) {
        for v in rowsStack.arrangedSubviews {
            rowsStack.removeArrangedSubview(v)
            v.removeFromSuperview()
        }
        members = []
        for (i, row) in rows.enumerated() {
            if i > 0 {
                prefsAddFilling(PrefsDivider(), to: rowsStack)
            }
            prefsAddFilling(row, to: rowsStack)
            if let m = row as? PrefsGroupMember {
                m.setGroupEnabled(isEnabled)
                members.append(m)
            }
        }
        box.isHidden = rows.isEmpty
    }

    /// Shows or hides a row; the dividers follow so that exactly one sits
    /// between any two visible rows.
    func setRow(_ row: NSView, hidden: Bool) {
        guard rowsStack.arrangedSubviews.contains(row) else { return }
        row.isHidden = hidden
        updateDividers()
    }

    private func updateDividers() {
        let views = rowsStack.arrangedSubviews
        var previousVisible = false
        for (i, v) in views.enumerated() {
            if v is PrefsDivider {
                v.isHidden = true
                continue
            }
            guard !v.isHidden else { continue }
            if previousVisible, i > 0 {
                views[i - 1].isHidden = false
            }
            previousVisible = true
        }
    }

    private func applyDescription() {
        descriptionLabel.stringValue = descriptionText
        descriptionLabel.isHidden = descriptionText.isEmpty
        header.isHidden = titleLabel.stringValue.isEmpty && descriptionText.isEmpty && !hasSuffix
    }
}
