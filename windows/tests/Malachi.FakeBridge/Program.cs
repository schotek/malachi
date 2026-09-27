// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Scaffold (phase B): the scripted answers of malachi-mcp status, install
// and uninstall arrive with the MCP registration controller in phase D4.
// Until then it refuses to run.

using System;

namespace Malachi.FakeBridge;

internal static class Program
{
    private static int Main()
    {
        Console.Error.WriteLine("Malachi.FakeBridge: no script yet (docs/windows-port.md §15, phase D4)");
        return 2;
    }
}
