// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The stand-in claude's records as JSON, source generated as everywhere in
// the Windows client (no reflection-based serialisation).

using System.Collections.Generic;
using System.Text.Json.Serialization;

namespace Malachi.FakeClaude;

/// <summary>The JSON of the records: the arguments and the environment.</summary>
[JsonSerializable(typeof(string[]), TypeInfoPropertyName = "StringArray")]
[JsonSerializable(typeof(Dictionary<string, string>), TypeInfoPropertyName = "DictionaryStringString")]
internal sealed partial class FakeClaudeJson : JsonSerializerContext;
