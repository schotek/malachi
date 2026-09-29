// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantPanel.swift
// (Assistant.PanelStrings); GTK: ui/internal/assistant/assistant.go
// (PanelStrings). As in Swift, the record also carries the buttons the panel
// shares with other windows under their existing msgids (Send, Cancel and
// Try Again, mnemonics kept: the view layer turns them into access keys) and
// the context chip's texts (SelectedMessage, AllMail), which Go leaves to
// ContextLabel.

namespace Malachi.Core.Assistants;

/// <summary>
/// The fixed texts of the assistant panel (target In App), its settings rows
/// and its consent question (<see cref="Assistant.PanelTexts"/>). The texts
/// that depend on something have functions of their own: the context chip
/// <see cref="Assistant.ContextLabel"/> (<see cref="Assistant.ConversationLabel"/>
/// once a question was asked), a tool's line <see cref="Assistant.ActivityLabel"/>,
/// a failed turn <see cref="Assistant.StoppedText"/>, a model
/// <see cref="Assistant.ModelName"/>, the panel's name in the Open In choice
/// <c>TargetName(App)</c>, the title of its settings row <c>TargetName(Code)</c>
/// and the panel's title <c>Texts().Assistant</c>.
/// </summary>
public sealed record PanelStrings
{
    /// <summary>The question field's placeholder.</summary>
    public required string Placeholder { get; init; }

    /// <summary>The placeholder while Draft a Reply… waits for the user's words.</summary>
    public required string ReplyPlaceholder { get; init; }

    /// <summary>The placeholder while Ask About This Message… or an attachment waits for the user's words.</summary>
    public required string AskPlaceholder { get; init; }

    /// <summary>The Send button (<c>_Send</c>, the msgid other windows use).</summary>
    public required string Send { get; init; }

    /// <summary>Ends the running turn (the button that is Send while nothing runs).</summary>
    public required string Stop { get; init; }

    /// <summary>Ends the conversation and clears the panel.</summary>
    public required string NewConversation { get; init; }

    /// <summary>The context chip for one message.</summary>
    public required string SelectedMessage { get; init; }

    /// <summary>The context chip without a selection.</summary>
    public required string AllMail { get; init; }

    /// <summary>A draft card.</summary>
    public required string DraftReady { get; init; }

    /// <summary>A draft card's button.</summary>
    public required string OpenDraft { get; init; }

    /// <summary>The bar over the transcript while the list's selection is not part of what the conversation is about.</summary>
    public string AnotherSelected { get; init; } = "";

    /// <summary>The bar's button that adds the selection (the other one is <see cref="NewConversation"/>).</summary>
    public string AddToConversation { get; init; } = "";

    /// <summary>The transcript's error line when Claude Code is not signed in.</summary>
    public required string NotSignedIn { get; init; }

    /// <summary>The transcript's error line when the bridge's tools are missing.</summary>
    public required string ToolsMissing { get; init; }

    /// <summary>The transcript's note after Stop.</summary>
    public required string Stopped { get; init; }

    /// <summary>The Try Again button (the msgid other windows use).</summary>
    public required string TryAgain { get; init; }

    /// <summary>The toast of an Open Draft whose draft is gone.</summary>
    public required string DraftGone { get; init; }

    /// <summary>The line under the question field.</summary>
    public required string Footer { get; init; }

    /// <summary>The heading of the question before the first question ever.</summary>
    public required string ConsentHeading { get; init; }

    /// <summary>The text of that question.</summary>
    public required string ConsentBody { get; init; }

    /// <summary>That question's button that allows it.</summary>
    public required string Allow { get; init; }

    /// <summary>That question's Cancel button (<c>_Cancel</c>, the msgid other windows use).</summary>
    public required string Cancel { get; init; }

    /// <summary>The View menu's item while the panel is hidden.</summary>
    public required string Show { get; init; }

    /// <summary>The View menu's item while the panel is shown.</summary>
    public required string Hide { get; init; }

    /// <summary>The model row's title in the settings.</summary>
    public required string Model { get; init; }

    /// <summary>The settings button that picks the claude executable.</summary>
    public required string Choose { get; init; }

    /// <summary>The state in the Claude Code row's subtitle when Claude Code is signed in.</summary>
    public required string SignedIn { get; init; }

    /// <summary>The state in the Claude Code row's subtitle when Claude Code is not signed in.</summary>
    public required string NotSignedInShort { get; init; }

    /// <summary>
    /// Claude Code was not found (the transcript's error line and the Claude
    /// Code row's subtitle; <c>Problem(App, …)</c> says the same).
    /// </summary>
    public string NotFound { get; init; } = "";
}
