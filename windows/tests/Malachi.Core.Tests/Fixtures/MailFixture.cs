// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/Fixtures/MailFixture.swift.
//
// Swift's actor is a lock-protected class here; the FakeDaemon serves its
// methods concurrently, and the fixture's state changes only under the
// lock. A folder is named by its account and folder ids where Swift takes
// a FolderKey (the model's type, which Malachi.Core.Model gives the
// controller tests), and the daemon's rules that Swift borrows from the
// model (hasFlag, matchesFilter, applyFlagChange, unionFlags,
// frontParticipants) are written out here from docs/api.md, so that the
// fixture does not test the model with itself. The delays run on a
// TimeProvider, a FakeTimeProvider in a test that wants to decide when a
// delayed answer comes.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Transport;

namespace Malachi.Core.Tests.Fixtures;

/// <summary>
/// A scripted malachid for the controller tests: a <see cref="FakeDaemon"/>
/// that answers the methods the main window asks from canned accounts,
/// folders and sync states, records what was asked, fails or delays a
/// method on request and pushes notifications.
/// </summary>
/// <remarks>
/// It serves <c>system.info</c>, <c>account.list</c>, <c>folder.list</c>,
/// <c>sync.status</c>, <c>sync.trigger</c> (recorded in
/// <see cref="Triggers"/>) and <c>config.get</c>, and
/// <c>message.list/get/body/flag/move/delete</c> and
/// <c>thread.list/get</c> over the messages of every folder
/// (<see cref="SetMessages"/>), <see cref="SetBody"/> and
/// <see cref="SetDetail"/>, with the folder counters following every
/// change (a folder-scoped conversation counts and, with withSent, returns
/// the members in the account's folders of role sent that the folder lacks,
/// by id: the fixture has no Message-IDs); the requests are recorded in
/// <see cref="ListRequests"/>, <see cref="ThreadListRequests"/>,
/// <see cref="ThreadGetRequests"/>, <see cref="BodyRequests"/>,
/// <see cref="FlagRequests"/>, <see cref="MoveRequests"/> and
/// <see cref="DeleteRequests"/>. A handler that needs no fixture state can
/// go straight to <see cref="On"/>.
/// </remarks>
internal sealed class MailFixture : IAsyncDisposable
{
    /// <summary>The methods answered from the fixture's state.</summary>
    public static readonly string[] ServedMethods =
    [
        API.SystemInfo.Name, API.AccountList.Name, API.FolderList.Name,
        API.SyncStatus.Name, API.SyncTrigger.Name, API.ConfigGet.Name,
        API.MessageList.Name, API.MessageGet.Name, API.MessageBody.Name,
        API.MessageFlag.Name, API.MessageMove.Name, API.MessageDelete.Name,
        API.ThreadList.Name, API.ThreadGet.Name,
    ];

    private static readonly string[] Markers = ["re:", "fwd:", "fw:"];

    private readonly Lock gate = new();
    private readonly TimeProvider time;
    private readonly Dictionary<AccountId, List<Folder>> folders = [];
    private readonly Dictionary<FolderKey, List<MessageSummary>> messages = [];
    private readonly Dictionary<MessageId, MessageBodyResult> bodies = [];
    private readonly Dictionary<MessageId, Message> details = [];
    private readonly Dictionary<string, RpcError> failures = [];
    private readonly Dictionary<string, TimeSpan> delays = [];
    private readonly List<SyncTriggerParams> triggers = [];
    private readonly List<string> served = [];
    private readonly List<MessageListParams> listRequests = [];
    private readonly List<ThreadListParams> threadListRequests = [];
    private readonly List<ThreadGetParams> threadGetRequests = [];
    private readonly List<MessageBodyParams> bodyRequests = [];
    private readonly List<MessageFlagParams> flagRequests = [];
    private readonly List<MessageMoveParams> moveRequests = [];
    private readonly List<MessageDeleteParams> deleteRequests = [];
    private IReadOnlyList<Account> accounts = [];
    private IReadOnlyList<SyncState> syncStates = [];
    private SystemInfoResult info = new() { Version = "fake", ProtocolVersion = API.ProtocolVersion, Pid = 7, StorePath = "/tmp/s.db" };
    private Preferences preferences = new() { SyncIntervalSeconds = 300, RemoteContent = RemoteContentPolicy.Block, OfflineDays = 30 };

