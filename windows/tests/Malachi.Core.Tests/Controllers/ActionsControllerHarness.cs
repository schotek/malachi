// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the Harness, ActionLog and helpers of
// macos/Tests/MalachiCoreTests/ActionsControllerTests.swift: a MailFixture,
// a connected client, the folder and list halves, the message cache and the
// actions controller over a throwaway settings store, with the initial
// folder (the inbox) listed.
//
// Swift's harness drives the real MailboxController, ListController and
// MessageCache. The actions reach those through IActionsMailbox,
// IActionsList and IActionsCache; the halves below implement the first two
// over the same MailModel, with the parts of the Swift controllers the suite
// exercises ported next to them: loading the accounts, the folders (with
// the outbox's delivery tracking) and the first page of a folder in either
// mode, removing rows with their restore (messages.go removeRows /
// removeMessageRow) and the conversation members of a selected row
// (threads.go selectedIDs, ensureMembers). The cache is the real
// MessageCache, as in Swift, on the fixture's clock (its download spinner)
// and counted in the harness's work. The mailbox's scope is the group's, as
// in the app.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Settings;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Tests.Model;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Time.Testing;
using Xunit;

namespace Malachi.Core.Tests.Controllers;

/// <summary>One confirmation the controller asked for.</summary>
internal sealed record Confirmation(string Heading, string Body, string Label);

/// <summary>What the controllers emitted, in order; touched on the UI thread, read by the test once it is idle.</summary>
internal sealed class ActionLog
{
    public List<string> Toasts { get; } = [];

    public List<Confirmation> Confirmations { get; } = [];

    /// <summary>What the next confirmation answers.</summary>
    public bool Answer { get; set; } = true;

    public List<ComposeParams> Composed { get; } = [];

    /// <summary>The drafts RaiseDraft was asked about.</summary>
    public List<Draft> Raised { get; } = [];

    /// <summary>What RaiseDraft answers.</summary>
    public bool Raise { get; set; }

    public List<MessageId> MessageWindows { get; } = [];

    public List<MessageId> ClosedWindows { get; } = [];

    /// <summary>"id:flagged" per star change.</summary>
    public List<string> Stars { get; } = [];

    /// <summary>"id:seen" per seen change a message window hears of.</summary>
    public List<string> Seen { get; } = [];

    public List<MessageId> OutboxStates { get; } = [];

    /// <summary>"id:loading" per remote-bar redraw.</summary>
    public List<string> Bars { get; } = [];
}

/// <summary>
/// A logger that keeps what a controller logged, level and message, for the
/// tests that check a failure is not lost.
/// </summary>
internal sealed class RecordingLogger<T> : ILogger<T>
{
    private readonly List<(LogLevel Level, string Message)> entries = [];

    /// <summary>What was logged so far, in order.</summary>
    public IReadOnlyList<(LogLevel Level, string Message)> Entries
    {
        get
        {
            lock (entries)
            {
                return [.. entries];
            }
        }
    }

    public IDisposable? BeginScope<TState>(TState state)
        where TState : notnull => null;

    public bool IsEnabled(LogLevel logLevel) => true;

    public void Log<TState>(LogLevel logLevel, EventId eventId, TState state, Exception? exception, Func<TState, Exception?, string> formatter)
    {
        ArgumentNullException.ThrowIfNull(formatter);
        lock (entries)
        {
            entries.Add((logLevel, formatter(state, exception)));
        }
    }
}

/// <summary>The harness of the actions tests.</summary>
internal sealed class ActionsControllerHarness : IAsyncDisposable
{
    public static readonly AccountId Acc = "a";
    public static readonly FolderKey Inbox = new("a", "in");
    public static readonly FolderKey Trash = new("a", "trash");
    public static readonly FolderKey ArchiveFolder = new("a", "arch");
    public static readonly FolderKey JunkFolder = new("a", "junk");
    public static readonly FolderKey OutboxFolder = new("a", "out");
    public static readonly FolderKey Drafts = new("a", "dr");

    /// <summary>2026-09-01T10:00:00Z.</summary>
    public static readonly DateTimeOffset Base = new(2026, 9, 1, 10, 0, 0, TimeSpan.Zero);

    private ActionsControllerHarness(MailFixture fixture, FakeTimeProvider time, RpcClient client, SettingsStore settings)
    {
        Fixture = fixture;
        Time = time;
        Client = client;
        Settings = settings;
    }

    public TestUIContext Ui { get; } = new();

    public PendingWork Pending { get; } = new();

