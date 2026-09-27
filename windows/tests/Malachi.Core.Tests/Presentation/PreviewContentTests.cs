// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the tests of PreviewContent, what the previewer renders
// (docs/windows-port.md §6.6): pictures by their bytes and never SVG, PDF by
// its signature, text escaped (HTML, SVG, XML and messages as source), and
// nothing of what the platform would run.

using System;
using System.Linq;
using System.Text;
using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class PreviewContentTests
{
    private static readonly byte[] Png = Convert.FromBase64String(
        "iVBORw0KGgoAAAANSUhEUgAAAAcAAAAFCAIAAAAG+GGPAAAAEUlEQVR42mNQaHiAiRhoJAoALlM0gX31oMMAAAAASUVORK5CYII=");

    private static readonly byte[] Pdf = Encoding.ASCII.GetBytes("%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n");

    [Theory]
    [InlineData("89504E470D0A1A0A0000", "image/png")]
    [InlineData("FFD8FFE000104A464946", "image/jpeg")]
    [InlineData("474946383961010001", "image/gif")]
    [InlineData("474946383761010001", "image/gif")]
    [InlineData("524946460000000057454250565038", "image/webp")]
    [InlineData("424D000000000000000000000000", "image/bmp")]
    [InlineData("000001000100101000000100", "image/x-icon")]
    [InlineData("000002000100101000000100", "image/x-icon")]
    [InlineData("0000001C6674797061766966000000", "image/avif")]
    [InlineData("0000001C6674797068656963000000", null)]
    [InlineData("3C73766720786D6C6E733D", null)]
    [InlineData("", null)]
    public void PicturesAreKnownByTheirBytes(string hex, string? type) =>
        Assert.Equal(type, PreviewContent.SniffImage(Convert.FromHexString(hex)));

    // The bytes decide, not the claim.
    [Fact]
    public void APictureIsServedAsWhatItIs()
    {
        var p = PreviewContent.Classify("photo.jpg", "image/jpeg", Png);
        Assert.Equal(PreviewKind.Image, p.Kind);
        Assert.Equal("image/png", p.MediaType);
        Assert.Equal(PreviewKind.Image, PreviewContent.Classify("", "application/octet-stream", Png).Kind);
        // A claim without the bytes is nothing.
        Assert.Equal(PreviewKind.None, PreviewContent.Classify("photo.png", "image/png", Encoding.ASCII.GetBytes("<html>")).Kind);
    }

    // SVG is a document with scripts of its own: never a picture, its source
    // at most.
    [Fact]
    public void SvgIsSource()
    {
        var svg = Encoding.UTF8.GetBytes("<svg xmlns=\"http://www.w3.org/2000/svg\"><script>alert(1)</script></svg>");
        var p = PreviewContent.Classify("drawing.svg", "image/svg+xml", svg);
        Assert.Equal(PreviewKind.Text, p.Kind);
        Assert.Contains("<script>", p.Text, StringComparison.Ordinal);
        Assert.Equal(PreviewKind.Text, PreviewContent.Classify("drawing", "image/svg+xml", svg).Kind);
        // PNG bytes under an SVG name are not shown as a picture either.
        Assert.Equal(PreviewKind.None, PreviewContent.Classify("drawing.svg", "image/png", Png).Kind);
        Assert.Equal(PreviewKind.None, PreviewContent.Classify("x.svgz", "image/svg+xml", [0x1F, 0x8B, 0x08, 0x00]).Kind);
    }

    [Fact]
    public void PdfByItsSignature()
    {
        var p = PreviewContent.Classify("report.pdf", "application/pdf", Pdf);
        Assert.Equal(PreviewKind.Pdf, p.Kind);
        Assert.Equal("application/pdf", p.MediaType);
        // Within the first KiB, as readers look for it; not after.
        var late = new byte[1030].Concat(Pdf).ToArray();
        Assert.Equal(PreviewKind.None, PreviewContent.Classify("report.pdf", "application/pdf", late).Kind);
        var early = new byte[500].Concat(Pdf).ToArray();
        Assert.Equal(PreviewKind.Pdf, PreviewContent.Classify("report.pdf", "application/pdf", early).Kind);
        Assert.Equal(PreviewKind.None, PreviewContent.Classify("report.pdf", "application/pdf", Png.AsSpan(0, 4)).Kind);
    }

    [Theory]
    [InlineData("page.html", "text/html")]
    [InlineData("page.htm", "application/octet-stream")]
    [InlineData("message.eml", "message/rfc822")]
    [InlineData("data.xml", "application/xml")]
    [InlineData("notes.txt", "text/plain")]
    [InlineData("table.csv", "text/csv")]
    [InlineData("invite.ics", "text/calendar")]
    [InlineData("config.json", "application/json")]
    [InlineData("README", "")]
    [InlineData("README", "application/octet-stream")]
    public void TextAndSourceAreText(string name, string type)
    {
        var p = PreviewContent.Classify(name, type, Encoding.UTF8.GetBytes("<b>bold</b> & text"));
        Assert.Equal(PreviewKind.Text, p.Kind);
        Assert.Equal("<b>bold</b> & text", p.Text);
    }

    // What the platform would run gets the panel, whatever its bytes
    // (DangerousTypes): a script is not even shown as text.
    [Theory]
    [InlineData("setup.exe", "application/octet-stream")]
    [InlineData("photo.png.exe", "image/png")]
    [InlineData("run.js", "text/javascript")]
    [InlineData("page.hta", "text/html")]
    [InlineData("archive.mht", "message/rfc822")]
    [InlineData("script.ps1", "text/plain")]
    [InlineData("invoice.pdf.lnk", "application/pdf")]
    public void WhatWouldRunIsNotRendered(string name, string type)
    {
        Assert.Equal(PreviewKind.None, PreviewContent.Classify(name, type, Png).Kind);
        Assert.Equal(PreviewKind.None, PreviewContent.Classify(name, type, Encoding.UTF8.GetBytes("text")).Kind);
    }

    [Theory]
    [InlineData("archive.zip", "application/zip")]
    [InlineData("doc.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")]
    [InlineData("data.bin", "application/octet-stream")]
    [InlineData("scan.tiff", "image/tiff")]
    public void OtherTypesGetThePanel(string name, string type) =>
        Assert.Equal(PreviewKind.None, PreviewContent.Classify(name, type, Encoding.UTF8.GetBytes("PK\u0003\u0004 text")).Kind);

    [Fact]
    public void NothingIsNothing() => Assert.Same(PreviewContent.Nothing, PreviewContent.Classify("a.txt", "text/plain", []));

    // A byte-order mark decides, then the claimed charset, then UTF-8 when
    // valid, Windows-1252 otherwise; a NUL is not text.
    [Fact]
    public void TextIsDecoded()
    {
        Assert.Equal("čeština", PreviewContent.DecodeText([0xEF, 0xBB, 0xBF, .. Encoding.UTF8.GetBytes("čeština")], "text/plain; charset=iso-8859-2"));
        Assert.Equal("čeština", PreviewContent.DecodeText([0xFF, 0xFE, .. Encoding.Unicode.GetBytes("čeština")], null));
        Assert.Equal("čeština", PreviewContent.DecodeText([0xFE, 0xFF, .. Encoding.BigEndianUnicode.GetBytes("čeština")], null));
        Assert.Equal("čeština", PreviewContent.DecodeText([0xE8, 0x65, 0xB9, 0x74, 0x69, 0x6E, 0x61], "text/plain; charset=\"ISO-8859-2\""));
        Assert.Equal("čeština", PreviewContent.DecodeText([0xE8, 0x65, 0x9A, 0x74, 0x69, 0x6E, 0x61], "text/plain; format=flowed; charset=windows-1250"));
        Assert.Equal("čeština", PreviewContent.DecodeText(Encoding.UTF8.GetBytes("čeština"), "text/plain"));
        Assert.Equal("čeština", PreviewContent.DecodeText(Encoding.UTF8.GetBytes("čeština"), "text/plain; charset=no-such-charset"));
        Assert.Equal("naïve – š", PreviewContent.DecodeText([0x6E, 0x61, 0xEF, 0x76, 0x65, 0x20, 0x96, 0x20, 0x9A], "text/plain"));
        Assert.Null(PreviewContent.DecodeText([0x61, 0x00, 0x62], "text/plain"));
        Assert.Null(PreviewContent.DecodeText([0xFF, 0xFE, 0x00, 0x00], null));
        Assert.Equal(PreviewKind.None, PreviewContent.Classify("notes.txt", "text/plain", [0x61, 0x00, 0x62]).Kind);
    }
}
