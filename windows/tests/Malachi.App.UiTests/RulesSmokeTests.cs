// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The smoke tests of Preferences → Rules and the rule editor
// (docs/plans/rules.md R12, R14; docs/windows-port.md §11.17) over the
// published app with the invented rules (MALACHI_RULES_SAMPLES=1,
// RulesSmokeFixture): the page after Integrations lists the samples in
// their order, with the warning of the rule the daemon turned off and the
// sending icon of the rules that send mail; Ctrl+Down and Ctrl+Up move the
// selected row; the switch of an outgoing rule asks the Turn On question
// and stays off when it is declined; Add Rule… opens the editor, whose
// Save stays disabled until the form is valid, Escape and Ctrl+W cancel
// it; a forward address that is not one shows its problem; a saved rule
// is a new row, and Remove asks first. Elements are found by AutomationId
// (the names follow the user's language); the samples' ids are data.

using System;
using System.Linq;
using System.Threading;
using System.Windows.Automation;
using Xunit;

namespace Malachi.App.UiTests;

/// <summary>Preferences → Rules and the rule editor over the invented rules.</summary>
[Collection(OneAppAtATime.Name)]
public sealed class RulesSmokeTests(RulesSmokeFixture fixture) : IClassFixture<RulesSmokeFixture>
{
    private const byte VkControl = 0x11;
    private const byte VkEscape = 0x1B;
    private const byte VkUp = 0x26;
    private const byte VkDown = 0x28;
    private const byte VkW = 0x57;

    // The samples (RulesSampleCaller): Newsletters, Invoices from Acme
    // (outgoing, on), Away (outgoing, off), Project Atlas (off, its folder gone).
    private const string Newsletters = "RuleRow:r_sample1";
    private const string Invoices = "RuleRow:r_sample2";
    private const string Away = "RuleRow:r_sample3";
    private const string Atlas = "RuleRow:r_sample4";

    private AppSession App
    {
        get
        {
            Assert.SkipWhen(fixture.SkipReason is not null, fixture.SkipReason ?? "");
            return fixture.Session;
        }
    }

    // The main window in any mode (its title bar's switch).
    private AutomationElement Main => Uia.WindowWith(App.ProcessId, "ModeMail", AppSession.StartTimeout);

    [Fact]
    public void TheRulesPageShowsTheSamples()
    {
        var preferences = OpenRules();
        try
        {
            var page = Uia.Find(preferences, "PrefsRules");
            Assert.True(Uia.Find(page, "PrefsRulesAdd").Current.IsEnabled);
            Assert.True(Uia.Find(page, "PrefsRulesDescription").Current.Name.Length > 0);
            var list = Uia.Find(page, "PrefsRulesList");
            foreach (var id in new[] { Newsletters, Invoices, Away, Atlas })
            {
                Assert.NotNull(Uia.Find(list, id));
            }
            // The rule the daemon turned off shows why; the others do not.
            Assert.NotNull(Uia.Find(Uia.Find(list, Atlas), "RuleProblem"));
            Assert.Null(Uia.TryFind(Uia.Find(list, Newsletters), "RuleProblem"));
            // The rules that send mail show the sending icon.
            Assert.NotNull(Uia.Find(Uia.Find(list, Invoices), "RuleOutgoing"));
            Assert.Null(Uia.TryFind(Uia.Find(list, Newsletters), "RuleOutgoing"));
            // Every row has its switch, Edit and Remove.
            var away = Uia.Find(list, Away);
            Assert.Equal(ToggleState.Off, ToggleOf(Uia.Find(away, "RuleEnabled")));
            Assert.NotNull(Uia.Find(away, "EditRule"));
            Assert.NotNull(Uia.Find(away, "RemoveRule"));
            Assert.Null(Uia.TryFind(page, "PrefsRulesEmpty"));
        }
        finally
        {
            ClosePreferences(preferences);
        }
    }

