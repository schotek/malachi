// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/PrefillTests.swift, the counterpart of
// ui/internal/compose/prefill_test.go. The texts are the English msgids: the
// process-wide catalogue is English unless a test swaps it (none does).

using System;
using System.Collections.Generic;
using System.Linq;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Xunit;

namespace Malachi.Core.Tests.Compose;

public sealed class PrefillTests
{
    private static readonly DateTimeOffset Date = new(2026, 9, 2, 14, 3, 0, TimeSpan.Zero);

    [Fact]
    public void SubjectPrefixes()
    {
        var cases = new Dictionary<string, string>
        {
            ["Hello"] = "Re: Hello",
            ["Re: Hello"] = "Re: Hello",
            ["RE: re: Hello"] = "Re: Hello",
            ["Fwd: FW: Hello"] = "Re: Hello",
            ["AW: Hello"] = "Re: Hello",
            ["  Re:   spaced  "] = "Re: spaced",
            [""] = "Re: ",
            ["Rear window"] = "Re: Rear window",
        };
        foreach (var (input, want) in cases)
        {
            Assert.Equal(want, Prefill.ReplySubject(input));
        }
        Assert.Equal("Fwd: x", Prefill.ForwardSubject("Re: Fwd: x"));
    }

    [Fact]
    public void PrefillEscapes()
    {
        var me = new Address { Email = "me@example.invalid" };
        var src = new ComposeSource
        {
            Id = "m_1",
            From = [new Address { Name = "<b>Alice</b>", Email = "alice@example.invalid" }],
            To = [me, new Address { Email = "bob@example.invalid" }],
            Cc = [new Address { Email = "ALICE@example.invalid" }, new Address { Email = "carol@example.invalid" }],
            Subject = "Re: <script>alert(1)</script> & co",
            Date = Date,
            Text = "line1\r\nline2 </blockquote><img src=x onerror=alert(1)>",
        };

        var p = Prefill.Create(ComposeKind.ReplyAll, src, me);
        Assert.Equal(ComposeKind.ReplyAll, p.Kind);
        Assert.Equal(new MessageId("m_1"), p.InReplyTo);
        Assert.Null(p.Forwarding);
        Assert.Equal(["alice@example.invalid"], [.. p.To.Select(a => a.Email)]);
        // Self and duplicates must be dropped.
        Assert.Equal(["bob@example.invalid", "carol@example.invalid"], [.. p.Cc.Select(a => a.Email)]);
        Assert.Equal("Re: <script>alert(1)</script> & co", p.Subject);
        // Nothing from the source may become markup: only our own tags exist.
        foreach (var bad in new[] { "<script", "<img", "<b>Alice" })
        {
            Assert.False(p.BodyHtml.Contains(bad, StringComparison.Ordinal), $"unescaped {bad} in body");
        }
        Assert.Equal(2, p.BodyHtml.Split("<blockquote").Length);
        Assert.Equal(2, p.BodyHtml.Split("</blockquote>").Length);
        foreach (var good in new[] { "&lt;b&gt;Alice&lt;/b&gt; wrote:</div>", "line1<br>line2", "<blockquote type=\"cite\">", "&lt;img src=x onerror=alert(1)&gt;" })
        {
            Assert.True(p.BodyHtml.Contains(good, StringComparison.Ordinal), $"missing {good} in body");
        }

        var f = Prefill.Create(ComposeKind.Forward, src, me);
        Assert.Equal(new MessageId("m_1"), f.Forwarding);
        Assert.Null(f.InReplyTo);
        Assert.Empty(f.To);
        Assert.Equal("Fwd: <script>alert(1)</script> & co", f.Subject);
        Assert.Contains("Forwarded message", f.BodyHtml, StringComparison.Ordinal);
        Assert.DoesNotContain("<script>", f.BodyHtml, StringComparison.Ordinal);

        var r = Prefill.Create(ComposeKind.Reply, new ComposeSource { From = [new Address { Email = "x@example.invalid" }], Text = "hi" }, me);
        Assert.Single(r.To);
        Assert.Empty(r.Cc);
        Assert.Null(r.InReplyTo);
        Assert.Contains("<div>x@example.invalid wrote:</div>", r.BodyHtml, StringComparison.Ordinal);

        var n = Prefill.Create(ComposeKind.New, src, me);
        Assert.Equal(new ComposeParams { Kind = ComposeKind.New }, n);
    }

