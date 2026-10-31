// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MessageList/AvatarView.swift
// (AvatarView); GTK: Adw.Avatar of ui/data/ui/message_row.blp (size 40,
// show-initials) and ui/internal/style's monochrome-avatars rule. A disc
// with the gradient of the sender's colour class and the initials of the
// name, or the person symbol at half the size without a name; the
// monochrome variant is the text colour at 12 % with the initials at 80 %;
// the accent variant (the user's own message on the conversation's
// timeline, conversation_rows.go avatar.conversation-own) is the accent
// fill of the theme with the text on it, over either.
// The palette and the initials are Core's (AvatarPalette). Nothing is
// fetched; the name is hostile input, only ever set as a TextBlock's text.
// Decoration: the row's name says who wrote, so a screen reader skips it.
//
// Contact photos (docs/plans/address-book.md §9, F9, F10; macOS
// AvatarView.photoSource and ContactInitialsView, ContactPhotoViews.swift):
// with a photo source, PhotoSender (a sender's address,
// ContactBook.AvatarOfSender, "" for the user's own message) or
// PhotoContact (a contact of the Contacts window with its photo state), the
// avatar registers with the application's ContactPhotos while it is loaded
// and visible, and the contact's photo, clipped to the circle, replaces the
// disc and the initials once decoded. monochrome-avatars and the accent
// tint apply to initials only, never to a photo.

using Malachi.App.Contacts;
using Malachi.App.Resources;
using Malachi.Core.Api;
using Malachi.Core.Contacts;
using Malachi.Core.Controllers;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.Shapes;
using Windows.Foundation;
using Windows.UI;

namespace Malachi.App.Main;

/// <summary>A sender's avatar (Adw.Avatar).</summary>
public sealed partial class Avatar : UserControl
{
    /// <summary>The name the colour and the initials come from.</summary>
    public static readonly DependencyProperty TextProperty = DependencyProperty.Register(
        nameof(Text), typeof(string), typeof(Avatar), new PropertyMetadata("", (d, _) => ((Avatar)d).Redraw()));

    /// <summary>The diameter.</summary>
    public static readonly DependencyProperty SizeProperty = DependencyProperty.Register(
        nameof(Size), typeof(double), typeof(Avatar), new PropertyMetadata(40.0, (d, _) => ((Avatar)d).Redraw()));

    /// <summary>The neutral variant.</summary>
    public static readonly DependencyProperty MonochromeProperty = DependencyProperty.Register(
        nameof(Monochrome), typeof(bool), typeof(Avatar), new PropertyMetadata(false, (d, _) => ((Avatar)d).Redraw()));

    /// <summary>The accent variant: the user's own message (convRail.Accent).</summary>
    public static readonly DependencyProperty AccentProperty = DependencyProperty.Register(
        nameof(Accent), typeof(bool), typeof(Avatar), new PropertyMetadata(false, (d, _) => ((Avatar)d).Redraw()));

    /// <summary>The address whose contact's photo replaces the initials ("" none: the user's own message, no address).</summary>
    public static readonly DependencyProperty PhotoSenderProperty = DependencyProperty.Register(
        nameof(PhotoSender), typeof(string), typeof(Avatar), new PropertyMetadata("", (d, _) => ((Avatar)d).PhotoSourceChanged()));

    private static readonly Windows.UI.ViewManagement.UISettings SystemColours = new();

    private readonly ImageBrush photoBrush = new() { Stretch = Stretch.UniformToFill };
    private readonly Ellipse photo = new() { Visibility = Visibility.Collapsed };
    private ContactsPhotosController.ContactRef? photoContact;
    private ContactId? shownContact; // whose photo is drawn; null initials
    private bool loaded;

    private readonly Ellipse disc = new();
    private readonly TextBlock initials = new()
    {
        HorizontalAlignment = HorizontalAlignment.Center,
        VerticalAlignment = VerticalAlignment.Center,
        FontWeight = Microsoft.UI.Text.FontWeights.Bold,
        TextLineBounds = TextLineBounds.Tight,
        IsTextScaleFactorEnabled = false,
    };

    private readonly FontIcon person = new()
    {
        Glyph = Icons.Glyph("avatar-default-symbolic"),
        HorizontalAlignment = HorizontalAlignment.Center,
        VerticalAlignment = VerticalAlignment.Center,
    };

    /// <summary>An avatar of 40 without a name.</summary>
    public Avatar()
    {
        IsTabStop = false;
        AutomationProperties.SetAccessibilityView(this, AccessibilityView.Raw);
        person.FontFamily = Icons.SymbolFont;
        AutomationProperties.SetAccessibilityView(initials, AccessibilityView.Raw);
        AutomationProperties.SetAccessibilityView(person, AccessibilityView.Raw);
        photo.Fill = photoBrush;
        var root = new Grid();
        root.Children.Add(disc);
        root.Children.Add(initials);
        root.Children.Add(person);
        root.Children.Add(photo);
        Content = root;
        ActualThemeChanged += (_, _) => Redraw();
        Loaded += (_, _) =>
        {
            loaded = true;
            ContactPhotos.Current?.Update(this);
        };
        Unloaded += (_, _) =>
        {
            loaded = false;
            ContactPhotos.Current?.Remove(this);
        };
        RegisterPropertyChangedCallback(VisibilityProperty, (_, _) => PhotoSourceChanged());
        Redraw();
    }

    /// <summary>The address whose contact's photo replaces the initials (ContactBook.Avatar.Sender); "" none.</summary>
    public string PhotoSender
    {
        get => (string)GetValue(PhotoSenderProperty);
        set => SetValue(PhotoSenderProperty, value ?? "");
    }

