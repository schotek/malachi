// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/MailModel.swift (MailModel,
// summaryMessage; RowMessage is RowMessage.cs); GTK:
// ui/internal/window/model.go (mailModel, clearMessages, bumpAll,
// rebuildEntries, refreshBadges, setMessages, appendMessages,
// insertMessage, removeMessage, reindex, messageAt, message, updateFlags,
// folder, folderListed, folderByRole, folderRole, inOutbox, inDrafts,
// adjustCounts, moveCounts, outboxKey, account, enabledAccounts,
// initialFolder, bumpList, bumpBody, bumpFolders, summaryMessage),
// thread_model.go (setOutbox), actions.go (canMoveToRole).
//
// Swift's MailModel is a struct the controllers change in place
// (mailbox.model.x = …); here it is a class the controllers own, changed in
// place the same way. What that changes: a Swift `let m = mailbox.model` is
// a copy that later changes do not reach, a C# reference is not. The
// collections this class hands out are views of its own state (Messages,
// Index, Threads, …) or lists it replaces whole and never changes again
// (Entries, Rows, the folder lists in Folders, the member lists in Members);
// a caller that needs the state as it is now takes Clone(). Every API record
// is immutable and changed with `with`.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Bulk;
using Malachi.Core.IssueTrackers;

namespace Malachi.Core.Model;

/// <summary>
/// The main window's view model: plain C#, no WinUI. It mirrors what the
/// backend returned (accounts, folders, the current message page) so the
/// views can be rebuilt from it and optimistic updates have one place to
/// live. Nothing here decides anything about mail; it only caches API data.
/// </summary>
/// <remarks>
/// The generation counters guard asynchronous replies: a caller bumps the
/// counter before starting a request and ignores the reply when the counter
/// moved on (folder changed, connection dropped, …). Not thread-safe: the
/// controllers use it on the UI thread only.
/// </remarks>
public sealed partial class MailModel
{
    private readonly List<MessageSummary> messages = [];
    private readonly Dictionary<MessageId, int> index = new();

    /// <summary>A model over the given accounts and folder lists, both copied.</summary>
    public MailModel(
        IEnumerable<Account>? accounts = null,
        IEnumerable<KeyValuePair<AccountId, IReadOnlyList<Folder>>>? folders = null,
        bool grouped = false,
        MessageFilter? listFilter = null,
        CollapseState? collapsed = null,
        FavouriteState? favourites = null)
    {
        Accounts = accounts is null ? [] : [.. accounts];
        if (folders is not null)
        {
            foreach (var (id, list) in folders)
            {
                Folders[id] = [.. list];
            }
        }
        Grouped = grouped;
        ListFilter = listFilter ?? MessageFilter.All;
        Collapsed = collapsed ?? new CollapseState();
        Favourites = favourites ?? new FavouriteState();
    }

    /// <summary>The accounts in list order; replaced whole, never changed in place.</summary>
    public IReadOnlyList<Account> Accounts { get; set; }

    /// <summary>
    /// The folder lists of the accounts. A list is replaced whole, never
    /// changed in place, so one handed out stays as it was.
    /// </summary>
    public Dictionary<AccountId, IReadOnlyList<Folder>> Folders { get; } = new();

    /// <summary>Why an account's folder.list failed.</summary>
    public Dictionary<AccountId, Exception> FolderErr { get; } = new();

    /// <summary>The sidebar rows; replaced whole, never changed in place.</summary>
    public IReadOnlyList<FolderEntry> Entries { get; private set; } = [];

    /// <summary>The selected folder.</summary>
    public FolderKey? Selected { get; set; }

    /// <summary>
    /// Which of the selected folder's rows carries the highlight: the one in
    /// the Favourites section or the one in the tree, whichever the user
    /// clicked last. Display only; <see cref="Selected"/> is the folder.
    /// </summary>
    public bool SelectedFav { get; set; }

    /// <summary>Which sidebar nodes are folded away.</summary>
    public CollapseState Collapsed { get; set; }

    /// <summary>Which folders are pinned to the top.</summary>
    public FavouriteState Favourites { get; set; }

    /// <summary>
    /// The folder messages belong to (or are being loaded for); it lags
    /// <see cref="Selected"/> between selectFolder and loadMessages.
    /// </summary>
    public FolderKey? ListFolder { get; set; }

