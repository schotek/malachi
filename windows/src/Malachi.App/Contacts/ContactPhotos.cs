// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Contacts/ContactPhotoViews.swift
// (ContactPhotoViews: register, show, changed, reset, image(for:),
// broadcast); GTK: ui/internal/window's use of contactsctl.Photos.
//
// Contact photos in the views (docs/plans/address-book.md §9, F9, F10): the
// application's one ContactsPhotosController, the avatars (Main.Avatar)
// that may show a photo, and the decoded images by Photo.Key. An avatar
// with a photo source (a contact by id in the Contacts window, a sender by
// address in mail; never the user's own message) registers here while it
// is loaded and visible; what the registered avatars stand for is what
// the views show near their viewports (a virtualised list loads only the
// containers in and around its viewport), handed to the controller as the
// one view "avatars" once per dispatcher turn. When the photo of a key
// arrives, changes or goes away, the avatars of that key are told.
//
// The bytes are the daemon's own re-encoded JPEG or PNG (F5), never the
// server's; a photo the decoder refuses (BitmapDecoder first, so that a
// broken image fails here and not later in the image element) is no photo
// and the avatar keeps its initials. Nothing here logs a contact or an
// address.
//
// Windows: the avatars are not handed in by each view as on macOS; the
// avatar control registers itself (Loaded, Unloaded, Visibility, its
// source), which reaches every place mail draws one without wiring each
// list. Current is the one instance AppContacts makes (null before it and
// after Quit's Release).

using System;
using System.Collections.Generic;
using System.Linq;
using System.Runtime.InteropServices.WindowsRuntime;
using System.Threading.Tasks;
using Malachi.App.Main;
using Malachi.Core.Api;
using Malachi.Core.Contacts;
using Malachi.Core.Controllers;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml.Media.Imaging;
using Windows.Graphics.Imaging;
using Windows.Storage.Streams;

namespace Malachi.App.Contacts;

/// <summary>The application's contact photos as the avatars use them (macOS <c>ContactPhotoViews</c>).</summary>
/// <remarks>Create and use it on the UI thread.</remarks>
internal sealed class ContactPhotos : IDisposable
{
    /// <summary>The controller's view of every registered avatar.</summary>
    public const string View = "avatars";

    /// <summary>How many decoded images are kept (the controller keeps the bytes of as many photos).</summary>
    public const int MaxImages = ContactsPhotosController.MaxPhotos;

    private readonly DispatcherQueue dispatcher;
    private readonly HashSet<Avatar> live = [];
    private readonly Dictionary<string, BitmapImage> images = new(StringComparer.Ordinal);
    private readonly List<string> imageOrder = [];
    private readonly HashSet<string> undecodable = new(StringComparer.Ordinal); // keys whose bytes did not decode
    private readonly HashSet<string> decoding = new(StringComparer.Ordinal);
    private bool showQueued;
    private int gen;

    /// <summary>The photos over <paramref name="options"/>; the instance <see cref="Current"/> from now on.</summary>
    public ContactPhotos(ContactsOptions options, DispatcherQueue dispatcher)
    {
        ArgumentNullException.ThrowIfNull(options);
        ArgumentNullException.ThrowIfNull(dispatcher);
        this.dispatcher = dispatcher;
        Controller = new ContactsPhotosController(options);
        Controller.PhotoChanged += OnPhotoChanged;
        Current = this;
    }

    /// <summary>The application's photos; null before AppContacts and after Quit's Release.</summary>
    public static ContactPhotos? Current { get; private set; }

    /// <summary>The cache of the bytes.</summary>
    public ContactsPhotosController Controller { get; }

    /// <summary>
    /// An avatar's source, loading or visibility changed: it is shown (and
    /// told its photo) while it wants a photo, forgotten otherwise.
    /// </summary>
    public void Update(Avatar avatar)
    {
        ArgumentNullException.ThrowIfNull(avatar);
        if (avatar.WantsPhoto)
        {
            live.Add(avatar);
            Apply(avatar);
        }
        else
        {
            live.Remove(avatar);
            avatar.ShowPhoto(null, null);
        }
        QueueShow();
    }

    /// <summary>The avatar left the tree (Unloaded): no longer shown; it keeps what it draws.</summary>
    public void Remove(Avatar avatar)
    {
        if (live.Remove(avatar))
        {
            QueueShow();
        }
    }

    /// <summary>notify.contactsChanged.</summary>
    public void Changed(ContactsChangedNotification n) => Controller.Changed(n);

