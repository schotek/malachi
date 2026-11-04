// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only (docs/windows-port.md §6.2): the content rule list of
// Windows. macOS blocks every load but its own scheme with a WKContentRuleList
// (MessageWebView.swift ruleListJSON, ComposeWebView.swift ruleListJSON) and
// serves that scheme with PartSchemeHandler / CIDSchemeHandler; GTK relies on
// the CSP and a dead proxy. WebView2 needs an answer for every request: a
// cancelled navigation still sends its GET (measured), and <link
// rel=prerender> passes the CSP. Each view adds a WebResourceRequested filter
// for everything before its first navigation and asks this gate, which
// knows the view's one current document and its own picture scheme, and
// refuses the rest. data: never reaches it.
//
// UI-thread-affine, as its view (docs/windows-port.md §7.1).

using System;
using Malachi.Core.Html;

namespace Malachi.Core.Presentation;

/// <summary>Decides what a web view answers each request with.</summary>
public sealed class RequestGate
{
    private const string PartPrefix = PartPath.PartScheme + ":";
    private const string CidPrefix = "cid:";

    /// <summary>A gate for a view of <paramref name="kind"/>, with no document yet.</summary>
    public RequestGate(WebViewKind kind)
    {
        Kind = kind;
    }

    /// <summary>The view this gate answers for.</summary>
    public WebViewKind Kind { get; }

    /// <summary>
    /// Counts the view's documents: a new document, or the end of one, moves
    /// it on, and a picture fetched for an older generation is answered 404
    /// (<see cref="IsCurrent"/>): WebView2 has no call that withdraws a
    /// request, as WebKit's scheme handler <c>stop</c> does.
    /// </summary>
    public long Generation { get; private set; }

    /// <summary>The URL of the view's current document; null before the first and after <see cref="Retire"/>.</summary>
    public string? DocumentUri { get; private set; }

    /// <summary>Whether the current document has been served (it is served once).</summary>
    public bool DocumentServed { get; private set; }

    /// <summary>
    /// The URL of the current document's one embedded resource
    /// (<c>&lt;document&gt;/content</c>: the previewer's PDF inside its
    /// own page); null when the document has none.
    /// </summary>
    public string? ContentUri { get; private set; }

    /// <summary>Whether the embedded resource has been served (it is served once, after the document).</summary>
    public bool ContentServed { get; private set; }

    /// <summary>
    /// Starts a new document: the next generation, a fresh nonce, and with
    /// <paramref name="withContent"/> one embedded resource under it. Returns
    /// the URL the view navigates to; the previous document's URLs no longer
    /// resolve.
    /// </summary>
    public string NextDocument(bool withContent = false)
    {
        Generation++;
        DocumentUri = DocumentAddress.For(Kind, Generation, DocumentAddress.NewNonce());
        DocumentServed = false;
        ContentUri = withContent ? DocumentUri + "/content" : null;
        ContentServed = false;
        return DocumentUri;
    }

    /// <summary>
    /// Ends the current document without a new one (the view closes, or its
    /// document is dropped): nothing is served any more, and pictures still
    /// being fetched are answered 404.
    /// </summary>
    public void Retire()
    {
        Generation++;
        DocumentUri = null;
        DocumentServed = false;
        ContentUri = null;
        ContentServed = false;
    }

    /// <summary>Whether <paramref name="generation"/> is still the view's (a fetched picture may be served).</summary>
    public bool IsCurrent(long generation) => generation == Generation && DocumentUri is not null;

    /// <summary>
    /// The answer to a request for <paramref name="requestUri"/> (as
    /// WebView2 reports it): the current document once, and its embedded
    /// resource once after it; the viewer's
    /// <c>malachi-cid:</c> parts whose path parses (404 otherwise); the
    /// editor's <c>cid:</c> ids; 403 for everything else, the document a
    /// second time, another view's scheme and <c>data:</c> included (which
    /// WebView2 never routes here; if a runtime ever did, pictures would
    /// fail rather than a rule open).
    /// </summary>
    public GateDecision Decide(string requestUri)
    {
        ArgumentNullException.ThrowIfNull(requestUri);
        if (DocumentUri is not null && string.Equals(requestUri, DocumentUri, StringComparison.Ordinal))
        {
            if (DocumentServed)
            {
                return GateDecision.Refused.Forbidden;
            }
            DocumentServed = true;
            return new GateDecision.Document(Generation);
        }
        if (ContentUri is not null && string.Equals(requestUri, ContentUri, StringComparison.Ordinal))
        {
            if (ContentServed || !DocumentServed)
            {
                return GateDecision.Refused.Forbidden;
            }
            ContentServed = true;
            return new GateDecision.Content(Generation);
        }
        if (Kind == WebViewKind.Viewer && requestUri.StartsWith(PartPrefix, StringComparison.OrdinalIgnoreCase))
        {
            // PartSchemeHandler.parse: the URL minus its scheme, checked by
            // parsePartPath; a query, a fragment or anything else fails it.
            return PartPath.ParsePartPath(requestUri[PartPrefix.Length..]) is { } part && DocumentUri is not null
                ? new GateDecision.Part(part, Generation)
                : GateDecision.Refused.NotFound;
        }
        if (Kind == WebViewKind.Editor && requestUri.StartsWith(CidPrefix, StringComparison.OrdinalIgnoreCase))
        {
            return DocumentUri is not null
                ? new GateDecision.InlineImage(CidRegistry.IdOf(requestUri), Generation)
                : GateDecision.Refused.NotFound;
        }
        return GateDecision.Refused.Forbidden;
    }
}
