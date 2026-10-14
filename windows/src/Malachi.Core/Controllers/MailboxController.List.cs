// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/MailboxController+List.swift
// (ListController; ListState, LoadMoreState and SelectionHint have files of
// their own); GTK: ui/internal/window/messages.go (loadMessages,
// setListFilter, loadMore, rebuildMessageRows, showListState, showLoadMore,
// folderUnsynced, removeMessageRow, removeRows), threads.go (loadThreadPage,
// syncRows, syncRowsAfterRemoval, reconcileRows, toggleThread,
// addThreadShortcuts, ensureMembers, fetchExpandedMembers, selectedRow,
// selectedIDs, rowSubject), folders.go (onNewMessage, the list half),
// window.go (the row-selected and row-activated handlers,
// onMessageRowSelected, showConnectionState's list share), actions.go
// (scheduleMarkRead, refreshMessageActions, refreshRows, the apply step of
// setSeenIDs / setFlaggedIDs) and outbox.go (refreshOutboxViews, the list
// share).
//
// The file keeps Swift's name: like MailboxController+List.swift it holds
// ListController, the second half of the one GTK window object, and
// MailboxController.Search.cs and MailboxController.Paging.cs hold the rest
// of the class. What changes on Windows (docs/windows-port.md §7): the calls
// go through the mailbox's ControllerScope (Swift's mailbox.perform), the
// mark-as-read delay runs on the TimeProvider, the Swift callbacks are
// events of the same words, and the state a view binds to is observable.
// The rows are a snapshot keyed by ListRow.Key for KeyedListSync, and
// SelectedKey is the source of truth for the selection, current whenever
// the rows are announced (RowsChanged, RowsRefreshed): the view applies
// them by key with row view models updated in place (KeyedListSync's view
// overload, so a changed flag is never a Replace) and selects SelectedKey
// with its own handler suppressed, after every apply, since WinUI may drop
// the selection of a moved item (docs/windows-port.md §7.5; phase E
// verifies).

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using CommunityToolkit.Mvvm.ComponentModel;
using Malachi.Core.Api;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Settings;
using Malachi.Core.Text;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>
/// The message-list half of the GTK main window: paging through
/// message.list or thread.list, the rows and their keys, the status pages,
/// folding conversations, the selection with its mark-as-read timer, and
/// the in-place edits between two loads (a notified arrival, a flag change,
/// a removal and its undo).
/// </summary>
/// <remarks>
/// <para>
/// It is the second half of one GTK window object: it holds the
/// <see cref="MailboxController"/> (the folder half), reads and writes the
/// shared model there and installs itself into the folder half's list hooks
/// (<see cref="MailboxController.ReloadMessages"/>,
/// <see cref="MailboxController.OnNewMessageForList"/>,
/// <see cref="MailboxController.RefreshOutboxViews"/>,
/// <see cref="MailboxController.CollapseLoading"/>). No WinUI: the view
/// subscribes to the events and sends the user's clicks back through the
/// methods; the actions drive <see cref="ApplyFlags"/>,
/// <see cref="RemoveRows"/>, <see cref="SelectedIds"/> and
/// <see cref="MarkRead"/>.
/// </para>
/// <para>
/// UI-thread-affine, like the mailbox: every RPC runs through the
/// mailbox's scope, so the continuation is on the UI thread too and the
/// list generation checks race with nothing.
/// </para>
/// </remarks>
public sealed partial class ListController : ObservableObject, IDisposable, IActionsList, IListSelection
{
    private readonly ILogger logger;
    private readonly TimeProvider time;

    // Closures to run once a conversation's members are known (the waiters
    // of thread_model.go threadMembers).
    private readonly Dictionary<ThreadId, List<Action>> waiters = [];

    // What runs instead when that thread.get fails.
    private readonly Dictionary<ThreadId, List<Action>> failureWaiters = [];

    // Conversations whose thread.get in flight is followed by another
    // (RefetchMembers).
    private readonly HashSet<ThreadId> refetchAfter = [];
    private CancellationTokenSource? markReadTimer;
    private MessageId? markReadId;
    private SettingsChangeToken? settingsToken;
    private bool closed;

    /// <summary>
    /// Installs the list half into <paramref name="mailbox"/>'s hooks and
    /// publishes the initial state (the folder the mailbox has selected, if
    /// any, is listed at once). Created on the UI thread.
    /// </summary>
    /// <param name="mailbox">The folder half, whose model and scope the list shares.</param>
    /// <param name="settings">The grouping, the mark-read delay and the search scope.</param>
    /// <param name="time">The clock of the mark-as-read delay and the search pause.</param>
    /// <param name="logger">Method names, codes and ids only; never what was searched for.</param>
    public ListController(MailboxController mailbox, SettingsStore settings, TimeProvider? time = null, ILogger<ListController>? logger = null)
    {
        ArgumentNullException.ThrowIfNull(mailbox);
        ArgumentNullException.ThrowIfNull(settings);
        Mailbox = mailbox;
        Settings = settings;
        this.time = time ?? TimeProvider.System;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        ListState = new ListState.Messages();
        LoadMoreState = new LoadMoreState();
        Rows = [];
        Scope.VerifyAccess();
        // The filter is deliberately not persisted (window.go).
        Model.ListFilter = MessageFilter.All;
        mailbox.ReloadMessages = LoadMessages;
        mailbox.OnNewMessageForList = ApplyNewMessage;
        mailbox.OnNewMessageElsewhere = ApplyNewMessageElsewhere;
        mailbox.RefreshShownSent = RefreshShownSent;
        mailbox.RefreshOutboxViews = RefreshOutboxViews;
        mailbox.CollapseLoading = CollapseLoadingRows;
        mailbox.RefreshShown = RefreshShown;
        // Grouping is a different listing (thread.list): LoadMessages
        // notices the mode change and starts the folder over (window.go),
        // unless the folder's mode stays (GroupingChanged).
        settingsToken = settings.OnChange(SettingsKey.GroupByConversation, GroupingChanged);
        if (Model.Selected is not null)
        {
            LoadMessages();
        }
        else
        {
            ShowListState();
        }
    }

    // Callbacks (the view)

    /// <summary>
    /// The rows changed (Swift <c>onRows</c>): reconcile the list by key,
    /// then mirror <see cref="SelectedKey"/>. <see cref="Rows"/> and
    /// <see cref="SelectedKey"/> are current when it is raised. A row that
    /// stays may be a new record (a conversation's counts after a flag
    /// change): its view model is updated in place, never replaced, and the
    /// view selects the key with its own selection handler suppressed
    /// (docs/windows-port.md §7.5).
    /// </summary>
    public event EventHandler<RowsUpdate>? RowsChanged;

    /// <summary>Switch between the rows and a status page (Swift <c>onListState</c>).</summary>
    public event EventHandler<ListState>? ListStateChanged;

    /// <summary>The footer changed (Swift <c>onLoadMore</c>).</summary>
    public event EventHandler<LoadMoreState>? LoadMoreChanged;

