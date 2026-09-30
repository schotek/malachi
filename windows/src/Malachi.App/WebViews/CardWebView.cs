// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/htmlview/card.go (Card: Load, SetZoom, SetHeight,
// Reset, Release, sizeMessage) and html_card.blp; macOS: the card half of
// MessageWebView.swift. The body of one card of the conversation view: the
// sanitised HTML of one message in a view as tall as its document, which the
// pane stacks with the other cards in one scrolling column. Never one
// document of the whole conversation: in one document a message's CSS could
// hide, restyle or forge the headers of the others (docs/security.md §3.2).
//
// Everything of the viewer holds (MessageWebView over HardenedWebView, the
// viewer's profile and request gate): page script off, the CSP as a <meta>
// and as a response header, pictures only through malachi-cid:, no network,
// no navigation but the document's own load (a link the user activates is
// read with LinkProbe and handed on, never followed), the reduced context
// menu, the link under the pointer reported (for the pane's one status
// label; the card shows none of its own), the text zoom as CSS (ViewerZoom).
// The document is ViewerDocument.CompactDocument: the column's padding cut
// to the card's.
//
// The one difference from GTK's Card: GTK runs a measuring script of its own
// in an isolated world of the page, which reports the height whenever it
// changes. With page script off no listener of an injected script ever
// fires here (measured, LinkProbe), so the host measures instead: CardSize's
// host script, run whenever the height may have changed: the document
// loaded (its load event: the pictures it asked for were answered), a
// picture was served after it, the view's width or the zoom changed, or the
// view's height changed (a report within 100 ms of that alone says so, as
// sizeScript's viewport flag, for the card's governor to stop a document
// that grows with the view). A report of a document the view no longer
// shows (a view handed from one card to another, a newer document) is
// dropped.

using System;
using System.Text;
using System.Threading.Tasks;
using Malachi.Core.Html;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.Web.WebView2.Core;

namespace Malachi.App.WebViews;

/// <summary>The web view of one card of the conversation view (htmlview.Card).</summary>
public sealed partial class CardWebView : HardenedWebView
{
    // A report within this long of a change of the view's height alone
    // followed that change (sizeScript's 100 ms).
    private static readonly TimeSpan ViewportWindow = TimeSpan.FromMilliseconds(100);

    // How long the view waits for more causes before it measures.
    private static readonly TimeSpan MeasureDelay = TimeSpan.FromMilliseconds(40);

    private readonly DispatcherQueueTimer measureTimer;
    private int zoom = 100;

    // The body on display (Load's), and whether the renderer went away so
    // that the next Load happens even for the same body.
    private string? loadedBody;
    private bool needsReload;

    // The generation whose document completed its load: only its size is
    // reported.
    private long? loadedGeneration;

    // What the last measure is about: the width or the zoom changed since
    // the last report (the card's governor measures a frozen height again),
    // and until when a report follows a change of the view's height alone.
    private bool relayout;
    private DateTimeOffset viewportUntil;
    private double width = -1;
    private double height = -1;

    /// <summary>A card's view with no document; <see cref="Parts"/> serves its pictures.</summary>
    public CardWebView()
        : base(WebViewKind.Viewer)
    {
        measureTimer = DispatcherQueue.GetForCurrentThread().CreateTimer();
        measureTimer.IsRepeating = false;
        measureTimer.Interval = MeasureDelay;
        measureTimer.Tick += (_, _) => _ = MeasureAsync();
        SizeChanged += OnSizeChanged;
        SetAccessibleName(Web);
    }

    /// <summary>OnLink: a link the user activated, an http(s) or mailto target <see cref="Links.AllowedLink"/> accepts. The view never follows one.</summary>
    public Action<ActivatedLink>? OnLink { get; set; }

    /// <summary>OnHover: the link under the pointer, capped (<see cref="HoverLabel"/>); "" when there is none.</summary>
    public Action<string>? OnHover { get; set; }

    /// <summary>
    /// OnSize: the document's height in CSS pixels of the viewport (the zoom
    /// included), whether the report followed a change of the view's height
    /// alone, and whether the view's width or the zoom changed since the
    /// last report.
    /// </summary>
    public Action<double, bool, bool>? OnSize { get; set; }

    /// <summary>The view could not show its document (<see cref="HardenedWebView.Unavailable"/>): the card shows the plain text.</summary>
    public Action? OnFailed { get; set; }

    /// <summary>Serves the <c>malachi-cid:</c> pictures; null answers 404.</summary>
    public PartFetcher? Parts { get; set; }

    /// <summary>
    /// SetZoom: the <c>text-zoom</c> setting in percent (0 or less is 100),
    /// applied to the loaded document at once and to every later one; the
    /// height is measured again.
    /// </summary>
    public int Zoom
    {
        get => zoom;
        set
        {
            var p = value <= 0 ? 100 : value;
            if (p == zoom)
            {
                return;
            }
            zoom = p;
            if (loadedBody is not null && IsReady)
            {
                Run(ViewerZoom.Script(p));
                relayout = true;
                ScheduleMeasure();
            }
        }
    }

    /// <summary>
    /// Load: shows a sanitised body fragment in the compact document. The
    /// fragment is the sanitiser's output of one message and nothing else.
    /// The body on display is not loaded again unless
    /// <paramref name="reload"/> says so (its pictures kept on the mail
    /// server were downloaded: the same <c>malachi-cid:</c> URLs have
    /// something to serve now) or the renderer went away.
    /// </summary>
    public void Load(string body, bool reload)
    {
        ArgumentNullException.ThrowIfNull(body);
        if (IsReady && !needsReload && !reload && string.Equals(loadedBody, body, StringComparison.Ordinal))
        {
            return;
        }
        needsReload = false;
        loadedBody = body;
        loadedGeneration = null;
        relayout = true;
        var document = ViewerZoom.Apply(ViewerDocument.CompactDocument(body), zoom);
        LoadDocument(Encoding.UTF8.GetBytes(document), "text/html; charset=utf-8", ViewerDocument.Csp);
    }

