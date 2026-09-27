// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Scaffold (phase B): the stand-in daemon's modes (listen, slow, exit, deaf)
// arrive with the supervisor in phase C6. Until then it refuses to run.

using System;

namespace Malachi.Core.TestDaemon;

internal static class Program
{
    private static int Main()
    {
        Console.Error.WriteLine("Malachi.Core.TestDaemon: no modes yet (docs/windows-port.md §15, phase C6)");
        return 2;
    }
}
