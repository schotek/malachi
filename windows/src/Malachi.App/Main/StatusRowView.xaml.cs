// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/window/status.go (newStatusRow's clicked handlers,
// statusRow.apply's label with its mnemonic); macOS:
// StatusPopoverViewController.swift (StatusRowViews.actionClicked,
// StatusLinkRow). The view of Core's StatusPopoverRow (StatusRowView.xaml);
// its clicks go to the status bar, which closes the flyout first.

using System.ComponentModel;
using Malachi.App.Localization;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;

namespace Malachi.App.Main;

/// <summary>One account's rows in the status flyout.</summary>
public sealed partial class StatusRowView : UserControl
{
    /// <summary>The rows it shows.</summary>
    public static readonly DependencyProperty RowProperty = DependencyProperty.Register(
        nameof(Row), typeof(StatusPopoverRow), typeof(StatusRowView), new PropertyMetadata(null, OnRowChanged));

    /// <summary>Empty rows.</summary>
    public StatusRowView()
    {
        InitializeComponent();
    }

    /// <summary>The rows it shows.</summary>
    public StatusPopoverRow? Row
    {
        get => (StatusPopoverRow?)GetValue(RowProperty);
        set => SetValue(RowProperty, value);
    }

    private static void OnRowChanged(DependencyObject d, DependencyPropertyChangedEventArgs e)
    {
        var view = (StatusRowView)d;
        if (e.OldValue is StatusPopoverRow old)
        {
            old.PropertyChanged -= view.OnRowPropertyChanged;
        }
        if (e.NewValue is StatusPopoverRow row)
        {
            row.PropertyChanged += view.OnRowPropertyChanged;
        }
        view.ShowLabel();
    }

    private void OnRowPropertyChanged(object? sender, PropertyChangedEventArgs e)
    {
        if (e.PropertyName is nameof(StatusPopoverRow.ButtonLabel) or nameof(StatusPopoverRow.ButtonMnemonic))
        {
            ShowLabel();
        }
    }

    // statusRow.apply: "_Edit Account…" keeps its access key; the other
    // labels are shown as they are.
    private void ShowLabel()
    {
        if (Row is not { } row)
        {
            return;
        }
        if (row.ButtonMnemonic)
        {
            MnemonicLabel.Apply(ActionButton, row.ButtonLabel);
            return;
        }
        ActionButton.Content = row.ButtonLabel;
        ActionButton.AccessKey = "";
    }

    private void OnActionClick(object sender, RoutedEventArgs e)
    {
        if (Row is { } row && FindBar() is { } bar)
        {
            bar.RunAction(row);
        }
    }

    private void OnFailedClick(object sender, RoutedEventArgs e)
    {
        if (Row is { FailedActivatable: true } row && FindBar() is { } bar)
        {
            bar.ShowOutbox(row);
        }
    }

    // The flyout's content is not in the bar's visual tree: its root is
    // the popup, whose owner the flyout knows.
    private StatusBarView? FindBar()
    {
        DependencyObject? d = this;
        while (d is not null)
        {
            if (d is FrameworkElement { Tag: StatusBarView bar })
            {
                return bar;
            }
            d = VisualTreeHelper.GetParent(d);
        }
        return null;
    }
}
