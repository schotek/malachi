// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/Assistant.swift
// (Assistant.Availability); GTK: ui/internal/assistant (Availability).

namespace Malachi.Core.Assistants;

/// <summary>
/// Whether a target can be used. <see cref="Handler"/>: an app handles its
/// link scheme; <see cref="Registered"/>: the malachi-mcp bridge is
/// registered in that client. For the panel (In App), <see cref="Handler"/>
/// is that the claude executable was found and the bridge is beside the
/// application, <see cref="Registered"/> that the bridge is registered in at
/// least one client (Assistant.Shown still requires the registration).
/// </summary>
/// <param name="Handler">An app handles the target's links (for In App: Claude Code and the bridge were found).</param>
/// <param name="Registered">The bridge is registered in the target's client (for In App: in any client).</param>
public readonly record struct AssistantAvailability(bool Handler = false, bool Registered = false);
