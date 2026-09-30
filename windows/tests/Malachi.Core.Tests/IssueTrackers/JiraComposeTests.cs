// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/JiraComposeTests.swift, the
// counterpart of ui/internal/jira/compose_test.go (TestVisibilityOptions,
// TestSelectedVisibility, TestCommentCompose, TestCommentAllows,
// TestSendProblem, TestComposeLabels), with the English catalogue.

using System;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.IssueTrackers;
using Xunit;

namespace Malachi.Core.Tests.IssueTrackers;

public sealed class JiraComposeTests
{
    private static IssueInfo Vis(params CommentVisibility[] v) =>
        new() { Key = "ITSD-42", Url = "", Summary = "", Status = "", CommentVisibilities = v };

    private static readonly JiraVisibilityOption[] Both =
    [
        new(CommentVisibility.Public, "Reply to Customer"),
        new(CommentVisibility.Internal, "Internal Note"),
    ];

    public static TheoryData<string, CommentVisibility[], bool> VisibilityCases => new()
    {
        { "none", [], false },
        { "public only", [CommentVisibility.Public], false },
        { "internal only", [CommentVisibility.Internal], false },
        { "both", [CommentVisibility.Public, CommentVisibility.Internal], true },
        { "both, other order", [CommentVisibility.Internal, CommentVisibility.Public], true },
        { "twice public", [CommentVisibility.Public, CommentVisibility.Public], false },
        { "unknown value", [CommentVisibility.Public, CommentVisibility.Internal, "partners"], false },
    };

    [Theory]
    [MemberData(nameof(VisibilityCases))]
    public void VisibilityOptions(string name, CommentVisibility[] visibilities, bool both)
    {
        var got = Jira.VisibilityOptions(Vis(visibilities));
        Assert.True(both ? Both.SequenceEqual(got) : got.Count == 0, $"{name}: {got.Count} options");
    }

    [Fact]
    public void VisibilityOptionsWithoutAList() =>
        Assert.Empty(Jira.VisibilityOptions(new IssueInfo { Key = "WEB-7", Url = "", Summary = "", Status = "" }));

    public static TheoryData<CommentVisibility[], string, string> SelectedCases => new()
    {
        { [CommentVisibility.Public, CommentVisibility.Internal], "", CommentVisibility.Public },
        { [CommentVisibility.Public, CommentVisibility.Internal], CommentVisibility.Internal, CommentVisibility.Internal },
        { [CommentVisibility.Public, CommentVisibility.Internal], CommentVisibility.Public, CommentVisibility.Public },
        { [], CommentVisibility.Internal, CommentVisibility.Public },
        { [CommentVisibility.Public, CommentVisibility.Internal], "secret", CommentVisibility.Public },
    };

    [Theory]
    [MemberData(nameof(SelectedCases))]
    public void SelectedVisibility(CommentVisibility[] allowed, string asked, string want) =>
        Assert.Equal(new CommentVisibility(want), Jira.SelectedVisibility(new DraftComment { Issue = Vis(allowed), Visibility = asked }));

    [Fact]
    public void CommentCompose()
    {
        Assert.Null(Jira.CommentCompose(new Draft { AccountId = "a", Subject = "Re: hello" })); // a mail draft has no comment mode
        var d = new Draft
        {
            AccountId = "j",
            Comment = new DraftComment
            {
                Issue = Vis(CommentVisibility.Public, CommentVisibility.Internal) with { Key = "ITSD-42" + JiraTests.Rlo },
                Visibility = CommentVisibility.Internal,
            },
        };
        var w = Jira.CommentCompose(d)!;
        Assert.Equal("Comment on ITSD-42", w.Title);
        Assert.Equal(2, w.Visibilities.Count);
        Assert.Equal(new CommentVisibility(CommentVisibility.Internal), w.Visibility);
        Assert.Equal(Jira.CommentFormats, w.Formats);
        Assert.NotSame(Jira.CommentFormats, w.Formats); // the window's formats are a copy
        var plain = Jira.CommentCompose(new Draft { AccountId = "j", Comment = new DraftComment { Issue = Vis() with { Key = "WEB-7" } } })!;
        Assert.Empty(plain.Visibilities);
        Assert.Equal(new CommentVisibility(CommentVisibility.Public), plain.Visibility);
        Assert.Equal("Comment on WEB-7", plain.Title);
    }

    [Theory]
    [InlineData(JiraFormat.Bold, true)]
    [InlineData(JiraFormat.Italic, true)]
    [InlineData(JiraFormat.Code, true)]
    [InlineData(JiraFormat.Link, true)]
    [InlineData(JiraFormat.BulletList, true)]
    [InlineData(JiraFormat.NumberedList, true)]
    [InlineData(JiraFormat.Quote, true)]
    [InlineData(JiraFormat.Clear, true)]
    [InlineData(JiraFormat.Underline, false)]
    [InlineData(JiraFormat.Heading, false)]
    [InlineData(JiraFormat.Alignment, false)]
    [InlineData(JiraFormat.Colour, false)]
    [InlineData(JiraFormat.Image, false)]
    [InlineData((JiraFormat)99, false)] // Go's "strike": no such control
    public void CommentAllows(JiraFormat format, bool want) => Assert.Equal(want, Jira.CommentAllows(format));

    public static TheoryData<string, string> SendCases => new()
    {
        { "", "Write a comment first" },
        { " \n\t ", "Write a comment first" },
        { JiraTests.Zwsp + JiraTests.Bom + " " + JiraTests.Shy, "Write a comment first" },
        { "\u00A0", "Write a comment first" }, // NO-BREAK SPACE, as an empty editor paragraph leaves
        { "Restarted the VPN concentrator", "" },
        { " ok ", "" },
        { JiraTests.Invalid, "" }, // Go's invalid UTF-8 is text, not blank
    };

    [Theory]
    [MemberData(nameof(SendCases))]
    public void SendProblem(string text, string want) => Assert.Equal(want, Jira.SendProblem(text));

    [Fact]
    public void ComposeLabels()
    {
        Assert.Equal("Comment", Jira.ReplyLabel(true));
        Assert.Equal("Reply", Jira.ReplyLabel(false));
        Assert.Equal("Comment queued", Jira.CommentQueued());
        Assert.Equal("Comment on WEB-7", Jira.CommentTitle(" WEB-7\n"));
    }
}
