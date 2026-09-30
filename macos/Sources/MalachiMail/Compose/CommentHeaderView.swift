// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The card over the editor of a compose window in comment mode
/// (`Jira.commentCompose`), in place of the header fields: "Comment on
/// KEY" (`Jira.commentTitle`), the issue's summary under it, and, on an
/// issue whose comments may be public or internal (a service-desk
/// request, `Jira.visibilityOptions`), the choice between a reply to the
/// customer and an internal note. There are no recipients, subject or
/// From: a comment goes to its issue, from the account it belongs to. The
/// key and the summary come from the Jira site and are plain text
/// (`stringValue`).
@MainActor
final class CommentHeaderView: NSBox {
    /// The user chose another visibility.
    var onVisibilityChanged: (@MainActor () -> Void)?

    private let titleLabel = NSTextField(labelWithString: "")
    private let summaryLabel = NSTextField(wrappingLabelWithString: "")
    private let visibilityControl: NSSegmentedControl
    private let options: [Jira.VisibilityOption]

    /// - Parameter comment: the issue and the visibility the draft carries.
    init(comment: DraftComment) {
        options = Jira.visibilityOptions(comment.issue)
        visibilityControl = NSSegmentedControl(
            labels: options.map(\.label), trackingMode: .selectOne, target: nil, action: nil)
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        boxType = .custom
        titlePosition = .noTitle
        cornerRadius = 12
        borderWidth = 1
        borderColor = Tint.cardBorder
        fillColor = Tint.cardFill
        contentViewMargins = .zero
        build(comment)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// The visibility chosen now: the draft's at first
    /// (`Jira.selectedVisibility`), public without a choice.
    var visibility: CommentVisibility {
        let i = visibilityControl.selectedSegment
        guard i >= 0, i < options.count else { return .public }
        return options[i].visibility
    }

    private func build(_ comment: DraftComment) {
        let title = Jira.commentTitle(comment.issue.key)
        titleLabel.stringValue = title
        titleLabel.font = Typo.heading
        titleLabel.textColor = .labelColor
        titleLabel.lineBreakMode = .byTruncatingTail
        titleLabel.maximumNumberOfLines = 1
        titleLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        titleLabel.setContentHuggingPriority(.defaultLow, for: .horizontal)

        // Two lines at most; `preferredMaxLayoutWidth` stays 0, so AppKit
        // wraps at the width the card gives it.
        summaryLabel.stringValue = Jira.clean(comment.issue.summary)
        summaryLabel.font = Typo.body
        summaryLabel.textColor = Tint.secondary
        summaryLabel.isSelectable = false
        summaryLabel.maximumNumberOfLines = 2
        summaryLabel.cell?.truncatesLastVisibleLine = true
        summaryLabel.isHidden = summaryLabel.stringValue.isEmpty
        summaryLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        summaryLabel.setContentHuggingPriority(.defaultLow, for: .horizontal)

        visibilityControl.segmentStyle = .rounded
        visibilityControl.controlSize = .regular
        visibilityControl.target = self
        visibilityControl.action = #selector(visibilityChosen(_:))
        visibilityControl.refusesFirstResponder = true
        visibilityControl.isHidden = options.isEmpty
        visibilityControl.setContentHuggingPriority(.required, for: .horizontal)
        visibilityControl.setContentCompressionResistancePriority(.required, for: .horizontal)
        let chosen = Jira.selectedVisibility(comment)
        if let i = options.firstIndex(where: { $0.visibility == chosen }) {
            visibilityControl.selectedSegment = i
        }
        for (i, o) in options.enumerated() {
            visibilityControl.setToolTip(o.label, forSegment: i)
        }

        let top = NSStackView(views: [titleLabel, visibilityControl])
        top.orientation = .horizontal
        top.alignment = .centerY
        top.distribution = .fill
        top.spacing = 12

        let stack = NSStackView(views: [top, summaryLabel])
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 4
        stack.translatesAutoresizingMaskIntoConstraints = false
        guard let content = contentView else { return }
        content.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.topAnchor.constraint(equalTo: content.topAnchor, constant: 10),
            content.bottomAnchor.constraint(equalTo: stack.bottomAnchor, constant: 10),
            stack.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: ComposeHeaderView.horizontalInset),
            content.trailingAnchor.constraint(equalTo: stack.trailingAnchor, constant: ComposeHeaderView.horizontalInset),
            top.widthAnchor.constraint(equalTo: stack.widthAnchor),
        ])
        if !summaryLabel.isHidden {
            // Wraps at the card's width instead of widening it.
            summaryLabel.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
        }
    }

    @objc private func visibilityChosen(_ sender: Any?) {
        onVisibilityChanged?()
    }
}
