// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of unified.Group[T] (ui/internal/unified/unified.go).

using System.Collections.Generic;
using Malachi.Core.Api;

namespace Malachi.Core.Model;

/// <summary>The items of one account, in their order (<see cref="Unified.GroupByAccount{T}"/>).</summary>
/// <typeparam name="T">The item: a selected row, a message id with its account.</typeparam>
/// <param name="Account">The account.</param>
/// <param name="Items">Its items.</param>
public sealed record UnifiedGroup<T>(AccountId Account, IReadOnlyList<T> Items);