    /// <summary>
    /// Reset: readies the view for another card of the pane (the pane's pool
    /// of views): the callbacks are dropped and the document shown goes, so a
    /// size report of the old document is never the next card's. The view's
    /// processes stay: a view handed on costs no new renderer.
    /// </summary>
    public void Reset()
    {
        OnLink = null;
        OnHover = null;
        OnSize = null;
        OnFailed = null;
        measureTimer.Stop();
        loadedBody = null;
        loadedGeneration = null;
        DropDocument();
        width = -1;
        height = -1;
        Height = double.NaN;
    }

    /// <summary>Release: lets go of the view for good (its browser resources); the owner removes it.</summary>
    public void Release()
    {
        Reset();
        measureTimer.Stop();
        Close();
    }

    private protected override Task ConfigureAsync(CoreWebView2 webView)
    {
        webView.StatusBarTextChanged += (sender, _) => OnHover?.Invoke(HoverLabel.Cap(sender.StatusBarText ?? ""));
        return Task.CompletedTask;
    }

    private protected override void OnDocumentLoaded()
    {
        loadedGeneration = Gate.Generation;
        relayout = true;
        ScheduleMeasure();
    }

    private protected override void OnUnavailable()
    {
        loadedBody = null;
        loadedGeneration = null;
        OnHover?.Invoke("");
        OnFailed?.Invoke();
    }

    // A body whose renderer went away is loaded again once (RendererRecovery).
    private protected override void OnRendererLost()
    {
        OnHover?.Invoke("");
        if (loadedBody is { } body)
        {
            needsReload = true;
            Load(body, reload: false);
        }
    }

    private protected override void OnWebViewReplaced(WebView2 web) => SetAccessibleName(web);

    private protected override void OnLinkCandidate(string uri, bool newWindow) => _ = ResolveLinkAsync(uri, newWindow);

    // MessageWebView's PartSchemeHandler: the part the URL names, if it is a
    // picture and the view still shows the document that asked for it. A
    // picture served after the load may change the height.
    private protected override async Task ServePictureAsync(CoreWebView2Environment environment, GateDecision decision, CoreWebView2WebResourceRequestedEventArgs args)
    {
        if (decision is not GateDecision.Part part || Parts is not { } fetch)
        {
            Respond(environment, args, GateDecision.Refused.NotFound);
            return;
        }
        var deferral = args.GetDeferral();
        try
        {
            var (contentType, data) = await fetch(part.Reference, GenerationToken);
            if (!Gate.IsCurrent(part.Generation) || IsClosed || !PartPath.IsImageType(contentType)
                || ResponseHeaders.Picture(contentType, data.LongLength) is not { } headers)
            {
                Respond(environment, args, GateDecision.Refused.NotFound);
                return;
            }
            Respond(environment, args, data, headers);
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            WebViewLog.PictureFailed(Log, e);
            Respond(environment, args, GateDecision.Refused.NotFound);
        }
        finally
        {
            deferral.Complete();
            if (loadedGeneration is not null)
            {
                ScheduleMeasure();
            }
        }
    }

    // The link the page's focus is on after a cancelled activation, if it
    // explains the navigation (LinkProbe).
    private async Task ResolveLinkAsync(string uri, bool newWindow)
    {
        var generation = Gate.Generation;
        var json = await EvaluateAsync(LinkProbe.Script);
        if (generation != Gate.Generation || IsClosed)
        {
            return;
        }
        var link = LinkProbe.Activation(LinkProbe.Parse(json), uri, newWindow);
        if (link is null || !Links.AllowedLink(link.Href))
        {
            WebViewLog.NavigationRefused(Log);
            return;
        }
        OnLink?.Invoke(link);
    }

    // The view's width reflows the document; a change of its height alone
    // may change what a document sized by the viewport reports.
    private void OnSizeChanged(object sender, SizeChangedEventArgs e)
    {
        var w = e.NewSize.Width;
        var h = e.NewSize.Height;
        if (Math.Abs(w - width) >= 0.5)
        {
            relayout = true;
        }
        else if (Math.Abs(h - height) >= 0.5)
        {
            viewportUntil = DateTimeOffset.UtcNow + ViewportWindow;
        }
        width = w;
        height = h;
        if (loadedGeneration is not null)
        {
            ScheduleMeasure();
        }
    }

    private void ScheduleMeasure()
    {
        if (IsClosed)
        {
            return;
        }
        measureTimer.Stop();
        measureTimer.Start();
    }

    // CardSize's host script for the document on display; the report goes
    // to the card when the view still shows that document.
    private async Task MeasureAsync()
    {
        if (loadedGeneration is not { } generation || IsClosed)
        {
            return;
        }
        var json = await EvaluateAsync(CardSize.Script);
        if (IsClosed || loadedGeneration != generation || Gate.Generation != generation || CardSize.Parse(json) is not { } css)
        {
            return;
        }
        var viewport = DateTimeOffset.UtcNow <= viewportUntil;
        var changed = relayout;
        relayout = false;
        OnSize?.Invoke(css, viewport, changed);
    }

    private static void SetAccessibleName(WebView2 web) => AutomationProperties.SetName(web, L10n.T("Message"));
}
