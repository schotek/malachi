// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/CRM/CRMDialogController.swift; GTK:
// crm_dialog.blp and window/crm_dialog.go of docs/eway.md §2.6 (written in
// T7). The Save to eWay-CRM dialog (Edit Links over a saved message) over
// Core's CrmDialogSession: a line naming the messages, the search field
// (crm.search after a pause in typing, from 2 bytes; its spinner and its
// failure under it), then while crm.lookup runs a spinner, after a failed
// lookup its sentence (only Cancel left), else the groups Contacts,
// Companies, Deals and Projects, each item a check box with its detail
// under it and, for a deal or project, the Superior Item radio; the empty
// text when nothing is listed, the quiet search hint when no deal or
// project is, the partial hint when the CRM did not answer every lookup in
// time; last the Private check box with its hint. Save closes the dialog
// and the controller saves in the background.
//
// Windows (a listed deviation, windows/README.md): a ContentDialog in
// WinUI's button order, Save the primary button on the left and Cancel on
// the right, built in code and queued on the window like the app's other
// dialogs (IAlerts.ShowDialogAsync); macOS shows a sheet of a fixed size.
// The groups are rebuilt only when the listed items change (a search added
// some), so a click keeps the keyboard where it was; a refused check
// (more than 50 items) is unchecked again. Every name and detail is from
// the CRM and set as TextBlock.Text or CheckBox content strings only.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Crm;
using Malachi.Core.I18n;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Crm;

/// <summary>The Save to eWay-CRM / Edit Links dialog of one session.</summary>
internal sealed partial class CrmSaveDialog : ContentDialog
{
    private readonly CrmDialogSession session;
    private readonly TextBox search;
    private readonly ProgressRing searchRing;
    private readonly TextBlock searchProblem;
    private readonly StackPanel loading;
    private readonly TextBlock failure;
    private readonly TextBlock partial;
    private readonly StackPanel groups;
    private readonly TextBlock empty;
    private readonly TextBlock searchHint;
    private readonly CheckBox privateBox;
    private readonly Dictionary<string, (CheckBox Check, RadioButton? Superior)> rows = new(StringComparer.Ordinal);
    private string listed = "";
    private bool syncing;

    /// <summary>The dialog of <paramref name="session"/>.</summary>
    public CrmSaveDialog(CrmDialogSession session)
    {
        ArgumentNullException.ThrowIfNull(session);
        this.session = session;
        var texts = session.Texts;
        CloseButtonText = Mnemonic.Strip(L10n.T("_Cancel"));
        DefaultButton = ContentDialogButton.Primary;
        AutomationProperties.SetAutomationId(this, "CrmDialog");

        var summary = new TextBlock
        {
            Text = session.Summary,
            TextWrapping = TextWrapping.Wrap,
            MaxLines = 2,
            TextTrimming = TextTrimming.CharacterEllipsis,
            Style = (Style)Application.Current.Resources["DimLabelStyle"],
        };
        AutomationProperties.SetAutomationId(summary, "CrmDialogSummary");
        search = new TextBox
        {
            PlaceholderText = texts.Search,
            IsSpellCheckEnabled = false,
            IsTextPredictionEnabled = false,
        };
        AutomationProperties.SetName(search, texts.Search);
        AutomationProperties.SetAutomationId(search, "CrmSearch");
        search.TextChanged += (_, _) => session.Search(search.Text);
        searchRing = new ProgressRing { Width = 16, Height = 16, IsActive = false, Visibility = Visibility.Collapsed };
        searchProblem = Caption("CrmSearchProblem", critical: true);
        loading = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8 };
        loading.Children.Add(new ProgressRing { Width = 20, Height = 20, IsActive = true });
        loading.Children.Add(new TextBlock { Text = CrmText.ActionText(CrmAction.Lookup), VerticalAlignment = VerticalAlignment.Center });
        AutomationProperties.SetAutomationId(loading, "CrmDialogLoading");
        failure = new TextBlock { TextWrapping = TextWrapping.Wrap, Visibility = Visibility.Collapsed };
        AutomationProperties.SetAutomationId(failure, "CrmDialogFailure");
        partial = Caption("CrmDialogPartial", critical: false);
        partial.Text = texts.Partial;
        groups = new StackPanel { Spacing = 12 };
        AutomationProperties.SetAutomationId(groups, "CrmDialogGroups");
        empty = Caption("CrmDialogEmpty", critical: false);
        empty.Text = texts.Empty;
        searchHint = Caption("CrmDialogSearchHint", critical: false);
        searchHint.Text = texts.SearchHint;
        privateBox = new CheckBox { Content = texts.Private };
        AutomationProperties.SetAutomationId(privateBox, "CrmPrivate");
        AutomationProperties.SetHelpText(privateBox, texts.PrivateHint);
        privateBox.Click += (_, _) => session.SetPrivate(privateBox.IsChecked == true);
        var privateHint = Caption("CrmPrivateHint", critical: false);
        privateHint.Text = texts.PrivateHint;
        privateHint.Margin = new Thickness(28, 0, 0, 0);

