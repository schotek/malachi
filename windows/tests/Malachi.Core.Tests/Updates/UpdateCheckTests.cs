// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/updates/updates_test.go (TestBannerFor, TestPromptFor,
// TestNotesAndAbout, TestOpenableURL). Go passes a translator that returns
// the msgid; the process-wide catalogue is English here, so every msgid is
// its own translation.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.Updates;
using Xunit;

namespace Malachi.Core.Tests.Updates;

public sealed class UpdateCheckTests
{
    private const string Dl = "https://github.com/schotek/malachi/releases/download/v1.0.0/Malachi-Mail-1.0.0-macos-universal.dmg";

    private static readonly DateTimeOffset Now = new(2026, 11, 2, 8, 0, 0, TimeSpan.Zero);

    private static UpdateInfo Info(bool security) => new()
    {
        Version = "1.0.0",
        PublishedAt = new DateTimeOffset(2026, 11, 1, 19, 0, 0, TimeSpan.Zero),
        Security = security,
        Notes = "  ## New\n- Things.\n",
        InfoUrl = "https://malachimail.app/download.html",
        Url = Dl,
        Size = 1,
        Prompt = security,
    };

    // statuses: one of every state and offer.
    private static UpdateStatus?[] Statuses(DateTimeOffset now)
    {
        var at = now.AddHours(-1);
        return
        [
            null,
            new() { State = UpdateState.Disabled },
            new() { Enabled = true, State = UpdateState.Unsupported },
            new() { Enabled = true, State = UpdateState.Idle },
            new() { Enabled = true, State = UpdateState.Checking },
            new() { Enabled = true, State = UpdateState.UpToDate, CheckedAt = at },
            new() { Enabled = true, State = UpdateState.UpToDate },
            new() { Enabled = true, State = UpdateState.Failed, Failure = UpdateFailure.Network },
            new() { Enabled = true, State = UpdateState.Available, Available = Info(false) },
            new() { Enabled = true, State = UpdateState.Available, Available = Info(true) },
            new() { Enabled = true, State = UpdateState.UpToDate, Notice = new UpdateNotice { Id = "n-1", Text = "Hello.", Url = "https://malachimail.app/news.html" } },
            new() { Enabled = true, State = UpdateState.UpToDate, Notice = new UpdateNotice { Id = "n-2", Text = "Hello." } },
        ];
    }

    private static BannerAction[] Labels(UpdateBanner b) => [.. b.Buttons.Select(x => x.Action)];

    private static string Date(DateTimeOffset t) => t.UtcDateTime.ToString("yyyy-MM-dd", CultureInfo.InvariantCulture);

    [Fact]
    public void BannerForTest()
    {
        var st = Statuses(Now);
        BannerKind[] kinds =
        [
            BannerKind.None, BannerKind.None, BannerKind.None, BannerKind.None, BannerKind.None, BannerKind.None,
            BannerKind.None, BannerKind.None, BannerKind.Update, BannerKind.Security, BannerKind.Notice, BannerKind.Notice,
        ];
        for (var i = 0; i < kinds.Length; i++)
        {
            Assert.True(UpdateCheck.BannerFor(st[i], Now).Kind == kinds[i], $"status {i}: {UpdateCheck.BannerFor(st[i], Now).Kind}, want {kinds[i]}");
        }
        var b = UpdateCheck.BannerFor(st[8], Now);
        Assert.Equal("Malachi Mail 1.0.0 is available.", b.Title);
        Assert.Equal(Dl, b.Url);
        Assert.Equal("1.0.0", b.Version);
        Assert.Equal([BannerAction.Download, BannerAction.WhatsNew, BannerAction.Later, BannerAction.Skip], Labels(b));
        b = UpdateCheck.BannerFor(st[9], Now);
        Assert.Equal([BannerAction.Download, BannerAction.WhatsNew], Labels(b));
        b = UpdateCheck.BannerFor(st[10], Now);
        Assert.Equal("Hello.", b.Title);
        Assert.Equal("n-1", b.NoticeId);
        Assert.Equal("https://malachimail.app/news.html", b.Url);
        Assert.Equal([BannerAction.MoreInfo, BannerAction.Close], Labels(b));
        Assert.Equal([BannerAction.Close], Labels(UpdateCheck.BannerFor(st[11], Now)));

        // Skipped or put off: the notice, if any, shows instead.
        var later = Now.AddHours(1);
        var s = new UpdateStatus
        {
            Enabled = true,
            State = UpdateState.Available,
            Available = Info(false) with { RemindAfter = later },
            Notice = new UpdateNotice { Id = "n", Text = "x" },
        };
        Assert.Equal(BannerKind.Notice, UpdateCheck.BannerFor(s, Now).Kind);
        Assert.Equal(BannerKind.Update, UpdateCheck.BannerFor(s, later).Kind);
        s = s with { Available = s.Available! with { RemindAfter = null, Skipped = true }, Notice = null };
        Assert.False(UpdateCheck.BannerFor(s, Now).Shown);
        // A security release is never put off, whatever the status says.
        s = s with { Available = s.Available! with { Security = true } };
        Assert.Equal(BannerKind.Security, UpdateCheck.BannerFor(s, Now).Kind);
        // A URL off the allowed hosts gives no Download.
        s = s with { Available = s.Available! with { Url = "https://example.com/x.dmg" } };
        b = UpdateCheck.BannerFor(s, Now);
        Assert.Equal([BannerAction.WhatsNew], Labels(b));
        Assert.Equal("", b.Url);
        // Disabled: nothing, even with an offer.
        s = s with { Enabled = false, State = UpdateState.Disabled };
        Assert.False(UpdateCheck.BannerFor(s, Now).Shown);
    }

