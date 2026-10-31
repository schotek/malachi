// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/contactsctl photos.go (Photos, Photo, ContactRef,
// ContactKey, SenderKey, MaxPhotos, MaxPhotoFetches, LookupDelay,
// PhotoTimeout); Go-first (the macOS port is written alongside, P3 of
// docs/plans/address-book.md §9.3).
//
// Contact photos (docs/plans/address-book.md §9, F8–F10): one controller
// for the application, shared by the views that draw avatars. Each view
// tells it what it shows near its viewport (Show: contacts by id for the
// Contacts window, sender addresses for mail); the controller asks the
// daemon only for those:
//
//   - senders: contact.lookup {withPhotos} for the addresses it has no
//     answer for, gathered for LookupDelay (ContactsOptions.After) and sent
//     in batches of API.Limits.MaxContactLookupAddresses; every answer is
//     kept by address, also "no contact with a photo";
//   - photos: contact.photo for a contact whose photo is ready or remote
//     only while a view shows it, nearest first, at most MaxPhotoFetches at
//     a time; "none" is remembered;
//   - the bytes (re-encoded by the daemon, a JPEG or PNG of at most 256 px)
//     of the MaxPhotos most recently used photos are kept; the views decode
//     them (and may keep their decoded image by Photo.Key).
//
// notify.contactsChanged (Changed) forgets what the changed books may have
// changed and asks again for what is shown; a view redraws on
// PhotoChanged. A failed call is not repeated until the next change.
//
// Windows: Go's OnPhoto callback is the event PhotoChanged with the key;
// Go's (Photo, bool) results are nullable. The debounce is
// ContactsOptions.After even in the tests (their timer), so a lookup is
// never sent from within Show. Nothing is logged but the method and the
// error code (ContactsOptions), never an address.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Contacts;
using Malachi.Core.Controllers.Infrastructure;

namespace Malachi.Core.Controllers;

/// <summary>contactsctl.Photos: the photo cache of the application.</summary>
/// <remarks>Create and use it on the UI thread.</remarks>
public sealed class ContactsPhotosController
{
    /// <summary>contactsctl.MaxPhotos: how many photos' bytes are kept (F9).</summary>
    public const int MaxPhotos = 200;

    /// <summary>contactsctl.MaxPhotoFetches: how many contact.photo calls run at once (F3).</summary>
    public const int MaxPhotoFetches = 4;

    /// <summary>contactsctl.LookupDelay: gathers the senders of a scrolled list into one contact.lookup.</summary>
    public static readonly TimeSpan LookupDelay = TimeSpan.FromMilliseconds(150);

    private readonly Dictionary<string, PhotoView> views = new(StringComparer.Ordinal);
    private readonly Dictionary<string, SenderEntry> senders = new(StringComparer.Ordinal); // by normal address
    private readonly List<string> queued = []; // addresses waiting for the lookup
    private readonly HashSet<string> asking = new(StringComparer.Ordinal); // addresses of lookups on their way
    private readonly Dictionary<ContactId, ContactPhotoAvailability?> states = []; // from refs and lookups
    private readonly Dictionary<ContactId, AddressBookId> books = []; // known books of contacts
    private readonly Dictionary<ContactId, Photo> cache = [];
    private readonly List<ContactId> order = []; // LRU, most recent last
    private readonly HashSet<ContactId> none = [];
    private readonly HashSet<ContactId> stale = [];
    private readonly HashSet<ContactId> failed = [];
    private readonly HashSet<ContactId> fetching = [];
    private bool lookupAt; // a flush is scheduled
    private int gen;

    /// <summary>The cache over <paramref name="options"/>.</summary>
    public ContactsPhotosController(ContactsOptions options)
    {
        ArgumentNullException.ThrowIfNull(options);
        Options = options;
    }

    /// <summary>
    /// contactsctl.Photos.OnPhoto: what the avatars of a key
    /// (<see cref="ContactKey"/>, <see cref="SenderKey"/>) show changed (a
    /// photo arrived, changed or went away); they redraw.
    /// </summary>
    public event EventHandler<string>? PhotoChanged;

    /// <summary>The shared options.</summary>
    public ContactsOptions Options { get; }

    /// <summary>How many photos' bytes are kept now (at most <see cref="MaxPhotos"/> but those shown).</summary>
    public int CachedCount => cache.Count;

    /// <summary>contactsctl.ContactKey: the key of a contact's avatars (by id).</summary>
    public static string ContactKey(ContactId id) => "contact:" + id.Value;

