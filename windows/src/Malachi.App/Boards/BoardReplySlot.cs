// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the reply slot of macos/Sources/MalachiMail/Board/BoardDetailViewController.swift
// (replySlotChanged, the slot's states: the Suggest control, loading,
// failed with Try Again, the pane with its note and the "could not be saved
// yet" line); GTK: window/board_reply_editor.go (renderDetailReplySlot) and
// window/board_suggest_reply.go (renderSuggestReply, suggestReplyView,
// suggestBoardReply).
//
// The detail's reply part over the daemon's board (IBoardDetailPart; the
// samples keep BoardSampleReply). While the panes have nothing for the
// selected case it shows Suggest Reply (BoardSuggestReplyControl, when the
// reply controller offers it); otherwise a card under the Suggested Reply
// heading: a spinner while the linked draft loads, the failure with Try
// Again, or the live inline editor (ComposePane) with the note that it
// stays on the board until sent and, while what was typed could not be
// saved, ReplyNotSaved. The pane's view is put in only when it is not there
// already, so a refresh of the board (every autosave lists it again) keeps
// its caret and its keyboard. Every text is Core's, as TextBlock.Text.

using System;
using Malachi.App.Compose;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Controllers;
using Microsoft.UI.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;

namespace Malachi.App.Boards;

/// <summary>The reply slot of the board's detail over the daemon's board.</summary>
public sealed partial class BoardReplySlot : UserControl, IBoardDetailPart
{
    private readonly BoardReplyEditorHost host;
    private readonly BoardActions actions;
    private readonly BoardReplyController reply;
    private readonly BoardObserverToken replyToken;
    private readonly BoardSuggestReplyControl suggest = new();
    private readonly Border card;
    private readonly StackPanel loadingRow = new() { Orientation = Orientation.Horizontal, Spacing = 6 };
    private readonly TextBlock failedText = new() { TextWrapping = TextWrapping.Wrap };
    private readonly HyperlinkButton retry = new() { Padding = new Thickness(0) };
    private readonly TextBlock noteText = new() { TextWrapping = TextWrapping.Wrap };
    private readonly TextBlock unsavedText = new() { TextWrapping = TextWrapping.Wrap };
    private readonly Border paneHost = new();
    private Board.Detail? shown;
    private bool applying;
    private bool closed;

    /// <summary>The slot of <paramref name="host"/>'s detail; <paramref name="reply"/> is the application's Suggest Reply.</summary>
    public BoardReplySlot(BoardReplyEditorHost host, BoardActions actions, BoardReplyController reply)
    {
        ArgumentNullException.ThrowIfNull(host);
        ArgumentNullException.ThrowIfNull(actions);
        ArgumentNullException.ThrowIfNull(reply);
        this.host = host;
        this.actions = actions;
        this.reply = reply;
        IsTabStop = false;

        var heading = new TextBlock { Text = Board.Text.DraftHeading, FontWeight = FontWeights.SemiBold };
        AutomationProperties.SetHeadingLevel(heading, Microsoft.UI.Xaml.Automation.Peers.AutomationHeadingLevel.Level3);
        loadingRow.Children.Add(new ProgressRing { IsActive = true, Width = 16, Height = 16, VerticalAlignment = VerticalAlignment.Center });
        loadingRow.Children.Add(new TextBlock { Text = Board.Text.ReplyLoading, VerticalAlignment = VerticalAlignment.Center });
        failedText.Text = Board.Text.ReplyLoadFailed;
        retry.Content = Board.Text.TryAgain;
        AutomationProperties.SetAutomationId(retry, "BoardReplyRetry");
        retry.Click += (_, _) => host.Retry();
        noteText.Text = Board.Text.DraftNote;
        noteText.FontSize = 12;
        noteText.Foreground = Brush("TextFillColorSecondaryBrush");
        unsavedText.Text = Board.Text.ReplyNotSaved;
        unsavedText.Foreground = Brush("SystemFillColorCautionBrush");
        AutomationProperties.SetAutomationId(paneHost, "BoardReplyEditor");
        var column = new StackPanel { Spacing = 6 };
        column.Children.Add(heading);
        column.Children.Add(loadingRow);
        column.Children.Add(failedText);
        column.Children.Add(retry);
        column.Children.Add(noteText);
        column.Children.Add(unsavedText);
        column.Children.Add(paneHost);
        card = new Border { Style = (Style)Application.Current.Resources["BoardBoxStyle"], Child = column };

        suggest.Suggest += (_, text) => Suggest(text);
        suggest.Stop += (_, _) =>
        {
            if (suggest.ShownView.Running)
            {
                reply.Cancel();
            }
        };
        suggest.KeyboardLost += (_, _) => KeyboardLost?.Invoke(this, EventArgs.Empty);

        var root = new Grid();
        root.Children.Add(card);
        root.Children.Add(suggest);
        Content = root;
        replyToken = reply.Observe(Render);
        Visibility = Visibility.Collapsed;
    }

