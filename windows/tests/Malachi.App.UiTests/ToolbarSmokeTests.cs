// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The toolbar customization's smoke tests (docs/plans/toolbar-customization.md
// W.11, §19) over the published app with no account: Preferences →
// General → Customize Toolbar… opens the editor on the Mail bar; Add Item…
// puts Contacts into the Folders section and the sidebar's command row
// shows it at once; Restore Default (after its question) takes it away
// again; Close ends the dialog. Elements by AutomationId only.

using System.Windows.Automation;
using Xunit;

namespace Malachi.App.UiTests;

/// <summary>The toolbar editor of the Preferences and the bars it arranges.</summary>
[Collection(OneAppAtATime.Name)]
public sealed class ToolbarSmokeTests(SmokeFixture fixture) : IClassFixture<SmokeFixture>
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
    public void TheEditorAddsAnItemAndRestoresTheDefault()
    {
        var app = App;
        var main = app.MainWindow;
        Assert.Null(Uia.TryFind(main, "SidebarContactsButton"));
        app.MenuItem("MenuPreferences");
        var preferences = Uia.WindowWith(app.ProcessId, "PreferencesNavigation");
        try
        {
            Uia.Select(Uia.Find(preferences, "GeneralPage"));
            Uia.Invoke(Uia.Find(preferences, "CustomizeToolbar"));
            Assert.NotNull(Uia.Find(preferences, "ToolbarEditorRows"));

            // Add Item… without a selection offers the first section's
            // items: Contacts goes into the Folders section.
            Uia.Invoke(Uia.Find(preferences, "ToolbarEditorAdd"));
            Uia.Invoke(Uia.Find(preferences, "ToolbarEditorAdd.contacts"));
            Uia.Find(main, "SidebarContactsButton");

            // Restore Default asks first.
            Uia.Invoke(Uia.Find(preferences, "ToolbarEditorRestore"));
            Uia.Invoke(Uia.Find(preferences, "ToolbarEditorRestoreConfirm"));
            Uia.WaitFor(() => Uia.TryFind(main, "SidebarContactsButton") is null, "the default bar without Contacts");

            // The dialog's Close.
            Uia.Invoke(Uia.Find(preferences, "CloseButton"));
            Uia.WaitFor(() => Uia.TryFind(preferences, "ToolbarEditorRows") is null, "the editor to close");
        }
        finally
        {
            Uia.Close(preferences);
            Uia.WaitGone(preferences, what: "the Preferences");
        }
    }
}
