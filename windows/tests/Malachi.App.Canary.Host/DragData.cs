// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The DevTools protocol's Input.DragData (DragInput).

using System.Collections.Generic;

namespace Malachi.App.Canary.Host;

/// <summary>Input.DragData.</summary>
/// <param name="Items">Typed data (none).</param>
/// <param name="Files">The paths of the files.</param>
/// <param name="DragOperationsMask">1: copy.</param>
internal sealed record DragData(IReadOnlyList<string> Items, IReadOnlyList<string> Files, int DragOperationsMask);
