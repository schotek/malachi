// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The tests of LinkProbe, the Windows way to the macOS viewer script's
// {raw, resolved} (MessageWebView.swift viewerScript): the focused link's
// attribute counts only for the navigation it explains; a new window a form
// did not make is confirmed with the URL alone.

using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class LinkProbeTests
{
    [Theory]
    [InlineData(null)]
    [InlineData("")]
    [InlineData("null")]
    [InlineData("42")]
    [InlineData("\"a\"")]
    [InlineData("[\"a\"]")]
    [InlineData("{}")]
    [InlineData("{\"tag\": 3}")]
    [InlineData("{not json")]
    public void NotAProbeResult(string? json) => Assert.Null(LinkProbe.Parse(json));

    [Fact]
    public void ProbeResults()
    {
        Assert.Equal(new LinkProbeResult("body", null, null), LinkProbe.Parse("{\"tag\":\"body\"}"));
        Assert.Equal(
            new LinkProbeResult("a", "HTTPS://Example.COM", "https://example.com/"),
            LinkProbe.Parse("{\"tag\":\"a\",\"raw\":\"HTTPS://Example.COM\",\"resolved\":\"https://example.com/\"}"));
        // A link is its attribute and its URL together, or nothing.
        Assert.Equal(new LinkProbeResult("a", null, null), LinkProbe.Parse("{\"tag\":\"a\",\"raw\":\"x\"}"));
        Assert.Equal(new LinkProbeResult("a", null, null), LinkProbe.Parse("{\"tag\":\"a\",\"raw\":null,\"resolved\":\"https://x/\"}"));
    }

    // The attribute decides, as on macOS, when it explains the navigation.
    [Fact]
    public void TheFocusedLinkExplainsTheNavigation()
    {
        var probe = new LinkProbeResult("span", "https://Example.COM", "https://example.com/");
        Assert.Equal(new ActivatedLink("https://Example.COM", "https://example.com/"),
            LinkProbe.Activation(probe, "https://example.com/", newWindow: false));
        Assert.Equal(new ActivatedLink("https://Example.COM", "https://example.com/"),
            LinkProbe.Activation(probe, "https://example.com/", newWindow: true));
    }

    // A meta refresh or a form submit arrives as a user navigation too: the
    // focus is on no link that leads there, and nothing is handed on.
    [Fact]
    public void NavigationsOfThePageAreNotLinks()
    {
        Assert.Null(LinkProbe.Activation(null, "https://evil.example/", newWindow: false));
        Assert.Null(LinkProbe.Activation(new LinkProbeResult("body", null, null), "https://evil.example/", newWindow: false));
        Assert.Null(LinkProbe.Activation(new LinkProbeResult("button", null, null), "https://evil.example/?q=1", newWindow: false));
        // A focused link elsewhere does not explain this navigation.
        Assert.Null(LinkProbe.Activation(new LinkProbeResult("a", "https://ok.example/", "https://ok.example/"),
            "https://evil.example/", newWindow: false));
    }

    // A new window only a user starts: confirmed with the URL alone when the
    // focus did not follow, unless a form made it.
    [Fact]
    public void NewWindowsWithoutTheirLink()
    {
        Assert.Equal(new ActivatedLink(null, "https://x.example/"), LinkProbe.Activation(null, "https://x.example/", newWindow: true));
        Assert.Equal(new ActivatedLink(null, "https://x.example/"),
            LinkProbe.Activation(new LinkProbeResult("body", null, null), "https://x.example/", newWindow: true));
        Assert.Equal(new ActivatedLink(null, "https://x.example/"),
            LinkProbe.Activation(new LinkProbeResult("a", "https://y.example/", "https://y.example/"), "https://x.example/", newWindow: true));
        foreach (var control in new[] { "button", "input", "select", "textarea", "form" })
        {
            Assert.Null(LinkProbe.Activation(new LinkProbeResult(control, null, null), "https://x.example/?q=1", newWindow: true));
        }
    }

    // The probe reads through the prototypes: a named element of the page
    // cannot stand in for the document's accessors.
    [Fact]
    public void TheScriptUsesThePrototypes()
    {
        Assert.Contains("Object.getOwnPropertyDescriptor(Document.prototype, 'activeElement')", LinkProbe.Script, System.StringComparison.Ordinal);
        Assert.Contains("Element.prototype.closest.call", LinkProbe.Script, System.StringComparison.Ordinal);
        Assert.DoesNotContain("document.activeElement", LinkProbe.Script, System.StringComparison.Ordinal);
    }
}
