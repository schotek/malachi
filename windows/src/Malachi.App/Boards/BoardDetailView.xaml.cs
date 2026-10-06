// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Board/BoardDetailViewController.swift
// (render, rebuildKey, rebuild, headerLine, whyBox, titleBlock,
// deadlineLine, summaryBox, tasksBlock, the bar's actions, the focus kept
// across a rebuild); GTK: window/board_detail.go (renderDetail,
// renderDetailHeader, renderDetailTop, renderDetailDue,
// renderDetailSummary, renderDetailCommitments) and boardDetailMenu
// (window/board_actions.go).
//
// Upper is filled anew from the detail whenever it differs from what is
// shown, except in what only the reply slot and the conversation show
// (RebuildKey): an autosave of the suggested reply or a new message in the
// conversation fill nothing. Upper's controls stay the same objects (WinUI
// fills them where AppKit replaces its views), so the keyboard stays where
// it was unless what had it hid; then the state pill takes it, as macOS's
// restore. ReplySlot and ConversationSlot hold parts (IBoardDetailPart)
// that follow the case in place; this view hands them every detail.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.App.Localization;
using Malachi.Core.Boards;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;

namespace Malachi.App.Boards;

/// <summary>The detail of the board's selected case.</summary>
public sealed partial class BoardDetailView : UserControl
{
    // Windows-only string: the separator of a line's parts, as board.go's " · ".
    private const string Separator = " · ";

    private BoardActions? actions;
    private IBoardDetailPart? conversationPart;
    private IBoardDetailPart? replyPart;

    // The detail as last applied (the bar's buttons act on its case), and
    // Upper as filled: the detail without what only the slots show, whether
    // the "why" box was open, the case's commitments.
    private Board.Detail? shown;
    private Board.Detail? built;
    private bool builtWhy;
    private IReadOnlyList<Board.CommitmentRow> builtCommitments = [];
    private bool inPanel;

    /// <summary>An empty detail; <see cref="Attach"/> connects it.</summary>
    public BoardDetailView()
    {
        InitializeComponent();
        MnemonicLabel.Apply(CloseButton, Board.Text.Close);
        RemindButton.Content = Board.Text.Remind;
        ArchiveButton.Content = Board.Text.Archive;
        WhyLink.Content = Board.Text.WhyLink;
        UnstarLink.Content = Board.Text.Unstar;
        SummaryHeading.Text = Board.Text.SummaryHeading;
        TasksHeading.Text = Board.Text.TasksHeading;
        CommitmentsHeading.Text = Board.Text.FromAssistant;
        var more = L10n.T("More Actions");
        AutomationProperties.SetName(MoreButton, more);
        ToolTipService.SetToolTip(MoreButton, more);
        AutomationProperties.SetName(StateButton, Board.Text.StateLabel);
        ToolTipService.SetToolTip(StateButton, Board.Text.StateLabel);
        Visibility = Visibility.Collapsed;
    }

    /// <summary>The panel's Close button: the page clears the selection.</summary>
    public event EventHandler? CloseRequested;

    /// <summary>
    /// Where the detail is: the sliding panel (true: Close shows) or the
    /// List's pane beside the list.
    /// </summary>
    public bool InPanel
    {
        get => inPanel;
        set
        {
            inPanel = value;
            CloseButton.Visibility = value ? Visibility.Visible : Visibility.Collapsed;
        }
    }

    /// <summary>
    /// The conversation under the suggested reply: the plain-text excerpts
    /// until the HTML cards replace them (BoardConversationBlock).
    /// </summary>
    public IBoardDetailPart? ConversationPart
    {
        get => conversationPart;
        set
        {
            conversationPart = value;
            ConversationSlot.Content = value?.View;
            value?.Apply(shown);
        }
    }

    /// <summary>
    /// The suggested reply: the samples' static block until stage 5's inline
    /// editor and Suggest Reply replace it (BoardReplyEditorHost).
    /// </summary>
    public IBoardDetailPart? ReplyPart
    {
        get => replyPart;
        set
        {
            replyPart = value;
            ReplySlot.Content = value?.View;
            value?.Apply(shown);
        }
    }

    /// <summary>The case the detail shows, as last applied.</summary>
    public Board.Detail? Shown => shown;

    private BoardController? Controller => actions?.Controller;

