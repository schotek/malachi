// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/MailModel+Threads.swift (ListKey);
// GTK: ui/internal/window/thread_model.go (listKey).

using Malachi.Core.Api;

namespace Malachi.Core.Model;

/// <summary>
/// Addresses one row of the message list. A conversation row has
/// <see cref="Message"/> null; a member row (and a single-message
/// conversation, which is shown as a plain row) has both; a flat-mode row
/// has <see cref="Thread"/> null.
/// </summary>
/// <param name="Thread">The conversation, in grouped mode.</param>
/// <param name="Message">The message, but on a conversation row.</param>
public readonly record struct ListKey(ThreadId? Thread = null, MessageId? Message = null);
