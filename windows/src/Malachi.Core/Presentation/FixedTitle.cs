// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only (docs/windows-port.md §6.2): the one title every document of
// the web views has. WinUI draws a WebView2 through a top-level window of
// the browser process titled with the document's title, which other
// processes can read. The viewer's and the editor's documents carry it as
// their first <title>; what the view does not write itself, a picture or a
// PDF of the previewer, gets its title from Chromium: the picture's URL and
// size, or the PDF's own /Title, which is content of the mail (measured). A
// host script puts the fixed title back whenever the title changes.

namespace Malachi.Core.Presentation;

/// <summary>The fixed title of the web views' documents.</summary>
public static class FixedTitle
{
    /// <summary>The title (a brand name, not translated).</summary>
    public const string Title = "Malachi Mail"; // Windows-only string

    /// <summary>
    /// The host script that sets it, through the prototype's setter, which a
    /// named element of the page cannot shadow.
    /// </summary>
    public const string Script =
        "Object.getOwnPropertyDescriptor(Document.prototype, 'title').set.call(document, '" + Title + "')";

    /// <summary>Whether <paramref name="title"/> (CoreWebView2.DocumentTitle) is the fixed one.</summary>
    public static bool IsFixed(string? title) => string.Equals(title, Title, System.StringComparison.Ordinal);
}
