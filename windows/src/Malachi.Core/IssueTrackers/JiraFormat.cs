// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Jira/JiraCompose.swift (Jira.Format);
// GTK: ui/internal/jira/compose.go (Format and its constants).

namespace Malachi.Core.IssueTrackers;

/// <summary>jira.Format: a formatting control of the compose window's toolbar.</summary>
public enum JiraFormat
{
    /// <summary>Bold ("bold").</summary>
    Bold,

    /// <summary>Italic ("italic").</summary>
    Italic,

    /// <summary>Underline ("underline").</summary>
    Underline,

    /// <summary>Monospace code ("code").</summary>
    Code,

    /// <summary>The paragraph style menu ("heading").</summary>
    Heading,

    /// <summary>The alignment menu ("alignment").</summary>
    Alignment,

    /// <summary>A bulleted list ("bulletList").</summary>
    BulletList,

    /// <summary>A numbered list ("numberedList").</summary>
    NumberedList,

    /// <summary>A quote ("quote").</summary>
    Quote,

    /// <summary>A link ("link").</summary>
    Link,

    /// <summary>The text colour ("colour").</summary>
    Colour,

    /// <summary>An inline picture ("image").</summary>
    Image,

    /// <summary>Removes formatting, so it stays within the others ("clear").</summary>
    Clear,
}
