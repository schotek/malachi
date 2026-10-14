// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantUsage.swift
// (Assistant.maxUsageTokens); GTK: ui/internal/assistant/usage.go
// (MaxUsageTokens). This file holds no translatable text.

namespace Malachi.Core.Assistants;

public static partial class Assistant
{
    /// <summary>
    /// The largest value of each counter of a tally's total
    /// (assistant.MaxUsageTokens): what the daemon stores at most
    /// (<c>API.Limits.maxBoardUsageTokens</c>).
    /// </summary>
    public const long MaxUsageTokens = 1_000_000_000_000;
}
