// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the MCP registration tests start a .NET stand-in bridge for
// every call, some of them at the same time. Run beside the rest of the
// suite, that load delays the process starts other tests time
// (BridgeRunnerTests' deadlines), so this collection runs on its own.

using Xunit;

namespace Malachi.Core.Tests.Controllers;

/// <summary>The collection of <see cref="McpRegistrationTests"/>: not run in parallel with any other.</summary>
[CollectionDefinition(DisableParallelization = true)]
public sealed class McpRegistrationProcesses;
