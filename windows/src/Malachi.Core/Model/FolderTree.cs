// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/FolderTree.swift; GTK:
// ui/internal/window/model.go (maxFolderDepth, accountLabel, hasFlag,
// matchesFilter, enabledAccounts, visibleFolders, selfAddress, sortFolders,
// favouriteSection, folderTree, markSubtree, badgeFor, sortSiblings,
// roleRank, roleIcon, firstFolder) and folders.go (folderTitle,
// folderCountsText).
//
// Swift's free functions are the static members of this class. Swift's
// folderTree is FolderTreeRows: C# allows no member named like its type.
// sortSiblings orders by Go's rules (case folded rune by rune, compared by
// code points, a stable sort), the walks that have no depth cap
// (markSubtree, badgeFor) go without recursion, so that a hostile server's
// endless folder chain cannot exhaust the stack.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Text;

namespace Malachi.Core.Model;

/// <summary>The sidebar layout and the folder naming.</summary>
public static class FolderTree
{
    /// <summary>
    /// Bounds the sidebar tree; deeper (or cyclic) chains fall back to a
    /// depth derived from the display path (model.go <c>maxFolderDepth</c>).
    /// </summary>
    public const int MaxFolderDepth = 32;

    /// <summary>
    /// The sidebar header text for an account: its configured name, falling
    /// back to the address. Both are user-entered, shown as plain text
    /// (model.go <c>accountLabel</c>).
    /// </summary>
    public static string AccountLabel(Account a)
    {
        ArgumentNullException.ThrowIfNull(a);
        var name = (a.Config.Name ?? "").Trim();
        return name.Length > 0 ? name : (a.Config.Email ?? "").Trim();
    }

    /// <summary>Whether <paramref name="f"/> is in <paramref name="flags"/> (model.go <c>hasFlag</c>).</summary>
    public static bool HasFlag(IReadOnlyList<Flag> flags, Flag f)
    {
        ArgumentNullException.ThrowIfNull(flags);
        foreach (var x in flags)
        {
            if (x == f)
            {
                return true;
            }
        }
        return false;
    }

    /// <summary>
    /// Whether <paramref name="s"/> belongs in a list shown under
    /// <paramref name="f"/> (model.go <c>matchesFilter</c>). It mirrors what
    /// the backend selects, and is used for the one message the UI adds
    /// without asking (notify.newMessage). A row already on screen is left
    /// alone when a flag change makes it stop matching; the next load of the
    /// list applies the filter again.
    /// </summary>
    public static bool MatchesFilter(MessageSummary s, MessageFilter f)
    {
        ArgumentNullException.ThrowIfNull(s);
        return f.Value switch
        {
            MessageFilter.Unread => !HasFlag(s.Flags, Flag.Seen),
            MessageFilter.Flagged => HasFlag(s.Flags, Flag.Flagged),
            _ => true,
        };
    }

    /// <summary>The enabled accounts, keeping order (model.go <c>enabledAccounts</c>).</summary>
    public static IReadOnlyList<Account> EnabledAccounts(IEnumerable<Account> accounts)
    {
        ArgumentNullException.ThrowIfNull(accounts);
        return [.. accounts.Where(a => a.Enabled)];
    }

    /// <summary>
    /// The folders the sidebar lists: an empty outbox is dropped (the folder
    /// exists from the first send on; it only earns a row while something is
    /// in it). The model keeps the full list so <c>FolderByRole</c> still
    /// finds it (model.go <c>visibleFolders</c>).
    /// </summary>
    public static IReadOnlyList<Folder> VisibleFolders(IEnumerable<Folder> list)
    {
        ArgumentNullException.ThrowIfNull(list);
        return [.. list.Where(f => !(f.Role == FolderRole.Outbox && f.Total == 0))];
    }

    /// <summary>The account's own address, for Reply All exclusion (model.go <c>selfAddress</c>).</summary>
    public static Address SelfAddress(Account a)
    {
        ArgumentNullException.ThrowIfNull(a);
        return new Address { Name = a.Config.DisplayName, Email = a.Config.Email };
    }

