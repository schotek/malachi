// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/AccountsPaneViewController.swift
// (loadAccounts, setAccounts, setAccountEnabled, removeAccount,
// moveSelected, moveAccount, reorderAccounts, acceptDrop, the pending set
// and closed; the add and edit completions' toasts); GTK:
// ui/internal/window/accounts_page.go (bindAccounts, loadAccounts,
// setAccounts, setAccountEnabled, removeAccount, presentEdit) and
// accounts_reorder.go (moveAccountBy, reorderAccounts). macOS keeps this
// in AppKit; on Windows it is a presentation class with tests
// (docs/windows-port.md §7.4), and the page renders Rows with
// KeyedListSync's view overload (§7.5).
//
// The page is the daemon's: account.list fills it, account.setEnabled,
// account.remove and account.reorder change it, and it reloads after its
// own actions (the page gets no notify.accountsChanged, as in GTK). The
// group stays insensitive until the first load succeeds and while an order
// is being saved; a row is insensitive while a call about its account is
// in flight. A pause or resume shows the switch as asked while it runs and
// flips it back when the daemon refuses; a new order shows at once and a
// failure reloads the page, because the daemon's order is the truth.
// Positions always come from the accounts, never from the view. Two
// guards GTK does without: a load that started before a reorder is
// dropped (it would put the old order back over the new one), and a move
// is refused while an order is being saved or while the row's own call
// runs (GTK's rows are insensitive then, so their keys do nothing).

using System;
using System.Collections.Generic;
using System.Linq;
using CommunityToolkit.Mvvm.ComponentModel;
using Malachi.Core.Api;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Presentation;

/// <summary>
/// The Accounts page of the preferences without the widgets: the rows, the
/// group's sensitivity and description, and the account actions of the
/// page. Create it, and call it, on the UI thread.
/// </summary>
public sealed partial class AccountsPageController : ObservableObject, IDisposable
{
    private readonly RpcClient client;
    private readonly ControllerScope scope;
    private readonly ILogger logger;

    // The accounts in the daemon's order (or the order just asked for).
    private List<Account> accounts = [];

    // Accounts whose call is in flight, with the switch state a pause or
    // resume asked for (null for a removal): their rows are insensitive.
    private readonly Dictionary<AccountId, bool?> pending = [];

    // The first load succeeded; a reorder is being saved.
    private bool loaded;
    private bool saving;

    // Bumped by every load and every reorder: a load's reply is only taken
    // while it is the newest of them.
    private int generation;

    /// <summary>A page over <paramref name="client"/>, on the calling (UI) thread.</summary>
    /// <param name="client">The daemon.</param>
    /// <param name="logger">Receives methods and codes, never an address.</param>
    /// <param name="pending">Counts the page's background work; one of its own when null.</param>
    public AccountsPageController(RpcClient client, ILogger<AccountsPageController>? logger = null, PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        this.client = client;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        scope = new ControllerScope(pending);
        Rows = [];
        Description = "";
        IsEmpty = true;
    }

    /// <summary>The rows changed (a load, a pause, a move, a call in flight or over).</summary>
    public event EventHandler<IReadOnlyList<AccountRow>>? RowsChanged;

    /// <summary>Show a toast over the preferences (a failed action, an account added or saved).</summary>
    public event EventHandler<string>? ToastRequested;

    /// <summary>
    /// The row of this account takes the keyboard focus (it was moved with
    /// the keyboard: holding Ctrl+Down keeps walking it down the list).
    /// </summary>
    public event EventHandler<AccountId>? FocusRequested;

    /// <summary>The accounts' rows, in the order shown.</summary>
    [ObservableProperty]
    public partial IReadOnlyList<AccountRow> Rows { get; private set; }

    /// <summary>The group's sensitivity: loaded, and no order being saved (the add button included).</summary>
    [ObservableProperty]
    public partial bool IsEnabled { get; private set; }

    /// <summary>The group's description: why the accounts could not be loaded, "" for none.</summary>
    [ObservableProperty]
    public partial string Description { get; private set; }

    /// <summary>No account: the page shows "No accounts yet".</summary>
    [ObservableProperty]
    public partial bool IsEmpty { get; private set; }

    /// <summary>The selected row (a click selects; Ctrl+Up and Ctrl+Down move it), null for none.</summary>
    [ObservableProperty]
    public partial AccountId? SelectedId { get; private set; }

    /// <summary>The page closed: late replies are dropped (Swift <c>closed</c>).</summary>
    public bool IsClosed => scope.IsClosed;

    /// <summary>The account of a row, for the wizard; null when it is not on the page.</summary>
    public Account? Account(AccountId id) => accounts.FirstOrDefault(a => a.Id == id);