    /// <summary>A fixture whose delays run on <paramref name="time"/> (the system's clock by default).</summary>
    public MailFixture(TimeProvider? time = null)
    {
        this.time = time ?? TimeProvider.System;
        Daemon = new FakeDaemon();
    }

    /// <summary>The daemon the fixture scripts.</summary>
    public FakeDaemon Daemon { get; }

    /// <summary>The socket path to dial.</summary>
    public string Path => Daemon.Path;

    /// <summary>The <c>system.info</c> answer.</summary>
    public SystemInfoResult Info
    {
        get => Locked(() => info);
        set => Locked(() => info = value);
    }

    /// <summary>What <c>config.get</c> returns.</summary>
    public Preferences Preferences
    {
        get => Locked(() => preferences);
        set => Locked(() => preferences = value);
    }

    /// <summary>What <c>account.list</c> returns.</summary>
    public IReadOnlyList<Account> Accounts => Locked(() => accounts);

    /// <summary>What <c>sync.status</c> returns.</summary>
    public IReadOnlyList<SyncState> SyncStates => Locked(() => syncStates);

    /// <summary>Every <c>sync.trigger</c>, in order.</summary>
    public IReadOnlyList<SyncTriggerParams> Triggers => Snapshot(triggers);

    /// <summary>Every method this fixture served, in order (the daemon's own log also has what went to handlers registered with <see cref="On"/>).</summary>
    public IReadOnlyList<string> Served => Snapshot(served);

    public IReadOnlyList<MessageListParams> ListRequests => Snapshot(listRequests);

    public IReadOnlyList<ThreadListParams> ThreadListRequests => Snapshot(threadListRequests);

    public IReadOnlyList<ThreadGetParams> ThreadGetRequests => Snapshot(threadGetRequests);

    public IReadOnlyList<MessageBodyParams> BodyRequests => Snapshot(bodyRequests);

    public IReadOnlyList<MessageFlagParams> FlagRequests => Snapshot(flagRequests);

    public IReadOnlyList<MessageMoveParams> MoveRequests => Snapshot(moveRequests);

    public IReadOnlyList<MessageDeleteParams> DeleteRequests => Snapshot(deleteRequests);

    /// <summary>Registers the handlers and starts listening.</summary>
    public async Task StartAsync()
    {
        foreach (var method in ServedMethods)
        {
            Daemon.On(method, p => ServeAsync(method, p));
        }
        await Daemon.StartAsync();
    }

    public Task StopAsync() => Daemon.StopAsync();

    public ValueTask DisposeAsync() => Daemon.DisposeAsync();

    /// <summary>Closes every client connection (the daemon went away).</summary>
    public void CloseAll() => Daemon.CloseAll();

    // Scripting

    public void SetAccounts(IReadOnlyList<Account> list) => Locked(() => accounts = [.. list]);

    /// <summary>What <c>folder.list</c> returns for <paramref name="account"/>; an unknown account is accountNotFound.</summary>
    public void SetFolders(IReadOnlyList<Folder> list, AccountId account) => Locked(() => folders[account] = [.. list]);

    public void SetFolders(IReadOnlyDictionary<AccountId, IReadOnlyList<Folder>> all) => Locked(() =>
    {
        folders.Clear();
        foreach (var (account, list) in all)
        {
            folders[account] = [.. list];
        }
    });

    /// <summary>The folders of <paramref name="account"/> as <c>folder.list</c> returns them now, counters included.</summary>
    public IReadOnlyList<Folder> Folders(AccountId account) => Locked(() => (IReadOnlyList<Folder>)(folders.TryGetValue(account, out var l) ? [.. l] : []));

    public void SetSyncStates(IReadOnlyList<SyncState> list) => Locked(() => syncStates = [.. list]);

    /// <summary>
    /// Replaces the messages of a folder; each summary gets the folder's
    /// account and folder ids, and the folder's counters follow.
    /// </summary>
    public void SetMessages(IReadOnlyList<MessageSummary> list, AccountId account, FolderId folder) => Locked(() =>
    {
        messages[new FolderKey(account, folder)] = [.. list.Select(s => s with { AccountId = account, FolderId = folder })];
        RefreshCounts(account);
    });

