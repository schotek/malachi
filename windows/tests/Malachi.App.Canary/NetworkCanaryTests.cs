// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The network canary of docs/windows-port.md §12, the automated form of the
// WebView2 spike (SPIKES.md §2c, the lockdown column): with the app's
// environment, profile settings and request gate, the hostile document and
// the raw corpus make no connection, no DNS lookup, no navigation, no window
// and no download, in the viewer, the editor and the previewer. The control
// run shows that the same document in an unprotected WebView2 does reach the
// canaries, so a silent canary means something.

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
    // (SPIKES.md §2c, column base) and of this canary.
    private static readonly string[] ControlLeaks = ["img", "css-bg-inline", "link-stylesheet", "iframe", "nav", "form", "refresh"];

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
        // The browser ended before the host, so its NetLog is whole.
        Assert.True(results.BrowserExited, "the browser process did not exit");
    }

    [Fact]
    public void NoCanaryWasReached()
    {
        SkipIfNeeded();
        Assert.Empty(fixture.Protected!.Reached());
    }

    [Fact]
    public void NothingWasLookedUp()
    {
        var log = ProtectedLog();
        Assert.Empty(log.Lookups);
        // Every name the resolver was asked for was mapped to nothing by the
        // environment's rule before any lookup.
        Assert.All(log.RequestedHosts, host => Assert.Contains("~notfound", host, StringComparison.OrdinalIgnoreCase));
        foreach (var host in HostileDocuments.DnsHosts(fixture.RunId))
        {
            Assert.DoesNotContain(log.RequestedHosts, h => h.Contains(host, StringComparison.OrdinalIgnoreCase));
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
        var ports = fixture.Protected!.Canaries.Values.Select(c => ":" + c.Port.ToString(System.Globalization.CultureInfo.InvariantCulture) + "/").ToArray();
        Assert.DoesNotContain(log.UrlRequests, url => ports.Any(p => url.Contains(p, StringComparison.Ordinal)));
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
        Assert.NotNull(control.NetLog);
        Assert.NotEmpty(control.NetLog!.TcpConnects);
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

    private NetLog ProtectedLog()
    {
        SkipIfNeeded();
        var log = fixture.Protected!.NetLog ?? throw new InvalidOperationException("the browser wrote no NetLog");
        Assert.True(log.EventCount > 0, "the NetLog is empty");
        return log;
    }

    // The embedded resource of the previewer's own page (its PDF).
    private static bool IsPreviewContent(HostEvent e) =>
        e.View == "preview" && e.Uri is { } uri && uri.StartsWith("malachi-doc://preview/", StringComparison.Ordinal)
        && uri.EndsWith("/content", StringComparison.Ordinal);

    private void SkipIfNeeded() => Assert.SkipWhen(fixture.SkipReason is not null, fixture.SkipReason ?? "");

    private static string Errors(HostResults? results) =>
        results is null ? "" : string.Concat(results.Events.Where(e => e.Kind == HostEvent.Kinds.Error).Select(e => "; " + e.Detail));
}
