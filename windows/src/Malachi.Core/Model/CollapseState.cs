// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/CollapseState.swift; GTK:
// ui/internal/window/collapse.go (collapseState, newCollapseState,
// folderCollapsed, accountCollapsed, setFolder, setAccount, loadCollapse,
// save; toggleFolder and toggleAccount without the window's part).
//
// Swift's struct is a class here: the model owns one and changes it in
// place, as Swift changes model.collapsed; Clone takes the copy a Swift
// assignment would make.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Settings;

namespace Malachi.Core.Model;

/// <summary>
/// Which parts of the folder sidebar the user folded away. This is pure
/// presentation: it changes nothing about the mail and no other client
/// cares, so it lives in the app's own settings rather than in the daemon.
/// A missing entry means expanded, which is what a fresh profile gets. The
/// <c>savingCollapse</c> guard against reacting to one's own write belongs
/// to the controller, as it does to the GTK window.
/// </summary>
public sealed class CollapseState
{
    /// <summary>An empty state: everything expanded (collapse.go <c>newCollapseState</c>).</summary>
    public CollapseState()
    {
    }

    /// <summary>A state with the given folders and accounts folded.</summary>
    public CollapseState(IEnumerable<FolderKey> folders, IEnumerable<AccountId> accounts)
    {
        ArgumentNullException.ThrowIfNull(folders);
        ArgumentNullException.ThrowIfNull(accounts);
        Folders.UnionWith(folders);
        Accounts.UnionWith(accounts);
    }

    /// <summary>The folded folders.</summary>
    public HashSet<FolderKey> Folders { get; } = [];

    /// <summary>The folded accounts.</summary>
    public HashSet<AccountId> Accounts { get; } = [];

    /// <summary>Whether the folder hides its children.</summary>
    public bool FolderCollapsed(FolderKey k) => Folders.Contains(k);

    /// <summary>Whether the account hides its folders.</summary>
    public bool AccountCollapsed(AccountId id) => Accounts.Contains(id);

    /// <summary>Folds <paramref name="k"/> away or unfolds it.</summary>
    public void SetFolder(FolderKey k, bool collapsed)
    {
        if (collapsed)
        {
            Folders.Add(k);
        }
        else
        {
            Folders.Remove(k);
        }
    }

    /// <summary>Folds an account's whole tree away or unfolds it.</summary>
    public void SetAccount(AccountId id, bool collapsed)
    {
        if (collapsed)
        {
            Accounts.Add(id);
        }
        else
        {
            Accounts.Remove(id);
        }
    }

    /// <summary>
    /// The state change of the window's <c>toggleFolder</c>: folds a
    /// folder's children away, or brings them back. Persisting and
    /// rebuilding the sidebar are the controller's part.
    /// </summary>
    public void ToggleFolder(FolderKey k) => SetFolder(k, !FolderCollapsed(k));

    /// <summary>The state change of the window's <c>toggleAccount</c>.</summary>
    public void ToggleAccount(AccountId id) => SetAccount(id, !AccountCollapsed(id));

    /// <summary>An independent copy (what a Swift assignment of the struct makes).</summary>
    public CollapseState Clone() => new(Folders, Accounts);

    /// <summary>
    /// Reads the stored state (collapse.go <c>loadCollapse</c>). Entries that
    /// do not parse are dropped: the list is data from disk, possibly written
    /// by another version.
    /// </summary>
    public static CollapseState Load(SettingsStore s)
    {
        ArgumentNullException.ThrowIfNull(s);
        var c = new CollapseState();
        foreach (var e in s.CollapsedFolders)
        {
            if (FolderKey.Decode(e) is { } k)
            {
                c.Folders.Add(k);
            }
        }
        foreach (var e in s.CollapsedAccounts)
        {
            var id = (e ?? "").Trim();
            if (id.Length > 0)
            {
                c.Accounts.Add(id);
            }
        }
        return c;
    }

    /// <summary>
    /// Writes the state back (collapse.go <c>save</c>), dropping entries of
    /// accounts that are gone. Folders are kept even when their account
    /// currently has no folder list: folder.list may simply have failed or
    /// the account may be switched off, and losing the tree's shape over that
    /// would be worse than a stale entry. An empty account list means "not
    /// loaded yet", not "no accounts": nothing is pruned against it. The
    /// stored lists are sorted (Go's byte order) so a no-op save does not
    /// look like a change to other windows.
    /// </summary>
    public void Save(SettingsStore s, IReadOnlyList<Account> accounts)
    {
        ArgumentNullException.ThrowIfNull(s);
        ArgumentNullException.ThrowIfNull(accounts);
        var known = accounts.Select(a => a.Id).ToHashSet();
        var prune = accounts.Count > 0;
        string[] folderEntries =
        [
            .. Folders
                .Where(k => !prune || known.Contains(k.Account))
                .Select(k => k.Encoded)
                .Order(CodePoints.Comparer),
        ];
        string[] accountEntries =
        [
            .. Accounts
                .Where(id => !prune || known.Contains(id))
                .Select(id => id.ToString())
                .Order(CodePoints.Comparer),
        ];
        s.CollapsedFolders = folderEntries;
        s.CollapsedAccounts = accountEntries;
    }
}
