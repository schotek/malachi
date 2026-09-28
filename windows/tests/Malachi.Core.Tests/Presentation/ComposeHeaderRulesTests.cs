// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of Malachi.Core.Presentation.ComposeHeaderRules, the rules of
// ui/internal/compose/compose.go (setAccounts, fromFactory, fromLocked,
// setCcBccVisible, updateTitle) and ComposeWindowController.swift, which
// neither side tests.

using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.Presentation;
using Malachi.Core.Tests.Model;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class ComposeHeaderRulesTests
{
    private static readonly Account One = MailModelTests.TestAccount("acc1", email: "one@example.org", displayName: "One");
    private static readonly Account Two = MailModelTests.TestAccount("acc2", email: "two@example.org");
    private static readonly Account Three = MailModelTests.TestAccount("acc3", email: "three@example.org", displayName: "Three");

    [Fact]
    public void TheFromLabelIsTheDisplayNameOrTheAccountsName()
    {
        // The name isolated from the address (DisplayText, Windows-only).
        Assert.Equal("\u2068One\u2069 <one@example.org>", ComposeHeaderRules.FromLabel(One));
        var named = Two with { Config = Two.Config with { Name = "Work", DisplayName = null } };
        Assert.Equal("\u2068Work\u2069 <two@example.org>", ComposeHeaderRules.FromLabel(named));
        var bare = Two with { Config = Two.Config with { Name = "", DisplayName = "" } };
        Assert.Equal("two@example.org", ComposeHeaderRules.FromLabel(bare));
        Assert.Equal("\u2068Malachi User\u2069 <me@example.invalid>", ComposeHeaderRules.FromLabel(Malachi.Core.Controllers.ComposeController.PlaceholderAccounts[0]));
    }

    [Fact]
    public void TheChosenIdentityWinsThenTheAccountOpenedForThenTheFirst()
    {
        Account[] accounts = [One, Two, Three];
        Assert.Equal((2, true), ComposeHeaderRules.FromSelection(accounts, "acc3", "acc2"));
        Assert.Equal((1, true), ComposeHeaderRules.FromSelection(accounts, null, "acc2"));
        Assert.Equal((0, false), ComposeHeaderRules.FromSelection(accounts, null, null));
        Assert.Equal((0, false), ComposeHeaderRules.FromSelection(accounts, null, "gone"));
        // A chosen identity no longer listed falls back to the first, as GTK's
        // selected index stays 0 when nothing matches.
        Assert.Equal((0, false), ComposeHeaderRules.FromSelection(accounts, "gone", "acc2"));
        Assert.Equal((0, false), ComposeHeaderRules.FromSelection([], "acc1", "acc1"));
    }

    [Fact]
    public void RepliesAndForwardsKeepTheirAccount()
    {
        Assert.False(ComposeHeaderRules.FromLocked(new ComposeParams { Kind = ComposeKind.New }));
        Assert.True(ComposeHeaderRules.FromLocked(new ComposeParams { Kind = ComposeKind.Reply, InReplyTo = "m1" }));
        Assert.True(ComposeHeaderRules.FromLocked(new ComposeParams { Kind = ComposeKind.Forward, Forwarding = "m1" }));
        Assert.True(ComposeHeaderRules.FromLocked(new ComposeParams { Kind = ComposeKind.Edit, InReplyTo = "m1" }));
        Assert.False(ComposeHeaderRules.FromLocked(new ComposeParams { Kind = ComposeKind.Edit }));
    }

    [Theory]
    [InlineData(1, false, false, false)]
    [InlineData(2, false, false, true)]
    [InlineData(2, false, true, true)]
    [InlineData(2, true, true, false)]
    [InlineData(2, true, false, true)]
    [InlineData(0, false, false, false)]
    public void FromTakesAChoiceOnlyWithOne(int count, bool locked, bool found, bool enabled) =>
        Assert.Equal(enabled, ComposeHeaderRules.FromEnabled(count, locked, found));

    [Theory]
    [InlineData(false, false, true)]
    [InlineData(true, false, true)]
    [InlineData(false, true, true)]
    [InlineData(true, true, false)]
    public void TheCcBccButtonStaysWhileALineIsHidden(bool cc, bool bcc, bool visible) =>
        Assert.Equal(visible, ComposeHeaderRules.CcBccButtonVisible(cc, bcc));

    [Theory]
    [InlineData("", "New Message")]
    [InlineData("   ", "New Message")]
    [InlineData("  Hello  ", "Hello")]
    [InlineData("Re: Lunch", "Re: Lunch")]
    // Windows-only: a reply's subject is the sender's text, cleaned for the
    // caption (DisplayText).
    [InlineData("Re: " + Text.DisplayTextTests.HostileSubject, "Re: " + Text.DisplayTextTests.CleanedSubject)]
    [InlineData("\u202E\u0007", "New Message")]
    [InlineData("\u200F\u200B", "New Message")]
    public void TheTitleIsTheSubject(string subject, string title) =>
        Assert.Equal(title, ComposeHeaderRules.WindowTitle(subject));
}
