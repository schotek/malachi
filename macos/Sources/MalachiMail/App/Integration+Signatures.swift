// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The shell's part of signature verification (docs/api.md §4.22, plan
/// §6): a verdict is the daemon's, so all that happens here is hearing that
/// trust changed. notify.signaturesChanged makes the messages on display
/// ask message.get again (their badge turns; `MessageCache.refreshSignatures`
/// tells every view through `onLoaded`) and wakes the open details popover
/// and Settings → Security's key list (`SignatureState`). The rows whose
/// verdict changed arrive as notify.messagesChanged like any other change
/// of a message.
extension Integration {
    func wireSignatures() {
        let signatures = state.signatures
        _ = state.notifications.addSignaturesChanged { [weak self] in
            self?.cache.refreshSignatures()
            signatures.changed()
        }
    }
}
