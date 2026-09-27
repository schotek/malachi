// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Scaffold (phase B): the generated P/Invoke of Malachi.Platform.Windows
// reaches Win32 and returns what .NET itself reports.

using System;
using Xunit;

namespace Malachi.Platform.Windows.Tests;

public sealed class NativeProbeTests
{
    [Fact]
    public void CurrentProcessIdMatchesTheRuntime()
    {
        Assert.Equal((uint)Environment.ProcessId, NativeProbe.CurrentProcessId);
    }
}
