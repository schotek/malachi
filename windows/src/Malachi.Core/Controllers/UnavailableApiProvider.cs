// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AssistantController+API.swift
// (UnavailableAPIProvider); GTK: ui/internal/window/ai_provider.go
// (unavailableAPI). Stands in for the provider over an ai account that
// cannot be used yet (no client, the account not listed yet, or removed):
// named after the account, every session fails with "api_unavailable", so
// that nothing runs Claude Code meanwhile. This file holds no translatable
// text.

using System;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Assistants;

namespace Malachi.Core.Controllers;

/// <summary>
/// The provider of an ai account that cannot run yet
/// (<see cref="AssistantController.ApiProviderOrUnavailable"/>).
/// </summary>
/// <param name="name">The account's name (<see cref="AssistantController.ApiBrand"/>).</param>
public sealed class UnavailableApiProvider(string name) : IAssistantProvider
{
    /// <summary>The reason every session fails with.</summary>
    public const string Reason = "api_unavailable";

    /// <inheritdoc/>
    public string Name { get; } = name ?? throw new ArgumentNullException(nameof(name));

    /// <inheritdoc/>
    public string Model => "";

    /// <inheritdoc/>
    public bool Available => false;

    /// <inheritdoc/>
    public bool Connected => false;

    /// <inheritdoc/>
    public bool HasConsent => false;

    /// <inheritdoc/>
    public bool HasBoardConsent => false;

    /// <inheritdoc/>
    public void AcceptConsent()
    {
    }

    /// <inheritdoc/>
    public void AcceptBoardConsent()
    {
    }

    /// <inheritdoc/>
    public Task<IAssistantSession> OpenAsync(AssistantSessionSpec spec, CancellationToken cancellationToken) =>
        Task.FromException<IAssistantSession>(new AssistantProviderException(Reason));
}
