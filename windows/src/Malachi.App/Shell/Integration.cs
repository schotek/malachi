// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/Integration.swift (Integration:
// init, wireConnection, wireNotifications, wireMailbox, wireActions,
// wireHooks, and ComposeManager.install's hooks); GTK: window.New in
// ui/internal/window/window.go and main.go's compose manager. The glue
// between the shell and the controllers, made once the main window
// exists, in Swift's order: the sync controller and the mailbox, the list
// half, the message cache, the actions, compose; then the wiring. Where
// the order of the handlers matters it is GTK's: the status line learns a
// connection state before the list does (the mailbox tells it first), and
// a new message reaches the desktop notification before the list.
//
// The screens come in wave 2 (docs/windows-port.md §15, phase E): the
// sidebar, the list and the reader go into MainWindow's regions, and their
// view-side wiring (the reader following the selection, the banners and
// their buttons, the status popover's rows, the message windows) joins
// here next to the controller wiring below. Their entry points are marked
// "Wave 2". Until then the main window shows the connection, the status
// line and the counts of what the mailbox loaded.

using System;
using System.Collections.Generic;
using Malachi.Core.Compose;
using Malachi.Core.Controllers;
using Microsoft.Extensions.Logging;

namespace Malachi.App.Shell;

/// <summary>The controllers of the main window and their wiring (Swift Integration).</summary>
public sealed class Integration : IDisposable
{
    private readonly AppState state;
    private readonly MainWindow mainWindow;
    private readonly List<IDisposable> tokens = [];

    /// <summary>Makes the controllers and wires them to the shell and to each other.</summary>
    public Integration(AppState state, MainWindow mainWindow)
    {
        ArgumentNullException.ThrowIfNull(state);
        ArgumentNullException.ThrowIfNull(mainWindow);
        this.state = state;
        this.mainWindow = mainWindow;
        var logs = state.Logs;
        // The controllers' toasts go over the main window's message pane,
        // whatever window is active (window.go Toast); a window-local
        // action toasts in its own window through the router.
        Action<string> mainToast = text => mainWindow.Toasts.Show(text);

        Sync = new SyncController(logger: logs.CreateLogger<SyncController>());
        Mailbox = new MailboxController(state.Client, state.Settings, Sync, mainToast, logger: logs.CreateLogger<MailboxController>());
        List = new ListController(Mailbox, state.Settings, logger: logs.CreateLogger<ListController>());
        Cache = new MessageCache(state.Client, mainToast, logs.CreateLogger<MessageCache>());
        Actions = new ActionsController(Mailbox, List, Cache, state.Settings, mainToast, logs.CreateLogger<ActionsController>());
        Compose = new ComposeController(state.Client, state.Settings, logger: logs.CreateLogger<ComposeController>());

        WireConnection();
        WireNotifications();
        WireMailbox();
        WireActions();
        WireCompose();
        WireHooks();
    }

    /// <summary>The status line, the sign-in and certificate banners.</summary>
    public SyncController Sync { get; }

    /// <summary>The sidebar and the data of the mailbox.</summary>
    public MailboxController Mailbox { get; }

    /// <summary>The message list half of the mailbox.</summary>
    public ListController List { get; }

    /// <summary>The loaded messages of the reader and the message windows.</summary>
    public MessageCache Cache { get; }

    /// <summary>The per-message actions.</summary>
    public ActionsController Actions { get; }

    /// <summary>The compose windows and what they share.</summary>
    public ComposeController Compose { get; }

    /// <summary>
    /// Wave 2 (E5): the compose window factory. Sets
    /// <see cref="ComposeController.MakeWindow"/> and the hooks that open
    /// compose windows (ComposeManager.install: composeNew, openCompose,
    /// raiseDraft), which enables New Message.
    /// </summary>
    public void InstallComposeWindows(Func<ComposeParams, IComposeWindowHandle> makeWindow)
    {
        ArgumentNullException.ThrowIfNull(makeWindow);
        Compose.MakeWindow = makeWindow;
        state.Hooks.OpenCompose = Compose.Open;
        state.Hooks.ComposeNew = () => Compose.Open(new ComposeParams { Kind = ComposeKind.New });
    }

