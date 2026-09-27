// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the tests of RequestGate and ResponseHeaders
// (docs/windows-port.md §6.2), the counterpart of what the macOS content
// rule list and scheme handlers let through (MessageWebView.swift
// ruleListJSON, PartSchemeHandler.swift parse, CIDSchemeHandler.swift id).

using Malachi.Core.Api;
using Malachi.Core.Html;
using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class RequestGateTests
{
    // The document is served once; a second request for it, and a request
    // for an older document, are refused.
    [Fact]
    public void TheDocumentIsServedOnce()
    {
        var gate = new RequestGate(WebViewKind.Viewer);
        Assert.Null(gate.DocumentUri);
        var first = gate.NextDocument();
        Assert.StartsWith("malachi-doc://viewer/1-", first, System.StringComparison.Ordinal);
        Assert.Equal(new GateDecision.Document(1), gate.Decide(first));
        Assert.True(gate.DocumentServed);
        Assert.Same(GateDecision.Refused.Forbidden, gate.Decide(first));

        var second = gate.NextDocument();
        Assert.NotEqual(first, second);
        Assert.Equal(2, gate.Generation);
        Assert.Same(GateDecision.Refused.Forbidden, gate.Decide(first));
        Assert.Equal(new GateDecision.Document(2), gate.Decide(second));
    }

    // Another view's document, a guessed nonce and a different case are not
    // the document.
    [Fact]
    public void OnlyTheExactDocumentIsServed()
    {
        var gate = new RequestGate(WebViewKind.Editor);
        var uri = gate.NextDocument();
        Assert.Same(GateDecision.Refused.Forbidden, gate.Decide(uri.ToUpperInvariant()));
        Assert.Same(GateDecision.Refused.Forbidden, gate.Decide(uri + "/"));
        Assert.Same(GateDecision.Refused.Forbidden, gate.Decide(uri + "?x"));
        Assert.Same(GateDecision.Refused.Forbidden, gate.Decide(DocumentAddress.For(WebViewKind.Editor, 1, "00")));
        Assert.Same(GateDecision.Refused.Forbidden, gate.Decide(uri.Replace("editor", "viewer", System.StringComparison.Ordinal)));
        Assert.False(gate.DocumentServed);
    }

    // PartSchemeHandler.parse: the path after malachi-cid: must parse.
    [Fact]
    public void TheViewerServesItsParts()
    {
        var gate = new RequestGate(WebViewKind.Viewer);
        // Before a document there is nothing to serve pictures for.
        Assert.Same(GateDecision.Refused.NotFound, gate.Decide("malachi-cid:acc_1/msg_2/2"));
        gate.NextDocument();
        var part = Assert.IsType<GateDecision.Part>(gate.Decide("malachi-cid:acc_1/msg_2/1.2"));
        Assert.Equal(new PartReference(new AccountId("acc_1"), new MessageId("msg_2"), "1.2"), part.Reference);
        Assert.Equal(1, part.Generation);
        Assert.IsType<GateDecision.Part>(gate.Decide("MALACHI-CID:acc_1/msg_2/2"));
        foreach (var bad in new[]
        {
            "malachi-cid:acc_1/msg_2", "malachi-cid:acc_1/msg_2/2?x", "malachi-cid:acc_1/msg_2/2#f", "malachi-cid:../msg/2",
            "malachi-cid:acc_1/msg_2/2.", "malachi-cid:acc 1/msg_2/2", "malachi-cid:", "malachi-cid://acc_1/msg_2/2",
        })
        {
            Assert.Same(GateDecision.Refused.NotFound, gate.Decide(bad));
        }
    }

    // CIDSchemeHandler.id(of:): the editor asks the registry for the id; the
    // gate only names it.
    [Fact]
    public void TheEditorServesItsInlinePictures()
    {
        var gate = new RequestGate(WebViewKind.Editor);
        Assert.Same(GateDecision.Refused.NotFound, gate.Decide("cid:x@y"));
        gate.NextDocument();
        Assert.Equal(new GateDecision.InlineImage("x@y", 1), gate.Decide("cid:x@y"));
        Assert.Equal(new GateDecision.InlineImage("../../etc/passwd", 1), gate.Decide("cid:../../etc/passwd"));
        Assert.Equal(new GateDecision.InlineImage("part", 1), gate.Decide("CID:part?query#fragment"));
    }

    // A view never serves another view's scheme, and the previewer no
    // picture at all: a message cannot address compose attachments, and a
    // draft cannot name a received message's parts.
    [Theory]
    [InlineData(WebViewKind.Viewer, "cid:x@y")]
    [InlineData(WebViewKind.Editor, "malachi-cid:acc_1/msg_2/2")]
    [InlineData(WebViewKind.Preview, "cid:x@y")]
    [InlineData(WebViewKind.Preview, "malachi-cid:acc_1/msg_2/2")]
    public void NoViewServesAnothersPictures(WebViewKind kind, string uri)
    {
        var gate = new RequestGate(kind);
        gate.NextDocument();
        Assert.Same(GateDecision.Refused.Forbidden, gate.Decide(uri));
    }

    // Everything else is refused, data: and our own document scheme included.
    [Theory]
    [InlineData("http://127.0.0.1:9/x.png")]
    [InlineData("https://example.org/")]
    [InlineData("file://127.0.0.1/c$/windows/win.ini")]
    [InlineData("data:image/png;base64,AAAA")]
    [InlineData("about:blank")]
    [InlineData("malachi-doc://viewer/")]
    [InlineData("javascript:alert(1)")]
    [InlineData("ws://127.0.0.1/")]
    [InlineData("")]
    public void EverythingElseIsForbidden(string uri)
    {
        foreach (var kind in new[] { WebViewKind.Viewer, WebViewKind.Editor, WebViewKind.Preview })
        {
            var gate = new RequestGate(kind);
            gate.NextDocument();
            Assert.Same(GateDecision.Refused.Forbidden, gate.Decide(uri));
        }
    }

    // A picture fetched for an older document is not served (404 once the
    // generation moved on); after Retire nothing is current.
    [Fact]
    public void AGenerationEnds()
    {
        var gate = new RequestGate(WebViewKind.Viewer);
        var uri = gate.NextDocument();
        var part = Assert.IsType<GateDecision.Part>(gate.Decide("malachi-cid:a/m/1"));
        Assert.True(gate.IsCurrent(part.Generation));
        gate.NextDocument();
        Assert.False(gate.IsCurrent(part.Generation));
        Assert.True(gate.IsCurrent(gate.Generation));
        gate.Retire();
        Assert.Null(gate.DocumentUri);
        Assert.False(gate.IsCurrent(gate.Generation));
        Assert.Same(GateDecision.Refused.Forbidden, gate.Decide(uri));
        Assert.Same(GateDecision.Refused.NotFound, gate.Decide("malachi-cid:a/m/1"));
    }

    [Fact]
    public void RefusalsHaveTheirStatusLines()
    {
        Assert.Equal(403, GateDecision.Refused.Forbidden.Status);
        Assert.Equal("Forbidden", GateDecision.Refused.Forbidden.ReasonPhrase);
        Assert.Equal(404, GateDecision.Refused.NotFound.Status);
        Assert.Equal("Not Found", GateDecision.Refused.NotFound.ReasonPhrase);
    }

    // The CSP goes out as a header as well; nothing is sniffed, cached or
    // referred.
    [Fact]
    public void DocumentHeaders()
    {
        Assert.Equal(
            "Content-Type: text/html; charset=utf-8\r\nContent-Security-Policy: " + ViewerDocument.Csp
                + "\r\nX-Content-Type-Options: nosniff\r\nCache-Control: no-store\r\nReferrer-Policy: no-referrer",
            ResponseHeaders.Document("text/html; charset=utf-8", ViewerDocument.Csp));
        Assert.Equal("Cache-Control: no-store", ResponseHeaders.Refused);
    }

    // PartSchemeHandler.mediaType / CIDSchemeHandler.respond: the type
    // without its parameters, lower-cased, and the length.
    [Fact]
    public void PictureHeaders()
    {
        Assert.Equal(
            "Content-Type: image/png\r\nContent-Length: 42\r\nX-Content-Type-Options: nosniff\r\nCache-Control: no-store",
            ResponseHeaders.Picture(" Image/PNG; name=\"x.png\" ", 42));
    }
}
