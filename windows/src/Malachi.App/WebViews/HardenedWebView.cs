// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only (docs/windows-port.md §6.1-§6.3, §6.5, §6.6): what the three
// web views share, the WebView2 counterpart of the configuration macOS builds
// per view (MessageWebView.swift and ComposeWebView.swift makeConfiguration,
// ensureRules, the navigation and UI delegates) and GTK per blueprint
// (html_view.blp, editor.blp, view.go, editor.go):
//
// - one InPrivate profile per kind (viewer, editor, preview) in the app's
//   environment (WebViewEnvironment), created with
//   EnsureCoreWebView2Async(environment, controllerOptions);
// - the settings of §6.3, applied before the first navigation; if any of
//   them cannot be applied (an old runtime) the view never loads a document
//   and says so (Unavailable): fail closed, as macOS without its content rule
//   list;
// - the request gate: a WebResourceRequested filter for every URL, context
//   and source kind added before the first navigation, and RequestGate's
//   answer for each request: the view's one current document once, its own
//   picture scheme, 403 for the rest;
// - navigation: only the pending document; everything else cancelled (the
//   gate keeps its request off the network), frames, new windows, downloads,
//   external schemes, permissions, authentication and certificates refused;
// - the context menu reduced to ContextMenuPolicy's items;
// - a crashed renderer or browser process handled per view; a dead browser
//   process means a new environment and a new control.
//
// A view is a UserControl hosting a WebView2 in a Grid (Root), built in code
// so the canary (tests/Malachi.App.Canary.Host) compiles the same files.
// UI-thread-affine, as the controllers (§7.1).

using System;
using System.Diagnostics.CodeAnalysis;
using System.IO;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Presentation;
using Microsoft.Extensions.Logging;
using Microsoft.UI;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.Web.WebView2.Core;

namespace Malachi.App.WebViews;

/// <summary>The hardened WebView2 every view of the app is built on.</summary>
[SuppressMessage("Design", "CA1001:Types that own disposable fields should be disposable",
    Justification = "A XAML control has no Dispose: Close() cancels and releases the generation's token source.")]
public abstract partial class HardenedWebView : UserControl
{
    private const CoreWebView2PdfToolbarItems HiddenPdfItems =
        CoreWebView2PdfToolbarItems.Save | CoreWebView2PdfToolbarItems.SaveAs | CoreWebView2PdfToolbarItems.Print
        | CoreWebView2PdfToolbarItems.FullScreen | CoreWebView2PdfToolbarItems.MoreSettings;

    private readonly ILogger log;
    private CoreWebView2Environment? environment;
    private CoreWebView2? core;
    private Task? initializing;
    private string? pendingUri;
    private string? pendingContentUri;
    private byte[]? documentBytes;
    private string documentHeaders = "";
    private byte[]? contentBytes;
    private string contentHeaders = "";
    private PendingDocument? waiting;
    private CancellationTokenSource generationCancel = new();
    private bool closed;

    /// <summary>A view of <paramref name="kind"/>, not yet initialised (that starts once it is loaded).</summary>
    private protected HardenedWebView(WebViewKind kind)
    {
        Kind = kind;
        Gate = new RequestGate(kind);
        log = WebViewEnvironment.LoggerFactory.CreateLogger("Malachi.App.WebViews." + kind);
        Root = new Grid();
        Web = NewWebView();
        Root.Children.Add(Web);
        Content = Root;
        IsTabStop = false;
        Loaded += (_, _) => EnsureInitialized();
    }

    /// <summary>
    /// The view has no document and will get none until it is loaded again:
    /// the environment could not be created or a protection could not be
    /// applied (macOS <c>onUnavailable</c>). The caller shows what it shows
    /// without HTML.
    /// </summary>
    public event EventHandler? Unavailable;

    /// <summary>Which view this is.</summary>
    public WebViewKind Kind { get; }

    /// <summary>Whether the view is initialised and loads documents.</summary>
    public bool IsReady => core is not null && !closed;

    /// <summary>
    /// The WebView2 inside (for focus and automation only; replaced after a
    /// browser process crash). Callers never navigate it.
    /// </summary>
    public WebView2 Web { get; private set; }

    /// <summary>The initialised core, or null (not yet, unavailable, closed).</summary>
    public CoreWebView2? CoreWebView => closed ? null : core;