    /// <summary>Suggest Reply gave up the keyboard as it went disabled: the detail's Reply takes it.</summary>
    public event EventHandler? KeyboardLost;

    /// <inheritdoc/>
    public UIElement View => this;

    /// <inheritdoc/>
    public void Apply(Board.Detail? detail)
    {
        shown = detail;
        applying = true;
        try
        {
            // Core hears of the selection first: the slot then shows what
            // its rules decided.
            host.Update();
        }
        finally
        {
            applying = false;
        }
        Render();
    }

    /// <summary>Shows what the slot holds for the case shown now (the panes changed, the request did).</summary>
    public void Render()
    {
        if (applying || closed)
        {
            return;
        }
        if (shown is not { } d)
        {
            Show(showCard: false, suggestShown: false);
            return;
        }
        switch (host.SlotFor(d.Id))
        {
            case BoardReplyPanes.Slot.Editor e:
                var pane = BoardReplyEditorHost.View(e.Pane);
                if (!ReferenceEquals(paneHost.Child, pane))
                {
                    // A parked pane may still sit where it was.
                    Remove(pane);
                    paneHost.Child = pane;
                }
                SetCard(loading: false, failed: false, retryOffered: false, editor: true, unsaved: e.Unsaved);
                Show(showCard: true, suggestShown: false);
                return;
            case BoardReplyPanes.Slot.Loading:
                paneHost.Child = null;
                SetCard(loading: true, failed: false, retryOffered: false, editor: false, unsaved: false);
                Show(showCard: true, suggestShown: false);
                return;
            case BoardReplyPanes.Slot.Failed f:
                paneHost.Child = null;
                SetCard(loading: false, failed: true, retryOffered: f.Retry, editor: false, unsaved: false);
                Show(showCard: true, suggestShown: false);
                return;
        }
        paneHost.Child = null;
        var v = SuggestView(d.Id);
        if (v.Shown)
        {
            suggest.Apply(v, d.Id);
        }
        Show(showCard: false, suggestShown: v.Shown);
    }

    /// <summary>Takes <paramref name="pane"/> out of the slot, if it is there (the panes let it go).</summary>
    internal void Remove(ComposePane pane)
    {
        if (ReferenceEquals(paneHost.Child, pane))
        {
            paneHost.Child = null;
        }
        else if (VisualTreeHelper.GetParent(pane) is Border other)
        {
            other.Child = null;
        }
    }

    /// <summary>The window is gone: the slot stops listening to the request.</summary>
    internal void Close()
    {
        closed = true;
        replyToken.Cancel();
        paneHost.Child = null;
    }

    private Board.SuggestReplyView SuggestView(BoardCaseId id)
    {
        var snapshot = actions.Controller.Source.Snapshot;
        return host.Suspended || snapshot.FindCase(id) is not { } c
            ? Board.SuggestReplyView.Hidden
            : reply.View(c, snapshot, samples: actions.Samples);
    }

    // suggestBoardReply: the view is asked again, also when Enter came just
    // as a refresh of the board removed or changed the case.
    private void Suggest(string instruction)
    {
        if (shown is not { } d)
        {
            return;
        }
        var v = SuggestView(d.Id);
        if (!v.Shown || !v.Enabled || v.Running)
        {
            return;
        }
        if (actions.Controller.Source.Snapshot.FindCase(d.Id) is { } c)
        {
            reply.Start(c, instruction);
        }
    }

    private void SetCard(bool loading, bool failed, bool retryOffered, bool editor, bool unsaved)
    {
        loadingRow.Visibility = Vis(loading);
        failedText.Visibility = Vis(failed);
        retry.Visibility = Vis(failed && retryOffered);
        noteText.Visibility = Vis(editor);
        unsavedText.Visibility = Vis(editor && unsaved);
        paneHost.Visibility = Vis(editor);
    }

    private void Show(bool showCard, bool suggestShown)
    {
        card.Visibility = Vis(showCard);
        suggest.Visibility = Vis(suggestShown);
        Visibility = Vis(showCard || suggestShown);
    }

    private static Visibility Vis(bool on) => on ? Visibility.Visible : Visibility.Collapsed;

    private static Brush Brush(string key) => (Brush)Application.Current.Resources[key];
}
