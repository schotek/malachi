// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The signature half of the list rows: ui/internal/signature ListIconFor over
// MessageSummary.Signature (docs/plans/signatures.md §6): a valid, valid-seen,
// invalid, revoked or mismatching signature has a small icon next to the
// paper clip, the grey verdicts, untrusted and weak none, a conversation shows
// its latest member's, and the row's name says it. The signature part of a
// message with a verdict gets no attachment chip (S15).

using System;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Malachi.Core.Tests.Model;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class SignatureMessageRowTests
{
    private static readonly RowAppearance Look = new(Compact: false, ShowPreview: true, ShowAvatars: true, Monochrome: false, Grouped: true);

    private static MessageSummary Signed(string id, string? status) =>
        MailModelTests.Summary(id) with
        {
            Signature = status is null ? null : new MessageSignature { Kind = SignatureKind.Smime, Status = status },
        };

    [Theory]
    [InlineData(SignatureStatus.Valid, "security-high-symbolic", "success", "Valid signature")]
    [InlineData(SignatureStatus.ValidSeen, "security-medium-symbolic", "success", "Valid signature, key not confirmed")]
    [InlineData(SignatureStatus.Invalid, "dialog-error-symbolic", "error", "Invalid signature")]
    [InlineData(SignatureStatus.Revoked, "dialog-error-symbolic", "error", "Revoked certificate")]
    [InlineData(SignatureStatus.Mismatch, "dialog-warning-symbolic", "warning", "Signed by someone else")]
    [InlineData(SignatureStatus.Untrusted, "", "", "")]
    [InlineData(SignatureStatus.Weak, "", "", "")]
    [InlineData(SignatureStatus.UnknownKey, "", "", "")]
    [InlineData(SignatureStatus.Unsupported, "", "", "")]
    [InlineData(SignatureStatus.Error, "", "", "")]
    [InlineData("somethingNew", "", "", "")]
    public void AVerdictHasAnIconOrNone(string status, string icon, string style, string tooltip)
    {
        var s = Signed("m1", status);
        var row = new MessageRow(new ListKey(s.ThreadId, s.Id));
        row.Update(new ListRow { Key = new ListKey(s.ThreadId, s.Id), Message = s }, MailModel.SummaryMessage(s), Look, s.Date);
        Assert.Equal((icon, style, tooltip), (row.SignatureIcon, row.SignatureStyle, row.SignatureTooltip));
        // The row's name says the verdict it shows ("" is in every string, so only a shown one is looked for).
        if (tooltip.Length > 0)
        {
            Assert.Contains(tooltip, row.ToString(), StringComparison.Ordinal);
        }
    }

    [Fact]
    public void TheIconFollowsTheRowAndAConversationShowsItsLatestMembers()
    {
        var key = new ListKey("t", "m1");
        var row = new MessageRow(key);
        var signed = Signed("m1", SignatureStatus.Valid);
        row.Update(new ListRow { Key = key, Message = signed }, MailModel.SummaryMessage(signed), Look, signed.Date);
        Assert.Equal("security-high-symbolic", row.SignatureIcon);
        var plain = Signed("m1", null);
        row.Update(new ListRow { Key = key, Message = plain }, MailModel.SummaryMessage(plain), Look, plain.Date);
        Assert.Equal("", row.SignatureIcon);

        var latest = Signed("m2", SignatureStatus.Invalid);
        var t = ThreadModelTests.Thr("t1", 3, 1, latest);
        var thread = new ListRow { Key = new ListKey(Thread: t.Id), Thread = true, Message = t.Latest, Summary = t };
        var threadRow = new MessageRow(thread.Key);
        threadRow.Update(thread, MailModel.SummaryMessage(t.Latest), Look, t.LatestDate);
        Assert.True(threadRow.IsThread);
        Assert.Equal("dialog-error-symbolic", threadRow.SignatureIcon);
    }

    [Fact]
    public void TheSignaturePartOfAVerdictGetsNoChip()
    {
        Attachment[] atts =
        [
            new() { PartId = "1.2", Filename = "a.pdf", ContentType = "application/pdf", Size = 1, Inline = false },
            new() { PartId = "2", Filename = "smime.p7s", ContentType = "application/pkcs7-signature", Size = 1, Inline = false },
        ];
        Assert.Equal(2, AttachmentChips.ChipAttachments(atts, null).Count);
        var verdict = new SignatureSummary { Kind = SignatureKind.Smime, Status = SignatureStatus.Valid, PartId = "2" };
        Assert.Equal(["1.2"], AttachmentChips.ChipAttachments(atts, null, signature: verdict).Select(a => a.PartId));
    }
}
