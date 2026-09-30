// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the reading half of macos/Sources/MalachiMail/App/Integration.swift
// (the MessageWindows hub, makePaneView, the MessageActionsController and
// its installHooks, wireReading, wireActions, showNoAccountsPage) and of
// MessageActionsController.swift's AppKit needs (the confirmation's window,
// the links' question, the toasts where the click was); GTK: what window.New
// wires for the message pane (showMessage on the row-selected signal,
// openMessageWindow on row-activated, openDraft, refreshOutboxViews'
// reader share, emptyPageName) and the win.* actions of registerActions
// acting on the selection. It makes the reader's services once, puts the
// pane into the main window's Reader region, gives the main window's
// per-message commands their handlers (the selection's actions; the
// Integration feeds their flags), and fans out what the cache and the
// actions learn to every view and message window.

using System;
using System.Linq;
using Malachi.App.Attachments;
using Malachi.App.Commands;
using Malachi.App.MessageWindows;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Presentation;
using Malachi.Platform.Windows.Attachments;
using Malachi.Platform.Windows.Files;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Xaml;

namespace Malachi.App.Reader;

/// <summary>The reader, the message windows, the attachments and the links, wired to the controllers.</summary>
public sealed class ReaderHub : IDisposable
{
    private readonly Integration integration;

    /// <summary>Makes the reader's services and the pane, and wires them.</summary>
    public ReaderHub(AppState state, MainWindow mainWindow, Integration integration)
    {
        ArgumentNullException.ThrowIfNull(state);
        ArgumentNullException.ThrowIfNull(mainWindow);
        ArgumentNullException.ThrowIfNull(integration);
        this.integration = integration;
        var logs = state.Logs;
        var policy = new FileTypePolicy();
        var router = new MessageActionRouter(integration.Actions, integration.List)
        {
            MainWindow = () => mainWindow,
            Compose = p => state.Hooks.OpenCompose?.Invoke(p),
        };
        var links = new LinkOpener(state.Launcher, logs.CreateLogger<LinkOpener>())
        {
            Compose = p => state.Hooks.OpenCompose?.Invoke(p),
            // "Open This Link?": the masked link's text beside the
            // destination, or the destination alone for a link the daemon
            // did not list (LinkDecision.Confirm.Body).
            Confirm = (window, c, destination) => state.Alerts.OpenLinkQuestionAsync(window as Window, c.Text, destination),
            Toast = ToastIn,
            Owner = ReaderServices.Owner,
        };
        var opener = new AttachmentOpener(
            integration.Cache,
            state.OpenDir,
            new MarkOfTheWeb(),
            policy,
            state.Launcher,
            new AttachmentPickers(() => mainWindow, logs.CreateLogger<AttachmentPickers>()),
            AnsiLookAlikes.OfThisMachine,
            logs.CreateLogger<AttachmentOpener>())
        {
            Toast = ToastIn,
            Owner = ReaderServices.Owner,
        };
        var registry = new MessageWindowRegistry(integration.Cache, logs.CreateLogger<MessageWindowRegistry>())
        {
            Toast = ToastIn,
        };
        Services = new ReaderServices
        {
            State = state,
            Cache = integration.Cache,
            Router = router,
            Links = links,
            Attachments = opener,
            Preview = new AttachmentPreview(opener, state),
            Windows = registry,
            FileTypes = policy,
            Icons = new ChipIcons(new ShellFileTypes()),
            Issues = integration.Issues,
            IssueSite = id => integration.Mailbox.Model.Account(id)?.Config.Jira?.SiteUrl ?? "",
        };
        // issueActions onBusy and onIssue: every card showing the issue
        // follows (window.go setIssueBusy, applyIssue).
        integration.Issues.BusyChanged += (_, e) =>
        {
            foreach (var reader in registry.Readers)
            {
                reader.IssueBusyChanged(e.Account, e.Key);
            }
        };
        integration.Issues.IssueChanged += (_, e) =>
        {
            foreach (var reader in registry.Readers)
            {
                reader.ApplyIssue(e.Account, e.Issue);
            }
        };
        registry.MakeMessageWindow = s => new MessageWindow(Services, s);
        registry.MakeEmbeddedWindow = (containing, attachment, result) => new EmbeddedMessageWindow(Services, containing, attachment, result);

        Pane = new MessageView(ReaderMode.Pane, Services, mainWindow.Commands) { HostWindow = mainWindow };
        registry.Track(Pane.Reader);
        mainWindow.Reader = Pane;

        WireMainCommands(mainWindow.Commands, router);
        // win.change-status pops up the Change Status menu of the issue card
        // on display (window.go).
        mainWindow.Commands.ChangeStatus.Handler = () => Pane.OpenStatusMenu();
        WireReading();
        WireActions();
    }

