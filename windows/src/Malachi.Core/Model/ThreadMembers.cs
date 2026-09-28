// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/MailModel+Threads.swift
// (ThreadMembers); GTK: ui/internal/window/thread_model.go (threadMembers).
// Immutable, and so is its list: the model replaces both (with), so the
// members a snapshot (ThreadSnapshot) keeps cannot change under it, as a
// Swift value cannot. Equality compares List by reference.

using System.Collections.Generic;
using Malachi.Core.Api;

namespace Malachi.Core.Model;

/// <summary>
/// What the window knows of one conversation's folder members, oldest
/// first: only the newest one (from thread.list) until thread.get answers,
/// then all of them. The waiters of the Go struct (run once the members are
/// known) live in the controller.
/// </summary>
/// <param name="List">The members known, oldest first; never changed once built.</param>
/// <param name="Complete">Every folder member is known.</param>
/// <param name="Fetching">thread.get is in flight.</param>
public sealed record ThreadMembers(IReadOnlyList<MessageSummary> List, bool Complete, bool Fetching = false);