    public MailFixture Fixture { get; }

    /// <summary>The fixture's clock, which its delays run on.</summary>
    public FakeTimeProvider Time { get; }

    public RpcClient Client { get; }

    public SettingsStore Settings { get; }

    public ActionLog Log { get; } = new();

    /// <summary>What the actions controller logged.</summary>
    public RecordingLogger<ActionsController> Logger { get; } = new();

    public MailboxHalf Mailbox { get; private set; } = null!;

    public ListHalf List { get; private set; } = null!;

    public MessageCache Cache { get; private set; } = null!;

    public ActionsController Actions { get; private set; } = null!;

    /// <summary>A message dated <paramref name="hours"/> after <see cref="Base"/> in the inbox; unread unless <see cref="Flag.Seen"/> is given.</summary>
    public static MessageSummary Msg(string id, int hours, string from = "alice", string? thread = null, IReadOnlyList<Flag>? flags = null) => new()
    {
        Id = id,
        AccountId = Acc,
        FolderId = Inbox.Folder,
        ThreadId = thread is null ? (ThreadId?)null : new ThreadId(thread),
        From = [new Address { Name = from, Email = from + "@example.invalid" }],
        Subject = "s-" + id,
        Date = Base.AddHours(hours),
        Snippet = "p-" + id,
        Flags = flags ?? [],
        HasAttachments = false,
        Size = 0,
    };

    public static Folder[] TestFolders() =>
    [
        MailModelTests.TestFolder("in", "INBOX", FolderRole.Inbox),
        MailModelTests.TestFolder("trash", "Trash", FolderRole.Trash),
        MailModelTests.TestFolder("arch", "Archive", FolderRole.Archive),
        MailModelTests.TestFolder("junk", "Junk", FolderRole.Junk),
        MailModelTests.TestFolder("out", "Outbox", FolderRole.Outbox),
    ];

    /// <summary>
    /// The conversations of the grouped tests: t1 with three members, t2 with
    /// one, t3 with two; t3 is the newest.
    /// </summary>
    public static MessageSummary[] ThreadedMessages() =>
    [
        Msg("a1", 1, from: "bob", thread: "t1", flags: [Flag.Seen]),
        Msg("a2", 2, from: "alice", thread: "t1", flags: [Flag.Seen]),
        Msg("a3", 3, from: "carol", thread: "t1"),
        Msg("b1", 5, from: "dave", thread: "t2", flags: [Flag.Seen]),
        Msg("c1", 4, from: "erin", thread: "t3", flags: [Flag.Seen]),
        Msg("c2", 6, from: "frank", thread: "t3"),
    ];

    /// <summary>The rows as ids, a conversation row as "T:" and its thread.</summary>
    public static string[] Ids(IEnumerable<ListRow> rows) =>
        [.. rows.Select(r => r.Thread ? "T:" + (r.Key.Thread?.Value ?? "") : r.Message.Id.Value)];

    public static MessageBodyResult Body(string id, string? html, string remote) => new()
    {
        MessageId = id,
        BodyState = BodyState.Fetched,
        HasHtml = html is not null,
        Html = html,
        Text = $"text of {id}",
        Blocked = new BlockedContent { RemoteImages = remote == RemoteContentPolicy.Block ? 2 : 0 },
        RemoteContent = remote,
        SanitizerVersion = "1",
    };

