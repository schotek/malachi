// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/window/status.go (statusRow, newStatusRow,
// statusRow.apply); macOS: MainWindow/StatusPopoverViewController.swift
// (StatusRowViews), which keeps this in AppKit and Windows in Core
// (docs/windows-port.md §7.4). One account's part of the status flyout: the
// account's row with its action (the check icon, or a labelled button for
// Try Again, the sign-in and Edit Account…; at most one shown), and the row
// that leads to its unsent messages, hidden while there are none. The
// buttons change only when the action does, so one that has the keyboard
// focus keeps it while the detail moves (progress arrives every half
// second). The name is the user's, the detail may carry the backend's
// message: plain text.

using CommunityToolkit.Mvvm.ComponentModel;
using Malachi.Core.Api;
using Malachi.Core.Model;

namespace Malachi.Core.Presentation;

/// <summary>One account's rows in the status flyout.</summary>
public sealed partial class StatusPopoverRow : ObservableObject
{
    /// <summary>The rows of <paramref name="account"/>; <see cref="Apply"/> fills them.</summary>
    public StatusPopoverRow(AccountId account)
    {
        Account = account;
        Title = "";
        Detail = "";
        ButtonLabel = "";
        FailedText = "";
    }

    /// <summary>The account.</summary>
    public AccountId Account { get; }

    /// <summary>What the rows show; null until the first <see cref="Apply"/>.</summary>
    public AccountStatus? Status { get; private set; }

    /// <summary>The account's name.</summary>
    [ObservableProperty]
    public partial string Title { get; private set; }

    /// <summary>The account's state, one whole sentence.</summary>
    [ObservableProperty]
    public partial string Detail { get; private set; }

    /// <summary>The check icon (<see cref="StatusAction.Check"/>).</summary>
    [ObservableProperty]
    public partial bool CheckVisible { get; private set; }

    /// <summary>The labelled button's text, with its GTK mnemonic marker when <see cref="ButtonMnemonic"/>.</summary>
    [ObservableProperty]
    public partial string ButtonLabel { get; private set; }

    /// <summary>Whether <see cref="ButtonLabel"/> carries a mnemonic ("_Edit Account…").</summary>
    [ObservableProperty]
    public partial bool ButtonMnemonic { get; private set; }

    /// <summary>The labelled button is shown.</summary>
    [ObservableProperty]
    public partial bool ButtonVisible { get; private set; }

    /// <summary>"%d messages not sent".</summary>
    [ObservableProperty]
    public partial string FailedText { get; private set; }

    /// <summary>The unsent messages' row is shown.</summary>
    [ObservableProperty]
    public partial bool FailedVisible { get; private set; }

    /// <summary>The unsent messages' row leads to the outbox (a paused account's cannot be shown).</summary>
    [ObservableProperty]
    public partial bool FailedActivatable { get; private set; }

    /// <summary>The first account's rows: no line above them in the boxed list.</summary>
    [ObservableProperty]
    public partial bool IsFirst { get; set; }

    /// <summary>
    /// Shows <paramref name="st"/> (status.go <c>statusRow.apply</c>);
    /// <paramref name="outbox"/> says whether the account's outbox can be
    /// shown.
    /// </summary>
    public void Apply(AccountStatus st, bool outbox)
    {
        System.ArgumentNullException.ThrowIfNull(st);
        Title = st.Title;
        Detail = st.Detail;
        if (Status is not { } was || st.Action != was.Action || st.SignIn != was.SignIn || st.Reason != was.Reason)
        {
            CheckVisible = st.Action == StatusAction.Check;
            var label = SyncStatusTexts.StatusButtonLabel(st);
            if (label.Length > 0)
            {
                ButtonMnemonic = SyncStatusTexts.StatusButtonMnemonic(st);
                ButtonLabel = label;
            }
            ButtonVisible = label.Length > 0;
        }
        if (st.Failed > 0)
        {
            FailedText = SyncStatusTexts.NotSentText(st.Failed);
        }
        FailedVisible = st.Failed > 0;
        FailedActivatable = outbox;
        Status = st;
    }
}