    [Fact]
    public void CtrlDownAndCtrlUpMoveTheSelectedRow()
    {
        var preferences = OpenRules();
        try
        {
            var list = Uia.Find(preferences, "PrefsRulesList");
            var first = Uia.Find(list, Newsletters);
            Uia.Select(first);
            var before = Order(list);
            var from = Array.IndexOf(before, Newsletters);
            Assert.True(from >= 0 && from < before.Length - 1, "Newsletters has a row under it");

            Uia.PressKey(Uia.Find(list, Newsletters), App.ProcessId, VkDown, VkControl);
            Uia.WaitFor(() => Array.IndexOf(Order(list), Newsletters) == from + 1, "Ctrl+Down to move the row down");

            Uia.PressKey(Uia.Find(list, Newsletters), App.ProcessId, VkUp, VkControl);
            Uia.WaitFor(() => Order(list).SequenceEqual(before), "Ctrl+Up to move it back");

            // A move turns no rule on or off: the list's recycled switches
            // once flipped the rules of the rows they were given.
            Thread.Sleep(TimeSpan.FromSeconds(1));
            Assert.Equal(ToggleState.Off, ToggleOf(Uia.Find(Uia.Find(list, Away), "RuleEnabled")));
            Assert.Equal(ToggleState.Off, ToggleOf(Uia.Find(Uia.Find(list, Atlas), "RuleEnabled")));
            Assert.Equal(ToggleState.On, ToggleOf(Uia.Find(Uia.Find(list, Newsletters), "RuleEnabled")));
            Assert.NotNull(Uia.Find(Uia.Find(list, Atlas), "RuleProblem"));
        }
        finally
        {
            ClosePreferences(preferences);
        }
    }

    [Fact]
    public void TheSwitchOfAnOutgoingRuleAsksTurnOn()
    {
        var preferences = OpenRules();
        try
        {
            var away = Uia.Find(Uia.Find(preferences, "PrefsRulesList"), Away);
            var toggle = Uia.Find(away, "RuleEnabled");
            Assert.Equal(ToggleState.Off, ToggleOf(toggle));
            Uia.Toggle(toggle);
            // "Turn On a Rule That Sends Mail?": Turn On and Cancel.
            var turnOn = Uia.Find(preferences, "PrimaryButton", TimeSpan.FromSeconds(10));
            Assert.True(turnOn.Current.Name.Length > 0);
            Uia.Invoke(Uia.Find(preferences, "CloseButton"));
            Uia.WaitFor(() => Uia.TryFind(preferences, "PrimaryButton") is null, "the question to close");
            // Declined: the switch is put back.
            Uia.WaitFor(
                () => ToggleOf(Uia.Find(Uia.Find(Uia.Find(preferences, "PrefsRulesList"), Away), "RuleEnabled")) == ToggleState.Off,
                "the switch back off");
        }
        finally
        {
            ClosePreferences(preferences);
        }
    }

    [Fact]
    public void AddRuleOpensTheEditorWhoseSaveWaitsForAValidForm()
    {
        var preferences = OpenRules();
        AutomationElement? editor = null;
        try
        {
            editor = OpenEditor(preferences);
            var save = Uia.Find(editor, "RuleSave");
            Assert.False(save.Current.IsEnabled, "Save of an empty form");
            Assert.NotNull(Uia.Find(editor, "RuleAccount"));
            Assert.NotNull(Uia.Find(editor, "RuleMatch"));
            Assert.NotNull(Uia.Find(editor, "RuleConditionField:0"));
            Assert.NotNull(Uia.Find(editor, "RuleConditionOperator:0"));
            Assert.NotNull(Uia.Find(editor, "RuleTryFolder"));

            Uia.SetValue(Uia.Find(editor, "RuleName"), "Smoke rule");
            Uia.SetValue(Uia.Find(editor, "RuleConditionValue:0"), "someone@example.test");
            Assert.False(save.Current.IsEnabled, "Save without an action");
            AddAction(editor, "markRead");
            Uia.Find(editor, "RuleAction:0");
            Uia.WaitFor(() => save.Current.IsEnabled, "Save of a valid form");

            // Emptied, the value is a problem again and Save waits.
            Uia.SetValue(Uia.Find(editor, "RuleConditionValue:0"), "");
            Uia.WaitFor(() => !save.Current.IsEnabled, "Save disabled by an empty value");
            Assert.True(Uia.Find(editor, "RuleConditionProblem:0").Current.Name.Length > 0);

            // Cancel closes it without a question.
            Uia.Invoke(Uia.Find(editor, "RuleCancel"));
            Uia.WaitGone(editor, what: "the rule editor");
        }
        finally
        {
            CloseEditor(editor);
            ClosePreferences(preferences);
        }
    }

