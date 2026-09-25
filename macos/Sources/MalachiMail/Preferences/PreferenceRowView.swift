// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// An `AdwActionRow`: an optional prefix, a title with an optional subtitle,
/// and a trailing control at the row's end. The text column takes the
/// width between the prefix and the control (so a subtitle wraps at the
/// right width); with `trailingFills` the trailing view takes the spare
/// width instead (an entry row: title left, field right).
///
/// The row is laid out with explicit constraints rather than a horizontal
/// stack view: `NSStackView.Distribution.fill` hands spare width out by
/// priority ties, which is not deterministic across rows.
@MainActor
final class PreferenceRowView: NSView, PrefsGroupMember {
    /// Row heights of libadwaita's action rows.
    static let minHeight: CGFloat = 46
    static let minHeightWithSubtitle: CGFloat = 58
    /// The horizontal padding and the gap between the parts.
    static let inset: CGFloat = 12
    static let verticalInset: CGFloat = 8

    private let titleLabel: NSTextField
    private let subtitleLabel: PrefsWrappingLabel
    private var heightConstraint: NSLayoutConstraint?
    let trailing: NSView?
    private var rowEnabled = true
    private var groupEnabled = true

    var title: String {
        get { titleLabel.stringValue }
        set { titleLabel.stringValue = newValue }
    }

    var subtitle: String {
        get { subtitleLabel.stringValue }
        set {
            subtitleLabel.stringValue = newValue
            subtitleLabel.isHidden = newValue.isEmpty
            heightConstraint?.constant = newValue.isEmpty ? PreferenceRowView.minHeight : PreferenceRowView.minHeightWithSubtitle
        }
    }

    /// Lets the subtitle be selected and copied (`subtitle-selectable`).
    func setSubtitleSelectable() {
        subtitleLabel.isSelectable = true
    }

    /// The row's own sensitivity.
    var isEnabled: Bool {
        get { rowEnabled }
        set {
            rowEnabled = newValue
            applyEnabled()
        }
    }

    init(title: String, subtitle: String? = nil, prefix: NSView? = nil, trailing: NSView? = nil, trailingFills: Bool = false) {
        titleLabel = NSTextField(labelWithString: title)
        titleLabel.font = .systemFont(ofSize: 13)
        titleLabel.lineBreakMode = .byTruncatingTail
        subtitleLabel = PrefsWrappingLabel(subtitle ?? "", size: 11, color: .secondaryLabelColor)
        subtitleLabel.isHidden = (subtitle ?? "").isEmpty
        self.trailing = trailing
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false

        let inset = PreferenceRowView.inset
        let vInset = PreferenceRowView.verticalInset

        // The text column: its labels span its width; the column's width is
        // fixed by the constraints below, so the labels wrap or truncate.
        let textStack = prefsColumn(spacing: 2)
        prefsAddFilling(titleLabel, to: textStack)
        prefsAddFilling(subtitleLabel, to: textStack)
        // Who yields when the text and the control compete for width: the
        // text (it truncates and wraps), unless the control is the entry
        // that is meant to take the spare width.
        titleLabel.setContentHuggingPriority(trailingFills ? .defaultHigh : .defaultLow, for: .horizontal)
        titleLabel.setContentCompressionResistancePriority(trailingFills ? .defaultHigh : .defaultLow, for: .horizontal)
        subtitleLabel.setContentHuggingPriority(.defaultLow, for: .horizontal)
        subtitleLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        addSubview(textStack)

        var constraints: [NSLayoutConstraint] = [
            textStack.centerYAnchor.constraint(equalTo: centerYAnchor),
            textStack.topAnchor.constraint(greaterThanOrEqualTo: topAnchor, constant: vInset),
            textStack.bottomAnchor.constraint(lessThanOrEqualTo: bottomAnchor, constant: -vInset),
        ]

        var textLeading = leadingAnchor
        if let prefix {
            prefix.translatesAutoresizingMaskIntoConstraints = false
            prefix.setContentHuggingPriority(.required, for: .horizontal)
            prefix.setContentCompressionResistancePriority(.required, for: .horizontal)
            addSubview(prefix)
            constraints += [
                prefix.leadingAnchor.constraint(equalTo: leadingAnchor, constant: inset),
                prefix.centerYAnchor.constraint(equalTo: centerYAnchor),
            ]
            textLeading = prefix.trailingAnchor
        }
        constraints.append(textStack.leadingAnchor.constraint(equalTo: textLeading, constant: inset))

        if let trailing {
            trailing.translatesAutoresizingMaskIntoConstraints = false
            addSubview(trailing)
            if trailingFills {
                trailing.setContentHuggingPriority(.defaultLow, for: .horizontal)
                constraints.append(trailing.widthAnchor.constraint(greaterThanOrEqualToConstant: 160))
            } else {
                trailing.setContentHuggingPriority(.required, for: .horizontal)
                trailing.setContentCompressionResistancePriority(.required, for: .horizontal)
                // A stack view has no intrinsic width for the hugging to
                // act on: once its only control is hidden it kept the
                // control's width and squeezed the text (the Trust
                // Certificate… slot of a test result row). It shrinks to
                // what it shows, ahead of the text's own hugging.
                let shrink = trailing.widthAnchor.constraint(equalToConstant: 0)
                shrink.priority = NSLayoutConstraint.Priority(NSLayoutConstraint.Priority.defaultLow.rawValue + 1)
                constraints.append(shrink)
            }
            constraints += [
                trailing.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -inset),
                trailing.centerYAnchor.constraint(equalTo: centerYAnchor),
                trailing.topAnchor.constraint(greaterThanOrEqualTo: topAnchor, constant: vInset),
                trailing.bottomAnchor.constraint(lessThanOrEqualTo: bottomAnchor, constant: -vInset),
                textStack.trailingAnchor.constraint(equalTo: trailing.leadingAnchor, constant: -inset),
            ]
        } else {
            constraints.append(textStack.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -inset))
        }

        let h = heightAnchor.constraint(greaterThanOrEqualToConstant: (subtitle ?? "").isEmpty ? PreferenceRowView.minHeight : PreferenceRowView.minHeightWithSubtitle)
        constraints.append(h)
        // The natural height of an action row: without it the height is
        // only bounded below, and a row that once held a longer text kept
        // the spare height of a stretched page.
        let hug = heightAnchor.constraint(equalToConstant: 0)
        hug.priority = NSLayoutConstraint.Priority(1)
        constraints.append(hug)
        NSLayoutConstraint.activate(constraints)
        heightConstraint = h
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    func setGroupEnabled(_ enabled: Bool) {
        groupEnabled = enabled
        applyEnabled()
    }

    private func applyEnabled() {
        let on = rowEnabled && groupEnabled
        titleLabel.textColor = on ? .labelColor : .tertiaryLabelColor
        subtitleLabel.textColor = on ? .secondaryLabelColor : .tertiaryLabelColor
        if let trailing {
            setControls(in: trailing, enabled: on)
        }
    }

    private func setControls(in view: NSView, enabled: Bool) {
        if let c = view as? NSControl {
            c.isEnabled = enabled
        }
        for sub in view.subviews {
            setControls(in: sub, enabled: enabled)
        }
    }
}

