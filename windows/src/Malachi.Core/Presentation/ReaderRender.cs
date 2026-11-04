// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the arguments of MessageViewController.swift's onRender
// (MessageSummary, LoadedMessage?). Windows-only type: a C# event carries
// one argument.

using Malachi.Core.Api;
using Malachi.Core.Model;

namespace Malachi.Core.Presentation;

/// <summary>What a message view rendered: the summary it was given and what the cache held.</summary>
/// <param name="Summary">The message on display.</param>
/// <param name="Loaded">Its cache entry so far; null before any answer.</param>
public readonly record struct ReaderRender(MessageSummary Summary, LoadedMessage? Loaded);