    /// <summary>The contact (Contacts window) whose photo replaces the initials, with the state its summary or card reports; null none.</summary>
    /// <remarks>A plain property, set from code (the Contacts window's rows and detail), never from markup.</remarks>
    public ContactsPhotosController.ContactRef? PhotoContact
    {
        get => photoContact;
        set
        {
            if (photoContact != value)
            {
                photoContact = value;
                PhotoSourceChanged();
            }
        }
    }

    /// <summary>Whose photo this avatar may show; null none (a contact without a photo, the user's own message).</summary>
    internal ContactBook.Avatar? PhotoSource
    {
        get
        {
            if (photoContact is { } r)
            {
                return ContactBook.MayHavePhoto(r.State) && !ContactBook.IsRecent(r.Book, r.Id) ? new ContactBook.Avatar("") { Contact = r.Id } : null;
            }
            var sender = PhotoSender;
            return sender.Length > 0 ? new ContactBook.Avatar("") { Sender = sender } : null;
        }
    }

    /// <summary>Loaded, visible and with a source: the photos are asked for it.</summary>
    internal bool WantsPhoto => loaded && Visibility == Visibility.Visible && PhotoSource is { MayShowPhoto: true };

    /// <summary>Whether the drawn photo is <paramref name="contact"/>'s.</summary>
    internal bool ShowsPhotoOf(ContactId contact) => shownContact == contact;

    /// <summary>Draws <paramref name="image"/> (the photo of <paramref name="contact"/>) instead of the initials; null the initials.</summary>
    internal void ShowPhoto(ImageSource? image, ContactId? contact)
    {
        if (image is null)
        {
            if (shownContact is null && photoBrush.ImageSource is null)
            {
                return;
            }
            photoBrush.ImageSource = null;
            shownContact = null;
        }
        else
        {
            photoBrush.ImageSource = image;
            shownContact = contact;
        }
        Redraw();
    }

    private void PhotoSourceChanged()
    {
        if (ContactPhotos.Current is { } photos)
        {
            photos.Update(this);
        }
        else
        {
            ShowPhoto(null, null);
        }
    }

    /// <summary>The name the colour and the initials come from.</summary>
    public string Text
    {
        get => (string)GetValue(TextProperty);
        set => SetValue(TextProperty, value ?? "");
    }

    /// <summary>The diameter.</summary>
    public double Size
    {
        get => (double)GetValue(SizeProperty);
        set => SetValue(SizeProperty, value);
    }

    /// <summary>The neutral variant.</summary>
    public bool Monochrome
    {
        get => (bool)GetValue(MonochromeProperty);
        set => SetValue(MonochromeProperty, value);
    }

    /// <summary>The accent variant, over <see cref="Monochrome"/>.</summary>
    public bool Accent
    {
        get => (bool)GetValue(AccentProperty);
        set => SetValue(AccentProperty, value);
    }

    private static Color Rgb(uint v) => Color.FromArgb(0xff, (byte)(v >> 16), (byte)(v >> 8), (byte)v);

    private void Redraw()
    {
        var size = Size;
        Width = size;
        Height = size;
        disc.Width = size;
        disc.Height = size;
        var text = Text ?? "";
        Brush foreground;
        var dark = ActualTheme == ElementTheme.Dark;
        if (Accent)
        {
            // AccentFillColorDefault and TextOnAccentFillColorPrimary of the
            // element's theme (@accent_bg_color, @accent_fg_color).
            var accent = SystemColours.GetColorValue(dark
                ? Windows.UI.ViewManagement.UIColorType.AccentLight2
                : Windows.UI.ViewManagement.UIColorType.AccentDark1);
            disc.Fill = new SolidColorBrush(accent);
            foreground = new SolidColorBrush(dark ? Color.FromArgb(0xff, 0, 0, 0) : Color.FromArgb(0xff, 0xff, 0xff, 0xff));
        }
        else if (Monochrome)
        {
            // alpha(@window_fg_color, …): the text colour of the theme.
            var fg = dark ? Color.FromArgb(0xff, 0xff, 0xff, 0xff) : Color.FromArgb(0xff, 0, 0, 0);
            disc.Fill = new SolidColorBrush(fg) { Opacity = AvatarPalette.MonochromeFillOpacity };
            foreground = new SolidColorBrush(fg) { Opacity = AvatarPalette.MonochromeTextOpacity };
        }
        else
        {
            var colours = AvatarPalette.ColoursOf(text);
            var gradient = new LinearGradientBrush { StartPoint = new Point(0.5, 0), EndPoint = new Point(0.5, 1) };
            gradient.GradientStops.Add(new GradientStop { Color = Rgb(colours.Top), Offset = 0 });
            gradient.GradientStops.Add(new GradientStop { Color = Rgb(colours.Bottom), Offset = 1 });
            disc.Fill = gradient;
            foreground = new SolidColorBrush(Rgb(colours.Foreground));
        }
        var hasPhoto = photoBrush.ImageSource is not null;
        photo.Width = size;
        photo.Height = size;
        photo.Visibility = hasPhoto ? Visibility.Visible : Visibility.Collapsed;
        disc.Visibility = hasPhoto ? Visibility.Collapsed : Visibility.Visible;
        var letters = hasPhoto ? "" : AvatarPalette.Initials(text);
        initials.Text = letters;
        initials.FontSize = AvatarPalette.FontSize(size);
        initials.Foreground = foreground;
        initials.Visibility = letters.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        person.FontSize = AvatarPalette.SymbolSize(size);
        person.Foreground = foreground;
        person.Visibility = letters.Length > 0 || hasPhoto ? Visibility.Collapsed : Visibility.Visible;
    }
}
