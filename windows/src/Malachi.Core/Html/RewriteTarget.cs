// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/HTML/EditorBridge.swift (RewriteTarget);
// GTK: ui/internal/editor/bridge.go (RewriteTarget).
//
// macOS reads it from what its bridge's rewriteTarget returns (decode);
// the Windows bridge posts it as GTK's does, a "rewrite" message
// (BridgeMessage, EditorChannel.BeginRewriteTarget), whose missing fields
// are their zero values. Mail text: ToString shows the length only
// (docs/windows-port.md §3.1).

using System.Globalization;

namespace Malachi.Core.Html;

/// <summary>
/// editor.RewriteTarget: what the compose window's rewrite works on, as the
/// bridge reports it: the selection (<see cref="Selected"/>), or the user's
/// own text above the quoted original, and its text as the page renders it
/// (paragraphs and line breaks as newlines). Mail text: shown and sent only
/// as plain text. The empty target is what a bridge that is not running
/// answers.
/// </summary>
public sealed record RewriteTarget
{
    /// <summary>The passage is the selection; otherwise the text above the attribution line (or the whole body).</summary>
    public bool Selected { get; init; }

    /// <summary>The passage's text; "" when there is none.</summary>
    public string Text { get; init => field = value ?? ""; } = "";

    /// <summary>Whether the passage is the selection and the length of its text, never the text.</summary>
    public override string ToString() => string.Create(
        CultureInfo.InvariantCulture, $"RewriteTarget(selected: {(Selected ? "true" : "false")}, text: {Text.Length} chars)");
}
