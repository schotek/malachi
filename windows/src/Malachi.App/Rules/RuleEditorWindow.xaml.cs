// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/RuleEditorSheet.swift
// (present, render, buildConditionRow, buildActionRow, saveClicked,
// tryClicked); GTK: ui/internal/window/rules_editor.go (docs/plans/rules.md
// R14, T2). The flow, its calls and its sentences are Core's
// RuleEditorController over ui/internal/rules; this window shows what the
// controller holds after every Changed and feeds the edits back.
//
// Windows (docs/windows-port.md §11.3, §11.12): the editor is an owned modal
// window over the window it was opened from (ModalDialog), 640×720
// effective pixels, as the CalDAV wizard is; its close button, Escape and
// Ctrl+W cancel it (the tracked window's CloseWindow) as Cancel does. The
// condition rows and the action rows are made in code and built again only
// when their structure changed (the key: the fields and operators, the kinds
// and the account), otherwise only the problems under them are drawn, so
// that a caret in a text box stays where it is. A problem is shown once the
// user changed something (or the form came filled in, or with a name), not
// on the empty form of a new rule; Save is disabled while there is one. The
// outgoing question of Save is the dialog of IAlerts.ConfirmAsync with Turn
// On and Cancel. A folder from the server is plain text (TextBlock.Text, the
// ComboBox's strings); so are the sender and the list id a rule was
// prefilled with.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.App.Resources;
using Malachi.App.Shell;
using Malachi.App.Wizard;
using Malachi.Core.Api;
using Malachi.Core.Contacts;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Malachi.Core.Rules;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.MailRules;

/// <summary>The mail rule editor window.</summary>
public sealed partial class RuleEditorWindow : Window
{
    /// <summary>The window's content width in effective pixels.</summary>
    public const int ContentWidth = 640;

    /// <summary>Its content height.</summary>
    public const int ContentHeight = 720;

    // The widths of a condition row's field and operator columns.
    private const double FieldColumnWidth = 172;
    private const double OperatorColumnWidth = 160;

    private readonly AppState state;
    private readonly RuleEditorController controller;
    private readonly Action<Rule>? saved;
    private readonly ModalDialog modal;
    private readonly List<AccountId?> accountChoices = [];
    private readonly List<TextBlock> conditionProblems = [];
    private readonly List<TextBlock> actionProblems = [];

    // The widgets are being set from the controller: their events are not the user's.
    private bool applying;

    // The window is closed: nothing is drawn any more.
    private bool closed;

    // The form came filled in or the user changed it: its problems are shown.
    private bool showProblems;

    // What the rows were built for, and the folders Try It lists.
    private string conditionsKey = "";
    private string actionsKey = "";
    private string addKey = "";
    private string tryKey = "";
    private IReadOnlyList<Folder> shownTryFolders = [];

    private RuleEditorWindow(AppState state, RuleEditorController controller, Action<Rule>? saved)
    {
        this.state = state;
        this.controller = controller;
        this.saved = saved;
        showProblems = controller.Original is not null || controller.Form.Name.Length > 0;
        InitializeComponent();
        var title = controller.Title;
        Title = title;
        ExtendsContentIntoTitleBar = true;
        SetTitleBar(Header);
        Header.Title = title;
        // The window keeps the editor's name (Alt+Tab, UIA) after the title
        // bar names it (AccountWizardWindow).
        Header.Loaded += (_, _) => Title = title;
        var icon = System.IO.Path.Combine(AppContext.BaseDirectory, "Assets", "Malachi.ico");
        if (System.IO.File.Exists(icon))
        {
            AppWindow.SetIcon(icon);
        }
        Tracked = state.Windows.Track(this, WindowKind.Wizard, Root, ToastsHost);
        modal = new ModalDialog(this);
        Build();
        controller.Changed += (_, _) => Render();
        Root.Loaded += (_, _) => NameBox.Focus(FocusState.Programmatic);
        Closed += (_, _) =>
        {
            closed = true;
            modal.ReturnToOwner();
        };
        Render();
    }

    /// <summary>The window as the shell tracks it.</summary>
    public TrackedWindow Tracked { get; }

