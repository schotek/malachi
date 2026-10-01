// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/BulkMailTests.swift, the counterpart
// of ui/internal/bulkmail/bulkmail_test.go (TestTag, TestStripFor,
// TestConfirm, TestFallback, TestApplied, TestTexts, TestOpenableURL,
// TestHostileStrings). Go passes a translator; the process-wide catalogue is
// English here, so every msgid is its own translation (the Czech side is in
// BulkMailTranslationTests). Go's invalid UTF-8 is a lone surrogate in a C#
// string, its NUL and RIGHT-TO-LEFT OVERRIDE the escapes below.

using System;
using System.Globalization;
using Malachi.Core.Api;
using Malachi.Core.Bulk;
using Malachi.Core.Model;
using Microsoft.Extensions.Time.Testing;
using Xunit;

namespace Malachi.Core.Tests.Bulk;

public sealed class BulkMailTests
{
    // Characters of hostile input, as escapes: the source stays free of
    // invisible characters.
    private const string Nul = "\u0000";
    private const string Rlo = "\u202E"; // RIGHT-TO-LEFT OVERRIDE
    private const string Isolate = "\u2066"; // LEFT-TO-RIGHT ISOLATE

    private static string Date(DateTimeOffset t) => t.UtcDateTime.ToString("yyyy-MM-dd", CultureInfo.InvariantCulture);

    private static Message Msg(BulkInfo? b, UnsubscribeOffer? o) => new()
    {
        Summary = new MessageSummary
        {
            Id = "m1",
            AccountId = "acc_1",
            FolderId = "f_inbox",
            Subject = "s",
            Date = new DateTimeOffset(2026, 9, 30, 12, 0, 0, TimeSpan.Zero),
            Snippet = "",
            HasAttachments = false,
            Size = 0,
            Bulk = b,
        },
        Unsubscribe = o,
    };

    [Fact]
    public void TagTest()
    {
        Assert.Equal("", BulkMail.Tag(null));
        Assert.Equal("Bulk", BulkMail.Tag(new BulkInfo { Kind = BulkKind.Newsletter }));
        Assert.Equal("Mailing List", BulkMail.Tag(new BulkInfo { Kind = BulkKind.List }));
        Assert.Equal("Automated", BulkMail.Tag(new BulkInfo { Kind = BulkKind.Automated }));
        Assert.Equal("", BulkMail.Tag(new BulkInfo { Kind = "weird" }));
        Assert.Equal("", BulkMail.Tag(new BulkInfo { Kind = default(BulkKind) }));
    }

