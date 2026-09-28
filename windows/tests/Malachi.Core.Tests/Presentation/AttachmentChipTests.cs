// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of AttachmentChip: ui/internal/window/attachments.go
// (renderAttachments, buildChip, chipMenu, buildSaveAll) and macos
// AttachmentChipView.swift; the pure helpers are AttachmentsTests'.

using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Malachi.Core.Platform;
using Malachi.Core.Presentation;
using Xunit;
using static Malachi.Core.Tests.Presentation.ReaderFixtures;

namespace Malachi.Core.Tests.Presentation;

public sealed class AttachmentChipTests
{
    private sealed class Policy(params string[] dangerous) : IFileTypePolicy
    {
        public bool IsDangerous(string? fileName, string? contentType) =>
            DangerousTypes.IsDangerous(fileName, contentType) || System.Array.IndexOf(dangerous, fileName) >= 0;
    }

    [Fact]
    public void NothingUntilMessageGetAnswered()
    {
        var s = Summary("m1");
        Assert.Empty(AttachmentChip.For(s, null, false, null).Chips);
        Assert.Empty(AttachmentChip.For(s, new LoadedMessage { Body = TextBody("m1") }, false, null).Chips);
    }

    [Fact]
    public void ChipsSayWhatTheyAllow()
    {
        var s = Summary("m1");
        var lm = new LoadedMessage
        {
            Msg = Message(s, [
                Attachment("2", "report.pdf", size: 3 << 20),
                Attachment("3", "setup.exe", "application/x-msdownload", 76),
                Attachment("4", "fwd.eml", "message/rfc822", 900),
                Attachment("5", "", "text/plain", 0),
            ]),
            Body = TextBody("m1"),
        };
        var (chips, saveAll) = AttachmentChip.For(s, lm, nestedView: false, new Policy());
        Assert.Equal(4, chips.Count);

        var pdf = chips[0];
        Assert.True(pdf.Available);
        Assert.True(pdf.CanOpen);
        Assert.False(pdf.Nested);
        Assert.Equal("report.pdf", pdf.Tooltip);
        Assert.Equal("More Actions", pdf.ArrowTooltip);
        Assert.Equal("3.0 MiB", pdf.SizeText);

        var exe = chips[1];
        Assert.False(exe.CanOpen);
        Assert.Equal("Programs and scripts are not opened directly; save the file and decide yourself.", exe.Tooltip);

        var eml = chips[2];
        Assert.True(eml.Nested);
        Assert.Equal("fwd.eml", eml.Tooltip);

        // A part without a name gets the placeholder, and no size.
        Assert.Equal("Attachment", chips[3].Name);
        Assert.Equal("", chips[3].SizeText);
        Assert.Same(s, chips[3].Message);

        // Save All with two or more, all available.
        Assert.Equal(4, saveAll.Count);
    }

    [Fact]
    public void ThePlatformPolicyDisablesOpen()
    {
        var s = Summary("m1");
        var lm = new LoadedMessage { Msg = Message(s, [Attachment("2", "invoice.pdf")]), Body = TextBody("m1") };
        Assert.True(AttachmentChip.For(s, lm, false, null).Chips[0].CanOpen);
        Assert.False(AttachmentChip.For(s, lm, false, new Policy("invoice.pdf")).Chips[0].CanOpen);
    }

    [Fact]
    public void InlinePicturesOfTheHtmlOnDisplayGetNoChip()
    {
        var s = Summary("m1");
        var atts = new List<Attachment> { Attachment("2", "logo.png", "image/png", cid: "logo@x"), Attachment("3", "a.pdf") };
        var html = new LoadedMessage { Msg = Message(s, atts), Body = HtmlBody("m1", inline: new Dictionary<string, string> { ["logo@x"] = "2" }) };
        var (chips, saveAll) = AttachmentChip.For(s, html, false, null);
        Assert.Equal(["3"], [.. System.Linq.Enumerable.Select(chips, c => c.Attachment.PartId)]);
        Assert.Empty(saveAll);

        // Shown as text, the picture is a file like any other.
        var text = new LoadedMessage { Msg = Message(s, atts), Body = TextBody("m1") };
        Assert.Equal(2, AttachmentChip.For(s, text, false, null).Chips.Count);
    }

    [Fact]
    public void UnavailablePartsSayWhyAndLeaveOutSaveAll()
    {
        var s = Summary("m1");
        var atts = new List<Attachment> { Attachment("2", "a.pdf"), Attachment("3", "big.iso", size: API.Limits.MaxAttachmentDataBytes + 1) };
        var lm = new LoadedMessage { Msg = Message(s, atts), Body = TextBody("m1") };
        var (chips, saveAll) = AttachmentChip.For(s, lm, false, null);
        Assert.True(chips[0].Available);
        Assert.False(chips[1].Available);
        Assert.StartsWith("Attachments over ", chips[1].Tooltip);
        Assert.Equal(chips[1].Tooltip, chips[1].ArrowTooltip);
        Assert.Empty(saveAll);

        // While the body is on its way: unavailable, no reason yet.
        var waiting = AttachmentChip.For(s, new LoadedMessage { Msg = Message(s, atts) }, false, null).Chips;
        Assert.All(waiting, c => Assert.False(c.Available));
        Assert.Equal("", waiting[0].Tooltip);

        var pending = new LoadedMessage { Msg = Message(s, atts), Body = TextBody("m1") with { BodyState = BodyState.Pending } };
        Assert.Equal("This message has not been downloaded yet.", AttachmentChip.For(s, pending, false, null).Chips[0].Tooltip);
    }

    [Fact]
    public void TheChipsOfAnAttachedMessageOnlyNameTheParts()
    {
        var s = Summary("m1");
        var lm = new LoadedMessage { Msg = Message(s, [Attachment("", "inner.pdf"), Attachment("", "deep.eml", "message/rfc822")]), Body = TextBody("m1") };
        var (chips, saveAll) = AttachmentChip.For(s, lm, nestedView: true, null);
        Assert.All(chips, c =>
        {
            Assert.False(c.Available);
            Assert.Equal("Files inside an attached message cannot be opened or saved yet.", c.Tooltip);
        });
        Assert.Empty(saveAll);
    }
}