    /// <summary>
    /// Narrows the listing (message.list "filter"). One setting for the
    /// whole window, kept across folder switches but not across restarts.
    /// </summary>
    public MessageFilter ListFilter { get; set; }

    /// <summary>The flat list, a view of the model's own state.</summary>
    public IReadOnlyList<MessageSummary> Messages => messages;

    /// <summary>The position of each listed message in <see cref="Messages"/>.</summary>
    public IReadOnlyDictionary<MessageId, int> Index => index;

    /// <summary>The cursor of the next page; null on the last one.</summary>
    public string? NextCursor { get; set; }

    /// <summary>-1 when the backend could not compute it.</summary>
    public int Total { get; set; }

    /// <summary>Why the listing failed.</summary>
    public Exception? ListErr { get; set; }

    /// <summary>The search's side of the list (MailModel.Search.cs).</summary>
    public SearchState Search { get; private set; } = new();

    /// <summary>Guards message.list, thread.list, thread.get and search.query replies.</summary>
    public ulong ListGen { get; private set; }

    /// <summary>Guards message.get and message.body replies.</summary>
    public ulong BodyGen { get; private set; }

    /// <summary>Guards account.list and folder.list replies.</summary>
    public ulong FoldersGen { get; private set; }

    /// <summary>The first page is on its way.</summary>
    public bool Loading { get; set; }

    /// <summary>A further page is on its way.</summary>
    public bool LoadingMore { get; set; }

    /// <summary>The accounts the sidebar shows, in list order.</summary>
    public IReadOnlyList<Account> EnabledAccounts => FolderTree.EnabledAccounts(Accounts);

    /// <summary>
    /// Projects a list summary onto what a row displays (model.go
    /// <c>summaryMessage</c>). A message of a Jira account adds its issue
    /// (<see cref="Jira.RowIssue"/>), and an event of an issue is never
    /// unread, whatever its flags.
    /// </summary>
    public static RowMessage SummaryMessage(MessageSummary s)
    {
        ArgumentNullException.ThrowIfNull(s);
        var issue = Jira.RowIssue(s);
        return new RowMessage
        {
            From = s.From,
            Subject = s.Subject,
            Snippet = s.Snippet,
            Date = s.Date,
            Unread = issue?.Unread ?? !FolderTree.HasFlag(s.Flags, Flag.Seen),
            Flagged = FolderTree.HasFlag(s.Flags, Flag.Flagged),
            HasAttachments = s.HasAttachments,
            Issue = issue,
            Tag = BulkMail.Tag(s.Bulk),
        };
    }

    /// <summary>
    /// A copy that later changes of this model do not reach: what a Swift
    /// `let m = mailbox.model` holds. The immutable lists and records are
    /// shared, everything that changes in place is copied.
    /// </summary>
    public MailModel Clone()
    {
        var c = new MailModel(grouped: Grouped, listFilter: ListFilter, collapsed: Collapsed.Clone(), favourites: Favourites.Clone())
        {
            Accounts = Accounts,
            Entries = Entries,
            Selected = Selected,
            SelectedFav = SelectedFav,
            ListFolder = ListFolder,
            NextCursor = NextCursor,
            Total = Total,
            ListErr = ListErr,
            Search = Search.Clone(),
            ListGen = ListGen,
            BodyGen = BodyGen,
            FoldersGen = FoldersGen,
            Loading = Loading,
            LoadingMore = LoadingMore,
        };
        foreach (var (id, list) in Folders)
        {
            c.Folders[id] = list;
        }
        foreach (var (id, err) in FolderErr)
        {
            c.FolderErr[id] = err;
        }
        c.messages.AddRange(messages);
        foreach (var (id, i) in index)
        {
            c.index[id] = i;
        }
        CloneThreads(c);
        return c;
    }

    // Lists

    /// <summary>
    /// Empties the list (folder switch, disconnect) without touching the
    /// generation counters.
    /// </summary>
    public void ClearMessages()
    {
        SetMessages([], new PageInfo { Total = -1 });
        ClearThreads();
    }

    /// <summary>
    /// Invalidates every in-flight reply and clears the loading flags
    /// (connection lost).
    /// </summary>
    public void BumpAll()
    {
        BumpList();
        BumpBody();
        BumpFolders();
        Loading = false;
        LoadingMore = false;
    }

