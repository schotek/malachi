// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only tests: the main window's caption that follows the list's
// title (ListHeading.Caption; windows/README.md, the deviation table). A
// folder's name is the server's text: cleaned by FolderTree.FolderTitle,
// and isolated in the caption, so a right-to-left name cannot move the
// app's name (docs/security.md §4).

using Malachi.Core.Controllers;
using Xunit;

namespace Malachi.Core.Tests.Controllers;

public sealed class ListHeadingTests
{
    [Fact]
    public void TheCaptionIsTheIsolatedTitleAndTheAppsName()
    {
        Assert.Equal("\u2068Inbox\u2069 – Malachi Mail", new ListHeading("Inbox", "2 unread of 10").Caption);
        Assert.Equal("\u2068חשבוניות\u2069 – Malachi Mail", new ListHeading("חשבוניות", "").Caption);
        // A title the server gave an override loses it (FolderTitle cleans
        // it already; the isolate cleans again).
        Assert.Equal("\u2068gnp.exe\u2069 – Malachi Mail", new ListHeading("\u202Egnp.exe", "").Caption);
    }

    [Theory]
    [InlineData("")]
    [InlineData("  ")]
    [InlineData("\u202E")]
    [InlineData("\u200F\u200B")]
    public void ATitleThatShowsNothingLeavesTheAppsName(string title) =>
        Assert.Equal("Malachi Mail", new ListHeading(title, "").Caption);
}