    /// <summary>Connects the detail to the case actions and shows the selection.</summary>
    public void Attach(BoardActions boardActions)
    {
        ArgumentNullException.ThrowIfNull(boardActions);
        actions = boardActions;
        ConversationPart = new BoardConversationExcerpts(boardActions.Controller);
        ReplyPart = new BoardSampleReply(boardActions);
        Render();
    }

    /// <summary>
    /// Brings the detail up to date: another case or the "why" box
    /// (Selection), the case itself (Content).
    /// </summary>
    public void Apply(BoardController.Changes changes)
    {
        if ((changes & (BoardController.Changes.Selection | BoardController.Changes.Content)) != 0)
        {
            Render();
        }
    }

    /// <summary>Gives the keyboard to the state pill, the first control worth it; false without a case.</summary>
    public bool FocusContent() => shown is not null && StateButton.Focus(FocusState.Programmatic);

    /// <summary>Renders the controller's detail.</summary>
    public void Render()
    {
        if (Controller is not { } controller)
        {
            return;
        }
        var d = controller.View.Detail;
        var why = controller.State.RevealsWhy;
        shown = d;
        Visibility = d is null ? Visibility.Collapsed : Visibility.Visible;
        IReadOnlyList<Board.CommitmentRow> commitments = d is null
            ? []
            : [.. controller.View.Commitments.Where(k => k.CaseId == d.Id)];
        var key = d is null ? null : RebuildKey(d);
        if (key != built || why != builtWhy || !commitments.SequenceEqual(builtCommitments))
        {
            var sameCase = d?.Id == built?.Id;
            built = key;
            builtWhy = why;
            builtCommitments = commitments;
            var hadFocus = FocusIn(Upper);
            if (d is not null)
            {
                Fill(d, why, commitments);
            }
            if (hadFocus && !FocusIn(Upper) && d is not null)
            {
                StateButton.Focus(FocusState.Programmatic);
            }
            if (!sameCase)
            {
                Scroll.ChangeView(null, 0, null, disableAnimation: true);
            }
        }
        UpdateBar(d);
        replyPart?.Apply(d);
        conversationPart?.Apply(d);
    }

    // The detail as far as Upper shows it: the suggested reply's text is
    // the reply slot's and the conversation the conversation slot's.
    private static Board.Detail RebuildKey(Board.Detail d) => d with
    {
        Draft = "",
        ConversationTitle = "",
        Messages = [],
        MessagesLoading = false,
        MessagesNote = "",
        MessagesRetry = false,
    };

    private void UpdateBar(Board.Detail? d)
    {
        ActionBar.Visibility = d is null ? Visibility.Collapsed : Visibility.Visible;
        if (d is null || actions is null)
        {
            return;
        }
        DoneButton.Content = d.IsDone ? Board.Text.NotDone : Board.Text.Done;
        ArchiveButton.IsEnabled = actions.CanArchive(d.Id);
        ReplyButton.Content = actions.ReplyLabel(d.AccountId);
        ReplyButton.IsEnabled = actions.CanReply(d.Id);
    }

