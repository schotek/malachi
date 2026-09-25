// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

// What the preferences window needs from the application shell, as plain
// closures so the shell's own alert and toast types stay out of here; the
// lead wires the shell's implementations.

/// The strings of the "Remove this account?" confirmation
/// (widget/rpc.go `ConfirmDestructiveExtra` with the check button of
/// accounts_page.go `removeAccount`). Heading and body are plain text; the
/// labels have their mnemonics already stripped.
struct PrefsConfirmation: Sendable {
    let heading: String
    let body: String
    let confirmLabel: String
    let extraLabel: String
    let extraDefault: Bool
}

/// Asks the confirmation as a sheet on `window` (nil: application-modal)
/// and answers whether the user confirmed and whether the extra check box
/// ("Also delete drafts and downloaded data") was on.
typealias PrefsConfirmRemoval = @MainActor (NSWindow?, PrefsConfirmation) async -> (confirmed: Bool, deleteLocalData: Bool)