    /// <summary>
    /// Runs <c>account.list</c> and shows its accounts (accounts_page.go
    /// <c>loadAccounts</c>): on failure the error goes into the group's
    /// description and the group stays insensitive.
    /// </summary>
    public void Load()
    {
        scope.VerifyAccess();
        if (IsClosed)
        {
            return;
        }
        var my = ++generation;
        scope.Perform(client, API.AccountList, new EmptyParams(), outcome =>
        {
            if (my != generation)
            {
                return;
            }
            if (!outcome.TryGetValue(out var res, out var error))
            {
                LogFailed(API.AccountList.Name, error);
                loaded = false;
                Description = RpcErrorText.Text(L10n.T("Loading accounts"), error);
                UpdateEnabled();
                return;
            }
            loaded = true;
            Description = "";
            SetAccounts(res.Accounts);
            UpdateEnabled();
        });
    }

    /// <summary>The row the user selected (a click), or none.</summary>
    public void Select(AccountId? id)
    {
        scope.VerifyAccess();
        SelectedId = id is { } a && Account(a) is not null ? a : (AccountId?)null;
    }

    /// <summary>
    /// Pauses or resumes the account through <c>account.setEnabled</c>
    /// (accounts_page.go <c>setAccountEnabled</c>): the row is insensitive
    /// and its switch shows <paramref name="want"/> meanwhile; a refusal
    /// flips it back and a toast says why; success leaves the account idle
    /// or disabled. A switch flipped where the row is insensitive (its call
    /// in flight, an order being saved) is put back.
    /// </summary>
    public void SetEnabled(AccountId id, bool want)
    {
        scope.VerifyAccess();
        if (IsClosed || Account(id) is not { } account)
        {
            return;
        }
        if (!IsEnabled || pending.ContainsKey(id) || account.Enabled == want)
        {
            Publish();
            return;
        }
        pending[id] = want;
        Publish();
        var what = want ? L10n.T("Resuming the account") : L10n.T("Pausing the account");
        scope.Perform(client, API.AccountSetEnabled, new AccountSetEnabledParams { AccountId = id, Enabled = want }, outcome =>
        {
            pending.Remove(id);
            // The row may have been torn down by a reload meanwhile.
            var i = IndexOf(id);
            if (i < 0)
            {
                Publish();
                return;
            }
            if (outcome.Error is { } error)
            {
                LogFailed(API.AccountSetEnabled.Name, error);
                Publish();
                Toast(RpcErrorText.Text(what, error));
                return;
            }
            var a = accounts[i];
            accounts[i] = a with
            {
                Enabled = want,
                State = a.State with { Status = want ? SyncStatus.Idle : SyncStatus.Disabled },
            };
            Publish();
        });
    }

    /// <summary>
    /// Asks "Remove this account?" through <paramref name="confirm"/>, then
    /// calls <c>account.remove</c> with the check box as
    /// <c>deleteLocalData</c> and reloads (accounts_page.go
    /// <c>removeAccount</c>); a failure makes the row sensitive again and a
    /// toast says why.
    /// </summary>
    public void Remove(AccountId id, ConfirmAccountRemoval confirm)
    {
        scope.VerifyAccess();
        ArgumentNullException.ThrowIfNull(confirm);
        if (IsClosed || !IsEnabled || Account(id) is not { } account || pending.ContainsKey(id))
        {
            return;
        }
        var prompt = new AccountRemovalPrompt(
            L10n.T("Remove this account?"),
            // TRANSLATORS: %s is the account's e-mail address.
            L10n.T("%s will be removed from Malachi Mail. Mail on the server is not affected.", account.Config.Email),
            L10n.T("_Remove"),
            L10n.T("Also delete _drafts and downloaded data"),
            ExtraDefault: true);
        scope.Perform(_ => confirm(prompt), answer =>
        {
            if (!answer.TryGetValue(out var a, out _) || !a.Confirmed || Account(id) is null || pending.ContainsKey(id))
            {
                return;
            }
            pending[id] = null;
            Publish();
            scope.Perform(client, API.AccountRemove, new AccountRemoveParams { AccountId = id, DeleteLocalData = a.DeleteLocalData }, outcome =>
            {
                pending.Remove(id);
                if (outcome.Error is { } error)
                {
                    LogFailed(API.AccountRemove.Name, error);
                    Publish();
                    Toast(RpcErrorText.Text(L10n.T("Removing the account"), error));
                    return;
                }
                Publish();
                Load();
            });
        });
    }

    /// <summary>Moves the selected account by <paramref name="delta"/> positions (Ctrl+Up, Ctrl+Down); false when nothing moved.</summary>
    public bool MoveSelected(int delta)
    {
        scope.VerifyAccess();
        return SelectedId is { } id && MoveBy(id, delta);
    }

    /// <summary>
    /// Moves the account by <paramref name="delta"/> positions, the keyboard
    /// path (accounts_reorder.go <c>moveAccountBy</c>): the moved row stays
    /// selected and takes the focus again. False when nothing moved (at the
    /// ends of the list, so the key is not swallowed there).
    /// </summary>
    public bool MoveBy(AccountId id, int delta)
    {
        scope.VerifyAccess();
        var from = IndexOf(id);
        return from >= 0 && Reorder(from, from + delta, focusMoved: true);
    }