    /// <summary>Adds one message to the folder its summary names.</summary>
    public void AddMessage(MessageSummary s) => Locked(() =>
    {
        var key = new FolderKey(s.AccountId, s.FolderId);
        if (!messages.TryGetValue(key, out var list))
        {
            messages[key] = list = [];
        }
        list.Add(s);
        RefreshCounts(key.Account);
    });

    /// <summary>The stored summary of a message, wherever it is.</summary>
    public MessageSummary? Message(MessageId id) => Locked(() => Locate(id)?.Summary);

    /// <summary>The messages of a folder, in store order.</summary>
    public IReadOnlyList<MessageSummary> Messages(AccountId account, FolderId folder) =>
        Locked(() => (IReadOnlyList<MessageSummary>)(messages.TryGetValue(new FolderKey(account, folder), out var l) ? [.. l] : []));

    /// <summary>What <c>message.body</c> returns for a message; without one it is a plain-text body saying which message it is.</summary>
    public void SetBody(MessageId id, MessageBodyResult body) => Locked(() => bodies[id] = body);

    /// <summary>What <c>message.get</c> returns for a message; without one it is the listing's summary.</summary>
    public void SetDetail(MessageId id, Message detail) => Locked(() => details[id] = detail);

    /// <summary>Makes <paramref name="method"/> fail with <paramref name="error"/> until <see cref="Succeed"/>.</summary>
    public void Fail(string method, RpcError error) => Locked(() => failures[method] = error);

    public void Succeed(string method) => Locked(() => failures.Remove(method));

    /// <summary>Makes <paramref name="method"/> answer after <paramref name="delay"/> on the fixture's clock.</summary>
    public void Delay(string method, TimeSpan delay) => Locked(() => delays[method] = delay);

    /// <summary>Every method the daemon served, in order (fixture and custom handlers alike).</summary>
    public IReadOnlyList<string> Calls() => Daemon.Calls;

    public int CallCount(string method) => Daemon.Calls.Count(m => m == method);

    /// <summary>Registers a handler for a method the fixture does not script; a second registration replaces the first.</summary>
    public void On(string method, FakeDaemon.MethodHandler handler) => Daemon.On(method, handler);

    // Notifications

    /// <summary>Pushes a notification to every connected client.</summary>
    public Task PushAsync(DaemonNotification n)
    {
        ArgumentNullException.ThrowIfNull(n);
        var (method, parameters) = n switch
        {
            DaemonNotification.NewMessage m => (API.Notify.NewMessage, JsonCoding.EncodeToString(m.Payload)),
            DaemonNotification.SyncState s => (API.Notify.SyncState, JsonCoding.EncodeToString(new SyncStateNotification { State = s.State })),
            DaemonNotification.AuthRequired a => (API.Notify.AuthRequired, JsonCoding.EncodeToString(a.Payload)),
            DaemonNotification.AccountsChanged => (API.Notify.AccountsChanged, "{}"),
            DaemonNotification.MessagesChanged c => (API.Notify.MessagesChanged, JsonCoding.EncodeToString(c.Payload)),
            DaemonNotification.Unknown u => (u.Method, "{}"),
            _ => throw new ArgumentException($"no such notification: {n}", nameof(n)),
        };
        return Daemon.PushNotificationAsync(method, parameters);
    }

    // Serving

    private async Task<string> ServeAsync(string method, string paramsJson)
    {
        TimeSpan? delay;
        lock (gate)
        {
            served.Add(method);
            delay = delays.TryGetValue(method, out var d) ? d : null;
        }
        if (delay is { } wait)
        {
            await Task.Delay(wait, time);
        }
        lock (gate)
        {
            if (failures.TryGetValue(method, out var error))
            {
                throw new RpcException(error);
            }
            return Respond(method, paramsJson.Length == 0 ? "{}" : paramsJson);
        }
    }

