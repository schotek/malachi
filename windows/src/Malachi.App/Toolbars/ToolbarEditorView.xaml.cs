// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/plans/toolbar-customization.md T13, §17.2): the
// view of the toolbar editor, Core's ToolbarEditor (ui/internal/toolbaredit)
// over the Windows variant; GTK: its toolbar editor dialog. The list is the
// editor's Sections flattened, a heading before each section's rows; a drop
// at flat position p is the insertion index p - 1 of the layout without the
// row (the first heading has no separator), which ToolbarEditor.Drop takes
// or refuses (the list is then drawn again from the editor, so a refused
// drop puts the row back). Ctrl+Up and Ctrl+Down move the selected row as
// on the Accounts page (AccountListSection), Delete removes it. Every
// change writes the editor's Tokens to the toolbar's key at once; a change
// of the key from elsewhere (another window, another process) reloads the
// editor. Restore Defaults asks in its flyout and writes the empty list.

using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;
using Malachi.App.Shell;
using Malachi.Core.I18n;
using Malachi.Core.Settings;
using Malachi.Core.Toolbars;
using Microsoft.UI.Input;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using Windows.ApplicationModel.DataTransfer;
using VirtualKey = Windows.System.VirtualKey;

namespace Malachi.App.Toolbars;

/// <summary>The Customize Toolbar dialog's content.</summary>
public sealed partial class ToolbarEditorView : UserControl
{
    private readonly AppState state;
    private readonly List<SettingsChangeToken> tokens = [];
    private ToolbarEditor editor;

    // The list is being drawn from the editor: its selection changes are not the user's.
    private bool syncing;

    // The last press in the list was on a row's drag handle.
    private bool pressedOnHandle;
    private ToolbarEditorEntry? dragged;

    /// <summary>An editor of <paramref name="bar"/> over the application's settings.</summary>
    internal ToolbarEditorView(AppState state, Toolbar.Bar bar)
    {
        this.state = state;
        editor = new ToolbarEditor(ToolbarVariant.Windows, bar, state.Settings.ToolbarTokens(bar), ToolbarFeatures.Of(state));
        InitializeComponent();
        foreach (var b in Toolbar.Bars)
        {
            BarChooser.Items.Add(new ComboBoxItem { Content = Toolbar.BarTitle(b), Tag = b });
        }
        AutomationProperties.SetName(BarChooser, Toolbar.DialogTitle());
        AutomationProperties.SetName(RowList, Toolbar.BarTitle(bar));
        RemoveButton.Content = Toolbar.RemoveTitle();
        AddButton.Content = Toolbar.AddItemTitle();
        RestoreButton.Content = Toolbar.RestoreTitle();
        RestoreQuestionText.Text = Toolbar.RestoreQuestion();
        RestoreConfirmButton.Content = Toolbar.RestoreTitle();
        RestoreCancelButton.Content = Toolbar.RestoreCancel();
        RowList.AddHandler(PointerPressedEvent, new PointerEventHandler(OnListPointerPressed), handledEventsToo: true);
        foreach (var b in Toolbar.Bars)
        {
            var which = b;
            tokens.Add(state.Settings.OnChange(SettingsStore.ToolbarKey(which), () => OnKeyChanged(which)));
        }
        Unloaded += (_, _) =>
        {
            foreach (var t in tokens)
            {
                t.Cancel();
            }
            tokens.Clear();
        };
        syncing = true;
        BarChooser.SelectedIndex = IndexOfBar(bar);
        syncing = false;
        Render(-1);
    }

    /// <summary>The list's lines (headings and rows).</summary>
    public ObservableCollection<ToolbarEditorEntry> Entries { get; } = [];

    /// <summary>
    /// Shows the Customize Toolbar dialog over <paramref name="window"/> on
    /// <paramref name="bar"/> (Preferences → General → Toolbar, or a right
    /// click on a bar, T15); its changes are written as they are made.
    /// </summary>
    internal static void Show(AppState state, Window window, Toolbar.Bar bar)
    {
        ArgumentNullException.ThrowIfNull(state);
        _ = state.Alerts.ShowDialogAsync(window, () => new ContentDialog
        {
            Title = Toolbar.DialogTitle(),
            Content = new ToolbarEditorView(state, bar),
            CloseButtonText = L10n.T("Close"),
            DefaultButton = ContentDialogButton.Close,
        });
    }