    [Fact]
    public void StripForTest()
    {
        var at = new DateTimeOffset(2026, 9, 30, 12, 0, 0, TimeSpan.Zero);
        var news = new BulkInfo { Kind = BulkKind.Newsletter, Domain = "shop.example", ListId = "news.shop.example" };
        var list = new BulkInfo { Kind = BulkKind.List, ListId = "golang-nuts.example", Domain = "example.org" };
        var listNoId = new BulkInfo { Kind = BulkKind.List, Domain = "example.org" };
        var auto = new BulkInfo { Kind = BulkKind.Automated, Domain = "bank.example" };
        var one = new UnsubscribeOffer { Method = UnsubscribeMethod.OneClick, Target = "shop.example" };
        var mailto = new UnsubscribeOffer { Method = UnsubscribeMethod.Mailto, Target = "u@shop.example" };
        var page = new UnsubscribeOffer { Method = UnsubscribeMethod.Url, Target = "shop.example", Url = "https://shop.example/u" };
        var done = new UnsubscribeOffer { Method = UnsubscribeMethod.OneClick, Target = "shop.example", UnsubscribedAt = at };
        const string JunkText = "Unsubscribing would confirm to the sender that your address exists.";
        var junk = new BulkStrip { Kind = BulkStripKind.Junk, Text = JunkText, Warning = true };
        var automated = new BulkStrip { Kind = BulkStripKind.Automated, Text = "Automated message" };
        var unsubscribed = new BulkStrip { Kind = BulkStripKind.Unsubscribed, Text = "Unsubscribed on 2026-09-30" };
        var cases = new (string Name, Message? M, FolderRole Role, BulkStrip Want)[]
        {
            ("nil message", null, FolderRole.Inbox, new BulkStrip()),
            ("nil bulk", Msg(null, one), FolderRole.Inbox, new BulkStrip()),
            ("unknown kind", Msg(new BulkInfo { Kind = "x" }, one), FolderRole.Inbox, new BulkStrip()),
            ("junk newsletter", Msg(news, one), FolderRole.Junk, junk),
            ("junk list", Msg(list, null), FolderRole.Junk, junk),
            ("junk role string", Msg(news, one), "junk", junk),
            ("junk automated", Msg(auto, null), FolderRole.Junk, automated),
            ("automated", Msg(auto, one), FolderRole.Inbox, automated),
            ("junk-like role", Msg(news, one), new FolderRole("junkish"),
                new BulkStrip { Kind = BulkStripKind.Newsletter, Text = "Bulk message from shop.example", Action = "_Unsubscribe" }),
            ("unsubscribed", Msg(news, done), FolderRole.Inbox, unsubscribed),
            ("unsubscribed list", Msg(list, done), FolderRole.Inbox, unsubscribed),
            ("newsletter one click", Msg(news, one), FolderRole.Inbox,
                new BulkStrip { Kind = BulkStripKind.Newsletter, Text = "Bulk message from shop.example", Action = "_Unsubscribe" }),
            ("newsletter mailto", Msg(news, mailto), FolderRole.Inbox,
                new BulkStrip { Kind = BulkStripKind.Newsletter, Text = "Bulk message from shop.example", Action = "_Unsubscribe" }),
            ("newsletter url", Msg(news, page), FolderRole.Inbox,
                new BulkStrip { Kind = BulkStripKind.Newsletter, Text = "Bulk message from shop.example", Action = "_Unsubscribe…" }),
            ("newsletter no offer", Msg(news, null), FolderRole.Inbox,
                new BulkStrip { Kind = BulkStripKind.Newsletter, Text = "Bulk message from shop.example" }),
            ("newsletter no domain", Msg(new BulkInfo { Kind = BulkKind.Newsletter, ListId = "l.example" }, null), FolderRole.Inbox,
                new BulkStrip { Kind = BulkStripKind.Newsletter, Text = "Bulk message from l.example" }),
            ("list mailto", Msg(list, mailto), FolderRole.Inbox,
                new BulkStrip { Kind = BulkStripKind.List, Text = "Message from mailing list golang-nuts.example", Action = "_Leave List" }),
            ("list url", Msg(list, page), FolderRole.Inbox,
                new BulkStrip { Kind = BulkStripKind.List, Text = "Message from mailing list golang-nuts.example", Action = "_Leave List…" }),
            ("list without list id falls back to domain", Msg(listNoId, one), FolderRole.Inbox,
                new BulkStrip { Kind = BulkStripKind.List, Text = "Message from mailing list example.org", Action = "_Leave List" }),
            ("list no offer", Msg(list, null), FolderRole.Inbox,
                new BulkStrip { Kind = BulkStripKind.List, Text = "Message from mailing list golang-nuts.example" }),
        };
        foreach (var (name, m, role, want) in cases)
        {
            var got = BulkMail.StripFor(m, role, Date);
            Assert.True(got == want, $"{name}: got {got}, want {want}");
            Assert.True(got.Visible == (want.Kind != BulkStripKind.None), $"{name}: Visible = {got.Visible}");
        }
        // A missing date formatter must not throw.
        _ = BulkMail.StripFor(Msg(news, done), FolderRole.Inbox, null);
    }

    [Fact]
    public void ConfirmTest()
    {
        var news = new BulkInfo { Kind = BulkKind.Newsletter, Domain = "shop.example" };
        var list = new BulkInfo { Kind = BulkKind.List, ListId = "l.example", Domain = "example.org" };
        var one = new UnsubscribeOffer { Method = UnsubscribeMethod.OneClick, Target = "shop.example" };
        var mailto = new UnsubscribeOffer { Method = UnsubscribeMethod.Mailto, Target = "u@shop.example" };
        var page = new UnsubscribeOffer { Method = UnsubscribeMethod.Url, Target = "shop.example", Url = "https://shop.example/u?x=1" };
        const string OneClickBody =
            "Malachi Mail will ask shop.example to stop sending these messages. The sender may still send a few more over the next days.";
        const string MailtoBody = "Malachi Mail will send an unsubscribe request to u@shop.example from your account. It will appear in Sent.";
        var cases = new (string Name, Message? M, UnsubscribeConfirmation? Want)[]
        {
            ("nil", null, null),
            ("no offer", Msg(news, null), null),
            ("unknown method", Msg(news, new UnsubscribeOffer { Method = "x", Target = "t" }), null),
            ("one click newsletter", Msg(news, one), new("Unsubscribe from shop.example?", OneClickBody, "_Unsubscribe")),
            ("one click list", Msg(list, one), new("Unsubscribe from l.example?", OneClickBody, "_Unsubscribe")),
            ("one click nil bulk", Msg(null, one), new("Unsubscribe from shop.example?", OneClickBody, "_Unsubscribe")),
            ("mailto newsletter", Msg(news, mailto), new("Unsubscribe from shop.example?", MailtoBody, "_Send Request")),
            ("mailto list", Msg(list, mailto), new("Leave the mailing list l.example?", MailtoBody, "_Send Request")),
            ("url", Msg(news, page), new(
                "Open the unsubscribe page?",
                "The sender does not offer unsubscribing in one step. This page opens in your browser:\nhttps://shop.example/u?x=1",
                "_Open in Browser")),
        };
        foreach (var (name, m, want) in cases)
        {
            var got = BulkMail.Confirm(m);
            Assert.True(got == want, $"{name}: got {got}, want {want}");
        }
    }

