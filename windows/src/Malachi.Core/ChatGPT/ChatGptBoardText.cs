// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/ChatGPT/AssistantProvider.swift
// (ChatGPTText.boardHeading, boardBody); GTK msgid reference:
// ui/internal/assistant/chatgpt.go (BoardConsentHeading, BoardConsentBody).
// The provider's other texts are the application's (Malachi.App
// ChatGptText); the board's consent lives here, with the core's board
// controllers that ask for it.

using Malachi.Core.I18n;

namespace Malachi.Core.ChatGPT;

/// <summary>The board's consent texts of the ChatGPT provider; never provider diagnostics or tokens.</summary>
public static class ChatGptBoardText
{
    /// <summary>The heading of the board's consent for ChatGPT.</summary>
    public static string BoardConsentHeading => L10n.T("Let OpenAI Refine the Board?");

    /// <summary>The body of the board's consent for ChatGPT.</summary>
    public static string BoardConsentBody => L10n.T("Malachi Mail will send board mail to OpenAI through Codex, using your ChatGPT plan. It reads the conversations on the board and any other mail and attachments it needs, and annotates cases. A triage you start yourself may also write replies, which stay on the board, never in your Drafts folder, until you send them. Automatic triage sends newly received mail while enabled. It cannot send, delete or move messages. Which accounts it triages you choose in Settings.");
}