    /// <summary>
    /// Opens the editor of <paramref name="form"/> over <paramref name="owner"/>
    /// (the main window when null or hidden, shown first). <paramref name="original"/>
    /// is the stored rule being edited, null for a new one;
    /// <paramref name="saved"/> runs with the rule the daemon stored, before
    /// the window closes. Null when there is no window to open it over.
    /// </summary>
    public static RuleEditorWindow? Show(
        AppState state,
        Window? owner,
        IRulesCaller caller,
        RuleForm form,
        Rule? original,
        IReadOnlyList<Account> accounts,
        Func<AccountId, IReadOnlyList<Folder>> foldersOf,
        Action<Rule>? saved)
    {
        ArgumentNullException.ThrowIfNull(state);
        ArgumentNullException.ThrowIfNull(caller);
        ArgumentNullException.ThrowIfNull(form);
        if (owner is null || !owner.AppWindow.IsVisible)
        {
            state.ShowMainWindow();
            owner = state.MainWindow;
        }
        if (owner is null || state.IsStopping)
        {
            return null;
        }
        var w = new RuleEditorWindow(state, new RuleEditorController(caller, form, original, accounts, foldersOf), saved);
        w.modal.Present(owner, ContentWidth, ContentHeight);
        return w;
    }

    // The texts and the choices that never change.
    private void Build()
    {
        var form = controller.Form;
        NameRow.Header = RulesText.NameLabel;
        AutomationProperties.SetName(NameBox, RulesText.NameLabel);
        AccountRow.Header = RulesText.AccountLabel;
        AutomationProperties.SetName(AccountBox, RulesText.AccountLabel);
        // The accounts, then every mail account; the account of a rule that is gone first.
        foreach (var a in controller.Accounts)
        {
            accountChoices.Add(a.Id);
            AccountBox.Items.Add(ContactBook.AccountName(a));
        }
        if (form.AccountId is { Value.Length: > 0 } current && !accountChoices.Contains(current))
        {
            accountChoices.Insert(0, current);
            AccountBox.Items.Insert(0, RulesText.ScopeText(current, new RuleNames()));
        }
        accountChoices.Add(null);
        AccountBox.Items.Add(RulesText.AllAccounts);
        EnabledRow.Header = RulesText.EnabledLabel;
        AutomationProperties.SetName(EnabledSwitch, RulesText.EnabledLabel);
        WhenText.Text = RulesText.MatchLabel;
        AutomationProperties.SetName(MatchBox, RulesText.MatchLabel);
        MatchBox.Items.Add(RulesText.MatchText(RuleMatch.All));
        MatchBox.Items.Add(RulesText.MatchText(RuleMatch.Any));
        AddConditionButton.Content = RulesText.AddCondition;
        ActionsHeadingText.Text = RulesText.ActionsHeading;
        AddActionButton.Content = RulesText.AddAction;
        StopBox.Content = RulesText.StopLabel;
        StopHintText.Text = RulesText.StopHint;
        TryFolderLabel.Text = RulesText.TestFolderLabel;
        AutomationProperties.SetName(TryFolderBox, RulesText.TestFolderLabel);
        TryButton.Content = RulesText.TestButton;
        SaveButton.Content = L10n.T("Save");
    }

    // render: the fields, the rows, the problems, Try It, Save.
    private void Render()
    {
        if (closed)
        {
            return;
        }
        var form = controller.Form;
        applying = true;
        try
        {
            if (NameBox.Text != form.Name)
            {
                NameBox.Text = form.Name;
            }
            ShowProblem(NameProblem, controller.ProblemText("name"));
            var account = accountChoices.FindIndex(c => c == form.AccountId);
            if (AccountBox.SelectedIndex != account)
            {
                AccountBox.SelectedIndex = account;
            }
            if (EnabledSwitch.IsOn != form.Enabled)
            {
                EnabledSwitch.IsOn = form.Enabled;
            }
            var match = form.Match.Value == RuleMatch.Any ? 1 : 0;
            if (MatchBox.SelectedIndex != match)
            {
                MatchBox.SelectedIndex = match;
            }
            RenderConditions();
            RenderActions();
            if (StopBox.IsChecked != form.Stop)
            {
                StopBox.IsChecked = form.Stop;
            }
            RenderTry();
            var error = controller.SaveError;
            SaveErrorBar.Message = error;
            SaveErrorBar.IsOpen = error.Length > 0;
            SaveButton.IsEnabled = controller.CanSave;
        }
        finally
        {
            applying = false;
        }
    }