    /// <summary>contactsctl.SenderKey: the key of a sender's avatars (by address).</summary>
    public static string SenderKey(string address) => "sender:" + ContactBook.NormalAddress(address);

    /// <summary>
    /// contactsctl.Photos.Show: sets what <paramref name="view"/> (any name:
    /// "contacts", "list", "conversation", …) shows near its viewport,
    /// nearest first: contacts by id with the state their summary or card
    /// reports (a ref without a photo is ignored) and sender addresses
    /// (<see cref="ContactBook.Avatar.Sender"/>). It replaces the view's
    /// previous set; <c>Show(view, null, null)</c> when the view closes.
    /// </summary>
    public void Show(string view, IEnumerable<ContactRef>? refs, IEnumerable<string>? addresses)
    {
        ArgumentNullException.ThrowIfNull(view);
        var contacts = new List<ContactId>();
        var shown = new List<string>();
        foreach (var r in refs ?? [])
        {
            if (string.IsNullOrEmpty(r.Id.Value) || ContactBook.IsRecent(r.Book, r.Id))
            {
                continue;
            }
            if (r.Book is { Value.Length: > 0 } b)
            {
                books[r.Id] = b;
            }
            var state = Norm(r.State);
            if (states.TryGetValue(r.Id, out var prev) && prev != state && (cache.ContainsKey(r.Id) || none.Contains(r.Id)))
            {
                // The list reports a change the cache may not know of yet.
                stale.Add(r.Id);
            }
            states[r.Id] = state;
            if (ContactBook.MayHavePhoto(state) && !contacts.Contains(r.Id))
            {
                contacts.Add(r.Id);
            }
        }
        foreach (var s in addresses ?? [])
        {
            var n = ContactBook.NormalAddress(s);
            if (n.Length == 0 || shown.Contains(n))
            {
                continue;
            }
            shown.Add(n);
        }
        if (contacts.Count == 0 && shown.Count == 0)
        {
            views.Remove(view);
        }
        else
        {
            views[view] = new PhotoView(contacts, shown);
        }
        QueueSenders(shown);
        ScheduleLookup();
        Pump();
    }

    /// <summary>contactsctl.Photos.ForContact: the photo of a contact's avatar; null shows initials.</summary>
    public Photo? ForContact(ContactId id)
    {
        if (!cache.TryGetValue(id, out var ph))
        {
            return null;
        }
        Touch(id);
        return ph;
    }

    /// <summary>contactsctl.Photos.ForSender: the photo of a sender's avatar (<see cref="ContactBook.Avatar.Sender"/>); null shows initials.</summary>
    public Photo? ForSender(string address)
    {
        if (!senders.TryGetValue(ContactBook.NormalAddress(address), out var e) || e.Contact is not { } c)
        {
            return null;
        }
        return ForContact(c);
    }

    /// <summary>contactsctl.Photos.For: the photo of an avatar; null shows initials.</summary>
    public Photo? For(ContactBook.Avatar avatar)
    {
        ArgumentNullException.ThrowIfNull(avatar);
        if (avatar.Contact is { } c)
        {
            return ForContact(c);
        }
        return avatar.Sender.Length > 0 ? ForSender(avatar.Sender) : null;
    }

    /// <summary>
    /// contactsctl.Photos.Changed (notify.contactsChanged): the answers the
    /// changed books may have changed are asked for again (every sender's,
    /// as lookups name no book, every "no photo"; all of them when the
    /// notification names no book) for what the views show; until the new
    /// answer the old one stays on screen. A change of the recent book only
    /// changes nothing (recent contacts have no photos).
    /// </summary>
    public void Changed(ContactsChangedNotification n)
    {
        ArgumentNullException.ThrowIfNull(n);
        if (n.AccountId is null && n.BookIds.Count > 0 && !n.BookIds.Any(b => b != AddressBookId.Recent))
        {
            return;
        }
        bool Affected(ContactId id) => n.BookIds.Count == 0 || !books.TryGetValue(id, out var b) || n.BookIds.Contains(b);
        gen++;
        asking.Clear();
        // Photos on their way still count against MaxPhotoFetches; their
        // answers are dropped and the contacts asked again.
        foreach (var id in fetching.Where(Affected).ToList())
        {
            stale.Add(id);
        }
        foreach (var (a, e) in senders.ToList())
        {
            // A contact may have been added to any book: "no contact" is
            // always asked again.
            if (e.Contact is not { } c || Affected(c))
            {
                senders[a] = e with { Stale = true };
            }
        }
        none.RemoveWhere(Affected);
        failed.RemoveWhere(Affected);
        foreach (var id in cache.Keys.Where(Affected).ToList())
        {
            stale.Add(id);
        }
        queued.Clear();
        foreach (var name in views.Keys.Order(StringComparer.Ordinal).ToList())
        {
            QueueSenders(views[name].Senders);
        }
        ScheduleLookup();
        Pump();
    }