    /// <summary>The grid the WebView2 sits in; views add their overlays to it.</summary>
    private protected Grid Root { get; }

    /// <summary>The view's request gate.</summary>
    private protected RequestGate Gate { get; }

    /// <summary>The environment, once initialised.</summary>
    private protected CoreWebView2Environment? Environment => environment;

    /// <summary>A token cancelled whenever the view's generation moves on (a new document, a close).</summary>
    private protected CancellationToken GenerationToken => closed ? new CancellationToken(canceled: true) : generationCancel.Token;

    /// <summary>The view's logger.</summary>
    private protected ILogger Log => log;

    /// <summary>Whether <see cref="Close"/> ran.</summary>
    private protected bool IsClosed => closed;

    /// <summary>
    /// Ends the view for good: its document and pictures are dropped, its
    /// browser resources released. A window calls it when it closes.
    /// </summary>
    public void Close()
    {
        if (closed)
        {
            return;
        }
        closed = true;
        Gate.Retire();
        generationCancel.Cancel();
        generationCancel.Dispose();
        waiting = null;
        documentBytes = null;
        contentBytes = null;
        try
        {
            Web.Close();
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            WebViewLog.CloseFailed(log, e);
        }
    }

    /// <summary>Puts the keyboard focus into the page.</summary>
    public void FocusPage() => Web.Focus(FocusState.Programmatic);

    /// <summary>
    /// Shows <paramref name="bytes"/> as the view's next document, served as
    /// <paramref name="mediaType"/> with <paramref name="csp"/>: at once when
    /// the view is ready, after its initialisation otherwise.
    /// </summary>
    private protected void LoadDocument(byte[] bytes, string mediaType, string csp)
    {
        ArgumentNullException.ThrowIfNull(bytes);
        Load(new PendingDocument(_ => bytes, mediaType, _ => csp, null, null));
    }

    /// <summary>
    /// Shows a page that embeds <paramref name="content"/> (served once, as
    /// <paramref name="contentType"/>, at the URL <paramref name="page"/> and
    /// <paramref name="csp"/> are given) as the view's next document.
    /// </summary>
    private protected void LoadDocument(Func<string, byte[]> page, string mediaType, Func<string, string> csp, byte[] content, string contentType)
    {
        ArgumentNullException.ThrowIfNull(page);
        ArgumentNullException.ThrowIfNull(csp);
        ArgumentNullException.ThrowIfNull(content);
        Load(new PendingDocument(uri => page(uri!), mediaType, uri => csp(uri!), content, contentType));
    }

    private void Load(PendingDocument document)
    {
        if (closed)
        {
            return;
        }
        if (core is null)
        {
            waiting = document;
            EnsureInitialized();
            return;
        }
        NextGeneration();
        var uri = Gate.NextDocument(withContent: document.Content is not null);
        documentBytes = document.Build(Gate.ContentUri);
        var csp = document.Csp(Gate.ContentUri);
        documentHeaders = ResponseHeaders.Document(document.MediaType, csp);
        contentBytes = document.Content;
        contentHeaders = document.Content is null ? "" : ResponseHeaders.Document(document.ContentType!, csp);
        pendingUri = uri;
        pendingContentUri = Gate.ContentUri;
        try
        {
            core.Navigate(uri);
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            WebViewLog.NavigateFailed(log, e);
            pendingUri = null;
            pendingContentUri = null;
            documentBytes = null;
            contentBytes = null;
        }
    }

    /// <summary>Drops the document without a new one (a document the caller withdrew).</summary>
    private protected void DropDocument()
    {
        waiting = null;
        NextGeneration();
        Gate.Retire();
        documentBytes = null;
        contentBytes = null;
        pendingUri = null;
        pendingContentUri = null;
    }

    /// <summary>Runs a host script, ignoring its result and its failure.</summary>
    private protected void Run(string script) => _ = EvaluateAsync(script);

    /// <summary>Runs a host script; its JSON result, or null when it failed (or there is no page).</summary>
    private protected async Task<string?> EvaluateAsync(string script)
    {
        if (CoreWebView is not { } c)
        {
            return null;
        }
        try
        {
            return await c.ExecuteScriptAsync(script);
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            WebViewLog.ScriptFailed(log, e);
            return null;
        }
    }

