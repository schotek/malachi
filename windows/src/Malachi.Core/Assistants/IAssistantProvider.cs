// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Provider boundary from docs/chatgpt-integration.md §3. The Claude path in
// AssistantPanelController/AssistantRequest remains the Go/Swift reference.

using System.Threading;
using System.Threading.Tasks;

namespace Malachi.Core.Assistants;

/// <summary>Account-independent widgets use this runtime, never credentials.</summary>
public interface IAssistantProvider
{
    /// <summary>Stable identity, separate from the desktop/app target.</summary>
    AssistantProviderID Id { get; }

    /// <summary>The model chosen for the next session.</summary>
    string Model { get; }

    /// <summary>Whether this provider's current consent version was accepted.</summary>
    bool HasConsent { get; }

    /// <summary>Stores consent for this provider only.</summary>
    void AcceptConsent();

    /// <summary>Opens a fresh isolated session and verifies policy before input.</summary>
    Task<IAssistantSession> OpenAsync(AssistantSessionSpec spec, CancellationToken cancellationToken);
}
