// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/MailModel+Threads.swift
// (ThreadSnapshot); GTK: ui/internal/window/thread_model.go
// (threadSnapshot). Immutable: its summary and members are records nothing
// changes, so the undo restores exactly what was removed.

using Malachi.Core.Api;

namespace Malachi.Core.Model;

/// <summary>One conversation's whole state, for undoing a removal.</summary>
/// <param name="Index">Its position in the list when it was taken.</param>
/// <param name="Summary">The conversation as it was.</param>
/// <param name="Members">Its members as they were, all known.</param>
/// <param name="Expanded">It was unfolded.</param>
/// <param name="Dropped">The removal took the last member; the row went away.</param>
public sealed record ThreadSnapshot(int Index, ThreadSummary Summary, ThreadMembers Members, bool Expanded, bool Dropped = false);
