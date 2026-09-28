// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The network canary of docs/windows-port.md §12, the automated form of the
// WebView2 spike (SPIKES.md §2c, the lockdown column): with the app's
// environment, profile settings and request gate, the hostile document and
// the raw corpus make no connection, no DNS lookup, no navigation, no window
// and no download, in the viewer, the editor and the previewer, and no URL
// request passes the gate (so the inner layers are checked apart from the
// resolver rule that hides the rest). The control run shows that the same
// document in an unprotected WebView2 does reach the canaries and that the
// NetLog checks see connections, URL requests and names, so a silent
// canary means something. The recovery run shows that a renderer that dies
// or hangs under a document gets it shown again once, never in a loop.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.App.Canary.Host;
using Xunit;

namespace Malachi.App.Canary;

public sealed class NetworkCanaryTests(CanaryFixture fixture) : IClassFixture<CanaryFixture>
{
    private static readonly string[] Views = ["viewer", "editor", "preview"];

    // Vectors an unprotected WebView2 reached in every run of the spike
    // (SPIKES.md §2c, column base) and of this canary; the preconnect (a
    // bare TCP connection) and the prerender (a full GET past the CSP and
    // the filter) are why the environment has its resolver rule.
    private static readonly string[] ControlLeaks =
        ["img", "css-bg-inline", "link-stylesheet", "link-preconnect", "link-prerender", "iframe", "nav", "form", "refresh"];

    // The hosts of WebView2's own background requests, which the network
    // stack starts whatever the page does (and the resolver rule stops):
    // runtime 153 fetches its configuration from config.edge.skype.com. A
    // runtime that adds one fails NothingPassedTheGate until it is named
    // here after a look at what it is.
    private static readonly string[] WebView2BackgroundHosts = ["config.edge.skype.com"];

    [Fact]
    public void TheViewsRanEveryStep()
    {
        var results = ProtectedResults();
        Assert.True(fixture.Protected!.ExitCode == 0, "host exit code " + fixture.Protected.ExitCode + Errors(results));
        Assert.True(results.Completed, "the run did not complete" + Errors(results));
        foreach (var view in Views)
        {
            Assert.Contains(results.Events, e => e.Kind == HostEvent.Kinds.Ready && e.View == view);
        }
        Assert.DoesNotContain(results.Events, e => e.Kind is HostEvent.Kinds.Unavailable or HostEvent.Kinds.ProcessFailed or HostEvent.Kinds.Error);
        // No document of the hostile run failed to load, and no view had to
        // recover.
        Assert.DoesNotContain(results.Events, e => e.Kind == HostEvent.Kinds.Log && e.Detail is { } d
            && (d.Contains("did not load", StringComparison.Ordinal) || d.Contains("recovery", StringComparison.Ordinal)));
        // The browser ended before the host, so its NetLog is whole.
        Assert.True(results.BrowserExited, "the browser process did not exit");
    }

    [Fact]
    public void NoCanaryWasReached()
    {
        SkipIfNeeded();
        Assert.Empty(fixture.Protected!.Reached());
    }

    // The reader fails when the runtime's NetLog lacks an event it relies on
    // (NetLog.RequiredEventTypes): a renamed event must not make every check
    // below pass by never matching.
    [Fact]
    public void TheNetLogsHaveEveryEventTheCanaryReads()
    {
        SkipIfNeeded();
        foreach (var run in new[] { fixture.Protected!, fixture.Control! })
        {
            Assert.True(run.NetLog is not null, run.Name + ": no NetLog" + (run.NetLogError is { } e ? " (" + e + ")" : ""));
            Assert.True(run.NetLog!.EventCount > 0, run.Name + ": the NetLog is empty");
        }
    }