    private static int IndexOfBar(Toolbar.Bar bar)
    {
        for (var i = 0; i < Toolbar.Bars.Count; i++)
        {
            if (Toolbar.Bars[i] == bar)
            {
                return i;
            }
        }
        return 0;
    }

    private static bool IsOnlyControlDown()
    {
        static bool Down(VirtualKey key) =>
            InputKeyboardSource.GetKeyStateForCurrentThread(key).HasFlag(Windows.UI.Core.CoreVirtualKeyStates.Down);
        return Down(VirtualKey.Control) && !Down(VirtualKey.Shift) && !Down(VirtualKey.Menu) && !Down(VirtualKey.LeftWindows) && !Down(VirtualKey.RightWindows);
    }

    // The editor's sections as lines, the row at layout index select
    // selected (a heading's index for a heading, -1 for none).
    private void Render(int select)
    {
        syncing = true;
        try
        {
            Entries.Clear();
            var layout = editor.Layout;
            ToolbarEditorEntry? chosen = null;
            var first = true;
            foreach (var section in editor.Sections())
            {
                var index = first ? -1 : IndexOf(layout, Toolbar.Separator(section.Section));
                first = false;
                var heading = new ToolbarEditorEntry(section.Section, Toolbar.SectionTitle(editor.Bar, section.Section), index);
                Entries.Add(heading);
                foreach (var row in section.Rows)
                {
                    var entry = new ToolbarEditorEntry(
                        section.Section,
                        row,
                        Toolbar.ItemLabel(editor.Bar, row.Id),
                        row.Unavailable ? Toolbar.UnavailableSubtitle() : "");
                    Entries.Add(entry);
                    if (row.Index == select)
                    {
                        chosen = entry;
                    }
                }
            }
            RowList.SelectedItem = chosen;
        }
        finally
        {
            syncing = false;
        }
        RestoreButton.IsEnabled = !editor.IsDefault;
        RestoreBodyText.Text = Toolbar.RestoreBody(editor.Bar);
        AutomationProperties.SetName(RowList, Toolbar.BarTitle(editor.Bar));
        UpdateButtons();
    }

    private static int IndexOf(IReadOnlyList<string> layout, string id)
    {
        for (var i = 0; i < layout.Count; i++)
        {
            if (string.Equals(layout[i], id, StringComparison.Ordinal))
            {
                return i;
            }
        }
        return -1;
    }

    private ToolbarEditorEntry? Selected => RowList.SelectedItem as ToolbarEditorEntry;

    private void UpdateButtons()
    {
        var row = Selected;
        RemoveButton.IsEnabled = row is { IsHeader: false, IsFixed: false };
    }

    // A change was made: the key gets the editor's tokens, the list shows it.
    private void Write(int select)
    {
        state.Settings.SetToolbarTokens(editor.Bar, editor.Tokens);
        editor.Reload(state.Settings.ToolbarTokens(editor.Bar));
        Render(select);
    }

    // The key changed: from this view (already shown) or from elsewhere.
    private void OnKeyChanged(Toolbar.Bar bar)
    {
        if (bar != editor.Bar)
        {
            return;
        }
        var select = Selected?.Index ?? -1;
        editor.Reload(state.Settings.ToolbarTokens(bar));
        Render(select);
    }

    private void OnBarChosen(object sender, SelectionChangedEventArgs e)
    {
        if (syncing || BarChooser.SelectedItem is not ComboBoxItem { Tag: Toolbar.Bar bar } || bar == editor.Bar)
        {
            return;
        }
        editor = new ToolbarEditor(ToolbarVariant.Windows, bar, state.Settings.ToolbarTokens(bar), ToolbarFeatures.Of(state));
        Render(-1);
    }

    private void OnSelectionChanged(object sender, SelectionChangedEventArgs e)
    {
        if (!syncing)
        {
            UpdateButtons();
        }
    }

    private void OnRemoveClick(object sender, RoutedEventArgs e) => RemoveSelected();

    private void RemoveSelected()
    {
        if (Selected is { IsHeader: false } row && editor.Remove(row.Index))
        {
            Write(Math.Max(row.Index - 1, -1));
        }
    }

