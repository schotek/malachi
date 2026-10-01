// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/QuotedTextTests.swift
// (QuotedTextLogicTests), the counterpart of
// ui/internal/conversation/quoted_test.go: the label of the "•••" button,
// what a view revealed, the API's trimQuoted and quotedTrimmed, the button's
// offer for a cached entry and the two variants of a cached body. The
// process-wide catalogue is English here (L10n falls back to the msgids).

using System.IO;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Tests.Api;
using Malachi.Core.Tests.I18n;
using Malachi.Core.Transport;
using Xunit;

namespace Malachi.Core.Tests.Model;

public sealed class QuotedTextLogicTests
{
    // The body of id: trimmed (cut says whether anything was) or whole,
    // under remote.
    internal static MessageBodyResult Body(string id, bool whole, bool cut = true, string remote = RemoteContentPolicy.Block) => new()
    {
        MessageId = id,
        BodyState = BodyState.Fetched,
        HasHtml = true,
        Html = whole ? "<p>new</p><blockquote>old</blockquote>" : "<p>new</p>",
        Text = whole ? "new\n> old" : "new",
        RemoteContent = remote,
        SanitizerVersion = "1",
        QuotedTrimmed = whole ? null : cut,
    };

    [Fact]
    public void Labels()
    {
        Assert.Equal("Show Quoted Text", Conversation.QuotedTextLabel(shown: false));
        Assert.Equal("Hide Quoted Text", Conversation.QuotedTextLabel(shown: true));
        Assert.Equal("Show Quoted Text", QuotedTextOffer.Show.Label);
        Assert.Equal("Hide Quoted Text", QuotedTextOffer.Hide.Label);
    }

    // po_test.go TestMsgidsInTemplate for quoted.go: the button's two
    // msgids are the template's.
    [Fact]
    public void TheLabelsAreTheTemplatesMsgids()
    {
        var pot = PoFile.Parse(File.ReadAllText(RepositoryPo.Pot), "malachi.pot");
        foreach (var msgid in new[] { "Show Quoted Text", "Hide Quoted Text" })
        {
            var entry = pot.Entries.Single(e => e.Msgid == msgid && e.Msgctxt is null);
            Assert.Null(entry.MsgidPlural);
        }
        Assert.Equal(L10n.T("Show Quoted Text"), Conversation.QuotedTextLabel(shown: false));
        Assert.Equal(L10n.T("Hide Quoted Text"), Conversation.QuotedTextLabel(shown: true));
    }

    [Fact]
    public void RevealHoldsForTheSelection()
    {
        var r = new QuotedReveal();
        r.Show("t1");
        Assert.False(r.IsRevealed("a"));
        r.Set("a", true);
        r.Set("b", true);
        r.Set("b", false);
        r.Set("", true);
        Assert.True(r.IsRevealed("a") && !r.IsRevealed("b") && !r.IsRevealed(""));
        r.Show("t1");
        Assert.True(r.IsRevealed("a")); // an update of the same selection keeps it
        r.Show("t2");
        Assert.False(r.IsRevealed("a")); // another selection forgets it
        r.Set("a", true);
        r.Clear();
        Assert.False(r.IsRevealed("a"));
        r.Show("t2");
        Assert.False(r.IsRevealed("a")); // cleared, the same selection starts anew
    }