    /// <summary>
    /// A fixture with the account "a" and <paramref name="folders"/>, the
    /// given messages per folder, a connected client and the controllers,
    /// with the inbox listed.
    /// </summary>
    public static async Task<ActionsControllerHarness> StartAsync(
        IReadOnlyList<Folder>? folders = null,
        IReadOnlyDictionary<FolderKey, MessageSummary[]>? messages = null,
        bool grouped = false,
        bool confirmDelete = true)
    {
        var time = new FakeTimeProvider(Base);
        var fixture = new MailFixture(time);
        fixture.SetAccounts([MailModelTests.TestAccount("a", email: "me@example.invalid", displayName: "Me")]);
        fixture.SetFolders(folders ?? TestFolders(), Acc);
        foreach (var (k, list) in messages ?? new Dictionary<FolderKey, MessageSummary[]>())
        {
            fixture.SetMessages(list, k.Account, k.Folder);
        }
        await fixture.StartAsync();
        var client = new RpcClient(fixture.Path, PortableKeyFilePolicy.Instance);
        await client.ConnectAsync(TestContext.Current.CancellationToken);
        var settings = new SettingsStore(new InMemorySettingsBackend(), null)
        {
            GroupByConversation = grouped,
            ConfirmDelete = confirmDelete,
            MarkReadDelay = 0,
        };
        var h = new ActionsControllerHarness(fixture, time, client, settings);
        await h.Ui.RunAsync(() =>
        {
            var scope = new ControllerScope(h.Pending);
            var log = h.Log;
            h.Mailbox = new MailboxHalf(scope, client, settings, log.Toasts.Add);
            h.List = new ListHalf(h.Mailbox);
            h.Mailbox.List = h.List;
            h.Cache = new MessageCache(client, log.Toasts.Add, pending: h.Pending, timeProvider: time);
            h.Cache.RemoteBarChanged += (_, e) => log.Bars.Add($"{e.Id.Value}:{(e.Loaded.LoadingImages ? "true" : "false")}");
            var actions = new ActionsController(h.Mailbox, h.List, h.Cache, settings, log.Toasts.Add, h.Logger)
            {
                Confirm = (_, heading, body, label) =>
                {
                    log.Confirmations.Add(new Confirmation(heading, body, label));
                    return Task.FromResult(log.Answer);
                },
                RaiseDraft = d =>
                {
                    log.Raised.Add(d);
                    return log.Raise;
                },
            };
            actions.OpenComposeRequested += (_, p) => log.Composed.Add(p);
            actions.OpenMessageWindowRequested += (_, s) => log.MessageWindows.Add(s.Id);
            actions.WindowsClose += (_, id) => log.ClosedWindows.Add(id);
            actions.StarChanged += (_, e) => log.Stars.Add($"{e.Id.Value}:{(e.Flagged ? "true" : "false")}");
            actions.SeenChanged += (_, e) => log.Seen.Add($"{e.Id.Value}:{(e.Seen ? "true" : "false")}");
            actions.OutboxStateChanged += (_, id) => log.OutboxStates.Add(id);
            h.Actions = actions;
            h.Mailbox.LoadAccounts();
        });
        await h.IdleAsync();
        Assert.Equal(Inbox, h.Mailbox.Model.ListFolder);
        return h;
    }

    /// <summary>Runs <paramref name="action"/> on the UI thread.</summary>
    public Task Run(Action action) => Ui.RunAsync(action);