    [Fact]
    public void AnInvalidForwardAddressShowsItsProblem()
    {
        var preferences = OpenRules();
        AutomationElement? editor = null;
        try
        {
            editor = OpenEditor(preferences);
            Uia.SetValue(Uia.Find(editor, "RuleName"), "Forward smoke");
            Uia.SetValue(Uia.Find(editor, "RuleConditionValue:0"), "someone@example.test");
            AddAction(editor, "forward");
            var to = Uia.Find(editor, "RuleActionTo:0");
            var save = Uia.Find(editor, "RuleSave");

            Uia.SetValue(to, "not an address");
            var problem = Uia.Find(editor, "RuleActionProblem:0");
            Uia.WaitFor(() => problem.Current.Name.Length > 0, "the problem of the address");
            Assert.False(save.Current.IsEnabled, "Save with an invalid address");

            Uia.SetValue(to, "someone.else@example.test");
            Uia.WaitFor(() => Uia.TryFind(editor, "RuleActionProblem:0") is null, "the problem to go");
            Uia.WaitFor(() => save.Current.IsEnabled, "Save with a valid address");
        }
        finally
        {
            CloseEditor(editor);
            ClosePreferences(preferences);
        }
    }

    [Fact]
    public void EscapeAndCtrlWCancelTheEditor()
    {
        var preferences = OpenRules();
        AutomationElement? editor = null;
        try
        {
            editor = OpenEditor(preferences);
            Uia.SetValue(Uia.Find(editor, "RuleName"), "Escape smoke");
            Uia.PressKey(Uia.Find(editor, "RuleName"), App.ProcessId, VkEscape);
            Uia.WaitGone(editor, what: "the rule editor after Escape");

            editor = OpenEditor(preferences);
            Uia.PressKey(Uia.Find(editor, "RuleName"), App.ProcessId, VkW, VkControl);
            Uia.WaitGone(editor, what: "the rule editor after Ctrl+W");

            // Nothing was saved.
            var list = Uia.Find(preferences, "PrefsRulesList");
            Assert.DoesNotContain(Uia.All(list, ControlType.ListItem).Cast<AutomationElement>(), r => r.Current.Name == "Escape smoke");
        }
        finally
        {
            CloseEditor(editor);
            ClosePreferences(preferences);
        }
    }

