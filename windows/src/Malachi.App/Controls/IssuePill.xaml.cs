// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/widget/pill.go (SetStatusPill, SetInternalPill,
// SetBulkPill), the crm_pill of docs/eway.md §2.6 and the draft-pill of ui/internal/style (the Draft badge of
// a conversation with the user's unsent reply);
// macOS: Shared/IssuePill.swift (colours, paint). The pill's text and kind
// as dependency properties, for x:Bind from a row or a card; the kind picks
// the visual state (IssuePill.xaml).

using Malachi.Core.IssueTrackers;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Controls;

/// <summary>A status pill or the Internal badge of an issue, the neutral tag of a bulk message, or the Draft badge.</summary>
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

    /// <summary>
    /// The neutral tag of a bulk message ("Bulk", "Mailing List", "Automated")
    /// instead of a status; its text is its own tooltip, as a status's is.
    /// </summary>
    public static readonly DependencyProperty IsBulkProperty = DependencyProperty.Register(
        nameof(IsBulk), typeof(bool), typeof(IssuePill), new PropertyMetadata(false, OnChanged));

    /// <summary>
    /// The red Draft badge of a conversation with the user's unsent reply
    /// (its row in the list, its draft card) instead of a status.
    /// </summary>
    public static readonly DependencyProperty IsDraftProperty = DependencyProperty.Register(
        nameof(IsDraft), typeof(bool), typeof(IssuePill), new PropertyMetadata(false, OnChanged));

    /// <summary>
    /// The eWay pill of a message saved in eWay-CRM (CrmText.PillFor; GTK
    /// crm_pill, macOS IssuePill.crmColours) instead of a status; its tooltip
    /// is <see cref="Hint"/>.
    /// </summary>
    public static readonly DependencyProperty IsCrmProperty = DependencyProperty.Register(
        nameof(IsCrm), typeof(bool), typeof(IssuePill), new PropertyMetadata(false, OnChanged));

    /// <summary>The tooltip of an eWay pill ("Saved in eWay-CRM").</summary>
    public static readonly DependencyProperty HintProperty = DependencyProperty.Register(
        nameof(Hint), typeof(string), typeof(IssuePill), new PropertyMetadata("", OnChanged));

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

    /// <summary>The neutral tag of a bulk message instead of a status.</summary>
    public bool IsBulk
    {
        get => (bool)GetValue(IsBulkProperty);
        set => SetValue(IsBulkProperty, value);
    }

    /// <summary>The red Draft badge instead of a status.</summary>
    public bool IsDraft
    {
        get => (bool)GetValue(IsDraftProperty);
        set => SetValue(IsDraftProperty, value);
    }

    /// <summary>The eWay pill instead of a status.</summary>
    public bool IsCrm
    {
        get => (bool)GetValue(IsCrmProperty);
        set => SetValue(IsCrmProperty, value);
    }

    /// <summary>The tooltip of an eWay pill.</summary>
    public string Hint
    {
        get => (string)GetValue(HintProperty);
        set => SetValue(HintProperty, value);
    }

    private static void OnChanged(DependencyObject d, DependencyPropertyChangedEventArgs e) => ((IssuePill)d).Apply();

    // SetStatusPill / SetInternalPill: the text, its tooltip (a status's
    // only: a long one is cut), the colour, and hidden while empty.
    private void Apply()
    {
        var text = Text ?? "";
        Label.Text = text;
        Visibility = text.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        ToolTipService.SetToolTip(this, IsCrm ? (text.Length == 0 || string.IsNullOrEmpty(Hint) ? null : Hint) : IsInternal || IsDraft || text.Length == 0 ? null : text);
        var state = IsCrm ? "Crm" : IsInternal ? "Internal" : IsDraft ? "Draft" : IsBulk ? "Bulk" : StatusStyle switch
        {
            JiraStatusStyle.InProgress => "InProgress",
            JiraStatusStyle.Done => "Done",
            _ => "Plain",
        };
        VisualStateManager.GoToState(this, state, false);
    }
}
