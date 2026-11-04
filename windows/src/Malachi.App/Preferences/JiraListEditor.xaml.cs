// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/JiraAccount/JiraListEditorView.swift;
// GTK: ui/internal/jiraaccount/lists.go (listEditor: apply, pending, commit,
// rebuildEntries, rebuildSuggestions). One list of texts of a Jira
// account's settings: the senders of notification e-mails, the bot
// accounts, the hidden lines or the name prefixes. A new entry is added
// with Add or Enter; the entries offered with one click are
// Jira.Suggestions; why the entry typed last was not added is
// Jira.CheckEntry's. It shows what JiraAccountController holds and reports
// what the user did; it checks nothing itself. The entries are the user's
// own texts, but an account edited elsewhere may carry anything: they are
// plain text, cut when long, whole in the tooltip.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.App.Resources;
using Malachi.Core.IssueTrackers;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using VirtualKey = Windows.System.VirtualKey;

namespace Malachi.App.Preferences;

/// <summary>A list of texts of a Jira account's settings.</summary>
public sealed partial class JiraListEditor : UserControl
{
    private static readonly FontFamily Monospace = new("Cascadia Mono, Consolas");

    private readonly string removeLabel;
    private IReadOnlyList<string> entries = [];
    private IReadOnlyList<JiraSuggestion> suggestions = [];

    // The field being emptied after an add, which is not typing.
    private bool clearing;

    /// <summary>The editor of the list <paramref name="kind"/>.</summary>
    public JiraListEditor(JiraListKind kind, string title, string subtitle, string addLabel, string removeLabel)
    {
        ArgumentNullException.ThrowIfNull(title);
        ArgumentNullException.ThrowIfNull(subtitle);
        Kind = kind;
        this.removeLabel = removeLabel;
        InitializeComponent();
        TitleLabel.Text = title;
        SubtitleLabel.Text = subtitle;
        SubtitleLabel.Visibility = subtitle.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        if (kind == JiraListKind.MetadataFilters)
        {
            FieldBox.FontFamily = Monospace;
        }
        // The title names the field for assistive technologies.
        AutomationProperties.SetName(FieldBox, title);
        AddButton.Content = addLabel;

        FieldBox.KeyDown += OnFieldKeyDown;
        FieldBox.TextChanged += (_, _) =>
        {
            if (!clearing)
            {
                Typed?.Invoke();
            }
        };
        AddButton.Click += (_, _) =>
        {
            Commit();
            FieldBox.Focus(FocusState.Programmatic);
        };
    }

    /// <summary>Which list this is.</summary>
    public JiraListKind Kind { get; }

    /// <summary>The field of a new entry.</summary>
    public TextBox Field => FieldBox;

    /// <summary>Add or Enter with the field's text; true empties the field.</summary>
    public Func<string, bool>? AddRequested { get; set; }

    /// <summary>The remove button of the entry at an index.</summary>
    public Action<int>? RemoveRequested { get; set; }

    /// <summary>A suggestion's button, with its entry.</summary>
    public Action<string>? SuggestionChosen { get; set; }

    /// <summary>The field's text changing under the user's hands.</summary>
    public Action? Typed { get; set; }

    /// <summary>Text in the field that was not added yet.</summary>
    public bool Pending => FieldBox.Text.Length > 0;

    /// <summary>The field's placeholder: what an empty list stands for.</summary>
    public void SetPlaceholder(string text) => FieldBox.PlaceholderText = text;

    /// <summary>Shows the list as the controller holds it.</summary>
    public void Apply(IReadOnlyList<string> entries, IReadOnlyList<JiraSuggestion> suggestions, string problem)
    {
        ArgumentNullException.ThrowIfNull(entries);
        ArgumentNullException.ThrowIfNull(suggestions);
        ArgumentNullException.ThrowIfNull(problem);
        if (!entries.SequenceEqual(this.entries))
        {
            this.entries = [.. entries];
            RebuildEntries();
        }
        if (!suggestions.SequenceEqual(this.suggestions))
        {
            this.suggestions = [.. suggestions];
            RebuildSuggestions();
        }
        ProblemText.Text = problem;
        ProblemText.Visibility = problem.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
    }

    /// <summary>Adds what the field holds, as Add does; whether nothing is left in it that could not be added.</summary>
    public bool Commit()
    {
        if (AddRequested is not { } add || !add(FieldBox.Text))
        {
            return false;
        }
        clearing = true;
        FieldBox.Text = "";
        clearing = false;
        return true;
    }

    private void OnFieldKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key == VirtualKey.Enter)
        {
            e.Handled = true;
            Commit();
        }
    }

    private void RebuildEntries()
    {
        EntriesBox.Children.Clear();
        for (var i = 0; i < entries.Count; i++)
        {
            var text = entries[i];
            var label = new TextBlock
            {
                Text = text,
                VerticalAlignment = VerticalAlignment.Center,
                TextTrimming = TextTrimming.CharacterEllipsis,
                TextWrapping = TextWrapping.NoWrap,
                MaxLines = 1,
            };
            if (Kind == JiraListKind.MetadataFilters)
            {
                label.FontFamily = Monospace;
            }
            ToolTipService.SetToolTip(label, text);
            var remove = new Button
            {
                Content = Icons.Create("list-remove"),
                Padding = new Thickness(6),
                VerticalAlignment = VerticalAlignment.Center,
                Style = (Style)Application.Current.Resources["SubtleButtonStyle"],
            };
            ToolTipService.SetToolTip(remove, removeLabel);
            // Named by what it removes, what it does said after it (no
            // sentence is put together from two).
            AutomationProperties.SetName(remove, text);
            AutomationProperties.SetHelpText(remove, removeLabel);
            var index = i;
            remove.Click += (_, _) => RemoveRequested?.Invoke(index);
            var row = new Grid { ColumnSpacing = 8 };
            row.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
            row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
            row.Children.Add(label);
            Grid.SetColumn(remove, 1);
            row.Children.Add(remove);
            EntriesBox.Children.Add(row);
        }
        EntriesBox.Visibility = entries.Count > 0 ? Visibility.Visible : Visibility.Collapsed;
    }

    private void RebuildSuggestions()
    {
        SuggestionsBox.Children.Clear();
        foreach (var s in suggestions)
        {
            // The label names data (a bot's name, a pattern): plain, cut
            // when long.
            var button = new Button
            {
                Content = new TextBlock { Text = s.Label, MaxWidth = 240, TextTrimming = TextTrimming.CharacterEllipsis, MaxLines = 1 },
            };
            ToolTipService.SetToolTip(button, s.Value);
            AutomationProperties.SetName(button, s.Label);
            var value = s.Value;
            button.Click += (_, _) => SuggestionChosen?.Invoke(value);
            SuggestionsBox.Children.Add(button);
        }
        SuggestionsBox.Visibility = suggestions.Count > 0 ? Visibility.Visible : Visibility.Collapsed;
    }
}
