// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only (docs/windows-port.md §6.6): how the previewer shows an
// attachment, the decided replacement for GNOME Sushi and Quick Look.

namespace Malachi.Core.Presentation;

/// <summary>What the previewer makes of an attachment (<see cref="PreviewContent.Classify"/>).</summary>
public enum PreviewKind
{
    /// <summary>Nothing to render: the panel with the icon, name, size and type.</summary>
    None,

    /// <summary>A picture Chromium decodes (never SVG), served as its sniffed type.</summary>
    Image,

    /// <summary>A PDF, in WebView2's viewer with its saving and printing hidden.</summary>
    Pdf,

    /// <summary>Text, shown as escaped text in the previewer's own document (HTML, SVG, XML and messages as source).</summary>
    Text,
}
