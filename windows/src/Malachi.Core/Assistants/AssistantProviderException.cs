// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Provider failure codes from docs/chatgpt-integration.md §3. No protocol
// payload, model output, credential or child stderr is retained in errors.

using System;

namespace Malachi.Core.Assistants;

/// <summary>A safe technical failure; callers may display its fixed code.</summary>
public sealed class AssistantProviderException(string code) : Exception(code)
{
    /// <summary>The bounded code, suitable for diagnostics.</summary>
    public string Code { get; } = code;
}
