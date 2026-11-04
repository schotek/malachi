// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/FavouriteState.swift; GTK:
// ui/internal/window/favourites.go (favouriteState, newFavouriteState, has,
// set, loadFavourites, save; toggleFavourite without the window's part).
//
// Swift's struct is a class here, as CollapseState: the model owns one and
// changes it in place; Clone takes the copy a Swift assignment would make.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Settings;

namespace Malachi.Core.Model;

/// <summary>
/// Which folders the user pinned to the Favourites section at the top of
/// the sidebar. Like the folds this is pure presentation, kept in the app's
/// own settings and encoded with the same account/folder keys. The section
/// lists the pins in tree order (<see cref="FolderTree.SortFolders"/>), so
/// the set carries no order of its own.
/// </summary>
public sealed class FavouriteState
{
    /// <summary>An empty set (favourites.go <c>newFavouriteState</c>).</summary>
    public FavouriteState()
    {
    }

    /// <summary>A set with <paramref name="folders"/> pinned.</summary>
    public FavouriteState(IEnumerable<FolderKey> folders)
    {
        ArgumentNullException.ThrowIfNull(folders);
        Folders.UnionWith(folders);
    }

    /// <summary>The pinned folders.</summary>
    public HashSet<FolderKey> Folders { get; } = [];

    /// <summary>How many folders are pinned.</summary>
    public int Count => Folders.Count;

    /// <summary>Whether nothing is pinned.</summary>
    public bool IsEmpty => Folders.Count == 0;

    /// <summary>Whether <paramref name="k"/> is pinned.</summary>
    public bool Has(FolderKey k) => Folders.Contains(k);

    /// <summary>Pins <paramref name="k"/> or unpins it.</summary>
    public void Set(FolderKey k, bool on)
    {
        if (on)
        {
            Folders.Add(k);
        }
        else
        {
            Folders.Remove(k);
        }
    }

    /// <summary>The state change of the window's <c>toggleFavourite</c>.</summary>
    public void Toggle(FolderKey k) => Set(k, !Has(k));

    /// <summary>An independent copy (what a Swift assignment of the struct makes).</summary>
    public FavouriteState Clone() => new(Folders);

    /// <summary>
    /// Reads the stored set (favourites.go <c>loadFavourites</c>). Entries
    /// that do not parse are dropped.
    /// </summary>
    public static FavouriteState Load(SettingsStore s)
    {
        ArgumentNullException.ThrowIfNull(s);
        var f = new FavouriteState();
        foreach (var e in s.FavouriteFolders)
        {
            if (FolderKey.Decode(e) is { } k)
            {
                f.Folders.Add(k);
            }
        }
        return f;
    }

    /// <summary>
    /// Writes the set back (favourites.go <c>save</c>), sorted (Go's byte
    /// order) so a no-op save does not look like a change to other windows,
    /// and without the entries of accounts that are gone. An empty account
    /// list means "not loaded yet" and prunes nothing (as
    /// <see cref="CollapseState.Save"/>). A folder that no longer resolves is
    /// kept: folder.list may have failed, the account may be switched off,
    /// and the pin is worth more than the stale entry costs.
    /// </summary>
    public void Save(SettingsStore s, IReadOnlyList<Account> accounts)
    {
        ArgumentNullException.ThrowIfNull(s);
        ArgumentNullException.ThrowIfNull(accounts);
        var known = accounts.Select(a => a.Id).ToHashSet();
        var prune = accounts.Count > 0;
        s.FavouriteFolders =
        [
            .. Folders
                .Where(k => !prune || known.Contains(k.Account))
                .Select(k => k.Encoded)
                .Order(CodePoints.Comparer),
        ];
    }
}