    /// <summary>
    /// Lays the sidebar out (model.go <c>sortFolders</c>): the Favourites
    /// section when anything is pinned (<see cref="FavouriteSection"/>), then
    /// the enabled accounts in list order, each preceded by a header row when
    /// there are at least two of them or a Favourites section above them,
    /// then the account's folders as a tree. Roots are ordered by (roleRank,
    /// path); the children of a folder follow it, ordered the same way. Depth
    /// comes from <c>parentId</c>; folders whose parent chain is cyclic or
    /// deeper than <see cref="MaxFolderDepth"/> are appended after the tree
    /// with a depth counted from their display path. Non-selectable
    /// containers are kept so their children have somewhere to hang.
    /// </summary>
    public static IReadOnlyList<FolderEntry> SortFolders(
        IReadOnlyList<Account> accounts,
        IReadOnlyDictionary<AccountId, IReadOnlyList<Folder>> folders,
        CollapseState collapsed,
        FavouriteState favourites)
    {
        ArgumentNullException.ThrowIfNull(folders);
        ArgumentNullException.ThrowIfNull(collapsed);
        var enabled = EnabledAccounts(accounts);
        var output = new List<FolderEntry>(FavouriteSection(enabled, folders, favourites));
        // A single account needs no heading of its own, unless a Favourites
        // section sits above its folders and the two would run into each other.
        var headers = enabled.Count >= 2 || output.Count > 0;
        foreach (var a in enabled)
        {
            // Only a header can fold a whole account away, and headers only
            // exist from two accounts on.
            var folded = headers && collapsed.AccountCollapsed(a.Id);
            if (headers)
            {
                output.Add(new FolderEntry { Header = true, Account = a, HasChildren = true, Collapsed = folded });
            }
            if (folded)
            {
                continue;
            }
            output.AddRange(FolderTreeRows(a, FoldersOf(folders, a.Id), collapsed, favourites));
        }
        return output;
    }

    /// <summary>
    /// The block at the top of the sidebar (model.go <c>favouriteSection</c>):
    /// a heading and one row per pinned folder, in tree order (accounts in
    /// list order, then roles, then names), each at depth 0 and without its
    /// children. A pin that does not resolve (the folder gone or renamed on
    /// the server, its account switched off, a container that cannot be
    /// opened, an outbox with nothing in it) is left out silently; when none
    /// resolves there is no section at all. Account folds do not apply here:
    /// the section is what stays in view while the tree is folded away.
    /// </summary>
    public static IReadOnlyList<FolderEntry> FavouriteSection(
        IReadOnlyList<Account> enabled,
        IReadOnlyDictionary<AccountId, IReadOnlyList<Folder>> folders,
        FavouriteState favourites)
    {
        ArgumentNullException.ThrowIfNull(enabled);
        ArgumentNullException.ThrowIfNull(folders);
        ArgumentNullException.ThrowIfNull(favourites);
        var rows = new List<FolderEntry>();
        foreach (var a in enabled)
        {
            var pinned = VisibleFolders(FoldersOf(folders, a.Id))
                .Where(f => f.Selectable && favourites.Has(new FolderKey(a.Id, f.Id)));
            foreach (var f in SortSiblings(pinned))
            {
                rows.Add(new FolderEntry { Account = a, Folder = f, Favourite = true, Starred = true, Badge = f.Unread });
            }
        }
        if (rows.Count == 0)
        {
            return [];
        }
        return [new FolderEntry { Header = true, Favourite = true }, .. rows];
    }

