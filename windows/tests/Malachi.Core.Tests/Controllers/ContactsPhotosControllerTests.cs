// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/contactsctl photos_test.go (photoDaemon,
// TestPhotosBySender, TestPhotosBatches, TestPhotosByContact,
// TestPhotosConcurrency, TestPhotosChanged, TestPhotosFailures,
// TestPhotosLRU, TestPhotosOwnAndReset); Go-first, and the sample daemon's
// photos (ContactsSampleCaller), Windows first.
//
// Windows: the Go tests' loop is the UI thread of the tests (TestUIContext)
// with the timer the test fires itself (Fire) and SettleAsync for loop.run;
// the photos the Go loop answers one by one are held by the fake daemon
// (Hold) and released by the test. Fictional people only.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Contacts;
using Malachi.Core.Controllers;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Xunit;
using Ref = Malachi.Core.Controllers.ContactsPhotosController.ContactRef;

namespace Malachi.Core.Tests.Controllers;

public sealed class ContactsPhotosControllerTests
{
    [Fact]
    public async Task BySender()
    {
        using var h = new PhotoHarness();
        var d = h.Daemon;
        d.Refs["jana@firma.cz"] = Ready("p_1");
        d.Refs["petr@firma.cz"] = new ContactPhotoRef { ContactId = "p_2", State = ContactPhotoAvailability.Remote };
        d.Refs["eva@firma.cz"] = new ContactPhotoRef { ContactId = "p_3", State = new ContactPhotoAvailability("") }; // a contact without a photo
        d.Revs["p_1"] = "e1";

        await h.Run(p =>
        {
            p.Show("list", null, ["Jana@Firma.cz", "petr@firma.cz", "eva@firma.cz", "x@y.cz"]);
            p.Show("conversation", null, ["jana@firma.cz"]);
        });
        await h.SettleAsync();
        Assert.Equal(0, d.Count(API.ContactLookup.Name)); // not before the debounce
        await h.FireAsync();
        var lp = d.LastLookup();
        Assert.Equal(1, d.Count(API.ContactLookup.Name));
        Assert.True(lp.WithPhotos);
        Assert.Equal(["jana@firma.cz", "petr@firma.cz", "eva@firma.cz", "x@y.cz"], lp.Addresses);
        // p_1 ready, p_2 remote: both asked lazily; p_3 and x@y.cz not.
        Assert.Equal(["p_1", "p_2"], d.PhotosAsked());
        var ph = await h.Run(p => p.ForSender("JANA@firma.cz"));
        Assert.NotNull(ph);
        Assert.Equal("jpeg:p_1:e1", System.Text.Encoding.ASCII.GetString(ph.Data));
        Assert.Equal("image/jpeg", ph.Mime);
        Assert.Equal("p_1\0e1", ph.Key);
        Assert.Null(await h.Run(p => p.ForSender("petr@firma.cz"))); // a remote contact the daemon has no photo of
        Assert.Null(await h.Run(p => p.For(ContactBook.AvatarOfSender("eva@firma.cz", "Eva", false))));
        Assert.Equal("p_1", (await h.Run(p => p.For(ContactBook.AvatarOfSender("jana@firma.cz", "Jana", false))))?.Contact.Value);
        Assert.Equal([ContactsPhotosController.ContactKey("p_1"), ContactsPhotosController.SenderKey("jana@firma.cz")], h.Keys);

        // Answers are kept, also the negative ones: nothing is asked again.
        await h.Run(p => p.Show("list", null, ["x@y.cz", "eva@firma.cz", "jana@firma.cz"]));
        await h.FireAsync();
        Assert.Equal(1, d.Count(API.ContactLookup.Name));
        Assert.Equal(2, d.Count(API.ContactPhoto.Name));
    }