    /// <summary>
    /// The controller dropped the selection (Swift
    /// <c>onSelectionCleared</c>; also announced as
    /// <see cref="SelectedMessageChanged"/> with null).
    /// </summary>
    public event EventHandler? SelectionCleared;

    /// <summary>
    /// Flat mode: the rows with these keys changed in place (a flag, as
    /// mark-as-read sets about a second after every selection; Swift
    /// <c>onRowsRefreshed</c>); <see cref="RowFor"/> and <see cref="Rows"/>
    /// have the new content, new records under the same keys, and
    /// <see cref="SelectedKey"/> stays. The view updates the rows' view
    /// models in place (or applies <see cref="Rows"/> through the view
    /// overload of <see cref="KeyedListSync"/>), never replaces them, which a
    /// WinUI selector takes for a removal and an insertion that drops the
    /// selection (docs/windows-port.md §7.5). Grouped mode goes through
    /// <see cref="RowsChanged"/>.
    /// </summary>
    public event EventHandler<IReadOnlyList<ListKey>>? RowsRefreshed;

    /// <summary>
    /// The scope bar of a search (search.go <c>refreshSearchScope</c>; Swift
    /// <c>onSearchBar</c>); null hides it, as the search ended.
    /// </summary>
    public event EventHandler<SearchBarState?>? SearchBarChanged;

    /// <summary>
    /// Select this row and give the list the keyboard (Enter in the search
    /// box: search.go <c>selectFirstResult</c>; Swift <c>onFocusRow</c>).
    /// </summary>
    public event EventHandler<ListKey>? FocusRow;

    // Callbacks (the rest of the window)

    /// <summary>
    /// The message the pane should show: the selected row's (a conversation
    /// row's newest folder member), null when nothing is selected (window.go
    /// <c>onMessageRowSelected</c>; Swift <c>onSelectedMessageChanged</c>).
    /// </summary>
    public event EventHandler<MessageSummary?>? SelectedMessageChanged;

    /// <summary>
    /// The selected row itself, announced just before
    /// <see cref="SelectedMessageChanged"/>, null when nothing is selected: a
    /// conversation row (<see cref="ListRow.ShowsConversation"/>) puts the
    /// whole conversation in the pane (<see cref="ConversationController"/>),
    /// any other row its message (Swift <c>onSelectedRowChanged</c>).
    /// </summary>
    public event EventHandler<ListRow?>? SelectedRowChanged;

    /// <summary>
    /// The folder members the model holds of a conversation changed while
    /// its row may have kept its key (an arrival, a flag change, a removal
    /// and its undo, thread.get answering, a reload): what the conversation
    /// view shows follows (<see cref="ConversationController"/>; Swift
    /// <c>onThreadMembersChanged</c>). Grouped mode only.
    /// </summary>
    public event EventHandler<ThreadId>? ThreadMembersChanged;

    /// <summary>
    /// The messages of the shown conversation were rebuilt in place by the
    /// daemon (notify.messagesChanged, <see cref="RefreshShown"/>): the
    /// conversation view lets go of the bodies it holds and asks for them
    /// again; its folder members were forgotten here and come back through
    /// <see cref="ThreadMembersChanged"/> once the reload asked thread.get
    /// for them (Swift <c>onConversationChanged</c>). Grouped mode only.
    /// </summary>
    public event EventHandler<ThreadId>? ConversationChanged;

    /// <summary>
    /// A message row was activated (double-click, Enter): open it in a
    /// window (window.go, the row-activated handler; Swift
    /// <c>onActivateMessage</c>). A conversation row folds or unfolds
    /// instead and never reaches this.
    /// </summary>
    public event EventHandler<MessageSummary>? ActivateMessage;

    /// <summary>
    /// A message row of a Drafts folder was activated: open it in the
    /// compose window (window.go, the row-activated handler; drafts.go
    /// <c>openDraft</c>; Swift <c>onActivateDraft</c>).
    /// </summary>
    public event EventHandler<MessageSummary>? ActivateDraft;

    /// <summary>The per-message actions changed with the selection or its flags (Swift <c>onActionFlagsChanged</c>).</summary>
    public event EventHandler<ActionFlags>? ActionFlagsChanged;

    /// <summary>
    /// The mark-as-read timer fired for the selected message (actions.go
    /// <c>scheduleMarkRead</c> → <c>markRead</c>; Swift <c>onMarkRead</c>);
    /// the actions set the flag.
    /// </summary>
    public event EventHandler<MessageId>? MarkRead;

    /// <summary>
    /// The list's share of outbox.go <c>refreshOutboxViews</c> ran (the
    /// outbox listing reloaded when it was shown; Swift
    /// <c>onOutboxRefreshed</c>); the reader's share follows.
    /// </summary>
    public event EventHandler<AccountId>? OutboxRefreshed;

    /// <summary>The folder half.</summary>
    public MailboxController Mailbox { get; }

    /// <summary>The grouping, the mark-read delay and the search scope.</summary>
    public SettingsStore Settings { get; }

    /// <summary>The unit of <see cref="SettingsStore.MarkReadDelay"/> (seconds, as in GTK).</summary>
    public TimeSpan MarkReadTick { get; set; } = TimeSpan.FromSeconds(1);

    /// <summary>What the pane shows: the rows or a status page.</summary>
    [ObservableProperty]
    public partial ListState ListState { get; private set; }

    /// <summary>The footer under the rows.</summary>
    [ObservableProperty]
    public partial LoadMoreState LoadMoreState { get; private set; }

    /// <summary>
    /// The rows on screen, in order: the model's rows in grouped mode, one
    /// row per message in flat mode (messages.go <c>rebuildMessageRows</c>).
    /// A snapshot, replaced whole; keyed by <see cref="ListRow.Key"/>.
    /// </summary>
    [ObservableProperty]
    public partial IReadOnlyList<ListRow> Rows { get; private set; }

    /// <summary>The key of the selected row, null for none (threads.go <c>selectedKey</c>).</summary>
    [ObservableProperty]
    public partial ListKey? SelectedKey { get; private set; }

    /// <summary>What the selection allows (actions.go <c>setMessageActionsSensitive</c>).</summary>
    [ObservableProperty]
    public partial ActionFlags ActionFlags { get; private set; }

    /// <summary>The filter the list shows (All until changed; not persisted).</summary>
    public MessageFilter ListFilter => Model.ListFilter;

    /// <summary>
    /// The special-use role of the listed folder,
    /// <see cref="Api.FolderRole.None"/> when nothing is listed or the folder
    /// is plain.
    /// </summary>
    public FolderRole FolderRole => FolderRoleOf(Model.ListFolder);

    /// <summary>The listed folder is the account's outbox: never grouped, never marked read.</summary>
    public bool InOutbox => FolderRole == Api.FolderRole.Outbox;

    /// <summary>The row behind the selection, if any (threads.go <c>selectedRow</c>).</summary>
    public ListRow? SelectedRow => SelectedKey is { } key ? RowFor(key) : null;