        var searchRow = new Grid { ColumnSpacing = 8 };
        searchRow.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        searchRow.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        searchRow.Children.Add(search);
        Grid.SetColumn(searchRing, 1);
        searchRing.VerticalAlignment = VerticalAlignment.Center;
        searchRow.Children.Add(searchRing);

        var list = new StackPanel { Spacing = 8 };
        list.Children.Add(loading);
        list.Children.Add(failure);
        list.Children.Add(groups);
        list.Children.Add(empty);
        list.Children.Add(searchHint);
        var scroller = new ScrollViewer
        {
            Content = list,
            MaxHeight = 360,
            HorizontalScrollBarVisibility = ScrollBarVisibility.Disabled,
            VerticalScrollBarVisibility = ScrollBarVisibility.Auto,
            IsTabStop = false,
        };
        var content = new StackPanel { Spacing = 10, MinWidth = 420 };
        content.Children.Add(summary);
        content.Children.Add(searchRow);
        content.Children.Add(searchProblem);
        content.Children.Add(partial);
        content.Children.Add(scroller);
        content.Children.Add(privateBox);
        content.Children.Add(privateHint);
        Content = content;

        PrimaryButtonClick += (_, e) =>
        {
            // Save closes the session and the controller saves in the
            // background; a session that cannot save keeps the dialog.
            session.Save();
            e.Cancel = !session.Finished;
        };
        Closing += (_, _) => session.Cancel();
        session.Changed += (_, _) => DispatcherQueue.TryEnqueue(Render);
        session.Closed += (_, _) => DispatcherQueue.TryEnqueue(Hide);
        Render();
    }

    // Draws the session: its phase, its texts, the groups (rebuilt only
    // when the items listed changed), the check boxes, Save's state.
    private void Render()
    {
        var texts = session.Texts;
        Title = texts.Title;
        PrimaryButtonText = Mnemonic.Strip(texts.Save);
        var phase = session.Phase;
        loading.Visibility = phase == CrmDialogSession.SessionPhase.Loading ? Visibility.Visible : Visibility.Collapsed;
        failure.Text = session.Failure ?? "";
        failure.Visibility = phase == CrmDialogSession.SessionPhase.Failed ? Visibility.Visible : Visibility.Collapsed;
        search.IsEnabled = phase == CrmDialogSession.SessionPhase.Ready;
        searchRing.IsActive = session.Searching;
        searchRing.Visibility = session.Searching ? Visibility.Visible : Visibility.Collapsed;
        searchProblem.Text = session.SearchProblem ?? "";
        searchProblem.Visibility = session.SearchProblem is null ? Visibility.Collapsed : Visibility.Visible;
        var d = session.Dialog;
        partial.Visibility = d?.Partial == true ? Visibility.Visible : Visibility.Collapsed;
        empty.Visibility = d?.IsEmpty == true ? Visibility.Visible : Visibility.Collapsed;
        searchHint.Visibility = d?.OffersSearchHint == true ? Visibility.Visible : Visibility.Collapsed;
        privateBox.IsEnabled = d is not null;
        syncing = true;
        privateBox.IsChecked = d?.Private == true;
        syncing = false;
        IsPrimaryButtonEnabled = phase == CrmDialogSession.SessionPhase.Ready && d is { CanSave: true };
        if (d is null)
        {
            return;
        }
        var all = d.Groups();
        var key = string.Join("|", all.SelectMany(g => g.Entries.Select(e => Key(e.Ref))));
        if (key != listed)
        {
            listed = key;
            Build(all, texts);
        }
        syncing = true;
        foreach (var g in all)
        {
            foreach (var e in g.Entries)
            {
                if (rows.TryGetValue(Key(e.Ref), out var r))
                {
                    r.Check.IsChecked = e.Checked;
                    if (r.Superior is { } radio)
                    {
                        radio.IsChecked = e.Superior;
                    }
                }
            }
        }
        syncing = false;
    }

    // The groups anew: a heading per kind, a check box per item with its
    // detail, the Superior Item radio of a deal or project.
    private void Build(IReadOnlyList<CrmDialogGroup> all, CrmDialogStrings texts)
    {
        groups.Children.Clear();
        rows.Clear();
        foreach (var g in all)
        {
            var group = new StackPanel { Spacing = 4 };
            var heading = new TextBlock
            {
                Text = CrmText.GroupTitle(g.Kind),
                Style = (Style)Application.Current.Resources["BodyStrongTextBlockStyle"],
            };
            AutomationProperties.SetHeadingLevel(heading, Microsoft.UI.Xaml.Automation.Peers.AutomationHeadingLevel.Level3);
            group.Children.Add(heading);
            foreach (var e in g.Entries)
            {
                var r = e.Ref;
                var check = new CheckBox { Content = e.Item.Name };
                AutomationProperties.SetAutomationId(check, "CrmItem:" + Key(r));
                check.Click += (_, _) =>
                {
                    if (syncing)
                    {
                        return;
                    }
                    if (!session.Toggle(r, check.IsChecked == true))
                    {
                        // Refused (the limit) or unchanged: the model's state.
                        check.IsChecked = session.Dialog?.Checked(r) == true;
                    }
                };
                var row = new StackPanel { Spacing = 0 };
                row.Children.Add(check);
                if (e.Item.Detail is { Length: > 0 } detail)
                {
                    var d = Caption("", critical: false);
                    d.Text = detail;
                    d.Margin = new Thickness(28, 0, 0, 0);
                    d.MaxLines = 2;
                    d.TextTrimming = TextTrimming.CharacterEllipsis;
                    row.Children.Add(d);
                }
                RadioButton? superior = null;
                if (e.CanBeSuperior)
                {
                    superior = new RadioButton
                    {
                        Content = texts.SuperiorItem,
                        GroupName = "CrmSuperior",
                        Margin = new Thickness(28, 0, 0, 0),
                        MinWidth = 0,
                    };
                    AutomationProperties.SetAutomationId(superior, "CrmSuperior:" + Key(r));
                    AutomationProperties.SetName(superior, texts.SuperiorItem + ": " + e.Item.Name);
                    superior.Checked += (_, _) =>
                    {
                        if (!syncing)
                        {
                            session.SetSuperior(r);
                        }
                    };
                    row.Children.Add(superior);
                }
                rows[Key(r)] = (check, superior);
                group.Children.Add(row);
            }
            groups.Children.Add(group);
        }
    }

    private static string Key(CrmItemRef r) => r.Kind.Value + ":" + r.ItemGuid;

    private static TextBlock Caption(string automationId, bool critical)
    {
        var t = new TextBlock
        {
            TextWrapping = TextWrapping.Wrap,
            Style = (Style)Application.Current.Resources[critical ? "CaptionLabelStyle" : "DimCaptionLabelStyle"],
            Visibility = Visibility.Visible,
        };
        if (critical)
        {
            t.Foreground = (Microsoft.UI.Xaml.Media.Brush)Application.Current.Resources["SystemFillColorCriticalBrush"];
            t.Visibility = Visibility.Collapsed;
        }
        if (automationId.Length > 0)
        {
            AutomationProperties.SetAutomationId(t, automationId);
        }
        return t;
    }
}
