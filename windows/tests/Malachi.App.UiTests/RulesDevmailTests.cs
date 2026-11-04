// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The rules' two context-menu items (docs/plans/rules.md R14;
// docs/windows-port.md §11.17) against devmail's seeded mailbox with the
// invented rules (RulesDevmailFixture, opt-in with MALACHI_DEVMAIL): Create
// Rule from Message… of a message's menu (Shift+F10 on its row) opens the
// rule editor with the sender's address (or the list's id) as its condition
// and a name; Apply Rules… of the Inbox's menu asks first, then shows the
// result as a toast (the samples' rules.apply changed 12 messages). Which
// messages the Inbox holds depends on the devmail (its seed or the corpus):
// the tests take the first row and compare with its reader.

using System;
using System.Linq;
using System.Windows.Automation;
using Xunit;

namespace Malachi.App.UiTests;

/// <summary>The rules' context-menu items against a local mail server.</summary>
[Collection(OneAppAtATime.Name)]
public sealed class RulesDevmailTests(RulesDevmailFixture fixture) : IClassFixture<RulesDevmailFixture>
{
    private const byte VkShift = 0x10;
    private const byte VkF10 = 0x79;

    private AppSession App
    {
        get
        {
            Assert.SkipWhen(fixture.SkipReason is not null, fixture.SkipReason ?? "");
            return fixture.Session;
        }
    }

    [Fact]
    public void CreateRuleFromMessageOpensTheEditorPrefilled()
    {
        var main = App.MainWindow;
        // The Inbox's first message (devmail's seed or the corpus: whichever this devmail holds).
        var row = Uia.Until(() => Rows().FirstOrDefault(), null, "a row in the Inbox");
        var name = row.Current.Name;
        Uia.Select(row);
        Uia.WaitFor(() => Uia.TryFind(main, "MessageSubject")?.Current.Name.Length > 0, "the reader to show the message");
        var senders = Uia.All(Uia.Find(main, "FromChips"), ControlType.Button).Cast<AutomationElement>().Select(b => b.Current.Name).ToList();
        Uia.PressKey(Uia.Until(() => Rows().FirstOrDefault(r => r.Current.Name == name), null, "the row again"), App.ProcessId, VkF10, VkShift);
        Uia.Invoke(MenuItem(main, "ContextMenu.createRule"));

        var editor = Uia.WindowWith(App.ProcessId, "RuleEditor");
        try
        {
            // From is the sender's address (or List-Id the list's, for a mailing list).
            var value = Uia.Value(Uia.Find(editor, "RuleConditionValue:0"));
            Assert.True(value.Length > 0, "the condition's value from the message");
            Assert.True(
                senders.Any(s => s.Contains(value, StringComparison.OrdinalIgnoreCase)) || !value.Contains('@', StringComparison.Ordinal),
                $"the condition '{value}' is the sender of {string.Join(", ", senders)} or a list id");
            Assert.True(Uia.Value(Uia.Find(editor, "RuleName")).Length > 0, "the rule's name from the sender");
            // No action yet: Save waits for one.
            Assert.False(Uia.Find(editor, "RuleSave").Current.IsEnabled);
            Uia.Invoke(Uia.Find(editor, "RuleCancel"));
            Uia.WaitGone(editor, what: "the rule editor");
        }
        finally
        {
            if (!Uia.IsGone(editor))
            {
                Uia.Close(editor);
            }
        }
    }

    [Fact]
    public void ApplyRulesOnTheInboxAsksThenToastsTheResult()
    {
        var main = App.MainWindow;
        var folders = Uia.Find(main, "FolderList");
        // The Inbox, which the mailbox selected for the new account.
        var inbox = Uia.Until(
            () => ((SelectionPattern)folders.GetCurrentPattern(SelectionPattern.Pattern)).Current.GetSelection().FirstOrDefault(),
            null,
            "the selected folder");
        Uia.PressKey(inbox, App.ProcessId, VkF10, VkShift);
        var apply = MenuItem(main, "ContextMenu.applyRules");
        Assert.True(apply.Current.IsEnabled, "Apply Rules… with an enabled rule for every account");
        Uia.Invoke(apply);

        // "Apply Rules to “Inbox”?": Apply and Cancel.
        Uia.Invoke(Uia.Find(main, "PrimaryButton", TimeSpan.FromSeconds(10)));
        // A toast may wait behind the one before it (ToastPresenter), and
        // build.ps1 test runs every test project at once.
        var toast = Uia.Find(main, "ToastText", TimeSpan.FromSeconds(30));
        Uia.WaitFor(() => toast.Current.Name.Contains("12", StringComparison.Ordinal), "the toast of the result");
    }

    // A context menu's item: the flyout is part of the window's tree, or a popup of its own.
    private static AutomationElement MenuItem(AutomationElement window, string id) =>
        Uia.Until(() => Uia.TryFind(window, id) ?? Uia.TryFind(AutomationElement.RootElement, id), null, "the menu item " + id);

    private System.Collections.Generic.IEnumerable<AutomationElement> Rows() =>
        Uia.All(Uia.Find(App.MainWindow, "MessageList"), ControlType.ListItem).Cast<AutomationElement>();
}