    // A problem under its row, once the user has started.
    private void ShowProblem(TextBlock block, string text)
    {
        var shown = showProblems && text.Length > 0;
        block.Text = shown ? text : "";
        block.Visibility = shown ? Visibility.Visible : Visibility.Collapsed;
    }

    private Style Resource(string key) => (Style)Root.Resources[key];

    // ----- the conditions -----

    private void RenderConditions()
    {
        var conditions = controller.Form.Conditions;
        var key = string.Join(';', conditions.Select(c => c.Field.Value + "|" + c.Op.Value));
        if (key != conditionsKey)
        {
            conditionsKey = key;
            BuildConditions();
        }
        for (var i = 0; i < conditionProblems.Count; i++)
        {
            ShowProblem(conditionProblems[i], controller.ProblemText("conditions", i));
        }
        ShowProblem(ConditionsProblem, controller.GroupProblemText("conditions"));
        AddConditionButton.IsEnabled = conditions.Count < RuleLimits.MaxRuleConditions;
    }

    private void BuildConditions()
    {
        ConditionsPanel.Children.Clear();
        conditionProblems.Clear();
        var conditions = controller.Form.Conditions;
        for (var i = 0; i < conditions.Count; i++)
        {
            ConditionsPanel.Children.Add(ConditionRow(i, conditions[i]));
        }
    }

