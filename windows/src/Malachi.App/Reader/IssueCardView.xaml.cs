// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/window/issue_card.go (issueCard.show, setBusy,
// popupStatus, openKey; transitionMenu: load, line, itemButton); macOS:
// MessageView/IssueCardView.swift and App/ChangeStatusMenus.swift. The view
// of Core's IssueCardState (IssueCardView.xaml). The Change Status menu is
// a MenuFlyout whose items are built when it opens: "Loading…" while
// issue.transitions runs (IssueActionsController.LoadTransitions), then one
// item per transition, the status it leads to (or, disabled, why it cannot
// be chosen here) on its right where a menu shows an accelerator, the
// failure or "no status change" as one disabled line; choosing an item
// performs it after the menu closed. Closing the menu drops a late answer.
// Every title is text from the site, plain.

using System;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Presentation;
using Malachi.Core.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Reader;

/// <summary>The issue card over the headers of a Jira message.</summary>
public sealed partial class IssueCardView : UserControl
{
    /// <summary>What the card shows; null hides it.</summary>
    public static readonly DependencyProperty StateProperty = DependencyProperty.Register(
        nameof(State), typeof(IssueCardState), typeof(IssueCardView), new PropertyMetadata(null, OnStateChanged));

    // What the open menu shows: the subject it was loaded for and the issue
    // issue.transitions named (the spinner's key when an item is chosen).
    private IssueActionsController.Subject? loadedFor;
    private IssueInfo? loadedIssue;

    /// <summary>A hidden card.</summary>
    public IssueCardView()
    {
        InitializeComponent();
        StatusMenu.Opening += (_, _) => LoadMenu();
        StatusMenu.Closed += (_, _) => Issues?.CancelLoad();
        Apply();
    }

    /// <summary>What the card shows; null hides it.</summary>
    public IssueCardState? State
    {
        get => (IssueCardState?)GetValue(StateProperty);
        set => SetValue(StateProperty, value);
    }

    /// <summary>The Change Status menus' controller of the window.</summary>
    public IssueActionsController? Issues { get; set; }

    /// <summary>The message the menu acts on when it opens (the one on display); null when there is none.</summary>
    public Func<IssueActionsController.Subject?>? Subject { get; set; }

    /// <summary>Opens the issue's URL in the browser (the key's click; only a URL of the account's own site gets here).</summary>
    public Action<string>? OpenIssue { get; set; }

    /// <summary>
    /// win.change-status (issue_card.go <c>popupStatus</c>): opens the Change
    /// Status menu, when the card offers it. False when it does not.
    /// </summary>
    public bool OpenStatusMenu()
    {
        if (State is not { Menu: true, Busy: false } || Visibility != Visibility.Visible)
        {
            return false;
        }
        StatusMenu.ShowAt(StatusButton);
        return true;
    }

    private static void OnStateChanged(DependencyObject d, DependencyPropertyChangedEventArgs e) => ((IssueCardView)d).Apply();

