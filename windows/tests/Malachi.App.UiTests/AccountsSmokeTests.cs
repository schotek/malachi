// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The smoke tests of the account list of Preferences → Accounts
// (AccountListSection, which Preferences → Integrations shares;
// docs/windows-port.md §7.5) over the published app with four mail
// accounts, two of them paused (AccountsSmokeFixture): Ctrl+Down and Ctrl+Up
// move the selected row, and neither they nor a reorder by another client
// turn any account on or off. The list once did: when it gave a recycled
// container another row, x:Bind set the switch before the template's
// DataContext followed, and the page took the flip for a click on the
// container's former account. A row's switch still pauses and resumes its
// own account. The daemon is the truth (account.list), the rows' switches
// must agree with it. Elements are found by AutomationId (the names follow
// the user's language), the rows by their titles, the fixture's account
// names; the accounts' ids are data.

using System;
using System.Linq;
using System.Threading.Tasks;
using System.Windows.Automation;
using Xunit;

namespace Malachi.App.UiTests;

/// <summary>The account list of Preferences → Accounts over four mail accounts.</summary>
[Collection(OneAppAtATime.Name)]
public sealed class AccountsSmokeTests(AccountsSmokeFixture fixture) : IClassFixture<AccountsSmokeFixture>
{
    private const byte VkControl = 0x11;
    private const byte VkUp = 0x26;
    private const byte VkDown = 0x28;

    // How long a recycled container's flip may take to reach the daemon.
    private static readonly TimeSpan Settle = TimeSpan.FromSeconds(2);

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
    public async Task CtrlDownAndCtrlUpMoveTheSelectedRowAndNoAccountsState()
    {
        var preferences = OpenAccounts();
        try
        {
            var list = Uia.Find(preferences, "AccountList");
            var before = Order(list);
            var from = Array.IndexOf(before, "Alpha");
            Assert.True(from >= 0 && from < before.Length - 1, "Alpha has a row under it");
            Uia.Select(Row(list, "Alpha"));

            Uia.PressKey(Row(list, "Alpha"), App.ProcessId, VkDown, VkControl);
            Uia.WaitFor(() => Array.IndexOf(Order(list), "Alpha") == from + 1, "Ctrl+Down to move the row down");
            await AssertNoAccountChangedAsync(list);

            Uia.PressKey(Row(list, "Alpha"), App.ProcessId, VkUp, VkControl);
            Uia.WaitFor(() => Order(list).SequenceEqual(before), "Ctrl+Up to move it back");
            await AssertNoAccountChangedAsync(list);
        }
        finally
        {
            ClosePreferences(preferences);
        }
    }

    // Another client's account.reorder reaches the open page as
    // notify.accountsChanged: the page loads the accounts again and moves
    // its rows, which the user did not touch.
    [Fact]
    public async Task AReorderByAnotherClientChangesNoAccountsState()
    {
        var preferences = OpenAccounts();
        try
        {
            var list = Uia.Find(preferences, "AccountList");
            string[] names = [.. AccountsSmokeFixture.Accounts.Select(a => a.Name)];
            string[] reversed = [.. names.AsEnumerable().Reverse()];
            var before = Order(list);
            Assert.Equal(names, before);

            await fixture.ReorderAsync(reversed);
            Uia.WaitFor(() => Order(list).SequenceEqual(reversed), "the rows to follow the new order");
            await AssertNoAccountChangedAsync(list);

            await fixture.ReorderAsync(names);
            Uia.WaitFor(() => Order(list).SequenceEqual(before), "the rows to follow the order back");
            await AssertNoAccountChangedAsync(list);
        }
        finally
        {
            ClosePreferences(preferences);
        }
    }

    // The page judges a flip once the list has settled: the user's still
    // pauses and resumes the row's account, and no other.
    [Fact]
    public async Task TheSwitchPausesAndResumesItsOwnAccount()
    {
        var preferences = OpenAccounts();
        try
        {
            var list = Uia.Find(preferences, "AccountList");
            var toggle = Uia.Find(Row(list, "Bravo"), "Enabled");
            Uia.WaitFor(() => toggle.Current.IsEnabled, "Bravo's switch to take input");
            Uia.Toggle(toggle);
            await WaitForDaemonAsync("Bravo", enabled: false);

            // The row is insensitive while its call runs.
            Uia.WaitFor(() => toggle.Current.IsEnabled, "Bravo's switch to take input again");
            Uia.Toggle(toggle);
            await WaitForDaemonAsync("Bravo", enabled: true);
            await AssertNoAccountChangedAsync(list);
        }
        finally
        {
            ClosePreferences(preferences);
        }
    }