    [Fact]
    public async Task Batches()
    {
        using var h = new PhotoHarness();
        var d = h.Daemon;
        var many = Enumerable.Range(0, 450).Select(i => $"a{i}@x.cz").ToArray();
        await h.Run(p =>
        {
            p.Show("list", null, many[..100]);
            p.Show("list", null, many);
        });
        await h.FireAsync();
        Assert.Equal(3, d.Count(API.ContactLookup.Name));
        Assert.All(d.Lookups(), l => Assert.True(l.Addresses.Count <= API.Limits.MaxContactLookupAddresses));
        // What a view no longer shows when the debounce runs is not asked.
        await h.Run(p =>
        {
            p.Show("list", null, ["gone@x.cz"]);
            p.Show("list", null, null);
        });
        await h.FireAsync();
        Assert.Equal(3, d.Count(API.ContactLookup.Name));
        // An address on its way is not asked twice.
        d.Hold(API.ContactLookup.Name);
        await h.Run(p => p.Show("list", null, ["b@x.cz"]));
        await h.FireTimersAsync();
        await Eventually.Holds(() => d.Count(API.ContactLookup.Name) == 4);
        await h.Run(p => p.Show("other", null, ["b@x.cz"]));
        await h.FireTimersAsync();
        d.ReleaseAll();
        await h.SettleAsync();
        await h.FireAsync();
        Assert.Equal(4, d.Count(API.ContactLookup.Name));
    }

    [Fact]
    public async Task ByContact()
    {
        using var h = new PhotoHarness();
        var d = h.Daemon;
        d.Revs["p_1"] = "e1";
        await h.Run(p => p.Show("contacts",
        [
            new Ref("p_1", "b_1", ContactPhotoAvailability.Ready),
            new Ref("p_2", "b_1", ContactPhotoAvailability.Remote),
            new Ref("p_3", "b_1", null),
            new Ref("r_1", AddressBookId.Recent, ContactPhotoAvailability.Ready),
        ], null));
        await h.SettleAsync();
        Assert.Equal(["p_1", "p_2"], d.PhotosAsked());
        Assert.NotNull(await h.Run(p => p.ForContact("p_1")));
        Assert.Null(await h.Run(p => p.ForContact("p_2"))); // p_2 has none
        Assert.Equal([ContactsPhotosController.ContactKey("p_1")], h.Keys);
        // "none" is remembered.
        await h.Run(p => p.Show("contacts", [new Ref("p_2", "b_1", ContactPhotoAvailability.Remote)], null));
        await h.SettleAsync();
        Assert.Equal(2, d.Count(API.ContactPhoto.Name));
        // The list reports a new state: asked again.
        d.Revs["p_2"] = "e7";
        await h.Run(p => p.Show("contacts", [new Ref("p_2", "b_1", ContactPhotoAvailability.Ready)], null));
        await h.SettleAsync();
        Assert.Equal("e7", (await h.Run(p => p.ForContact("p_2")))?.Revision);
    }

    [Fact]
    public async Task Concurrency()
    {
        using var h = new PhotoHarness();
        var d = h.Daemon;
        var refs = new List<Ref>();
        for (var i = 0; i < 10; i++)
        {
            var id = new ContactId("p_" + i.ToString(CultureInfo.InvariantCulture));
            d.Revs[id] = "e";
            refs.Add(new Ref(id, "b_1", ContactPhotoAvailability.Remote));
        }
        d.Hold(API.ContactPhoto.Name);
        await h.Run(p => p.Show("contacts", refs, null));
        await Eventually.Holds(() => d.Count(API.ContactPhoto.Name) == ContactsPhotosController.MaxPhotoFetches);
        await h.Ui.DrainAsync();
        Assert.Equal(ContactsPhotosController.MaxPhotoFetches, d.Count(API.ContactPhoto.Name));
        // Nearest first; the next start as the first answers come.
        Assert.Equal(["p_0", "p_1", "p_2", "p_3"], d.PhotosAsked());
        d.Release(1);
        await Eventually.Holds(() => d.Count(API.ContactPhoto.Name) == ContactsPhotosController.MaxPhotoFetches + 1);
        // What scrolled away is not asked.
        await h.Run(p => p.Show("contacts", refs.Take(6).ToList(), null));
        d.Unhold();
        d.ReleaseAll();
        await h.SettleAsync();
        Assert.Equal(6, d.Count(API.ContactPhoto.Name));
    }

