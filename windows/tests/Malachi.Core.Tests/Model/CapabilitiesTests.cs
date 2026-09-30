// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CapabilitiesTests.swift, the
// counterpart of ui/internal/capabilities/capabilities_test.go (TestCan,
// TestSupported, TestAvailable, TestForwardAccounts, TestComposeAccounts).

using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Xunit;

namespace Malachi.Core.Tests.Model;

public sealed class CapabilitiesTests
{
    private static Account Acc(string id, bool enabled, Capability[]? capabilities, string kind = "") => new()
    {
        Id = id,
        Config = new AccountConfig { Name = "", Email = "jana@acme.example", Kind = kind.Length == 0 ? null : (AccountKind?)new AccountKind(kind) },
        Enabled = enabled,
        State = new SyncState { AccountId = id, Status = SyncStatus.Idle },
        Capabilities = capabilities,
    };

    // Accounts as the daemon sends them. OldMail is a mail account of a
    // daemon from before capabilities; JiraM1 reads and flags only; JiraM2
    // comments and forwards into a mail account.
    private static readonly Account OldMail = Acc("a1", true, null);
    private static readonly Account Mail = Acc("a2", true, [.. API.MailCapabilities]);
    private static readonly Account JiraM1 = Acc("j1", true, [], AccountKind.Jira);
    private static readonly Account JiraM2 = Acc("j2", true, [Capability.Comment, Capability.Forward], AccountKind.Jira);
    private static readonly Account Paused = Acc("p", false, null);

    private static readonly Capability[] All =
    [
        Capability.Compose, Capability.Reply, Capability.ReplyAll, Capability.Forward,
        Capability.Comment, Capability.Move, Capability.Delete, "unknown", "transition",
    ];

    [Fact]
    public void Can()
    {
        Capability[] mailSet = [Capability.Compose, Capability.Reply, Capability.ReplyAll, Capability.Forward, Capability.Move, Capability.Delete];
        foreach (var (name, acc, want) in new (string, Account, Capability[])[]
        {
            ("nil means mail", OldMail, mailSet),
            ("mail", Mail, mailSet),
            ("empty means none", JiraM1, []),
            ("jira comments and forwards", JiraM2, [Capability.Comment, Capability.Forward]),
            ("unknown values", Acc("x", true, ["transition"]), ["transition"]),
        })
        {
            foreach (var c in All)
            {
                Assert.True(want.Contains(c) == Capabilities.Can(acc, c), $"{name}: Can({c})");
            }
        }
    }

    private static readonly CapabilityActions Everything = new()
    {
        Reply = true,
        ReplyAll = true,
        Forward = true,
        Move = true,
        Trash = true,
        Archive = true,
        Junk = true,
    };

    public static TheoryData<string, CapabilitySituation, CapabilityActions> SupportedCases => new()
    {
        { "no folder listed", new(), Everything },
        { "old daemon", new() { Account = OldMail }, Everything },
        { "mail", new() { Account = Mail }, Everything },
        { "mail without another compose account", new() { Account = Mail, ComposeAccount = false }, Everything },
        { "jira M1", new() { Account = JiraM1, ComposeAccount = true }, new() },
        { "jira M1 outbox", new() { Account = JiraM1, Outbox = true }, new() { Trash = true } },
        { "jira M2", new() { Account = JiraM2, ComposeAccount = true }, new() { Reply = true, Forward = true, Comment = true } },
        { "jira M2 without a mail account", new() { Account = JiraM2 }, new() { Reply = true, Comment = true } },
        { "reply only", new() { Account = Acc("r", true, [Capability.Reply]) }, new() { Reply = true } },
        { "move without delete", new() { Account = Acc("m", true, [Capability.Move]) }, new() { Move = true, Archive = true, Junk = true } },
        { "forward with compose of its own", new() { Account = Acc("f", true, [Capability.Forward, Capability.Compose]) }, new() { Forward = true } },
    };

