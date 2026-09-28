// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/AttachmentsTests.swift (the chips:
// chipAttachmentsTest, partAvailableTest, attachedMessageTest,
// saveAllSummaryTest, chipIconTypeTest), the counterpart of
// ui/internal/window/attachments_test.go (TestChipAttachments,
// TestPartAvailable, TestAttachedMessage, TestSaveAllSummary). The other
// suites of that Swift file test Malachi.Core.Platform and are ported there
// (DangerousTypesTests: executableAttachmentTest; WindowsFileNamesTests:
// safeFileNameTest, uniqueNameTest, fileNameTest; OpenDirTests:
// sweepOpenDir, openDirWrite); claimedTypesTest, the UTType conformance
// check, is the Windows file-type policy's (Malachi.Platform.Windows.Tests
// FileTypePolicyTests); TestOpenDirFor is the Linux and Flatpak path of the
// open directory, which Windows places elsewhere (docs/windows-port.md §1).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Malachi.Core.Platform;
using Xunit;

namespace Malachi.Core.Tests.Model;

public sealed class AttachmentsTests
{
    [Fact]
    public void ChipAttachmentsTest()
    {
        var logo = Attachment("1.2", filename: "logo.png", contentId: "logo@x");
        var stray = Attachment("1.3", filename: "stray.png", contentId: "stray@x");
        var pdf = Attachment("2", filename: "report.pdf");
        Attachment[] atts = [logo, stray, pdf];
        var html = Body(BodyState.Fetched, html: "<p>x</p>", inlineParts: new() { ["logo@x"] = "1.2" });

        (string Name, MessageBodyResult? B, string[] Want)[] cases =
        [
            ("html shows the logo", html, ["1.3", "2"]),
            ("text only lists every part", Body(BodyState.Fetched, text: "hi", inlineParts: new() { ["logo@x"] = "1.2" }), ["1.2", "1.3", "2"]),
            ("withheld html lists every part", Body(BodyState.Fetched, text: "hi", withheld: true), ["1.2", "1.3", "2"]),
            ("no inline parts", Body(BodyState.Fetched, html: "<p>x</p>"), ["1.2", "1.3", "2"]),
            ("another part under the same cid", Body(BodyState.Fetched, html: "<p>x</p>", inlineParts: new() { ["logo@x"] = "9" }), ["1.2", "1.3", "2"]),
            ("no body yet", null, ["1.2", "1.3", "2"]),
        ];
        foreach (var (name, b, want) in cases)
        {
            Assert.True(want.SequenceEqual(AttachmentChips.ChipAttachments(atts, b).Select(a => a.PartId)), name);
        }

        // A part without a part number is never mistaken for a shown picture.
        Attachment[] odd = [Attachment(contentId: "x@x")];
        Assert.Single(AttachmentChips.ChipAttachments(odd, Body(BodyState.Fetched, html: "<p>x</p>"))); // empty partId hidden
    }

    [Fact]
    public void PartAvailableTest()
    {
        var small = Attachment(size: 1024);
        var huge = Attachment(size: API.Limits.MaxAttachmentDataBytes + 1);
        var edge = Attachment(size: API.Limits.MaxAttachmentDataBytes);
        var fetched = Body(BodyState.Fetched);
        (string Name, Attachment A, MessageBodyResult? B, bool Ok, string Why)[] cases =
        [
            ("no body", small, null, false, ""),
            ("pending", small, Body(BodyState.Pending), false, "This message has not been downloaded yet."),
            ("tooBig", small, Body(BodyState.TooBig), false, "This message is too large to download."),
            ("failed", small, Body(BodyState.Failed), false, "This message could not be read."),
            ("fetched", small, fetched, true, ""),
            ("at the cap", edge, fetched, true, ""),
            ("over the cap", huge, fetched, false, "Attachments over 16.0 MiB cannot be opened or saved yet."),
            // A state a newer daemon adds says nothing yet.
            ("unknown state", small, Body(new BodyState("archived")), false, ""),
        ];
        foreach (var (name, a, b, ok, why) in cases)
        {
            var got = AttachmentChips.PartAvailable(a, b);
            Assert.True(got.Ok == ok && got.Why == why, $"{name}: got ({got.Ok}, {got.Why})");
        }
    }

    [Theory]
    [InlineData("report.eml", "message/rfc822", true)]
    [InlineData("attachment-1.eml", "message/rfc822", true)]
    [InlineData("whatever", "Message/RFC822; name=x", true)]
    [InlineData("Forwarded.EML", "application/octet-stream", true)]
    [InlineData("report.eml.exe", "application/octet-stream", false)]
    [InlineData("a.pdf", "application/pdf", false)]
    [InlineData("", "", false)]
    public void AttachedMessageTest(string filename, string contentType, bool want)
    {
        Assert.Equal(want, AttachmentChips.AttachedMessage(Attachment(filename: filename, contentType: contentType)));
    }

