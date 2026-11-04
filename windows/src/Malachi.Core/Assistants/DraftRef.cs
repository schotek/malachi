// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantEvents.swift
// (Assistant.DraftRef); GTK: ui/internal/assistant/events.go (DraftRef).

namespace Malachi.Core.Assistants;

/// <summary>
/// A draft the bridge stored for the panel, as the head of a create_draft
/// result names it (<see cref="Assistant.ParseDraftResult"/>). Opaque ids of
/// the API, as strings.
/// </summary>
/// <param name="AccountId">The account id.</param>
/// <param name="DraftId">The draft id.</param>
/// <param name="Version">The draft's version, 0 to 999 999 999.</param>
public sealed record DraftRef(string AccountId, string DraftId, int Version);
