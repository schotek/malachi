// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The tests of ContextMenuPolicy over the menus WebView2 153 offers
// (SPIKES.md §2g): the viewer keeps Copy and Copy Link (htmlview/view.go
// contextMenu, MessageWebView.swift keptItems), the editor the editing
// commands, the previewer Copy.

using System;
using System.Linq;
using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class ContextMenuPolicyTests
{
    private const string Separator = "other";

    [Fact]
    public void TheKeptItems()
    {
        Assert.Equal(["copy", "copyLinkLocation"], ContextMenuPolicy.KeptItems(WebViewKind.Viewer).Order(StringComparer.Ordinal));
        Assert.Equal(
            ["copy", "cut", "paste", "pasteAndMatchStyle", "redo", "selectAll", "undo"],
            ContextMenuPolicy.KeptItems(WebViewKind.Editor).Order(StringComparer.Ordinal));
        Assert.Equal(["copy"], ContextMenuPolicy.KeptItems(WebViewKind.Preview));
        Assert.False(ContextMenuPolicy.Keeps(WebViewKind.Viewer, null));
        Assert.False(ContextMenuPolicy.Keeps(WebViewKind.Viewer, "Copy"));
        Assert.False(ContextMenuPolicy.Keeps(WebViewKind.Viewer, "inspectElement"));
    }

    // The link menu of the spike: only Copy Link stays, without separators.
    [Fact]
    public void TheViewersLinkMenu()
    {
        string[] names = ["openLinkInNewWindow", Separator, "saveLinkAs", "copyLinkLocation", Separator, "moreTools", Separator, "inspectElement"];
        var keep = ContextMenuPolicy.Keep(WebViewKind.Viewer, names, names.Select(n => n == Separator).ToArray());
        Assert.Equal(["copyLinkLocation"], names.Where((_, i) => keep[i]));
    }

    // The editable menu of the spike: the editing commands in their groups.
    [Fact]
    public void TheEditorsMenu()
    {
        string[] names =
        [
            "emoji", Separator, "undo", "redo", Separator, "cut", "copy", "paste", "pasteAndMatchStyle", "selectAll", Separator,
            "other57328", Separator, "moreTools", Separator, "inspectElement",
        ];
        var keep = ContextMenuPolicy.Keep(WebViewKind.Editor, names, names.Select(n => n == Separator).ToArray());
        Assert.Equal(["undo", "redo", Separator, "cut", "copy", "paste", "pasteAndMatchStyle", "selectAll"], names.Where((_, i) => keep[i]));
    }

    // Separators only between kept groups: never first, last or doubled.
    [Fact]
    public void Separators()
    {
        string[] names = [Separator, "copy", Separator, "print", Separator, Separator, "copyLinkLocation", Separator];
        var keep = ContextMenuPolicy.Keep(WebViewKind.Viewer, names, names.Select(n => n == Separator).ToArray());
        Assert.Equal([false, true, true, false, false, false, true, false], keep);
        Assert.Empty(ContextMenuPolicy.Keep(WebViewKind.Viewer, [], []));
        Assert.Throws<ArgumentException>(() => ContextMenuPolicy.Keep(WebViewKind.Viewer, ["copy"], []));
    }
}
