// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ComposeController.swift
// (ComposeController); GTK: ui/internal/compose/manager.go (Manager, Open,
// Accounts, Placeholder, SelfAddress, FindDraft, remove, Invalidate,
// refreshAccounts) and dummy.go (dummyAccounts).
//
// What the compose windows share (the client, the settings, the account
// list) and the list of open windows, without the widgets. Windows are made
// by an injected factory (the WinUI layer) and reached through
// IComposeWindowHandle. Manager.OnSent is the Sent event, which the windows
// raise through ReportSent (C# lets no other class raise an event).
// SaveForQuitAsync is the Windows addition of docs/windows-port.md §0: Quit
// saves every window's dirty draft and asks only where that failed. Another
// Windows addition: a window the factory could not make is logged and
// leaves nothing behind, since what asks for one (a key, a reply, a
// mailto: activation) has nothing above it to catch the failure, and one
// bad window would otherwise end the app with every other window's text.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using CommunityToolkit.Mvvm.ComponentModel;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Settings;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>
/// compose.Manager: opens compose windows and keeps what they share.
/// Created on the UI thread, lives as long as the app, UI-thread-affine.
/// </summary>
public sealed partial class ComposeController : ObservableObject, IDisposable
{
    private readonly List<IComposeWindowHandle> windows = [];
    private readonly ILogger logger;
    private readonly ControllerScope scope;
    private IReadOnlyList<Account> known = [];
    private bool fetched;

    /// <param name="client">The transport.</param>
    /// <param name="settings">The settings the windows share.</param>
    /// <param name="pending">Where the background work is counted (tests wait on it); a tracker of its own by default.</param>
    /// <param name="logger">Method names and errors only, never mail content.</param>
    public ComposeController(RpcClient client, SettingsStore settings, PendingWork? pending = null, ILogger<ComposeController>? logger = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        ArgumentNullException.ThrowIfNull(settings);
        Client = client;
        Settings = settings;
        this.logger = logger ?? (ILogger)NullLogger.Instance;
        scope = new ControllerScope(pending);
    }

    /// <summary>
    /// Manager.OnSent: raised with a short message when a window queued a
    /// message (a toast on the main window).
    /// </summary>
    public event EventHandler<string>? Sent;

    /// <summary>
    /// compose.dummyAccounts: the identity used while <c>account.list</c>
    /// lists nothing (or cannot be asked).
    /// </summary>
    public static IReadOnlyList<Account> PlaceholderAccounts { get; } =
    [
        new Account
        {
            Id = "acc_dummy",
            Config = new AccountConfig { Name = "Placeholder", Email = "me@example.invalid", DisplayName = "Malachi User" },
            Enabled = true,
            State = new SyncState { AccountId = "acc_dummy", Status = SyncStatus.Idle },
        },
    ];

    /// <summary>The transport.</summary>
    public RpcClient Client { get; }

    /// <summary>The settings the windows share.</summary>
    public SettingsStore Settings { get; }

    /// <summary>
    /// compose.newWindow + Present: builds and shows a window prefilled from
    /// the params; the window reads <see cref="Accounts"/> and
    /// <see cref="Placeholder"/> for it.
    /// </summary>
    public Func<ComposeParams, IComposeWindowHandle>? MakeWindow { get; set; }

    /// <summary>Manager.Accounts: the known accounts, or the placeholder while the backend cannot list any.</summary>
    public IReadOnlyList<Account> Accounts => known.Count == 0 ? PlaceholderAccounts : known;

    /// <summary>Manager.Placeholder: whether <see cref="Accounts"/> is the placeholder identity.</summary>
    public bool Placeholder => known.Count == 0;

    /// <summary>Manager.SelfAddress: the first account's address, for Reply All exclusion.</summary>
    public Address SelfAddress
    {
        get
        {
            var a = Accounts[0];
            return new Address { Name = a.Config.DisplayName, Email = a.Config.Email };
        }
    }

    /// <summary>The open windows, in the order they were opened.</summary>
    public IReadOnlyList<IComposeWindowHandle> OpenWindows => [.. windows];

