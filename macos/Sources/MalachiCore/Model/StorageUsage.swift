// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// The texts of the Disk Space Used row of the settings (ui/internal/window/
// preferences.go `storageTexts`).

/// The row's value, the total size of the mail store, and its details, one
/// line each for a background conversion that runs or stopped, what
/// compression saves and what is kept on the mail server; a line only when
/// there is something to say, empty details when nothing applies
/// (preferences.go `storageTexts`).
public func storageTexts(_ r: SystemStorageResult) -> (value: String, details: String) {
    var lines: [String] = []
    if r.conversion == .running {
        lines.append(L10n.T("Converting the stored mail in the background"))
    } else if r.conversion == .noSpace {
        lines.append(L10n.T("Converting stopped: the disk is full"))
    }
    if r.savedBytes > 0 {
        // TRANSLATORS: %s is a size such as "1.2 GiB".
        lines.append(L10n.T("Compression saves %s", formatSize(r.savedBytes)))
    }
    if r.remoteAttachmentBytes > 0 {
        // TRANSLATORS: %s is a size such as "1.2 GiB".
        lines.append(L10n.T("%s of attachments are on the server only", formatSize(r.remoteAttachmentBytes)))
    }
    return (formatSize(r.totalBytes), lines.joined(separator: "\n"))
}
