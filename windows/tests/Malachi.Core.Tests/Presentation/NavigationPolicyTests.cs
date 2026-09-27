// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The tests of NavigationPolicy, the WebView2 form of the navigation
// policies of MessageWebView.swift / ComposeWebView.swift (decidePolicyFor)
// and htmlview/view.go / editor/editor.go (decidePolicy): only the view's
// own document loads; the viewer hands link activations on, new windows
// included (GTK treats NewWindowAction like a click); the editor and the
// previewer follow nothing.

using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class NavigationPolicyTests
{
    private const string Pending = "malachi-doc://viewer/3-00ff";

    [Theory]
    [InlineData(WebViewKind.Viewer)]
    [InlineData(WebViewKind.Editor)]
    [InlineData(WebViewKind.Preview)]
    public void OnlyThePendingDocumentLoads(WebViewKind kind)
    {
        Assert.Equal(NavigationAction.Allow, NavigationPolicy.Starting(kind, Pending, Pending, isRedirected: false));
        // Not as the target of a redirect, not without a pending document,
        // not another one.
        Assert.NotEqual(NavigationAction.Allow, NavigationPolicy.Starting(kind, Pending, Pending, isRedirected: true));
        Assert.NotEqual(NavigationAction.Allow, NavigationPolicy.Starting(kind, Pending, null, isRedirected: false));
        Assert.NotEqual(NavigationAction.Allow, NavigationPolicy.Starting(kind, "malachi-doc://viewer/2-00ff", Pending, isRedirected: false));
        Assert.NotEqual(NavigationAction.Allow, NavigationPolicy.Starting(kind, "MALACHI-DOC://viewer/3-00ff", Pending, isRedirected: false));
    }

    // A navigation the viewer could have taken from a link: http, https,
    // mailto (htmlview.AllowedLink); anything else is refused outright.
    [Theory]
    [InlineData("http://example.org/", NavigationAction.CancelAndProbe)]
    [InlineData("HTTPS://example.org/x?y#z", NavigationAction.CancelAndProbe)]
    [InlineData("mailto:a@example.org", NavigationAction.CancelAndProbe)]
    [InlineData("javascript:alert(1)", NavigationAction.Cancel)]
    [InlineData("file:///c:/windows/win.ini", NavigationAction.Cancel)]
    [InlineData("about:blank", NavigationAction.Cancel)]
    [InlineData("data:text/html,x", NavigationAction.Cancel)]
    [InlineData("malachi-doc://viewer/2-00ff", NavigationAction.Cancel)]
    [InlineData("ms-settings:", NavigationAction.Cancel)]
    public void TheViewerProbesPossibleLinks(string uri, NavigationAction expected) =>
        Assert.Equal(expected, NavigationPolicy.Starting(WebViewKind.Viewer, uri, Pending, isRedirected: false));

    // The editor and the previewer follow nothing (editor.go decidePolicy;
    // the previewer's PDF links too).
    [Theory]
    [InlineData(WebViewKind.Editor)]
    [InlineData(WebViewKind.Preview)]
    public void TheOthersCancelEverything(WebViewKind kind)
    {
        Assert.Equal(NavigationAction.Cancel, NavigationPolicy.Starting(kind, "https://example.org/", Pending, isRedirected: false));
        Assert.Equal(NavigationAction.Cancel, NavigationPolicy.Starting(kind, "mailto:a@example.org", Pending, isRedirected: false));
        Assert.Equal(NavigationAction.Cancel, NavigationPolicy.NewWindow(kind, "https://example.org/", isUserInitiated: true));
    }

    // No frame navigates but the one the view's own page embeds.
    [Fact]
    public void Frames()
    {
        const string content = "malachi-doc://preview/3-00ff/content";
        Assert.Equal(NavigationAction.Allow, NavigationPolicy.Frame(content, content));
        Assert.Equal(NavigationAction.Cancel, NavigationPolicy.Frame(content, null));
        Assert.Equal(NavigationAction.Cancel, NavigationPolicy.Frame("about:blank", content));
        Assert.Equal(NavigationAction.Cancel, NavigationPolicy.Frame("https://example.org/frame", content));
        Assert.Equal(NavigationAction.Cancel, NavigationPolicy.Frame("about:srcdoc", null));
    }

    // Never a window; a user's middle, Ctrl or Shift click or a target in
    // the viewer is a link activation.
    [Fact]
    public void NewWindowsInTheViewer()
    {
        Assert.Equal(NavigationAction.CancelAndProbe, NavigationPolicy.NewWindow(WebViewKind.Viewer, "https://example.org/", isUserInitiated: true));
        Assert.Equal(NavigationAction.Cancel, NavigationPolicy.NewWindow(WebViewKind.Viewer, "https://example.org/", isUserInitiated: false));
        Assert.Equal(NavigationAction.Cancel, NavigationPolicy.NewWindow(WebViewKind.Viewer, "javascript:x", isUserInitiated: true));
    }
}
