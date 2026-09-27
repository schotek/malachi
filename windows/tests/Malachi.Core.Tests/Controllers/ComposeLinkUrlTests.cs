// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/ComposeControllerTests.swift
// (ComposeLinkURLTests, both tests): the link popover's URL check
// (ui/internal/compose/compose.go insertLink), whose Go original has no
// test of its own.

using Malachi.Core.Controllers;
using Xunit;

namespace Malachi.Core.Tests.Controllers;

public sealed class ComposeLinkUrlTests
{
    [Theory]
    [InlineData("https://example.com/a?b=c", "https://example.com/a?b=c")]
    [InlineData("  HTTP://Example.com/x  ", "http://Example.com/x")]
    [InlineData("mailto:alice@example.org", "mailto:alice@example.org")]
    [InlineData("https://example.com/#frag", "https://example.com/#frag")]
    [InlineData("https://example.com/%C3%A9", "https://example.com/%C3%A9")]
    public void AcceptsWebAndMailLinks(string raw, string want) =>
        Assert.Equal(want, ComposeLinkUrl.Accept(raw));

    [Theory]
    [InlineData("", "empty")]
    [InlineData("example.com", "no scheme")]
    [InlineData("ftp://example.com", "another scheme")]
    [InlineData("javascript:alert(1)", "script")]
    [InlineData("https://ex ample.com/", "a space in the host")]
    [InlineData("https://example.com/%zz", "a bad escape")]
    [InlineData("https://example.com:port/", "a bad port")]
    [InlineData("http://a\u0007b", "a control character")]
    [InlineData("https://example.com/#%zz", "a bad escape in the fragment")]
    public void RefusesWhatGoRefuses(string raw, string why) =>
        Assert.True(ComposeLinkUrl.Accept(raw) is null, why);
}
