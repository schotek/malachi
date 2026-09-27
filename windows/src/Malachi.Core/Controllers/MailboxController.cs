// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/MailboxController.swift
// (MailboxController; SidebarStatus is SidebarStatus.cs, and the private
// Countdown class a counter the fan-out's lambdas share); GTK:
// ui/internal/window/folders.go (loadAccounts, loadFolders, fetchFolders,
// rebuildFolderList, showEmptySidebarStatus, showFolderStatus,
// selectFolder, highlightFolderRow, updateFolderRow, onSyncFinished,
// onOutboxChanged, onNewMessage), collapse.go (toggleFolder,
// toggleAccount, saveCollapse, onCollapseChanged), favourites.go
// (toggleFavourite, saveFavourites, onFavouritesChanged), notify.go
// (handleNotification, the sidebar's share), sync.go (loadSyncStatus,
// applySyncState, triggerSync, triggerAccountSync, startSync), outbox.go
// (trackOutbox, cancelSendFrom), status.go (showOutbox), window.go
// (refreshListTitle, showConnectionState's data side) and actions.go
// (callThen).
//
// What changes on Windows (docs/windows-port.md §7): the RPC plumbing is a
// ControllerScope the list half (MailboxController.List.cs) and the actions
// share, as they share Swift's `perform`; Swift's `closed` flag is the
// scope's IsClosed. The Swift callbacks are events of the same words, the
// list half's hooks settable delegates as in Swift (the list installs
// them, a test replaces them). The state a view binds to is observable,
// and the sidebar is published as a snapshot (Entries) whose rows keep
// their SidebarKey across rebuilds, with the highlighted row as a key
// (SelectedEntryKey), which is the source of truth for the WinUI list: a
// view applies Entries by key (KeyedListSync) and then selects
// SelectedEntryKey, again after a sync that moved rows (WinUI may drop the
// selection of a moved item; phase E verifies). The status line is reached
// through IMailboxSync, the share of SyncController the mailbox uses.

using System;
using System.Collections.Generic;
using System.Threading.Tasks;
using CommunityToolkit.Mvvm.ComponentModel;
using Malachi.Core.Api;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Settings;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>
/// The folder half of the GTK main window: accounts and folders, the
/// sidebar entries, the selection, folds and pins, and the reactions to
/// sync and new-message events that touch the sidebar. No WinUI: the views
/// subscribe to the events and bind to the properties, and send the user's
/// actions back through the methods.
/// </summary>
/// <remarks>
/// <para>
/// The message list half is <see cref="ListController"/>, which installs
/// itself into the list hooks (<see cref="ReloadMessages"/> and the
/// others).
/// </para>
/// <para>
/// UI-thread-affine: create it on the UI thread and call it there. Every
/// RPC runs through <see cref="Scope"/>, so the continuation after the call
/// is on the UI thread too and the generation checks race with nothing (the
/// GTK window's <c>glib.IdleAdd</c> discipline).
/// </para>
/// </remarks>
public sealed partial class MailboxController : ObservableObject, IDisposable
{
    private readonly Action<string> toast;
    private readonly ILogger logger;
    private readonly OutboxTracker outbox = new();
    private readonly List<SettingsChangeToken> settingsTokens = [];
    private bool savingCollapse;
    private bool savingFavourites;

    /// <summary>A mailbox over <paramref name="client"/>, created on the UI thread.</summary>
    /// <param name="client">The transport; calls fail with not connected until the connection controller reports a connection.</param>
    /// <param name="settings">Where folds and pins persist.</param>
    /// <param name="sync">The status line and banner state (SyncController).</param>
    /// <param name="toast">Shows a transient message (the window's toast overlay).</param>
    /// <param name="pending">Counts the background work of the mailbox and its halves; a tracker of its own when null.</param>
    /// <param name="logger">Method names, codes and ids only.</param>
    public MailboxController(
        RpcClient client,
        SettingsStore settings,
        IMailboxSync sync,
        Action<string> toast,
        PendingWork? pending = null,
        ILogger<MailboxController>? logger = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        ArgumentNullException.ThrowIfNull(settings);
        ArgumentNullException.ThrowIfNull(sync);
        ArgumentNullException.ThrowIfNull(toast);
        Client = client;
        Settings = settings;
        Sync = sync;
        this.toast = toast;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        Scope = new ControllerScope(pending ?? new PendingWork(this.logger));
        Model = new MailModel(collapsed: CollapseState.Load(settings), favourites: FavouriteState.Load(settings));
        SidebarStatus = new SidebarStatus.Folders();
        Entries = [];
        ListHeading = new ListHeading(L10n.T("Messages"), "");
        // The folded-away parts of the sidebar and the pinned folders follow
        // along when another window (or reg add) changes them.
        settingsTokens.Add(settings.OnChange(SettingsKey.CollapsedFolders, OnCollapseChanged));
        settingsTokens.Add(settings.OnChange(SettingsKey.CollapsedAccounts, OnCollapseChanged));
        settingsTokens.Add(settings.OnChange(SettingsKey.FavouriteFolders, OnFavouritesChanged));
        sync.Accounts = () => Model.Accounts;
        sync.FolderName = (acc, id) => Model.Folder(new FolderKey(acc, id)) is { } f ? FolderTree.FolderTitle(f) : "";
    }

