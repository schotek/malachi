// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Daemon/DaemonSupervisor.swift
// (DaemonSupervisor.Launch); GTK: ui/internal/daemon/daemon.go (Supervisor
// Path and Socket). A type of its own, not nested as in Swift, the way the
// port keeps public types. keychainHelper is KeyringHelper here: the helper
// is malachi-credentials.exe over Credential Manager.

namespace Malachi.Core.Daemon;

/// <summary>
/// How the daemon is launched. The arguments go to the process as a list,
/// so spaces in the paths need no quoting.
/// </summary>
public sealed record DaemonLaunch
{
    /// <summary>The daemon's executable (<see cref="DaemonSupervisor.Locate()"/>).</summary>
    public required string Executable { get; init; }

    /// <summary><c>--socket</c>: the daemon listens exactly where the client dials.</summary>
    public required string Socket { get; init; }

    /// <summary><c>--config</c>.</summary>
    public required string Config { get; init; }

    /// <summary><c>--store</c>.</summary>
    public required string Store { get; init; }

    /// <summary>
    /// The bundled <c>malachi-credentials.exe</c> (<see cref="Paths.KeyringHelper"/>),
    /// which the daemon gets as its keyring; null runs it without one.
    /// </summary>
    public string? KeyringHelper { get; init; }
}