    [Fact]
    public void NothingWasLookedUp()
    {
        var log = ProtectedLog();
        Assert.Empty(log.Lookups);
        // The resolver was asked (WebView2's own background request: the
        // reader sees resolver requests), and every name was mapped to
        // nothing by the environment's rule before any lookup.
        Assert.NotEmpty(log.RequestedHosts);
        Assert.All(log.RequestedHosts, host => Assert.Contains("~notfound", host, StringComparison.OrdinalIgnoreCase));
        // A mapped request is logged as ~notfound, so the DNS canaries'
        // names are looked for in the whole log: nothing past the page ever
        // had them (the control run's log does, TheControlRunLeaks).
        foreach (var host in HostileDocuments.DnsHosts(fixture.RunId))
        {
            Assert.False(log.Mentions(host), "the network stack saw " + host);
        }
    }

    [Fact]
    public void NothingConnected()
    {
        var log = ProtectedLog();
        Assert.Empty(log.TcpConnects);
        // Chromium's IPv6 reachability probe connects a UDP socket, which
        // sends nothing; it must fail, as every UDP connect.
        Assert.All(log.UdpConnects, udp => Assert.False(udp.EndsWith(" ok", StringComparison.Ordinal), "UDP connected: " + udp));
    }

    // The resolver rule hides whatever reaches the network stack, so the
    // inner layers are checked on their own: no URL request was started but
    // WebView2's own background ones. Every request of the pages, a cancelled
    // navigation's or a prerender's included, was answered by the gate (or
    // blocked by the CSP) before it became one, and SmartScreen
    // (IsReputationCheckingRequired=false) sent nothing about the clicked
    // links. What only the resolver rule stops, by design (§6.1), is the
    // speculative preconnect of a navigation, which starts no URL request
    // and fails before any socket (NothingConnected).
    [Fact]
    public void NothingPassedTheGate()
    {
        var log = ProtectedLog();
        Assert.NotEmpty(log.UrlRequests);
        Assert.All(log.UrlRequests, url => Assert.True(
            Uri.TryCreate(url, UriKind.Absolute, out var u) && u.Scheme == Uri.UriSchemeHttps
                && WebView2BackgroundHosts.Contains(u.Host, StringComparer.OrdinalIgnoreCase),
            "a URL request past the gate: " + url));
    }

    [Fact]
    public void NothingNavigated()
    {
        var results = ProtectedResults();
        foreach (var e in results.Events.Where(e => e.Kind == HostEvent.Kinds.Navigation && e.Stopped == false))
        {
            Assert.StartsWith("malachi-doc://" + e.View + "/", e.Uri, StringComparison.Ordinal);
        }
        foreach (var e in results.Events.Where(e => e.Kind is HostEvent.Kinds.Completed or HostEvent.Kinds.Source))
        {
            Assert.True(e.Uri is null || e.Uri.StartsWith("malachi-doc://" + e.View + "/", StringComparison.Ordinal) || e.Uri == "about:blank",
                e.Kind + " in " + e.View + " at " + e.Uri);
        }
        Assert.All(results.Events.Where(e => e.Kind is HostEvent.Kinds.Frame or HostEvent.Kinds.ExternalScheme),
            e => Assert.True(e.Stopped == true || IsPreviewContent(e), e.Kind + " not cancelled: " + e.Uri));
        // The previewer's PDF is the one frame that loads, in its own page.
        Assert.Contains(results.Events, e => e.Kind == HostEvent.Kinds.Frame && e.Stopped == false && IsPreviewContent(e));
        // The activations happened (the canary is not silent for want of
        // them): each was cancelled.
        Assert.Contains(results.Events, e => e.Kind == HostEvent.Kinds.Navigation && e.Stopped == true && e.Phase == "viewer-nav");
        // Chromium turns a web page's file: link into about:blank#blocked
        // (the control run's too, so no run observes the UNC vectors); the
        // view cancels even that.
        Assert.Contains(results.Events, e => e.Kind == HostEvent.Kinds.Navigation && e.Stopped == true && e.Phase == "viewer-unc"
            && (e.Uri!.StartsWith("file:", StringComparison.OrdinalIgnoreCase) || e.Uri == "about:blank#blocked"));
    }

