// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/WebViews/MessageWebView.swift and
// PartSchemeHandler.swift; GTK: ui/internal/htmlview (view.go, scheme.go,
// document.go) and ui/data/ui/html_view.blp.
//
// The viewer of a message's sanitised HTML body: a WebView2 with page script
// off, a strict Content-Security-Policy, no network and no navigation. It is
// layer 2 of docs/security.md §3.2 for WebView2 (docs/windows-port.md §6.3):
// the backend's sanitiser is what makes the content safe, this view is what
// keeps a sanitiser bug from becoming a compromise. It knows nothing about
// mail beyond "here is a body fragment and a way to fetch its pictures".
//
// docs/security.md §3.2, layer 2, bullet by bullet, as WebView2 does it:
//
// - JavaScript off (IsScriptEnabled=false), no web messages, no host objects,
//   no dialogs, DevTools, autofill, password saving, SmartScreen, zoom
//   gestures or browser keys; InPrivate profile "viewer" (HardenedWebView).
// - The CSP of htmlview.CSP, as a <meta> of the document (ViewerDocument,
//   byte for byte GTK's with a fixed <title>) and as a response header.
// - malachi-cid: pictures served through message.part (PartSchemeHandler:
//   parsePartPath, images only, never SVG); the network otherwise denied by
//   the request gate (403 for everything but the document and its pictures),
//   the environment's resolver rule and its dead proxy.
// - Navigation intercepted: a link activation is cancelled, the link the
//   page's focus is on is read with a host script (LinkProbe; the page runs
//   none) and handed on as an ActivatedLink, which the reader decides with
//   LinkDecision (listed, masked, unlisted → confirmed, mailto: → composer);
//   a new-window request (target, middle, Ctrl or Shift click) the same.
// - The link under the pointer shown as plain text at the bottom left
//   (StatusBarTextChanged, which fires with the status bar off).
// - One view per pane, reused. A renderer that dies (or hangs, or takes the
//   browser with it) under a body reloads that body once; the same body
//   failing again is given up: Unavailable, and the reader shows the plain
//   text (RendererRecovery; GTK only logs, macOS reloads on the next Load).
// - The context menu keeps Copy and Copy Link.
//
// Dark theme: the page stays on a white canvas (baseCSS, PreferredColorScheme
// Light), as in GTK and macOS.

using System;
using System.Text;
using System.Threading.Tasks;
using Malachi.Core.Controllers;
using Malachi.Core.Html;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Microsoft.UI;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Microsoft.Web.WebView2.Core;

namespace Malachi.App.WebViews;

/// <summary>The viewer of a message's sanitised HTML body (htmlview.View, MessageWebView).</summary>
public sealed partial class MessageWebView : HardenedWebView
{
    private readonly TextBlock status;
    private readonly Border statusBox;
    private int zoom = 100;

    // The body on display, so a re-render of the same message does not
    // reload the document (and lose its scroll position) for nothing
    // (macOS loadedBody); null before the first.
    private string? loadedBody;

    // The renderer went away: the next load must happen even for the body
    // on display (macOS needsReload).
    private bool needsReload;

    /// <summary>A viewer with no document; <see cref="Parts"/> serves its pictures.</summary>
    public MessageWebView()
        : base(WebViewKind.Viewer)
    {
        // The link under the pointer, browser-style, in the bottom-left
        // corner (html_view: an osd caption label, margins 6, the middle
        // ellipsised to 80 characters; plain text).
        status = new TextBlock
        {
            FontSize = 12,
            Foreground = new SolidColorBrush(Colors.White),
            TextTrimming = TextTrimming.CharacterEllipsis,
            TextWrapping = TextWrapping.NoWrap,
            IsTextSelectionEnabled = false,
        };
        statusBox = new Border
        {
            Background = new SolidColorBrush(Windows.UI.Color.FromArgb(0xB3, 0, 0, 0)),
            CornerRadius = new CornerRadius(4),
            Padding = new Thickness(6, 2, 6, 2),
            Margin = new Thickness(6, 0, 0, 6),
            HorizontalAlignment = HorizontalAlignment.Left,
            VerticalAlignment = VerticalAlignment.Bottom,
            Visibility = Visibility.Collapsed,
            IsHitTestVisible = false,
            Child = status,
        };
        Root.Children.Add(statusBox);
        SetAccessibleName(Web);
    }

    /// <summary>
    /// A link the user activated, an http(s) or mailto target that
    /// <see cref="Links.AllowedLink"/> accepts (macOS <c>onLink</c>, GTK
    /// <c>OnLink</c>). The view itself never follows one: the reader decides
    /// it with <see cref="LinkDecision.For(ActivatedLink, System.Collections.Generic.IReadOnlyList{Core.Api.Link}, System.Func{string, string})"/>
    /// and opens, confirms (through its alerts) or composes.
    /// </summary>
    public event EventHandler<ActivatedLink>? LinkActivated;

