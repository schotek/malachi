// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the pages of window.blp's message_stack ("empty", "no-accounts",
// "message"); macOS: MessageViewController.swift (setPage) and
// Integration.swift (showNoAccountsPage); GTK: window.go emptyPageName.

namespace Malachi.Core.Presentation;

/// <summary>What the message pane shows (window.blp <c>message_stack</c>).</summary>
public enum ReaderPage
{
    /// <summary>No Message Selected.</summary>
    Empty,

    /// <summary>No Accounts, with Add Account… (while <c>account.list</c> is empty).</summary>
    NoAccounts,

    /// <summary>The headers and the body.</summary>
    Message,

    /// <summary>
    /// The whole conversation of a folded conversation row (the pane only,
    /// window.go <c>convPageName</c>): the pane's conversation view, which
    /// the reader leaves empty (<see cref="ReaderController.LeaveForConversation"/>).
    /// </summary>
    Conversation,
}
