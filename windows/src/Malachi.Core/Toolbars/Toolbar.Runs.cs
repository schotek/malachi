// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/plans/toolbar-customization.md W.2): a toolbar's
// sections are drawn by separate bars on Windows (Mail's sidebar row, the
// list's header and MessageCommandBar; the Board's bar and its detail's
// row), so each bar takes its run of Toolbar.PresentFor: the entries
// between the separator that starts its section and the next one. GTK's
// header bars do the same from window.go; macOS has one NSToolbar.

using System;
using System.Collections.Generic;

namespace Malachi.Core.Toolbars;

public static partial class Toolbar
{
    /// <summary>
    /// The entries of <paramref name="shown"/> in section <paramref name="s"/>
    /// of <paramref name="bar"/>, without separators: from the start for the
    /// first section, else after its separator, up to the next separator.
    /// A section Present left out (or its separator) has no run.
    /// </summary>
    public static IReadOnlyList<Shown> SectionRun(this IReadOnlyList<Shown> shown, Bar bar, Section s)
    {
        ArgumentNullException.ThrowIfNull(shown);
        var output = new List<Shown>();
        var secs = SectionsOf[bar];
        var inside = secs.Length > 0 && secs[0] == s;
        var sep = Separator(s);
        foreach (var e in shown)
        {
            if (IsSeparator(e.Id))
            {
                if (inside)
                {
                    break;
                }
                inside = string.Equals(e.Id, sep, StringComparison.Ordinal);
                continue;
            }
            if (inside)
            {
                output.Add(e);
            }
        }
        return output;
    }

    /// <summary>The entries of <paramref name="shown"/> in any of <paramref name="sections"/>, in order, without separators.</summary>
    public static IReadOnlyList<Shown> SectionRuns(this IReadOnlyList<Shown> shown, Bar bar, params Section[] sections)
    {
        ArgumentNullException.ThrowIfNull(sections);
        var output = new List<Shown>();
        foreach (var s in sections)
        {
            output.AddRange(SectionRun(shown, bar, s));
        }
        return output;
    }
}