    /// <summary>Raised when <see cref="HoveredLink"/> changes.</summary>
    public event EventHandler? HoveredLinkChanged;

    /// <summary>Serves the <c>malachi-cid:</c> pictures (<see cref="UseCache"/>); null answers 404.</summary>
    public PartFetcher? Parts { get; set; }

    /// <summary>
    /// The link under the pointer as WebView2 displays it, capped at
    /// <see cref="HoverLabel.MaxChars"/>; "" when there is none.
    /// </summary>
    public string HoveredLink { get; private set; } = "";

    /// <summary>
    /// htmlview.SetZoom: the <c>text-zoom</c> setting in percent (0 or less
    /// is 100), applied to the loaded document at once and to every later one.
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
            }
        }
    }

    /// <summary>Serves the pictures through <paramref name="cache"/> (<c>message.part</c>).</summary>
    public void UseCache(MessageCache cache)
    {
        ArgumentNullException.ThrowIfNull(cache);
        Parts = (part, token) => cache.FetchPartAsync(part.AccountId, part.MessageId, part.PartId, token);
    }

    /// <summary>
    /// htmlview.Load: shows a sanitised body fragment. The fragment is the
    /// sanitiser's output and nothing else may ever be passed here. Until the
    /// view is initialised the body waits; should that fail,
    /// <see cref="HardenedWebView.Unavailable"/> is raised and nothing loads.
    /// The body already on display is not loaded again unless
    /// <paramref name="reload"/> says so: its pictures kept on the mail
    /// server were downloaded, and the same <c>malachi-cid:</c> URLs have
    /// something to serve now (macOS <c>load(body:reload:)</c>).
    /// </summary>
    public void Load(string body, bool reload = false)
    {
        ArgumentNullException.ThrowIfNull(body);
        ShowStatus("");
        if (IsReady && !needsReload && !reload && string.Equals(loadedBody, body, StringComparison.Ordinal))
        {
            return;
        }
        needsReload = false;
        loadedBody = body;
        var document = ViewerZoom.Apply(ViewerDocument.Document(body), zoom);
        LoadDocument(Encoding.UTF8.GetBytes(document), "text/html; charset=utf-8", ViewerDocument.Csp);
    }

    /// <summary>htmlview.Clear: drops the current document (and its pictures).</summary>
    public void Clear() => Load("");

    private protected override Task ConfigureAsync(CoreWebView2 webView)
    {
        webView.StatusBarTextChanged += (sender, _) => Hover(sender.StatusBarText ?? "");
        return Task.CompletedTask;
    }

    // Also when the body failed again after it was shown again once: the
    // reader shows the plain text; its next Load loads, the same body too
    // (macOS needsReload), without a further automatic reload.
    private protected override void OnUnavailable()
    {
        loadedBody = null;
        ShowStatus("");
    }

    // The body's one reload (RendererRecovery).
    private protected override void OnRendererLost()
    {
        ShowStatus("");
        if (loadedBody is { } body)
        {
            needsReload = true;
            Load(body);
        }
    }

    private protected override void OnWebViewReplaced(WebView2 web) => SetAccessibleName(web);

    private protected override void OnLinkCandidate(string uri, bool newWindow) => _ = ResolveLinkAsync(uri, newWindow);

    // PartSchemeHandler.start: the part the URL names, if it is a picture and
    // the view still shows the document that asked for it.
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
        }
    }

    // The link the page's focus is on after a cancelled activation, if it
    // explains the navigation (LinkProbe); handed on when it is one the
    // application opens (macOS linkActivated).
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
        LinkActivated?.Invoke(this, link);
    }

    // The link under the pointer; "" hides the label. Plain text, capped:
    // it is content of the mail.
    private void Hover(string text)
    {
        var capped = HoverLabel.Cap(text);
        if (!string.Equals(capped, HoveredLink, StringComparison.Ordinal))
        {
            HoveredLink = capped;
            HoveredLinkChanged?.Invoke(this, EventArgs.Empty);
        }
        ShowStatus(capped);
    }

    private void ShowStatus(string text)
    {
        if (text.Length == 0 && HoveredLink.Length > 0)
        {
            HoveredLink = "";
            HoveredLinkChanged?.Invoke(this, EventArgs.Empty);
        }
        status.Text = HoverLabel.Display(text);
        statusBox.Visibility = text.Length == 0 ? Visibility.Collapsed : Visibility.Visible;
    }

    private static void SetAccessibleName(WebView2 web) => AutomationProperties.SetName(web, L10n.T("Message"));
}
