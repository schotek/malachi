// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Compose/ComposeParams.swift
// (ComposeParams); GTK: ui/internal/compose/prefill.go (Params).
//
// Swift's == compares the lists by value; so does Equals here (a record
// would compare them by reference).

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using Malachi.Core.Api;

namespace Malachi.Core.Compose;

/// <summary>compose.Params: opens a compose window with these fields prefilled.</summary>
public sealed record ComposeParams
{
    /// <summary>What the window is opened for.</summary>
    public ComposeKind Kind { get; init; }

    /// <summary>Preselects the From identity; null means the first account.</summary>
    public AccountId? AccountId { get; init; }

    /// <summary>The To row.</summary>
    public IReadOnlyList<Address> To { get; init => field = value ?? []; } = [];

    /// <summary>The Cc row.</summary>
    public IReadOnlyList<Address> Cc { get; init => field = value ?? []; } = [];

    /// <summary>The Bcc row.</summary>
    public IReadOnlyList<Address> Bcc { get; init => field = value ?? []; } = [];

    /// <summary>The subject.</summary>
    public string Subject { get; init => field = value ?? ""; } = "";

    /// <summary>
    /// Inserted into the editor document verbatim and therefore already safe:
    /// only the daemon (<see cref="Prefill.FromDraft"/>),
    /// <see cref="Prefill.Create"/> and <see cref="Mailto.ParseMailto"/>
    /// produce it.
    /// </summary>
    public string BodyHtml { get; init => field = value ?? ""; } = "";

    /// <summary>The message a reply answers.</summary>
    public MessageId? InReplyTo { get; init; }

    /// <summary>The message a forward carries.</summary>
    public MessageId? Forwarding { get; init; }

    /// <summary>
    /// What the daemon imported for the draft (the quoted original's
    /// pictures, a forwarded message's files): not yet bound, the first
    /// <c>draft.save</c> binds them.
    /// </summary>
    public IReadOnlyList<DraftAttachment> Attachments { get; init => field = value ?? []; } = [];

    /// <summary>What the daemon's sanitiser removed from the quoted original; the window says so once.</summary>
    public BlockedContent Blocked { get; init => field = value ?? new(); } = new();

    /// <summary>The saved draft the window edits (<see cref="ComposeKind.Edit"/> from <c>draft.open</c>); null for a new one.</summary>
    public DraftId? DraftId { get; init; }

    /// <summary>The version of <see cref="DraftId"/>.</summary>
    public int Version { get; init; }

    /// <summary>The Drafts message the first save takes over (<c>draft.open</c> sets it).</summary>
    public MessageId? Replaces { get; init; }

    /// <summary>
    /// How many parts of the original <c>draft.create</c> could not import
    /// (over a cap, unreadable, or kept on the mail server); the window says
    /// so once (compose.Params <c>Skipped</c>).
    /// </summary>
    public int Skipped { get; init; }

    /// <inheritdoc/>
    public bool Equals(ComposeParams? other) =>
        other is not null && Kind == other.Kind && AccountId == other.AccountId
        && To.SequenceEqual(other.To) && Cc.SequenceEqual(other.Cc) && Bcc.SequenceEqual(other.Bcc)
        && string.Equals(Subject, other.Subject, StringComparison.Ordinal)
        && string.Equals(BodyHtml, other.BodyHtml, StringComparison.Ordinal)
        && InReplyTo == other.InReplyTo && Forwarding == other.Forwarding
        && Attachments.SequenceEqual(other.Attachments) && Blocked == other.Blocked
        && DraftId == other.DraftId && Version == other.Version && Replaces == other.Replaces
        && Skipped == other.Skipped;

    /// <inheritdoc/>
    public override int GetHashCode() => HashCode.Combine(Kind, AccountId, To.Count, Subject, BodyHtml, DraftId, Version);

    /// <summary>
    /// The kind, the ids and the sizes of the rest: never an address, the
    /// subject or the body (docs/windows-port.md §3.1: no mail content in
    /// logs).
    /// </summary>
    public override string ToString() => string.Create(
        CultureInfo.InvariantCulture,
        $"ComposeParams(kind: {Kind}, accountId: {Id(AccountId)}, to: {To.Count}, cc: {Cc.Count}, bcc: {Bcc.Count}, "
        + $"subject: {Subject.Length} chars, bodyHtml: {BodyHtml.Length} chars, inReplyTo: {Id(InReplyTo)}, "
        + $"forwarding: {Id(Forwarding)}, attachments: {Attachments.Count}, draftId: {Id(DraftId)}, version: {Version}, "
        + $"replaces: {Id(Replaces)}, skipped: {Skipped})");

    private static string Id<T>(T? id)
        where T : struct => id is { } value ? value.ToString() ?? "" : "null";
}