    // Callbacks (the sidebar)

    /// <summary><see cref="MailModel.Entries"/> were rebuilt: reload the sidebar rows (Swift <c>onEntriesChanged</c>).</summary>
    public event EventHandler? EntriesChanged;

    /// <summary>
    /// Only the badges of the entries moved (folders.go
    /// <c>updateFolderRow</c>; Swift <c>onBadgesChanged</c>); without a
    /// handler <see cref="EntriesChanged"/> is raised instead.
    /// </summary>
    public event EventHandler? BadgesChanged;

    /// <summary>Switch between the folder list and a status page (Swift <c>onFolderStatus</c>).</summary>
    public event EventHandler<SidebarStatus>? FolderStatusChanged;

    /// <summary>
    /// Highlight the selected folder's row (a null key clears the
    /// highlight); the flag says which of a pinned folder's two rows the user
    /// clicked last (folders.go <c>highlightFolderRow</c>; Swift
    /// <c>onSelectionChanged</c>). Raised on every selection, a re-entry for
    /// the already selected folder included, after
    /// <see cref="SelectedEntryKey"/> was updated.
    /// </summary>
    public event EventHandler<FolderSelection>? SelectionChanged;

    // Callbacks (the rest of the window)

    /// <summary>The selected folder changed (a null key: nothing selected any more), with the Favourites flag (Swift <c>onFolderSelected</c>).</summary>
    public event EventHandler<FolderSelection>? FolderSelected;

    /// <summary>
    /// The title over the message list and the counts under it (window.go
    /// <c>refreshListTitle</c>; Swift <c>onListTitleChanged</c>): raised
    /// wherever the selection or the cached counts change, after
    /// <see cref="ListHeading"/> was updated.
    /// </summary>
    public event EventHandler<ListHeading>? ListTitleChanged;

    /// <summary>account.list answered (Swift <c>onAccountsLoaded</c>).</summary>
    public event EventHandler<IReadOnlyList<Account>>? AccountsLoaded;

    // The list half's hooks

    /// <summary>
    /// The list's <c>loadMessages</c> (messages.go), installed by the list
    /// half (Swift <c>reloadMessages</c>). Called wherever the GTK window
    /// calls <c>loadMessages</c> from the folder code: a new selection, a
    /// cleared one, a finished sync of the selected folder.
    /// </summary>
    public Action? ReloadMessages { get; set; }

    /// <summary>
    /// A notified message for the listed folder that the model does not
    /// hold yet: the list inserts it (folders.go <c>onNewMessage</c>, the
    /// list half; Swift <c>onNewMessageForList</c>). The notification's
    /// summary carries its account and folder ids filled in.
    /// </summary>
    public Action<NewMessageNotification>? OnNewMessageForList { get; set; }

    /// <summary>
    /// The account's outbox contents changed and its folder list was
    /// reloaded: refresh the views showing the outbox (outbox.go
    /// <c>refreshOutboxViews</c>; Swift <c>refreshOutboxViews</c>).
    /// </summary>
    public Action<AccountId>? RefreshOutboxViews { get; set; }

    /// <summary>
    /// The backend went away: fold a conversation waiting for its members
    /// back (window.go <c>showConnectionState</c>, <c>collapseLoading</c> +
    /// <c>syncRows</c>; Swift <c>collapseLoading</c>).
    /// </summary>
    public Action? CollapseLoading { get; set; }

    /// <summary>The transport.</summary>
    public RpcClient Client { get; }

    /// <summary>Where folds and pins persist.</summary>
    public SettingsStore Settings { get; }

    /// <summary>The status line and banner state.</summary>
    public IMailboxSync Sync { get; }

    /// <summary>
    /// The UI thread, the closed flag, the lifetime and the background work
    /// of the mailbox, its list half and the actions, which live and die
    /// together.
    /// </summary>
    public ControllerScope Scope { get; }

    /// <summary>The view model; the list half changes its message fields.</summary>
    public MailModel Model { get; }

    /// <summary>
    /// account.list returned at least one account (window.go
    /// <c>hasAccounts</c>); false until the first answer. The message pane's
    /// No Accounts placeholder keys off it.
    /// </summary>
    [ObservableProperty]
    public partial bool HasAccounts { get; private set; }

    /// <summary>What the sidebar shows right now.</summary>
    [ObservableProperty]
    public partial SidebarStatus SidebarStatus { get; private set; }

    /// <summary>
    /// The sidebar rows as last announced by <see cref="EntriesChanged"/> or
    /// <see cref="BadgesChanged"/>: a snapshot, replaced whole, never changed
    /// in place; keyed by <see cref="SidebarKey.Of"/>.
    /// </summary>
    [ObservableProperty]
    public partial IReadOnlyList<FolderEntry> Entries { get; private set; }

