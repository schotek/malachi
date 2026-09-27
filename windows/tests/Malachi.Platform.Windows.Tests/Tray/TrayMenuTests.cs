// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of TrayMenu: the menu measured in APP-SPIKES §4.2 (Open in bold, New
// Message, Check for New Mail, a separator, Quit) with GTK's labels where
// GTK has them, in the current language, and GTK mnemonics as Win32 access
// keys. The Czech case reads po/cs.po as the app does.

using System;
using System.IO;
using System.Linq;
using Malachi.Core.I18n;
using Malachi.Platform.Windows.Tray;
using Xunit;

namespace Malachi.Platform.Windows.Tests.Tray;

public sealed class TrayMenuTests
{
    [Fact]
    public void OpenNewMessageCheckForNewMailAndQuit()
    {
        var items = TrayMenu.Items();
        Assert.Equal(
            [TrayCommand.Open, TrayCommand.NewMessage, TrayCommand.CheckForNewMail, TrayCommand.None, TrayCommand.Quit],
            items.Select(i => i.Command));
        Assert.Equal(["&Open Malachi Mail", "&New Message", "Check for New Mail", null, "&Quit"], items.Select(i => i.Text));
        Assert.Equal([TrayCommand.Open], items.Where(i => i.IsDefault).Select(i => i.Command));
        Assert.True(items[3].IsSeparator);
    }

    /// <summary>
    /// The GTK labels have translations whose mnemonics survive (read from
    /// po/cs.po without touching the process-wide catalogue, which other
    /// tests read at the same time).
    /// </summary>
    [Fact]
    public void TheGtkLabelsAreTranslated()
    {
        var cs = Catalogue.Load(Path.Combine(RepositoryRoot(), "po"), ["cs"]);
        var newMessage = TrayMenu.FromGtkLabel(cs.Translate("_New Message"));
        Assert.NotEqual("&New Message", newMessage);
        Assert.Equal(1, newMessage.Count(c => c == '&'));
        Assert.NotEqual("Check for New Mail", cs.Translate("Check for New Mail"));
    }

    [Theory]
    [InlineData("_New Message", "&New Message")]
    [InlineData("Check for New Mail", "Check for New Mail")]
    [InlineData("Load _Images", "Load &Images")]
    [InlineData("_Nová zpráva", "&Nová zpráva")]
    [InlineData("Save __all", "Save _all")]
    [InlineData("_First _Second", "&First Second")]
    [InlineData("Tom & Jerry", "Tom && Jerry")]
    [InlineData("_&Amp", "&&Amp")]
    [InlineData("trailing_", "trailing_")]
    [InlineData("", "")]
    public void AGtkMnemonicBecomesAnAccessKey(string gtk, string win32)
    {
        Assert.Equal(win32, TrayMenu.FromGtkLabel(gtk));
    }

    private static string RepositoryRoot()
    {
        for (var dir = new DirectoryInfo(AppContext.BaseDirectory); dir is not null; dir = dir.Parent)
        {
            if (File.Exists(Path.Combine(dir.FullName, "po", "LINGUAS")))
            {
                return dir.FullName;
            }
        }
        throw new InvalidOperationException("the repository's po/ was not found");
    }
}
