// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Daemon/DaemonSupervisor.swift
// (SupervisorError's cases); GTK: ui/internal/daemon/daemon.go (ErrNoDaemon
// and the errors of Ensure).

namespace Malachi.Core.Daemon;

/// <summary>Why <see cref="DaemonSupervisor"/> has no daemon to offer.</summary>
public enum DaemonSupervisorFailure
{
    /// <summary>Nothing answers and there is no daemon to start (SupervisorError.noDaemon).</summary>
    NoDaemon,

    /// <summary>The daemon kept exiting; the next start waits (SupervisorError.backoff).</summary>
    Backoff,

    /// <summary>The daemon exited before its socket answered (SupervisorError.exitedEarly).</summary>
    ExitedEarly,

    /// <summary>The daemon runs but its socket did not answer in time (SupervisorError.startTimeout).</summary>
    StartTimeout,

    /// <summary>The application is quitting; nothing is started any more (SupervisorError.stopping).</summary>
    Stopping,
}
