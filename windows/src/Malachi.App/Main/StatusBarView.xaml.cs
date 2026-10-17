// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MainWindow/StatusBarViewController.swift
// (show(line), refreshStatusPopover, the popover's actions after it
// closed); GTK: ui/internal/window/sync.go (refreshSyncLabel: the label,
// the spinner, the connection icon, the button's sensitivity, an open
// popover following every change) and status.go (refreshStatusPopover,
// onStatusAction and the unsent row's showOutbox, each after Popdown). See
// StatusBarView.xaml. The line cannot be clicked without a connection or
// without an account (StatusLine.Active), and an open flyout closes then;
// while it is open its rows follow every change of the line. A screen
// reader reads the line itself (window.blp: labelled by sync_label), or
// the button's tooltip while the line is empty (no account), where GTK's
// label would leave the button nameless. In the Calendar mode the line's
// text is the calendars' state and the note leads with the unanswered
// invitations (SetCalendar, macOS setCalendar).

using System;
using Malachi.App.Resources;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Main;

/// <summary>The status line across the window's bottom edge, with its flyout.</summary>
public sealed partial class StatusBarView : UserControl
{
    private readonly StatusPopover popover = new();
    private SyncController? sync;
    private MailboxController? mailbox;
    private bool open;
    private Action? afterClose;
    private StatusLine? shown;
    private string note = "";

    // The Calendar mode's texts (SetCalendar): the calendars' state line in
    // place of the sync line's text, null otherwise, and the unanswered
    // count ahead of the triage's note.
    private string? calendarLine;
    private string calendarNote = "";

    /// <summary>An empty line; <see cref="Attach"/> connects it.</summary>
    public StatusBarView()
    {
        InitializeComponent();
        FlyoutContent.Tag = this;
        AccountRows.ItemsSource = popover.Rows;
    }

    /// <summary>An account's row asked for its action (status.go onStatusAction), after the flyout closed.</summary>
    public event EventHandler<AccountStatus>? ActionRequested;

    /// <summary>An account's unsent messages were chosen (status.go showOutbox), after the flyout closed.</summary>
    public event EventHandler<AccountId>? OutboxRequested;

    /// <summary>Shows <paramref name="syncController"/>'s line and follows it; the flyout's rows come from it and the mailbox's model.</summary>
    public void Attach(SyncController syncController, MailboxController mailboxController)
    {
        ArgumentNullException.ThrowIfNull(syncController);
        ArgumentNullException.ThrowIfNull(mailboxController);
        sync = syncController;
        mailbox = mailboxController;
        syncController.StatusLineChanged += (_, line) => Show(line);
        Show(syncController.Line);
    }

    /// <summary>A row's button: the flyout closes, then its action runs.</summary>
    internal void RunAction(StatusPopoverRow row)
    {
        if (row.Status is not { } st)
        {
            return;
        }
        CloseThen(() => ActionRequested?.Invoke(this, st));
    }

    /// <summary>A row's unsent messages: the flyout closes, then the outbox is shown.</summary>
    internal void ShowOutbox(StatusPopoverRow row)
    {
        var acc = row.Account;
        CloseThen(() => OutboxRequested?.Invoke(this, acc));
    }

    /// <summary>
    /// A note after the line (the board's triage while a run works:
    /// StatusBarViewController.setNote), "" for none; the line keeps its
    /// place and the note gives way first when the width runs out.
    /// </summary>
    public void SetNote(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        if (text == note)
        {
            return;
        }
        note = text;
        if (shown is { } line)
        {
            Show(line);
        }
    }

    /// <summary>
    /// The Calendar mode's texts (macOS StatusBarViewController.setCalendar):
    /// <paramref name="line"/> in place of the sync line's text (null: the
    /// sync line again), <paramref name="calendar"/> (the unanswered count)
    /// ahead of the triage's note ("%s · %s"). The spinner, the connection
    /// glyph and the flyout (the accounts, the Outbox) stay the sync line's.
    /// </summary>
    public void SetCalendar(string? line, string calendar)
    {
        ArgumentNullException.ThrowIfNull(calendar);
        var nextNote = line is null ? "" : calendar;
        if (line == calendarLine && nextNote == calendarNote)
        {
            return;
        }
        calendarLine = line;
        calendarNote = nextNote;
        if (shown is { } current)
        {
            Show(current);
        }
    }

    // sync.go refreshSyncLabel.
    private void Show(StatusLine line)
    {
        shown = line;
        var text = calendarLine ?? line.Text;
        var notes = calendarNote.Length == 0 || note.Length == 0 ? calendarNote + note : Core.Calendars.Cal.Text.Dot(calendarNote, note);
        SyncLabel.Text = text.Length == 0 ? notes : Core.Boards.Board.JoinedNote(text, notes);
        AutomationProperties.SetName(StatusButton, SyncLabel.Text.Length > 0 ? SyncLabel.Text : L10n.T("Sync Status"));
        SyncSpinner.IsActive = line.Spinning;
        SyncSpinner.Visibility = line.Spinning ? Visibility.Visible : Visibility.Collapsed;
        if (line.Icon.Length > 0)
        {
            ConnectionIcon.Glyph = Icons.Glyph(line.Icon);
            ConnectionIcon.FontFamily = Icons.SymbolFont;
        }
        ConnectionIcon.Visibility = line.Icon.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        if (!line.Active && open)
        {
            StatusFlyout.Hide();
        }
        StatusButton.IsEnabled = line.Active;
        popover.Daemon = line.Daemon;
        StatusDaemon.Text = line.Daemon;
        StatusDaemon.Visibility = line.Daemon.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        if (open)
        {
            Refresh();
        }
    }

    // status.go refreshStatusPopover, as it opens and while it is open.
    private void Refresh()
    {
        if (sync is not { } s || mailbox is not { } m)
        {
            return;
        }
        popover.Update(s.AccountStatuses(), acc => m.Model.OutboxKey(acc) is not null);
        StatusAccounts.Visibility = popover.HasRows ? Visibility.Visible : Visibility.Collapsed;
    }

    private void OnFlyoutOpening(object? sender, object e)
    {
        open = true;
        Refresh();
    }

    private void OnFlyoutClosed(object? sender, object e)
    {
        open = false;
        var action = afterClose;
        afterClose = null;
        action?.Invoke();
    }

    private void CloseThen(Action action)
    {
        if (!open)
        {
            action();
            return;
        }
        afterClose = action;
        StatusFlyout.Hide();
    }
}
