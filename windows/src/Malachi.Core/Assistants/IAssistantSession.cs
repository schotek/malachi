// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Normalized session from docs/chatgpt-integration.md §3, reusing the event
// semantics of ui/internal/assistant/events.go and MalachiCore/Assistant.

using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;

namespace Malachi.Core.Assistants;

/// <summary>One ephemeral conversation; callbacks run on its opening context.</summary>
public interface IAssistantSession
{
    /// <summary>Ordered normalized events; wire payloads must never be logged.</summary>
    event EventHandler<IReadOnlyList<AssistantEvent>>? EventsReceived;

    /// <summary>Raised once after the last event and the owned children exit.</summary>
    event EventHandler<AssistantSessionExit>? Exited;

    /// <summary>Completes after all owned children and private state are removed.</summary>
    Task Completion { get; }

    /// <summary>Whether another turn can be submitted.</summary>
    bool IsRunning { get; }

    /// <summary>Submits exactly once; an active turn is an error, never replayed.</summary>
    Task SubmitAsync(string input, CancellationToken cancellationToken);

    /// <summary>Interrupts through the protocol, then ends the owned process tree.</summary>
    void Terminate();
}
