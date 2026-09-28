// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/NotificationTextTests.swift, the
// counterpart of ui/internal/window/notify_test.go (TestNotificationText,
// TestNearestInterval, TestIndexOfRetention), which also holds the
// preference-choice cases of preferences.go.

using System;
using System.Globalization;
using System.Text;
using System.Xml;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Xunit;

namespace Malachi.Core.Tests.Model;

public sealed class NotificationTextTests
{
    [Fact]
    public void NotificationTextTest()
    {
        var n = new NewMessageNotification
        {
            AccountId = new AccountId("a"),
            FolderId = new FolderId("f"),
            Message = Summary("m") with
            {
                From = [new Address { Name = "Alice Example", Email = "alice@example.invalid" }],
                Subject = "  Lunch?  ",
            },
        };
        var got = NotificationText.Of(n);
        Assert.Equal("Alice Example", got.Title);
        Assert.Equal("Lunch?", got.Body);

        n = n with { Message = n.Message with { From = [], Subject = "" } };
        got = NotificationText.Of(n);
        Assert.Equal("New message", got.Title);
        Assert.Equal("(No subject)", got.Body);

        n = n with { Message = n.Message with { Subject = new string('ž', 500) } };
        got = NotificationText.Of(n);
        Assert.True(Scalars(got.Body) <= NotificationText.NotificationBodyMax + 1);
        Assert.EndsWith("…", got.Body, StringComparison.Ordinal);
        Assert.Contains("ž", got.Body, StringComparison.Ordinal);
        Assert.DoesNotContain("�", got.Body, StringComparison.Ordinal); // cap left an invalid UTF-8 sequence
        // 200 bytes hold exactly 100 two-byte characters.
        Assert.Equal(101, Scalars(got.Body));

        // The title, a display name, is capped the same way.
        n = n with { Message = n.Message with { From = [new Address { Name = new string('ž', 500), Email = "x@example.invalid" }] } };
        got = NotificationText.Of(n);
        Assert.Equal(101, Scalars(got.Title));
        Assert.EndsWith("…", got.Title, StringComparison.Ordinal);
        Assert.DoesNotContain("�", got.Title, StringComparison.Ordinal);
        n = n with { Message = n.Message with { From = [new Address { Name = new string('a', 200), Email = "x@example.invalid" }] } };
        Assert.Equal(200, Scalars(NotificationText.Of(n).Title)); // a title at the cap is not cut
    }

    // Windows-only (DisplayText, docs/security.md §4): the review's sender
    // and subject. The bell made the toast's XML invalid, so Windows dropped
    // the notification; the overrides turned the texts around.
    [Fact]
    public void NotificationTextIsCleaned()
    {
        var n = new NewMessageNotification
        {
            AccountId = new AccountId("a"),
            FolderId = new FolderId("f"),
            Message = Summary("m") with
            {
                From = [new Address { Name = Text.DisplayTextTests.HostileName, Email = "admin@evil.example" }],
                Subject = Text.DisplayTextTests.HostileSubject,
            },
        };
        var got = NotificationText.Of(n);
        Assert.Equal(Text.DisplayTextTests.CleanedName, got.Title);
        Assert.Equal(Text.DisplayTextTests.CleanedSubject, got.Body);
        XmlConvert.VerifyXmlChars(got.Title);
        XmlConvert.VerifyXmlChars(got.Body);

        // Nothing but controls is nothing: the placeholders.
        n = n with { Message = n.Message with { From = [new Address { Name = "\u202E", Email = "\u0007" }], Subject = "\u0007\u202E\r\n" } };
        got = NotificationText.Of(n);
        Assert.Equal("New message", got.Title);
        Assert.Equal("(No subject)", got.Body);

        // Cleaned before the cap: every control of a long subject is gone.
        n = n with { Message = n.Message with { Subject = string.Concat(System.Linq.Enumerable.Repeat("a\u0001\u202E", 300)) } };
        got = NotificationText.Of(n);
        XmlConvert.VerifyXmlChars(got.Body);
        Assert.DoesNotContain("\u202E", got.Body, StringComparison.Ordinal);
    }

    // A character outside the Basic Multilingual Plane is one scalar of four
    // bytes: the cap never splits its surrogate pair.
    [Fact]
    public void NotificationTextCutsOnScalars()
    {
        var subject = string.Concat(System.Linq.Enumerable.Repeat("😀", 60)); // 240 bytes
        var n = new NewMessageNotification
        {
            AccountId = new AccountId("a"),
            FolderId = new FolderId("f"),
            Message = Summary("m") with { Subject = subject },
        };
        var body = NotificationText.Of(n).Body;
        Assert.Equal(51, Scalars(body)); // 50 × 4 bytes and the ellipsis
        Assert.Equal(NotificationText.NotificationBodyMax + 3, Encoding.UTF8.GetByteCount(body));
        Assert.DoesNotContain("�", body, StringComparison.Ordinal);
    }

    [Theory]
    [InlineData(0, 0)]
    [InlineData(-5, 0)]
    [InlineData(60, 1)]
    [InlineData(300, 1)]
    [InlineData(500, 1)]
    [InlineData(700, 2)]
    [InlineData(900, 2)]
    [InlineData(1300, 2)]
    [InlineData(1400, 3)]
    [InlineData(1800, 3)]
    [InlineData(99999, 3)]
    public void NearestIntervalTest(int input, int want)
    {
        Assert.Equal(want, PreferenceChoices.NearestInterval(input));
    }

    [Fact]
    public void IndexOfPolicyTest()
    {
        Assert.Equal(2, PreferenceChoices.IndexOfPolicy(RemoteContentPolicy.Allow));
        Assert.Equal(0, PreferenceChoices.IndexOfPolicy(new RemoteContentPolicy("bogus")));
    }

    [Theory]
    [InlineData(7, 0)]
    [InlineData(10, 0)]
    [InlineData(30, 1)]
    [InlineData(60, 1)]
    [InlineData(90, 2)]
    [InlineData(200, 2)]
    [InlineData(365, 3)]
    [InlineData(1000, 3)]
    [InlineData(0, 4)]
    [InlineData(-1, 4)]
    public void IndexOfRetentionTest(int input, int want)
    {
        Assert.Equal(want, PreferenceChoices.IndexOfRetention(input));
    }

    [Fact]
    public void IndexOfRetentionTestMapsEveryChoiceBack()
    {
        for (var i = 0; i < PreferenceChoices.RetentionChoices.Count; i++)
        {
            Assert.True(i == PreferenceChoices.IndexOfRetention(PreferenceChoices.RetentionChoices[i]),
                string.Create(CultureInfo.InvariantCulture, $"retentionChoices[{i}]={PreferenceChoices.RetentionChoices[i]} maps back"));
        }
    }

    private static int Scalars(string s)
    {
        var n = 0;
        foreach (var unused in s.EnumerateRunes())
        {
            n++;
        }
        return n;
    }

    private static MessageSummary Summary(string id) => new()
    {
        Id = new MessageId(id),
        AccountId = new AccountId("acc"),
        FolderId = new FolderId("f"),
        Subject = "",
        Date = DateTimeOffset.MinValue,
        Snippet = "",
        HasAttachments = false,
        Size = 0,
    };
}
