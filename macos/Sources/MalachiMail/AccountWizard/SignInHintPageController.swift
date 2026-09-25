// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The wizard's page for an address of a provider whose sign-in belongs to
/// GNOME Online Accounts (account_wizard.blp `goa_hint_page`). GNOME
/// Online Accounts does not exist on macOS, so the page is a static notice
/// without the Open / Check Again buttons (deviation D12); the hint text
/// the controller delivers is not shown.
@MainActor
final class SignInHintPageController: NSViewController {
    override func loadView() {
        let page = WizardStatusPageView(
            illustration: .symbol("person.2"),
            title: "Sign-in not available", // macOS-only string
            description: "Gmail and Microsoft 365 accounts sign in through GNOME Online Accounts, which macOS does not have. They cannot be added here yet." // macOS-only string
        )
        let root = WizardPageView()
        root.addSubview(page)
        NSLayoutConstraint.activate([
            page.topAnchor.constraint(equalTo: root.topAnchor),
            page.bottomAnchor.constraint(equalTo: root.bottomAnchor),
            page.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            page.trailingAnchor.constraint(equalTo: root.trailingAnchor),
        ])
        view = root
    }
}
