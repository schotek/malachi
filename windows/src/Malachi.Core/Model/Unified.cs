// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/unified (unified.go, text.go) 1:1; macOS:
// MalachiCore/Model/Unified.swift.
//
// The "All Inboxes" item of the sidebar: the inboxes of every enabled mail
// account in one message list (docs/plans/unified-inbox.md, decisions
// U1-U4; docs/api.md §4.3 and §4.4, "Unified listing"). It says when the
// item is shown (Shown), which folders it covers (Inboxes, Covers,
// CoversAny) and its badge (Unread, Total), builds the listing's parameters
// (MessageParams, ThreadParams), names its sidebar key (Key) and splits an
// action on rows of several accounts into one call per account
// (GroupByAccount). Pure logic, no widgets.
//
// The scope is the daemon's (core/unified.go): folders of role inbox that
// are synced and not virtual, of the enabled accounts of kind imap or
// graph, never jira, caldav or eway. The client mirrors it only to know
// which notifications and counts belong to the list; the rows come from the
// daemon.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.I18n;

namespace Malachi.Core.Model;

/// <summary>ui/internal/unified: the All Inboxes item.</summary>
public static class Unified
{
    /// <summary>unified.MinAccounts: how many mail accounts the item needs to be shown; with one, its inbox is the same list.</summary>
    public const int MinAccounts = 2;

    /// <summary>
    /// unified.KeyFolder: the folder of the item's sidebar key; its account
    /// is "". A real folder id never starts with "@" and no real key has an
    /// empty account, so the key cannot collide. It is never stored in
    /// favourite-folders or collapsed-folders and cannot be pinned.
    /// </summary>
    public const string KeyFolder = "@unified:inbox";

    /// <summary>unified.Role: the folder role the item lists (api.UnifiedRoles); the API takes it as <c>unified</c>.</summary>
    public static FolderRole Role { get; } = FolderRole.Inbox;

    /// <summary>unified.Key: the sidebar key of the All Inboxes item.</summary>
    public static FolderKey Key { get; } = new(new AccountId(""), new FolderId(KeyFolder));

    /// <summary>unified.IsKey: whether the pair is the item's key.</summary>
    public static bool IsKey(AccountId account, FolderId folder) => string.IsNullOrEmpty(account.Value) && folder.Value == KeyFolder;

    /// <summary>
    /// unified.IsMailAccount: an account the item covers when enabled, of
    /// kind imap or graph (an empty kind is imap).
    /// </summary>
    public static bool IsMailAccount(Account a)
    {
        ArgumentNullException.ThrowIfNull(a);
        var kind = a.Config.ProtocolKind;
        return kind == AccountKind.Imap || kind == AccountKind.Graph;
    }

    /// <summary>
    /// unified.Accounts: the ids of the accounts the item covers, the
    /// enabled mail accounts, in the order given (account.list's, which is
    /// the sidebar's).
    /// </summary>
    public static IReadOnlyList<AccountId> Accounts(IEnumerable<Account> accounts)
    {
        ArgumentNullException.ThrowIfNull(accounts);
        return [.. accounts.Where(a => a.Enabled && IsMailAccount(a)).Select(a => a.Id)];
    }

    /// <summary>
    /// unified.Shown: whether the sidebar has the item: at least
    /// <see cref="MinAccounts"/> accounts it covers and the preference
    /// unified-inbox (<paramref name="setting"/>) on.
    /// </summary>
    public static bool Shown(IEnumerable<Account> accounts, bool setting) => setting && Accounts(accounts).Count >= MinAccounts;

    /// <summary>unified.IsInbox: whether <paramref name="f"/> is a folder the item lists: role inbox, synced and not a view of an issue tracker.</summary>
    public static bool IsInbox(Folder f)
    {
        ArgumentNullException.ThrowIfNull(f);
        return f.Role == Role && f.Synced && string.IsNullOrEmpty(f.Virtual?.Value);
    }

    /// <summary>
    /// unified.Inboxes: the folders the item lists: those <see cref="IsInbox"/>
    /// of each account <see cref="Accounts"/> covers, in the accounts' order
    /// and then the order of folders (folder.list's of each account, keyed
    /// by account).
    /// </summary>
    public static IReadOnlyList<Folder> Inboxes(IEnumerable<Account> accounts, IReadOnlyDictionary<AccountId, IReadOnlyList<Folder>> folders)
    {
        ArgumentNullException.ThrowIfNull(accounts);
        ArgumentNullException.ThrowIfNull(folders);
        var output = new List<Folder>();
        foreach (var id in Accounts(accounts))
        {
            if (folders.TryGetValue(id, out var list))
            {
                output.AddRange(list.Where(IsInbox));
            }
        }
        return output;
    }

    /// <summary>unified.Unread: the item's badge, the unread count of the inboxes (<see cref="Inboxes"/>).</summary>
    public static int Unread(IEnumerable<Folder> inboxes)
    {
        ArgumentNullException.ThrowIfNull(inboxes);
        return inboxes.Sum(f => f.Unread);
    }

    /// <summary>unified.Total: the number of messages of the inboxes, for the list's subtitle ("%d unread of %d", the existing msgid).</summary>
    public static int Total(IEnumerable<Folder> inboxes)
    {
        ArgumentNullException.ThrowIfNull(inboxes);
        return inboxes.Sum(f => f.Total);
    }

