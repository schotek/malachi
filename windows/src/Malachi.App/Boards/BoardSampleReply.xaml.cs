// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Board/BoardDetailViewController.swift
// (draftBox, discardClicked, the .staticDraft slot state); GTK: the
// samples' reply block of window/board_reply_editor.go. Shown for the
// invented samples only, while the case has a suggested reply's text.

using System;
using Malachi.App.Localization;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Boards;

/// <summary>The samples' static suggested reply.</summary>
public sealed partial class BoardSampleReply : UserControl, IBoardDetailPart
{
    private readonly BoardActions actions;
    private BoardCaseId? shown;

    /// <summary>The block over <paramref name="actions"/> (Discard asks them).</summary>
    public BoardSampleReply(BoardActions actions)
    {
        ArgumentNullException.ThrowIfNull(actions);
        this.actions = actions;
        InitializeComponent();
        Heading.Text = Board.Text.DraftHeading;
        NoteText.Text = Board.Text.DraftNote;
        MnemonicLabel.Apply(DiscardButton, Board.Text.Discard);
        Visibility = Visibility.Collapsed;
    }

    /// <inheritdoc/>
    public UIElement View => this;

    /// <inheritdoc/>
    public void Apply(Board.Detail? detail)
    {
        var on = actions.Samples && detail is { Draft.Length: > 0 };
        Visibility = on ? Visibility.Visible : Visibility.Collapsed;
        shown = on ? detail!.Id : (BoardCaseId?)null;
        DraftText.Text = on ? detail!.Draft : "";
    }

    private void OnDiscardClick(object sender, RoutedEventArgs e)
    {
        if (shown is { } id)
        {
            _ = actions.DiscardDraftAsync(id);
        }
    }
}