    private void Fill(Board.Detail d, bool why, IReadOnlyList<Board.CommitmentRow> commitments)
    {
        AutomationProperties.SetName(this, d.SpokenTitle);

        // The header line. A done case has no Move To (as its context
        // menu): a new state would not take it out of Done.
        StatePill.State = d.State;
        StatePill.Text = d.StateTitle;
        StateButton.IsEnabled = !d.IsDone;
        Show(RemindText, d.RemindText);
        AccountText.Text = d.Account;
        ToolTipService.SetToolTip(AccountTag, d.Account.Length > 0 ? d.Account : null);
        AccountTag.Visibility = Vis(d.Account.Length > 0);
        if (d.Issue is { } issue)
        {
            IssueTag.StatusStyle = issue.Style;
            IssueTag.Text = issue.Key.Length == 0 ? issue.Status : issue.Status.Length == 0 ? issue.Key : issue.Key + " " + issue.Status;
        }
        else
        {
            IssueTag.Text = "";
        }
        UnstarLink.Visibility = Vis(d.CanUnstar);

        // Why is this here?
        WhyBox.Visibility = Vis(why);
        WhyMark.Text = d.WhyIsAssistant ? Board.Text.AssistantMark : "";
        WhyMark.Visibility = Vis(d.WhyIsAssistant);
        WhyText.Text = d.Why;
        AutomationProperties.SetName(WhyText, d.WhyIsAssistant ? Board.Text.SpokenAssistant(d.Why) : "");
        Show(SourceText, d.SourceText);

        // The title: who and when above it, the subject under an assistant's.
        Show(MetaText, string.Join(Separator, new[] { d.Person, d.Time }.Where(s => s.Length > 0)));
        TitleMark.Text = d.TitleIsAssistant ? Board.Text.AssistantMark : "";
        TitleMark.Visibility = Vis(d.TitleIsAssistant);
        TitleText.Text = d.Title;
        AutomationProperties.SetName(TitleText, d.SpokenTitle);
        Show(SubjectText, d.Subject);

        // The deadline with its quote on a line of its own.
        DeadlineBlock.Visibility = Vis(d.Due.Length > 0);
        DueText.Text = d.Due;
        Show(DueQuoteText, d.DueQuote.Length > 0 ? Board.Text.Quoted(d.DueQuote) : "");

        // Where the summary would be, quietly: the notes no longer count.
        Show(StaleText, d.StaleNote);
        SummaryBox.Visibility = Vis(d.Summary.Length > 0);
        SummaryText.Text = d.Summary;
        TasksBlock.Visibility = Vis(d.Tasks.Count > 0);
        TasksList.ItemsSource = d.Tasks.ToList();

        CommitmentsBlock.Visibility = Vis(commitments.Count > 0);
        CommitmentsList.Children.Clear();
        foreach (var k in commitments)
        {
            CommitmentsList.Children.Add(new BoardCommitmentView
            {
                Commitment = k,
                OnTick = (id, done) => Controller?.SetCommitmentDone(id, done),
            });
        }
    }

    private static void Show(TextBlock block, string text)
    {
        block.Text = text;
        block.Visibility = Vis(text.Length > 0);
    }

    private static Visibility Vis(bool on) => on ? Visibility.Visible : Visibility.Collapsed;

    // Whether the keyboard is in container.
    private bool FocusIn(DependencyObject container)
    {
        if (XamlRoot is null)
        {
            return false;
        }
        for (var d = FocusManager.GetFocusedElement(XamlRoot) as DependencyObject; d is not null; d = VisualTreeHelper.GetParent(d))
        {
            if (ReferenceEquals(d, container))
            {
                return true;
            }
        }
        return false;
    }

    private void OnCloseClick(object sender, RoutedEventArgs e) => CloseRequested?.Invoke(this, EventArgs.Empty);

    private void OnDoneClick(object sender, RoutedEventArgs e)
    {
        if (shown is { } d)
        {
            actions?.ToggleDone(d.Id);
        }
    }

    private void OnArchiveClick(object sender, RoutedEventArgs e)
    {
        if (shown is { } d)
        {
            actions?.Archive(d.Id);
        }
    }

    private void OnReplyClick(object sender, RoutedEventArgs e)
    {
        if (shown is { } d)
        {
            actions?.Reply(d.Id);
        }
    }

    private void OnWhyClick(object sender, RoutedEventArgs e) => Controller?.ToggleWhy();

    private void OnUnstarClick(object sender, RoutedEventArgs e)
    {
        if (shown is { } d)
        {
            actions?.Unstar(d.Id);
        }
    }

    // Remind…'s presets for now (the date moves on), built as the menu opens.
    private void OnRemindOpening(object? sender, object e)
    {
        if (shown is { } d && actions is not null)
        {
            actions.FillRemindMenu(RemindMenu.Items, d.Id);
        }
    }

    private void OnStateMenuOpening(object? sender, object e)
    {
        if (shown is { } d && actions is not null)
        {
            actions.FillStateMenu(StateMenu.Items, d.Id);
        }
    }

    // GTK's boardDetailMenu: Show in Mail; Unstar too, as the link beside
    // Why is this here?.
    private void OnMoreOpening(object? sender, object e)
    {
        MoreMenu.Items.Clear();
        if (shown is not { } d || actions is null)
        {
            return;
        }
        var id = d.Id;
        MoreMenu.Items.Add(BoardMenuItem.Make(
            Board.Text.ShowInMail, () => actions.ShowInMail(id), actions.CanShowInMail(id), "BoardShowInMail"));
        if (actions.CanUnstar(id))
        {
            MoreMenu.Items.Add(BoardMenuItem.Make(Board.Text.Unstar, () => actions.Unstar(id), automationId: "BoardMenuUnstar"));
        }
    }
}