    /// <summary>
    /// The account's row was dragged to <paramref name="to"/> (by its
    /// handle; the list may have moved it already): the new order is saved.
    /// False when nothing moved; the rows are published again then, so a
    /// list that moved the row itself puts it back.
    /// </summary>
    public bool MoveTo(AccountId id, int to)
    {
        scope.VerifyAccess();
        if (Reorder(IndexOf(id), to, focusMoved: false))
        {
            return true;
        }
        if (!IsClosed)
        {
            Publish();
        }
        return false;
    }

    /// <summary>
    /// The account wizard added an account (bindAccounts' OnDone): the page
    /// reloads and a toast names it.
    /// </summary>
    public void AccountAdded(AccountConfig config)
    {
        scope.VerifyAccess();
        ArgumentNullException.ThrowIfNull(config);
        if (IsClosed)
        {
            return;
        }
        Load();
        // TRANSLATORS: %s is the new account's e-mail address.
        Toast(L10n.T("Added %s", config.Email));
    }

    /// <summary>
    /// The account wizard saved an edited account, or signed it in again
    /// (presentEdit's OnDone): the page reloads and a toast names it.
    /// </summary>
    public void AccountSaved(AccountConfig config)
    {
        scope.VerifyAccess();
        ArgumentNullException.ThrowIfNull(config);
        if (IsClosed)
        {
            return;
        }
        Load();
        // TRANSLATORS: %s is the edited account's e-mail address.
        Toast(L10n.T("Saved %s", config.Email));
    }

    /// <summary>The page closed: every late reply is dropped from now on.</summary>
    public void Close()
    {
        scope.VerifyAccess();
        scope.Close();
    }

    /// <summary>Closes the page.</summary>
    public void Dispose() => Close();

    // accounts_reorder.go reorderAccounts: the rows show the new order at
    // once, then account.reorder saves it and the daemon's
    // notify.accountsChanged reorders the main window's sidebar; a failure
    // reloads the page.
    private bool Reorder(int from, int to, bool focusMoved)
    {
        if (IsClosed || !IsEnabled || from == to || from < 0 || to < 0 || from >= accounts.Count || to >= accounts.Count
            || pending.ContainsKey(accounts[from].Id))
        {
            return false;
        }
        var moved = accounts[from].Id;
        var reordered = AccountsPage.MoveAccount(accounts, from, to);
        // A load that started before this would bring the old order back.
        generation++;
        SetAccounts(reordered);
        if (focusMoved)
        {
            SelectedId = moved;
            scope.Raise(FocusRequested, this, moved);
        }
        saving = true;
        UpdateEnabled();
        var ids = reordered.Select(a => a.Id).ToList();
        scope.Perform(client, API.AccountReorder, new AccountReorderParams { AccountIds = ids }, outcome =>
        {
            saving = false;
            UpdateEnabled();
            if (outcome.Error is { } error)
            {
                LogFailed(API.AccountReorder.Name, error);
                Toast(RpcErrorText.Text(L10n.T("Saving the account order"), error));
                Load();
            }
        });
        return true;
    }

    // accounts_page.go setAccounts: the rows of these accounts; the calls
    // in flight and the selection stay with the accounts still there.
    private void SetAccounts(IReadOnlyList<Account> list)
    {
        accounts = [.. list];
        foreach (var id in pending.Keys.Where(id => IndexOf(id) < 0).ToList())
        {
            pending.Remove(id);
        }
        if (SelectedId is { } selected && IndexOf(selected) < 0)
        {
            SelectedId = null;
        }
        Publish();
    }

    private void Publish()
    {
        var rows = accounts
            .Select(a => pending.TryGetValue(a.Id, out var wanted) ? AccountRow.For(a, busy: true, wanted) : AccountRow.For(a))
            .ToList();
        Rows = rows;
        IsEmpty = rows.Count == 0;
        scope.Raise(RowsChanged, this, (IReadOnlyList<AccountRow>)rows);
    }

    private void UpdateEnabled() => IsEnabled = loaded && !saving;

    private void Toast(string text) => scope.Raise(ToastRequested, this, text);

    private int IndexOf(AccountId id) => accounts.FindIndex(a => a.Id == id);

    private void LogFailed(string method, Exception? error) =>
        LogCallFailed(logger, method, RpcErrorText.Classify(error).Kind, RpcErrorText.DaemonError(error)?.Code.Value ?? 0);

    [LoggerMessage(Level = LogLevel.Warning, Message = "{Method} failed: {Kind} {Code}")]
    private static partial void LogCallFailed(ILogger logger, string method, RpcErrorText.FailureKind kind, int code);
}