    // issueCard.show and setBusy.
    private void Apply()
    {
        if (State is not { } st)
        {
            Visibility = Visibility.Collapsed;
            return;
        }
        Visibility = Visibility.Visible;
        var card = st.Card;
        KeyButtonText.Text = card.Key;
        KeyButton.Visibility = card.Key.Length > 0 && st.Openable ? Visibility.Visible : Visibility.Collapsed;
        ToolTipService.SetToolTip(KeyButton, card.OpenTooltip.Length > 0 ? card.OpenTooltip : null);
        AutomationProperties.SetName(KeyButton, card.OpenTooltip.Length > 0 ? card.OpenTooltip : card.Key);
        KeyLabel.Text = card.Key;
        KeyLabel.Visibility = card.Key.Length > 0 && !st.Openable ? Visibility.Visible : Visibility.Collapsed;

        StatusButton.Visibility = st.Menu ? Visibility.Visible : Visibility.Collapsed;
        StatusText.Text = card.Status;
        var changeStatus = Jira.ChangeStatusLabel();
        ToolTipService.SetToolTip(StatusButton, changeStatus);
        // Named by its status, what it does said after it (no sentence is put
        // together from two).
        AutomationProperties.SetName(StatusButton, card.Status.Length > 0 ? card.Status : changeStatus);
        AutomationProperties.SetHelpText(StatusButton, changeStatus);
        VisualStateManager.GoToState(this, card.StatusStyle switch
        {
            JiraStatusStyle.InProgress => "InProgress",
            JiraStatusStyle.Done => "Done",
            _ => "Plain",
        }, false);
        StatusSpinner.IsActive = st.Busy;
        StatusSpinner.Visibility = st.Busy ? Visibility.Visible : Visibility.Collapsed;
        StatusButton.IsEnabled = !st.Busy;
        PlainStatus.Text = st.Menu ? "" : card.Status;
        PlainStatus.StatusStyle = card.StatusStyle;

        InternalPill.Text = card.Internal ? card.InternalLabel : "";
        ViaLabel.Text = card.Via;
        ViaLabel.Visibility = card.Via.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        EditedLabel.Text = card.Edited;
        EditedLabel.Visibility = card.Edited.Length > 0 ? Visibility.Visible : Visibility.Collapsed;

        Fields.Children.Clear();
        foreach (var row in card.Rows)
        {
            var pair = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 4 };
            pair.Children.Add(new TextBlock { Text = row.Label, Style = (Style)Application.Current.Resources["DimCaptionLabelStyle"] });
            var value = new TextBlock
            {
                Text = row.Value,
                MaxWidth = 200,
                MaxLines = 1,
                TextTrimming = TextTrimming.CharacterEllipsis,
                Style = (Style)Application.Current.Resources[row.Missing ? "DimCaptionLabelStyle" : "CaptionLabelStyle"],
            };
            if (!row.Missing)
            {
                ToolTipService.SetToolTip(value, row.Value);
            }
            pair.Children.Add(value);
            Fields.Children.Add(pair);
        }
    }

    // openKey: only a URL of the account's own site (IssueCardState.Openable),
    // since it comes from the site and is hostile input like mail.
    private void OnKeyClick(object sender, RoutedEventArgs e)
    {
        if (State is { Openable: true } st && st.Card.Url.Length > 0)
        {
            OpenIssue?.Invoke(st.Card.Url);
        }
    }

    // transitionMenu.load: the items for the message the subject names now.
    private void LoadMenu()
    {
        StatusMenu.Items.Clear();
        loadedFor = null;
        loadedIssue = null;
        if (Subject?.Invoke() is not { } subject || Issues is not { } issues)
        {
            Line(Jira.NoTransitions());
            return;
        }
        Line(Jira.TransitionsLoading());
        var started = issues.LoadTransitions(subject, outcome =>
        {
            StatusMenu.Items.Clear();
            if (!outcome.TryGetValue(out var loaded, out var error))
            {
                Line(RpcErrorText.Text(Jira.LoadTransitionsAction(), error));
                return;
            }
            loadedFor = subject;
            loadedIssue = loaded.Issue;
            if (loaded.Items.Count == 0)
            {
                Line(Jira.NoTransitions());
                return;
            }
            foreach (var item in loaded.Items)
            {
                StatusMenu.Items.Add(ItemFor(item));
            }
        });
        if (!started)
        {
            StatusMenu.Items.Clear();
            Line(Jira.NoTransitions());
        }
    }

    // transitionMenu.line: one disabled line of text (loading, none, the failure).
    private void Line(string text) => StatusMenu.Items.Add(new MenuFlyoutItem { Text = text, IsEnabled = false });

    // transitionMenu.itemButton: a transition's name, the status it leads to
    // or why it is disabled beside it; chosen, it is performed once the menu
    // closed.
    private MenuFlyoutItem ItemFor(JiraTransitionItem item)
    {
        var side = item.Enabled ? item.Subtitle : item.Hint;
        var m = new MenuFlyoutItem
        {
            Text = item.Title,
            IsEnabled = item.Enabled,
            KeyboardAcceleratorTextOverride = side,
        };
        if (!item.Enabled && item.Hint.Length > 0)
        {
            ToolTipService.SetToolTip(m, item.Hint);
        }
        if (side.Length > 0)
        {
            AutomationProperties.SetHelpText(m, side);
        }
        m.Click += (_, _) =>
        {
            if (loadedFor is not { } subject || loadedIssue is not { } issue)
            {
                return;
            }
            // After the menu closed: its closing drops nothing a perform needs.
            DispatcherQueue.TryEnqueue(() => Issues?.Perform(subject, item, issue));
        };
        return m;
    }
}