    /// <summary>The reader's services.</summary>
    public ReaderServices Services { get; }

    /// <summary>The main window's message pane.</summary>
    public MessageView Pane { get; }

    /// <summary>Closes the previewer and lets the pane go; the message windows close with the app.</summary>
    public void Dispose()
    {
        Services.Preview.Close();
        Pane.Close();
    }

    // A toast where the click was (macOS windowToast).
    private void ToastIn(object? window, string text) => Services.ToastIn(window, text);

    // window.go registerActions: the command row, its menu and the keys of
    // the main window act on the list's selection.
    private static void WireMainCommands(WindowCommands c, MessageActionRouter router)
    {
        c.Reply.Handler = router.Reply;
        c.ReplyAll.Handler = router.ReplyAll;
        c.Forward.Handler = router.Forward;
        c.Trash.Handler = router.Trash;
        c.Junk.Handler = router.Junk;
        c.Archive.Handler = router.Archive;
        c.ToggleFlag.Handler = router.ToggleFlag;
        c.MarkRead.Handler = router.MarkRead;
        c.MarkUnread.Handler = router.MarkUnread;
        c.LoadImages.Handler = router.LoadImages;
        c.TrustSender.Handler = router.TrustSender;
    }

    // window.go: the selection drives the pane, activation opens a window
    // (a draft in the compose window), an outbox change fetches every view
    // of that account's outbox again (outbox.go refreshOutboxViews), and the
    // pane's placeholder follows account.list (emptyPageName).
    private void WireReading()
    {
        var list = integration.List;
        var registry = Services.Windows;
        list.SelectedMessageChanged += (_, s) =>
        {
            if (s is null)
            {
                Pane.Reader.Clear();
            }
            else
            {
                Pane.Reader.Show(s);
            }
        };
        list.ActivateMessage += (_, s) => registry.OpenMessage(s);
        list.ActivateDraft += (_, s) => integration.Actions.OpenDraft(s.Id);
        list.OutboxRefreshed += (_, account) => RefetchOutbox(account);
        integration.Mailbox.AccountsLoaded += (_, accounts) => Pane.Reader.SetHasAccounts(accounts.Count > 0);
        integration.Cache.MessageLoaded += (_, e) => registry.ShowLoaded(e.Id, e.Loaded);
        integration.Cache.RemoteBarChanged += (_, e) => registry.RefreshRemoteBar(e.Id, e.Loaded);
        // download.go refreshChips: a download began to show its spinner or
        // ended.
        integration.Cache.ChipsChanged += (_, e) => registry.RefreshChips(e.Id, e.Loaded);
    }

    // MessageActionsController.installHooks: the message windows follow the
    // actions (a moved message's window closes, the stars and the seen state
    // follow, the outbox banner shows the new delivery state).
    private void WireActions()
    {
        var actions = integration.Actions;
        var registry = Services.Windows;
        actions.OpenMessageWindowRequested += (_, s) => registry.OpenMessage(s);
        actions.WindowsClose += (_, id) => registry.CloseMessage(id);
        actions.StarChanged += (_, _) => registry.RefreshActions();
        actions.SeenChanged += (_, _) => registry.RefreshActions();
        actions.OutboxStateChanged += (_, id) => registry.ShowOutboxState(id);
    }

    // Integration.swift list.onOutboxRefreshed: every view showing a message
    // of the account's outbox fetches its message again; the fan-out renders
    // the new delivery state.
    private void RefetchOutbox(AccountId account)
    {
        var model = integration.Mailbox.Model;
        foreach (var current in Services.Windows.Readers.Select(v => v.Current).OfType<MessageSummary>().ToList())
        {
            if (current.AccountId == account && model.InOutbox(current))
            {
                integration.Cache.Refetch(current, static _ => { });
            }
        }
    }
}