    // What NavigationPolicy relies on (SPIKES.md §2h): WebView2 calls a
    // click user-initiated and a meta refresh not, so a refresh is never
    // probed as a link.
    [Fact]
    public void OnlyTheUserInitiatesLinks()
    {
        var results = ProtectedResults();
        var refresh = fixture.Protected!.Canary("refresh").Origin + "/refresh";
        var nav = fixture.Protected.Canary("nav").Origin + "/nav";
        Assert.Contains(results.Events, e => e.Kind == HostEvent.Kinds.Navigation && e.View == "viewer" && e.Uri == nav
            && e.Stopped == true && e.Detail == HostEvent.UserInitiated);
        Assert.Contains(results.Events, e => e.Kind == HostEvent.Kinds.Navigation && e.View == "viewer" && e.Uri == refresh
            && e.Stopped == true && e.Detail == HostEvent.NotUserInitiated);
    }

    [Fact]
    public void NoWindowOpened()
    {
        var results = ProtectedResults();
        Assert.All(results.Events.Where(e => e.Kind == HostEvent.Kinds.NewWindow), e => Assert.True(e.Stopped, "new window: " + e.Uri));
        Assert.Contains(results.Events, e => e.Kind == HostEvent.Kinds.NewWindow && e.Phase == "viewer-blank");
        Assert.DoesNotContain(results.Events, e => e.Kind == HostEvent.Kinds.Window);
    }

    [Fact]
    public void WindowTitlesAreFixed()
    {
        // The windows WebView2 draws the views in carry the document's
        // title, which other processes can read (docs/windows-port.md §6.2):
        // always the views' fixed one, never a message's.
        var results = ProtectedResults();
        var titles = results.Events.Where(e => e.Kind == HostEvent.Kinds.Title).Select(e => (e.Phase, Title: e.Detail!.Split(" | ")[1])).ToList();
        Assert.NotEmpty(titles);
        // Taken while the previewer shows the PDF (its metadata has a title
        // of its own), the picture, and at the end.
        Assert.Contains(titles, t => t.Phase == "preview-pdf");
        Assert.Contains(titles, t => t.Phase == "preview-png");
        Assert.All(titles, t => Assert.True(t.Title.Length == 0 || t.Title.StartsWith("Malachi Mail", StringComparison.Ordinal),
            "window title " + t.Title + " in " + t.Phase));
        Assert.DoesNotContain(titles, t => t.Title.Contains(HostileDocuments.PdfTitle, StringComparison.Ordinal));
    }

    [Fact]
    public void NothingWasDownloaded()
    {
        var results = ProtectedResults();
        Assert.All(results.Events.Where(e => e.Kind == HostEvent.Kinds.Download), e => Assert.True(e.Stopped, "download: " + e.Uri));
        Assert.DoesNotContain(results.Events, e => e.Kind == HostEvent.Kinds.DownloadedFile);
    }

    [Fact]
    public void TheGateRefusedEverythingNotTheViewsOwn()
    {
        var results = ProtectedResults();
        var served = 0;
        foreach (var e in results.Events.Where(e => e.Kind == HostEvent.Kinds.Request && e.Uri is { } u && !u.StartsWith("shown:", StringComparison.Ordinal)))
        {
            var uri = e.Uri!;
            if (uri.StartsWith("malachi-doc://" + e.View + "/", StringComparison.Ordinal))
            {
                served += e.Detail == "200" ? 1 : 0;
                Assert.True(e.Detail is "200" or "403", "document answered " + e.Detail);
            }
            else if ((e.View == "viewer" && uri.StartsWith("malachi-cid:", StringComparison.Ordinal))
                || (e.View == "editor" && uri.StartsWith("cid:", StringComparison.Ordinal)))
            {
                // Served (at once when the stand-in fetcher answers
                // synchronously), refused, or still being fetched.
                Assert.True(e.Detail is "200" or "deferred" or "404", "picture answered " + e.Detail);
            }
            else
            {
                Assert.True(e.Detail == "403", e.View + " answered " + e.Detail + " for " + uri);
            }
        }
        Assert.True(served > 0, "no document was served");
    }