    private MailModel Model => Mailbox.Model;

    private ControllerScope Scope => Mailbox.Scope;

    /// <summary>Stops the timers and detaches from the settings (the window closed).</summary>
    public void Close()
    {
        closed = true;
        CancelMarkRead();
        CancelSearchWork();
        settingsToken?.Cancel();
        settingsToken = null;
        waiters.Clear();
        failureWaiters.Clear();
        refetchAfter.Clear();
    }

    /// <summary>Closes the list (<see cref="Close"/>).</summary>
    public void Dispose() => Close();

    // Loading

    /// <summary>
    /// Runs message.list (or thread.list) for the selected folder, first
    /// page (messages.go <c>loadMessages</c>). A switch to another folder,
    /// or of the grouping mode, empties the list at once; a reload of the
    /// same folder keeps the rows until the reply, so the list never
    /// flickers through the loading state and the selection survives (by
    /// key).
    /// </summary>
    public void LoadMessages()
    {
        Scope.VerifyAccess();
        if (Model.Search.Active)
        {
            // The list shows search results; a reload of the folder (a
            // sync, a selection) is a new search only if it changes the
            // scope (search.go).
            PublishSearchBar();
            RunSearch(force: false);
            return;
        }
        var k = Model.Selected;
        var gen = Model.BumpList();
        Model.LoadingMore = false;
        var grouped = GroupedListing(k);
        if (k != Model.ListFolder || grouped != Model.Grouped)
        {
            Model.ListFolder = k;
            Model.Grouped = grouped;
            Model.ClearMessages();
            Reconcile(SelectionHint.Clear);
            OnPropertyChanged(nameof(FolderRole));
            OnPropertyChanged(nameof(InOutbox));
            // With nothing selected the actions follow the listed folder's
            // account (a Jira folder has no Reply).
            RefreshActionFlags();
        }
        if (k is not { } key)
        {
            Model.Loading = false;
            ShowListState();
            return;
        }
        if (FolderUnsynced(key))
        {
            // Never downloaded (Gmail's All Mail): nothing to ask for.
            Model.Loading = false;
            ShowListState();
            return;
        }
        Model.Loading = true;
        Model.ListErr = null;
        ShowListState();
        if (grouped)
        {
            LoadThreadPage(key, gen);
            return;
        }
        var parameters = new MessageListParams
        {
            AccountId = key.Account,
            FolderId = key.Folder,
            Page = new Page { Limit = API.Limits.DefaultPageLimit },
            Sort = SortOrder.DateDesc,
            Filter = Model.ListFilter,
        };
        Mailbox.Perform(API.MessageList, parameters, outcome =>
        {
            if (gen != Model.ListGen)
            {
                return;
            }
            Model.Loading = false;
            if (!outcome.TryGetValue(out var res, out var err))
            {
                LogListFailed(logger, "message.list", key.Folder.ToString(), err!.Message);
                if (Model.RowCount > 0)
                {
                    // A reload failed: keep what is shown, say so once.
                    Mailbox.Toast(RpcErrorText.Text(L10n.T("Loading messages"), err));
                    return;
                }
                Model.ListErr = err;
                ShowListState();
                return;
            }
            Model.SetMessages(res.Messages, res.Page);
            Reconcile(SelectionHint.Keep);
        });
    }

    /// <summary>
    /// Whether folder <paramref name="k"/> is listed as conversations
    /// (thread.list; messages.go <c>groupedListing</c>): with the "group by
    /// conversation" setting, and always for a folder of a Jira account
    /// (<see cref="MailModel.AlwaysGrouped"/>); never the outbox.
    /// </summary>
    public bool GroupedListing(FolderKey? k) =>
        (Settings.GroupByConversation || Model.AlwaysGrouped(k)) && FolderRoleOf(k) != Api.FolderRole.Outbox;

    // The "group by conversation" setting changed: the folder is listed
    // again in the other mode. A folder whose mode stays (a Jira account's,
    // the outbox) keeps its rows and is not asked again.
    private void GroupingChanged()
    {
        if (!Model.Search.Active && Model.ListFolder == Model.Selected && GroupedListing(Model.Selected) == Model.Grouped)
        {
            return;
        }
        LoadMessages();
    }

    /// <summary>
    /// Runs thread.list for folder <paramref name="k"/>, first page
    /// (threads.go <c>loadThreadPage</c>); <paramref name="gen"/> is the list
    /// generation the reply belongs to.
    /// </summary>
    private void LoadThreadPage(FolderKey k, ulong gen)
    {
        var parameters = new ThreadListParams
        {
            AccountId = k.Account,
            FolderId = k.Folder,
            Page = new Page { Limit = API.Limits.DefaultPageLimit },
            Sort = SortOrder.DateDesc,
            Filter = Model.ListFilter,
        };
        Mailbox.Perform(API.ThreadList, parameters, outcome =>
        {
            if (gen != Model.ListGen)
            {
                return;
            }
            Model.Loading = false;
            if (!outcome.TryGetValue(out var res, out var err))
            {
                LogListFailed(logger, "thread.list", k.Folder.ToString(), err!.Message);
                if (Model.RowCount > 0)
                {
                    Mailbox.Toast(RpcErrorText.Text(L10n.T("Loading messages"), err));
                    return;
                }
                Model.ListErr = err;
                ShowListState();
                return;
            }
            Model.SetThreads(res.Threads, res.Page);
            SyncRows();
            FetchExpandedMembers();
            AnnounceSelectedMembers();
        });
    }

    /// <summary>The Try Again button of the error page.</summary>
    public void Retry() => LoadMessages();

    /// <summary>
    /// Switches the list between all, unread and flagged messages
    /// (messages.go <c>setListFilter</c>). The backend does the filtering,
    /// so the rows and the cursor of the previous filter are dropped and the
    /// folder is paged again from the start. A no-op on an unchanged filter,
    /// as the filter bar also fires for its own write-back; an empty filter
    /// is All.
    /// </summary>
    public void SetListFilter(MessageFilter filter)
    {
        Scope.VerifyAccess();
        var f = string.IsNullOrEmpty(filter.Value) ? (MessageFilter)MessageFilter.All : filter;
        if (f == Model.ListFilter)
        {
            return;
        }
        Model.ListFilter = f;
        OnPropertyChanged(nameof(ListFilter));
        Model.ClearMessages();
        Reconcile(SelectionHint.Clear);
        LoadMessages();
    }