    [Fact]
    public void ApiCoding()
    {
        var trim = ApiJson.Parse(JsonCoding.EncodeToString(new MessageBodyParams { AccountId = "acc_1", MessageId = "m", TrimQuoted = true }));
        Assert.True(trim.GetProperty("trimQuoted").GetBoolean());
        foreach (var p in new[]
        {
            new MessageBodyParams { AccountId = "acc_1", MessageId = "m" },
            new MessageBodyParams { AccountId = "acc_1", MessageId = "m", TrimQuoted = false },
        })
        {
            Assert.Null(p.TrimQuoted); // false is left out
            Assert.False(ApiJson.Parse(JsonCoding.EncodeToString(p)).TryGetProperty("trimQuoted", out _));
        }
        const string old = """
            {"messageId":"m_1","bodyState":"fetched","hasHtml":false,"text":"x",
             "blocked":{"remoteImages":0,"remoteStyles":0,"remoteFonts":0,"scripts":0,"forms":0,"eventHandlers":0,"dangerousUrls":0,"embeddedFrames":0,"trackingPixels":0},
             "links":[],"remoteContent":"block","sanitizerVersion":"1"}
            """;
        var r = JsonCoding.Decode<MessageBodyResult>(old);
        Assert.True(r.QuotedTrimmed is null && !r.IsQuotedTrimmed); // an older daemon: absent is false
        Assert.False(ApiJson.Parse(JsonCoding.EncodeToString(r)).TryGetProperty("quotedTrimmed", out _));
        var cut = JsonCoding.Decode<MessageBodyResult>(
            old.Replace("\"sanitizerVersion\":\"1\"", "\"sanitizerVersion\":\"1\",\"quotedTrimmed\":true", System.StringComparison.Ordinal));
        Assert.True(cut.IsQuotedTrimmed);
        Assert.False(ApiJson.Parse(JsonCoding.EncodeToString(cut)).TryGetProperty("isQuotedTrimmed", out _)); // derived, never on the wire
    }

    [Fact]
    public void Offer()
    {
        Assert.Null(LoadedMessageText.QuotedTextOfferFor(null));
        var lm = new LoadedMessage();
        Assert.Null(LoadedMessageText.QuotedTextOfferFor(lm)); // nothing yet
        lm.Body = Body("a", whole: false, cut: false);
        Assert.Null(LoadedMessageText.QuotedTextOfferFor(lm)); // nothing was cut
        lm.Body = Body("a", whole: false);
        Assert.Equal(QuotedTextOffer.Show, LoadedMessageText.QuotedTextOfferFor(lm));
        lm.Err = new RpcException(new RpcError { Code = ErrorCode.InternalError, Message = "x" });
        Assert.Null(LoadedMessageText.QuotedTextOfferFor(lm)); // a failed body
        lm.Err = null;
        lm.ShowQuoted(true);
        Assert.Null(lm.Body);
        Assert.Equal(QuotedTextOffer.Hide, LoadedMessageText.QuotedTextOfferFor(lm)); // the whole body on its way
        lm.Err = new RpcException(new RpcError { Code = ErrorCode.InternalError, Message = "x" });
        Assert.Equal(QuotedTextOffer.Hide, LoadedMessageText.QuotedTextOfferFor(lm)); // failed: the way back stays
    }

    [Fact]
    public void VariantsTradePlaces()
    {
        var trimmed = Body("a", whole: false);
        var whole = Body("a", whole: true);
        var lm = new LoadedMessage { Body = trimmed };
        var small = lm.Size;
        Assert.False(lm.ShowQuoted(false)); // already trimmed
        Assert.True(lm.ShowQuoted(true));
        Assert.True(lm.QuotedShown && lm.Body is null && ReferenceEquals(lm.OtherBody, trimmed));
        Assert.Null(lm.SwitchPolicy); // no images loaded
        lm.Store(whole, quoted: true);
        Assert.Same(whole, lm.Body);
        Assert.True(lm.Size > small); // both variants count
        lm.ShowQuoted(false);
        Assert.Same(trimmed, lm.Body); // back without asking
        Assert.Same(whole, lm.OtherBody);
        // An answer for the variant left goes aside.
        var allowed = Body("a", whole: true, remote: RemoteContentPolicy.Allow);
        lm.Store(allowed, quoted: true);
        Assert.Same(trimmed, lm.Body);
        Assert.Same(allowed, lm.OtherBody);
        // Under another policy the variant aside goes.
        var trimmedAllowed = Body("a", whole: false, remote: RemoteContentPolicy.Allow);
        lm.Store(trimmedAllowed, quoted: false, replacing: true);
        Assert.Same(trimmedAllowed, lm.Body);
        Assert.Null(lm.OtherBody);
        lm.ShowQuoted(true);
        Assert.True(lm.SwitchPolicy == RemoteContentPolicy.Allow); // the images stay loaded in the whole body
        // The fetches in flight trade places too; a body error belongs to
        // the variant left.
        lm.Fetching = true;
        lm.Err = new RpcException(new RpcError { Code = ErrorCode.InternalError, Message = "x" });
        lm.ShowQuoted(false);
        Assert.True(!lm.Fetching && lm.FetchingOther && lm.Err is null);
    }
}