    /// <summary>Manager.Open: shows a new compose window prefilled from <paramref name="p"/>. Nothing happens without a factory.</summary>
    public void Open(ComposeParams p)
    {
        scope.VerifyAccess();
        ArgumentNullException.ThrowIfNull(p);
        if (MakeWindow is not { } make)
        {
            LogNoFactory(logger);
            return;
        }
        if (!fetched)
        {
            RefreshAccounts();
        }
        IComposeWindowHandle w;
        try
        {
            w = make(p);
        }
#pragma warning disable CA1031 // The factory is the UI's: any failure to make a window is logged, and the app and its other windows go on.
        catch (Exception e) when (e is not OutOfMemoryException)
#pragma warning restore CA1031
        {
            LogWindowFailed(logger, e);
            return;
        }
        windows.Add(w);
        OnPropertyChanged(nameof(OpenWindows));
        var message = BlockedSummary.Text(p.Blocked);
        if (message.Length > 0)
        {
            // The backend quoted the original without its remote images and
            // scripts; said once, as after a save.
            w.Toast(message);
        }
    }

    /// <summary>Manager.FindDraft: the open window that edits <paramref name="draft"/> (from <c>draft.open</c>), null when none.</summary>
    public IComposeWindowHandle? FindDraft(Draft draft)
    {
        ArgumentNullException.ThrowIfNull(draft);
        return windows.FirstOrDefault(w => w.Edits(draft));
    }

    /// <summary>
    /// Manager.remove: the window closed. The window calls it once it really
    /// closes, after its draft controller's
    /// <see cref="ComposeDraftController.Cleanup"/> (GTK: cleanup); a window
    /// left in the list would keep being asked by <see cref="FindDraft"/> and
    /// <see cref="SaveForQuitAsync"/>.
    /// </summary>
    public void Remove(IComposeWindowHandle w)
    {
        scope.VerifyAccess();
        if (windows.RemoveAll(x => ReferenceEquals(x, w)) > 0)
        {
            OnPropertyChanged(nameof(OpenWindows));
        }
    }

    /// <summary>
    /// Manager.Invalidate: drops the cached account list
    /// (<c>notify.accountsChanged</c>). Open windows are refreshed at once;
    /// otherwise the next window fetches again.
    /// </summary>
    public void Invalidate()
    {
        scope.VerifyAccess();
        fetched = false;
        if (windows.Count > 0)
        {
            RefreshAccounts();
        }
    }

    /// <summary>
    /// Manager.refreshAccounts: asks the backend once per process (until
    /// <see cref="Invalidate"/>) and pushes the result to open windows.
    /// </summary>
    public void RefreshAccounts()
    {
        scope.VerifyAccess();
        fetched = true;
        scope.Perform(Client, API.AccountList, new EmptyParams(), outcome =>
        {
            if (!outcome.TryGetValue(out var res, out var error))
            {
                LogAccountListFailed(logger, error!);
                fetched = false; // try again on the next window
                return;
            }
            known = res.Accounts;
            OnPropertyChanged(nameof(Accounts));
            OnPropertyChanged(nameof(Placeholder));
            OnPropertyChanged(nameof(SelfAddress));
            foreach (var w in windows.ToArray())
            {
                w.SetAccounts(Accounts, Placeholder);
            }
        });
    }

    /// <summary>
    /// A window queued a message (its draft controller's
    /// <see cref="ComposeDraftController.Sent"/>): raises <see cref="Sent"/>.
    /// </summary>
    public void ReportSent(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        Sent?.Invoke(this, text);
    }

    /// <summary>
    /// Windows addition (docs/windows-port.md §0): Quit saves the unsaved
    /// changes of every open window as drafts, without asking. Returns the
    /// windows whose save failed, in the order they were opened: the app
    /// asks their close question, and only theirs.
    /// </summary>
    public async Task<IReadOnlyList<IComposeWindowHandle>> SaveForQuitAsync()
    {
        scope.VerifyAccess();
        var open = windows.ToArray();
        var saved = await Task.WhenAll(open.Select(w => w.SaveForQuitAsync()));
        return [.. open.Where((_, i) => !saved[i])];
    }

    /// <summary>
    /// The app is quitting: accounts that arrive later are dropped. Swift's
    /// manager has no end; it lives as long as the app.
    /// </summary>
    public void Dispose() => scope.Close();

    [LoggerMessage(Level = LogLevel.Error, Message = "compose window requested before the window factory was installed")]
    private static partial void LogNoFactory(ILogger logger);

    [LoggerMessage(Level = LogLevel.Error, Message = "a compose window could not be made")]
    private static partial void LogWindowFailed(ILogger logger, Exception error);

    [LoggerMessage(Level = LogLevel.Debug, Message = "account.list failed")]
    private static partial void LogAccountListFailed(ILogger logger, Exception error);
}