    /// <summary>
    /// Fetches the next page (messages.go <c>loadMore</c>; the list asks by
    /// itself, see MailboxController.Paging.cs). A no-op while a page is in
    /// flight or when the last page is shown.
    /// </summary>
    public void LoadMore()
    {
        Scope.VerifyAccess();
        if (Model.Loading || Model.LoadingMore || string.IsNullOrEmpty(Model.NextCursor) || Model.ListErr is not null)
        {
            return;
        }
        var cursor = Model.NextCursor;
        if (Model.Search.Active)
        {
            var searchGen = Model.ListGen;
            Model.LoadingMore = true;
            ShowLoadMore();
            LoadMoreSearch(searchGen, cursor);
            return;
        }
        if (Model.ListFolder is not { } k)
        {
            return;
        }
        var gen = Model.ListGen;
        Model.LoadingMore = true;
        ShowLoadMore();
        if (Model.Grouped)
        {
            var threadParams = new ThreadListParams
            {
                AccountId = k.Account,
                FolderId = k.Folder,
                Page = new Page { Cursor = cursor, Limit = API.Limits.DefaultPageLimit },
                Sort = SortOrder.DateDesc,
                Filter = Model.ListFilter,
            };
            Mailbox.Perform(API.ThreadList, threadParams, outcome =>
            {
                if (gen != Model.ListGen)
                {
                    return;
                }
                Model.LoadingMore = false;
                if (!outcome.TryGetValue(out var res, out var err))
                {
                    LogMoreFailed(logger, "thread.list", err!.Message);
                    Mailbox.Toast(RpcErrorText.Text(L10n.T("Loading more messages"), err));
                    ShowLoadMore();
                    return;
                }
                Model.AppendThreads(res.Threads, res.Page);
                SyncRows();
            });
            return;
        }
        var parameters = new MessageListParams
        {
            AccountId = k.Account,
            FolderId = k.Folder,
            Page = new Page { Cursor = cursor, Limit = API.Limits.DefaultPageLimit },
            Sort = SortOrder.DateDesc,
            Filter = Model.ListFilter,
        };
        Mailbox.Perform(API.MessageList, parameters, outcome =>
        {
            if (gen != Model.ListGen)
            {
                return;
            }
            Model.LoadingMore = false;
            if (!outcome.TryGetValue(out var res, out var err))
            {
                LogMoreFailed(logger, "message.list", err!.Message);
                Mailbox.Toast(RpcErrorText.Text(L10n.T("Loading more messages"), err));
                ShowLoadMore(); // the cursor is still there; the list offers a retry
                return;
            }
            Model.AppendMessages(res.Messages, res.Page);
            Reconcile(SelectionHint.Keep);
        });
    }

    // States

    /// <summary>
    /// Switches between the rows and the status page (messages.go
    /// <c>showListState</c>): nothing selected, not synchronised, an error
    /// with Try Again, loading, or empty for the active filter.
    /// </summary>
    /// <remarks>
    /// The footer follows the model under a status page too (Windows only):
    /// GTK and Swift leave it as the last rows had it, hidden with the rows'
    /// page, but here the list pages itself from
    /// <see cref="LoadMoreState"/>, and the emptied list of another listing,
    /// which the view reports, must not ask for the page the listing before
    /// offered.
    /// </remarks>
    public void ShowListState()
    {
        Scope.VerifyAccess();
        if (Model.RowCount > 0)
        {
            SetListState(new ListState.Messages());
            ShowLoadMore();
            return;
        }
        if (Model.Search.Active)
        {
            SetListState(SearchListState());
            ShowLoadMore();
            return;
        }
        ListState state;
        if (Model.Selected is not { } sel)
        {
            state = new ListState.Status(
                "folder-symbolic", L10n.T("Select a folder"), L10n.T("Choose a folder in the sidebar to see its messages."), false);
        }
        else if (FolderUnsynced(sel))
        {
            state = new ListState.Status(
                "folder-download-symbolic",
                L10n.T("Not Synchronised"),
                L10n.T("Messages moved here are archived on the server; the folder itself is not downloaded."),
                false);
        }
        else if (Model.ListErr is { } err)
        {
            state = new ListState.Status(
                "dialog-warning-symbolic", L10n.T("Messages Unavailable"), RpcErrorText.Text(L10n.T("Loading messages"), err), true);
        }
        else if (Model.Loading)
        {
            state = new ListState.Status("", L10n.T("Loading…"), "", false);
        }
        else if (Model.ListFilter == MessageFilter.Unread)
        {
            state = new ListState.Status(
                "mail-read-symbolic", L10n.T("No Unread Messages"), L10n.T("Everything in this folder has been read."), false);
        }
        else if (Model.ListFilter == MessageFilter.Flagged)
        {
            state = new ListState.Status(
                "starred-symbolic", L10n.T("No Flagged Messages"), L10n.T("No message in this folder carries a flag."), false);
        }
        else
        {
            state = new ListState.Status("mail-unread-symbolic", L10n.T("No Messages"), L10n.T("This folder is empty."), false);
        }
        SetListState(state);
        ShowLoadMore();
    }

    /// <summary>
    /// Offers a further page while one exists and the spinner while it is
    /// being fetched (messages.go <c>showLoadMore</c>).
    /// </summary>
    public void ShowLoadMore()
    {
        Scope.VerifyAccess();
        var cursor = Model.NextCursor ?? "";
        // Under the last page of results: how far back search reaches.
        var st = Model.Search;
        var note = st.Active && st.Shown && cursor.Length == 0 && !Model.LoadingMore && Model.RowCount > 0
            ? SearchModel.SearchRetentionText(st.OfflineDays, st.OfflineKnown)
            : "";
        var state = new LoadMoreState
        {
            Spinner = Model.LoadingMore,
            Button = !Model.LoadingMore && cursor.Length > 0 && Model.ListErr is null,
            Note = note,
        };
        if (state == LoadMoreState)
        {
            return;
        }
        LoadMoreState = state;
        LoadMoreChanged?.Invoke(this, state);
        FollowLoadMore(state);
    }

    internal void SetListState(ListState s)
    {
        if (s == ListState)
        {
            return;
        }
        ListState = s;
        ListStateChanged?.Invoke(this, s);
    }

    private FolderRole FolderRoleOf(FolderKey? k) => k is { } key ? Model.FolderRole(key) : Api.FolderRole.None;

    /// <summary>A selected folder the daemon never downloads (messages.go <c>folderUnsynced</c>).</summary>
    private bool FolderUnsynced(FolderKey k) => Model.Folder(k) is { } f && !f.Synced;

    // Rows

    /// <summary>The row with the given key, if listed (Swift <c>row(for:)</c>).</summary>
    public ListRow? RowFor(ListKey key) => Model.RowAt(Model.RowIndexOf(key));

    /// <summary>The rows the model has right now, in either mode.</summary>
    private IReadOnlyList<ListRow> CurrentRows() =>
        Model.Grouped
            ? Model.Rows
            : Model.Messages.Select(s => new ListRow { Key = new ListKey(Message: s.Id), Message = s }).ToArray();

    /// <summary>
    /// Brings the rows in line with the model (threads.go <c>syncRows</c>):
    /// the selection follows its key; when a member row folded away its
    /// conversation row takes over (and the pane shows the newest member);
    /// when the selected row is gone the pane is cleared.
    /// </summary>
    public void SyncRows()
    {
        Scope.VerifyAccess();
        Reconcile(SelectionHint.Keep);
    }

