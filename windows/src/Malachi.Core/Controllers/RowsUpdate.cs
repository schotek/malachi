// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the ([ListRow], SelectionHint) pair that
// macos/Sources/MalachiCore/Controllers/MailboxController+List.swift hands
// to onRows; GTK: ui/internal/window/threads.go (reconcileRows) and
// messages.go (rebuildMessageRows).

using System.Collections.Generic;
using Malachi.Core.Model;

namespace Malachi.Core.Controllers;

/// <summary>
/// A new snapshot of the list's rows and how the selection was reconciled
/// with it. The view applies the rows by key
/// (<see cref="Infrastructure.KeyedListSync"/>, <see cref="ListRow.Key"/>),
/// then mirrors <see cref="ListController.SelectedKey"/>, which is current
/// when the update arrives.
/// </summary>
/// <param name="Rows">Every row, in order; never changed afterwards.</param>
/// <param name="Hint">How the selection followed.</param>
public readonly record struct RowsUpdate(IReadOnlyList<ListRow> Rows, SelectionHint Hint);
