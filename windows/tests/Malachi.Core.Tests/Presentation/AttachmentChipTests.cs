// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of AttachmentChip: ui/internal/window/attachments.go
// (renderAttachments, buildChip, remoteIndicator, chipMenu, buildSaveAll)
// and macos AttachmentChipView.swift; the pure helpers are
// AttachmentsTests'.

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
    public void ANameIsShownCleaned()
    {
        // The daemon's sanitiser removes control and bidi characters from
        // names already; the chip cleans what it shows all the same
        // (DisplayText, Windows-only), and judges the name as received.
        var s = Summary("m1");
        var lm = new LoadedMessage { Msg = Message(s, [Attachment("2", "photo\u202Egnp.exe\u0007")]), Body = TextBody("m1") };
        var chip = AttachmentChip.For(s, lm, false, new Policy()).Chips[0];
        Assert.Equal("photognp.exe", chip.Name);
        Assert.Equal("photognp.exe", AttachmentChips.ChipName(chip.Attachment));
        Assert.False(chip.CanOpen);
        Assert.Equal("Attachment", AttachmentChips.ChipName(Attachment("3", "\u202E\u0007")));
        Assert.Equal("Attachment", AttachmentChips.ChipName(Attachment("4", "\u200B\uFEFF")));
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
        Assert.All(waiting, c => Assert.Equal(PartState.Waiting, c.State));
        Assert.Equal("", waiting[0].Tooltip);

        // A body too large to download: out of reach, whatever the server has.
        var tooBig = new LoadedMessage { Msg = Message(s, atts), Body = TextBody("m1") with { BodyState = BodyState.TooBig } };
        Assert.All(AttachmentChip.For(s, tooBig, false, null).Chips, c =>
        {
            Assert.Equal(PartState.Unavailable, c.State);
            Assert.Equal("This message is too large to download.", c.Tooltip);
        });
    }

    /// <summary>
    /// attachments.go <c>buildChip</c> with <c>partRemote</c>: a part on the
    /// mail server only is enabled, named as any other, and carries the
    /// server symbol with the reason, or the spinner while the message
    /// downloads; Save All is offered and downloads first.
    /// </summary>
    [Fact]
    public void APartOnTheServerIsEnabledAndSaysSo()
    {
        var s = Summary("m1");
        List<Attachment> atts = [Attachment("2", "a.pdf"), Attachment("3", "big.pdf", size: 300_000) with { Remote = true }];
        var lm = new LoadedMessage { Msg = Message(s, atts), Body = TextBody("m1") };
        var (chips, saveAll) = AttachmentChip.For(s, lm, false, null);
        Assert.Equal(PartState.Local, chips[0].State);
        Assert.False(chips[0].OnServer);
        Assert.Equal("", chips[0].ServerTooltip);
        Assert.True(chips[1].Available && chips[1].OnServer);
        Assert.Equal("big.pdf", chips[1].Tooltip);
        Assert.Equal("More Actions", chips[1].ArrowTooltip);
        Assert.Equal("On the server only; it is downloaded when you open it", chips[1].ServerTooltip);
        Assert.False(chips[1].Downloading);
        Assert.Equal(2, saveAll.Count);
        Assert.True(AttachmentChips.AnyRemote(saveAll, lm.Body));

        // While the message downloads: the spinner, on the parts on the
        // server only.
        var spinning = AttachmentChip.For(s, lm, false, null, downloading: true).Chips;
        Assert.False(spinning[0].Downloading);
        Assert.True(spinning[1].Downloading);

        // A body not downloaded yet: every part comes with the download.
        var pending = new LoadedMessage { Msg = Message(s, atts), Body = TextBody("m1") with { BodyState = BodyState.Pending } };
        Assert.All(AttachmentChip.For(s, pending, false, null).Chips, c =>
        {
            Assert.True(c.Available && c.OnServer);
            Assert.Equal("On the server only; it is downloaded when you open it", c.ServerTooltip);
        });

        // An attached message's parts have no numbers: never downloaded.
        Assert.All(AttachmentChip.For(s, lm, nestedView: true, null, downloading: true).Chips, c =>
        {
            Assert.False(c.Available);
            Assert.False(c.Downloading);
            Assert.Equal("", c.ServerTooltip);
        });
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