    // Called under the lock.
    private string Respond(string method, string p)
    {
        switch (method)
        {
            case API.SystemInfoName:
                return JsonCoding.EncodeToString(info);
            case "account.list":
                return JsonCoding.EncodeToString(new AccountListResult { Accounts = accounts });
            case "folder.list":
                {
                    var q = JsonCoding.Decode<FolderListParams>(p);
                    RequireAccount(q.AccountId);
                    return JsonCoding.EncodeToString(new FolderListResult { Folders = folders.TryGetValue(q.AccountId, out var l) ? [.. l] : [] });
                }
            case "sync.status":
                {
                    var q = JsonCoding.Decode<SyncStatusParams>(p);
                    var list = q.AccountId is { } id ? syncStates.Where(s => s.AccountId == id).ToList() : [.. syncStates];
                    return JsonCoding.EncodeToString(new SyncStatusResult { Accounts = list });
                }
            case "sync.trigger":
                triggers.Add(JsonCoding.Decode<SyncTriggerParams>(p));
                return JsonCoding.EncodeToString(new EmptyResult());
            case "config.get":
                return JsonCoding.EncodeToString(new ConfigGetResult { Preferences = preferences });
            case "message.list":
                {
                    var q = JsonCoding.Decode<MessageListParams>(p);
                    listRequests.Add(q);
                    return JsonCoding.EncodeToString(ListMessages(q));
                }
            case "message.get":
                {
                    var q = JsonCoding.Decode<MessageGetParams>(p);
                    RequireAccount(q.AccountId);
                    if (Locate(q.MessageId) is not { } found || found.Key.Account != q.AccountId)
                    {
                        throw Error(ErrorCode.MessageNotFound, $"unknown message {q.MessageId}");
                    }
                    var message = details.TryGetValue(q.MessageId, out var d) ? d : new Message { Summary = found.Summary };
                    return JsonCoding.EncodeToString(new MessageGetResult { Message = message });
                }
            case "message.body":
                {
                    var q = JsonCoding.Decode<MessageBodyParams>(p);
                    bodyRequests.Add(q);
                    RequireAccount(q.AccountId);
                    if (Locate(q.MessageId) is not { } found || found.Key.Account != q.AccountId)
                    {
                        throw Error(ErrorCode.MessageNotFound, $"unknown message {q.MessageId}");
                    }
                    var body = bodies.TryGetValue(q.MessageId, out var b) ? b : new MessageBodyResult
                    {
                        MessageId = q.MessageId,
                        BodyState = BodyState.Fetched,
                        HasHtml = false,
                        Text = $"body of {q.MessageId}",
                        RemoteContent = q.RemoteContent?.Value == RemoteContentPolicy.Allow ? RemoteContentPolicy.Allow : RemoteContentPolicy.Block,
                        SanitizerVersion = "1",
                    };
                    return JsonCoding.EncodeToString(body);
                }
            case "message.flag":
                {
                    var q = JsonCoding.Decode<MessageFlagParams>(p);
                    flagRequests.Add(q);
                    FlagMessages(q);
                    return JsonCoding.EncodeToString(new EmptyResult());
                }
            case "message.move":
                {
                    var q = JsonCoding.Decode<MessageMoveParams>(p);
                    moveRequests.Add(q);
                    MoveMessages(q);
                    return JsonCoding.EncodeToString(new EmptyResult());
                }
            case "message.delete":
                {
                    var q = JsonCoding.Decode<MessageDeleteParams>(p);
                    deleteRequests.Add(q);
                    DeleteMessages(q);
                    return JsonCoding.EncodeToString(new EmptyResult());
                }
            case "thread.list":
                {
                    var q = JsonCoding.Decode<ThreadListParams>(p);
                    threadListRequests.Add(q);
                    return JsonCoding.EncodeToString(ListThreads(q));
                }
            case "thread.get":
                {
                    var q = JsonCoding.Decode<ThreadGetParams>(p);
                    threadGetRequests.Add(q);
                    return JsonCoding.EncodeToString(GetThread(q));
                }
            default:
                throw Error(ErrorCode.MethodNotFound, $"unknown method {method}");
        }
    }

    // Messages

    private void RequireAccount(AccountId id)
    {
        if (!accounts.Any(a => a.Id == id))
        {
            throw Error(ErrorCode.AccountNotFound, $"unknown account {id}");
        }
    }

    private Folder? FolderOf(FolderKey key) =>
        folders.TryGetValue(key.Account, out var list) ? list.FirstOrDefault(f => f.Id == key.Folder) : null;

    private Folder RequireFolder(FolderKey key) => FolderOf(key) ?? throw Error(ErrorCode.FolderNotFound, $"unknown folder {key.Folder}");