    [Fact]
    public void ActivatedLinksReachTheReaderAndNothingElse()
    {
        var results = ProtectedResults();
        var links = results.Events.Where(e => e.Kind == HostEvent.Kinds.Link).ToList();
        var nav = fixture.Protected!.Canary("nav").Origin + "/nav";
        // The clicked link, with its attribute as written.
        Assert.Contains(links, e => e.Phase == "viewer-nav" && e.Uri == nav && e.Detail == nav);
        Assert.Contains(links, e => e.Phase == "viewer-mailto" && e.Uri!.StartsWith("mailto:", StringComparison.Ordinal));
        Assert.Contains(links, e => e.Phase == "viewer-middle" && e.Uri == fixture.Protected.Canary("middle").Origin + "/middle");
        // A form submit and a refresh are not links.
        Assert.DoesNotContain(links, e => e.Phase is "viewer-form" or "viewer-refresh");
        // Nothing but the viewer hands links on.
        Assert.All(links, e => Assert.Equal("viewer", e.View));
        Assert.Contains(results.Events, e => e.Kind == HostEvent.Kinds.Hover && e.Phase == "viewer-hover"
            && e.Detail is { Length: > 0 } d && d.Contains("/hover", StringComparison.Ordinal));
    }

    // The bridge runs under the editor's CSP (script on, page script
    // blocked): it reports ready, formats, and a flush returns the typed
    // content (docs/windows-port.md §6.5).
    [Fact]
    public void TheEditorBridgeWorks()
    {
        var results = ProtectedResults();
        Assert.Contains(results.Events, e => e.Kind == HostEvent.Kinds.Bridge && e.Phase == "editor-bridge" && e.Detail == "ready");
        var flushed = Assert.Single(results.Events, e => e.Kind == HostEvent.Kinds.Flushed);
        Assert.Contains("hello", flushed.Detail, StringComparison.Ordinal);
        Assert.Contains("<b>world</b>", flushed.Detail, StringComparison.Ordinal);
        Assert.DoesNotContain(results.Events, e => e.Kind == HostEvent.Kinds.Bridge && e.Detail == "crashed");
    }

    // A file dropped on the page goes to the window, as a path; the page
    // does not navigate to it.
    [Fact]
    public void DroppedFilesReachTheWindow()
    {
        var results = ProtectedResults();
        var dropped = Assert.Single(results.Events, e => e.Kind == HostEvent.Kinds.Dropped);
        Assert.Equal(fixture.DroppedFile, dropped.Detail);
        Assert.DoesNotContain(results.Events, e => e.Kind == HostEvent.Kinds.Navigation && e.Uri!.StartsWith("file:", StringComparison.OrdinalIgnoreCase)
            && e.Stopped == false);
    }

    // The text zoom applies to the message on display (CSS zoom).
    [Fact]
    public void TheViewerZooms()
    {
        var results = ProtectedResults();
        var probe = Assert.Single(results.Events, e => e.Kind == HostEvent.Kinds.Probe && e.Phase == "viewer-zoom");
        Assert.Equal("\"1.5\"", probe.Detail);
    }