    /// <summary>
    /// The kind's settings and handlers beyond the common ones, applied
    /// before the first navigation (the editor's bridge, the viewer's hover).
    /// Throwing fails the view closed.
    /// </summary>
    private protected virtual Task ConfigureAsync(CoreWebView2 webView) => Task.CompletedTask;

    /// <summary>The view is initialised; a document handed over meanwhile is being loaded.</summary>
    private protected virtual void OnReady()
    {
    }

    /// <summary>The view cannot show documents (after <see cref="Unavailable"/> was raised).</summary>
    private protected virtual void OnUnavailable()
    {
    }

    /// <summary>
    /// A cancelled navigation or new-window request the policy says may be a
    /// link (<see cref="NavigationAction.CancelAndProbe"/>; the viewer only).
    /// </summary>
    private protected virtual void OnLinkCandidate(string uri, bool newWindow)
    {
    }

    /// <summary>Serves a picture of the view's own scheme; the default answers 404.</summary>
    private protected virtual Task ServePictureAsync(GateDecision decision, CoreWebView2WebResourceRequestedEventArgs args)
    {
        Respond(args, GateDecision.Refused.NotFound);
        return Task.CompletedTask;
    }

    /// <summary>The page's renderer died or hung; the view has a fresh one (or a new control).</summary>
    private protected abstract void OnRendererLost();

    /// <summary>Answers <paramref name="args"/> with an error response.</summary>
    private protected void Respond(CoreWebView2WebResourceRequestedEventArgs args, GateDecision.Refused refused)
    {
        if (environment is { } env)
        {
            args.Response = env.CreateWebResourceResponse(null, refused.Status, refused.ReasonPhrase, ResponseHeaders.Refused);
        }
    }

    /// <summary>Answers <paramref name="args"/> with <paramref name="bytes"/> and <paramref name="headers"/>.</summary>
    private protected void Respond(CoreWebView2WebResourceRequestedEventArgs args, byte[] bytes, string headers)
    {
        if (environment is { } env)
        {
            args.Response = env.CreateWebResourceResponse(new MemoryStream(bytes, writable: false).AsRandomAccessStream(), 200, "OK", headers);
        }
    }

    private WebView2 NewWebView()
    {
        var web = new WebView2
        {
            // WinUI 3 has no transparent WebView2; the pages' canvas is white.
            DefaultBackgroundColor = Colors.White,
            HorizontalAlignment = HorizontalAlignment.Stretch,
            VerticalAlignment = VerticalAlignment.Stretch,
        };
        web.CoreProcessFailed += (_, args) => ProcessFailed(args.ProcessFailedKind);
        return web;
    }

    private void NextGeneration()
    {
        if (closed)
        {
            return;
        }
        generationCancel.Cancel();
        generationCancel.Dispose();
        generationCancel = new CancellationTokenSource();
    }

    private void EnsureInitialized()
    {
        if (closed || core is not null || initializing is { IsCompleted: false })
        {
            return;
        }
        if (XamlRoot is null)
        {
            // Not in a window yet: Loaded starts it.
            return;
        }
        initializing = InitializeAsync();
    }

    private async Task InitializeAsync()
    {
        var web = Web;
        try
        {
            var env = await WebViewEnvironment.GetAsync();
            if (env is null || closed || !ReferenceEquals(web, Web))
            {
                if (env is null)
                {
                    Fail(null);
                }
                return;
            }
            var options = env.CreateCoreWebView2ControllerOptions();
            options.IsInPrivateModeEnabled = true;
            options.ProfileName = Kind.ProfileName;
            await web.EnsureCoreWebView2Async(env, options);
            if (closed || !ReferenceEquals(web, Web))
            {
                return;
            }
            var c = web.CoreWebView2;
            Harden(c);
            await ConfigureAsync(c);
            if (closed || !ReferenceEquals(web, Web))
            {
                return;
            }
            environment = env;
            core = c;
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            Fail(e);
            return;
        }
        OnReady();
        if (waiting is { } w)
        {
            waiting = null;
            Load(w);
        }
    }

    // The failure is not remembered: the next Load of a document tries again
    // (the environment too).
    private void Fail(Exception? error)
    {
        WebViewLog.Unavailable(log, error);
        waiting = null;
        core = null;
        ReplaceWebView();
        OnUnavailable();
        Unavailable?.Invoke(this, EventArgs.Empty);
    }

