// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ComposeDraftController.swift
// (DraftState); GTK: ui/internal/compose/draft.go (draftState).
//
// Swift's mutable struct is an immutable record here, changed with `with`:
// ComposeDraftController.Draft is replaced whole, so every change is one
// PropertyChanged and a copy a caller holds never changes under it (the
// value semantics of the Swift struct). GTK keeps the autosave source, the
// queued callbacks and the flush echo in the same struct; they are the
// controller's private fields here, as on macOS.

using System;
using Malachi.Core.Api;

namespace Malachi.Core.Controllers;

/// <summary>compose.draftState: the lifecycle of the draft behind a compose window.</summary>
public sealed record DraftState
{
    /// <summary>Null until the first successful save (Go's "").</summary>
    public DraftId? DraftId { get; init; }

    /// <summary>The stored version the next save is based on.</summary>
    public int Version { get; init; }

    /// <summary>The message a reply answers.</summary>
    public MessageId? InReplyTo { get; init; }

    /// <summary>
    /// The issue of a comment draft as <c>draft.create</c> returned it
    /// (<see cref="Api.Draft.Comment"/>), sent back with the form's
    /// visibility; null for an e-mail.
    /// </summary>
    public DraftComment? Comment { get; init; }

    /// <summary>The message a forward carries.</summary>
    public MessageId? Forwarding { get; init; }

    /// <summary>
    /// The Drafts message the first save takes over (<c>draft.open</c>);
    /// cleared once a save went through.
    /// </summary>
    public MessageId? Replaces { get; init; }

    /// <summary>
    /// The draft is the user's to keep: saved with Ctrl+S, the menu or the
    /// close question, or opened from the Drafts folder. Until then Discard in
    /// the close question deletes what the autosave stored, which would
    /// otherwise live on in the Drafts folder.
    /// </summary>
    public bool ExplicitSave { get; init; }

    /// <summary>Edits not yet persisted.</summary>
    public bool Dirty { get; init; }

    /// <summary><c>draft.save</c> in flight.</summary>
    public bool Saving { get; init; }

    /// <summary>Sending: the save and <c>message.send</c> in flight.</summary>
    public bool Sending { get; init; }

    /// <summary>When the last save went through; null before the first.</summary>
    public DateTimeOffset? LastSaved { get; init; }

    /// <summary>The last autosave error shown as a toast.</summary>
    public string LastError { get; init => field = value ?? ""; } = "";

    /// <summary>The window is gone; late callbacks are dropped.</summary>
    public bool Closed { get; init; }

    /// <summary>Close without asking.</summary>
    public bool Discard { get; init; }
}
