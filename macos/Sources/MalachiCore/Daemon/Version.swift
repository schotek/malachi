// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// The build's version, baked into Info.plist by macos/Makefile.
public enum Version {
    /// The full `git describe` string (Info.plist `MalachiVersion`); `dev`
    /// when running unbundled.
    public static let full: String =
        Bundle.main.object(forInfoDictionaryKey: "MalachiVersion") as? String ?? "dev"

    /// `CFBundleShortVersionString`, one to three integers.
    public static let short: String =
        Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "0.0.0"
}
