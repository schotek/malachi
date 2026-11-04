// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: the System.Text.Json source-generated context of the
// malachi-mcp --json report (docs/mcp.md), which Swift reads with
// JSONDecoder. One context per contract (docs/windows-port.md §3.1): the
// report is the bridge's, not the daemon's, so it is not in ApiJsonContext.

using System.Text.Json.Serialization;

namespace Malachi.Core.Controllers;

/// <summary>
/// The metadata of the bridge's report: a null in a member that is not
/// nullable fails the decoding, as it fails Swift's; unknown members are
/// ignored.
/// </summary>
[JsonSourceGenerationOptions(
    DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
    RespectNullableAnnotations = true)]
[JsonSerializable(typeof(McpStatus))]
[JsonSerializable(typeof(McpClient))]
internal sealed partial class McpJsonContext : JsonSerializerContext;