    // buildConditionRow: field, operator, value and the remove button, the
    // problem under them.
    private StackPanel ConditionRow(int index, RuleCondition c)
    {
        // The field and the operator in columns of their own width, so that
        // the rows line up whatever each one's choice is (measured: Auto
        // columns let a longer field push its row's operator and value out
        // of line with the rows above).
        var grid = new Grid { ColumnSpacing = 8 };
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(FieldColumnWidth) });
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(OperatorColumnWidth) });
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });

        var field = new ComboBox { HorizontalAlignment = HorizontalAlignment.Stretch, VerticalAlignment = VerticalAlignment.Top };
        foreach (var f in RuleBook.Fields)
        {
            field.Items.Add(RulesText.FieldText(f));
        }
        field.SelectedIndex = IndexOf(RuleBook.Fields, c.Field);
        AutomationProperties.SetAutomationId(field, "RuleConditionField:" + index);
        AutomationProperties.SetName(field, RulesText.FieldText(c.Field));
        field.SelectionChanged += (_, _) =>
        {
            if (!applying && field.SelectedIndex >= 0)
            {
                controller.SetConditionField(index, RuleBook.Fields[field.SelectedIndex]);
            }
        };
        grid.Children.Add(field);

        var operators = RuleBook.OperatorsFor(c.Field);
        var op = new ComboBox { HorizontalAlignment = HorizontalAlignment.Stretch, VerticalAlignment = VerticalAlignment.Top };
        foreach (var o in operators)
        {
            op.Items.Add(RulesText.OperatorText(c.Field, o));
        }
        op.SelectedIndex = IndexOf(operators, c.Op);
        AutomationProperties.SetAutomationId(op, "RuleConditionOperator:" + index);
        AutomationProperties.SetName(op, RulesText.OperatorText(c.Field, c.Op));
        op.SelectionChanged += (_, _) =>
        {
            if (!applying && op.SelectedIndex >= 0)
            {
                controller.SetConditionOperator(index, operators[op.SelectedIndex]);
            }
        };
        Grid.SetColumn(op, 1);
        grid.Children.Add(op);

        var value = ValueControl(index, c);
        Grid.SetColumn(value, 2);
        grid.Children.Add(value);

        var remove = RemoveButton("RuleConditionRemove:" + index, () => controller.RemoveCondition(index));
        Grid.SetColumn(remove, 3);
        grid.Children.Add(remove);

        var problem = new TextBlock { Style = Resource("RuleProblemStyle"), Visibility = Visibility.Collapsed };
        AutomationProperties.SetAutomationId(problem, "RuleConditionProblem:" + index);
        conditionProblems.Add(problem);
        var row = new StackPanel { Spacing = 2 };
        row.Children.Add(grid);
        row.Children.Add(problem);
        return row;
    }

    // The value: a text box for text, a choice for a bulk kind or for
    // attachments (ValueKindOf).
    private FrameworkElement ValueControl(int index, RuleCondition c)
    {
        var id = "RuleConditionValue:" + index;
        switch (RuleBook.ValueKindOf(c.Field))
        {
            case RuleValueKind.Bulk:
                return Choice(id, index, RuleBook.BulkValues, c, v => RulesText.ValueText(c.Field, v));
            case RuleValueKind.Bool:
                return Choice(id, index, [RuleBook.ValueTrue, RuleBook.ValueFalse], c, v => RulesText.ValueText(c.Field, v));
        }
        var box = new TextBox
        {
            Text = c.Value,
            PlaceholderText = RulesText.ValuePlaceholder(c.Field, c.Op),
            IsSpellCheckEnabled = false,
            VerticalAlignment = VerticalAlignment.Top,
        };
        AutomationProperties.SetAutomationId(box, id);
        AutomationProperties.SetName(box, RulesText.FieldText(c.Field));
        box.TextChanged += (_, _) =>
        {
            if (!applying)
            {
                showProblems = true;
                controller.SetConditionValue(index, box.Text);
            }
        };
        return box;
    }

    private ComboBox Choice(string id, int index, IReadOnlyList<string> values, RuleCondition c, Func<string, string> text)
    {
        var box = new ComboBox { HorizontalAlignment = HorizontalAlignment.Stretch, VerticalAlignment = VerticalAlignment.Top };
        foreach (var v in values)
        {
            box.Items.Add(text(v));
        }
        box.SelectedIndex = values.ToList().IndexOf(c.Value);
        AutomationProperties.SetAutomationId(box, id);
        AutomationProperties.SetName(box, RulesText.FieldText(c.Field));
        box.SelectionChanged += (_, _) =>
        {
            if (!applying && box.SelectedIndex >= 0)
            {
                controller.SetConditionValue(index, values[box.SelectedIndex]);
            }
        };
        return box;
    }

    // ----- the actions -----

    private void RenderActions()
    {
        var form = controller.Form;
        var key = (form.AccountId?.Value ?? "") + "/" + string.Join(';', form.Actions.Select(a => a.Kind.Value));
        if (key != actionsKey)
        {
            actionsKey = key;
            BuildActions();
        }
        for (var i = 0; i < actionProblems.Count; i++)
        {
            ShowProblem(actionProblems[i], controller.ProblemText("actions", i));
        }
        ShowProblem(ActionsProblem, controller.GroupProblemText("actions"));
        RenderAddAction();
    }

    private void BuildActions()
    {
        ActionsPanel.Children.Clear();
        actionProblems.Clear();
        var actions = controller.Form.Actions;
        for (var i = 0; i < actions.Count; i++)
        {
            ActionsPanel.Children.Add(ActionRow(i, actions[i]));
        }
    }

    // buildActionRow: the kind and the remove button, then the controls of
    // the kind: a folder for Move to folder, an address for Forward to, a
    // subject and a text for Reply automatically, the hint of an outgoing
    // action, the problem.
    private Border ActionRow(int index, RuleAction a)
    {
        var column = new StackPanel { Spacing = 8 };
        var head = new Grid { ColumnSpacing = 8 };
        head.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        head.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        var kind = new TextBlock
        {
            Text = RulesText.ActionText(a.Kind),
            VerticalAlignment = VerticalAlignment.Center,
            Style = (Style)Application.Current.Resources["BodyStrongTextBlockStyle"],
        };
        head.Children.Add(kind);
        var remove = RemoveButton("RuleActionRemove:" + index, () => controller.RemoveAction(index));
        Grid.SetColumn(remove, 1);
        head.Children.Add(remove);
        column.Children.Add(head);

        switch (a.Kind.Value)
        {
            case RuleActionKind.Move:
                column.Children.Add(FolderChoice(index, a));
                break;
            case RuleActionKind.Forward:
                column.Children.Add(ForwardBox(index, a));
                break;
            case RuleActionKind.AutoReply:
                column.Children.Add(ReplySubjectBox(index, a));
                column.Children.Add(ReplyTextBox(index, a));
                break;
        }
        if (a.Kind.IsOutgoing)
        {
            column.Children.Add(new TextBlock
            {
                Text = RulesText.OutgoingHint(a.Kind),
                Style = Resource("SettingsGroupDescriptionStyle"),
            });
        }
        var problem = new TextBlock { Style = Resource("RuleProblemStyle"), Visibility = Visibility.Collapsed };
        AutomationProperties.SetAutomationId(problem, "RuleActionProblem:" + index);
        actionProblems.Add(problem);
        column.Children.Add(problem);

        var card = new Border { Style = Resource("RuleCardStyle"), Child = column };
        AutomationProperties.SetAutomationId(card, "RuleAction:" + index);
        AutomationProperties.SetName(card, RulesText.ActionText(a.Kind));
        return card;
    }

    private ComboBox FolderChoice(int index, RuleAction a)
    {
        var folders = controller.MoveFolders;
        var box = new ComboBox
        {
            PlaceholderText = RulesText.FolderPlaceholder,
            HorizontalAlignment = HorizontalAlignment.Stretch,
        };
        foreach (var f in folders)
        {
            box.Items.Add(FolderTree.FolderTitle(f));
        }
        box.SelectedIndex = a.FolderId is { } id ? folders.ToList().FindIndex(f => f.Id == id) : -1;
        AutomationProperties.SetAutomationId(box, "RuleActionFolder:" + index);
        AutomationProperties.SetName(box, RulesText.ActionText(a.Kind));
        box.SelectionChanged += (_, _) =>
        {
            if (!applying && box.SelectedIndex >= 0)
            {
                showProblems = true;
                controller.SetMoveFolder(index, folders[box.SelectedIndex].Id);
            }
        };
        return box;
    }

    private TextBox ForwardBox(int index, RuleAction a)
    {
        var box = new TextBox
        {
            Text = a.To ?? "",
            PlaceholderText = RulesText.ForwardPlaceholder,
            IsSpellCheckEnabled = false,
        };
        AutomationProperties.SetAutomationId(box, "RuleActionTo:" + index);
        AutomationProperties.SetName(box, RulesText.ActionText(a.Kind));
        box.TextChanged += (_, _) =>
        {
            if (!applying)
            {
                showProblems = true;
                controller.SetForwardTo(index, box.Text);
            }
        };
        return box;
    }

    private TextBox ReplySubjectBox(int index, RuleAction a)
    {
        var box = new TextBox { Text = a.Subject ?? "", PlaceholderText = RulesText.ReplySubjectPlaceholder };
        AutomationProperties.SetAutomationId(box, "RuleActionSubject:" + index);
        AutomationProperties.SetName(box, RulesText.ReplySubjectPlaceholder);
        box.TextChanged += (_, _) =>
        {
            if (!applying)
            {
                showProblems = true;
                controller.SetReplySubject(index, box.Text);
            }
        };
        return box;
    }

    private TextBox ReplyTextBox(int index, RuleAction a)
    {
        var box = new TextBox
        {
            Text = a.Text ?? "",
            PlaceholderText = RulesText.ReplyTextPlaceholder,
            AcceptsReturn = true,
            TextWrapping = TextWrapping.Wrap,
            MinHeight = 96,
            MaxHeight = 220,
        };
        AutomationProperties.SetAutomationId(box, "RuleActionText:" + index);
        AutomationProperties.SetName(box, RulesText.ReplyTextPlaceholder);
        box.TextChanged += (_, _) =>
        {
            if (!applying)
            {
                showProblems = true;
                controller.SetReplyText(index, box.Text);
            }
        };
        return box;
    }

    // Add Action: a menu of the kinds still available.
    private void RenderAddAction()
    {
        var available = RuleBook.AvailableActions(controller.Form);
        var key = string.Join(';', available.Select(k => k.Value));
        if (key != addKey)
        {
            addKey = key;
            var menu = new MenuFlyout();
            foreach (var k in available)
            {
                var item = new MenuFlyoutItem { Text = RulesText.ActionText(k) };
                AutomationProperties.SetAutomationId(item, "RuleAddAction:" + k.Value);
                var kind = k;
                item.Click += (_, _) =>
                {
                    showProblems = true;
                    controller.AddAction(kind);
                };
                menu.Items.Add(item);
            }
            AddActionButton.Flyout = menu;
        }
        AddActionButton.IsEnabled = available.Count > 0 && controller.Form.Actions.Count < RuleLimits.MaxRuleActions;
    }

    // ----- Try It -----

    private void RenderTry()
    {
        var folders = controller.TestFolders;
        var key = (controller.TestAccountId?.Value ?? "") + "/" + string.Join(';', folders.Select(f => f.Id.Value));
        if (key != tryKey)
        {
            tryKey = key;
            shownTryFolders = folders;
            TryFolderBox.Items.Clear();
            foreach (var f in folders)
            {
                TryFolderBox.Items.Add(FolderTree.FolderTitle(f));
            }
        }
        var chosen = controller.TestFolderId is { } id ? shownTryFolders.ToList().FindIndex(f => f.Id == id) : -1;
        if (TryFolderBox.SelectedIndex != chosen)
        {
            TryFolderBox.SelectedIndex = chosen;
        }
        TryFolderBox.IsEnabled = shownTryFolders.Count > 0 && !controller.Testing;
        TryButton.IsEnabled = controller.CanTest;
        TrySpinner.IsActive = controller.Testing;
        TrySpinner.Visibility = controller.Testing ? Visibility.Visible : Visibility.Collapsed;
        var text = controller.TestText;
        TryText.Text = text;
        TryText.Style = Resource(controller.TestFailed ? "RuleProblemStyle" : "SettingsGroupDescriptionStyle");
        TryText.Visibility = text.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
    }

    // ----- events -----

    private void OnNameChanged(object sender, TextChangedEventArgs e)
    {
        if (applying)
        {
            return;
        }
        showProblems = true;
        controller.SetName(NameBox.Text);
    }

    private void OnAccountChanged(object sender, SelectionChangedEventArgs e)
    {
        if (!applying && AccountBox.SelectedIndex >= 0)
        {
            controller.SetAccount(accountChoices[AccountBox.SelectedIndex]);
        }
    }

    private void OnEnabledToggled(object sender, RoutedEventArgs e)
    {
        if (!applying)
        {
            controller.SetEnabled(EnabledSwitch.IsOn);
        }
    }

    private void OnMatchChanged(object sender, SelectionChangedEventArgs e)
    {
        if (!applying && MatchBox.SelectedIndex >= 0)
        {
            controller.SetMatch(MatchBox.SelectedIndex == 1 ? RuleMatch.Any : RuleMatch.All);
        }
    }

    private void OnStopChanged(object sender, RoutedEventArgs e)
    {
        if (!applying)
        {
            controller.SetStop(StopBox.IsChecked == true);
        }
    }

    private void OnAddConditionClick(object sender, RoutedEventArgs e)
    {
        showProblems = true;
        controller.AddCondition();
    }

    private void OnTryFolderChanged(object sender, SelectionChangedEventArgs e)
    {
        if (!applying && TryFolderBox.SelectedIndex >= 0 && TryFolderBox.SelectedIndex < shownTryFolders.Count)
        {
            controller.SetTestFolder(shownTryFolders[TryFolderBox.SelectedIndex].Id);
        }
    }

    private void OnTryClick(object sender, RoutedEventArgs e) => _ = controller.TestAsync();

    // saveClicked: the outgoing question (Turn On / Cancel) when a rule
    // that sends mail is saved enabled, then the call; the rule goes to the
    // page and the window closes, or the sentence stays under the form.
    private async void OnSaveClick(object sender, RoutedEventArgs e)
    {
        var result = await controller.SaveAsync(
            q => state.Alerts.ConfirmAsync(this, q.Heading, q.Body, q.Confirm, L10n.T("Cancel")));
        if (result.Ok && controller.Saved is { } rule)
        {
            saved?.Invoke(rule);
            Close();
        }
    }

    private void OnCancelClick(object sender, RoutedEventArgs e) => Close();

    // ----- helpers -----

    private static int IndexOf<T>(IReadOnlyList<T> list, T value)
    {
        for (var i = 0; i < list.Count; i++)
        {
            if (EqualityComparer<T>.Default.Equals(list[i], value))
            {
                return i;
            }
        }
        return -1;
    }

    private static Button RemoveButton(string automationId, Action remove)
    {
        var button = new Button
        {
            Content = new FontIcon { FontSize = 14, Glyph = Icons.Glyph("list-remove") },
            Style = (Style)Application.Current.Resources["SubtleButtonStyle"],
            VerticalAlignment = VerticalAlignment.Top,
        };
        AutomationProperties.SetAutomationId(button, automationId);
        AutomationProperties.SetName(button, RulesText.RemoveRow);
        ToolTipService.SetToolTip(button, RulesText.RemoveRow);
        button.Click += (_, _) => remove();
        return button;
    }
}