    [Fact]
    public async Task Changed()
    {
        using var h = new PhotoHarness();
        var d = h.Daemon;
        d.Refs["jana@firma.cz"] = Ready("p_1");
        d.Revs["p_1"] = "e1";
        d.Revs["p_9"] = "e1";
        await h.Run(p =>
        {
            p.Show("list", null, ["jana@firma.cz", "x@y.cz"]);
            p.Show("contacts", [new Ref("p_9", "b_2", ContactPhotoAvailability.Ready)], null);
        });
        await h.FireAsync();
        Assert.Equal(2, d.Count(API.ContactPhoto.Name));
        h.Keys.Clear();

        // The recent book only: nothing.
        await h.Run(p => p.Changed(new ContactsChangedNotification { BookIds = [AddressBookId.Recent] }));
        await h.FireAsync();
        Assert.Equal(1, d.Count(API.ContactLookup.Name));
        Assert.Equal(2, d.Count(API.ContactPhoto.Name));

        // Book b_1 changed: senders (book unknown) and negatives are asked
        // again, p_9 of b_2 is not; the photo stays shown meanwhile.
        d.Refs["x@y.cz"] = Ready("p_5");
        d.Revs["p_5"] = "e1";
        var kept = await h.Run(p =>
        {
            p.Changed(new ContactsChangedNotification { AccountId = "acc_m", BookIds = ["b_1"] });
            return p.ForSender("jana@firma.cz");
        });
        Assert.NotNull(kept); // the photo stays until the new answer
        await h.FireAsync();
        Assert.Equal(["jana@firma.cz", "x@y.cz"], d.LastLookup().Addresses);
        Assert.Equal(["p_1", "p_5"], d.PhotosAsked().Skip(2));
        // p_1's revision did not change: no redraw; x@y.cz got a photo.
        Assert.Equal(
            [ContactsPhotosController.SenderKey("x@y.cz"), ContactsPhotosController.ContactKey("p_5"), ContactsPhotosController.SenderKey("x@y.cz")],
            h.Keys);

        // All books: a new revision redraws; a removed photo too.
        h.Keys.Clear();
        d.Revs["p_1"] = "e2";
        d.Revs.Remove("p_9");
        await h.Run(p => p.Changed(new ContactsChangedNotification { AccountId = "acc_m" }));
        await h.FireAsync();
        Assert.Equal("e2", (await h.Run(p => p.ForSender("jana@firma.cz")))?.Revision);
        Assert.Null(await h.Run(p => p.ForContact("p_9")));
        Assert.Contains(ContactsPhotosController.ContactKey("p_1"), h.Keys);
        Assert.Contains(ContactsPhotosController.SenderKey("jana@firma.cz"), h.Keys);
        Assert.Contains(ContactsPhotosController.ContactKey("p_9"), h.Keys);

        // An answer on its way when the books change is dropped and asked
        // again.
        await h.Run(p => p.Changed(new ContactsChangedNotification()));
        var n = d.Count(API.ContactLookup.Name);
        await h.Run(p =>
        {
            h.RunTimers(); // the flush
            p.Changed(new ContactsChangedNotification());
        });
        await h.SettleAsync();
        Assert.NotNull(await h.Run(p => p.ForSender("jana@firma.cz"))); // the photo is not lost
        await h.FireAsync();
        Assert.Equal(n + 2, d.Count(API.ContactLookup.Name));
    }

    [Fact]
    public async Task Failures()
    {
        using var h = new PhotoHarness();
        var d = h.Daemon;
        d.LookupFails = true;
        await h.Run(p => p.Show("list", null, ["jana@firma.cz"]));
        await h.FireAsync();
        await h.Run(p => p.Show("list", null, ["jana@firma.cz"]));
        await h.FireAsync();
        Assert.Equal(1, d.Count(API.ContactLookup.Name)); // a failed lookup is not repeated
        d.PhotoFails = true;
        await h.Run(p => p.Show("contacts", [new Ref("p_1", "b_1", ContactPhotoAvailability.Remote)], null));
        await h.SettleAsync();
        await h.Run(p => p.Show("contacts", [new Ref("p_1", "b_1", ContactPhotoAvailability.Remote)], null));
        await h.SettleAsync();
        Assert.Equal(1, d.Count(API.ContactPhoto.Name)); // a failed photo is not repeated
        // A change asks again.
        await h.Run(p => p.Changed(new ContactsChangedNotification()));
        await h.FireAsync();
        Assert.Equal(2, d.Count(API.ContactLookup.Name));
        Assert.Equal(2, d.Count(API.ContactPhoto.Name));
    }

