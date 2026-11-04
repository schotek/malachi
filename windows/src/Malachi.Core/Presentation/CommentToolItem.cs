// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/compose/comment.go (toolItem). A child of the compose
// window's formatting bar as CommentMode.RestrictedToolbar reads it.

using Malachi.Core.IssueTrackers;

namespace Malachi.Core.Presentation;

/// <summary>
/// A child of the formatting bar: a separator, or a control of
/// <see cref="Format"/> (null: a control the comment mode leaves alone).
/// </summary>
public readonly record struct CommentToolItem(bool Separator, JiraFormat? Format = null)
{
    /// <summary>A separator between two groups of controls.</summary>
    public static CommentToolItem Divider { get; } = new(Separator: true);

    /// <summary>A control that applies <paramref name="format"/>.</summary>
    public static CommentToolItem Control(JiraFormat? format) => new(Separator: false, format);
}