    /// <summary>
    /// The row that carries the selection (folders.go
    /// <c>highlightFolderRow</c>): the selected folder's row in the
    /// Favourites section or in the tree, whichever the user clicked last,
    /// the other one when that one is gone; null while nothing is selected
    /// or the folder's rows are folded out of sight. The view's selection
    /// follows it.
    /// </summary>
    [ObservableProperty]
    public partial SidebarKey? SelectedEntryKey { get; private set; }

    /// <summary>The title and counts over the message list (<see cref="ListTitleChanged"/>).</summary>
    [ObservableProperty]
    public partial ListHeading ListHeading { get; private set; }

    /// <summary>
    /// The title of the list page for the selected folder (window.go
    /// <c>refreshListTitle</c>): the folder's display name, or "Messages"
    /// when none is selected. The window caption follows it.
    /// </summary>
    public string SelectedFolderTitle =>
        Model.Selected is { } k && Model.Folder(k) is { } f ? FolderTree.FolderTitle(f) : L10n.T("Messages");

    /// <summary>
    /// The counts under the title (window.go <c>refreshListTitle</c>,
    /// <c>folderCountsText</c>): "" while no folder is selected.
    /// </summary>
    public string SelectedFolderSubtitle =>
        Model.Selected is { } k && Model.Folder(k) is { } f ? FolderTree.FolderCountsText(f) : "";

    /// <summary>
    /// Detaches from the settings and drops every late reply (the window
    /// closed): the scope closes, which ends the calls of the list half and
    /// the actions too.
    /// </summary>
    public void Close()
    {
        Scope.Close();
        foreach (var t in settingsTokens)
        {
            t.Cancel();
        }
        settingsTokens.Clear();
        Sync.Close();
    }

    /// <summary>Closes the mailbox (<see cref="Close"/>).</summary>
    public void Dispose() => Close();

    // RPC plumbing

    /// <summary>
    /// Runs one call and hands the outcome to <paramref name="done"/> on the
    /// UI thread, unless the mailbox closed meanwhile (Swift
    /// <c>perform</c>). A null <paramref name="timeout"/> takes the
    /// method's own (5 s for everything the sidebar asks).
    /// </summary>
    public Task Perform<TParams, TResult>(
        RpcMethod<TParams, TResult> method, TParams parameters, Action<Outcome<TResult>> done, TimeSpan? timeout = null) =>
        Scope.Perform(Client, method, parameters, done, timeout);

    /// <summary>
    /// The window's <c>callThen</c> (actions.go; Swift <c>call</c>): on
    /// failure the error is logged, toasted as
    /// <see cref="RpcErrorText.Text(string, Exception)"/> of
    /// <paramref name="what"/> and handed to <paramref name="onError"/>; on
    /// success <paramref name="onOk"/> runs with the result. Either may be
    /// null.
    /// </summary>
    public Task Call<TParams, TResult>(
        RpcMethod<TParams, TResult> method,
        TParams parameters,
        string what,
        Action<Exception>? onError = null,
        Action<TResult>? onOk = null,
        TimeSpan? timeout = null)
    {
        ArgumentNullException.ThrowIfNull(method);
        ArgumentNullException.ThrowIfNull(what);
        return Perform(method, parameters, outcome =>
        {
            if (outcome.TryGetValue(out var res, out var err))
            {
                onOk?.Invoke(res);
                return;
            }
            LogCallFailed(logger, method.Name, err!.Message);
            toast(RpcErrorText.Text(what, err));
            onError?.Invoke(err);
        }, timeout);
    }

    /// <summary>Shows a transient message (the window's toast; Swift <c>toast</c>).</summary>
    internal void Toast(string text) => toast(text);

    // Accounts and folders

    /// <summary>
    /// Runs account.list, then folder.list for every enabled account,
    /// rebuilds the sidebar and selects the initial folder (folders.go
    /// <c>loadAccounts</c>). Every reply is guarded by the folders
    /// generation, so a reconnect or a repeated call in the meantime simply
    /// wins.
    /// </summary>
    public void LoadAccounts()
    {
        Scope.VerifyAccess();
        var gen = Model.BumpFolders();
        if (Model.Entries.Count == 0)
        {
            ShowFolderStatus("", L10n.T("Loading…"), "");
        }
        Perform(API.AccountList, new EmptyParams(), outcome =>
        {
            if (gen != Model.FoldersGen)
            {
                return;
            }
            if (!outcome.TryGetValue(out var res, out var err))
            {
                LogAccountListFailed(logger, err!.Message);
                if (Model.Entries.Count == 0)
                {
                    ShowFolderStatus("dialog-warning-symbolic", L10n.T("Folders Unavailable"), RpcErrorText.Text(L10n.T("Loading folders"), err));
                }
                return;
            }
            Model.Accounts = res.Accounts;
            Model.Folders.Clear();
            Model.FolderErr.Clear();
            HasAccounts = res.Accounts.Count > 0;
            AccountsLoaded?.Invoke(this, res.Accounts);
            // sync.status may have answered before the accounts were known,
            // and the status line and its popover follow the account set
            // (added, removed, paused, renamed).
            Sync.RefreshFooter();

            var enabled = Model.EnabledAccounts;
            if (enabled.Count == 0)
            {
                RebuildFolderList();
                return;
            }
            // One rebuild once every account answered (or failed): Swift's
            // Countdown, shared by the lambdas below.
            var pending = enabled.Count;
            foreach (var a in enabled)
            {
                FetchFolders(a.Id, gen, () =>
                {
                    if (--pending == 0)
                    {
                        RebuildFolderList();
                    }
                });
            }
        });
    }

