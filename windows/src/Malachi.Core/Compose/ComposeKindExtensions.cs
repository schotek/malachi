// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Compose/ComposeParams.swift
// (ComposeKind.mode); GTK: ui/internal/compose/prefill.go (Kind.Mode).

using Malachi.Core.Api;

namespace Malachi.Core.Compose;

/// <summary>Swift's computed properties of <see cref="ComposeKind"/>.</summary>
public static class ComposeKindExtensions
{
    extension(ComposeKind kind)
    {
        /// <summary>compose.Kind.Mode: the <c>draft.create</c> mode of the kind.</summary>
        public ComposeMode Mode => kind switch
        {
            ComposeKind.Reply => ComposeMode.Reply,
            ComposeKind.ReplyAll => ComposeMode.ReplyAll,
            ComposeKind.Forward => ComposeMode.Forward,
            _ => ComposeMode.New,
        };
    }
}
