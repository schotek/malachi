// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The JSON of the canary's configuration and results, source generated as
// everywhere in the client (docs/windows-port.md §3.1).

using System.Text.Json.Serialization;

namespace Malachi.App.Canary.Host;

/// <summary>The serializer context of <see cref="HostConfig"/> and <see cref="HostResults"/>.</summary>
[JsonSourceGenerationOptions(PropertyNamingPolicy = JsonKnownNamingPolicy.CamelCase, WriteIndented = true,
    DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull)]
[JsonSerializable(typeof(HostConfig))]
[JsonSerializable(typeof(HostResults))]
public sealed partial class CanaryJson : JsonSerializerContext;
