// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/MailModel+Threads.swift (Removal);
// GTK: ui/internal/window/thread_model.go (removal).

using System.Collections.Generic;

namespace Malachi.Core.Model;

/// <summary>What <see cref="MailModel.RemoveMessages"/> leaves behind for <see cref="MailModel.RestoreRemoval"/>.</summary>
/// <param name="Threads">The conversations it changed, in the order it changed them.</param>
public sealed record Removal(IReadOnlyList<ThreadSnapshot> Threads);
