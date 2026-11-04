// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Compose/ComposeParams.swift (ComposeKind);
// GTK: ui/internal/compose/prefill.go (Kind). Swift's computed property
// `mode` is the extension property Mode of ComposeKindExtensions.

namespace Malachi.Core.Compose;

/// <summary>compose.Kind: what the compose window was opened for.</summary>
public enum ComposeKind
{
    /// <summary>A new message.</summary>
    New,

    /// <summary>A reply to the sender.</summary>
    Reply,

    /// <summary>A reply to the sender and the other recipients.</summary>
    ReplyAll,

    /// <summary>A forward.</summary>
    Forward,

    /// <summary>A draft opened from the Drafts folder (<c>draft.open</c>).</summary>
    Edit,
}