/// A numeric field with a stepper, the `AdwSpinRow` control: an integer
/// between `min` and `max` in steps of `step`. Its width is its content's;
/// it never takes a row's spare width.
@MainActor
final class PrefsSpinControl: NSView, NSTextFieldDelegate {
    let field = NSTextField()
    let stepper = NSStepper()
    let minimum: Int
    let maximum: Int
    /// Called on every change made by the user.
    var onChange: ((Int) -> Void)?

    var value: Int {
        get { stepper.integerValue }
        set {
            let v = clamp(newValue)
            stepper.integerValue = v
            field.integerValue = v
        }
    }

    var isEnabled: Bool {
        get { stepper.isEnabled }
        set {
            stepper.isEnabled = newValue
            field.isEnabled = newValue
        }
    }

    init(min: Int, max: Int, step: Int = 1, value: Int, digits: Int = 3) {
        minimum = min
        maximum = max
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        stepper.minValue = Double(min)
        stepper.maxValue = Double(max)
        stepper.increment = Double(step)
        stepper.valueWraps = false
        stepper.target = self
        stepper.action = #selector(stepped(_:))
        // No formatter: a formatter that refuses the text keeps the field
        // editing; the value is parsed and clamped in `commitField` instead.
        field.alignment = .right
        field.font = .monospacedDigitSystemFont(ofSize: 13, weight: .regular)
        field.delegate = self
        field.target = self
        field.action = #selector(edited(_:))
        field.translatesAutoresizingMaskIntoConstraints = false
        stepper.translatesAutoresizingMaskIntoConstraints = false
        addSubview(field)
        addSubview(stepper)
        NSLayoutConstraint.activate([
            field.widthAnchor.constraint(equalToConstant: CGFloat(24 + digits * 9)),
            field.leadingAnchor.constraint(equalTo: leadingAnchor),
            field.centerYAnchor.constraint(equalTo: centerYAnchor),
            field.topAnchor.constraint(greaterThanOrEqualTo: topAnchor),
            field.bottomAnchor.constraint(lessThanOrEqualTo: bottomAnchor),
            stepper.leadingAnchor.constraint(equalTo: field.trailingAnchor, constant: 4),
            stepper.trailingAnchor.constraint(equalTo: trailingAnchor),
            stepper.centerYAnchor.constraint(equalTo: centerYAnchor),
            stepper.topAnchor.constraint(greaterThanOrEqualTo: topAnchor),
            stepper.bottomAnchor.constraint(lessThanOrEqualTo: bottomAnchor),
            heightAnchor.constraint(greaterThanOrEqualTo: stepper.heightAnchor),
            heightAnchor.constraint(greaterThanOrEqualTo: field.heightAnchor),
        ])
        stepper.setContentHuggingPriority(.required, for: .horizontal)
        stepper.setContentCompressionResistancePriority(.required, for: .horizontal)
        self.value = value
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    private func clamp(_ v: Int) -> Int {
        Swift.min(Swift.max(v, minimum), maximum)
    }

    @objc private func stepped(_ sender: Any?) {
        field.integerValue = stepper.integerValue
        onChange?(stepper.integerValue)
    }

    @objc private func edited(_ sender: Any?) {
        commitField()
    }

    func controlTextDidChange(_ obj: Foundation.Notification) {
        // Follow the typing while it is a number in range; the rest is
        // settled when editing ends.
        guard let v = Int(field.stringValue.trimmingCharacters(in: .whitespaces)), v == clamp(v) else { return }
        stepper.integerValue = v
        onChange?(v)
    }

    func controlTextDidEndEditing(_ obj: Foundation.Notification) {
        commitField()
    }

    private func commitField() {
        let v = clamp(Int(field.stringValue.trimmingCharacters(in: .whitespaces)) ?? stepper.integerValue)
        let changed = v != stepper.integerValue
        stepper.integerValue = v
        field.integerValue = v
        if changed {
            onChange?(v)
        }
    }
}