    [Fact]
    public void TheControlRunLeaks()
    {
        SkipIfNeeded();
        var control = fixture.Control!;
        Assert.True(control.Results?.Completed == true, "the control run did not complete (exit " + control.ExitCode + ")"
            + Errors(control.Results));
        var reached = control.Canaries.Values.Where(c => c.Hits.Count > 0).Select(c => c.Vector).ToHashSet();
        Assert.All(ControlLeaks, vector => Assert.Contains(vector, reached));
        // The NetLog checks see what they look for: connections, URL
        // requests to the canaries, and the DNS canaries' names.
        Assert.True(control.NetLog is not null, "the control run wrote no NetLog" + (control.NetLogError is { } e ? " (" + e + ")" : ""));
        var log = control.NetLog!;
        Assert.NotEmpty(log.TcpConnects);
        var ports = control.Canaries.Values.Select(c => ":" + c.Port.ToString(System.Globalization.CultureInfo.InvariantCulture) + "/").ToArray();
        Assert.Contains(log.UrlRequests, url => ports.Any(p => url.Contains(p, StringComparison.Ordinal)));
        var hosts = HostileDocuments.DnsHosts(fixture.RunId);
        Assert.True(log.Mentions(hosts[0]), "the control run's NetLog does not name " + hosts[0]);
        Assert.True(log.Mentions(hosts[1]), "the control run's NetLog does not name " + hosts[1]);
    }

    // A renderer that dies under a document shows it again once; when the
    // same document kills it again the view gives up (Unavailable: the
    // reader shows the plain text, the previewer its panel, the editor stays
    // blank with its text kept), and nothing loads until the caller loads a
    // document (RendererRecovery). The editor's reload is its window's
    // (Crashed, which the host answers as the compose window does), raised
    // once.
    [Theory]
    [InlineData("viewer")]
    [InlineData("editor")]
    [InlineData("preview")]
    public void ACrashedRendererShowsTheDocumentAgainOnce(string view)
    {
        var results = RecoveryResults();
        var first = results.Events.Where(e => e.View == view && e.Phase == view + "-crash-1").ToList();
        var second = results.Events.Where(e => e.View == view && e.Phase == view + "-crash-2").ToList();
        Assert.Single(first, e => e.Kind == HostEvent.Kinds.ProcessFailed && e.Detail == "RenderProcessExited");
        Assert.Single(first, e => IsOwnDocument(e, view));
        Assert.Contains(first, e => e.Kind == HostEvent.Kinds.Completed && e.Detail!.StartsWith("True", StringComparison.Ordinal));
        Assert.DoesNotContain(first, e => e.Kind == HostEvent.Kinds.Unavailable);
        Assert.Single(second, e => e.Kind == HostEvent.Kinds.ProcessFailed && e.Detail == "RenderProcessExited");
        Assert.DoesNotContain(second, e => IsOwnDocument(e, view));
        Assert.Single(second, e => e.Kind == HostEvent.Kinds.Unavailable);
        if (view == "editor")
        {
            Assert.Single(first, e => e.Kind == HostEvent.Kinds.Bridge && e.Detail == "crashed");
            Assert.DoesNotContain(second, e => e.Kind == HostEvent.Kinds.Bridge && e.Detail == "crashed");
        }
    }

    // After giving up, the viewer shows the next document; a renderer that
    // dies while a document still loads is recovered the same way (WebView2
    // reports the process, not a failed navigation).
    [Fact]
    public void TheViewerRecoversFromACrashWhileLoading()
    {
        var results = RecoveryResults();
        Assert.Contains(results.Events, e => e.Phase == "viewer-after" && e.Kind == HostEvent.Kinds.Completed
            && e.Detail!.StartsWith("True", StringComparison.Ordinal));
        var loading = results.Events.Where(e => e.View == "viewer" && e.Phase == "viewer-loadcrash").ToList();
        Assert.Single(loading, e => e.Kind == HostEvent.Kinds.ProcessFailed);
        // The document, and once more.
        Assert.Equal(2, loading.Count(e => IsOwnDocument(e, "viewer")));
        Assert.DoesNotContain(loading, e => e.Kind == HostEvent.Kinds.Unavailable);
    }

