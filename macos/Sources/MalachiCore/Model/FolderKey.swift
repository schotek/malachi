// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// A folder across accounts (ui/internal/window/model.go `folderKey`).
///
/// The stored form (`encoded`) joins the two ids with a slash, as
/// ui/internal/window/collapse.go does for the collapsed and favourite lists:
/// the ids are generated tokens ("acc_" / "f_" plus hex), so the separator
/// cannot occur inside one.
public struct FolderKey: Hashable, Sendable {
    public var account: AccountID
    public var folder: FolderID

    public init(account: AccountID, folder: FolderID) {
        self.account = account
        self.folder = folder
    }

    /// The separator of the stored form (collapse.go `collapseSep`).
    public static let separator = "/"

    /// The key as one stored entry (collapse.go `encodeFolderKey`).
    public var encoded: String {
        account.rawValue + FolderKey.separator + folder.rawValue
    }

    /// Parses one stored entry (collapse.go `decodeFolderKey`): the text up
    /// to the first separator is the account, the rest the folder; anything
    /// that is not two non-empty halves is rejected.
    public init?(encoded: String) {
        guard let cut = encoded.firstIndex(of: "/") else { return nil }
        let acc = String(encoded[..<cut])
        let folder = String(encoded[encoded.index(after: cut)...])
        guard !acc.isEmpty, !folder.isEmpty else { return nil }
        self.init(account: AccountID(acc), folder: FolderID(folder))
    }
}