    /// <summary>
    /// unified.Covers: whether a change of <paramref name="folderId"/> of
    /// <paramref name="accountId"/> (notify.newMessage, a folder's sync
    /// finishing, an undone move into it) belongs to the list shown: the
    /// list is the item (<paramref name="listIsUnified"/>) and the folder is
    /// one of <paramref name="inboxes"/> (<see cref="Inboxes"/>).
    /// </summary>
    public static bool Covers(bool listIsUnified, AccountId accountId, FolderId folderId, IEnumerable<Folder> inboxes)
    {
        ArgumentNullException.ThrowIfNull(inboxes);
        return listIsUnified && inboxes.Any(f => f.AccountId == accountId && f.Id == folderId);
    }

    /// <summary>
    /// unified.CoversAny: <see cref="Covers"/> for a notification naming
    /// several folders of one account (notify.messagesChanged): any of them
    /// is covered; no folder at all means any folder of the account, covered
    /// when the account has an inbox among <paramref name="inboxes"/>.
    /// </summary>
    public static bool CoversAny(bool listIsUnified, AccountId accountId, IReadOnlyList<FolderId>? folderIds, IEnumerable<Folder> inboxes)
    {
        ArgumentNullException.ThrowIfNull(inboxes);
        if (!listIsUnified)
        {
            return false;
        }
        var list = inboxes as IReadOnlyCollection<Folder> ?? [.. inboxes];
        if (folderIds is null || folderIds.Count == 0)
        {
            return list.Any(f => f.AccountId == accountId);
        }
        return folderIds.Any(id => Covers(true, accountId, id, list));
    }

    /// <summary>
    /// unified.MessageParams: message.list of the item: newest first, the
    /// default page size, the filter of the list and the cursor of the next
    /// page (null for the first). The cursor must be one a unified listing
    /// returned. The account and folder ids stay empty, as the API wants.
    /// </summary>
    public static MessageListParams MessageParams(MessageFilter filter, string? cursor) => new()
    {
        AccountId = new AccountId(""),
        FolderId = new FolderId(""),
        Unified = Role,
        Page = new Page { Limit = API.Limits.DefaultPageLimit, Cursor = string.IsNullOrEmpty(cursor) ? null : cursor },
        Sort = SortOrder.DateDesc,
        Filter = filter,
    };

    /// <summary>
    /// unified.ThreadParams: thread.list of the item, as
    /// <see cref="MessageParams"/>. A row's conversation is then read with
    /// thread.get of the row's account (<see cref="ThreadSummary.AccountId"/>)
    /// and folder (its latest message's folder).
    /// </summary>
    public static ThreadListParams ThreadParams(MessageFilter filter, string? cursor) => new()
    {
        AccountId = new AccountId(""),
        FolderId = new FolderId(""),
        Unified = Role,
        Page = new Page { Limit = API.Limits.DefaultPageLimit, Cursor = string.IsNullOrEmpty(cursor) ? null : cursor },
        Sort = SortOrder.DateDesc,
        Filter = filter,
    };

    /// <summary>
    /// unified.GroupByAccount: splits items (selected rows, message ids with
    /// their account) by account, the accounts in the order they first appear
    /// and the items of each in their order: an action on a selection of the
    /// item runs one call per group (every message method takes one account).
    /// </summary>
    public static IReadOnlyList<UnifiedGroup<T>> GroupByAccount<T>(IEnumerable<T> items, Func<T, AccountId> account)
    {
        ArgumentNullException.ThrowIfNull(items);
        ArgumentNullException.ThrowIfNull(account);
        var order = new List<AccountId>();
        var groups = new Dictionary<AccountId, List<T>>();
        foreach (var it in items)
        {
            var a = account(it);
            if (!groups.TryGetValue(a, out var list))
            {
                list = [];
                groups[a] = list;
                order.Add(a);
            }
            list.Add(it);
        }
        return [.. order.Select(a => new UnifiedGroup<T>(a, groups[a]))];
    }

    /// <summary>
    /// unified.Mixed: whether items belong to more than one account (a
    /// selection of the item across accounts, decision U2).
    /// </summary>
    public static bool Mixed<T>(IReadOnlyList<T> items, Func<T, AccountId> account)
    {
        ArgumentNullException.ThrowIfNull(items);
        ArgumentNullException.ThrowIfNull(account);
        for (var i = 1; i < items.Count; i++)
        {
            if (account(items[i]) != account(items[0]))
            {
                return true;
            }
        }
        return false;
    }

    // Texts (unified/text.go). The keys are the GTK msgids.

    /// <summary>unified.Title: the item's name in the sidebar and the title of its list.</summary>
    public static string Title =>
        // TRANSLATORS: The sidebar item and the list of the inboxes of every mail account together.
        L10n.T("All Inboxes");

    /// <summary>unified.PreferenceTitle: the title of the preference row (key unified-inbox).</summary>
    public static string PreferenceTitle =>
        // TRANSLATORS: A switch in Preferences; "All Inboxes" is the sidebar item's name.
        L10n.T("Show All Inboxes");

    /// <summary>unified.PreferenceSubtitle: the subtitle of the preference row.</summary>
    public static string PreferenceSubtitle =>
        // TRANSLATORS: The subtitle of the "Show All Inboxes" switch in Preferences.
        L10n.T("In the sidebar when you have more than one mail account");
}