    [Fact]
    public void SaveAllSummaryTest()
    {
        Assert.Equal("Saved 1 attachment", AttachmentChips.SaveAllSummary(saved: 1, failed: 0));
        Assert.Equal("Saved 3 attachments", AttachmentChips.SaveAllSummary(saved: 3, failed: 0));
        Assert.Equal("1 of 3 attachments could not be saved", AttachmentChips.SaveAllSummary(saved: 2, failed: 1));
    }

    // Windows only: what Save All left out, with its plural.
    [Fact]
    public void SaveAllSkippedTest()
    {
        Assert.Equal(
            "1 attachment was not saved; save programs and scripts with Save As…",
            AttachmentChips.SaveAllSkipped(1));
        Assert.Equal(
            "3 attachments were not saved; save programs and scripts with Save As…",
            AttachmentChips.SaveAllSkipped(3));
    }

    // The type is the claimed one without its parameters; a guess from the
    // name (Windows' registered Content Type, here a table) only when the
    // sender said nothing useful; application/octet-stream when nothing is
    // known. Swift's UTType cases, as media types.
    [Fact]
    public void ChipIconTypeTest()
    {
        var registry = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase)
        {
            [".jpg"] = "image/jpeg",
            [".txt"] = "text/plain",
            [".pdf"] = "application/pdf",
        };
        string? Guess(string name) => registry.GetValueOrDefault(System.IO.Path.GetExtension(name));

        Assert.Equal("application/pdf", AttachmentChips.ChipIconType(Attachment(filename: "x.bin", contentType: "application/pdf; name=x"), Guess));
        Assert.Equal("image/jpeg", AttachmentChips.ChipIconType(Attachment(filename: "photo.JPG", contentType: "application/octet-stream"), Guess));
        Assert.Equal("text/plain", AttachmentChips.ChipIconType(Attachment(filename: "notes.txt"), Guess));
        Assert.Equal("application/octet-stream", AttachmentChips.ChipIconType(Attachment(filename: "blob"), Guess));
        Assert.Equal("application/octet-stream", AttachmentChips.ChipIconType(Attachment(), Guess));
        // The claimed type wins over the name, in lower case.
        Assert.Equal("image/png", AttachmentChips.ChipIconType(Attachment(filename: "notes.txt", contentType: " Image/PNG "), Guess));
        // Without a guesser, a name tells nothing.
        Assert.Equal("application/octet-stream", AttachmentChips.ChipIconType(Attachment(filename: "notes.txt")));
        // A guess with parameters is cut like a claim.
        Assert.Equal("text/plain", AttachmentChips.ChipIconType(Attachment(filename: "a.txt"), _ => "Text/Plain; charset=utf-8"));
        Assert.Equal("application/octet-stream", AttachmentChips.ChipIconType(Attachment(filename: "a.txt"), _ => " "));

        Assert.Equal("x.pdf", AttachmentChips.ChipName(Attachment(filename: "  x.pdf ")));
        Assert.Equal("Attachment", AttachmentChips.ChipName(Attachment(filename: " ")));
    }

    // fileName over the API records: what message.part served, else the
    // listed name, each through WindowsFileNames (WindowsFileNamesTests
    // FileNameTest holds the name cases).
    [Fact]
    public void FileNameTakesTheServedNameFirst()
    {
        var a = Attachment(filename: "listed.pdf");
        Assert.Equal("served.pdf", AttachmentChips.FileName(Part("served.pdf"), a));
        Assert.Equal("listed.pdf", AttachmentChips.FileName(Part(""), a));
        Assert.Equal("listed.pdf", AttachmentChips.FileName(null, a));
        Assert.Equal("a_b.txt", AttachmentChips.FileName(Part("a:b.txt"), a));
        Assert.Equal(WindowsFileNames.Fallback, AttachmentChips.FileName(Part(""), Attachment()));
        Assert.Equal("x_y.txt", AttachmentChips.FileName(Part("x¥y.txt"), a, new HashSet<char> { '¥' }));
    }

    private static Attachment Attachment(
        string partId = "", string filename = "", string contentType = "", long size = 0, string? contentId = null) =>
        new()
        {
            PartId = partId,
            Filename = filename,
            ContentType = contentType,
            Size = size,
            Inline = contentId is not null,
            ContentId = contentId,
        };

    private static MessageBodyResult Body(
        BodyState state, string? html = null, string text = "", bool? withheld = null,
        Dictionary<string, string>? inlineParts = null) =>
        new()
        {
            MessageId = new MessageId("m"),
            BodyState = state,
            HasHtml = html is not null,
            Html = html,
            HtmlWithheld = withheld,
            Text = text,
            InlineParts = inlineParts,
            RemoteContent = RemoteContentPolicy.Block,
            SanitizerVersion = "1",
        };

    private static MessagePartResult Part(string filename) => new()
    {
        PartId = "1",
        ContentType = "application/octet-stream",
        Filename = filename,
        Size = 0,
        Data = [],
    };
}