    /// <summary>
    /// Runs folder.list for one account and rebuilds the sidebar from the
    /// answer (folders.go <c>loadFolders</c>); <paramref name="gen"/> guards
    /// the reply.
    /// </summary>
    public void LoadFolders(AccountId acc, ulong gen)
    {
        Scope.VerifyAccess();
        FetchFolders(acc, gen, RebuildFolderList);
    }

    /// <summary>
    /// Runs folder.list for <paramref name="acc"/> and stores the result (or
    /// the error) in the model, then calls <paramref name="done"/>
    /// (folders.go <c>fetchFolders</c>). A reply from a stale generation is
    /// dropped without calling <paramref name="done"/>.
    /// </summary>
    internal void FetchFolders(AccountId acc, ulong gen, Action done)
    {
        Perform(API.FolderList, new FolderListParams { AccountId = acc }, outcome =>
        {
            if (gen != Model.FoldersGen)
            {
                return;
            }
            if (!outcome.TryGetValue(out var res, out var err))
            {
                LogFolderListFailed(logger, acc.ToString(), err!.Message);
                // Keep the last good list, if any, rather than emptying the
                // sidebar on a transient failure.
                Model.FolderErr[acc] = err;
            }
            else
            {
                Model.Folders[acc] = res.Folders;
                Model.FolderErr.Remove(acc);
                TrackOutbox(acc);
            }
            done();
        });
    }

    /// <summary>
    /// The bookkeeping after every folder.list of <paramref name="acc"/>
    /// (outbox.go <c>trackOutbox</c>): a shrink of the outbox that the user
    /// did not cause by cancelling means messages were delivered, which
    /// gets a toast.
    /// </summary>
    private void TrackOutbox(AccountId acc)
    {
        var total = Model.FolderByRole(acc, FolderRole.Outbox)?.Total ?? 0;
        var sent = outbox.Track(acc, total);
        if (sent > 0)
        {
            // TRANSLATORS: toast after delivery; %d is the number of messages.
            toast(L10n.N("%d message sent", "%d messages sent", sent));
        }
    }

    /// <summary>
    /// Notes that the user removed one outbox message of
    /// <paramref name="acc"/> (cancel sending), so the next shrink of the
    /// outbox is not toasted as a delivery (outbox.go
    /// <c>cancelSendFrom</c>). The actions call it before message.delete:
    /// removing a queued or failed message moves pendingOutbox or
    /// failedOutbox, and the notify.syncState that follows reloads the
    /// folders, possibly before the reply arrives.
    /// </summary>
    public void NoteOutboxCancelled(AccountId acc) => outbox.NoteCancelled(acc);

    /// <summary>
    /// Takes a <see cref="NoteOutboxCancelled"/> back after the daemon
    /// refused the removal (outbox.go <c>cancelSendFrom</c>'s error path); a
    /// folder reload in between may have used it up already.
    /// </summary>
    public void NoteOutboxCancelFailed(AccountId acc) => outbox.NoteCancelFailed(acc);

    /// <summary>
    /// Recreates the sidebar entries from the model (folders.go
    /// <c>rebuildFolderList</c>). The selection is preserved when its
    /// folder still exists, otherwise the initial folder is selected. A
    /// folder hidden under a fold still exists: folding must not move the
    /// selection or reload the message list, so existence is decided
    /// against the model (<see cref="MailModel.FolderListed"/>) and not
    /// against the rows on screen.
    /// </summary>
    public void RebuildFolderList()
    {
        Scope.VerifyAccess();
        try
        {
            Model.RebuildEntries();
            Entries = Model.Entries;
            EntriesChanged?.Invoke(this, EventArgs.Empty);

            // Entries, not rows: an account folded shut leaves its header
            // behind and the sidebar is not empty.
            if (Model.Entries.Count == 0)
            {
                ShowEmptySidebarStatus();
                // Nothing to show; a folder selected earlier is gone with its rows.
                if (Model.Selected is not null)
                {
                    ClearSelection();
                }
                return;
            }
            SetSidebarStatus(new SidebarStatus.Folders());

            if (Model.Selected is { } sel && Model.FolderListed(sel))
            {
                // Highlights the row when there is one, and does nothing
                // beyond that while the folder is folded out of sight.
                Select(sel);
                return;
            }
            if (Model.InitialFolder() is { } k)
            {
                Select(k);
                return;
            }
            // Only non-selectable containers: clear the list.
            ClearSelection();
        }
        finally
        {
            // The list title follows on every way out: the reload that led
            // here may have brought new counts, or taken the selected folder
            // away.
            RefreshListTitle();
        }
    }

