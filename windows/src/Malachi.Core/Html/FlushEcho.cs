// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/compose/draft.go (flushEcho, record, echo); macOS keeps
// the same rule as lastFlushedHTML in ComposeWindowController.swift.
//
// The compose window records what a save's flush reported and asks, for
// every Changed of the editor, whether it is that report. It is correct in
// either order of the flush's two answers only together with EditorChannel,
// which holds a changed back until the flush it may belong to has returned
// (docs/windows-port.md §6.5).

using System;

namespace Malachi.Core.Html;

/// <summary>
/// compose.flushEcho: remembers the content a save's flush reported: the
/// editor's <c>changed</c> carrying the same HTML is that report, not an
/// edit. Without it every save marked the draft dirty again and armed the
/// next autosave.
/// </summary>
public sealed class FlushEcho
{
    private string html = "";
    private bool set;

    /// <summary>record: notes the HTML the flush reported.</summary>
    public void Record(string html)
    {
        ArgumentNullException.ThrowIfNull(html);
        this.html = html;
        set = true;
    }

    /// <summary>
    /// echo: whether <paramref name="html"/> is what the last flush reported;
    /// any other content is an edit and forgets the record.
    /// </summary>
    public bool Echo(string html)
    {
        ArgumentNullException.ThrowIfNull(html);
        if (set && string.Equals(this.html, html, StringComparison.Ordinal))
        {
            return true;
        }
        this.html = "";
        set = false;
        return false;
    }
}