    // A hung renderer is acted on once Chromium reported it and it has not
    // answered the view's script within RendererRecovery.AnswerTimeout: a
    // new control shows the document again, and the view goes on.
    [Fact]
    public void AHungRendererIsReplacedOnce()
    {
        var results = RecoveryResults();
        var hang = results.Events.Where(e => e.View == "viewer" && e.Phase == "viewer-hang-1").ToList();
        Assert.Contains(hang, e => e.Kind == HostEvent.Kinds.ProcessFailed && e.Detail == "RenderProcessUnresponsive");
        Assert.Contains(results.Events, e => e.Kind == HostEvent.Kinds.Log && e.Detail!.EndsWith("recovery: ReplaceAndReload", StringComparison.Ordinal));
        Assert.Single(hang, e => e.Kind == HostEvent.Kinds.Ready && e.Detail == "again");
        Assert.Single(hang, e => IsOwnDocument(e, "viewer"));
        Assert.DoesNotContain(hang, e => e.Kind == HostEvent.Kinds.Unavailable);
        Assert.Contains(results.Events, e => e.Phase == "viewer-after-hang" && e.Kind == HostEvent.Kinds.Completed
            && e.Detail!.StartsWith("True", StringComparison.Ordinal));
    }

    [Fact]
    public void TheRecoveryRunCompleted()
    {
        var results = RecoveryResults();
        Assert.True(fixture.Recovery!.ExitCode == 0, "host exit code " + fixture.Recovery.ExitCode + Errors(results));
        Assert.True(results.Completed, "the recovery run did not complete" + Errors(results));
        Assert.DoesNotContain(results.Events, e => e.Kind == HostEvent.Kinds.Error);
    }

    [Fact]
    public void TheCorpusHasItsHtmlParts()
    {
        var parts = MimeCorpus.HtmlParts(System.IO.Path.Combine(CanaryFixture.RepositoryRoot, "backend", "testdata", "mime"));
        Assert.True(parts.Count >= 15, "only " + parts.Count + " HTML parts");
        Assert.Contains(parts, p => p.File == "alternative.eml" && p.Html.Contains("český", StringComparison.Ordinal));
        Assert.Contains(parts, p => p.File == "html-scripts-events.eml");
    }

    private HostResults ProtectedResults()
    {
        SkipIfNeeded();
        return fixture.Protected!.Results ?? throw new InvalidOperationException(
            "the canary host wrote no results (exit " + fixture.Protected.ExitCode + ")");
    }

    private HostResults RecoveryResults()
    {
        SkipIfNeeded();
        return fixture.Recovery!.Results ?? throw new InvalidOperationException(
            "the recovery run wrote no results (exit " + fixture.Recovery.ExitCode + ")");
    }

    private NetLog ProtectedLog()
    {
        SkipIfNeeded();
        var log = fixture.Protected!.NetLog ?? throw new InvalidOperationException(
            "the browser wrote no NetLog" + (fixture.Protected.NetLogError is { } e ? " (" + e + ")" : ""));
        Assert.True(log.EventCount > 0, "the NetLog is empty");
        return log;
    }

    // The navigation of the view's own document, allowed.
    private static bool IsOwnDocument(HostEvent e, string view) =>
        e.Kind == HostEvent.Kinds.Navigation && e.Stopped == false
        && e.Uri is { } uri && uri.StartsWith("malachi-doc://" + view + "/", StringComparison.Ordinal);

    // The embedded resource of the previewer's own page (its PDF).
    private static bool IsPreviewContent(HostEvent e) =>
        e.View == "preview" && e.Uri is { } uri && uri.StartsWith("malachi-doc://preview/", StringComparison.Ordinal)
        && uri.EndsWith("/content", StringComparison.Ordinal);

    private void SkipIfNeeded() => Assert.SkipWhen(fixture.SkipReason is not null, fixture.SkipReason ?? "");

    private static string Errors(HostResults? results) =>
        results is null ? "" : string.Concat(results.Events.Where(e => e.Kind == HostEvent.Kinds.Error).Select(e => "; " + e.Detail));
}
