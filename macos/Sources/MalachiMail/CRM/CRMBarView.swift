// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The bar under the header of a message saved in eWay-CRM (docs/eway.md
/// §2.5 `BarFor`, the approved wireframe; GTK `window/crm_bar.go`), as the
/// Mac's banner card like the bulk-mail strip: a check mark, "Saved in
/// eWay-CRM" over the names of the linked items (and the private hint when
/// the saved item is private), and Open in eWay-CRM, which opens the saved
/// item's URL in the browser at once. The URL is the daemon's, built from
/// the account's service address and the item's GUID (D15), so no "Open
/// This Link?" question is asked (a deviation from the mail's links,
/// macos/README.md). Every name is plain text (`stringValue`). Shown in the
/// reading pane, the message window and the conversation's cards, never
/// for an attached message.
@MainActor
final class CRMBarView: NSView {
    private let symbol = CalloutCard.symbolView()
    private let titleLabel = NSTextField(labelWithString: "")
    private let namesLabel = NSTextField(wrappingLabelWithString: "")
    private let noteLabel = NSTextField(wrappingLabelWithString: "")
    private let button = NSButton(title: "", target: nil, action: nil)
    private var url = ""
    private(set) var bar: CRM.Bar?

    /// Opens a URL the daemon built (`NSWorkspace`); installed by the view
    /// that owns the bar.
    var openURL: (@MainActor (String) -> Void)?

    init(margins: NSEdgeInsets = CalloutCard.margins) {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        isHidden = true
        CalloutCard.show(Icon.crmSavedSymbol, .info, in: symbol)
        titleLabel.font = Typo.body
        titleLabel.isSelectable = false
        namesLabel.font = .systemFont(ofSize: 12)
        namesLabel.textColor = .secondaryLabelColor
        namesLabel.isSelectable = false
        namesLabel.maximumNumberOfLines = 3
        namesLabel.cell?.truncatesLastVisibleLine = true
        noteLabel.font = .systemFont(ofSize: 11)
        noteLabel.textColor = .secondaryLabelColor
        noteLabel.isSelectable = false
        for l in [titleLabel, namesLabel, noteLabel] {
            l.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
            l.setContentHuggingPriority(.defaultLow, for: .horizontal)
        }
        button.bezelStyle = .rounded
        button.controlSize = .small
        button.font = NSFont.systemFont(ofSize: NSFont.systemFontSize(for: .small))
        button.target = self
        button.action = #selector(openClicked(_:))
        button.setContentHuggingPriority(.required, for: .horizontal)
        button.setContentCompressionResistancePriority(.required, for: .horizontal)
        let text = NSStackView(views: [titleLabel, namesLabel, noteLabel])
        text.orientation = .vertical
        text.alignment = .leading
        text.spacing = 1
        // The text takes the width between the symbol and the button, so
        // the button sits at the card's trailing edge, centred on the two
        // lines (owner, 2026-10-17); `.gravityAreas`, the default, would
        // pack it right after the text.
        text.setContentHuggingPriority(.defaultLow, for: .horizontal)
        text.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        let stack = NSStackView(views: [symbol, text, button])
        stack.orientation = .horizontal
        stack.alignment = .centerY
        stack.distribution = .fill
        stack.spacing = CalloutCard.spacing
        CalloutCard.install(stack, in: self, margins: margins)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Shows the bar of `record` (`CRM.barFor`), or hides it for none.
    func show(_ record: CrmRecord?) {
        let b = CRM.barFor(record)
        guard b != bar else { return }
        bar = b
        guard let b else {
            isHidden = true
            return
        }
        titleLabel.stringValue = b.title
        namesLabel.stringValue = b.names
        namesLabel.toolTip = b.names.isEmpty ? nil : b.names
        namesLabel.isHidden = b.names.isEmpty
        noteLabel.stringValue = b.note
        noteLabel.isHidden = b.note.isEmpty
        url = b.url
        button.title = b.openLabel
        button.isHidden = b.url.isEmpty
        isHidden = false
    }

    @objc private func openClicked(_ sender: Any?) {
        guard !url.isEmpty else { return }
        openURL?(url)
    }

    /// Opens `url` (built by the daemon, https only: `CRM.openURL`) in the
    /// browser without a question.
    static func open(_ url: String) {
        let u = CRM.openURL(url)
        guard !u.isEmpty, let target = URL(string: u) else { return }
        NSWorkspace.shared.open(target)
    }
}

/// What every window does with its `CRMController`: the dialog as a sheet
/// on the window, the removal's question there too (Cancel / Remove from
/// eWay-CRM, destructive), the toasts in the window, the saved item opened
/// in the browser without a question.
@MainActor
enum CRMWindowSupport {
    static func configure(
        _ c: CRMController, state: AppState, window: @escaping @MainActor () -> NSWindow?,
        toast: @escaping @MainActor (String) -> Void
    ) {
        c.present = { session in
            guard let w = window() else {
                session.cancel()
                return
            }
            // A sheet up already (another dialog, a question): nothing new.
            if w.attachedSheet != nil {
                session.cancel()
                return
            }
            CRMDialogController.present(session, on: w)
        }
        let alerts = state.alerts
        c.confirm = { q in
            await alerts.confirmDestructive(on: window(), heading: q.heading, body: q.body, confirmLabel: mn(q.confirm))
        }
        c.toast = toast
        c.openURL = { CRMBarView.open($0) }
    }
}