    // Attribution is plain text for the daemon to escape: names go in as they
    // are, lines are joined with "\n", and a new message has none.
    [Fact]
    public void AttributionText()
    {
        var src = new ComposeSource
        {
            From = [new Address { Name = "<b>Alice</b>", Email = "alice@example.invalid" }, new Address { Email = "bob@example.invalid" }],
            To = [new Address { Name = "Me", Email = "me@example.invalid" }],
            Subject = "Hi & bye",
            Date = Date,
        };
        var reply = Prefill.Attribution(ComposeKind.Reply, src);
        Assert.StartsWith("On ", reply, StringComparison.Ordinal);
        Assert.EndsWith(", <b>Alice</b>, bob@example.invalid wrote:", reply, StringComparison.Ordinal);
        Assert.DoesNotContain("\n", reply, StringComparison.Ordinal);

        Assert.Equal("<b>Alice</b> wrote:", Prefill.Attribution(ComposeKind.ReplyAll, new ComposeSource { From = [src.From[0]] }));
        // Go's zero time is no date either.
        Assert.Equal("<b>Alice</b> wrote:", Prefill.Attribution(ComposeKind.Reply, new ComposeSource { From = [src.From[0]], Date = DateTimeOffset.GoZero }));

        var lines = Prefill.Attribution(ComposeKind.Forward, src).Split('\n');
        Assert.Equal(5, lines.Length);
        Assert.Equal("---------- Forwarded message ----------", lines[0]);
        Assert.StartsWith("From: ", lines[1], StringComparison.Ordinal);
        Assert.Contains("alice@example.invalid", lines[1], StringComparison.Ordinal);
        Assert.StartsWith("Date: ", lines[2], StringComparison.Ordinal);
        Assert.Equal("Subject: Hi & bye", lines[3]);
        Assert.StartsWith("To: ", lines[4], StringComparison.Ordinal);
        Assert.Contains("Me <me@example.invalid>", lines[4], StringComparison.Ordinal);

        var bare = Prefill.Attribution(ComposeKind.Forward, new ComposeSource { Subject = "x" });
        Assert.Equal(3, bare.Split('\n').Length);

        Assert.Equal("", Prefill.Attribution(ComposeKind.New, src));

        // A To: line of hundreds of addresses is cut to the daemon's cap.
        var many = new ComposeSource { Subject = "x", To = [.. Enumerable.Repeat(new Address { Name = "Řehoř", Email = "r@example.invalid" }, 300)] };
        var longText = Prefill.Attribution(ComposeKind.Forward, many);
        Assert.True(Encoding.UTF8.GetByteCount(longText) <= API.Limits.MaxDraftAttributionBytes);
        Assert.EndsWith("\x2026", longText, StringComparison.Ordinal);
        // The cut never splits a multi-byte character.
        Assert.DoesNotContain("\xFFFD", longText, StringComparison.Ordinal);
        Assert.Equal(longText, Encoding.UTF8.GetString(Encoding.UTF8.GetBytes(longText)));
    }

    [Fact]
    public void KindMode()
    {
        var want = new Dictionary<ComposeKind, ComposeMode>
        {
            [ComposeKind.New] = ComposeMode.New,
            [ComposeKind.Reply] = ComposeMode.Reply,
            [ComposeKind.ReplyAll] = ComposeMode.ReplyAll,
            [ComposeKind.Forward] = ComposeMode.Forward,
            [ComposeKind.Edit] = ComposeMode.New,
        };
        foreach (var (kind, mode) in want)
        {
            Assert.Equal(mode, kind.Mode);
        }
        Assert.Equal(Enum.GetValues<ComposeKind>().ToHashSet(), want.Keys.ToHashSet());
    }

