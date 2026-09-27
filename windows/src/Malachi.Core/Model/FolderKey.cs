// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/FolderKey.swift; GTK:
// ui/internal/window/model.go (folderKey), collapse.go (collapseSep,
// encodeFolderKey, decodeFolderKey). Swift's failable init(encoded:) is
// Decode, which returns null for an entry it rejects.

using System;
using Malachi.Core.Api;

namespace Malachi.Core.Model;

/// <summary>
/// A folder across accounts. The stored form (<see cref="Encoded"/>) joins
/// the two ids with a slash, as ui/internal/window/collapse.go does for the
/// collapsed and favourite lists: the ids are generated tokens ("acc_" /
/// "f_" plus hex), so the separator cannot occur inside one.
/// </summary>
/// <param name="Account">The account.</param>
/// <param name="Folder">The folder within it.</param>
public readonly record struct FolderKey(AccountId Account, FolderId Folder)
{
    /// <summary>The separator of the stored form (collapse.go <c>collapseSep</c>).</summary>
    public const string Separator = "/";

    /// <summary>The key as one stored entry (collapse.go <c>encodeFolderKey</c>).</summary>
    public string Encoded => Account.ToString() + Separator + Folder.ToString();

    /// <summary>
    /// Parses one stored entry (collapse.go <c>decodeFolderKey</c>): the text
    /// up to the first separator is the account, the rest the folder;
    /// anything that is not two non-empty halves is rejected (null).
    /// </summary>
    public static FolderKey? Decode(string encoded)
    {
        if (encoded is null)
        {
            return null;
        }
        var cut = encoded.IndexOf(Separator, StringComparison.Ordinal);
        if (cut < 0)
        {
            return null;
        }
        var acc = encoded[..cut];
        var folder = encoded[(cut + Separator.Length)..];
        if (acc.Length == 0 || folder.Length == 0)
        {
            return null;
        }
        return new FolderKey(acc, folder);
    }
}
