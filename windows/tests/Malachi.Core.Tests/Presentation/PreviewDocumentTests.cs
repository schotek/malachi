// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the tests of PreviewDocument and PreviewPanel
// (docs/windows-port.md §6.6).

using System;
using Malachi.Core.Api;
using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class PreviewDocumentTests
{
    // Source stays source: nothing of it is markup of the document.
    [Fact]
    public void TextIsEscaped()
    {
        var html = PreviewDocument.Text("<script>alert('x')</script> & \"q\"\0");
        Assert.Contains("<pre>&lt;script&gt;alert(&#39;x&#39;)&lt;/script&gt; &amp; &quot;q&quot;�</pre>", html, StringComparison.Ordinal);
        Assert.DoesNotContain("<script>", html, StringComparison.Ordinal);
        Assert.Equal("&lt;&gt;&amp;&quot;&#39;", PreviewDocument.Escape("<>&\"'"));
    }

    // The document's own CSP, fixed title and light canvas, as the viewer's.
    [Fact]
    public void TheDocumentIsLockedDown()
    {
        var html = PreviewDocument.Text("x");
        Assert.StartsWith("<!DOCTYPE html><html><head><meta charset=\"utf-8\"><title>Malachi Mail</title>"
            + "<meta http-equiv=\"Content-Security-Policy\" content=\"default-src 'none'; style-src 'unsafe-inline'\">",
            html, StringComparison.Ordinal);
        Assert.Contains("background: #ffffff", html, StringComparison.Ordinal);
        Assert.Contains("white-space: pre-wrap", html, StringComparison.Ordinal);
        Assert.Equal("default-src 'none'; style-src 'unsafe-inline'", PreviewDocument.Csp);
        Assert.StartsWith("default-src 'none'", PreviewDocument.MediaCsp, StringComparison.Ordinal);
    }

    [Fact]
    public void ThePanel()
    {
        var a = new Attachment { PartId = "2", Filename = " Report Q3.PDF ", ContentType = "application/pdf", Size = 3 << 20, Inline = false };
        var panel = PreviewPanel.For(a, null, "PDF Document");
        Assert.Equal(new PreviewPanel("Report Q3.PDF", "3.0 MiB", "PDF Document", ".pdf"), panel);
        // The fetched length wins over the listed size; no type name leaves
        // the claimed type.
        Assert.Equal(new PreviewPanel("Report Q3.PDF", "2 KiB", "application/pdf", ".pdf"), PreviewPanel.For(a, 2048, " "));
        var unnamed = a with { Filename = "", ContentType = "" };
        Assert.Equal(new PreviewPanel("Attachment", "3.0 MiB", "application/octet-stream", ""), PreviewPanel.For(unnamed, null, null));
    }

    // Only a plain extension reaches the shell's lookup.
    [Theory]
    [InlineData("report.PDF", ".pdf")]
    [InlineData("archive.tar.gz", ".gz")]
    [InlineData("invoice.exe.", ".exe")]
    [InlineData("noextension", "")]
    [InlineData("trailing.", "")]
    [InlineData("", "")]
    [InlineData(null, "")]
    [InlineData("a.verylongextension12", "")]
    [InlineData("a.p-df", "")]
    [InlineData("a.pdf:stream", "")]
    [InlineData("a.čs", "")]
    public void TheIconExtension(string? name, string extension) => Assert.Equal(extension, PreviewPanel.IconExtensionOf(name));
}