    /// <summary>
    /// <see cref="SyncRows"/> for a removal (threads.go
    /// <c>syncRowsAfterRemoval</c>): a selected row that is gone hands the
    /// selection to the row now at its place, pane included, as the flat
    /// list does.
    /// </summary>
    private void SyncRowsAfterRemoval() => Reconcile(SelectionHint.Neighbour);

    /// <summary>
    /// Publishes the model's rows and reconciles the selection with them
    /// (threads.go <c>reconcileRows</c>, messages.go
    /// <c>rebuildMessageRows</c>). The index of the selected row is read off
    /// the rows as they were before the model moved on, as the GTK code
    /// reads it off the widgets.
    /// </summary>
    internal void Reconcile(SelectionHint hint)
    {
        var prevKey = SelectedKey;
        var prevIdx = -1;
        if (prevKey is { } pk)
        {
            for (var i = 0; i < Rows.Count; i++)
            {
                if (Rows[i].Key == pk)
                {
                    prevIdx = i;
                    break;
                }
            }
        }
        var rows = CurrentRows();
        var key = prevKey;
        var changed = false;
        if (prevKey is { } k)
        {
            if (hint == SelectionHint.Clear)
            {
                key = null;
                changed = true;
            }
            else if (rows.Any(r => r.Key == k))
            {
                // Still listed: the pane already shows it.
            }
            else if (k.Thread is { } tid && rows.FirstOrDefault(r => r.Key == new ListKey(tid)) is { } conversation)
            {
                key = conversation.Key;
                changed = true;
            }
            else if (hint == SelectionHint.Neighbour && prevIdx >= 0 && Math.Min(prevIdx, rows.Count - 1) >= 0)
            {
                key = rows[Math.Min(prevIdx, rows.Count - 1)].Key;
                changed = true;
            }
            else
            {
                key = null;
                changed = true;
            }
        }
        if (hint == SelectionHint.Clear)
        {
            ResetPaging();
        }
        Rows = rows;
        SelectedKey = key;
        RowsChanged?.Invoke(this, new RowsUpdate(rows, hint));
        if (changed)
        {
            AnnounceSelection();
        }
        ShowListState();
    }

    /// <summary>
    /// Pushes the model to the rows of the given messages (actions.go
    /// <c>refreshRows</c>); in grouped mode the conversation rows carry
    /// aggregates, so the whole list is reconciled.
    /// </summary>
    public void RefreshRows(IReadOnlyList<MessageId> ids)
    {
        ArgumentNullException.ThrowIfNull(ids);
        Scope.VerifyAccess();
        if (Model.Grouped)
        {
            SyncRows();
            return;
        }
        Rows = CurrentRows();
        ListKey[] keys = [.. ids.Where(Model.Index.ContainsKey).Select(id => new ListKey(Message: id))];
        if (keys.Length > 0)
        {
            RowsRefreshed?.Invoke(this, keys);
        }
    }

    // Conversations

    /// <summary>
    /// Folds or unfolds a conversation row; unfolding asks for the members
    /// when they are not known yet (threads.go <c>toggleThread</c>).
    /// </summary>
    public void ToggleThread(ThreadId tid)
    {
        Scope.VerifyAccess();
        var on = !Model.Expanded.Contains(tid);
        Model.SetExpanded(tid, on);
        if (on)
        {
            EnsureMembers(tid, null);
        }
        SyncRows();
    }

    /// <summary>
    /// <see cref="ToggleThread"/> for the Left/Right keys (threads.go
    /// <c>addThreadShortcuts</c>): false when the conversation is in that
    /// state already, so the key still reaches the list for its normal
    /// navigation.
    /// </summary>
    public bool SetThreadExpanded(ThreadId tid, bool on)
    {
        Scope.VerifyAccess();
        if (Model.Expanded.Contains(tid) == on)
        {
            return false;
        }
        ToggleThread(tid);
        return true;
    }

    /// <summary>
    /// Double-click or Enter on a row (window.go, the row-activated
    /// handler): a conversation row folds or unfolds, a message opens in a
    /// window — a draft in the compose window.
    /// </summary>
    public void Activate(ListKey key)
    {
        Scope.VerifyAccess();
        if (RowFor(key) is not { } r)
        {
            return;
        }
        if (r.Thread && r.Key.Thread is { } tid)
        {
            ToggleThread(tid);
        }
        else if (Model.InDrafts(r.Message))
        {
            ActivateDraft?.Invoke(this, r.Message);
        }
        else
        {
            ActivateMessage?.Invoke(this, r.Message);
        }
    }

    /// <summary>
    /// Makes the folder members of a conversation known, through thread.get
    /// when needed, and runs <paramref name="then"/> afterwards (at once when
    /// they are). A failed fetch folds the row back and says why (threads.go
    /// <c>ensureMembers</c>), and runs <paramref name="failed"/> instead of
    /// <paramref name="then"/>. Neither runs when the list moved on meanwhile
    /// (another folder, a reload, the connection lost). thread.get asks for
    /// the user's replies in Sent too (withSent), which the conversation view
    /// shows beside the members (<see cref="ThreadMembers.Sent"/>).
    /// </summary>
    public void EnsureMembers(ThreadId tid, Action? then, Action? failed = null)
    {
        Scope.VerifyAccess();
        if (!Model.Members.TryGetValue(tid, out var mem))
        {
            return;
        }
        if (mem.Complete)
        {
            then?.Invoke();
            return;
        }
        if (then is not null)
        {
            if (!waiters.TryGetValue(tid, out var list))
            {
                waiters[tid] = list = [];
            }
            list.Add(then);
        }
        if (failed is not null)
        {
            if (!failureWaiters.TryGetValue(tid, out var list))
            {
                failureWaiters[tid] = list = [];
            }
            list.Add(failed);
        }
        if (mem.Fetching)
        {
            return;
        }
        FetchMembers(tid, mem);
    }

    /// <summary>
    /// Asks thread.get for conversation <paramref name="tid"/> again while
    /// the model keeps what it holds of it, so nothing blinks: the user's
    /// replies in Sent changed (one arrived, or a message that may carry a
    /// reply's Message-ID arrived in the folder; the client cannot compare
    /// Message-IDs, the daemon does), or the shown conversation's sent folder
    /// changed. The answer replaces the members, the replies and the summary
    /// (SentCount, so a single message the user answered becomes a
    /// conversation row) and reaches the conversation view through
    /// <see cref="ThreadMembersChanged"/>. A fetch in flight is followed by
    /// one more, since it may have been answered before the change. A
    /// failure is only logged, unless someone waits for the members.
    /// </summary>
    public void RefetchMembers(ThreadId tid)
    {
        Scope.VerifyAccess();
        if (!Model.Members.TryGetValue(tid, out var mem))
        {
            return;
        }
        if (mem.Fetching)
        {
            refetchAfter.Add(tid);
            return;
        }
        FetchMembers(tid, mem, quiet: true);
    }

