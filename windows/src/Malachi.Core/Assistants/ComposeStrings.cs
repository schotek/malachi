// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantRewrite.swift
// (Assistant.ComposeStrings); GTK: ui/internal/assistant/assistant.go
// (ComposeStrings). Swift adds Discard, which Go leaves to the window.

namespace Malachi.Core.Assistants;

/// <summary>
/// The fixed texts of the compose window's rewrite
/// (<see cref="Assistant.ComposeTexts"/>): a popover under the toolbar's
/// Assistant button, with the presets (<see cref="Assistant.RewriteLabel"/>),
/// a field for the user's own instruction, the answer and what to do with
/// it. Its errors are the panel's (PanelTexts' NotFound and NotSignedIn,
/// StoppedText). The button that closes it without a change keeps the
/// existing msgid of Discard (<see cref="Discard"/>, the mnemonic kept; the
/// view turns it into an access key).
/// </summary>
public sealed record ComposeStrings
{
    /// <summary>The popover's title with a selection in the editor.</summary>
    public required string RewriteSelection { get; init; }

    /// <summary>The popover's title when it works on the user's own text above the quoted original.</summary>
    public required string RewriteText { get; init; }

    /// <summary>The placeholder of the field for the user's own instruction.</summary>
    public required string Custom { get; init; }

    /// <summary>The line while the answer arrives.</summary>
    public required string Rewriting { get; init; }

    /// <summary>The answer in place of the passage (the default button).</summary>
    public required string Replace { get; init; }

    /// <summary>The answer after the passage, which stays.</summary>
    public required string InsertBelow { get; init; }

    /// <summary>"_Discard".</summary>
    public required string Discard { get; init; }
}