    /// <summary>
    /// Orders one account's folders (model.go <c>folderTree</c>; see
    /// <see cref="SortFolders"/>), leaving out what
    /// <see cref="VisibleFolders"/> hides and everything below a collapsed
    /// folder. Pinned folders are marked starred.
    /// </summary>
    public static IReadOnlyList<FolderEntry> FolderTreeRows(
        Account a,
        IReadOnlyList<Folder> folders,
        CollapseState collapsed,
        FavouriteState favourites)
    {
        ArgumentNullException.ThrowIfNull(a);
        ArgumentNullException.ThrowIfNull(collapsed);
        ArgumentNullException.ThrowIfNull(favourites);
        var list = VisibleFolders(folders);
        var byId = list.Select(f => f.Id).ToHashSet();
        var children = new Dictionary<FolderId, IReadOnlyList<Folder>>();
        var childLists = new Dictionary<FolderId, List<Folder>>();
        var roots = new List<Folder>();
        foreach (var f in list)
        {
            if (f.ParentId is not { } parent || string.IsNullOrEmpty(parent.Value) || parent == f.Id || !byId.Contains(parent))
            {
                roots.Add(f);
                continue;
            }
            if (!childLists.TryGetValue(parent, out var kids))
            {
                kids = [];
                childLists[parent] = kids;
            }
            kids.Add(f);
        }
        var sortedRoots = SortSiblings(roots);
        foreach (var (id, kids) in childLists)
        {
            children[id] = SortSiblings(kids);
        }
        // The rows of an account either all reserve the arrow's width or none
        // do, so a flat mailbox keeps the layout it had before folding existed.
        var nested = children.Count > 0;

        var output = new List<FolderEntry>(list.Count);
        var visited = new HashSet<FolderId>();

        IReadOnlyList<Folder> KidsOf(FolderId id) => children.TryGetValue(id, out var kids) ? kids : [];

        // Records f and everything below it as visited, so a collapsed branch
        // is not mistaken for an unreachable one (model.go markSubtree).
        void MarkSubtree(Folder f)
        {
            var pending = new Stack<Folder>();
            pending.Push(f);
            while (pending.Count > 0)
            {
                var next = pending.Pop();
                if (!visited.Add(next.Id))
                {
                    continue;
                }
                foreach (var c in KidsOf(next.Id))
                {
                    pending.Push(c);
                }
            }
        }

        void Walk(Folder f, int depth)
        {
            if (visited.Contains(f.Id) || depth > MaxFolderDepth)
            {
                return;
            }
            visited.Add(f.Id);
            var kids = KidsOf(f.Id);
            var key = new FolderKey(a.Id, f.Id);
            var fold = kids.Count > 0 && collapsed.FolderCollapsed(key);
            output.Add(new FolderEntry
            {
                Account = a,
                Folder = f,
                Depth = depth,
                Starred = favourites.Has(key),
                HasChildren = kids.Count > 0,
                Collapsed = fold,
                Nested = nested,
                Badge = BadgeFor(list, f, fold),
            });
            if (fold)
            {
                // Mark the whole subtree seen, or the orphan sweep below would
                // list every hidden descendant flat.
                foreach (var c in kids)
                {
                    MarkSubtree(c);
                }
                return;
            }
            foreach (var c in kids)
            {
                Walk(c, depth + 1);
            }
        }

        foreach (var r in sortedRoots)
        {
            Walk(r, 0);
        }

        // Whatever the walk did not reach sits in a parent cycle or below the
        // depth cap: list it flat, ordered like roots, indented by its path.
        foreach (var f in SortSiblings(list.Where(f => !visited.Contains(f.Id))))
        {
            output.Add(new FolderEntry
            {
                Account = a,
                Folder = f,
                Depth = PathDepth(f.Path),
                Starred = favourites.Has(new FolderKey(a.Id, f.Id)),
                Nested = nested,
                Badge = f.Unread,
            });
        }
        return output;
    }

    /// <summary>
    /// The unread count a row shows (model.go <c>badgeFor</c>): the folder's
    /// own, plus every descendant's while the folder is collapsed and they
    /// are out of sight. The walk is over the account's whole list, which is
    /// why it tolerates parent cycles by visiting each folder at most once.
    /// </summary>
    public static int BadgeFor(IReadOnlyList<Folder> list, Folder f, bool collapsed)
    {
        ArgumentNullException.ThrowIfNull(list);
        ArgumentNullException.ThrowIfNull(f);
        if (!collapsed)
        {
            return f.Unread;
        }
        var children = new Dictionary<FolderId, List<Folder>>();
        foreach (var c in list)
        {
            if (c.ParentId is { } parent && !string.IsNullOrEmpty(parent.Value) && parent != c.Id)
            {
                if (!children.TryGetValue(parent, out var kids))
                {
                    kids = [];
                    children[parent] = kids;
                }
                kids.Add(c);
            }
        }
        var total = f.Unread;
        var seen = new HashSet<FolderId> { f.Id };
        var pending = new Stack<FolderId>();
        pending.Push(f.Id);
        while (pending.Count > 0)
        {
            if (!children.TryGetValue(pending.Pop(), out var kids))
            {
                continue;
            }
            foreach (var c in kids)
            {
                if (seen.Add(c.Id))
                {
                    total += c.Unread;
                    pending.Push(c.Id);
                }
            }
        }
        return total;
    }

    /// <summary>
    /// Orders folders at one tree level (model.go <c>sortSiblings</c>):
    /// special-use roles first (Inbox, Drafts, Sent, …), then alphabetically
    /// by path, case-insensitively. Stable, so equal keys keep the server's
    /// order.
    /// </summary>
    public static IReadOnlyList<Folder> SortSiblings(IEnumerable<Folder> list)
    {
        ArgumentNullException.ThrowIfNull(list);
        // The keys are computed once per folder; OrderBy and ThenBy are stable.
        return [.. list.OrderBy(f => RoleRank(f.Role)).ThenBy(f => CodePoints.ToLower(f.Path ?? ""), CodePoints.Comparer)];
    }

    /// <summary>
    /// The sidebar order of special-use folders; ordinary folders sort last
    /// (model.go <c>roleRank</c>).
    /// </summary>
    public static int RoleRank(FolderRole r) => r.Value switch
    {
        FolderRole.Inbox => 0,
        FolderRole.Drafts => 1,
        FolderRole.Sent => 2,
        FolderRole.Archive => 3,
        FolderRole.Junk => 4,
        FolderRole.Trash => 5,
        FolderRole.Outbox => 6,
        FolderRole.All => 7,
        _ => 100,
    };