    [Fact]
    public void ASavedRuleIsANewRowAndRemoveAsksFirst()
    {
        var preferences = OpenRules();
        AutomationElement? editor = null;
        try
        {
            var list = Uia.Find(preferences, "PrefsRulesList");
            var before = Order(list);
            editor = OpenEditor(preferences);
            Uia.SetValue(Uia.Find(editor, "RuleName"), "Saved smoke");
            Uia.SetValue(Uia.Find(editor, "RuleConditionValue:0"), "saved@example.test");
            AddAction(editor, "flag");
            var save = Uia.Find(editor, "RuleSave");
            Uia.WaitFor(() => save.Current.IsEnabled, "Save of a valid form");
            Uia.Invoke(save);
            Uia.WaitGone(editor, what: "the rule editor after Save");
            editor = null;

            var added = Uia.Until(() => Order(list).Except(before).FirstOrDefault(), null, "the saved rule's row");
            var row = Uia.Find(list, added);
            Assert.Equal("Saved smoke", row.Current.Name);

            // Remove asks; Cancel keeps the row.
            Uia.Invoke(Uia.Find(row, "RemoveRule"));
            Uia.Find(preferences, "PrimaryButton", TimeSpan.FromSeconds(10));
            Uia.Invoke(Uia.Find(preferences, "CloseButton"));
            Uia.WaitFor(() => Uia.TryFind(preferences, "PrimaryButton") is null, "the question to close");
            Assert.NotNull(Uia.Find(list, added));

            // Confirmed, the row goes.
            Uia.Invoke(Uia.Find(Uia.Find(list, added), "RemoveRule"));
            Uia.Invoke(Uia.Find(preferences, "PrimaryButton", TimeSpan.FromSeconds(10)));
            Uia.WaitFor(() => Uia.TryFind(list, added) is null, "the removed row to go");
            Assert.Equal(before, Order(list));
        }
        finally
        {
            CloseEditor(editor);
            ClosePreferences(preferences);
        }
    }

    // ----- helpers -----

    // The Preferences on the Rules page, from the primary menu of the mail's sidebar.
    private AutomationElement OpenRules()
    {
        var main = Main;
        if (Uia.TryFind(main, "MainMenuButton") is null)
        {
            Uia.Toggle(Uia.Find(main, "ModeMail"));
            Uia.Find(main, "MainMenuButton");
        }
        App.MenuItem("MenuPreferences");
        var preferences = Uia.WindowWith(App.ProcessId, "PreferencesNavigation");
        Uia.Select(Uia.Find(preferences, "RulesPage"));
        var list = Uia.Find(preferences, "PrefsRulesList");
        Uia.Find(list, Newsletters);
        return preferences;
    }

    // Add Rule…: the editor's window.
    private AutomationElement OpenEditor(AutomationElement preferences)
    {
        Uia.Invoke(Uia.Find(preferences, "PrefsRulesAdd"));
        return Uia.WindowWith(App.ProcessId, "RuleEditor");
    }

    // Add Action's menu and its item of <kind>; the menu is a popup, found from the desktop when not in the window.
    private static void AddAction(AutomationElement editor, string kind)
    {
        var button = Uia.Find(editor, "RuleAddAction");
        if (button.TryGetCurrentPattern(ExpandCollapsePattern.Pattern, out var expand))
        {
            ((ExpandCollapsePattern)expand).Expand();
        }
        else
        {
            Uia.Invoke(button);
        }
        var id = "RuleAddAction:" + kind;
        var item = Uia.Until(() => Uia.TryFind(editor, id) ?? Uia.TryFind(AutomationElement.RootElement, id), null, "Add Action's " + kind);
        Uia.Invoke(item);
    }

    // The rows' ids in the list's order.
    private static string[] Order(AutomationElement list) =>
        [.. Uia.All(list, ControlType.ListItem).Cast<AutomationElement>().Select(r => r.Current.AutomationId).Where(id => id.StartsWith("RuleRow:", StringComparison.Ordinal))];

    private static ToggleState ToggleOf(AutomationElement toggle) =>
        ((TogglePattern)toggle.GetCurrentPattern(TogglePattern.Pattern)).Current.ToggleState;

    private static void CloseEditor(AutomationElement? editor)
    {
        if (editor is not null && !Uia.IsGone(editor))
        {
            Uia.Close(editor);
            Uia.WaitGone(editor, what: "the rule editor");
        }
    }

    private static void ClosePreferences(AutomationElement preferences)
    {
        if (!Uia.IsGone(preferences))
        {
            Uia.Close(preferences);
            Uia.WaitGone(preferences, what: "the Preferences");
        }
    }
}
