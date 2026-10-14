// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Provider-neutral exit from docs/chatgpt-integration.md §3; unlike the
// Claude exit, provider diagnostics are fixed codes, never child stderr.

namespace Malachi.Core.Assistants;

/// <summary>An owned runtime ended; the reason is safe technical text.</summary>
public sealed record AssistantSessionExit(int Status, string Description);
