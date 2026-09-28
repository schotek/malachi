// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The JSON of the DevTools protocol calls the host makes, source generated.

using System.Text.Json.Serialization;

namespace Malachi.App.Canary.Host;

/// <summary>The serializer context of the host's DevTools protocol parameters.</summary>
[JsonSourceGenerationOptions(PropertyNamingPolicy = JsonKnownNamingPolicy.CamelCase)]
[JsonSerializable(typeof(TextInput))]
[JsonSerializable(typeof(DragInput))]
[JsonSerializable(typeof(string))]
internal sealed partial class DevToolsJson : JsonSerializerContext;
