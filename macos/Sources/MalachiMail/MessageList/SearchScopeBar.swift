// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// Where a search looks, over the list while one is on (window.blp
/// `search_scope`, the toggle group under the search entry in GTK): the
/// selected folder, its account, or every account, as Mail's scope bar
/// offers its mailboxes. Folder and Account need a selected folder; the
/// tooltips say what each searches (ListController `onSearchBar`).
@MainActor
final class SearchScopeBar: NSView {
    /// The user chose a scope.
    var onScope: (@MainActor (Settings.SearchScope) -> Void)?

    private static let scopes: [Settings.SearchScope] = [.folder, .account, .all]
    private static let margin: CGFloat = 6

    private let control = NSSegmentedControl()

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        control.segmentCount = Self.scopes.count
        control.trackingMode = .selectOne
        control.controlSize = .small
        control.font = NSFont.systemFont(ofSize: NSFont.systemFontSize(for: .small))
        control.segmentDistribution = .fillEqually
        control.setLabel(L10n.C("search scope", "Folder"), forSegment: 0)
        control.setLabel(L10n.C("search scope", "Account"), forSegment: 1)
        control.setLabel(L10n.C("search scope", "All Accounts"), forSegment: 2)
        control.target = self
        control.action = #selector(changed(_:))
        control.translatesAutoresizingMaskIntoConstraints = false
        addSubview(control)
        NSLayoutConstraint.activate([
            control.topAnchor.constraint(equalTo: topAnchor, constant: Self.margin),
            bottomAnchor.constraint(equalTo: control.bottomAnchor, constant: Self.margin),
            control.centerXAnchor.constraint(equalTo: centerXAnchor),
            control.leadingAnchor.constraint(greaterThanOrEqualTo: leadingAnchor, constant: Self.margin),
            trailingAnchor.constraint(greaterThanOrEqualTo: control.trailingAnchor, constant: Self.margin),
        ])
        isHidden = true
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Shows the bar for `state`, or hides it (nil: no search).
    func show(_ state: SearchBarState?) {
        guard let state else {
            isHidden = true
            return
        }
        isHidden = false
        control.selectedSegment = Self.scopes.firstIndex(of: state.scope) ?? 0
        control.setEnabled(state.narrowEnabled, forSegment: 0)
        control.setEnabled(state.narrowEnabled, forSegment: 1)
        control.setToolTip(state.folderTooltip, forSegment: 0)
        control.setToolTip(state.accountTooltip.isEmpty ? nil : state.accountTooltip, forSegment: 1)
        control.setToolTip(state.allTooltip, forSegment: 2)
    }

    @objc private func changed(_ sender: Any?) {
        let i = control.selectedSegment
        guard Self.scopes.indices.contains(i) else { return }
        onScope?(Self.scopes[i])
    }
}
