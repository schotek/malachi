// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/widget/pill.go (SetStatusPill, SetInternalPill);
// macOS: Shared/IssuePill.swift (colours, paint). The pill's text and kind
// as dependency properties, for x:Bind from a row or a card; the kind picks
// the visual state (IssuePill.xaml).

using Malachi.Core.IssueTrackers;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Controls;

/// <summary>A status pill or the Internal badge of an issue.</summary>
public sealed partial class IssuePill : UserControl
{
    /// <summary>The pill's text; empty hides it.</summary>
    public static readonly DependencyProperty TextProperty = DependencyProperty.Register(
        nameof(Text), typeof(string), typeof(IssuePill), new PropertyMetadata("", OnChanged));

    /// <summary>The status's colour (<see cref="JiraStatusStyle"/>), when the pill is no Internal badge.</summary>
    public static readonly DependencyProperty StatusStyleProperty = DependencyProperty.Register(
        nameof(StatusStyle), typeof(JiraStatusStyle), typeof(IssuePill), new PropertyMetadata(JiraStatusStyle.Plain, OnChanged));

    /// <summary>The Internal badge of a service-desk comment instead of a status.</summary>
    public static readonly DependencyProperty IsInternalProperty = DependencyProperty.Register(
        nameof(IsInternal), typeof(bool), typeof(IssuePill), new PropertyMetadata(false, OnChanged));

    /// <summary>An empty, hidden pill.</summary>
    public IssuePill()
    {
        InitializeComponent();
        Apply();
    }

    /// <summary>The pill's text; empty hides it.</summary>
    public string Text
    {
        get => (string)GetValue(TextProperty);
        set => SetValue(TextProperty, value);
    }

    /// <summary>The status's colour, when the pill is no Internal badge.</summary>
    public JiraStatusStyle StatusStyle
    {
        get => (JiraStatusStyle)GetValue(StatusStyleProperty);
        set => SetValue(StatusStyleProperty, value);
    }

    /// <summary>The Internal badge of a service-desk comment instead of a status.</summary>
    public bool IsInternal
    {
        get => (bool)GetValue(IsInternalProperty);
        set => SetValue(IsInternalProperty, value);
    }

    private static void OnChanged(DependencyObject d, DependencyPropertyChangedEventArgs e) => ((IssuePill)d).Apply();

    // SetStatusPill / SetInternalPill: the text, its tooltip (a status's
    // only: a long one is cut), the colour, and hidden while empty.
    private void Apply()
    {
        var text = Text ?? "";
        Label.Text = text;
        Visibility = text.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        ToolTipService.SetToolTip(this, IsInternal || text.Length == 0 ? null : text);
        var state = IsInternal ? "Internal" : StatusStyle switch
        {
            JiraStatusStyle.InProgress => "InProgress",
            JiraStatusStyle.Done => "Done",
            _ => "Plain",
        };
        VisualStateManager.GoToState(this, state, false);
    }
}