    // The thread.get of EnsureMembers and RefetchMembers.
    private void FetchMembers(ThreadId tid, ThreadMembers mem, bool quiet = false)
    {
        if (Model.ListFolder is not { } k)
        {
            return;
        }
        Model.Members[tid] = mem with { Fetching = true };
        var gen = Model.ListGen;
        var parameters = new ThreadGetParams { AccountId = k.Account, ThreadId = tid, FolderId = k.Folder, WithSent = true };
        Mailbox.Perform(API.ThreadGet, parameters, outcome =>
        {
            if (gen != Model.ListGen || !Model.Members.TryGetValue(tid, out var now))
            {
                return;
            }
            Model.Members[tid] = now with { Fetching = false };
            var waiting = waiters.Remove(tid, out var w) ? w : [];
            var failing = failureWaiters.Remove(tid, out var f) ? f : [];
            var again = refetchAfter.Remove(tid);
            if (!outcome.TryGetValue(out var res, out var err))
            {
                LogThreadGetFailed(logger, err!.Message);
                if (quiet && waiting.Count == 0 && failing.Count == 0)
                {
                    return;
                }
                Mailbox.Toast(RpcErrorText.Text(L10n.T("Loading the conversation"), err));
                Model.SetExpanded(tid, false);
                SyncRows();
                foreach (var fn in failing)
                {
                    fn();
                }
                return;
            }
            Model.SetMembers(tid, res.Thread, res.Messages, res.Sent);
            SyncRows();
            if (SelectedRow is { } row && row.Key.Thread == tid)
            {
                RefreshActionFlags();
            }
            ThreadMembersChanged?.Invoke(this, tid);
            foreach (var fn in waiting)
            {
                fn();
            }
            if (again)
            {
                RefetchMembers(tid);
            }
        });
    }

    /// <summary>
    /// Forgets that the folder members of conversation <paramref name="tid"/>
    /// are known, so the next <see cref="EnsureMembers"/> asks thread.get
    /// again (the conversation view lost every member it showed while older
    /// ones are left out, or the daemon rebuilt the messages).
    /// </summary>
    public void RefreshMembers(ThreadId tid)
    {
        Scope.VerifyAccess();
        if (!Model.Members.TryGetValue(tid, out var mem) || !mem.Complete)
        {
            return;
        }
        Model.Members[tid] = mem with { Complete = false };
        Model.RebuildRows();
        SyncRows();
    }

    /// <summary>
    /// Asks for the members of every unfolded conversation that lost them (a
    /// reload that changed the conversation; threads.go
    /// <c>fetchExpandedMembers</c>).
    /// </summary>
    private void FetchExpandedMembers()
    {
        foreach (var tid in Model.Expanded.ToArray())
        {
            if (Model.Members.TryGetValue(tid, out var mem) && !mem.Complete)
            {
                EnsureMembers(tid, null);
            }
        }
    }

    /// <summary>
    /// Folds every conversation waiting for its members and forgets their
    /// waiters (the backend went away; window.go <c>showConnectionState</c>).
    /// </summary>
    public void CollapseLoadingRows()
    {
        Scope.VerifyAccess();
        Model.CollapseLoading();
        waiters.Clear();
        failureWaiters.Clear();
        refetchAfter.Clear();
        SyncRows();
        ShowLoadMore();
    }

    /// <summary>
    /// The list's share of window.go <c>showConnectionState</c>, after the
    /// folder half's: the model's <c>BumpAll</c> there dropped the in-flight
    /// replies and cleared the loading flags, so the footer is redrawn from
    /// it (the spinner stops) and a conversation waiting for its members
    /// folds back. The folder half's <c>CollapseLoading</c> hook does the
    /// latter as well; both are idempotent. A protocol mismatch leaves no
    /// connection either.
    /// </summary>
    public void HandleConnection(ConnectionState state)
    {
        ArgumentNullException.ThrowIfNull(state);
        Scope.VerifyAccess();
        switch (state)
        {
            case ConnectionState.Unavailable or ConnectionState.ProtocolMismatch or ConnectionState.Stopping:
                if (Model.Grouped)
                {
                    CollapseLoadingRows();
                }
                ShowLoadMore();
                break;
        }
    }

    // Selection

    /// <summary>
    /// The user selected a row, or cleared the selection (window.go, the
    /// row-selected handler → <c>onMessageRowSelected</c>; Swift
    /// <c>select(key:)</c>): the pane shows the row's message (a
    /// conversation row's newest folder member), the actions follow, the
    /// mark-as-read timer is armed.
    /// </summary>
    public void Select(ListKey? key)
    {
        Scope.VerifyAccess();
        SelectedKey = key;
        AnnounceSelection();
    }

    /// <summary>
    /// The board's Show in Mail (board_show_in_mail.go <c>finish</c>, Swift
    /// <c>ListController.reveal</c>'s last step): the row of
    /// <paramref name="key"/> is selected and given the keyboard through
    /// <see cref="FocusRow"/>, as the search's first result is. False when
    /// the list shows no such row.
    /// </summary>
    public bool RevealRow(ListKey key)
    {
        Scope.VerifyAccess();
        if (RowFor(key) is null)
        {
            return false;
        }
        FocusRow?.Invoke(this, key);
        return true;
    }

    /// <summary>
    /// window.go <c>onMessageRowSelected</c> for the current
    /// <see cref="SelectedKey"/>. A conversation row shows the whole
    /// conversation and marks only the member <c>Conversation.Build</c> picks
    /// (<see cref="MarkConversationRead"/>).
    /// </summary>
    private void AnnounceSelection()
    {
        var row = SelectedRow;
        if (row is null)
        {
            SelectedKey = null;
            SelectionCleared?.Invoke(this, EventArgs.Empty);
        }
        SelectedRowChanged?.Invoke(this, row);
        SelectedMessageChanged?.Invoke(this, row?.Message);
        RefreshActionFlags();
        if (row is { ShowsConversation: true, Key.Thread: { } tid })
        {
            MarkConversationRead(tid);
        }
        else if (row is not null && !Model.InOutbox(row.Message))
        {
            ScheduleMarkRead(row.Message.Id);
        }
        else
        {
            ScheduleMarkRead(null); // the daemon refuses flags on outbox messages
        }
    }

    /// <summary>
    /// Arms the mark-as-read timer for the member opening conversation
    /// <paramref name="tid"/> marks read (<see cref="ConversationModel.MarkRead"/>:
    /// the newest message that is not an event, when it is unread; older
    /// unread members stay unread), once the folder members are known;
    /// nothing when the selection has moved on by then, or when thread.get
    /// fails. The conversation view's controller calls it again when the
    /// members arrive after such a failure
    /// (<see cref="ConversationController.MembersChanged"/>).
    /// </summary>
    public void MarkConversationRead(ThreadId tid)
    {
        Scope.VerifyAccess();
        // The previous selection's timer goes now, whatever the wait.
        ScheduleMarkRead(null);
        EnsureMembers(tid, () =>
        {
            if (SelectedRow is not { ShowsConversation: true, Summary: { } thread } row || row.Key != new ListKey(Thread: tid)
                || !Model.Members.TryGetValue(tid, out var mem) || !mem.Complete)
            {
                return;
            }
            ScheduleMarkRead(Conversation.Build(thread, mem.List).MarkRead);
        });
    }

