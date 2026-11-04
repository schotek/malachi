// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/NotifiedMessages.swift
// (NotifiedEntry); GTK: ui/internal/window/notified.go (notifiedEntry).

using Malachi.Core.Api;

namespace Malachi.Core.Model;

/// <summary>One notified message and the folder it arrived in (<see cref="NotifiedMessages"/>).</summary>
/// <param name="Id">The message.</param>
/// <param name="Key">The folder it was notified in.</param>
public readonly record struct NotifiedEntry(MessageId Id, FolderKey Key);