    private Located? Locate(MessageId id)
    {
        foreach (var (key, list) in messages)
        {
            var i = list.FindIndex(s => s.Id == id);
            if (i >= 0)
            {
                return new Located(key, i, list[i]);
            }
        }
        return null;
    }

    /// <summary>Every message of an account, wherever it is.</summary>
    private List<MessageSummary> AllMessages(AccountId account) =>
        [.. messages.Where(e => e.Key.Account == account).SelectMany(e => e.Value)];

    /// <summary>Sets the folder counters of an account from its messages (the daemon's counters reflect flag changes at once).</summary>
    private void RefreshCounts(AccountId account)
    {
        if (!folders.TryGetValue(account, out var list))
        {
            return;
        }
        for (var i = 0; i < list.Count; i++)
        {
            var msgs = messages.TryGetValue(new FolderKey(account, list[i].Id), out var m) ? m : [];
            list[i] = list[i] with { Total = msgs.Count, Unread = msgs.Count(s => !HasFlag(s.Flags, Flag.Seen)) };
        }
    }

    /// <summary>The daemon's list order: <c>dateDesc</c> unless asked otherwise, ties by id so the order is stable across calls.</summary>
    private static List<MessageSummary> Sorted(IEnumerable<MessageSummary> list, SortOrder? sort) =>
        sort?.Value == SortOrder.DateAsc
            ? [.. list.OrderBy(s => s.Date).ThenBy(s => s.Id.Value, StringComparer.Ordinal)]
            : [.. list.OrderByDescending(s => s.Date).ThenBy(s => s.Id.Value, StringComparer.Ordinal)];

    /// <summary>Cursor = offset as a decimal string, limit clamped to the daemon's bounds (docs/api.md §4.3).</summary>
    private static (List<T> Slice, PageInfo Page) Paged<T>(List<T> list, Page p)
    {
        var offset = 0;
        if (!string.IsNullOrEmpty(p.Cursor))
        {
            if (!int.TryParse(p.Cursor, NumberStyles.None, CultureInfo.InvariantCulture, out offset))
            {
                throw Error(ErrorCode.InvalidArgument, "bad cursor");
            }
        }
        var limit = Math.Min(Math.Max(p.Limit ?? API.Limits.DefaultPageLimit, 1), API.Limits.MaxPageLimit);
        var end = Math.Min(offset + limit, list.Count);
        List<T> slice = offset < list.Count ? list[offset..end] : [];
        var next = end < list.Count ? end.ToString(CultureInfo.InvariantCulture) : null;
        return (slice, new PageInfo { NextCursor = next, Total = list.Count });
    }

    private MessageListResult ListMessages(MessageListParams p)
    {
        RequireAccount(p.AccountId);
        var key = new FolderKey(p.AccountId, p.FolderId);
        RequireFolder(key);
        var filter = p.Filter ?? MessageFilter.All;
        if (p.Filter is null && p.UnreadOnly == true)
        {
            filter = MessageFilter.Unread;
        }
        var list = Sorted((messages.TryGetValue(key, out var l) ? l : []).Where(s => MatchesFilter(s, filter)), p.Sort);
        var (slice, page) = Paged(list, p.Page);
        return new MessageListResult { Messages = slice, Page = page };
    }

    private void FlagMessages(MessageFlagParams p)
    {
        RequireAccount(p.AccountId);
        var set = p.Set ?? [];
        var clear = p.Clear ?? [];
        if (p.MessageIds.Count == 0 || p.MessageIds.Count > API.Limits.MaxMessageIdsPerCall || (set.Count == 0 && clear.Count == 0)
            || HasFlag(set, Flag.Deleted) || HasFlag(clear, Flag.Deleted) || set.Any(f => HasFlag(clear, f)))
        {
            throw Error(ErrorCode.InvalidArgument, "bad flag change");
        }
        var found = LocateAll(p.MessageIds, p.AccountId);
        foreach (var l in found.Where(l => FolderOf(l.Key)?.Role.Value == FolderRole.Outbox))
        {
            throw Error(ErrorCode.InvalidArgument, $"outbox message {l.Summary.Id}");
        }
        foreach (var l in found)
        {
            var list = messages[l.Key];
            list[l.Index] = list[l.Index] with { Flags = ApplyFlagChange(l.Summary.Flags, set, clear) };
        }
        RefreshCounts(p.AccountId);
    }

