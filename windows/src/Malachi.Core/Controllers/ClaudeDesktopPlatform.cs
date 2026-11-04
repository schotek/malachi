// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ClaudeDesktopController.swift
// (ClaudeDesktopController.Platform); no GTK counterpart. The application
// answers it with Malachi.Platform.Windows.Claude.ClaudeDesktopApp, the
// tests with a fake.

using System;
using System.Threading;
using System.Threading.Tasks;

namespace Malachi.Core.Controllers;

/// <summary>What the application does to Claude Desktop, for <see cref="ClaudeDesktopController"/>.</summary>
/// <param name="IsRunning">Whether Claude Desktop runs now.</param>
/// <param name="Quit">
/// Asks Claude Desktop to quit and waits until it has, at most the given
/// time; true when it has (or did not run at all).
/// </param>
/// <param name="Launch">Starts Claude Desktop.</param>
public sealed record ClaudeDesktopPlatform(
    Func<bool> IsRunning,
    Func<TimeSpan, CancellationToken, Task<bool>> Quit,
    Action Launch);