    /// <summary>
    /// Explains an empty sidebar (folders.go <c>showEmptySidebarStatus</c>):
    /// no account, none enabled, an error, or accounts that have not
    /// synchronised yet.
    /// </summary>
    public void ShowEmptySidebarStatus()
    {
        var enabled = Model.EnabledAccounts;
        if (!HasAccounts)
        {
            ShowFolderStatus("system-users-symbolic", L10n.T("No Accounts"), L10n.T("Add a mail account in Preferences to see its folders here."));
            return;
        }
        if (enabled.Count == 0)
        {
            ShowFolderStatus("system-users-symbolic", L10n.T("No Enabled Accounts"), L10n.T("Enable an account in Preferences to see its folders here."));
            return;
        }
        foreach (var a in enabled)
        {
            if (Model.FolderErr.TryGetValue(a.Id, out var err))
            {
                ShowFolderStatus("dialog-warning-symbolic", L10n.T("Folders Unavailable"), RpcErrorText.Text(L10n.T("Loading folders"), err));
                return;
            }
        }
        ShowFolderStatus("folder-symbolic", L10n.T("No Folders Yet"), L10n.T("Folders appear after the first synchronisation."));
    }

    /// <summary>
    /// Switches the sidebar to a status page (folders.go
    /// <c>showFolderStatus</c>). Everything is plain text; error texts carry
    /// a technical detail from the backend and nothing shown to the user is
    /// interpreted as markup.
    /// </summary>
    private void ShowFolderStatus(string icon, string title, string description) =>
        SetSidebarStatus(new SidebarStatus.Status(icon, title, description));

    private void SetSidebarStatus(SidebarStatus s)
    {
        SidebarStatus = s;
        FolderStatusChanged?.Invoke(this, s);
    }

    // Selection

    /// <summary>
    /// The user chose a folder row (window.go, the row-selected handler):
    /// remembers which of a pinned folder's two rows was clicked, so the
    /// highlight stays on it across rebuilds, and makes the folder current.
    /// </summary>
    public void SelectFolder(FolderKey k, bool fav)
    {
        Scope.VerifyAccess();
        Model.SelectedFav = fav;
        Select(k);
    }

    /// <summary>
    /// Makes <paramref name="k"/> the current folder (folders.go
    /// <c>selectFolder</c>): highlights its row, announces it and loads its
    /// messages. Idempotent for the already selected and listed folder,
    /// which only re-highlights the row and refreshes the title, whose
    /// counts a folder reload may have changed.
    /// </summary>
    private void Select(FolderKey k)
    {
        if (k == Model.Selected && k == Model.ListFolder)
        {
            Highlight(k, Model.SelectedFav);
            RefreshListTitle();
            return;
        }
        Model.Selected = k;
        RefreshListTitle();
        FolderSelected?.Invoke(this, new FolderSelection(k, Model.SelectedFav));
        Highlight(k, Model.SelectedFav);
        RequestReloadMessages();
    }

    /// <summary>Nothing selected any more: clears the highlight and the list.</summary>
    private void ClearSelection()
    {
        Model.Selected = null;
        FolderSelected?.Invoke(this, new FolderSelection(null, Model.SelectedFav));
        Highlight(null, Model.SelectedFav);
        RequestReloadMessages();
    }

    /// <summary>
    /// Moves the highlight (folders.go <c>highlightFolderRow</c>, Swift
    /// <c>onSelectionChanged</c>): <see cref="SelectedEntryKey"/> first, then
    /// the event.
    /// </summary>
    private void Highlight(FolderKey? k, bool fav)
    {
        SelectedEntryKey = HighlightKey(k, fav);
        SelectionChanged?.Invoke(this, new FolderSelection(k, fav));
    }

    /// <summary>
    /// The row folders.go <c>highlightFolderRow</c> selects: the folder's row
    /// on the side clicked last, the other one when that is gone (folded
    /// away, or just unpinned); null when neither is listed.
    /// </summary>
    private SidebarKey? HighlightKey(FolderKey? k, bool fav)
    {
        if (k is not { } key)
        {
            return null;
        }
        SidebarKey? other = null;
        foreach (var e in Model.Entries)
        {
            if (e.Header || e.Key != key)
            {
                continue;
            }
            if (e.Favourite == fav)
            {
                return SidebarKey.Of(e);
            }
            other ??= SidebarKey.Of(e);
        }
        return other;
    }

    /// <summary>The window's <c>loadMessages</c>: the list half's hook.</summary>
    private void RequestReloadMessages() => ReloadMessages?.Invoke();

    /// <summary>
    /// Announces the title and the counts of the selected folder (window.go
    /// <c>refreshListTitle</c>): "Search" and the number of results while
    /// searching. Called wherever the selection or the cached counts change:
    /// <c>Select</c>, <see cref="UpdateFolderRow"/> and
    /// <see cref="RebuildFolderList"/>, and by the list's search.
    /// </summary>
    public void RefreshListTitle()
    {
        var heading = Model.Search.Active
            ? new ListHeading(L10n.T("Search"), SearchModel.SearchTotalText(Model.Total, Model.Search.Shown))
            : new ListHeading(SelectedFolderTitle, SelectedFolderSubtitle);
        ListHeading = heading;
        ListTitleChanged?.Invoke(this, heading);
    }

