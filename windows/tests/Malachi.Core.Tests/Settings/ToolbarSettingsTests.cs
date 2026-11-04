// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only tests of SettingsStore's toolbar accessors (ToolbarLayout,
// SetToolbarLayout, ToolbarTokens, SetToolbarTokens, ToolbarDisplayMode,
// ToolbarKey; docs/plans/toolbar-customization.md W.3 and §17.4): an empty
// key is the Windows default, the Windows default over an empty key writes
// nothing, a save keeps the items another client has, an unknown nick reads
// as icons only, and a write fires the key's handlers.

using Malachi.Core.Settings;
using Malachi.Core.Toolbars;
using Xunit;
using static Malachi.Core.Tests.Toolbars.ToolbarTests;

namespace Malachi.Core.Tests.Settings;

public sealed class ToolbarSettingsTests
{
    [Fact]
    public void AnEmptyKeyIsTheWindowsDefault()
    {
        var s = Scratch();
        foreach (var bar in Toolbar.Bars)
        {
            Assert.Empty(s.ToolbarTokens(bar));
            Assert.Equal(Join(Toolbar.DefaultFor(ToolbarVariant.Windows, bar)), Join(s.ToolbarLayout(bar)));
            Assert.Equal(Toolbar.Key(bar), SettingsStore.Name(SettingsStore.ToolbarKey(bar)));
        }
        Assert.Equal(Toolbar.DisplayMode.Icon, s.ToolbarDisplayMode);
    }

    [Fact]
    public void TheDefaultStoresNothingAndAnEditRoundTrips()
    {
        var s = Scratch();
        var fired = 0;
        s.OnChange(SettingsKey.ToolbarMail, () => fired++);
        s.SetToolbarLayout(Toolbar.Bar.Mail, Toolbar.DefaultFor(ToolbarVariant.Windows, Toolbar.Bar.Mail));
        Assert.Empty(s.ToolbarTokens(Toolbar.Bar.Mail));
        Assert.Equal(0, fired);
        var edited = Ids("mode newMessage |list refresh |message reply archive junk search");
        s.SetToolbarLayout(Toolbar.Bar.Mail, edited);
        Assert.Equal(1, fired);
        Assert.Equal(Join(edited), Join(s.ToolbarLayout(Toolbar.Bar.Mail)));
        // Restore Defaults writes the empty list.
        s.SetToolbarTokens(Toolbar.Bar.Mail, []);
        Assert.Empty(s.ToolbarTokens(Toolbar.Bar.Mail));
        Assert.Equal(2, fired);
    }

    [Fact]
    public void ASaveKeepsWhatAnotherClientStored()
    {
        // GTK put the search into the list section; Windows allows it there too.
        var s = Scratch();
        s.SetToolbarTokens(Toolbar.Bar.Mail, Ids("mode |list refresh search |message reply"));
        Assert.Equal("mode |list refresh search |message reply", Join(s.ToolbarLayout(Toolbar.Bar.Mail)));
        s.SetToolbarLayout(Toolbar.Bar.Mail, Ids("mode |list refresh search |message reply star"));
        Assert.Equal("mode |list refresh search |message reply star", Join(s.ToolbarTokens(Toolbar.Bar.Mail)));
    }

    [Fact]
    public void TheDisplayModeRoundTrips()
    {
        var s = Scratch();
        var fired = 0;
        s.OnChange(SettingsKey.ToolbarDisplayMode, () => fired++);
        s.ToolbarDisplayMode = Toolbar.DisplayMode.IconAndLabel;
        Assert.Equal(Toolbar.DisplayMode.IconAndLabel, s.ToolbarDisplayMode);
        Assert.Equal(1, fired);
        s.ToolbarDisplayMode = Toolbar.DisplayMode.IconAndLabel;
        Assert.Equal(1, fired);
        s.Backend.SetString("toolbar-display-mode", "bogus");
        Assert.Equal(Toolbar.DisplayMode.Icon, s.ToolbarDisplayMode);
    }

    private static SettingsStore Scratch() => new(new InMemorySettingsBackend(), null);
}