    [Fact]
    public void PromptForTest()
    {
        var st = Statuses(Now);
        for (var i = 0; i < st.Length; i++)
        {
            Assert.True((UpdateCheck.PromptFor(st[i]) is not null) == (i == 9), $"status {i}");
        }
        var p = UpdateCheck.PromptFor(st[9])!;
        Assert.Equal(Dl, p.Url);
        Assert.Equal("1.0.0", p.Version);
        Assert.NotEqual("", p.Quit);
        Assert.Equal("Download", p.Download);
        var asked = st[9]! with { Available = st[9]!.Available! with { Prompt = false } };
        Assert.Null(UpdateCheck.PromptFor(asked)); // asked twice
    }

    [Fact]
    public void NotesAndAboutTest()
    {
        var st = Statuses(Now);
        var n = UpdateCheck.NotesFor(st[8], Date)!;
        Assert.Equal("What's New in Malachi Mail 1.0.0", n.Title);
        Assert.Equal("Released 2026-11-01", n.Subtitle);
        Assert.Equal("## New\n- Things.", n.Body);
        Assert.Equal(Dl, n.Url);
        Assert.Equal(UpdateCheck.NewsUrl, n.InfoUrl);
        Assert.Equal([MarkdownBlockKind.Heading, MarkdownBlockKind.Bullet], n.Blocks.Select(b => b.Kind));
        var b = UpdateCheck.NotesBlocks("See [the site](https://malachimail.app/) and https://github.com/x.");
        Assert.All(b[0].Spans, s => Assert.Equal("", s.Link)); // a link stayed
        Assert.Null(UpdateCheck.NotesFor(st[5], Date)); // notes without an offer
        string[] lines =
        [
            "", "Automatic update checks are off.", "This build does not check for updates.", "Not checked for updates yet.",
            "Checking for updates…", "You are using the latest version (checked 2026-11-02).", "You are using the latest version.",
            "Could not check for updates.", "Malachi Mail 1.0.0 is available.", "Malachi Mail 1.0.0 is available.",
        ];
        for (var i = 0; i < lines.Length; i++)
        {
            Assert.Equal((i, lines[i]), (i, UpdateCheck.AboutLine(st[i], Date)));
        }
        bool[] can = [false, false, false, true, false, true, true, true, true, true];
        for (var i = 0; i < can.Length; i++)
        {
            Assert.Equal((i, can[i]), (i, UpdateCheck.CanCheckNow(st[i])));
        }
    }

    public static TheoryData<string, bool> Urls => new()
    {
        { Dl, true },
        { "https://malachimail.app/", true },
        { "http://malachimail.app/", false },
        { "https://malachimail.app:443/", false },
        { "https://user@github.com/", false },
        { "https://github.com/#x", false },
        { "https://GITHUB.com/", false },
        { "https://github.com.evil/", false },
        { "javascript:alert(1)", false },
        { "", false },
        { "https://github.com/a b", false },
        { "https://github.com\\@example.com", false },
    };

    [Theory]
    [MemberData(nameof(Urls))]
    public void OpenableUrlTest(string raw, bool ok) => Assert.Equal(ok, UpdateCheck.OpenableUrl(raw) is not null);

    /// <summary>Windows addition: null is no URL, and the URL comes back as it was.</summary>
    [Fact]
    public void OpenableUrlReturnsItsInput()
    {
        Assert.Null(UpdateCheck.OpenableUrl(null));
        Assert.Same(Dl, UpdateCheck.OpenableUrl(Dl));
        Assert.Null(UpdateCheck.OpenableUrl("https://github.com/" + new string('a', UpdateCheck.MaxUrlBytes)));
        IEnumerable<string> texts = [UpdateCheck.CheckNow(), UpdateCheck.PrefsGroup(), UpdateCheck.PrefsTitle(), UpdateCheck.PrefsSubtitle(), UpdateCheck.WizardNote()];
        Assert.All(texts, t => Assert.NotEqual("", t));
    }
}