    [Fact]
    public async Task Lru()
    {
        using var h = new PhotoHarness();
        var d = h.Daemon;
        Ref R(int i)
        {
            var id = new ContactId("p_" + i.ToString(CultureInfo.InvariantCulture));
            d.Revs[id] = "e";
            return new Ref(id, "b_1", ContactPhotoAvailability.Ready);
        }
        // Pages of 50 shown one after another: the least recently used
        // photos go, those shown never.
        for (var page = 0; page < 5; page++)
        {
            var refs = Enumerable.Range(page * 50, 50).Select(R).ToList();
            await h.Run(p => p.Show("contacts", refs, null));
            await h.SettleAsync();
        }
        Assert.Equal(ContactsPhotosController.MaxPhotos, await h.Run(p => p.CachedCount));
        Assert.Null(await h.Run(p => p.ForContact("p_0"))); // the oldest went
        Assert.NotNull(await h.Run(p => p.ForContact("p_249"))); // a shown one stays
        // A photo used again is kept over older ones.
        await h.Run(p => p.ForContact("p_50"));
        var r300 = R(300);
        await h.Run(p => p.Show("contacts", [r300], null));
        await h.SettleAsync();
        Assert.NotNull(await h.Run(p => p.ForContact("p_50")));
        Assert.Null(await h.Run(p => p.ForContact("p_51")));
        // More shown than kept: no endless refetching.
        var all = Enumerable.Range(1000, ContactsPhotosController.MaxPhotos + 20).Select(R).ToList();
        var n = d.Count(API.ContactPhoto.Name);
        await h.Run(p => p.Show("contacts", all, null));
        await h.SettleAsync();
        Assert.Equal(ContactsPhotosController.MaxPhotos, d.Count(API.ContactPhoto.Name) - n);
    }

    [Fact]
    public async Task OwnAndReset()
    {
        using var h = new PhotoHarness();
        var d = h.Daemon;
        d.Refs["jana@firma.cz"] = Ready("p_1");
        d.Revs["p_1"] = "e1";
        // The user's own message never asks (its avatar has no Sender).
        var own = ContactBook.AvatarOfSender("jana@firma.cz", "Jana", true);
        await h.Run(p => p.Show("list", null, [own.Sender]));
        await h.FireAsync();
        Assert.Equal(0, d.Count(API.ContactLookup.Name));
        Assert.Null(await h.Run(p => p.For(own)));
        await h.Run(p => p.Show("list", null, ["jana@firma.cz"]));
        await h.FireAsync();
        Assert.NotNull(await h.Run(p => p.ForSender("jana@firma.cz")));
        await h.Run(p => p.Reset());
        Assert.Null(await h.Run(p => p.ForSender("jana@firma.cz")));
    }

    /// <summary>The sample daemon: ready photos at once, a remote one fetched once, and the lookup's photos.</summary>
    [Fact]
    public async Task SamplesHavePhotos()
    {
        var samples = new ContactsSampleCaller(ContactsHarness.Stamp("2026-10-30T12:00:00Z"));
        var notified = new List<ContactsChangedNotification>();
        samples.Notified += (_, n) => notified.Add(n);
        var ct = TestContext.Current.CancellationToken;
        var list = await samples.CallAsync(API.ContactList, new ContactListParams { BookId = AddressBookId.All }, TimeSpan.FromSeconds(1), ct);
        var jana = list.Contacts.Single(c => c.Email == "jana.novakova@example.cz");
        var marie = list.Contacts.Single(c => c.Email == "marie@example.cz");
        var karel = list.Contacts.Single(c => c.Email == "karel@kucera.example");
        Assert.Equal(ContactPhotoAvailability.Ready, jana.Photo?.Value);
        Assert.Equal(ContactPhotoAvailability.Remote, marie.Photo?.Value);
        Assert.Null(list.Contacts.Single(c => c.Email == "pavel.novak@example.cz").Photo);
        var card = await samples.CallAsync(API.ContactGet, new ContactGetParams { ContactId = jana.Id }, TimeSpan.FromSeconds(1), ct);
        Assert.Equal(ContactPhotoAvailability.Ready, card.Contact.Photo?.Value);

        var ph = await samples.CallAsync(API.ContactPhoto, new ContactPhotoParams { ContactId = jana.Id }, TimeSpan.FromSeconds(1), ct);
        Assert.Equal(ContactPhotoState.Ready, ph.State.Value);
        Assert.Equal("image/png", ph.Mime);
        Assert.Equal(new byte[] { 0x89, 0x50, 0x4E, 0x47 }, ph.Data![..4]);
        Assert.True(ph.Data.Length <= API.Limits.MaxContactPhotoBytes);
        Assert.Empty(notified);
        // The remote one: downloaded once, ready from then on.
        ph = await samples.CallAsync(API.ContactPhoto, new ContactPhotoParams { ContactId = marie.Id }, TimeSpan.FromSeconds(1), ct);
        Assert.Equal(ContactPhotoState.Ready, ph.State.Value);
        Assert.Single(notified);
        list = await samples.CallAsync(API.ContactList, new ContactListParams { BookId = AddressBookId.All }, TimeSpan.FromSeconds(1), ct);
        Assert.Equal(ContactPhotoAvailability.Ready, list.Contacts.Single(c => c.Id == marie.Id).Photo?.Value);
        // A remote one without a photo: none, and no photo from then on.
        ph = await samples.CallAsync(API.ContactPhoto, new ContactPhotoParams { ContactId = karel.Id }, TimeSpan.FromSeconds(1), ct);
        Assert.Equal(ContactPhotoState.None, ph.State.Value);
        Assert.Null(ph.Data);
        list = await samples.CallAsync(API.ContactList, new ContactListParams { BookId = AddressBookId.All }, TimeSpan.FromSeconds(1), ct);
        Assert.Null(list.Contacts.Single(c => c.Id == karel.Id).Photo);

        var lookup = await samples.CallAsync(
            API.ContactLookup, new ContactLookupParams { Addresses = ["jana.novakova@example.cz", "pavel.novak@example.cz"], WithPhotos = true }, TimeSpan.FromSeconds(1), ct);
        Assert.Equal(jana.Id, lookup.Photos!["jana.novakova@example.cz"].ContactId);
        Assert.False(lookup.Photos.ContainsKey("pavel.novak@example.cz"));
        Assert.Equal(2, lookup.Known.Count);
        lookup = await samples.CallAsync(
            API.ContactLookup, new ContactLookupParams { Addresses = ["jana.novakova@example.cz"] }, TimeSpan.FromSeconds(1), ct);
        Assert.Null(lookup.Photos);
    }