    [Fact]
    public void FallbackTest()
    {
        var m = Msg(
            new BulkInfo { Kind = BulkKind.Newsletter, Domain = "shop.example" },
            new UnsubscribeOffer { Method = UnsubscribeMethod.OneClick, Target = "t.example" });
        var res = new MessageUnsubscribeResult { Outcome = UnsubscribeOutcome.OpenUrl, Url = "https://shop.example/u", Unverified = true };
        var want = new UnsubscribeConfirmation(
            "The sender could not be verified",
            "Malachi Mail sent nothing because the message is not signed by shop.example. You can unsubscribe on the sender's page instead:\nhttps://shop.example/u",
            "_Open in Browser");
        Assert.Equal(want, BulkMail.Fallback(m, res));
        // Without a domain the offer's target stands in; a missing message is safe.
        var noBulk = Msg(null, m.Unsubscribe);
        Assert.Contains("signed by t.example.", BulkMail.Fallback(noBulk, res).Body, StringComparison.Ordinal);
        Assert.NotEqual("", BulkMail.Fallback(null, res).Heading);
    }

    [Fact]
    public void AppliedTest()
    {
        var at = new DateTimeOffset(2026, 9, 30, 1, 2, 3, TimeSpan.Zero);
        var offer = new UnsubscribeOffer { Method = UnsubscribeMethod.Mailto, Target = "u@x.example" };
        Assert.Null(BulkMail.Applied(null, new MessageUnsubscribeResult { Outcome = UnsubscribeOutcome.Unsubscribed }));
        foreach (var outcome in new[] { UnsubscribeOutcome.Unsubscribed, UnsubscribeOutcome.Queued })
        {
            var got = BulkMail.Applied(offer, new MessageUnsubscribeResult { Outcome = outcome, UnsubscribedAt = at });
            Assert.NotNull(got);
            Assert.NotSame(offer, got);
            Assert.Equal(at, got.UnsubscribedAt);
            Assert.True(got.Method == offer.Method && got.Target == offer.Target, $"{outcome}: fields lost: {got}");
            Assert.Null(offer.UnsubscribedAt); // the original offer was not modified
        }
        // A result without a time still marks the offer as done, with the clock's.
        var now = new DateTimeOffset(2026, 10, 1, 8, 0, 0, TimeSpan.Zero);
        var clock = new FakeTimeProvider(now);
        Assert.Equal(now, BulkMail.Applied(offer, new MessageUnsubscribeResult { Outcome = UnsubscribeOutcome.Queued }, clock)!.UnsubscribedAt);
        Assert.NotNull(BulkMail.Applied(offer, new MessageUnsubscribeResult { Outcome = UnsubscribeOutcome.Queued })!.UnsubscribedAt);
        // openUrl never marks the offer, whatever the result carries.
        var open = BulkMail.Applied(offer, new MessageUnsubscribeResult { Outcome = UnsubscribeOutcome.OpenUrl, UnsubscribedAt = at });
        Assert.Null(open!.UnsubscribedAt);
        Assert.NotSame(offer, open);
    }

    [Fact]
    public void TextsTest()
    {
        Assert.Equal("Unsubscribe request queued", BulkMail.Queued());
        Assert.Equal("Unsubscribing", BulkMail.ErrorWhat());
        Assert.Equal("The sender's server refused the request.", BulkMail.Refused());
    }