    // ----- helpers -----

    // The Preferences on the Accounts page, from the primary menu of the
    // mail's sidebar, once every account has its row.
    private AutomationElement OpenAccounts()
    {
        var main = Main;
        if (Uia.TryFind(main, "MainMenuButton") is null)
        {
            Uia.Toggle(Uia.Find(main, "ModeMail"));
            Uia.Find(main, "MainMenuButton");
        }
        App.MenuItem("MenuPreferences");
        var preferences = Uia.WindowWith(App.ProcessId, "PreferencesNavigation");
        Uia.Select(Uia.Find(preferences, "AccountsPage"));
        var list = Uia.Find(preferences, "AccountList");
        Uia.WaitFor(() => Order(list).Length == AccountsSmokeFixture.Accounts.Count, "a row for every account");
        return preferences;
    }

    // No account was turned on or off, by the daemon's account.list, and
    // every row's switch shows its account's state, once a flip of a
    // recycled container would have reached the daemon.
    private async Task AssertNoAccountChangedAsync(AutomationElement list)
    {
        await Task.Delay(Settle, TestContext.Current.CancellationToken);
        var accounts = await fixture.ListAsync();
        foreach (var (name, enabled) in AccountsSmokeFixture.Accounts)
        {
            var account = accounts.Single(a => a.Id == fixture.Ids[name]);
            Assert.True(account.Enabled == enabled, $"{name} is {(account.Enabled ? "enabled" : "paused")} in the daemon");
            var toggle = Uia.Find(Row(list, name), "Enabled");
            Assert.True(ToggleOf(toggle) == (enabled ? ToggleState.On : ToggleState.Off), $"{name}'s switch shows {ToggleOf(toggle)}");
        }
    }

    // Waits for the daemon to have the account <name> enabled or paused.
    private async Task WaitForDaemonAsync(string name, bool enabled)
    {
        var deadline = DateTime.UtcNow + Uia.Timeout;
        while ((await fixture.ListAsync()).Single(a => a.Id == fixture.Ids[name]).Enabled != enabled)
        {
            Assert.True(DateTime.UtcNow < deadline, $"{name} to be {(enabled ? "enabled" : "paused")} in the daemon");
            await Task.Delay(TimeSpan.FromMilliseconds(200), TestContext.Current.CancellationToken);
        }
    }

    // The row of the account <name>, by its title.
    private static AutomationElement Row(AutomationElement list, string name) =>
        Uia.Until(() => Rows(list).FirstOrDefault(r => r.Name == name).Row, null, name + "'s row");

    // The accounts' names in the list's order.
    private static string[] Order(AutomationElement list) => [.. Rows(list).Select(r => r.Name)];

    // The list's rows with the name of their account: the row's text that
    // is one of the fixture's names (its title; the address is not).
    private static (string Name, AutomationElement Row)[] Rows(AutomationElement list) =>
        [.. Uia.All(list, ControlType.ListItem).Cast<AutomationElement>()
            .Select(r => (Name: Uia.All(r, ControlType.Text).Cast<AutomationElement>().Select(t => t.Current.Name).FirstOrDefault(AccountsSmokeFixture.Names.Contains), Row: r))
            .Where(r => r.Name is not null)
            .Select(r => (r.Name!, r.Row))];

    private static ToggleState ToggleOf(AutomationElement toggle) =>
        ((TogglePattern)toggle.GetCurrentPattern(TogglePattern.Pattern)).Current.ToggleState;

    private static void ClosePreferences(AutomationElement preferences)
    {
        if (!Uia.IsGone(preferences))
        {
            Uia.Close(preferences);
            Uia.WaitGone(preferences, what: "the Preferences");
        }
    }
}
