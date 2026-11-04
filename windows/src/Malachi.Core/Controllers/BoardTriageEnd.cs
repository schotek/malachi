// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the tuple (trigger:, failure:) of macos/Sources/MalachiCore/
// Controllers/BoardTriageController.swift (lastEnded) and
// BoardAutoTriageScheduler.swift (BoardAutoTriageTarget.lastEnded); GTK:
// ui/internal/boardtriage/controller.go (Ended). A record where Swift has a
// tuple: one type for the controller, its schedule and their stand-ins.

using Malachi.Core.Boards;

namespace Malachi.Core.Controllers;

/// <summary>How a triage run ended: who started it and, when it failed, why.</summary>
/// <param name="Trigger">Who started it.</param>
/// <param name="Failure">Why it failed; null after a success.</param>
public sealed record BoardTriageEnd(Board.TriageTrigger Trigger, Board.TriageFailure? Failure);
