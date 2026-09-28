// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the tests of DocumentAddress and WebViewKindExtensions
// (docs/windows-port.md §6.2).

using System;
using System.Linq;
using Malachi.Core.Html;
using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class DocumentAddressTests
{
    [Theory]
    [InlineData(WebViewKind.Viewer, "viewer", "malachi-doc://viewer/")]
    [InlineData(WebViewKind.Editor, "editor", "malachi-doc://editor/")]
    [InlineData(WebViewKind.Preview, "preview", "malachi-doc://preview/")]
    public void EachViewHasItsProfileAndHost(WebViewKind kind, string profile, string prefix)
    {
        Assert.Equal(profile, kind.ProfileName);
        Assert.Equal(prefix, DocumentAddress.Prefix(kind));
    }

    // The editor bridge installs itself on the editor's documents and on
    // nothing else: the prefix it checks is the one the editor serves under.
    [Fact]
    public void TheBridgeKnowsTheEditorsDocuments() =>
        Assert.Equal(EditorBridge.DocumentUrlPrefix, DocumentAddress.Prefix(WebViewKind.Editor));

    [Fact]
    public void AnAddressNamesViewGenerationAndNonce()
    {
        Assert.Equal("malachi-doc://viewer/7-0123abcd", DocumentAddress.For(WebViewKind.Viewer, 7, "0123abcd"));
        Assert.Throws<ArgumentOutOfRangeException>(() => DocumentAddress.For(WebViewKind.Viewer, -1, "x"));
    }

    // 128 random bits as lower-case hex, the canonical form Chromium keeps.
    [Fact]
    public void NoncesAreFreshLowerCaseHex()
    {
        var nonces = Enumerable.Range(0, 64).Select(_ => DocumentAddress.NewNonce()).ToList();
        Assert.All(nonces, n =>
        {
            Assert.Equal(32, n.Length);
            Assert.All(n, c => Assert.True(char.IsAsciiDigit(c) || c is >= 'a' and <= 'f', n));
        });
        Assert.Equal(nonces.Count, nonces.Distinct(StringComparer.Ordinal).Count());
    }
}