    /// <summary>
    /// The GTK symbolic icon name for a folder role (model.go
    /// <c>roleIcon</c>). The WinUI layer maps these names to its glyphs; the
    /// names are kept so the mapping table has one key per GTK icon.
    /// </summary>
    public static string RoleIcon(FolderRole r) => r.Value switch
    {
        FolderRole.Inbox => "mail-unread-symbolic",
        FolderRole.Drafts => "document-edit-symbolic",
        FolderRole.Sent or FolderRole.Outbox => "mail-send-symbolic",
        FolderRole.Trash => "user-trash-symbolic",
        FolderRole.Junk => "mail-mark-junk-symbolic",
        FolderRole.Archive => "folder-download-symbolic",
        _ => "folder-symbolic",
    };

    /// <summary>
    /// The display name of a folder (folders.go <c>folderTitle</c>): the
    /// localised name for a role folder (whatever the server calls it), the
    /// server's name otherwise. Plain text either way; Windows-only, the
    /// server's name is cleaned for display (<see cref="DisplayText.Clean"/>,
    /// docs/security.md §4), since a server can name a folder with an
    /// override or a control character as a sender names a subject, and the
    /// sidebar, the list's header, the window's caption, the search results
    /// and the status line show it. Where it is composed with other text
    /// the caller isolates it (<see cref="DisplayText.Isolate"/>).
    /// </summary>
    public static string FolderTitle(Folder f)
    {
        ArgumentNullException.ThrowIfNull(f);
        return f.Role.Value switch
        {
            FolderRole.Inbox => L10n.C("folder", "Inbox"),
            FolderRole.Drafts => L10n.C("folder", "Drafts"),
            FolderRole.Sent => L10n.C("folder", "Sent"),
            FolderRole.Trash => L10n.C("folder", "Trash"),
            FolderRole.Junk => L10n.C("folder", "Junk"),
            FolderRole.Archive => L10n.C("folder", "Archive"),
            FolderRole.All => L10n.C("folder", "All Mail"),
            FolderRole.Outbox => L10n.C("folder", "Outbox"),
            _ => DisplayText.Clean(f.Name),
        };
    }

    /// <summary>
    /// The counts under the list title (folders.go <c>folderCountsText</c>):
    /// the folder's unread and total counts, whatever the list filter or
    /// grouping shows. An empty folder (or one the daemon never downloads)
    /// has none, and a folder with nothing unread, or the outbox, only its
    /// total.
    /// </summary>
    public static string FolderCountsText(Folder f)
    {
        ArgumentNullException.ThrowIfNull(f);
        if (f.Total <= 0)
        {
            return "";
        }
        if (f.Role == FolderRole.Outbox || f.Unread <= 0)
        {
            // TRANSLATORS: subtitle of the message list; %d is the number of
            // messages in the folder.
            return L10n.N("%d message", "%d messages", f.Total);
        }
        // TRANSLATORS: subtitle of the message list, e.g. "12 unread of 1234";
        // the first %d is the number of unread messages (the plural follows
        // it), the second the number of all messages in the folder.
        return L10n.N("%d unread of %d", "%d unread of %d", f.Unread, f.Unread, f.Total);
    }

    /// <summary>
    /// <c>InitialFolder</c> over the rows of one section (model.go
    /// <c>firstFolder</c>): the Favourites rows when
    /// <paramref name="favourite"/> is set, the tree rows otherwise. The
    /// first Inbox wins, else the first selectable folder.
    /// </summary>
    public static FolderKey? FirstFolder(IReadOnlyList<FolderEntry> entries, bool favourite)
    {
        ArgumentNullException.ThrowIfNull(entries);
        FolderKey? first = null;
        foreach (var e in entries)
        {
            if (e.Header || e.Favourite != favourite || e.Folder is not { Selectable: true } folder || e.Key is not { } k)
            {
                continue;
            }
            if (folder.Role == FolderRole.Inbox)
            {
                return k;
            }
            first ??= k;
        }
        return first;
    }

    // The folders of an account, none when it has no list.
    private static IReadOnlyList<Folder> FoldersOf(IReadOnlyDictionary<AccountId, IReadOnlyList<Folder>> folders, AccountId id) =>
        folders.TryGetValue(id, out var list) ? list : [];

    // The number of separators in a display path (strings.Count(path, "/")).
    // A slash is one UTF-16 unit as it is one UTF-8 byte, and no other
    // character contains either, so a combining mark after it cannot hide it.
    private static int PathDepth(string path)
    {
        var n = 0;
        foreach (var c in path ?? "")
        {
            if (c == '/')
            {
                n++;
            }
        }
        return n;
    }
}