    private List<Located> LocateAll(IReadOnlyList<MessageId> ids, AccountId account)
    {
        var found = new List<Located>();
        foreach (var id in ids)
        {
            if (Locate(id) is not { } l || l.Key.Account != account)
            {
                throw Error(ErrorCode.MessageNotFound, $"unknown message {id}");
            }
            found.Add(l);
        }
        return found;
    }

    private void MoveMessages(MessageMoveParams p)
    {
        RequireAccount(p.AccountId);
        if (p.MessageIds.Count == 0 || p.MessageIds.Count > API.Limits.MaxMessageIdsPerCall)
        {
            throw Error(ErrorCode.InvalidArgument, "bad ids");
        }
        var target = RequireFolder(new FolderKey(p.AccountId, p.TargetFolderId));
        if (!target.Selectable)
        {
            throw Error(ErrorCode.InvalidArgument, "target not selectable");
        }
        var found = LocateAll(p.MessageIds, p.AccountId);
        foreach (var l in found.Where(l => FolderOf(l.Key)?.Role.Value == FolderRole.Outbox))
        {
            throw Error(ErrorCode.InvalidArgument, $"outbox message {l.Summary.Id}");
        }
        Relocate(found, target, p.AccountId);
    }

    /// <summary>Moves messages into <paramref name="target"/>, or out of the store when it is not synchronised; null removes them.</summary>
    private void Relocate(List<Located> found, Folder? target, AccountId account)
    {
        FolderKey? targetKey = target is null ? null : new FolderKey(account, target.Id);
        foreach (var l in found)
        {
            if (l.Key == targetKey)
            {
                continue;
            }
            messages[l.Key].RemoveAll(s => s.Id == l.Summary.Id);
            if (target is { Synced: true } && targetKey is { } tk)
            {
                if (!messages.TryGetValue(tk, out var list))
                {
                    messages[tk] = list = [];
                }
                list.Add(l.Summary with { FolderId = target.Id });
            }
        }
        RefreshCounts(account);
    }

    private void DeleteMessages(MessageDeleteParams p)
    {
        RequireAccount(p.AccountId);
        if (p.MessageIds.Count == 0 || p.MessageIds.Count > API.Limits.MaxMessageIdsPerCall)
        {
            throw Error(ErrorCode.InvalidArgument, "bad ids");
        }
        var found = LocateAll(p.MessageIds, p.AccountId);
        var trash = folders.TryGetValue(p.AccountId, out var list) ? list.FirstOrDefault(f => f.Role.Value == FolderRole.Trash) : null;
        if (p.Permanent != true && trash is null && found.Any(l => FolderOf(l.Key)?.Role.Value != FolderRole.Outbox))
        {
            throw Error(ErrorCode.FolderNotFound, "no trash folder");
        }
        // Outbox messages are cancelled (removed), messages in Trash and
        // permanent deletes go too; the rest move to Trash.
        var remove = new List<Located>();
        var move = new List<Located>();
        foreach (var l in found)
        {
            var role = FolderOf(l.Key)?.Role.Value;
            if (p.Permanent == true || role == FolderRole.Outbox || role == FolderRole.Trash)
            {
                remove.Add(l);
            }
            else
            {
                move.Add(l);
            }
        }
        Relocate(remove, null, p.AccountId);
        Relocate(move, trash, p.AccountId);
    }

    // Threads

    /// <summary>The conversation key of a message: its thread id, or its own id for one an older daemon never linked.</summary>
    private static ThreadId ThreadKey(MessageSummary s) => s.ThreadId ?? new ThreadId("unlinked:" + s.Id.Value);

