// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Installed runtime model catalog from docs/chatgpt-integration.md §3, §5;
// provider-specific IDs stay outside the Go/Swift Claude model enum.

namespace Malachi.Core.ChatGPT;

/// <summary>A model the installed App Server exposes, not a billing promise.</summary>
public sealed record CodexModel(string Id, string DisplayName);
