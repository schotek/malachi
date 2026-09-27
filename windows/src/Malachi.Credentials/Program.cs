// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The keyring helper's entry point. Scaffold (phase B): it fails every
// operation with exit 1 and prints nothing. To the daemon any status but 0
// (done), 2 (not found) and 3 (malformed request) is a keyringError, so a
// daemon pointed at this build reports the error instead of losing a
// secret. The protocol of backend/internal/auth/helper lands in phase C4.

namespace Malachi.Credentials;

/// <summary>malachi-credentials get|set|delete.</summary>
internal static class Program
{
    /// <summary>The exit status of an operation that failed.</summary>
    internal const int Failed = 1;

    internal static int Main(string[] args) => Failed;
}