    /// <summary>
    /// The daemon restarted: everything is forgotten, every avatar draws its
    /// initials until its photo comes again, and what the avatars show is
    /// asked for again.
    /// </summary>
    public void Reset()
    {
        gen++;
        images.Clear();
        imageOrder.Clear();
        undecodable.Clear();
        decoding.Clear();
        Controller.Reset();
        foreach (var a in live.ToList())
        {
            a.ShowPhoto(null, null);
        }
        QueueShow();
    }

    /// <summary>Quit's Release: the avatars draw initials from now on.</summary>
    public void Dispose()
    {
        Controller.PhotoChanged -= OnPhotoChanged;
        live.Clear();
        if (ReferenceEquals(Current, this))
        {
            Current = null;
        }
    }

    // Whether key (ContactKey, SenderKey) names the avatar's photo.
    private static bool Matches(Avatar a, string key)
    {
        if (a.PhotoSource is not { MayShowPhoto: true } s)
        {
            return false;
        }
        if (s.Contact is { } c && key == ContactsPhotosController.ContactKey(c))
        {
            return true;
        }
        return s.Sender.Length > 0 && key == ContactsPhotosController.SenderKey(s.Sender);
    }

    private void OnPhotoChanged(object? sender, string key)
    {
        foreach (var a in live.Where(a => Matches(a, key)).ToList())
        {
            Apply(a);
        }
    }

    // Once per dispatcher turn: what the live avatars stand for, to the
    // controller.
    private void QueueShow()
    {
        if (showQueued)
        {
            return;
        }
        showQueued = true;
        if (!dispatcher.TryEnqueue(DispatcherQueuePriority.Low, Show))
        {
            showQueued = false;
        }
    }

    private void Show()
    {
        showQueued = false;
        if (!ReferenceEquals(Current, this))
        {
            return;
        }
        var refs = new List<ContactsPhotosController.ContactRef>();
        var senders = new List<string>();
        foreach (var a in live)
        {
            if (a.PhotoContact is { } r)
            {
                refs.Add(r);
            }
            else if (a.PhotoSource is { Sender.Length: > 0 } s)
            {
                senders.Add(s.Sender);
            }
        }
        Controller.Show(View, refs, senders);
    }

    // The avatar's photo: the decoded image, or a decoding started (the
    // avatar keeps a photo of the same contact meanwhile, never another's).
    private void Apply(Avatar a)
    {
        var ph = a.PhotoSource is { MayShowPhoto: true } s ? Controller.For(s) : null;
        if (ph is null || undecodable.Contains(ph.Key))
        {
            a.ShowPhoto(null, null);
            return;
        }
        if (images.TryGetValue(ph.Key, out var image))
        {
            imageOrder.Remove(ph.Key);
            imageOrder.Add(ph.Key);
            a.ShowPhoto(image, ph.Contact);
            return;
        }
        if (!a.ShowsPhotoOf(ph.Contact))
        {
            a.ShowPhoto(null, null);
        }
        if (decoding.Add(ph.Key))
        {
            _ = DecodeAsync(ph);
        }
    }

    private async Task DecodeAsync(ContactsPhotosController.Photo ph)
    {
        var g = gen;
        BitmapImage? image = null;
        try
        {
            if (ph.Data.Length <= API.Limits.MaxContactPhotoBytes)
            {
                using var stream = new InMemoryRandomAccessStream();
                await stream.WriteAsync(ph.Data.AsBuffer());
                stream.Seek(0);
                // Refuses what is not an image before the image element sees it.
                var decoder = await BitmapDecoder.CreateAsync(stream);
                if (decoder.PixelWidth > 0 && decoder.PixelHeight > 0)
                {
                    stream.Seek(0);
                    var bitmap = new BitmapImage();
                    await bitmap.SetSourceAsync(stream);
                    image = bitmap;
                }
            }
        }
#pragma warning disable CA1031 // Any failure of the decoders is a photo that does not decode: initials.
        catch (Exception)
#pragma warning restore CA1031
        {
            image = null;
        }
        if (g != gen || !ReferenceEquals(Current, this))
        {
            return;
        }
        decoding.Remove(ph.Key);
        if (image is null)
        {
            undecodable.Add(ph.Key);
        }
        else
        {
            images[ph.Key] = image;
            imageOrder.Add(ph.Key);
            while (imageOrder.Count > MaxImages)
            {
                images.Remove(imageOrder[0]);
                imageOrder.RemoveAt(0);
            }
        }
        foreach (var a in live.Where(a => a.PhotoSource is { MayShowPhoto: true } s && Controller.For(s)?.Key == ph.Key).ToList())
        {
            Apply(a);
        }
    }
}
