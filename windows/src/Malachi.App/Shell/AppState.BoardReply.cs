// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the suggested reply's part of macos/Sources/MalachiMail/App/AppState.swift
// (boardReply, its request's provider, providerChanged, the consent in
// wireBoardTriage, cancelAndCleanUp in stopBoardTriage); GTK:
// window/board_suggest_reply.go (AttachBoardReply, StopBoardReply). The
// board's Suggest Reply, once for the application: its request runs the
// user's Claude Code (or the selected provider) with the bridge beside the
// application, asks the assistant's own consent (the panel's, not the
// triage's) on the main window, and after a linked draft lists the main
// window's board again. Quitting stops it and waits for the deletes of a
// draft it created and did not link yet (Integration's board step).

using Malachi.Core.Controllers;
using Malachi.Core.Settings;
using Microsoft.Extensions.Logging;

namespace Malachi.App.Shell;

/// <summary>The board's suggested reply.</summary>
public sealed partial class AppState
{
    private SettingsChangeToken? boardReplyClaudePath;

    /// <summary>The board's Suggest Reply, one for the application.</summary>
    public BoardReplyController BoardReply { get; private set; } = null!;

    // After the Assistant (its locator, its availability, the provider).
    private void InitializeBoardReply()
    {
        var request = NewAssistantRequest();
        BoardReply = new BoardReplyController(
            Client,
            Settings,
            ClaudeCode,
            request,
            Assistant,
            Paths.McpBridge,
            Paths.Socket,
            logger: Logs.CreateLogger<BoardReplyController>())
        {
            Consent = () => AskAssistantConsentAsync(MainWindow),
            // The main window's board lists again (the linked draft shows).
            OnRefresh = () => Integration?.RefreshBoard(),
        };
        // Another path to Claude Code may find another one (GTK).
        boardReplyClaudePath = Settings.OnChange(SettingsKey.AssistantClaudePath, BoardReply.AvailabilityChanged);
        InAppProviderChanged += (_, _) =>
        {
            // The request runs on the selected provider; changing it stops
            // what runs (Swift providerChanged).
            BoardReply.Request.Provider = InAppProvider;
            BoardReply.ProviderChanged();
        };
        BoardReply.CheckSignIn();
    }

    private void CloseBoardReply()
    {
        boardReplyClaudePath?.Cancel();
        boardReplyClaudePath = null;
        if (BoardReply is { } reply)
        {
            reply.Dispose();
            reply.Request.Dispose();
        }
    }
}
