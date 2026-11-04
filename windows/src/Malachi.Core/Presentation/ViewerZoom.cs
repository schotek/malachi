// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/WebViews/MessageWebView.swift (setZoom:
// pageZoom = percent / 100); GTK: ui/internal/htmlview/view.go (SetZoom:
// SetZoomLevel).
//
// The WinUI WebView2 has no ZoomFactor (it belongs to the controller, which
// the control does not expose), so the viewer scales the page with CSS
// zoom, which script-off pages still take from a host script (measured):
// set on the document's root as it is served (no flash at 100 % first) and
// through ExecuteScriptAsync when the setting changes. It is important, so
// a message's own stylesheet cannot undo the reader's choice, as GTK's and
// macOS's page zoom cannot be undone by the page.

using System;
using System.Globalization;

namespace Malachi.Core.Presentation;

/// <summary>The viewer's text zoom as CSS.</summary>
public static class ViewerZoom
{
    private const string Root = "<html>";

    /// <summary>
    /// The CSS zoom of <paramref name="percent"/> (the <c>text-zoom</c>
    /// setting): its hundredth, invariant; 1 for zero or less, as SetZoom
    /// reads them.
    /// </summary>
    public static string Factor(int percent)
    {
        var p = percent <= 0 ? 100 : percent;
        return (p / 100m).ToString("0.##", CultureInfo.InvariantCulture);
    }

    /// <summary>
    /// <paramref name="document"/> (a <see cref="Html.ViewerDocument"/>) with
    /// the zoom on its root element; unchanged at 100 %.
    /// </summary>
    public static string Apply(string document, int percent)
    {
        ArgumentNullException.ThrowIfNull(document);
        var factor = Factor(percent);
        if (factor == "1")
        {
            return document;
        }
        var root = document.IndexOf(Root, StringComparison.Ordinal);
        return root < 0
            ? document
            : document[..root] + "<html style=\"zoom: " + factor + " !important\">" + document[(root + Root.Length)..];
    }

    /// <summary>
    /// The host script that sets the zoom of the loaded document; it reaches
    /// the root through the prototype, which a named element of the page
    /// cannot shadow.
    /// </summary>
    public static string Script(int percent) =>
        "Object.getOwnPropertyDescriptor(Document.prototype, 'documentElement').get.call(document)"
        + ".style.setProperty('zoom', '" + Factor(percent) + "', 'important')";
}