    // FromDraft carries the daemon's template over as it is, and shows the
    // text when there is no HTML.
    [Fact]
    public void FromDraftParams()
    {
        var d = new Draft
        {
            AccountId = "acc_1",
            To = [new Address { Email = "a@example.invalid" }],
            Cc = [new Address { Email = "c@example.invalid" }],
            Subject = "Re: x",
            TextBody = "> hi",
            HtmlBody = "<p><br/></p><blockquote type=\"cite\">hi</blockquote>",
            InReplyTo = "m_1",
            Attachments = [new DraftAttachment { Id = "att_1", Filename = "a.png", ContentType = "image/png", Size = 1, Inline = true, ContentId = "c@malachi.local" }],
        };
        var blocked = new BlockedContent { RemoteImages = 2 };
        var p = Prefill.FromDraft(ComposeKind.Reply, d, blocked);
        Assert.Equal(ComposeKind.Reply, p.Kind);
        Assert.Equal(new AccountId("acc_1"), p.AccountId);
        Assert.Single(p.To);
        Assert.Single(p.Cc);
        Assert.Empty(p.Bcc);
        Assert.Equal("Re: x", p.Subject);
        Assert.Equal(d.HtmlBody, p.BodyHtml);
        Assert.Equal(new MessageId("m_1"), p.InReplyTo);
        Assert.Null(p.Forwarding);
        Assert.Single(p.Attachments);
        Assert.Equal(blocked, p.Blocked);

        var plain = Prefill.FromDraft(ComposeKind.Forward, new Draft { AccountId = "", TextBody = "a <b>\nc", Forwarding = "m_2" }, new BlockedContent());
        Assert.Equal("a &lt;b&gt;<br>c", plain.BodyHtml);
        Assert.Equal(new MessageId("m_2"), plain.Forwarding);
        Assert.Null(plain.AccountId);
    }

    // FromDraft keeps what makes the window edit a saved draft
    // (prefill_test.go TestFromDraftKeepsDraftIdentity).
    [Fact]
    public void FromDraftKeepsDraftIdentity()
    {
        var d = new Draft { Id = "d_1", AccountId = "a", Version = 4, Subject = "s", TextBody = "a < b", Replaces = "m_9" };
        var p = Prefill.FromDraft(ComposeKind.Edit, d, new BlockedContent());
        Assert.True(p.Kind == ComposeKind.Edit && p.DraftId == new DraftId("d_1") && p.Version == 4
            && p.Replaces == new MessageId("m_9") && p.AccountId == new AccountId("a"));
        Assert.Equal("a &lt; b", p.BodyHtml);
        var template = Prefill.FromDraft(ComposeKind.Reply, new Draft { AccountId = "a" }, new BlockedContent());
        Assert.True(template.DraftId is null && template.Version == 0 && template.Replaces is null);
    }

    [Fact]
    public void EscapeTextEscapesLikeHtmlEscapeString()
    {
        Assert.Equal("a &lt;b&gt; &amp; &#39;c&#39; &#34;d&#34;<br>e<br>f", Prefill.EscapeText("a <b> & 'c' \"d\"\r\ne\nf"));
        Assert.Equal("", Prefill.EscapeText(""));
    }

    [Fact]
    public void Dedupe()
    {
        Address[] list =
        [
            new Address { Name = "A", Email = "a@x.example" }, new Address { Email = " A@X.example " }, new Address { Email = "" },
            new Address { Email = "b@x.example" }, new Address { Email = "c@x.example" },
        ];
        Assert.Equal(
            ["a@x.example", "b@x.example"],
            [.. Prefill.DedupeAddresses(list, [new Address { Email = "C@x.example" }]).Select(a => a.Email)]);
    }

    // Windows: a lone surrogate in a name (a string the daemon could not
    // decode) becomes U+FFFD, as Go's strings.ToValidUTF8 makes it, and is
    // counted as such against the cap.
    [Fact]
    public void AttributionReplacesLoneSurrogates()
    {
        var src = new ComposeSource { From = [new Address { Name = "A" + (char)0xD800, Email = "a@example.invalid" }] };
        Assert.Equal("A\xFFFD wrote:", Prefill.Attribution(ComposeKind.Reply, src));
    }
}
