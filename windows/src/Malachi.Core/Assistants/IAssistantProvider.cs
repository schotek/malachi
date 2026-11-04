// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Provider boundary from docs/chatgpt-integration.md §3. The Claude path in
// AssistantPanelController/AssistantRequest remains the Go/Swift reference;
// Available, Connected and the board consent are those of
// macos/Sources/MalachiCore/ChatGPT/AssistantProvider.swift (available,
// connected) and of its settings' board consent version.

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

    /// <summary>Whether the runtime is installed on this computer.</summary>
    bool Available { get; }

    /// <summary>Whether the account is connected.</summary>
    bool Connected { get; }

    /// <summary>Whether this provider's current consent version was accepted.</summary>
    bool HasConsent { get; }

    /// <summary>Whether this provider's current board consent version was accepted (triage and suggested replies).</summary>
    bool HasBoardConsent { get; }

    /// <summary>Stores consent for this provider only.</summary>
    void AcceptConsent();

    /// <summary>Stores the board consent for this provider only.</summary>
    void AcceptBoardConsent();

    /// <summary>Opens a fresh isolated session and verifies policy before input.</summary>
    Task<IAssistantSession> OpenAsync(AssistantSessionSpec spec, CancellationToken cancellationToken);
}