    // Folds and pins

    /// <summary>
    /// Folds a folder's children away, or brings them back, and persists the
    /// change (collapse.go <c>toggleFolder</c>). The selection and the
    /// message list are left alone on purpose: folding is a way of looking
    /// at the sidebar, not a way of navigating.
    /// </summary>
    public void ToggleFolder(FolderKey k)
    {
        Scope.VerifyAccess();
        Model.Collapsed.ToggleFolder(k);
        SaveCollapse();
        RebuildFolderList();
    }

    /// <summary>Folds a whole account's tree away, or brings it back (collapse.go <c>toggleAccount</c>).</summary>
    public void ToggleAccount(AccountId id)
    {
        Scope.VerifyAccess();
        Model.Collapsed.ToggleAccount(id);
        SaveCollapse();
        RebuildFolderList();
    }

    /// <summary>
    /// <see cref="ToggleFolder"/> for a view that knows the target state (a
    /// twisty, Left and Right on a folder row): a no-op when the model
    /// already agrees, which returns false so the key still reaches the list
    /// for its normal navigation (collapse.go <c>addFolderShortcuts</c>).
    /// </summary>
    public bool SetFolderCollapsed(FolderKey k, bool collapsed)
    {
        if (Model.Collapsed.FolderCollapsed(k) == collapsed)
        {
            return false;
        }
        ToggleFolder(k);
        return true;
    }

    /// <summary><see cref="ToggleAccount"/> for a view that knows the target state; false when the model already agrees.</summary>
    public bool SetAccountCollapsed(AccountId id, bool collapsed)
    {
        if (Model.Collapsed.AccountCollapsed(id) == collapsed)
        {
            return false;
        }
        ToggleAccount(id);
        return true;
    }

    /// <summary>
    /// Pins a folder or unpins it, persists the change and redraws the
    /// sidebar at once (favourites.go <c>toggleFavourite</c>). The selection
    /// and the message list are left alone: pinning is a way of arranging
    /// the sidebar, not of navigating.
    /// </summary>
    public void ToggleFavourite(FolderKey k)
    {
        Scope.VerifyAccess();
        Model.Favourites.Toggle(k);
        SaveFavourites();
        RebuildFolderList();
    }

    /// <summary>
    /// Writes the folds out without reacting to the change notification it
    /// causes in this window (collapse.go <c>saveCollapse</c>): the settings
    /// raise it synchronously, inside the write.
    /// </summary>
    private void SaveCollapse()
    {
        savingCollapse = true;
        try
        {
            Model.Collapsed.Save(Settings, Model.Accounts);
        }
        finally
        {
            savingCollapse = false;
        }
    }

    /// <summary>
    /// Re-reads the folds after another window (or another process sharing
    /// the profile) folded something (collapse.go <c>onCollapseChanged</c>).
    /// </summary>
    private void OnCollapseChanged()
    {
        if (savingCollapse || Scope.IsClosed)
        {
            return;
        }
        Model.Collapsed = CollapseState.Load(Settings);
        RebuildFolderList();
    }

    private void SaveFavourites()
    {
        savingFavourites = true;
        try
        {
            Model.Favourites.Save(Settings, Model.Accounts);
        }
        finally
        {
            savingFavourites = false;
        }
    }

    private void OnFavouritesChanged()
    {
        if (savingFavourites || Scope.IsClosed)
        {
            return;
        }
        Model.Favourites = FavouriteState.Load(Settings);
        RebuildFolderList();
    }

    // Counts

    /// <summary>
    /// Changes the cached unread and total counts of a folder and refreshes
    /// the badges and the title (the mark-read / mark-unread bookkeeping of
    /// actions.go <c>setSeenIDs</c>).
    /// </summary>
    public void AdjustCounts(FolderKey k, int dUnread, int dTotal)
    {
        Scope.VerifyAccess();
        Model.AdjustCounts(k, dUnread, dTotal);
        UpdateFolderRow(k);
    }

    /// <summary>
    /// Shifts the cached counts for messages leaving <paramref name="src"/>
    /// for <paramref name="target"/> (null: leaving the store;
    /// <see cref="MailModel.MoveCounts"/>) and refreshes the badges and the
    /// title (actions.go <c>trackMoves</c>' shift).
    /// </summary>
    public void MoveCounts(FolderKey src, FolderKey? target, int unread, int n)
    {
        Scope.VerifyAccess();
        Model.MoveCounts(src, target, unread, n);
        UpdateFolderRow(src); // refreshes every row, the target's too
    }