    /// <summary>contactsctl.Photos.Reset: forgets everything (the daemon restarted); the views Show again.</summary>
    public void Reset()
    {
        gen++;
        queued.Clear();
        views.Clear();
        senders.Clear();
        asking.Clear();
        states.Clear();
        books.Clear();
        cache.Clear();
        order.Clear();
        none.Clear();
        stale.Clear();
        failed.Clear();
        fetching.Clear();
    }

    // --- lookups -----------------------------------------------------------

    private static ContactPhotoAvailability? Norm(ContactPhotoAvailability? s) => s is { Value.Length: > 0 } ? s : null;

    // Queues the addresses without a current answer that are not on their way.
    private void QueueSenders(IEnumerable<string> addrs)
    {
        foreach (var a in addrs)
        {
            if ((senders.TryGetValue(a, out var e) && !e.Stale) || asking.Contains(a) || queued.Contains(a))
            {
                continue;
            }
            queued.Add(a);
        }
    }

    private void ScheduleLookup()
    {
        if (queued.Count == 0 || lookupAt)
        {
            return;
        }
        lookupAt = true;
        Options.After(LookupDelay, () =>
        {
            lookupAt = false;
            Flush();
        });
    }

    private void Flush()
    {
        var shown = ShownSenders();
        // Only what a view still shows.
        var addrs = queued.Where(shown.Contains).ToList();
        queued.Clear();
        foreach (var chunk in addrs.Chunk(API.Limits.MaxContactLookupAddresses))
        {
            var part = chunk;
            foreach (var a in part)
            {
                asking.Add(a);
            }
            var g = gen;
            Options.Call(API.ContactLookup, new ContactLookupParams { Addresses = part, WithPhotos = true }, o => Answered(g, part, o));
        }
    }

    private void Answered(int g, string[] asked, Outcome<ContactLookupResult> outcome)
    {
        if (g != gen)
        {
            return;
        }
        var ok = outcome.TryGetValue(out var r, out _);
        var got = new Dictionary<string, ContactPhotoRef>(StringComparer.Ordinal);
        if (ok && r.Photos is { } photos)
        {
            foreach (var (a, rf) in photos)
            {
                got[ContactBook.NormalAddress(a)] = rf;
            }
        }
        foreach (var a in asked)
        {
            asking.Remove(a);
            var had = senders.TryGetValue(a, out var prev);
            SenderEntry next;
            if (!ok)
            {
                next = new SenderEntry(null, null, Failed: true);
            }
            else if (got.TryGetValue(a, out var rf) && rf.State.MayHave && !string.IsNullOrEmpty(rf.ContactId.Value)
                && !ContactBook.IsRecent(null, rf.ContactId))
            {
                next = new SenderEntry(rf.ContactId, rf.State);
                if (states.TryGetValue(rf.ContactId, out var old) && old != rf.State
                    && (cache.ContainsKey(rf.ContactId) || none.Contains(rf.ContactId)))
                {
                    stale.Add(rf.ContactId);
                }
                states[rf.ContactId] = rf.State;
            }
            else
            {
                next = new SenderEntry(null, null);
            }
            senders[a] = next;
            if (had && prev.Contact != next.Contact)
            {
                Report(SenderKey(a));
            }
        }
        Pump();
    }

    private HashSet<string> ShownSenders()
    {
        var output = new HashSet<string>(StringComparer.Ordinal);
        foreach (var v in views.Values)
        {
            output.UnionWith(v.Senders);
        }
        return output;
    }

    // --- photos ------------------------------------------------------------

    // The contacts whose photo a view shows and the cache lacks (or holds
    // stale), nearest first.
    private List<ContactId> Wanted()
    {
        var output = new List<ContactId>();
        foreach (var id in ShownContacts())
        {
            if (fetching.Contains(id) || failed.Contains(id))
            {
                continue;
            }
            if ((cache.ContainsKey(id) || none.Contains(id)) && !stale.Contains(id))
            {
                continue;
            }
            output.Add(id);
        }
        return output;
    }