    /// <summary>Stops the controllers' work; the daemon has been stopped by then.</summary>
    public void Dispose()
    {
        foreach (var t in tokens)
        {
            t.Dispose();
        }
        tokens.Clear();
        Compose.Dispose();
        Cache.Dispose();
        List.Dispose();
        Mailbox.Dispose();
        Sync.Dispose();
    }

    // window.go showConnectionState: the mailbox, which tells the status line
    // first and loads the accounts and the sync status once connected, then
    // the list (its banner, the loading rows, late replies dropped). Until
    // the connection reports anything the line says the first attempt is
    // underway. The line is redrawn every minute, so "Up to date · 15:04"
    // becomes a date the next day.
    private void WireConnection()
    {
        var hub = state.Notifications;
        Sync.SetConnection(hub.ConnectionState);
        Sync.StartRefreshing();
        tokens.Add(hub.AddConnectionState(s =>
        {
            Mailbox.HandleConnection(s);
            List.HandleConnection(s);
        }));
    }

    // window/notify.go handleNotification.
    private void WireNotifications()
    {
        var hub = state.Notifications;
        tokens.Add(hub.AddNewMessage(n =>
        {
            // GTK order: the desktop notification first, then the list.
            Platform.PlatformServices.NewMessage(n);
            Mailbox.HandleNewMessage(n);
        }));
        tokens.Add(hub.AddSyncState(Mailbox.HandleSyncState));
        tokens.Add(hub.AddAuthRequired(Mailbox.HandleAuthRequired));
        tokens.Add(hub.AddAccountsChanged(() =>
        {
            Mailbox.HandleAccountsChanged();
            // ComposeManager.install: the From rows read the accounts afresh.
            Compose.Invalidate();
        }));
    }

    // window.go refreshListTitle: the selected folder is the window's
    // title; the list's own header shows it with its counts (wave 2).
    private void WireMailbox()
    {
        Mailbox.ListTitleChanged += (_, heading) => mainWindow.ShowListHeading(heading);
        Mailbox.AccountsLoaded += (_, accounts) => mainWindow.ShowAccounts(accounts);
        Mailbox.EntriesChanged += (_, _) => mainWindow.ShowFolderCount(FolderCount());
    }

    // window.go 337-360 and 411-433: the main window's commands act on the
    // selection; the list's mark-read timer and its flags feed back. Wave 2
    // (E3, E4) sets the handlers of the per-message commands (the
    // selection's actions) and the message windows' delegate.
    private void WireActions()
    {
        Actions.Confirm = ((AlertService)state.Alerts).ConfirmHook();
        Actions.RaiseDraft = d => state.Hooks.RaiseDraft?.Invoke(d) ?? false;
        Actions.OpenComposeRequested += (_, p) => state.Hooks.OpenCompose?.Invoke(p);
        List.MarkRead += (_, id) => Actions.MarkRead(id);
        List.ActionFlagsChanged += (_, flags) => mainWindow.Commands.Flags = flags;
    }

    // ComposeManager.install and main.go's mgr.OnSent: a short confirmation
    // over the main window (the outbox and the "sent" toast carry the
    // rest).
    private void WireCompose()
    {
        Compose.Sent += (_, text) => mainWindow.Toasts.Show(text, 2);
        state.Hooks.RaiseDraft = draft =>
        {
            if (Compose.FindDraft(draft) is not { } w)
            {
                return false;
            }
            w.Present();
            return true;
        };
    }

    // ui/main.go addActions and Integration.swift wireHooks.
    private void WireHooks()
    {
        var hooks = state.Hooks;
        hooks.CheckForNewMail = Mailbox.TriggerSync;
        hooks.SetMessageFilter = List.SetListFilter;
        hooks.MessageFilter = () => List.ListFilter;
        hooks.SearchActive = () => List.SearchActive;
        hooks.FocusSearch = mainWindow.FocusSearch;
    }

    private int FolderCount()
    {
        var n = 0;
        foreach (var folders in Mailbox.Model.Folders.Values)
        {
            n += folders.Count;
        }
        return n;
    }
}