    /// <summary>Aggregates a conversation over its members (oldest first), as docs/api.md §4.4 describes thread.list's summary; a jira thread's issue is the latest member's.</summary>
    private ThreadSummary Aggregate(ThreadId tid, List<MessageSummary> members, AccountId account, FolderId? folder = null)
    {
        var latest = members[^1];
        var flags = new List<Flag>();
        var senders = new List<Address>();
        var attachments = false;
        var unread = 0;
        foreach (var s in Enumerable.Reverse(members))
        {
            flags = UnionFlags(flags, s.Flags);
            senders.AddRange(s.From);
            attachments = attachments || s.HasAttachments;
            if (!HasFlag(s.Flags, Flag.Seen))
            {
                unread++;
            }
        }
        var folderIds = messages
            .Where(e => e.Key.Account == account && e.Value.Any(s => ThreadKey(s) == tid))
            .Select(e => e.Key.Folder)
            .OrderBy(f => f.Value, StringComparer.Ordinal)
            .ToList();
        return new ThreadSummary
        {
            Id = tid,
            AccountId = account,
            Subject = StripMarkers(latest.Subject),
            Participants = FrontParticipants([], senders),
            MessageCount = members.Count,
            UnreadCount = unread,
            LatestDate = latest.Date,
            Latest = latest,
            Snippet = latest.Snippet,
            Flags = flags,
            HasAttachments = attachments,
            FolderIds = folderIds,
            Issue = latest.Issue?.Info,
            SentCount = folder is { } f ? Sent(tid, new FolderKey(account, f), members).Count : 0,
        };
    }

    /// <summary>
    /// The members of conversation <paramref name="tid"/> in the sent folders
    /// of <paramref name="key"/>'s account that are not among
    /// <paramref name="members"/> (the folder's), oldest first; none in a sent
    /// folder, the outbox and a jira account.
    /// </summary>
    private List<MessageSummary> Sent(ThreadId tid, FolderKey key, List<MessageSummary> members)
    {
        var role = FolderOf(key)?.Role;
        var jira = accounts.FirstOrDefault(a => a.Id == key.Account)?.Config.Kind == AccountKind.Jira;
        if (role == FolderRole.Sent || role == FolderRole.Outbox || jira)
        {
            return [];
        }
        var own = members.Select(s => s.Id).ToHashSet();
        var list = (folders.TryGetValue(key.Account, out var fs) ? fs : [])
            .Where(f => f.Role == FolderRole.Sent)
            .SelectMany(f => messages.TryGetValue(new FolderKey(key.Account, f.Id), out var l) ? l : [])
            .Where(s => ThreadKey(s) == tid && !own.Contains(s.Id));
        return Sorted(list, SortOrder.DateAsc);
    }

    /// <summary>Drops leading Re:/Fwd: markers, the way the daemon names a thread.</summary>
    private static string StripMarkers(string subject)
    {
        var s = subject.Trim(' ', '\t');
        while (true)
        {
            var marker = Markers.FirstOrDefault(m => s.StartsWith(m, StringComparison.OrdinalIgnoreCase));
            if (marker is null)
            {
                break;
            }
            var rest = s[marker.Length..].Trim(' ', '\t');
            if (rest.Length == 0)
            {
                break;
            }
            s = rest;
        }
        return s;
    }

    /// <summary>The conversations of a folder with their members, oldest first.</summary>
    private List<(ThreadSummary Summary, List<MessageSummary> Members)> Threads(FolderKey key)
    {
        var groups = new Dictionary<ThreadId, List<MessageSummary>>();
        foreach (var s in messages.TryGetValue(key, out var l) ? l : [])
        {
            var tid = ThreadKey(s);
            if (!groups.TryGetValue(tid, out var g))
            {
                groups[tid] = g = [];
            }
            g.Add(s);
        }
        return [.. groups.Select(e =>
        {
            var members = Sorted(e.Value, SortOrder.DateAsc);
            return (Aggregate(e.Key, members, key.Account, key.Folder), members);
        })];
    }

    private ThreadListResult ListThreads(ThreadListParams p)
    {
        RequireAccount(p.AccountId);
        var key = new FolderKey(p.AccountId, p.FolderId);
        RequireFolder(key);
        var filter = p.Filter?.Value ?? MessageFilter.All;
        var list = Threads(key).Select(t => t.Summary).Where(t => filter switch
        {
            MessageFilter.Unread => t.UnreadCount > 0,
            MessageFilter.Flagged => HasFlag(t.Flags, Flag.Flagged),
            _ => true,
        });
        list = p.Sort?.Value == SortOrder.DateAsc
            ? list.OrderBy(t => t.LatestDate).ThenBy(t => t.Id.Value, StringComparer.Ordinal)
            : list.OrderByDescending(t => t.LatestDate).ThenBy(t => t.Id.Value, StringComparer.Ordinal);
        var (slice, page) = Paged(list.ToList(), p.Page);
        return new ThreadListResult { Threads = slice, Page = page };
    }