    /// <summary>
    /// Replaces the list with a first page. Duplicate ids (which a
    /// well-behaved backend never sends) keep their first occurrence.
    /// </summary>
    public void SetMessages(IReadOnlyList<MessageSummary> list, PageInfo page)
    {
        ArgumentNullException.ThrowIfNull(list);
        ArgumentNullException.ThrowIfNull(page);
        messages.Clear();
        index.Clear();
        index.EnsureCapacity(list.Count);
        foreach (var s in list)
        {
            if (index.ContainsKey(s.Id))
            {
                continue;
            }
            index[s.Id] = messages.Count;
            messages.Add(s);
        }
        NextCursor = page.NextCursor;
        Total = page.Total;
        ListErr = null;
    }

    /// <summary>
    /// Adds a further page, skipping messages already listed (a message
    /// inserted from notify.newMessage may show up again in a page). Returns
    /// how many rows were added.
    /// </summary>
    public int AppendMessages(IReadOnlyList<MessageSummary> list, PageInfo page)
    {
        ArgumentNullException.ThrowIfNull(list);
        ArgumentNullException.ThrowIfNull(page);
        var added = 0;
        foreach (var s in list)
        {
            if (index.ContainsKey(s.Id))
            {
                continue;
            }
            index[s.Id] = messages.Count;
            messages.Add(s);
            added++;
        }
        NextCursor = page.NextCursor;
        Total = page.Total;
        return added;
    }

    /// <summary>
    /// Places <paramref name="s"/> at <paramref name="idx"/> (clamped to the
    /// list bounds) and reports whether it was inserted; an id already in
    /// the list is left alone.
    /// </summary>
    public bool InsertMessage(int idx, MessageSummary s)
    {
        ArgumentNullException.ThrowIfNull(s);
        if (index.ContainsKey(s.Id))
        {
            return false;
        }
        var at = Math.Min(Math.Max(idx, 0), messages.Count);
        messages.Insert(at, s);
        Reindex(at);
        if (Total >= 0)
        {
            Total++;
        }
        return true;
    }

    /// <summary>
    /// Drops <paramref name="id"/> from the list and returns the removed
    /// summary and the index it had (the natural place to select a neighbour
    /// afterwards); null when it was not listed.
    /// </summary>
    public (MessageSummary Summary, int Index)? RemoveMessage(MessageId id)
    {
        if (!index.TryGetValue(id, out var idx))
        {
            return null;
        }
        var s = messages[idx];
        messages.RemoveAt(idx);
        index.Remove(id);
        Reindex(idx);
        if (Total > 0)
        {
            Total--;
        }
        return (s, idx);
    }

    /// <summary>The summary at list position <paramref name="idx"/>.</summary>
    public MessageSummary? MessageAt(int idx) => idx >= 0 && idx < messages.Count ? messages[idx] : null;

    /// <summary>
    /// The summary with the given id and its list position. In grouped mode
    /// a member of a listed conversation is found too; the index is then the
    /// row it has (-1 while the conversation is folded), never a position in
    /// <see cref="Messages"/>.
    /// </summary>
    public (MessageSummary Summary, int Index)? Message(MessageId id)
    {
        if (index.TryGetValue(id, out var idx))
        {
            return (messages[idx], idx);
        }
        if (memberOf.TryGetValue(id, out var tid) && Members.TryGetValue(tid, out var mem))
        {
            foreach (var s in mem.List)
            {
                if (s.Id == id)
                {
                    return (s, RowIndexOf(new ListKey(tid, id)));
                }
            }
        }
        return null;
    }

    /// <summary>
    /// Applies a flag change to the cached summary (optimistic update or
    /// server notification) and reports whether anything changed. A flag in
    /// both <paramref name="set"/> and <paramref name="clear"/> ends up set.
    /// </summary>
    public bool UpdateFlags(MessageId id, IReadOnlyList<Flag>? set = null, IReadOnlyList<Flag>? clear = null)
    {
        if (!index.TryGetValue(id, out var idx))
        {
            return false;
        }
        set ??= [];
        clear ??= [];
        var (flags, changed) = ApplyFlagChange(messages[idx].Flags, set, clear);
        if (changed)
        {
            messages[idx] = messages[idx] with { Flags = flags };
        }
        return changed;
    }