    // Add Item…: what the selected line's section may take (the first
    // section's without a selection), then the two spaces after the
    // selected row (at the start of the section for a heading).
    private void OnAddOpening(object? sender, object e)
    {
        AddMenu.Items.Clear();
        var line = Selected;
        var section = line?.Section ?? Toolbar.Sections(editor.Bar)[0];
        var after = line?.Index ?? -1;
        foreach (var id in editor.Available(section))
        {
            var item = new MenuFlyoutItem { Text = Toolbar.ItemLabel(editor.Bar, id) };
            AutomationProperties.SetAutomationId(item, "ToolbarEditorAdd." + id);
            var which = id;
            item.Click += (_, _) =>
            {
                if (editor.Add(which, section))
                {
                    Write(IndexOf(editor.Layout, which));
                }
            };
            AddMenu.Items.Add(item);
        }
        if (AddMenu.Items.Count > 0)
        {
            AddMenu.Items.Add(new MenuFlyoutSeparator());
        }
        foreach (var kind in new[] { Toolbar.Space, Toolbar.FlexibleSpace })
        {
            var item = new MenuFlyoutItem { Text = Toolbar.ItemLabel(editor.Bar, kind) };
            AutomationProperties.SetAutomationId(item, "ToolbarEditorAdd." + kind);
            var which = kind;
            item.Click += (_, _) =>
            {
                if (editor.AddSpace(which, after))
                {
                    Write(after + 1);
                }
            };
            AddMenu.Items.Add(item);
        }
    }

    private void OnRestoreConfirmClick(object sender, RoutedEventArgs e)
    {
        RestoreFlyout.Hide();
        state.Settings.SetToolbarTokens(editor.Bar, editor.Reset());
        editor.Reload(state.Settings.ToolbarTokens(editor.Bar));
        Render(-1);
    }

    private void OnRestoreCancelClick(object sender, RoutedEventArgs e) => RestoreFlyout.Hide();

    // Ctrl+Up and Ctrl+Down move the selected row; Delete removes it.
    private void OnListPreviewKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key == VirtualKey.Delete && Selected is { IsHeader: false, IsFixed: false })
        {
            RemoveSelected();
            e.Handled = true;
            return;
        }
        if (e.Key is not (VirtualKey.Up or VirtualKey.Down) || !IsOnlyControlDown() || Selected is not { IsHeader: false } row)
        {
            return;
        }
        int index;
        var moved = e.Key == VirtualKey.Up ? editor.MoveUp(row.Index, out index) : editor.MoveDown(row.Index, out index);
        if (moved)
        {
            Write(index);
            if (RowList.ContainerFromItem(RowList.SelectedItem) is ListViewItem item)
            {
                item.Focus(FocusState.Keyboard);
            }
            e.Handled = true;
        }
    }

    // A drag may start only from a row's handle.
    private void OnListPointerPressed(object sender, PointerRoutedEventArgs e)
    {
        pressedOnHandle = false;
        for (var d = e.OriginalSource as DependencyObject; d is not null && d is not ListViewItem; d = VisualTreeHelper.GetParent(d))
        {
            if (d is FrameworkElement { Tag: "DragHandle" })
            {
                pressedOnHandle = true;
                return;
            }
        }
    }

    private void OnDragItemsStarting(object sender, DragItemsStartingEventArgs e)
    {
        var entry = e.Items.Count == 1 ? e.Items[0] as ToolbarEditorEntry : null;
        if (!pressedOnHandle || entry is null || entry.IsHeader || entry.IsFixed)
        {
            e.Cancel = true;
            return;
        }
        dragged = entry;
        e.Data.RequestedOperation = DataPackageOperation.Move;
    }

    // The list moved the line; the editor takes the drop or refuses it, and
    // the list is drawn again from the editor either way.
    private void OnDragItemsCompleted(ListViewBase sender, DragItemsCompletedEventArgs args)
    {
        var entry = dragged;
        dragged = null;
        pressedOnHandle = false;
        if (entry is null)
        {
            return;
        }
        var p = Entries.IndexOf(entry);
        if (args.DropResult == DataPackageOperation.Move && p >= 0 && editor.Drop(entry.Index, Math.Max(p - 1, 0), out var index))
        {
            Write(index);
            return;
        }
        Render(entry.Index);
    }
}
