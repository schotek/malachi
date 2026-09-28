// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Mnemonic (Core/Presentation): mn() of macos/Sources/MalachiMail/App/
// Actions.swift, plus the access key Windows keeps (GTK use-underline).

using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class MnemonicTests
{
    [Theory]
    [InlineData("_New Message", "New Message", "N")]
    [InlineData("_Add Account…", "Add Account…", "A")]
    [InlineData("Move to _Trash", "Move to Trash", "T")]
    [InlineData("Mark as _Unread", "Mark as Unread", "U")]
    [InlineData("Save _Anyway", "Save Anyway", "A")]
    [InlineData("_uložit", "uložit", "U")]
    [InlineData("_čeština", "čeština", "Č")]
    [InlineData("No mnemonic", "No mnemonic", null)]
    [InlineData("snake__case", "snake_case", null)]
    [InlineData("__init_ fn", "_init fn", null)]
    [InlineData("_a _b", "a _b", "A")]
    [InlineData("trailing_", "trailing_", null)]
    [InlineData("_ space", " space", null)]
    [InlineData("", "", null)]
    public void Parses(string text, string label, string? key)
    {
        var m = Mnemonic.Parse(text);
        Assert.Equal(label, m.Label);
        Assert.Equal(key, m.AccessKey);
        Assert.Equal(label, Mnemonic.Strip(text));
    }

    [Fact]
    public void AMarkedSurrogatePairHasNoAccessKeyButKeepsItsText()
    {
        var m = Mnemonic.Parse("_\U0001F4E7 Mail");
        Assert.Equal("\U0001F4E7 Mail", m.Label);
        Assert.Null(m.AccessKey);
    }
}
