// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/htmlview/size.go (sizeScript's measure, validHeight,
// maxReportedCSS); macOS: MessageWebView.swift (the card's size report).
//
// The height measurement of a conversation card on Windows. GTK runs its
// measuring script in an isolated world of the card's page, which reports
// the height whenever it changes (a ResizeObserver, every picture's load).
// A WebView2 page with script off runs no listener of an injected script
// (measured, LinkProbe), so the host asks instead: this host script
// (ExecuteScriptAsync runs with page script off) reads where the column
// Document puts the message in ends, its overflow included, exactly as
// sizeScript's measure does, whenever the card may have changed height (the
// document loaded, a picture was served, the view's width, height or zoom
// changed). It reads only through the prototypes' own accessors, so a named
// element of the message cannot stand in for document.getElementById or an
// element's size, and changes nothing. The height is in CSS pixels of the
// viewport, the CSS zoom of the text-zoom setting included (the viewer's
// zoom is CSS, ViewerZoom), so the card's governor runs at zoom 1. The
// column's own height is scaled by the zoom as its box is, so that the
// scroll height, which the zoom does not scale, does not make the card too
// tall at a zoom below 100 %.

using System;
using System.Globalization;

namespace Malachi.Core.Presentation;

/// <summary>A conversation card's document height, read by the host.</summary>
public static class CardSize
{
    /// <summary>maxReportedCSS: the tallest height taken, in CSS pixels, before the card's governor (which caps the view at 4000 pixels) sees it.</summary>
    public const double MaxReported = 1 << 20;

    /// <summary>
    /// The host script: the document's height in CSS pixels (a number), or
    /// null when it cannot be read.
    /// </summary>
    public const string Script = """
        (() => {
          try {
            const col = Document.prototype.getElementById.call(document, 'malachi-column');
            let h = 0;
            if (col) {
              const r = Element.prototype.getBoundingClientRect.call(col);
              const offset = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight').get.call(col);
              const scroll = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollHeight').get.call(col);
              const scale = offset > 0 ? r.height / offset : 1;
              h = r.top + window.scrollY + Math.max(r.height, scroll * scale);
            } else {
              const body = Object.getOwnPropertyDescriptor(Document.prototype, 'body').get.call(document);
              if (body) h = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollHeight').get.call(body);
            }
            return Math.ceil(h);
          } catch (e) {
            return null;
          }
        })()
        """;

    /// <summary>
    /// validHeight over the script's JSON result (ExecuteScriptAsync's
    /// answer): a finite number that is not negative, capped at
    /// <see cref="MaxReported"/>; null for anything else.
    /// </summary>
    public static double? Parse(string? json)
    {
        if (string.IsNullOrWhiteSpace(json)
            || !double.TryParse(json, NumberStyles.Float, CultureInfo.InvariantCulture, out var h)
            || double.IsNaN(h) || double.IsInfinity(h) || h < 0)
        {
            return null;
        }
        return Math.Min(h, MaxReported);
    }
}
