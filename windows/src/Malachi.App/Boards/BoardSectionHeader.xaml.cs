// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Board/BoardViews.swift
// (BoardSectionHeaderView.configure, configureCommitments); GTK:
// boardSectionHeader (window/board_list.go). The group header of the List
// style's ListView (BoardListView.xaml's GroupStyle).

using System.ComponentModel;
using Malachi.Core.Boards;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Boards;

/// <summary>The header of a section of the board's list.</summary>
public sealed partial class BoardSectionHeader : UserControl
{
    /// <summary>The group it heads.</summary>
    public static readonly DependencyProperty GroupProperty = DependencyProperty.Register(
        nameof(Group), typeof(BoardListGroup), typeof(BoardSectionHeader), new PropertyMetadata(null, OnGroupChanged));

    /// <summary>An empty header.</summary>
    public BoardSectionHeader()
    {
        InitializeComponent();
        Loaded += (_, _) => Apply();
    }

    /// <summary>The group it heads.</summary>
    public BoardListGroup? Group
    {
        get => (BoardListGroup?)GetValue(GroupProperty);
        set => SetValue(GroupProperty, value);
    }

    // The group updates in place (its count, its title): the header follows.
    private static void OnGroupChanged(DependencyObject d, DependencyPropertyChangedEventArgs e)
    {
        var header = (BoardSectionHeader)d;
        if (e.OldValue is INotifyPropertyChanged old)
        {
            old.PropertyChanged -= header.OnGroupPropertyChanged;
        }
        if (e.NewValue is INotifyPropertyChanged next)
        {
            next.PropertyChanged += header.OnGroupPropertyChanged;
        }
        header.Apply();
    }

    private void OnGroupPropertyChanged(object? sender, PropertyChangedEventArgs e) => Apply();

    private void Apply()
    {
        if (Group is not { } g)
        {
            return;
        }
        TitleText.Text = g.Title;
        CountText.Text = g.CountText;
        AutomationProperties.SetName(this, g.Spoken);
        var state = g.Kind switch
        {
            BoardListGroupKind.Commitments => "Assistant",
            BoardListGroupKind.State when g.State != Board.State.Info => g.State.ToString(),
            _ => "Plain",
        };
        VisualStateManager.GoToState(this, state, false);
    }
}
