// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: what DaemonSupervisor.swift hands Foundation.Process in
// spawn (executableURL, arguments, environment), passed to the process host
// of the platform (IDaemonProcessHost) instead.

using System.Collections.Generic;

namespace Malachi.Core.Daemon;

/// <summary>One start of the daemon, as the supervisor asks the process host for it.</summary>
public sealed record DaemonStartInfo
{
    /// <summary>The executable.</summary>
    public required string Executable { get; init; }

    /// <summary>The arguments, each passed as one argument whatever it contains.</summary>
    public required IReadOnlyList<string> Arguments { get; init; }

    /// <summary>The whole environment of the process (<see cref="DaemonSupervisor.Environment"/>).</summary>
    public required IReadOnlyDictionary<string, string> Environment { get; init; }
}