    /// <summary>
    /// Refreshes the unread badges and the list title's counts after the
    /// counts of <paramref name="k"/> moved (folders.go
    /// <c>updateFolderRow</c>). Every row is refreshed, not just k's: a
    /// collapsed ancestor's badge counts the folders it hides, k itself may
    /// be one of them, and a pinned folder has a second row in the
    /// Favourites section. The keys of the entries stay as they were.
    /// </summary>
    public void UpdateFolderRow(FolderKey k)
    {
        Entries = Model.Entries;
        if (BadgesChanged is { } badges)
        {
            badges(this, EventArgs.Empty);
        }
        else
        {
            EntriesChanged?.Invoke(this, EventArgs.Empty);
        }
        RefreshListTitle();
    }

    // Notifications and connection

    /// <summary>
    /// Dispatches a decoded daemon notification to the handlers below
    /// (notify.go <c>handleNotification</c>, the sidebar's share). The
    /// desktop notification of a new message and the compose windows'
    /// account refresh belong to other components the app wires alongside.
    /// </summary>
    public void HandleNotification(DaemonNotification n)
    {
        ArgumentNullException.ThrowIfNull(n);
        Scope.VerifyAccess();
        switch (n)
        {
            case DaemonNotification.NewMessage m:
                HandleNewMessage(m.Payload);
                break;
            case DaemonNotification.SyncState s:
                HandleSyncState(s.State);
                break;
            case DaemonNotification.AuthRequired a:
                HandleAuthRequired(a.Payload);
                break;
            case DaemonNotification.AccountsChanged:
                HandleAccountsChanged();
                break;
            case DaemonNotification.Unknown u:
                LogUnknownNotification(logger, u.Method);
                break;
        }
    }

    /// <summary>
    /// The account set changed under us (notify.accountsChanged): the
    /// banner's account may be gone or edited; a still-failing account is
    /// announced again by the daemon once its syncer restarts.
    /// </summary>
    public void HandleAccountsChanged()
    {
        Sync.HideAuthBanner();
        LoadAccounts();
    }

    /// <summary>notify.authRequired: reveals the sign-in banner for the account.</summary>
    public void HandleAuthRequired(AuthRequiredNotification n)
    {
        ArgumentNullException.ThrowIfNull(n);
        Sync.ShowAuthRequired(n, Model.Account(n.AccountId));
    }

    /// <summary>
    /// notify.newMessage, the sidebar's share (folders.go
    /// <c>onNewMessage</c>): the folder's counts move, the total always, the
    /// unread count for an unseen message; a message for the listed folder
    /// goes to the list through <see cref="OnNewMessageForList"/> first,
    /// unless the list holds it already (delivered twice: the counts were
    /// adjusted the first time).
    /// </summary>
    public void HandleNewMessage(NewMessageNotification n)
    {
        ArgumentNullException.ThrowIfNull(n);
        Scope.VerifyAccess();
        var k = new FolderKey(n.AccountId, n.FolderId);
        var s = n.Message;
        if (string.IsNullOrEmpty(s.AccountId.Value))
        {
            s = s with { AccountId = n.AccountId };
        }
        if (string.IsNullOrEmpty(s.FolderId.Value))
        {
            s = s with { FolderId = n.FolderId };
        }
        if (k == Model.ListFolder)
        {
            if (Model.Message(s.Id) is not null)
            {
                return;
            }
            OnNewMessageForList?.Invoke(n with { Message = s });
        }
        var unread = FolderTree.HasFlag(s.Flags, Flag.Seen) ? 0 : 1;
        Model.AdjustCounts(k, unread, 1);
        UpdateFolderRow(k);
    }

    /// <summary>
    /// notify.syncState and every state of sync.status (sync.go
    /// <c>applySyncState</c>): records the state, then reloads folders when
    /// the account left the syncing state and refreshes the outbox views
    /// when its number of pending or failed outgoing messages moved. An
    /// account's first state is no move: its folders are loaded with the
    /// accounts.
    /// </summary>
    public void HandleSyncState(SyncState s)
    {
        ArgumentNullException.ThrowIfNull(s);
        Scope.VerifyAccess();
        var (prev, cur) = Sync.Apply(s);
        OnSyncFinished(prev, cur);
        if (prev is not null && (prev.PendingOutbox != cur.PendingOutbox || prev.FailedOutbox != cur.FailedOutbox))
        {
            OnOutboxChanged(cur.AccountId);
        }
    }

    /// <summary>
    /// Runs when an account leaves the syncing state (folders.go
    /// <c>onSyncFinished</c>): folders are reloaded and, when the synced
    /// folder is the selected one (or the whole account was synced), the
    /// list.
    /// </summary>
    internal void OnSyncFinished(SyncState? prev, SyncState cur)
    {
        if (prev is null || prev.Status != SyncStatus.Syncing || cur.Status == SyncStatus.Syncing)
        {
            return;
        }
        LoadFolders(cur.AccountId, Model.FoldersGen);
        if (Model.Selected is { } sel && sel.Account == cur.AccountId && (cur.FolderId is null || cur.FolderId == sel.Folder))
        {
            ReloadMessages?.Invoke();
        }
    }