    [Theory]
    [MemberData(nameof(SupportedCases))]
    public void Supported(string name, CapabilitySituation s, CapabilityActions want) =>
        Assert.True(want == Capabilities.Supported(s), $"{name}: Supported = {Capabilities.Supported(s)}");

    public static TheoryData<string, CapabilitySituation, CapabilityActions> AvailableCases => new()
    {
        { "nothing selected", new() { Account = Mail, Archive = true, Junk = true, ComposeAccount = true }, new() },
        { "nothing selected in jira keeps the label", new() { Account = JiraM2, ComposeAccount = true }, new() { Comment = true } },
        {
            "mail message",
            new() { Account = Mail, Selected = true, Archive = true, Junk = true, ComposeAccount = true },
            new() { Reply = true, ReplyAll = true, Forward = true, Move = true, Trash = true, Archive = true, Junk = true }
        },
        {
            "mail without archive and junk folders",
            new() { Account = Mail, Selected = true },
            new() { Reply = true, ReplyAll = true, Forward = true, Move = true, Trash = true }
        },
        {
            "old daemon",
            new() { Account = OldMail, Selected = true, Archive = true },
            new() { Reply = true, ReplyAll = true, Forward = true, Move = true, Trash = true, Archive = true }
        },
        {
            "queued message",
            new() { Account = Mail, Selected = true, Outbox = true, Archive = true, Junk = true },
            new() { Reply = true, ReplyAll = true, Forward = true, Trash = true }
        },
        { "jira M1", new() { Account = JiraM1, Selected = true, Archive = true, Junk = true, ComposeAccount = true }, new() },
        { "jira M1 queued comment", new() { Account = JiraM1, Selected = true, Outbox = true }, new() { Trash = true } },
        { "jira M2", new() { Account = JiraM2, Selected = true, ComposeAccount = true }, new() { Reply = true, Forward = true, Comment = true } },
        { "jira M2 without a mail account", new() { Account = JiraM2, Selected = true }, new() { Reply = true, Comment = true } },
    };

    [Theory]
    [MemberData(nameof(AvailableCases))]
    public void Available(string name, CapabilitySituation s, CapabilityActions want) =>
        Assert.True(want == Capabilities.Available(s), $"{name}: Available = {Capabilities.Available(s)}");

    private static string Ids(System.Collections.Generic.IEnumerable<Account> list) => string.Join(",", list.Select(a => a.Id.Value));

    [Fact]
    public void ForwardAccounts()
    {
        Account[] accounts = [JiraM2, Paused, Mail, JiraM1, OldMail];
        Assert.Equal("a2,a1", Ids(Capabilities.ForwardAccounts(accounts)));
        Assert.Empty(Capabilities.ForwardAccounts([JiraM1, Paused]));
        foreach (var (from, want) in new[] { ("a1", "a1"), ("a2", "a2"), ("j2", "a2"), ("p", "a2"), ("missing", "a2") })
        {
            Assert.True(want == Capabilities.ForwardFrom(accounts, from)?.Id.Value, $"ForwardFrom({from})");
        }
        Assert.Null(Capabilities.ForwardFrom([JiraM2, Paused], "j2"));
    }

    [Fact]
    public void ComposeAccounts()
    {
        Account[] accounts = [JiraM2, Paused, Mail, JiraM1, OldMail];
        Assert.Equal("p,a2,a1", Ids(Capabilities.ComposeAccounts(accounts)));
        Assert.Empty(Capabilities.ComposeAccounts([JiraM1, JiraM2]));
        Assert.True(Capabilities.CanComposeNew([])); // nothing known yet
        Assert.True(Capabilities.CanComposeNew([Mail]));
        Assert.True(Capabilities.CanComposeNew([OldMail]));
        Assert.True(Capabilities.CanComposeNew([JiraM2, Paused])); // a paused mail account
        Assert.False(Capabilities.CanComposeNew([JiraM2, JiraM1])); // issue trackers alone
        Assert.False(Capabilities.CanComposeNew([Acc("r", true, [Capability.Reply])])); // reply only
    }
}
