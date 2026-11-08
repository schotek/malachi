// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantAPI.swift
// (Assistant.ProviderChoiceState); GTK: ui/internal/assistant/api.go
// (ProviderChoiceState). This file holds no translatable text.

namespace Malachi.Core.Assistants;

/// <summary>
/// What the client knows of a local provider for the Assistant Uses row:
/// whether it can run now, and why not (a sentence shown with the greyed
/// entry; "" when <paramref name="Available"/>).
/// </summary>
/// <param name="Available">Whether it can run now.</param>
/// <param name="Problem">Why not; "" when it can.</param>
public sealed record ProviderChoiceState(bool Available = false, string Problem = "");
