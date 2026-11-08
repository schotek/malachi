// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantAPI.swift
// (Assistant.ProviderChoice); GTK: ui/internal/assistant/api.go
// (ProviderChoice). This file holds no translatable text.

namespace Malachi.Core.Assistants;

/// <summary>
/// One entry of the Assistant Uses row (<see cref="Assistant.ProviderChoices"/>):
/// its setting value, its name, whether it can be chosen, and why not.
/// </summary>
/// <param name="Id">The value of the setting assistant-provider.</param>
/// <param name="Name">The entry's name.</param>
/// <param name="Available">Whether it can be chosen.</param>
/// <param name="Problem">Why not; "" when it can.</param>
public sealed record ProviderChoice(string Id, string Name, bool Available = false, string Problem = "");