    // The settings and handlers every view has (docs/windows-port.md §6.3).
    private void Harden(CoreWebView2 c)
    {
        if (!c.Profile.IsInPrivateModeEnabled)
        {
            throw new InvalidOperationException("the web view's profile is not InPrivate");
        }
        var s = c.Settings;
        s.IsScriptEnabled = Kind == WebViewKind.Editor;
        s.IsWebMessageEnabled = Kind == WebViewKind.Editor;
        s.AreHostObjectsAllowed = false;
        s.AreDefaultScriptDialogsEnabled = false;
        s.AreDevToolsEnabled = false;
        s.IsStatusBarEnabled = false;
        s.IsReputationCheckingRequired = false;
        s.IsGeneralAutofillEnabled = false;
        s.IsPasswordAutosaveEnabled = false;
        s.IsPinchZoomEnabled = false;
        s.IsSwipeNavigationEnabled = false;
        s.IsZoomControlEnabled = false;
        s.IsBuiltInErrorPageEnabled = false;
        s.AreBrowserAcceleratorKeysEnabled = false;
        s.AreDefaultContextMenusEnabled = true;
        s.IsNonClientRegionSupportEnabled = false;
        s.HiddenPdfToolbarItems = HiddenPdfItems;
        c.Profile.PreferredColorScheme = CoreWebView2PreferredColorScheme.Light;
        c.Profile.IsGeneralAutofillEnabled = false;
        c.Profile.IsPasswordAutosaveEnabled = false;

        c.AddWebResourceRequestedFilter("*", CoreWebView2WebResourceContext.All, CoreWebView2WebResourceRequestSourceKinds.All);
        c.WebResourceRequested += OnWebResourceRequested;
        c.NavigationStarting += OnNavigationStarting;
        c.FrameNavigationStarting += OnFrameNavigationStarting;
        c.NewWindowRequested += OnNewWindowRequested;
        c.DownloadStarting += (_, e) =>
        {
            e.Cancel = true;
            e.Handled = true;
        };
        c.LaunchingExternalUriScheme += (_, e) => e.Cancel = true;
        c.PermissionRequested += (_, e) =>
        {
            e.State = CoreWebView2PermissionState.Deny;
            e.Handled = true;
        };
        c.BasicAuthenticationRequested += (_, e) => e.Cancel = true;
        c.ClientCertificateRequested += (_, e) =>
        {
            e.Cancel = true;
            e.Handled = true;
        };
        c.ServerCertificateErrorDetected += (_, e) => e.Action = CoreWebView2ServerCertificateErrorAction.Cancel;
        c.ScreenCaptureStarting += (_, e) =>
        {
            e.Cancel = true;
            e.Handled = true;
        };
        c.SaveAsUIShowing += (_, e) => e.Cancel = true;
        c.ContextMenuRequested += OnContextMenuRequested;
        // The window WebView2 draws in carries the document's title; a PDF
        // names its own, a picture its URL.
        c.DocumentTitleChanged += (sender, _) =>
        {
            if (!FixedTitle.IsFixed(sender.DocumentTitle))
            {
                Run(FixedTitle.Script);
            }
        };
    }

    private void OnWebResourceRequested(CoreWebView2 sender, CoreWebView2WebResourceRequestedEventArgs args)
    {
        GateDecision decision;
        try
        {
            decision = Gate.Decide(args.Request.Uri);
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            WebViewLog.GateFailed(log, e);
            Respond(args, GateDecision.Refused.Forbidden);
            return;
        }
        switch (decision)
        {
            case GateDecision.Document when documentBytes is { } bytes:
                // Served once: the bytes are forgotten with it.
                documentBytes = null;
                Respond(args, bytes, documentHeaders);
                break;
            case GateDecision.Content when contentBytes is { } content:
                contentBytes = null;
                Respond(args, content, contentHeaders);
                break;
            case GateDecision.Part or GateDecision.InlineImage:
                _ = ServePictureAsync(decision, args);
                break;
            case GateDecision.Refused refused:
                WebViewLog.Refused(log, refused.Status);
                Respond(args, refused);
                break;
            default:
                Respond(args, GateDecision.Refused.Forbidden);
                break;
        }
    }

