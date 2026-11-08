// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// The neutral capsule after an AI integration's name in Settings →
/// Integrations ("This computer", "API key"; `Integrations.badges`, GTK
/// widget/pill.go `NewSourcePill`): the look of the kind capsule after an
/// account's name in the sidebar (`FolderCellView`), hidden while `text`
/// is empty. The text is a translated label of this client, never from a
/// server.
@MainActor
func prefsSourcePill(_ text: String) -> PillLabel {
    let pill = PillLabel()
    pill.font = Typo.sidebarKindBadge
    pill.textColor = .secondaryLabelColor
    pill.stringValue = text
    pill.isHidden = text.isEmpty
    return pill
}

/// The page's banner: a callout card with a symbol and a wrapping text
/// (why a call failed, a plain-http warning), hidden while there is none.
/// The text is pinned between the symbol and the card's edge, so that it
/// wraps at the width of the page whatever width the label had while the
/// banner was hidden (the Jira account sheet's banner, shared since the AI
/// provider dialog needed the same).
@MainActor
final class PrefsBannerView: NSView {
    private let label = PrefsWrappingLabel("")
    private let symbolView = CalloutCard.symbolView()

    init(symbol: String = "exclamationmark.triangle.fill", severity: CalloutSeverity = .warning) {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        setContentHuggingPriority(.required, for: .vertical)
        CalloutCard.show(symbol, severity, in: symbolView)
        let content = NSView()
        symbolView.translatesAutoresizingMaskIntoConstraints = false
        label.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(symbolView)
        content.addSubview(label)
        NSLayoutConstraint.activate([
            symbolView.leadingAnchor.constraint(equalTo: content.leadingAnchor),
            symbolView.centerYAnchor.constraint(equalTo: content.centerYAnchor),
            symbolView.topAnchor.constraint(greaterThanOrEqualTo: content.topAnchor),
            symbolView.bottomAnchor.constraint(lessThanOrEqualTo: content.bottomAnchor),
            label.leadingAnchor.constraint(equalTo: symbolView.trailingAnchor, constant: CalloutCard.spacing),
            label.trailingAnchor.constraint(equalTo: content.trailingAnchor),
            label.topAnchor.constraint(equalTo: content.topAnchor),
            label.bottomAnchor.constraint(equalTo: content.bottomAnchor),
        ])
        CalloutCard.install(content, in: self)
        isHidden = true
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Shows the banner with `text`, or hides it for nil.
    func reveal(_ text: String?) {
        if let text {
            label.stringValue = text
            isHidden = false
        } else {
            isHidden = true
        }
    }
}
