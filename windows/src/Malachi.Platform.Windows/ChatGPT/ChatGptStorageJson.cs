// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first SIWC storage, no existing Swift/Go counterpart. AOT-safe serialization.

using System.Text.Json.Serialization;
using Malachi.Core.ChatGPT;

namespace Malachi.Platform.Windows.ChatGPT;

internal sealed record CredentialGeneration(string Id, int Count, string Hash);

[JsonSerializable(typeof(ChatGptRegistration))]
[JsonSerializable(typeof(ChatGptTokens))]
[JsonSerializable(typeof(CredentialGeneration))]
internal sealed partial class ChatGptStorageJson : JsonSerializerContext;
