// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ContactsLookupController.swift;
// GTK: ui/internal/contactsctl lookup.go (Lookup, LookupKept,
// NormalAddress).
//
// The address chips' contacts item (W2): Show Contact for an address a
// visible book has exactly (contact.lookup, never by name), else Add to
// Contacts…. The lookup is asked once per shown message for all its header
// addresses (the key is the message's id, or any key the view uses) and
// kept for the Kept most recent messages; notify.contactsChanged forgets
// every answer (Invalidate), and the chips ask again.
//
// Windows: Swift's onChange is the event Changed with the key. One for the
// application: Ask when a message's header shows, Item for each chip's
// menu, Invalidate on every notify.contactsChanged.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Contacts;

namespace Malachi.Core.Controllers;

/// <summary>contactsctl.Lookup: which header addresses a visible book knows.</summary>
/// <remarks>Create and use it on the UI thread.</remarks>
public sealed class ContactsLookupController
{
    /// <summary>contactsctl.LookupKept: how many messages' answers are kept.</summary>
    public const int Kept = 50;

    private readonly Dictionary<string, Dictionary<string, ContactId>> answers = new(StringComparer.Ordinal);
    private readonly Dictionary<string, int> pending = new(StringComparer.Ordinal);
    private readonly Dictionary<string, Dictionary<string, ContactId>> partial = new(StringComparer.Ordinal);
    private readonly HashSet<string> failed = new(StringComparer.Ordinal);
    private readonly List<string> order = [];
    private int gen;

    /// <summary>A lookup over <paramref name="options"/>.</summary>
    public ContactsLookupController(ContactsOptions options)
    {
        ArgumentNullException.ThrowIfNull(options);
        Options = options;
    }

    /// <summary>The answer for a key arrived (the chips of that message rebuild their menus).</summary>
    public event EventHandler<string>? Changed;

    /// <summary>The shared options.</summary>
    public ContactsOptions Options { get; }

    /// <summary>contactsctl.NormalAddress: trimmed, lower case (contact.lookup's keys).</summary>
    public static string NormalAddress(string? a) => ContactBook.TrimSpace(a).ToLowerInvariant();

    /// <summary>
    /// Asks for the addresses of a message once (a key answered or on its
    /// way is not asked again), at most
    /// <see cref="API.Limits.MaxContactLookupAddresses"/> per call. A failed
    /// call stores nothing: not known is the safe answer, and the key is
    /// asked again next time.
    /// </summary>
    public void Ask(string key, IEnumerable<string> addresses)
    {
        ArgumentNullException.ThrowIfNull(key);
        ArgumentNullException.ThrowIfNull(addresses);
        if (answers.ContainsKey(key) || pending.ContainsKey(key))
        {
            return;
        }
        var addrs = new List<string>();
        foreach (var a in addresses)
        {
            var n = NormalAddress(a);
            if (n.Length > 0 && !addrs.Contains(n))
            {
                addrs.Add(n);
            }
        }
        if (addrs.Count == 0)
        {
            Store(key, []);
            return;
        }
        var g = gen;
        var chunks = addrs.Chunk(API.Limits.MaxContactLookupAddresses).ToList();
        pending[key] = chunks.Count;
        partial[key] = [];
        failed.Remove(key);
        foreach (var part in chunks)
        {
            Options.Call(API.ContactLookup, new ContactLookupParams { Addresses = part }, r =>
            {
                if (g != gen)
                {
                    return;
                }
                if (r.TryGetValue(out var res, out _))
                {
                    var into = partial.TryGetValue(key, out var p) ? p : partial[key] = [];
                    foreach (var (a, id) in res.Known)
                    {
                        into[NormalAddress(a)] = id;
                    }
                }
                else
                {
                    failed.Add(key);
                }
                var left = (pending.TryGetValue(key, out var l) ? l : 1) - 1;
                if (left > 0)
                {
                    pending[key] = left;
                    return;
                }
                pending.Remove(key);
                partial.Remove(key, out var known);
                if (failed.Remove(key))
                {
                    Changed?.Invoke(this, key);
                    return;
                }
                Store(key, known ?? []);
            });
        }
    }

    /// <summary>The contact of an address of <paramref name="key"/>'s message; Answered false before the answer.</summary>
    public (ContactId? Id, bool Answered) Known(string key, string address)
    {
        ArgumentNullException.ThrowIfNull(key);
        if (!answers.TryGetValue(key, out var m))
        {
            return (null, false);
        }
        return (m.TryGetValue(NormalAddress(address), out var id) ? id : (ContactId?)null, true);
    }

    /// <summary>
    /// The chip's contacts item for an address (<paramref name="books"/> are
    /// the window's or the preferences' addressBook.list; empty = no book can
    /// take a new contact).
    /// </summary>
    public (ContactBook.ChipItem Item, ContactId? Id) Item(string key, string address, IReadOnlyList<AddressBook> books)
    {
        var k = Known(key, address);
        if (!k.Answered)
        {
            return (ContactBook.ChipItem.None, null);
        }
        return (ContactBook.ChipItemFor(k.Id is not null, books), k.Id);
    }

    /// <summary>Forgets every answer (notify.contactsChanged); answers on their way are dropped.</summary>
    public void Invalidate()
    {
        gen++;
        answers.Clear();
        pending.Clear();
        partial.Clear();
        failed.Clear();
        order.Clear();
    }

    private void Store(string key, Dictionary<string, ContactId> known)
    {
        answers[key] = known;
        order.Remove(key);
        order.Add(key);
        while (order.Count > Kept)
        {
            answers.Remove(order[0]);
            order.RemoveAt(0);
        }
        Changed?.Invoke(this, key);
    }
}