    /// <summary>Everything done: the controllers, the daemon, the UI queue.</summary>
    public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending, Fixture.Daemon);

    /// <summary>Lists folder <paramref name="k"/> and waits for its first page.</summary>
    public async Task SelectAsync(FolderKey k)
    {
        await Run(() => Mailbox.Select(k));
        await IdleAsync();
        Assert.Equal(k, Mailbox.Model.ListFolder);
        Assert.False(Mailbox.Model.Loading);
    }

    /// <summary>The cached unread count of a folder (the sidebar badge).</summary>
    public int Unread(FolderKey k) => Mailbox.Model.Folder(k)?.Unread ?? -1;

    /// <summary>The summary the list holds for <paramref name="id"/>.</summary>
    public MessageSummary Summary(MessageId id) => Mailbox.Model.Message(id)?.Summary ?? throw new InvalidOperationException($"{id} is not listed");

    /// <summary>Loads <paramref name="id"/> into the cache (message.get and message.body).</summary>
    public async Task LoadAsync(MessageId id)
    {
        var s = Summary(id);
        await Run(() => Cache.Fetch(s, static _ => { }));
        await IdleAsync();
        Assert.True(Cache.Loaded(id)?.Complete);
    }

    public async ValueTask DisposeAsync()
    {
        await Ui.RunAsync(() =>
        {
            Mailbox.Scope.Close();
            Cache.Dispose();
        });
        Client.Dispose();
        await Fixture.DisposeAsync();
        Ui.Dispose();
    }

    /// <summary>
    /// The folder half (MailboxController.swift's share of the suite):
    /// accounts, folders with the outbox's delivery tracking, the counts.
    /// </summary>
    internal sealed class MailboxHalf(ControllerScope scope, RpcClient client, SettingsStore settings, Action<string> toast) : IActionsMailbox
    {
        private readonly OutboxTracker outbox = new();

        public MailModel Model { get; } = new(grouped: settings.GroupByConversation);

        public ControllerScope Scope => scope;

        public RpcClient Client => client;

        public SettingsStore Settings => settings;

        public ListHalf? List { get; set; }

        /// <summary>folders.go loadAccounts: account.list, folder.list per enabled account, the sidebar, the initial folder.</summary>
        public void LoadAccounts()
        {
            Scope.Perform(Client, API.AccountList, new EmptyParams(), outcome =>
            {
                Model.Accounts = Required(outcome).Accounts;
                var left = Model.EnabledAccounts.Count;
                foreach (var a in Model.EnabledAccounts)
                {
                    FetchFolders(a.Id, () =>
                    {
                        if (--left == 0)
                        {
                            Model.RebuildEntries();
                            Select(Model.InitialFolder() ?? throw new InvalidOperationException("no initial folder"));
                        }
                    });
                }
            });
        }

        /// <summary>folders.go fetchFolders with outbox.go trackOutbox.</summary>
        public void FetchFolders(AccountId acc, Action? then = null)
        {
            Scope.Perform(Client, API.FolderList, new FolderListParams { AccountId = acc }, outcome =>
            {
                Model.Folders[acc] = Required(outcome).Folders;
                var total = Model.FolderByRole(acc, FolderRole.Outbox)?.Total ?? 0;
                var sent = outbox.Track(acc, total);
                if (sent > 0)
                {
                    toast(L10n.N("%d message sent", "%d messages sent", sent));
                }
                then?.Invoke();
            });
        }

        /// <summary>folders.go selectFolder: the list follows.</summary>
        public void Select(FolderKey k)
        {
            Model.Selected = k;
            List!.LoadMessages();
        }

        public void AdjustCounts(FolderKey k, int dUnread, int dTotal) => Model.AdjustCounts(k, dUnread, dTotal);

        public void MoveCounts(FolderKey src, FolderKey? target, int unread, int n) => Model.MoveCounts(src, target, unread, n);

        public void NoteOutboxCancelled(AccountId acc) => outbox.NoteCancelled(acc);

        public void NoteOutboxCancelFailed(AccountId acc) => outbox.NoteCancelFailed(acc);

        public void OnOutboxChanged(AccountId acc) => FetchFolders(acc, () =>
        {
            Model.RebuildEntries();
            List!.RefreshOutboxViews(acc);
        });

        private static T Required<T>(Outcome<T> outcome) =>
            outcome.TryGetValue(out var value, out var error) ? value : throw new InvalidOperationException("the fixture failed a load", error);
    }

    /// <summary>
    /// The list half (MailboxController+List.swift's share of the suite):
    /// the first page in either mode, the rows, the selection and its
    /// members, the optimistic removal with its restore.
    /// </summary>
    internal sealed class ListHalf(MailboxHalf mailbox) : IActionsList
    {
        private readonly Dictionary<ThreadId, List<Action>> waiters = [];

        public ListKey? SelectedKey { get; private set; }

        public ListRow? SelectedRow => SelectedKey is { } k ? Row(k) : null;

        /// <summary>The rows the model has right now, in either mode.</summary>
        public IReadOnlyList<ListRow> Rows => Model.Grouped
            ? Model.Rows
            : [.. Model.Messages.Select(s => new ListRow { Key = new ListKey(Message: s.Id), Message = s })];

        /// <summary>The listed folder is the account's outbox.</summary>
        public bool InOutbox => Model.ListFolder is { } k && Model.FolderRole(k) == FolderRole.Outbox;

        private MailModel Model => mailbox.Model;

        public ListRow? Row(ListKey key) => Model.RowAt(Model.RowIndexOf(key));

        public void Select(ListKey? key) => SelectedKey = key;

        /// <summary>messages.go loadMessages / threads.go loadThreadPage for the selected folder.</summary>
        public void LoadMessages()
        {
            var k = Model.Selected;
            var gen = Model.BumpList();
            var grouped = mailbox.Settings.GroupByConversation && (k is not { } sel || Model.FolderRole(sel) != FolderRole.Outbox);
            if (k != Model.ListFolder || grouped != Model.Grouped)
            {
                Model.ListFolder = k;
                Model.Grouped = grouped;
                Model.ClearMessages();
                SelectedKey = null;
            }
            if (k is not { } key)
            {
                Model.Loading = false;
                return;
            }
            Model.Loading = true;
            var page = new Page { Limit = API.Limits.DefaultPageLimit };
            if (grouped)
            {
                var threadParams = new ThreadListParams { AccountId = key.Account, FolderId = key.Folder, Page = page, Sort = SortOrder.DateDesc, Filter = Model.ListFilter };
                mailbox.Scope.Perform(mailbox.Client, API.ThreadList, threadParams, outcome =>
                {
                    if (gen != Model.ListGen)
                    {
                        return;
                    }
                    Model.Loading = false;
                    var res = outcome.Value ?? throw new InvalidOperationException("thread.list failed", outcome.Error);
                    Model.SetThreads(res.Threads, res.Page);
                });
                return;
            }
            var listParams = new MessageListParams { AccountId = key.Account, FolderId = key.Folder, Page = page, Sort = SortOrder.DateDesc, Filter = Model.ListFilter };
            mailbox.Scope.Perform(mailbox.Client, API.MessageList, listParams, outcome =>
            {
                if (gen != Model.ListGen)
                {
                    return;
                }
                Model.Loading = false;
                var res = outcome.Value ?? throw new InvalidOperationException("message.list failed", outcome.Error);
                Model.SetMessages(res.Messages, res.Page);
            });
        }

        /// <summary>The list's share of outbox.go refreshOutboxViews.</summary>
        public void RefreshOutboxViews(AccountId acc)
        {
            if (Model.FolderByRole(acc, FolderRole.Outbox) is { } box && Model.ListFolder == new FolderKey(acc, box.Id))
            {
                LoadMessages();
            }
        }

        /// <summary>
        /// threads.go selectedIDs: the selected row and every message it
        /// stands for, the members fetched first when they are not known.
        /// </summary>
        public void SelectedIds(Action<ListRow, IReadOnlyList<MessageId>> then)
        {
            if (SelectedRow is not { } row)
            {
                return;
            }
            if (Model.RowIds(row) is { } ids)
            {
                then(row, ids);
                return;
            }
            if (row.Key.Thread is not { } tid)
            {
                return;
            }
            EnsureMembers(tid, () =>
            {
                if (SelectedRow is { Thread: true } r && r.Key.Thread == tid && Model.RowIds(r) is { } members)
                {
                    then(r, members);
                }
            });
        }

        /// <summary>thread_model.go flagTarget.</summary>
        public static bool FlagTarget(ListRow row) => MailModel.FlagTarget(row);

        public IReadOnlyList<MessageId> ApplyFlags(IReadOnlyList<MessageId> ids, IReadOnlyList<Flag>? setFlags = null, IReadOnlyList<Flag>? clearFlags = null) =>
            Model.ApplyFlags(ids, setFlags, clearFlags);

        /// <summary>messages.go removeRows.</summary>
        public Action RemoveRows(IReadOnlyList<MessageId> ids)
        {
            if (!Model.Grouped)
            {
                var restores = ids.Select(RemoveMessageRow).ToArray();
                return () =>
                {
                    foreach (var r in restores.Reverse())
                    {
                        r();
                    }
                };
            }
            if (Model.RemoveMessages(ids) is not { } removal)
            {
                LoadMessages();
                return static () => { };
            }
            var gen = Model.ListGen;
            return () =>
            {
                if (Model.ListGen == gen)
                {
                    Model.RestoreRemoval(removal);
                }
            };
        }

        // messages.go removeMessageRow.
        private Action RemoveMessageRow(MessageId id)
        {
            if (Model.RemoveMessage(id) is not { } removed)
            {
                return static () => { };
            }
            var gen = Model.ListGen;
            return () =>
            {
                if (Model.ListGen == gen)
                {
                    Model.InsertMessage(removed.Index, removed.Summary);
                }
            };
        }

        // threads.go ensureMembers.
        private void EnsureMembers(ThreadId tid, Action then)
        {
            if (!Model.Members.TryGetValue(tid, out var mem))
            {
                return;
            }
            if (mem.Complete)
            {
                then();
                return;
            }
            if (!waiters.TryGetValue(tid, out var list))
            {
                waiters[tid] = list = [];
            }
            list.Add(then);
            if (mem.Fetching || Model.ListFolder is not { } k)
            {
                return;
            }
            Model.Members[tid] = mem with { Fetching = true };
            var gen = Model.ListGen;
            var threadParams = new ThreadGetParams { AccountId = k.Account, ThreadId = tid, FolderId = k.Folder };
            mailbox.Scope.Perform(mailbox.Client, API.ThreadGet, threadParams, outcome =>
            {
                if (gen != Model.ListGen || !Model.Members.TryGetValue(tid, out var current))
                {
                    return;
                }
                Model.Members[tid] = current with { Fetching = false };
                waiters.Remove(tid, out var waiting);
                var res = outcome.Value ?? throw new InvalidOperationException("thread.get failed", outcome.Error);
                Model.SetMembers(tid, res.Thread, res.Messages);
                foreach (var fn in waiting ?? [])
                {
                    fn();
                }
            });
        }
    }
}