    private static ContactPhotoRef Ready(string id) => new() { ContactId = id, State = ContactPhotoAvailability.Ready };

    /// <summary>The UI thread, the photo daemon and the timer of a test.</summary>
    private sealed class PhotoHarness : IDisposable
    {
        private readonly List<Action> timers = [];

        public PhotoHarness()
        {
            Options = new ContactsOptions(Daemon, ContactBook.EnglishStrftime)
            {
                After = (_, f) => timers.Add(f),
            };
            // Its constructor touches nothing of the UI thread; every call after it runs there (Run).
            Photos = new ContactsPhotosController(Options);
            Photos.PhotoChanged += (_, k) => Keys.Add(k);
        }

        public TestUIContext Ui { get; } = new();

        public PhotoDaemon Daemon { get; } = new();

        public ContactsOptions Options { get; }

        public ContactsPhotosController Photos { get; }

        /// <summary>The keys PhotoChanged reported (on the UI thread).</summary>
        public List<string> Keys { get; } = [];

        public Task Run(Action<ContactsPhotosController> f) => Ui.RunAsync(() => f(Photos));

        public Task<T> Run<T>(Func<ContactsPhotosController, T> f) => Ui.RunAsync(() => f(Photos));

        /// <summary>loop.run.</summary>
        public Task SettleAsync() => Quiescence.IdleAsync(Ui, Options.Pending);

        /// <summary>The timers set so far, on the UI thread's turn (call it there).</summary>
        public void RunTimers()
        {
            var fs = timers.ToList();
            timers.Clear();
            foreach (var f in fs)
            {
                f();
            }
        }

        /// <summary>The timers set so far, without waiting for what they started.</summary>
        public Task FireTimersAsync() => Ui.RunAsync(RunTimers);

        /// <summary>loop.fire: the timers set so far, then what they started.</summary>
        public async Task FireAsync()
        {
            await FireTimersAsync();
            await SettleAsync();
        }

        public void Dispose() => Ui.Dispose();
    }

    /// <summary>
    /// photoDaemon: contact.lookup {withPhotos} from <see cref="Refs"/>
    /// (address → contact with a photo) and contact.photo from
    /// <see cref="Revs"/> (contact → revision; absent = none). A held method
    /// waits until the test releases its calls in order.
    /// </summary>
    private sealed class PhotoDaemon : IContactsCaller
    {
        private readonly Lock gate = new();
        private readonly List<(string Method, JsonElement Params)> calls = [];
        private readonly List<TaskCompletionSource> held = [];
        private readonly HashSet<string> holding = new(StringComparer.Ordinal);

