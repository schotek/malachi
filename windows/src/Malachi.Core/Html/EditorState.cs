// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/HTML/EditorBridge.swift (EditorState);
// GTK: ui/internal/editor/bridge.go (State).

namespace Malachi.Core.Html;

/// <summary>editor.State: the formatting at the caret, for the toolbar's toggles.</summary>
public sealed record EditorState
{
    /// <summary>Bold.</summary>
    public bool Bold { get; init; }

    /// <summary>Italic.</summary>
    public bool Italic { get; init; }

    /// <summary>Underlined.</summary>
    public bool Underline { get; init; }

    /// <summary>Struck through.</summary>
    public bool Strike { get; init; }

    /// <summary>In a bulleted list.</summary>
    public bool Ul { get; init; }

    /// <summary>In a numbered list.</summary>
    public bool Ol { get; init; }

    /// <summary>Inside a link.</summary>
    public bool Link { get; init; }

    /// <summary>The block: "p", "h1", "blockquote", …; "" when unknown.</summary>
    public string Block { get; init => field = value ?? ""; } = "";

    /// <summary>left, center or right; "" when unknown.</summary>
    public string Align { get; init => field = value ?? ""; } = "";
}