    [Theory]
    [InlineData("https://shop.example/u?x=1", true)]
    [InlineData("HTTPS://shop.example/", true)]
    [InlineData("http://shop.example/", false)]
    [InlineData("javascript:alert(1)", false)]
    [InlineData("data:text/html,x", false)]
    [InlineData("https:///path", false)]
    [InlineData("/relative", false)]
    [InlineData("", false)]
    [InlineData("https://shop.example/a b", false)]
    [InlineData("https://shop.example/\u0000", false)]
    [InlineData("https://shop.example/\u202E", false)]
    public void OpenableUrlTest(string raw, bool ok)
    {
        Assert.Equal(ok ? raw : null, BulkMail.OpenableUrl(raw));
    }

    [Fact]
    public void OpenableUrlRefusesWhatIsTooLong()
    {
        Assert.Null(BulkMail.OpenableUrl("https://" + new string('a', 3000)));
        Assert.Null(BulkMail.OpenableUrl(null));
        // The limit is in bytes, as Go's len: 2048 bytes pass, one more does not.
        var url = "https://a.example/" + new string('b', 2048 - "https://a.example/".Length);
        Assert.Equal(url, BulkMail.OpenableUrl(url));
        Assert.Null(BulkMail.OpenableUrl(url + "b"));
    }

    // Hostile strings are shown as plain text by the view; here they must
    // only pass through without a throw or a rewrite.
    [Fact]
    public void HostileStrings()
    {
        var huge = new string('a', 1 << 20);
        var bidi = Rlo + "evil" + Isolate + ".example" + Nul + "%s%d<b>";
        foreach (var s in new[] { huge, bidi, "", "%", "%!s(MISSING)" })
        {
            var b = new BulkInfo { Kind = BulkKind.List, ListId = s, Domain = s };
            var o = new UnsubscribeOffer { Method = UnsubscribeMethod.Url, Target = s, Url = s };
            var m = Msg(b, o);
            var strip = BulkMail.StripFor(m, FolderRole.Inbox, Date);
            Assert.True(s.Length == 0 || strip.Text.Contains(s, StringComparison.Ordinal), "the list id was rewritten");
            _ = BulkMail.Confirm(m);
            _ = BulkMail.Fallback(m, new MessageUnsubscribeResult { Outcome = UnsubscribeOutcome.OpenUrl, Url = s });
            _ = BulkMail.Tag(b);
        }
    }

    // window/bulk.go: the message the strip is decided from, and which
    // summaries want message.get for the offer.
    [Fact]
    public void ReadingTest()
    {
        var news = new BulkInfo { Kind = BulkKind.Newsletter, Domain = "shop.example" };
        var summary = Msg(news, null).Summary;
        var offer = new UnsubscribeOffer { Method = UnsubscribeMethod.OneClick, Target = "shop.example" };

        // No entry, or one without message.get: the summary alone.
        Assert.Equal(summary, BulkReading.MessageFor(summary, null).Summary);
        Assert.Null(BulkReading.MessageFor(summary, new LoadedMessage()).Unsubscribe);
        // The cached message wins, its classification completed from the summary.
        var cached = new LoadedMessage { Msg = Msg(null, offer) };
        var m = BulkReading.MessageFor(summary, cached);
        Assert.Equal(news, m.Summary.Bulk);
        Assert.Equal(offer, m.Unsubscribe);
        Assert.Null(cached.Msg.Summary.Bulk); // the entry itself is left alone
        var own = new LoadedMessage { Msg = Msg(new BulkInfo { Kind = BulkKind.List, ListId = "l" }, offer) };
        Assert.Equal(BulkKind.List, BulkReading.MessageFor(summary, own).Summary.Bulk!.Kind.Value);

        Assert.True(BulkReading.WantsOffer(summary));
        Assert.True(BulkReading.WantsOffer(summary with { Bulk = new BulkInfo { Kind = BulkKind.List } }));
        Assert.False(BulkReading.WantsOffer(summary with { Bulk = new BulkInfo { Kind = BulkKind.Automated } }));
        Assert.False(BulkReading.WantsOffer(summary with { Bulk = null }));

        // The strip of the entry, with the offer once message.get brought it.
        Assert.Equal("", BulkReading.StripFor(summary, null, FolderRole.Inbox).Action);
        Assert.Equal("_Unsubscribe", BulkReading.StripFor(summary, cached, FolderRole.Inbox).Action);
        Assert.Equal(BulkStripKind.Junk, BulkReading.StripFor(summary, cached, FolderRole.Junk).Kind);
        foreach (var kind in Enum.GetValues<BulkStripKind>())
        {
            Assert.Equal(kind == BulkStripKind.None, BulkReading.IconName(kind).Length == 0);
        }
    }
}