    private void OnNavigationStarting(CoreWebView2 sender, CoreWebView2NavigationStartingEventArgs args)
    {
        var uri = args.Uri;
        var action = NavigationPolicy.Starting(Kind, uri, pendingUri, args.IsRedirected);
        if (action == NavigationAction.Allow)
        {
            // Once: a second navigation to the same URL is not ours.
            pendingUri = null;
            return;
        }
        args.Cancel = true;
        WebViewLog.NavigationRefused(log);
        if (action == NavigationAction.CancelAndProbe)
        {
            OnLinkCandidate(uri, newWindow: false);
        }
    }

    private void OnFrameNavigationStarting(CoreWebView2 sender, CoreWebView2NavigationStartingEventArgs args)
    {
        if (NavigationPolicy.Frame(args.Uri, pendingContentUri) == NavigationAction.Allow && !args.IsRedirected)
        {
            // Once, as the document.
            pendingContentUri = null;
            return;
        }
        args.Cancel = true;
        WebViewLog.NavigationRefused(log);
    }

    private void OnNewWindowRequested(CoreWebView2 sender, CoreWebView2NewWindowRequestedEventArgs args)
    {
        args.Handled = true;
        var uri = args.Uri;
        if (NavigationPolicy.NewWindow(Kind, uri, args.IsUserInitiated) == NavigationAction.CancelAndProbe)
        {
            OnLinkCandidate(uri, newWindow: true);
        }
        else
        {
            WebViewLog.NavigationRefused(log);
        }
    }

    // Reading the target or an item can throw (measured: SourceUri with
    // 0x8000000E), so everything is in the try, and a menu that could not be
    // reduced is not shown at all.
    private void OnContextMenuRequested(CoreWebView2 sender, CoreWebView2ContextMenuRequestedEventArgs args)
    {
        var shown = false;
        try
        {
            var items = args.MenuItems;
            var names = new string?[items.Count];
            var separators = new bool[items.Count];
            for (var i = 0; i < items.Count; i++)
            {
                separators[i] = items[i].Kind == CoreWebView2ContextMenuItemKind.Separator;
                names[i] = items[i].Name;
            }
            var keep = ContextMenuPolicy.Keep(Kind, names, separators);
            for (var i = items.Count - 1; i >= 0; i--)
            {
                if (!keep[i])
                {
                    items.RemoveAt(i);
                }
            }
            shown = items.Count > 0;
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            WebViewLog.ContextMenuFailed(log, e);
        }
        finally
        {
            args.Handled = !shown;
        }
    }

    private void ProcessFailed(CoreWebView2ProcessFailedKind kind)
    {
        if (closed)
        {
            return;
        }
        WebViewLog.ProcessFailed(log, kind.ToString());
        switch (kind)
        {
            case CoreWebView2ProcessFailedKind.BrowserProcessExited:
                if (environment is { } env)
                {
                    WebViewEnvironment.Invalidate(env);
                }
                Recreate();
                break;
            case CoreWebView2ProcessFailedKind.RenderProcessUnresponsive:
                Recreate();
                break;
            case CoreWebView2ProcessFailedKind.RenderProcessExited:
                DropDocument();
                OnRendererLost();
                break;
            default:
                break;
        }
    }

    // A new control in place of one whose browser (or hung renderer) is
    // gone; the view reloads what it showed once it is initialised.
    private void Recreate()
    {
        DropDocument();
        ReplaceWebView();
        OnRendererLost();
        EnsureInitialized();
    }

    private void ReplaceWebView()
    {
        var old = Web;
        core = null;
        environment = null;
        initializing = null;
        try
        {
            old.Close();
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            WebViewLog.CloseFailed(log, e);
        }
        var index = Root.Children.IndexOf(old);
        Web = NewWebView();
        if (index >= 0)
        {
            Root.Children[index] = Web;
        }
        else
        {
            Root.Children.Insert(0, Web);
        }
        OnWebViewReplaced(Web);
    }

    // A document to load: its bytes (built once its embedded resource's URL
    // is known), its type and CSP, and that resource when it has one.
    private sealed record PendingDocument(
        Func<string?, byte[]> Build, string MediaType, Func<string?, string> Csp, byte[]? Content, string? ContentType);

    /// <summary>A new WebView2 took the old one's place (automation name, visibility).</summary>
    private protected virtual void OnWebViewReplaced(WebView2 web)
    {
    }
}
