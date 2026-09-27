// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/SearchModel.swift (SearchHit);
// GTK: ui/internal/window/search_model.go (searchHit).

using System.Collections.Generic;
using Malachi.Core.Api;

namespace Malachi.Core.Model;

/// <summary>What a result shows beyond its summary: the excerpt around the match and the matched words in it.</summary>
/// <param name="Snippet">The excerpt, plain text.</param>
/// <param name="Ranges">The matched words, UTF-8 byte ranges into <paramref name="Snippet"/>.</param>
public sealed record SearchHit(string Snippet, IReadOnlyList<MatchRange> Ranges);
