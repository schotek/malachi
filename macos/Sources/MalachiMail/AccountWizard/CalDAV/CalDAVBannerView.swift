// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// The Add CalDAV Calendar sheet's error banner: a `CalloutCard` with the
/// warning symbol and the sentence, the label pinned from the symbol to the
/// card's trailing edge so it wraps at the card's width (a stack view's
/// gravity areas let a wrapping label collapse to a narrow column).
@MainActor
final class CalDAVBannerView: NSView {
    let label = PrefsWrappingLabel("")
    private let symbolView = CalloutCard.symbolView()
    private(set) var card: CalloutCard?

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        setContentHuggingPriority(.required, for: .vertical)
        CalloutCard.show("exclamationmark.triangle.fill", .warning, in: symbolView)
        let content = NSView()
        symbolView.translatesAutoresizingMaskIntoConstraints = false
        label.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(symbolView)
        content.addSubview(label)
        NSLayoutConstraint.activate([
            symbolView.leadingAnchor.constraint(equalTo: content.leadingAnchor),
            symbolView.topAnchor.constraint(equalTo: label.topAnchor),
            label.leadingAnchor.constraint(equalTo: symbolView.trailingAnchor, constant: CalloutCard.spacing),
            label.trailingAnchor.constraint(equalTo: content.trailingAnchor),
            label.topAnchor.constraint(equalTo: content.topAnchor),
            label.bottomAnchor.constraint(equalTo: content.bottomAnchor),
            content.bottomAnchor.constraint(greaterThanOrEqualTo: symbolView.bottomAnchor),
        ])
        card = CalloutCard.install(content, in: self, margins: NSEdgeInsets())
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