    /// <summary>
    /// Replaces the outbox state of a listed message (flat mode; the outbox
    /// folder is never grouped).
    /// </summary>
    public void SetOutbox(MessageId id, OutboxInfo? o)
    {
        if (index.TryGetValue(id, out var idx))
        {
            messages[idx] = messages[idx] with { Outbox = o };
        }
    }

    // Folders and accounts

    /// <summary>Recomputes the sidebar rows from accounts and folders.</summary>
    public void RebuildEntries() => Entries = FolderTree.SortFolders(Accounts, Folders, Collapsed, Favourites);

    /// <summary>
    /// Recomputes the number every visible row shows, after an unread count
    /// moved. A collapsed row sums its hidden descendants, so one changed
    /// folder can move an ancestor's badge and a single-row update is not
    /// enough.
    /// </summary>
    public void RefreshBadges()
    {
        var entries = Entries.ToArray();
        for (var i = 0; i < entries.Length; i++)
        {
            var e = entries[i];
            if (e.Header || e.Account is not { } account || e.Folder is not { } folder)
            {
                continue;
            }
            entries[i] = e with { Badge = FolderTree.BadgeFor(FoldersOf(account.Id), folder, e.Collapsed) };
        }
        Entries = entries;
    }

    /// <summary>Looks a folder up by key.</summary>
    public Folder? Folder(FolderKey k) => FoldersOf(k.Account).FirstOrDefault(f => f.Id == k.Folder);

    /// <summary>
    /// Whether the sidebar still lists <paramref name="k"/>, whether or not a
    /// fold currently hides its row. It is the test for keeping a selection:
    /// folding a parent must not move it, but an account switched off, a
    /// folder gone from the server or an outbox that has just drained must.
    /// </summary>
    public bool FolderListed(FolderKey? k)
    {
        if (k is not { } key || Account(key.Account) is not { Enabled: true })
        {
            return false;
        }
        return FolderTree.VisibleFolders(FoldersOf(key.Account)).Any(f => f.Id == key.Folder);
    }

    /// <summary>
    /// The account's folder with the given special-use role (the first one
    /// when a server reports several).
    /// </summary>
    public Folder? FolderByRole(AccountId acc, FolderRole role) => FoldersOf(acc).FirstOrDefault(f => f.Role == role);

    /// <summary>
    /// The special-use role of folder <paramref name="k"/>,
    /// <see cref="FolderRole.None"/> when the folder is unknown or plain.
    /// </summary>
    public FolderRole FolderRole(FolderKey k) => Folder(k)?.Role ?? Api.FolderRole.None;

    /// <summary>
    /// Whether <paramref name="s"/> is a queued outgoing message: the backend
    /// marks it with <c>outbox</c>, and its folder is the account's outbox.
    /// </summary>
    public bool InOutbox(MessageSummary s)
    {
        ArgumentNullException.ThrowIfNull(s);
        return s.Outbox is not null || FolderRole(new FolderKey(s.AccountId, s.FolderId)) == Api.FolderRole.Outbox;
    }

    /// <summary>
    /// Whether <paramref name="s"/> lies in its account's Drafts folder: such
    /// a message opens in the compose window (draft.open) rather than as mail
    /// (model.go <c>inDrafts</c>).
    /// </summary>
    public bool InDrafts(MessageSummary s)
    {
        ArgumentNullException.ThrowIfNull(s);
        return FolderRole(new FolderKey(s.AccountId, s.FolderId)) == Api.FolderRole.Drafts;
    }

    /// <summary>
    /// Whether <paramref name="s"/> can go to its account's role folder: the
    /// folder exists and <paramref name="s"/> is not in it (actions.go
    /// <c>canMoveToRole</c>).
    /// </summary>
    public bool CanMoveToRole(MessageSummary s, FolderRole role)
    {
        ArgumentNullException.ThrowIfNull(s);
        return FolderByRole(s.AccountId, role) is { } f && f.Id != s.FolderId;
    }

