// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The smoke tests of docs/windows-port.md §12 over the published app with
// no account: the main window with its No Accounts page, the entry points
// of the other windows (the sidebar's New Message, the primary menu's Add
// Account…, Preferences and About, the No Accounts page's Add Account…),
// each window found by an element of its own and closed again. Elements are
// found by AutomationId: the names follow the user's language.

using System.Diagnostics;
using System.Windows.Automation;
using Xunit;

namespace Malachi.App.UiTests;

/// <summary>The main window and the windows it opens.</summary>
[Collection(OneAppAtATime.Name)]
public sealed class SmokeTests(SmokeFixture fixture) : IClassFixture<SmokeFixture>
{
    private AppSession App
    {
        get
        {
            Assert.SkipWhen(fixture.SkipReason is not null, fixture.SkipReason ?? "");
            return fixture.Session;
        }
    }

    [Fact]
    public void TheMainWindowShowsTheNoAccountsPage()
    {
        var main = App.MainWindow;
        Assert.True(Uia.Find(main, "NewMessageButton").Current.IsEnabled);
        Assert.NotNull(Uia.Find(main, "MainMenuButton"));
        // account.list answered with nothing (the daemon was started and connected).
        var add = Uia.Find(main, "AddAccountButton", AppSession.StartTimeout);
        Uia.WaitFor(() => !add.Current.IsOffscreen && add.Current.IsEnabled, "the No Accounts page");
        Assert.NotNull(App.Daemon());
    }

    [Fact]
    public void NewMessageOpensAComposerThatCloses()
    {
        Uia.Invoke(Uia.Find(App.MainWindow, "NewMessageButton"));
        var composer = Uia.WindowWith(App.ProcessId, "ComposeSend");
        Assert.NotNull(Uia.Find(composer, "ComposeTo"));
        Assert.NotNull(Uia.Find(composer, "ComposeSubject"));
        Assert.NotNull(Uia.Find(composer, "ComposeEditor"));
        // Nothing at stake: it closes without a question.
        Uia.Close(composer);
        Uia.WaitGone(composer, what: "the composer");
    }

    [Fact]
    public void ThePrimaryMenuOpensThePreferences()
    {
        App.MenuItem("MenuPreferences");
        var preferences = Uia.WindowWith(App.ProcessId, "PreferencesNavigation");
        Assert.NotNull(Uia.Find(preferences, "AccountsPage"));
        Assert.NotNull(Uia.Find(preferences, "GeneralPage"));
        Uia.Close(preferences);
        Uia.WaitGone(preferences, what: "the Preferences");
    }

    [Fact]
    public void ThePrimaryMenuOpensTheWizardOnItsFirstPage()
    {
        App.MenuItem("MenuAddAccount");
        AssertWizardOnItsFirstPage();
    }

    [Fact]
    public void TheNoAccountsPageOpensTheWizard()
    {
        var add = Uia.Find(App.MainWindow, "AddAccountButton", AppSession.StartTimeout);
        Uia.WaitFor(() => add.Current.IsEnabled, "Add Account… to be enabled");
        Uia.Invoke(add);
        AssertWizardOnItsFirstPage();
    }

    [Fact]
    public void AboutShowsTheVersion()
    {
        var version = FileVersionInfo.GetVersionInfo(UiEnvironment.AppExecutable).ProductVersion;
        Assert.False(string.IsNullOrEmpty(version));
        App.MenuItem("MenuAbout");
        var main = App.MainWindow;
        var shown = Uia.Until(
            () => main.FindFirst(TreeScope.Descendants, new PropertyCondition(AutomationElement.NameProperty, version)),
            null,
            "About with the version " + version);
        Assert.Equal(ControlType.Text, shown.Current.ControlType);
        // The dialog's own button (ContentDialog's CloseButton).
        Uia.Invoke(Uia.Find(main, "CloseButton"));
        Uia.WaitFor(() => Uia.TryFind(main, "CloseButton") is null, "About to close");
    }

    // The identity page: the address and the password, and Next; closed
    // again (Alt+F4's way), which gives the main window back.
    private void AssertWizardOnItsFirstPage()
    {
        var wizard = Uia.WindowWith(App.ProcessId, "Email");
        Assert.NotNull(Uia.Find(wizard, "Password"));
        Assert.NotNull(Uia.Find(wizard, "Next"));
        Uia.Close(wizard);
        Uia.WaitGone(wizard, what: "the wizard");
        var main = App.MainWindow;
        Uia.WaitFor(() => main.Current.IsEnabled, "the main window to be enabled again");
    }
}
