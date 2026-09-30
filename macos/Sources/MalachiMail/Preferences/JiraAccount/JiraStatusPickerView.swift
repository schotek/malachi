// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The picker of the statuses that count as closed, as a row of the
/// Folders list of a Jira account's settings: under the title and its
/// explanation the statuses of the site by category (`Jira.statusGroups`),
/// each category under its name in the colour of its pills, each status
/// name a check box. The names come from the site and are shown as plain
/// text.
@MainActor
final class JiraStatusPickerView: NSView, PrefsGroupMember {
    /// A check box changed: its choice and whether it is ticked now.
    var onToggle: ((Jira.StatusChoice, Bool) -> Void)?

    private let titleLabel: NSTextField
    private let subtitleLabel: PrefsWrappingLabel
    private let body = prefsColumn(spacing: 10)
    private let problemLabel = PrefsWrappingLabel("", size: 11, color: .systemRed)

    private var groups: [Jira.StatusGroup] = []
    /// The choices in the order of their check boxes' tags.
    private var choices: [Jira.StatusChoice] = []
    private var checks: [NSButton] = []
    private var rowEnabled = true
    private var groupEnabled = true

    /// The row's own sensitivity.
    var isEnabled: Bool {
        get { rowEnabled }
        set {
            rowEnabled = newValue
            applyEnabled()
        }
    }

    init(title: String, subtitle: String) {
        titleLabel = NSTextField(labelWithString: title)
        titleLabel.font = .systemFont(ofSize: 13)
        titleLabel.lineBreakMode = .byTruncatingTail
        titleLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        subtitleLabel = PrefsWrappingLabel(subtitle, size: 11, color: .secondaryLabelColor)
        subtitleLabel.isHidden = subtitle.isEmpty
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false

        problemLabel.isHidden = true
        body.isHidden = true
        let inset = PreferenceRowView.inset
        let column = prefsColumn(spacing: 8, insets: NSEdgeInsets(top: 10, left: inset, bottom: 12, right: inset))
        prefsAddFilling(titleLabel, to: column)
        prefsAddFilling(subtitleLabel, to: column)
        column.setCustomSpacing(2, after: titleLabel)
        prefsAddFilling(body, to: column)
        prefsAddFilling(problemLabel, to: column)
        addSubview(column)
        NSLayoutConstraint.activate([
            column.topAnchor.constraint(equalTo: topAnchor),
            column.bottomAnchor.constraint(equalTo: bottomAnchor),
            column.leadingAnchor.constraint(equalTo: leadingAnchor),
            column.trailingAnchor.constraint(equalTo: trailingAnchor),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Shows the picker as the controller holds it.
    func apply(groups: [Jira.StatusGroup], problem: String) {
        if !sameChoices(groups, self.groups) {
            self.groups = groups
            rebuild()
        } else {
            self.groups = groups
            choices = groups.flatMap(\.choices)
            for (check, choice) in zip(checks, choices) {
                check.state = choice.selected ? .on : .off
            }
        }
        problemLabel.stringValue = problem
        problemLabel.isHidden = problem.isEmpty
    }

    func setGroupEnabled(_ enabled: Bool) {
        groupEnabled = enabled
        applyEnabled()
    }

    /// The same check boxes under the same titles: only what is ticked may
    /// differ.
    private func sameChoices(_ a: [Jira.StatusGroup], _ b: [Jira.StatusGroup]) -> Bool {
        guard a.count == b.count else { return false }
        for (x, y) in zip(a, b) {
            if x.category != y.category || x.title != y.title || x.choices.count != y.choices.count {
                return false
            }
            for (p, q) in zip(x.choices, y.choices) where p.name != q.name || p.ids != q.ids {
                return false
            }
        }
        return true
    }

    private func rebuild() {
        for v in body.arrangedSubviews {
            body.removeArrangedSubview(v)
            v.removeFromSuperview()
        }
        choices = []
        checks = []
        let on = rowEnabled && groupEnabled
        for group in groups {
            let pill = PillLabel()
            pill.font = .systemFont(ofSize: 11, weight: .medium)
            pill.stringValue = group.title
            IssuePill.paint(pill, IssuePill.colours(group.style), emphasized: false)
            let header = NSStackView(views: [pill])
            header.orientation = .horizontal
            header.alignment = .centerY

            let flow = FlowView(spacing: 14, lineSpacing: 6)
            for choice in group.choices {
                let check = NSButton(checkboxWithTitle: "", target: self, action: #selector(toggled(_:)))
                check.title = choice.name
                check.state = choice.selected ? .on : .off
                check.tag = choices.count
                check.isEnabled = on
                check.lineBreakMode = .byTruncatingTail
                // A status name comes from the site: a long one is cut and
                // the tooltip has it whole (statuses.go: 32 characters).
                check.toolTip = choice.name
                check.frame.size = check.fittingSize
                check.frame.size.width = min(check.frame.width, Self.maxCheckWidth(check.font))
                choices.append(choice)
                checks.append(check)
                flow.addView(check)
            }
            let section = prefsColumn(spacing: 6)
            prefsAddFilling(header, to: section)
            prefsAddFilling(flow, to: section)
            prefsAddFilling(section, to: body)
        }
        body.isHidden = groups.isEmpty
    }

    private func applyEnabled() {
        let on = rowEnabled && groupEnabled
        titleLabel.textColor = on ? .labelColor : .tertiaryLabelColor
        subtitleLabel.textColor = on ? .secondaryLabelColor : .tertiaryLabelColor
        for check in checks {
            check.isEnabled = on
        }
    }

    /// The widest a check box gets: the box and 32 characters of its font.
    private static func maxCheckWidth(_ font: NSFont?) -> CGFloat {
        let font = font ?? .systemFont(ofSize: NSFont.systemFontSize)
        let text = String(repeating: "0", count: 32) as NSString
        return ceil(text.size(withAttributes: [.font: font]).width) + 22
    }

    @objc private func toggled(_ sender: NSButton) {
        guard choices.indices.contains(sender.tag) else { return }
        onToggle?(choices[sender.tag], sender.state == .on)
    }
}
