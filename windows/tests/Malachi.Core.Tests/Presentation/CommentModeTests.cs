// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/compose/comment_test.go (TestRestrictedToolbar,
// TestChosenVisibility). The formatting bar of the Windows compose window
// (FormatToolbar.xaml) has GTK's order, so a comment keeps the same
// controls.

using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class CommentModeTests
{
    // FormatToolbar.xaml, as compose.blp: Bold, Italic, Underline | Paragraph
    // Style, Alignment | Bulleted, Numbered, Quote | Link, Colour, Image, Clear.
    private static readonly CommentToolItem[] WindowsToolbar =
    [
        Control(JiraFormat.Bold), Control(JiraFormat.Italic), Control(JiraFormat.Underline),
        CommentToolItem.Divider,
        Control(JiraFormat.Heading), Control(JiraFormat.Alignment),
        CommentToolItem.Divider,
        Control(JiraFormat.BulletList), Control(JiraFormat.NumberedList), Control(JiraFormat.Quote),
        CommentToolItem.Divider,
        Control(JiraFormat.Link), Control(JiraFormat.Colour), Control(JiraFormat.Image), Control(JiraFormat.Clear),
    ];

    private static readonly CommentToolItem Sep = CommentToolItem.Divider;
    private static readonly CommentToolItem Under = Control(JiraFormat.Underline);
    private static readonly CommentToolItem Bold = Control(JiraFormat.Bold);
    private static readonly CommentToolItem Other = Control(null); // a control of no format stays

    public static TheoryData<string, CommentToolItem[], bool[]> Cases => new()
    {
        { "empty", [], [] },
        { "leading separator", [Sep, Bold], [false, true] },
        { "trailing separator", [Bold, Sep], [true, false] },
        { "emptied group at the end", [Bold, Sep, Under], [true, false, false] },
        { "emptied group at the start", [Under, Sep, Bold], [false, false, true] },
        { "two separators in a row", [Bold, Sep, Sep, Bold], [true, true, false, true] },
        { "nothing kept", [Under, Sep, Under], [false, false, false] },
        { "control without format", [Other, Sep, Bold], [true, true, true] },
    };

    [Fact]
    public void TheCommentToolbar()
    {
        var shown = CommentMode.RestrictedToolbar(WindowsToolbar);
        var got = new List<string>();
        for (var i = 0; i < shown.Count; i++)
        {
            if (shown[i])
            {
                got.Add(WindowsToolbar[i].Separator ? "|" : WindowsToolbar[i].Format!.Value.ToString());
            }
        }
        Assert.Equal(["Bold", "Italic", "|", "BulletList", "NumberedList", "Quote", "|", "Link", "Clear"], got);
    }

    [Theory]
    [MemberData(nameof(Cases))]
    public void RestrictedToolbar(string name, CommentToolItem[] items, bool[] want) =>
        Assert.True(want.SequenceEqual(CommentMode.RestrictedToolbar(items)), name);

    public static TheoryData<bool, string?, string> VisibilityCases => new()
    {
        { true, "internal", CommentVisibility.Internal },
        { true, "public", CommentVisibility.Public },
        { true, "", CommentVisibility.Public },
        { true, null, CommentVisibility.Public },
        { true, "partners", CommentVisibility.Public },
        // An ordinary issue: no choice, public.
        { false, "internal", CommentVisibility.Public },
        { false, "", CommentVisibility.Public },
    };

    [Theory]
    [MemberData(nameof(VisibilityCases))]
    public void ChosenVisibility(bool both, string? active, string want) =>
        Assert.Equal(new CommentVisibility(want), CommentMode.ChosenVisibility(both ? Jira.VisibilityOptions(Request) : [], active));

    // What the window shows first is the draft's choice (Jira.SelectedVisibility),
    // found among the options by name.
    [Theory]
    [InlineData(true, CommentVisibility.Internal)]
    [InlineData(true, "")]
    [InlineData(false, CommentVisibility.Internal)]
    public void TheDraftsChoiceIsFound(bool request, string visibility)
    {
        var d = new Draft
        {
            AccountId = "j",
            Comment = new DraftComment { Issue = request ? Request : Issue, Visibility = visibility },
        };
        var cw = Jira.CommentCompose(d)!;
        Assert.Equal(cw.Visibility, CommentMode.ChosenVisibility(cw.Visibilities, cw.Visibility.Value));
    }

    private static CommentToolItem Control(JiraFormat? f) => CommentToolItem.Control(f);

    private static IssueInfo Request => new()
    {
        Key = "ITSD-42",
        Url = "",
        Summary = "The printer",
        Status = "Waiting for support",
        CommentVisibilities = [CommentVisibility.Public, CommentVisibility.Internal],
    };

    private static IssueInfo Issue => new() { Key = "WEB-7", Url = "", Summary = "Logo", Status = "To Do" };
}
