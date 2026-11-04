// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/compose/comment.go (restrictedToolbar,
// chosenVisibility); macOS: FormatToolbar.restrict and CommentHeaderView
// (visibility). The pure half of a compose window's comment mode
// (Jira.CommentCompose): which controls of the formatting bar a comment
// keeps, and the visibility the header's choice names.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.IssueTrackers;

namespace Malachi.Core.Presentation;

/// <summary>The rules of a compose window's comment mode.</summary>
public static class CommentMode
{
    /// <summary>
    /// restrictedToolbar: which of the bar's <paramref name="items"/> show in
    /// comment mode: a control when a comment keeps its format
    /// (<see cref="Jira.CommentAllows"/>) or when it has none, a separator
    /// only between two groups that kept a control. Formats without a control
    /// (code) change nothing.
    /// </summary>
    public static IReadOnlyList<bool> RestrictedToolbar(IReadOnlyList<CommentToolItem> items)
    {
        ArgumentNullException.ThrowIfNull(items);
        var shown = new bool[items.Count];
        var last = -1; // the last item shown
        for (var i = 0; i < items.Count; i++)
        {
            var it = items[i];
            if (it.Separator)
            {
                if (last >= 0 && !items[last].Separator)
                {
                    shown[i] = true;
                    last = i;
                }
            }
            else if (it.Format is not { } f || Jira.CommentAllows(f))
            {
                shown[i] = true;
                last = i;
            }
        }
        if (last >= 0 && items[last].Separator)
        {
            shown[last] = false;
        }
        return shown;
    }

    /// <summary>
    /// chosenVisibility: the visibility of the option named
    /// <paramref name="active"/> (the wire value the header's choice
    /// carries); public when no option has that name, as without a choice.
    /// </summary>
    public static CommentVisibility ChosenVisibility(IReadOnlyList<JiraVisibilityOption> options, string? active)
    {
        ArgumentNullException.ThrowIfNull(options);
        foreach (var o in options)
        {
            if (string.Equals(o.Visibility.Value, active, StringComparison.Ordinal))
            {
                return o.Visibility;
            }
        }
        return CommentVisibility.Public;
    }
}
