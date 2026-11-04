// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/contacts avatar.go (PhotoState.MayHave, Avatar,
// MayShowPhoto, NormalAddress, IsRecent, AvatarOfRow, AvatarOfDetail,
// AvatarOfSender, Monochrome, NearRows); Go-first (the macOS port is
// written alongside, P3 of the plan).
//
// Contact photos, read only (docs/plans/address-book.md §9, F1–F11). An
// avatar shows a contact's photo instead of its initials when there is
// one: in the Contacts window's list and detail (F9) and wherever mail
// draws a sender's avatar (F10: the message list with show-avatars, the
// conversation's cards, the chat's bubbles of Mail and the Board). The
// bytes come from the daemon only (re-encoded by it, F5) through
// ContactsPhotosController; the views decode them and clip the circle.
// Without a photo the avatar is what it was.
//
// Windows: Go's PhotoState is the contract's ContactPhotoAvailability (null
// is none, MayHave on the value); Go's (from, to) is a tuple.

using System;
using Malachi.Core.Api;

namespace Malachi.Core.Contacts;

public static partial class ContactBook
{
    /// <summary>contacts.PhotoState.MayHave: a state whose contact has, or may have, a photo (ready or remote); null and unknown values none.</summary>
    public static bool MayHavePhoto(ContactPhotoAvailability? state) => state is { MayHave: true };

    /// <summary>contacts.NormalAddress: an address as contact.lookup's keys are, trimmed and lower case.</summary>
    public static string NormalAddress(string? address) => TrimSpace(address).ToLowerInvariant();

    /// <summary>
    /// contacts.IsRecent: a contact of the recent book (its book, or the
    /// "r_" id contact.get takes): recent addresses never have a photo.
    /// </summary>
    public static bool IsRecent(AddressBookId? book, ContactId id) =>
        book == AddressBookId.Recent || (id.Value ?? "").StartsWith("r_", StringComparison.Ordinal);

    /// <summary>contacts.AvatarOfRow: the avatar of a row of the list (<see cref="Rows"/>) whose summary reports <paramref name="photo"/>.</summary>
    public static Avatar AvatarOfRow(Row row, ContactPhotoAvailability? photo)
    {
        ArgumentNullException.ThrowIfNull(row);
        var a = new Avatar(row.Initials);
        if (MayHavePhoto(photo) && row.Contact is { } c && !string.IsNullOrEmpty(c.Id.Value) && !IsRecent(c.BookId, c.Id))
        {
            a = a with { Contact = c.Id };
        }
        return a;
    }

    /// <summary>contacts.AvatarOfDetail: the avatar of the detail <paramref name="detail"/> of <paramref name="card"/>, whose card reports <paramref name="photo"/>.</summary>
    public static Avatar AvatarOfDetail(ContactCard card, ContactDetail detail, ContactPhotoAvailability? photo)
    {
        ArgumentNullException.ThrowIfNull(card);
        ArgumentNullException.ThrowIfNull(detail);
        var a = new Avatar(detail.Initials);
        if (MayHavePhoto(photo) && !string.IsNullOrEmpty(card.Id.Value) && !IsRecent(card.BookId, card.Id))
        {
            a = a with { Contact = card.Id };
        }
        return a;
    }

    /// <summary>
    /// contacts.AvatarOfSender: the avatar of a message's sender: initials
    /// of <paramref name="name"/> (else of the address) and the photo of the
    /// contact with exactly <paramref name="address"/>. The user's own
    /// messages (<paramref name="mine"/>: the row of a sent or outbox folder,
    /// a bubble on the right) never get a photo; the "You" avatar stays.
    /// </summary>
    public static Avatar AvatarOfSender(string? address, string? name, bool mine)
    {
        var a = new Avatar(Initials(name ?? "", address ?? ""));
        var n = NormalAddress(address);
        if (!mine && n.Contains('@', StringComparison.Ordinal))
        {
            a = a with { Sender = n };
        }
        return a;
    }

    /// <summary>contacts.Monochrome: monochrome-avatars greys initials only, never a photo shown.</summary>
    public static bool Monochrome(bool setting, bool photoShown) => setting && !photoShown;

    /// <summary>
    /// contacts.NearRows: the range [From, To) of rows whose photos are
    /// asked for: the visible rows <paramref name="first"/>..<paramref name="last"/>
    /// (inclusive) and as many again above and below, clamped to
    /// <paramref name="count"/> rows. Nothing visible (last &lt; first) is an
    /// empty range.
    /// </summary>
    public static (int From, int To) NearRows(int first, int last, int count)
    {
        if (count <= 0 || last < first)
        {
            return (0, 0);
        }
        first = Math.Max(first, 0);
        last = Math.Min(last, count - 1);
        if (last < first)
        {
            return (0, 0);
        }
        var span = last - first + 1;
        return (Math.Max(first - span, 0), Math.Min(last + span + 1, count));
    }

    /// <summary>
    /// contacts.Avatar: what an avatar shows: <see cref="Initials"/>, and the
    /// photo of <see cref="Contact"/> (by id, the Contacts window) or of
    /// <see cref="Sender"/>'s contact (by address, mail) when the photos
    /// controller has it. Neither: initials only, never a photo.
    /// </summary>
    /// <param name="Initials">The letters without a photo.</param>
    public sealed record Avatar(string Initials)
    {
        /// <summary>The contact whose photo replaces the initials; null none.</summary>
        public ContactId? Contact { get; init; }

        /// <summary>
        /// The From address (<see cref="NormalAddress"/>) whose contact's
        /// photo replaces the initials; matched only by the exact address,
        /// never by name (F10). "" none.
        /// </summary>
        public string Sender { get; init; } = "";

        /// <summary>contacts.Avatar.MayShowPhoto: an avatar that asks the photos controller.</summary>
        public bool MayShowPhoto => Contact is not null || Sender.Length > 0;
    }
}
