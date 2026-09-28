// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the tests of PreviewDocument and PreviewPanel
// (docs/windows-port.md §6.6).

using System;
using Malachi.Core.Api;
using Malachi.Core.Html;
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

    // A PDF is embedded in a page of the previewer's own, whose title stays
    // fixed (a PDF served as the document names the window after its
    // metadata), and whose CSP admits exactly that PDF.
    [Fact]
    public void ThePdfPage()
    {
        const string content = "malachi-doc://preview/2-00ff/content";
        Assert.Equal(
            "default-src 'none'; object-src malachi-doc://preview/2-00ff/content; frame-src malachi-doc://preview/2-00ff/content; style-src 'unsafe-inline'",
            PreviewDocument.PdfCsp(content));
        var page = PreviewDocument.PdfPage(content);
        Assert.StartsWith("<!DOCTYPE html><html><head><meta charset=\"utf-8\"><title>Malachi Mail</title>"
            + "<meta http-equiv=\"Content-Security-Policy\" content=\"" + PreviewDocument.PdfCsp(content) + "\">",
            page, StringComparison.Ordinal);
        Assert.Contains("<embed type=\"application/pdf\" src=\"" + content + "\">", page, StringComparison.Ordinal);
        Assert.Contains("src=\"a&quot;b\"", PreviewDocument.PdfPage("a\"b"), StringComparison.Ordinal);
    }

    [Fact]
    public void TheFixedTitle()
    {
        Assert.Equal("Malachi Mail", FixedTitle.Title);
        Assert.Equal(ViewerDocument.Title, FixedTitle.Title);
        Assert.Equal(EditorDocument.Title, FixedTitle.Title);
        Assert.Equal(PreviewDocument.Title, FixedTitle.Title);
        Assert.True(FixedTitle.IsFixed("Malachi Mail"));
        Assert.False(FixedTitle.IsFixed("canary-pdf-title"));
        Assert.False(FixedTitle.IsFixed(null));
        Assert.Equal("Object.getOwnPropertyDescriptor(Document.prototype, 'title').set.call(document, 'Malachi Mail')", FixedTitle.Script);
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