    // Tells the conversation view that the members of the selected
    // conversation may have changed (a reload kept its key): it merges them,
    // or asks for them again when the reload dropped them.
    private void AnnounceSelectedMembers()
    {
        if (SelectedRow is { ShowsConversation: true, Key.Thread: { } tid })
        {
            ThreadMembersChanged?.Invoke(this, tid);
        }
    }

    /// <summary>
    /// notify.messagesChanged for the account of the selected row, just
    /// before the folder is listed again (notify.go <c>refreshShown</c>;
    /// <see cref="MailboxController.RefreshShown"/>): the cache let go of the
    /// account's messages, so what the pane shows is fetched again. A message
    /// row is announced again, which shows its message afresh (and re-arms
    /// the mark-as-read timer, a no-op for a read message); a conversation
    /// row's folder members are forgotten, so the reload's answer asks
    /// thread.get for them again (their senders and dates may have changed),
    /// and the conversation view drops the bodies it holds
    /// (<see cref="ConversationChanged"/>). Not the whole selection
    /// announcement for a conversation: that would ask thread.get a second
    /// time (<see cref="MarkConversationRead"/>).
    /// </summary>
    public void RefreshShown(AccountId acc)
    {
        Scope.VerifyAccess();
        if (SelectedRow is not { } row || row.Message.AccountId != acc)
        {
            return;
        }
        if (row is { ShowsConversation: true, Key.Thread: { } tid })
        {
            RefreshMembers(tid);
            ConversationChanged?.Invoke(this, tid);
            return;
        }
        AnnounceSelection();
    }

    /// <summary>Re-evaluates the per-message actions for the selected row (actions.go <c>refreshMessageActions</c>).</summary>
    public void RefreshActionFlags()
    {
        Scope.VerifyAccess();
        var f = ActionRules.MessageActionState(SelectedRow, Model);
        if (f == ActionFlags)
        {
            return;
        }
        ActionFlags = f;
        ActionFlagsChanged?.Invoke(this, f);
    }

    /// <summary>
    /// Runs <paramref name="done"/> with the selected row and every message
    /// it stands for: one, or all the folder members of a conversation row,
    /// fetched first when they are not known yet (the selection must still
    /// be that conversation by then; threads.go <c>selectedIDs</c>).
    /// </summary>
    public void SelectedIds(Action<ListRow, IReadOnlyList<MessageId>> done)
    {
        ArgumentNullException.ThrowIfNull(done);
        Scope.VerifyAccess();
        if (SelectedRow is not { } row)
        {
            return;
        }
        if (Model.RowIds(row) is { } ids)
        {
            done(row, ids);
            return;
        }
        if (row.Key.Thread is not { } tid)
        {
            return;
        }
        EnsureMembers(tid, () =>
        {
            if (SelectedRow is { Thread: true } r && r.Key.Thread == tid && Model.RowIds(r) is { } known)
            {
                done(r, known);
            }
        });
    }

    /// <summary>The subject a confirmation shows for a row: the conversation's, or the message's (threads.go <c>rowSubject</c>).</summary>
    public static string RowSubject(ListRow row)
    {
        ArgumentNullException.ThrowIfNull(row);
        if (row.Thread && row.Summary is { } summary)
        {
            return LoadedMessageText.SubjectText(summary.Subject);
        }
        return LoadedMessageText.SubjectText(row.Message.Subject);
    }

    /// <summary>The flagged state the star moves a row to (thread_model.go <c>flagTarget</c>).</summary>
    public static bool FlagTarget(ListRow row) => MailModel.FlagTarget(row);

    /// <summary>
    /// Arms the mark-as-read timer for the newly selected message,
    /// cancelling any pending one; null only cancels (actions.go
    /// <c>scheduleMarkRead</c>). A message that is read already, or unknown
    /// to the list, arms nothing. The timer runs on the
    /// <see cref="TimeProvider"/>.
    /// </summary>
    private void ScheduleMarkRead(MessageId? id)
    {
        CancelMarkRead();
        markReadId = id;
        if (id is not { } mid || Model.Message(mid) is not { } found || FolderTree.HasFlag(found.Summary.Flags, Flag.Seen))
        {
            return;
        }
        var delay = Settings.MarkReadDelay;
        if (delay <= 0)
        {
            MarkRead?.Invoke(this, mid);
            return;
        }
        var wait = MarkReadTick * delay;
        var timer = CancellationTokenSource.CreateLinkedTokenSource(Scope.Lifetime);
        markReadTimer = timer;
        // Taken now: a later selection may cancel and dispose the source
        // before the wait has even started.
        var cancelled = timer.Token;
        Scope.RunDetached(async _ =>
        {
            try
            {
                await Task.Delay(wait, time, cancelled);
            }
            catch (OperationCanceledException)
            {
                return;
            }
            // Cancelled meanwhile: the field no longer holds this timer.
            if (!ReferenceEquals(markReadTimer, timer) || closed)
            {
                return;
            }
            CancelMarkRead();
            if (markReadId == mid)
            {
                MarkRead?.Invoke(this, mid);
            }
        });
    }

    private void CancelMarkRead()
    {
        var timer = markReadTimer;
        markReadTimer = null;
        if (timer is not null)
        {
            timer.Cancel();
            timer.Dispose();
        }
    }

    // Changes between loads

    /// <summary>
    /// Inserts a notified message at the top of the list when it belongs to
    /// the listed folder (the list part of folders.go <c>onNewMessage</c>;
    /// the folder half adjusts the badge and filters out what the model
    /// holds already). While the page is (re)loading the reply will include
    /// the message; only a settled list gets the row, and only when the
    /// active filter would have listed it anyway.
    /// </summary>
    public void ApplyNewMessage(NewMessageNotification n)
    {
        ArgumentNullException.ThrowIfNull(n);
        Scope.VerifyAccess();
        var k = new FolderKey(n.AccountId, n.FolderId);
        var s = n.Message;
        if (k != Model.ListFolder || Model.Message(s.Id) is not null)
        {
            return;
        }
        if (Model.Loading || Model.ListErr is not null)
        {
            return;
        }
        if (Model.Grouped)
        {
            // Into its conversation row, or a new one at the top; without a
            // thread id (a daemon still linking) the list is asked again.
            if (Model.ApplyNewMessage(s, Model.ListFilter, SelectedKey ?? new ListKey()))
            {
                SyncRows();
                if (s.ThreadId is { } tid)
                {
                    ThreadMembersChanged?.Invoke(this, tid);
                    // A conversation that shows the user's replies: the
                    // arrival may be one of them (a Bcc to oneself), which
                    // only the daemon tells by its Message-ID.
                    var replies = Model.TIndex.TryGetValue(tid, out var ti) && Model.Threads[ti].SentCount > 0;
                    if (replies || (Model.Members.TryGetValue(tid, out var mem) && mem.Sent.Count > 0))
                    {
                        RefetchMembers(tid);
                    }
                }
            }
            else
            {
                LoadMessages();
            }
            return;
        }
        if (FolderTree.MatchesFilter(s, Model.ListFilter) && Model.InsertMessage(0, s))
        {
            Reconcile(SelectionHint.Keep);
        }
    }