    /// <summary>
    /// Changes the cached unread and total counts of a folder by
    /// <paramref name="dUnread"/> and <paramref name="dTotal"/>, neither below
    /// zero (model.go <c>adjustCounts</c>). Both the folders map and the
    /// sidebar entries are updated so a later rebuild does not undo the
    /// change; the next folder.list brings the daemon's numbers back.
    /// </summary>
    public void AdjustCounts(FolderKey k, int dUnread, int dTotal)
    {
        if (!Folders.TryGetValue(k.Account, out var current))
        {
            return;
        }
        var list = current.ToArray();
        var i = Array.FindIndex(list, f => f.Id == k.Folder);
        if (i < 0)
        {
            return;
        }
        list[i] = list[i] with
        {
            Unread = Math.Max(list[i].Unread + dUnread, 0),
            Total = Math.Max(list[i].Total + dTotal, 0),
        };
        Folders[k.Account] = list;
        var entries = Entries.ToArray();
        for (var j = 0; j < entries.Length; j++)
        {
            var e = entries[j];
            if (!e.Header && e.Account?.Id == k.Account && e.Folder is { } folder && folder.Id == k.Folder)
            {
                entries[j] = e with { Folder = folder with { Unread = list[i].Unread, Total = list[i].Total } };
            }
        }
        Entries = entries;
        // The folder may be hidden under a collapsed ancestor whose badge
        // counts it, so every badge is recomputed, not just this row's.
        RefreshBadges();
    }

    /// <summary>
    /// Shifts the cached counts for <paramref name="n"/> messages,
    /// <paramref name="unread"/> of them unread, leaving
    /// <paramref name="src"/> for <paramref name="target"/> (null: leaving
    /// the store, as when Trash expunges or a send is cancelled); negative
    /// numbers put them back (model.go <c>moveCounts</c>). The total is left
    /// alone where an optimistic change would mislead: in an outbox source,
    /// whose row <see cref="FolderTree.VisibleFolders"/> drops at zero,
    /// taking the selection with it on the next rebuild before an undo could
    /// bring it back (<c>onOutboxChanged</c> reloads the outbox anyway); and
    /// in a target the daemon never downloads (<c>synced</c> false, Gmail's
    /// All Mail), which counts nothing at all and so gets neither count.
    /// </summary>
    public void MoveCounts(FolderKey src, FolderKey? target, int unread, int n)
    {
        var srcTotal = FolderRole(src) == Api.FolderRole.Outbox ? 0 : -n;
        AdjustCounts(src, -unread, srcTotal);
        if (target is not { } t)
        {
            return;
        }
        if (Folder(t) is { Synced: false })
        {
            return;
        }
        AdjustCounts(t, unread, n);
    }

    /// <summary>
    /// The account's outbox folder while the window can show it (model.go
    /// <c>outboxKey</c>): the account is enabled (only those have their
    /// folders loaded) and its outbox is known.
    /// </summary>
    public FolderKey? OutboxKey(AccountId acc)
    {
        if (Account(acc) is not { Enabled: true } || FolderByRole(acc, Api.FolderRole.Outbox) is not { } f)
        {
            return null;
        }
        return new FolderKey(acc, f.Id);
    }

    /// <summary>Looks an account up by id.</summary>
    public Account? Account(AccountId id) => Accounts.FirstOrDefault(a => a.Id == id);

    /// <summary>
    /// The folder to select when nothing is selected yet: the first Inbox,
    /// otherwise the first selectable folder. The tree is searched before the
    /// Favourites section, which repeats folders of the tree: a pinned Inbox
    /// of a later account must not win over the first account's. The section
    /// only decides when the tree gave nothing (every account folded).
    /// </summary>
    public FolderKey? InitialFolder() => FolderTree.FirstFolder(Entries, favourite: false) ?? FolderTree.FirstFolder(Entries, favourite: true);

    // Generations

    /// <summary>Invalidates in-flight message.list replies.</summary>
    public ulong BumpList() => ++ListGen;

    /// <summary>Invalidates in-flight message.get / message.body replies.</summary>
    public ulong BumpBody() => ++BodyGen;

    /// <summary>Invalidates in-flight account.list / folder.list replies.</summary>
    public ulong BumpFolders() => ++FoldersGen;

    // Refreshes index for the rows from idx onwards.
    private void Reindex(int idx)
    {
        for (var i = idx; i < messages.Count; i++)
        {
            index[messages[i].Id] = i;
        }
    }

    // The account's folder list, none when it has none.
    private IReadOnlyList<Folder> FoldersOf(AccountId acc) => Folders.TryGetValue(acc, out var list) ? list : [];
}