    /// <summary>
    /// Runs when the account's number of pending or failed outgoing
    /// messages moved (folders.go <c>onOutboxChanged</c>): the folders are
    /// reloaded, so the outbox row appears or goes with its contents, and
    /// the views showing the outbox are refreshed. When the selected folder
    /// was the outbox and it emptied, <see cref="RebuildFolderList"/> falls
    /// back to the initial folder.
    /// </summary>
    public void OnOutboxChanged(AccountId acc)
    {
        Scope.VerifyAccess();
        FetchFolders(acc, Model.FoldersGen, () =>
        {
            RebuildFolderList();
            RefreshOutboxViews?.Invoke(acc);
        });
    }

    /// <summary>
    /// Runs sync.trigger for the selected folder, or for every account when
    /// nothing is selected (sync.go <c>triggerSync</c>; win.refresh).
    /// </summary>
    public void TriggerSync()
    {
        Scope.VerifyAccess();
        var parameters = Model.Selected is { } sel
            ? new SyncTriggerParams { AccountId = sel.Account, FolderId = sel.Folder }
            : new SyncTriggerParams();
        StartSync(parameters);
    }

    /// <summary>
    /// Runs sync.trigger for one whole account (sync.go
    /// <c>triggerAccountSync</c>: Check and Try Again in the status
    /// popover).
    /// </summary>
    public void TriggerSync(AccountId accountId)
    {
        Scope.VerifyAccess();
        StartSync(new SyncTriggerParams { AccountId = accountId });
    }

    /// <summary>
    /// Runs sync.trigger with <paramref name="parameters"/> (sync.go
    /// <c>startSync</c>). The line's spinner starts at once; the daemon's
    /// notify.syncState takes over, with the status line's fallback timer
    /// so the spinner never sticks.
    /// </summary>
    private void StartSync(SyncTriggerParams parameters)
    {
        Sync.BeginChecking();
        Perform(API.SyncTrigger, parameters, outcome =>
        {
            if (outcome.IsSuccess)
            {
                return;
            }
            LogSyncTriggerFailed(logger, outcome.Error!.Message);
            Sync.RefreshFooter();
            toast(RpcErrorText.Text(L10n.T("Checking for new mail"), outcome.Error));
        });
    }

    /// <summary>Runs sync.status and applies every state (sync.go <c>loadSyncStatus</c>).</summary>
    public void LoadSyncStatus()
    {
        Scope.VerifyAccess();
        Sync.LoadSyncStatus(Client, s =>
        {
            if (!Scope.IsClosed)
            {
                HandleSyncState(s);
            }
        });
    }

    /// <summary>
    /// Selects the account's outbox, as a click on its row in the account's
    /// tree would (status.go <c>showOutbox</c>: the status popover's link to
    /// unsent messages). False when the outbox cannot be shown: the account
    /// is paused or has no outbox folder (<see cref="MailModel.OutboxKey"/>).
    /// </summary>
    public bool ShowOutbox(AccountId acc)
    {
        Scope.VerifyAccess();
        if (Model.OutboxKey(acc) is not { } k)
        {
            return false;
        }
        SelectFolder(k, fav: false);
        return true;
    }

    /// <summary>
    /// The connection state changed (window.go <c>showConnectionState</c>,
    /// the data side): the status line learns it first (it names the
    /// connection while there is none, and forgets what sync.status said); on
    /// a connection the accounts and the sync states are loaded; when the
    /// backend went away every in-flight reply is dropped and what is shown
    /// stays until the reconnect reloads it. A connection with a failed
    /// system.info still loads, as the GTK window does (it loads on the
    /// socket, not on the answer). A daemon of another protocol version is
    /// no connection at all (the handshake refused it): nothing loads, as
    /// when the backend went away.
    /// </summary>
    public void HandleConnection(ConnectionState state)
    {
        ArgumentNullException.ThrowIfNull(state);
        Scope.VerifyAccess();
        Sync.SetConnection(state);
        switch (state)
        {
            case ConnectionState.Connected or ConnectionState.InfoFailed:
                LoadAccounts();
                LoadSyncStatus();
                break;
            case ConnectionState.Unavailable or ConnectionState.ProtocolMismatch or ConnectionState.Stopping:
                Model.BumpAll();
                if (Model.Grouped)
                {
                    CollapseLoading?.Invoke();
                }
                break;
        }
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "{Method} failed: {Reason}")]
    private static partial void LogCallFailed(ILogger logger, string method, string reason);

    [LoggerMessage(Level = LogLevel.Warning, Message = "account.list failed: {Reason}")]
    private static partial void LogAccountListFailed(ILogger logger, string reason);

    [LoggerMessage(Level = LogLevel.Warning, Message = "folder.list {Account} failed: {Reason}")]
    private static partial void LogFolderListFailed(ILogger logger, string account, string reason);

    [LoggerMessage(Level = LogLevel.Debug, Message = "sync.trigger failed: {Reason}")]
    private static partial void LogSyncTriggerFailed(ILogger logger, string reason);

    [LoggerMessage(Level = LogLevel.Information, Message = "notification {Method}")]
    private static partial void LogUnknownNotification(ILogger logger, string method);
}