    private ThreadGetResult GetThread(ThreadGetParams p)
    {
        RequireAccount(p.AccountId);
        List<MessageSummary> pool;
        if (p.FolderId is { } fid)
        {
            var key = new FolderKey(p.AccountId, fid);
            RequireFolder(key);
            pool = messages.TryGetValue(key, out var l) ? [.. l] : [];
        }
        else
        {
            pool = AllMessages(p.AccountId);
        }
        pool = [.. pool.Where(s => ThreadKey(s) == p.ThreadId)];
        if (pool.Count == 0)
        {
            throw Error(ErrorCode.ThreadNotFound, $"unknown thread {p.ThreadId}");
        }
        var members = Sorted(pool, SortOrder.DateAsc);
        var summary = Aggregate(p.ThreadId, members, p.AccountId, p.FolderId);
        List<MessageSummary> replies = p.WithSent == true && p.FolderId is { } folder ? Sent(p.ThreadId, new FolderKey(p.AccountId, folder), members) : [];
        return new ThreadGetResult
        {
            Thread = summary,
            Messages = members.TakeLast(API.Limits.MaxThreadMessages).ToList(),
            Sent = replies.TakeLast(API.Limits.MaxThreadMessages).ToList(),
        };
    }

    // The daemon's flag rules (docs/api.md §4.3, message.flag; thread.list's aggregates).

    private static bool HasFlag(IReadOnlyList<Flag> flags, string flag) => flags.Any(f => f.Value == flag);

    private static bool HasFlag(IReadOnlyList<Flag> flags, Flag flag) => flags.Contains(flag);

    private static bool MatchesFilter(MessageSummary s, MessageFilter filter) => filter.Value switch
    {
        MessageFilter.Unread => !HasFlag(s.Flags, Flag.Seen),
        MessageFilter.Flagged => HasFlag(s.Flags, Flag.Flagged),
        _ => true,
    };

    // flags with set added and clear removed; a flag in both ends up set.
    private static List<Flag> ApplyFlagChange(IReadOnlyList<Flag> flags, IReadOnlyList<Flag> set, IReadOnlyList<Flag> clear)
    {
        var @out = flags.Where(f => !HasFlag(clear, f) || HasFlag(set, f)).ToList();
        foreach (var f in set.Where(f => !HasFlag(@out, f)))
        {
            @out.Add(f);
        }
        return @out;
    }

    // a ∪ b in a's order, then b's.
    private static List<Flag> UnionFlags(IReadOnlyList<Flag> a, IReadOnlyList<Flag> b)
    {
        var @out = a.ToList();
        foreach (var f in b.Where(f => !HasFlag(@out, f)))
        {
            @out.Add(f);
        }
        return @out;
    }

    // The senders of the newest message first, each address once
    // (case-insensitively), at most MaxThreadParticipants.
    private static List<Address> FrontParticipants(IReadOnlyList<Address> list, IReadOnlyList<Address> from)
    {
        var @out = new List<Address>();
        var seen = new HashSet<string>(StringComparer.Ordinal);
        foreach (var a in from.Concat(list))
        {
            var key = a.Email.Trim().ToLowerInvariant();
            if (key.Length == 0)
            {
                key = "name:" + (a.Name ?? "").Trim().ToLowerInvariant();
            }
            if (key == "name:" || !seen.Add(key))
            {
                continue;
            }
            @out.Add(a);
            if (@out.Count == API.Limits.MaxThreadParticipants)
            {
                break;
            }
        }
        return @out;
    }

    private static RpcException Error(int code, string message) => new(new RpcError { Code = code, Message = message });

    private T Locked<T>(Func<T> read)
    {
        lock (gate)
        {
            return read();
        }
    }

    private void Locked(Action change)
    {
        lock (gate)
        {
            change();
        }
    }

    private IReadOnlyList<T> Snapshot<T>(List<T> list)
    {
        lock (gate)
        {
            return [.. list];
        }
    }

    /// <summary>A folder across accounts.</summary>
    private readonly record struct FolderKey(AccountId Account, FolderId Folder);

    private sealed record Located(FolderKey Key, int Index, MessageSummary Summary);
}