        public Dictionary<string, ContactPhotoRef> Refs { get; } = new(StringComparer.Ordinal);

        public Dictionary<ContactId, string> Revs { get; } = [];

        public bool LookupFails { get; set; }

        public bool PhotoFails { get; set; }

        public void Hold(string method)
        {
            lock (gate)
            {
                holding.Add(method);
            }
        }

        public void Unhold()
        {
            lock (gate)
            {
                holding.Clear();
            }
        }

        public void Release(int n)
        {
            List<TaskCompletionSource> go;
            lock (gate)
            {
                go = held.Take(n).ToList();
                held.RemoveRange(0, go.Count);
            }
            foreach (var t in go)
            {
                t.SetResult();
            }
        }

        public void ReleaseAll() => Release(int.MaxValue);

        public int Count(string method)
        {
            lock (gate)
            {
                return calls.Count(c => c.Method == method);
            }
        }

        public IReadOnlyList<ContactLookupParams> Lookups()
        {
            lock (gate)
            {
                return [.. calls.Where(c => c.Method == API.ContactLookup.Name).Select(c => c.Params.Deserialize(API.ContactLookup.ParamsInfo)!)];
            }
        }

        public ContactLookupParams LastLookup() => Lookups()[^1];

        public IReadOnlyList<string> PhotosAsked()
        {
            lock (gate)
            {
                return [.. calls.Where(c => c.Method == API.ContactPhoto.Name).Select(c => c.Params.Deserialize(API.ContactPhoto.ParamsInfo)!.ContactId.Value)];
            }
        }

        public async Task<TResult> CallAsync<TParams, TResult>(
            RpcMethod<TParams, TResult> method, TParams parameters, TimeSpan timeout, CancellationToken cancellationToken)
        {
            ArgumentNullException.ThrowIfNull(method);
            var raw = JsonSerializer.SerializeToElement(parameters, method.ParamsInfo);
            TaskCompletionSource? wait = null;
            lock (gate)
            {
                calls.Add((method.Name, raw));
                if (holding.Contains(method.Name))
                {
                    wait = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
                    held.Add(wait);
                }
            }
            if (wait is not null)
            {
                await wait.Task;
            }
            object v;
            lock (gate)
            {
                v = Answer(method.Name, raw);
            }
            var bytes = JsonSerializer.SerializeToUtf8Bytes(v, ApiJsonContext.Wire.GetTypeInfo(v.GetType())!);
            return JsonSerializer.Deserialize(bytes, method.ResultInfo)!;
        }

        private object Answer(string method, JsonElement raw)
        {
            if (method == API.ContactLookup.Name)
            {
                if (LookupFails)
                {
                    throw new RpcException(new RpcError { Code = ErrorCode.Unavailable, Message = "x" });
                }
                var p = raw.Deserialize(API.ContactLookup.ParamsInfo)!;
                var known = new Dictionary<string, ContactId>(StringComparer.Ordinal);
                var photos = new Dictionary<string, ContactPhotoRef>(StringComparer.Ordinal);
                foreach (var a in p.Addresses)
                {
                    if (Refs.TryGetValue(a, out var r))
                    {
                        known[a] = r.ContactId;
                        if (p.WithPhotos == true)
                        {
                            photos[a] = r;
                        }
                    }
                }
                return new ContactLookupResult { Known = known, Photos = photos };
            }
            if (method == API.ContactPhoto.Name)
            {
                if (PhotoFails)
                {
                    throw new RpcException(new RpcError { Code = ErrorCode.Unavailable, Message = "x" });
                }
                var p = raw.Deserialize(API.ContactPhoto.ParamsInfo)!;
                if (!Revs.TryGetValue(p.ContactId, out var rev))
                {
                    return new ContactPhotoResult { State = ContactPhotoState.None };
                }
                return new ContactPhotoResult
                {
                    State = ContactPhotoState.Ready,
                    Mime = "image/jpeg",
                    Data = System.Text.Encoding.ASCII.GetBytes("jpeg:" + p.ContactId.Value + ":" + rev),
                    Revision = rev,
                };
            }
            throw new RpcException(new RpcError { Code = ErrorCode.MethodNotFound, Message = method });
        }
    }
}