    /// <summary>
    /// notify.newMessage for another folder of the listed folder's account
    /// (<see cref="MailboxController.OnNewMessageElsewhere"/>): a message in a
    /// sent folder that belongs to a listed conversation is the user's reply
    /// to it, so the conversation is asked for again
    /// (<see cref="RefetchMembers"/>): its SentCount changes (a single
    /// message becomes a conversation row) and the conversation view, when it
    /// shows it, gets the reply. Nothing in flat mode, and nothing while a
    /// sent folder or the outbox is listed (their conversations have no
    /// replies of their own).
    /// </summary>
    public void ApplyNewMessageElsewhere(NewMessageNotification n)
    {
        ArgumentNullException.ThrowIfNull(n);
        Scope.VerifyAccess();
        if (!Model.Grouped || Model.Loading || Model.ListFolder is not { } k || k.Account != n.AccountId || k.Folder == n.FolderId
            || n.Message.ThreadId is not { } tid || !Model.TIndex.ContainsKey(tid)
            || Model.FolderRole(new FolderKey(n.AccountId, n.FolderId)) != Api.FolderRole.Sent)
        {
            return;
        }
        var listed = FolderRoleOf(k);
        if (listed == Api.FolderRole.Sent || listed == Api.FolderRole.Outbox)
        {
            return;
        }
        RefetchMembers(tid);
    }

    /// <summary>
    /// notify.messagesChanged named a sent folder of the account of the
    /// selected conversation (<see cref="MailboxController.RefreshShownSent"/>),
    /// whose folder is not named itself (RefreshShown covers that): the
    /// user's replies it shows are asked for again.
    /// </summary>
    public void RefreshShownSent(AccountId acc)
    {
        Scope.VerifyAccess();
        if (!Model.Grouped || SelectedRow is not { } row || row.Message.AccountId != acc || !row.ShowsConversation
            || row.Key.Thread is not { } tid)
        {
            return;
        }
        RefetchMembers(tid);
    }

    /// <summary>
    /// Changes the flags of the given messages in the model, refreshes their
    /// rows and the actions, and returns the ids that actually changed (the
    /// <c>apply</c> step of actions.go <c>setSeenIDs</c> /
    /// <c>setFlaggedIDs</c>; the caller adjusts the folder badge and sends
    /// message.flag).
    /// </summary>
    public IReadOnlyList<MessageId> ApplyFlags(IReadOnlyList<MessageId> ids, IReadOnlyList<Flag>? setFlags = null, IReadOnlyList<Flag>? clearFlags = null)
    {
        Scope.VerifyAccess();
        var changed = Model.ApplyFlags(ids, setFlags ?? [], clearFlags ?? []);
        RefreshRows(changed);
        RefreshActionFlags();
        if (Model.Grouped)
        {
            foreach (var tid in ThreadsOf(changed))
            {
                ThreadMembersChanged?.Invoke(this, tid);
            }
        }
        return changed;
    }

    // The conversations the given messages are members of in the grouped
    // list, each once, in the order they first appear.
    private List<ThreadId> ThreadsOf(IReadOnlyList<MessageId> ids)
    {
        var seen = new HashSet<ThreadId>();
        var output = new List<ThreadId>();
        foreach (var id in ids)
        {
            if (Model.MemberOf.TryGetValue(id, out var tid) && seen.Add(tid))
            {
                output.Add(tid);
            }
        }
        return output;
    }

    /// <summary>
    /// Drops messages from the list in either mode and returns what puts
    /// them back after a failed move or delete (messages.go
    /// <c>removeRows</c>; Swift's <c>Restore</c>). A grouped conversation
    /// whose members are not all known cannot be edited in place; the list
    /// is loaded again instead and the restore does nothing. A restore after
    /// the list moved on (a reload) does nothing either.
    /// </summary>
    public Action RemoveRows(IReadOnlyList<MessageId> ids)
    {
        ArgumentNullException.ThrowIfNull(ids);
        Scope.VerifyAccess();
        if (!Model.Grouped)
        {
            var restores = ids.Select(RemoveMessageRow).ToList();
            return () =>
            {
                for (var i = restores.Count - 1; i >= 0; i--)
                {
                    restores[i]();
                }
            };
        }
        // The conversations the messages belong to, while the model still
        // knows it.
        var tids = ThreadsOf(ids);
        if (Model.RemoveMessages(ids) is not { } removal)
        {
            LoadMessages();
            return () => { };
        }
        var gen = Model.ListGen;
        SyncRowsAfterRemoval();
        foreach (var tid in tids)
        {
            ThreadMembersChanged?.Invoke(this, tid);
        }
        return () =>
        {
            if (Model.ListGen != gen)
            {
                return;
            }
            Model.RestoreRemoval(removal);
            SyncRows();
            foreach (var tid in tids)
            {
                ThreadMembersChanged?.Invoke(this, tid);
            }
        };
    }

    /// <summary>
    /// Drops a message from the flat list (messages.go
    /// <c>removeMessageRow</c>). When it was the selected one its neighbour
    /// is selected (the pane follows), or the pane is cleared when the list
    /// ran empty.
    /// </summary>
    private Action RemoveMessageRow(MessageId id)
    {
        if (Model.RemoveMessage(id) is not { } removed)
        {
            return () => { };
        }
        var gen = Model.ListGen;
        SyncRowsAfterRemoval();
        return () =>
        {
            if (Model.ListGen != gen || !Model.InsertMessage(removed.Index, removed.Summary))
            {
                return;
            }
            Reconcile(SelectionHint.Keep);
        };
    }

    /// <summary>
    /// The list's share of outbox.go <c>refreshOutboxViews</c>: the outbox
    /// listing is reloaded when it is shown; <see cref="OutboxRefreshed"/>
    /// hands the account on for the reader's share.
    /// </summary>
    public void RefreshOutboxViews(AccountId acc)
    {
        Scope.VerifyAccess();
        if (Model.FolderByRole(acc, Api.FolderRole.Outbox) is { } outbox && Model.ListFolder == new FolderKey(acc, outbox.Id))
        {
            LoadMessages();
        }
        OutboxRefreshed?.Invoke(this, acc);
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "{Method} {Folder} failed: {Reason}")]
    private static partial void LogListFailed(ILogger logger, string method, string folder, string reason);

    [LoggerMessage(Level = LogLevel.Warning, Message = "{Method} (more) failed: {Reason}")]
    private static partial void LogMoreFailed(ILogger logger, string method, string reason);

    [LoggerMessage(Level = LogLevel.Warning, Message = "thread.get failed: {Reason}")]
    private static partial void LogThreadGetFailed(ILogger logger, string reason);
}