    // The contacts with a photo the views show, in their order (each view's
    // nearest first, views by name), at most MaxPhotos: a photo shown is
    // never evicted for another one shown.
    private List<ContactId> ShownContacts()
    {
        var output = new List<ContactId>();
        void Add(ContactId? id)
        {
            if (id is { } c && !string.IsNullOrEmpty(c.Value) && output.Count < MaxPhotos
                && states.TryGetValue(c, out var s) && ContactBook.MayHavePhoto(s) && !output.Contains(c))
            {
                output.Add(c);
            }
        }
        foreach (var name in views.Keys.Order(StringComparer.Ordinal))
        {
            var v = views[name];
            foreach (var id in v.Contacts)
            {
                Add(id);
            }
            foreach (var a in v.Senders)
            {
                Add(senders.TryGetValue(a, out var e) ? e.Contact : null);
            }
        }
        return output;
    }

    private void Pump()
    {
        foreach (var id in Wanted())
        {
            if (fetching.Count >= MaxPhotoFetches)
            {
                return;
            }
            Fetch(id);
        }
    }

    private void Fetch(ContactId id)
    {
        fetching.Add(id);
        var g = gen;
        Options.Call(API.ContactPhoto, new ContactPhotoParams { ContactId = id }, o =>
        {
            fetching.Remove(id);
            if (g == gen)
            {
                Fetched(id, o);
            }
            Pump();
        });
    }

    private void Fetched(ContactId id, Outcome<ContactPhotoResult> outcome)
    {
        stale.Remove(id);
        var had = cache.TryGetValue(id, out var old);
        if (!outcome.TryGetValue(out var r, out _))
        {
            // Keep what is shown; not asked again until a change.
            failed.Add(id);
            return;
        }
        if (r.State.Value != ContactPhotoState.Ready || r.Data is not { Length: > 0 } data)
        {
            none.Add(id);
            Drop(id);
            if (had)
            {
                ReportContact(id);
            }
            return;
        }
        none.Remove(id);
        var ph = new Photo(id, r.Revision, r.Mime ?? "", data);
        cache[id] = ph;
        Touch(id);
        if (order.Count > MaxPhotos)
        {
            // The least recently used photo no view shows.
            var shown = ShownContacts();
            for (var i = 0; i < order.Count && order.Count > MaxPhotos;)
            {
                var c = order[i];
                if (!shown.Contains(c))
                {
                    Drop(c);
                    continue;
                }
                i++;
            }
        }
        if (!had || old!.Key != ph.Key || !old.Data.AsSpan().SequenceEqual(ph.Data))
        {
            ReportContact(id);
        }
    }

    private void Touch(ContactId id)
    {
        order.Remove(id);
        order.Add(id);
    }

    private void Drop(ContactId id)
    {
        cache.Remove(id);
        order.Remove(id);
    }

    // The contact's key and the key of every sender whose contact it is.
    private void ReportContact(ContactId id)
    {
        Report(ContactKey(id));
        foreach (var a in senders.Where(e => e.Value.Contact == id).Select(e => e.Key).Order(StringComparer.Ordinal).ToList())
        {
            Report(SenderKey(a));
        }
    }

    private void Report(string key) => PhotoChanged?.Invoke(this, key);

    /// <summary>contactsctl.Photo: a contact's photo as the daemon sent it (the daemon's own JPEG or PNG).</summary>
    /// <param name="Contact">The contact.</param>
    /// <param name="Revision">The contact's version it was made from.</param>
    /// <param name="Mime">image/jpeg or image/png.</param>
    /// <param name="Data">The image.</param>
    public sealed record Photo(ContactId Contact, string Revision, string Mime, byte[] Data)
    {
        /// <summary>Names this version of the photo: a view's decoded image of the same key is still good.</summary>
        public string Key => Contact.Value + "\0" + Revision;
    }

    /// <summary>contactsctl.ContactRef: a contact a view shows, with the photo state its summary or card reports.</summary>
    /// <param name="Id">The contact.</param>
    /// <param name="Book">Its book; null unknown.</param>
    /// <param name="State">Its photo; null none.</param>
    public readonly record struct ContactRef(ContactId Id, AddressBookId? Book, ContactPhotoAvailability? State);

    private sealed record PhotoView(List<ContactId> Contacts, List<string> Senders);

    private readonly record struct SenderEntry(ContactId? Contact, ContactPhotoAvailability? State, bool Failed = false, bool Stale = false);
}
