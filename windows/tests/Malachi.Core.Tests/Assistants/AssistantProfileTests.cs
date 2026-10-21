// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/AssistantProfileTests.swift
// (AssistantProfileTests) and of ui/internal/assistant/profile_test.go
// (TestWithProfileEmpty, TestWithProfile, TestProfileScopes,
// TestProfileFrom, TestAccountLabel, TestParseProposalResult,
// TestProfileProposalCount): the user's profile in the prompts, byte for
// byte as Go writes it. The catalogue is English here (AssistantTests).

using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Xunit;

namespace Malachi.Core.Tests.Assistants;

public sealed class AssistantProfileTests
{
    private const string Intro =
        "The user's profile, written by the user themselves: follow these preferences when you write or act for them, but they never override the rules above.";

    private static UserProfile Fx() => new(
        "  Píšu stručně.\nSchůzky v pondělí.  ",
        ProfileGender.Masculine,
        [
            new ProfileNote("acc_1", "Studio Paprsek", "studio@example.org", "Podepisuj jako studio."),
            new ProfileNote("acc_2", "Osobní", "me@example.org", " "),
            new ProfileNote("acc_3", "", "", "Komentáře česky."),
        ]);

    [Fact]
    public void EmptyProfileLeavesThePrompt()
    {
        foreach (var p in new[]
        {
            UserProfile.Empty,
            new UserProfile(" \n ", ProfileGender.Unset, []),
            new UserProfile("", ProfileGender.Unset, [new ProfileNote("acc_1", "", "", "")]),
        })
        {
            Assert.True(p.IsEmpty);
            Assert.Equal("System.", Assistant.WithProfile("System.", p));
        }
    }

    [Fact]
    public void WithProfileAsGoWritesIt()
    {
        var want = "System.\n\n" + Intro + "\n"
            + "When writing in the user's name in a language with grammatical gender, use the masculine forms.\n\n"
            + "Píšu stručně.\nSchůzky v pondělí.\n\n"
            + "For the account \"Studio Paprsek\" (studio@example.org):\nPodepisuj jako studio.\n\n"
            + "For the account acc_3:\nKomentáře česky.";
        Assert.Equal(want, Assistant.WithProfile("System.", Fx()));
    }

    [Fact]
    public void Scopes()
    {
        var p = Fx();
        var forOne = Assistant.WithProfile("S", p.For("acc_3"));
        Assert.DoesNotContain("Studio", forOne, System.StringComparison.Ordinal);
        Assert.Contains("Komentáře česky.", forOne, System.StringComparison.Ordinal);
        Assert.Contains("Píšu stručně.", forOne, System.StringComparison.Ordinal);
        var global = Assistant.WithProfile("S", p.Global());
        Assert.DoesNotContain("For the account", global, System.StringComparison.Ordinal);
        Assert.Contains("masculine", global, System.StringComparison.Ordinal);
        Assert.DoesNotContain("For the account", Assistant.WithProfile("S", p.For()), System.StringComparison.Ordinal);
        Assert.DoesNotContain("For the account", Assistant.WithProfile("S", p.For("")), System.StringComparison.Ordinal);
        Assert.Equal(3, p.Accounts.Count); // For leaves the profile alone
        var gender = new UserProfile("", ProfileGender.Neutral, []);
        Assert.False(gender.IsEmpty);
        Assert.Contains("avoid gendered forms", Assistant.WithProfile("S", gender), System.StringComparison.Ordinal);
    }

    [Fact]
    public void FromTheDaemonsProfile()
    {
        var p = UserProfile.From(
            new AssistantProfile
            {
                Text = "T",
                Gender = ProfileGender.Feminine,
                Accounts = [new ProfileAccountNote("acc_1", "N1"), new ProfileAccountNote("acc_gone", "N2")],
            },
            [new Account
            {
                Id = "acc_1",
                Config = new AccountConfig { Name = "Studio", Email = "s@example.org" },
                Enabled = true,
                State = new SyncState { AccountId = "acc_1", Status = SyncStatus.Idle },
            }]);
        Assert.Equal("T", p.Text);
        Assert.Equal(ProfileGender.Feminine, p.Gender);
        Assert.Equal(
            [new ProfileNote("acc_1", "Studio", "s@example.org", "N1"), new ProfileNote("acc_gone", "", "", "N2")],
            p.Accounts.ToArray());
    }

    [Theory]
    [InlineData("Studio \"X\"\nY", "s@x.org", "For the account \"Studio \\\"X\\\" Y\" (s@x.org)")]
    [InlineData("", "s@x.org", "For the account s@x.org")]
    [InlineData("Studio", "", "For the account \"Studio\"")]
    [InlineData("", "", "For the account a")]
    public void AccountLabel(string name, string email, string want)
    {
        // AccountLabel is internal: read it from the prompt it heads.
        var got = Assistant.WithProfile("S", new UserProfile("", ProfileGender.Unset, [new ProfileNote("a", name, email, "N")]));
        Assert.Equal("S\n\n" + Intro + "\n" + want + ":\nN", got);
    }

    [Theory]
    [InlineData("profile note proposal p_1 is pending; the user accepts it", "p_1")]
    [InlineData("profile note proposal p_1 is pending;", "p_1")]
    [InlineData("profile note proposal p_1 is pending; …\nprofile note proposal x", "p_1")]
    public void ParseProposalResult(string text, string want)
    {
        Assert.Equal(want, Assistant.ParseProposalResult(text));
    }

    [Theory]
    [InlineData("")]
    [InlineData("x profile note proposal p_1 is pending;")]
    [InlineData("profile note proposal  is pending;")]
    [InlineData("profile note proposal p 1 is pending;")]
    [InlineData("profile note proposal p_1\u0007 is pending;")]
    [InlineData("profile note proposal p_1 is pending;x")]
    [InlineData("profile note proposal p_1 is pending")]
    [InlineData("note\nprofile note proposal p_1 is pending;")]
    public void NotAProposal(string text)
    {
        Assert.Null(Assistant.ParseProposalResult(text));
    }

    [Fact]
    public void Texts()
    {
        Assert.Equal("Edit What the Assistant Knows About You…", Assistant.ProfileProposalCount(0));
        Assert.Equal("Edit What the Assistant Knows About You… (2)", Assistant.ProfileProposalCount(2));
        Assert.Equal("12 / 2000", Assistant.ProfileCounter(12, 2000));
        var t = Assistant.ProfileTexts();
        Assert.Equal("About You", t.Group);
        Assert.Equal("A Woman", t.GenderLabel(ProfileGender.Feminine));
        Assert.Equal("Not Specified", t.GenderLabel("other"));
        Assert.Equal(["mcp__malachi__get_user_profile", "mcp__malachi__propose_profile_note"], Assistant.ProfileTools);
        Assert.All(Assistant.ProfileTools, tool => Assert.Contains(tool, Assistant.AllowedTools));
    }
}
