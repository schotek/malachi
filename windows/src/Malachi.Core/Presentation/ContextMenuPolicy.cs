// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/WebViews/MessageWebView.swift
// (ViewerWebView.keptItems, willOpenMenu) and ComposeWebView.swift
// (willOpenMenu); GTK: ui/internal/htmlview/view.go (contextMenu: Copy and
// Copy Link) and ui/internal/editor/editor.go (no menu).
//
// WebView2's ContextMenuRequested lists its items by name (measured on
// runtime 153: copy, copyLinkLocation, undo, redo, cut, paste,
// pasteAndMatchStyle, selectAll, and the ones removed here: reload, back,
// print, saveAs, saveLinkAs, openLinkInNewWindow, inspectElement, moreTools,
// emoji, …, separators named "other"). The viewer keeps Copy and Copy Link
// as GTK and macOS do. The editor keeps the editing commands, where GTK and
// macOS show no menu at all: WebView2's menu without them would offer
// nothing, and Windows users paste from the menu (windows/README.md).
// The previewer keeps Copy (a text file's selection). Labels are the
// runtime's own, localised by it: no new strings.

using System;
using System.Collections.Frozen;
using System.Collections.Generic;

namespace Malachi.Core.Presentation;

/// <summary>Which items of WebView2's context menu a view keeps.</summary>
public static class ContextMenuPolicy
{
    private static readonly FrozenSet<string> Viewer = FrozenSet.Create(StringComparer.Ordinal, "copy", "copyLinkLocation");

    private static readonly FrozenSet<string> Editor = FrozenSet.Create(
        StringComparer.Ordinal, "undo", "redo", "cut", "copy", "paste", "pasteAndMatchStyle", "selectAll");

    private static readonly FrozenSet<string> Preview = FrozenSet.Create(StringComparer.Ordinal, "copy");

    /// <summary>The names of the items a view of <paramref name="kind"/> keeps; every other item goes.</summary>
    public static IReadOnlySet<string> KeptItems(WebViewKind kind) => kind switch
    {
        WebViewKind.Editor => Editor,
        WebViewKind.Preview => Preview,
        _ => Viewer,
    };

    /// <summary>Whether a view of <paramref name="kind"/> keeps the item named <paramref name="name"/> (matched exactly).</summary>
    public static bool Keeps(WebViewKind kind, string? name) => name is not null && KeptItems(kind).Contains(name);

    /// <summary>
    /// Which items of a menu stay: the kept commands, and a separator only
    /// between two groups of them (never first, last or twice in a row).
    /// <paramref name="names"/> and <paramref name="separators"/> describe
    /// the items in order.
    /// </summary>
    public static bool[] Keep(WebViewKind kind, IReadOnlyList<string?> names, IReadOnlyList<bool> separators)
    {
        ArgumentNullException.ThrowIfNull(names);
        ArgumentNullException.ThrowIfNull(separators);
        if (names.Count != separators.Count)
        {
            throw new ArgumentException("one separator flag per item", nameof(separators));
        }
        var keep = new bool[names.Count];
        var lastKept = -1;
        var pendingSeparator = -1;
        for (var i = 0; i < names.Count; i++)
        {
            if (separators[i])
            {
                if (lastKept >= 0 && pendingSeparator < 0)
                {
                    pendingSeparator = i;
                }
                continue;
            }
            if (!Keeps(kind, names[i]))
            {
                continue;
            }
            if (pendingSeparator >= 0)
            {
                keep[pendingSeparator] = true;
                pendingSeparator = -1;
            }
            keep[i] = true;
            lastKept = i;
        }
        return keep;
    }
}
