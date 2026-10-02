// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Use-case policy from docs/chatgpt-integration.md §6; ui/internal/assistant/
// claude.go and macos/Sources/MalachiCore/Assistant/AssistantPanel.swift
// are the reference for the panel's semantic allow list.

namespace Malachi.Core.Assistants;

/// <summary>Immutable tool authority of one isolated provider session.</summary>
public enum AssistantToolPolicy
{
    /// <summary>Rewrite/search conversion: no bridge or external tools.</summary>
    None,
    /// <summary>The panel's read operations and create_draft, never send/modify.</summary>
    Panel,
}
