// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only (docs/windows-port.md §6.6): the content of the attachment
// previewer, the decided replacement for GNOME Sushi (ui/internal/preview)
// and Quick Look (macos AttachmentActions). Like them it renders and never
// runs anything: its own hardened view (script off, InPrivate profile
// "preview", the request gate, downloads cancelled, the PDF viewer's Save,
// Save As, Print, Full screen and More settings hidden), fed with the bytes
// message.part returned, served from memory as
// malachi-doc://preview/<generation>-<nonce> with the type PreviewContent
// sniffed: pictures (never SVG), PDF, and text in its own escaped document,
// HTML, SVG, XML and messages as their source. Nothing is written to disk.
// Anything else, and everything when the view is unavailable, gets the
// panel: Windows' icon for the extension, the name, the size and the type.
// Links in a PDF or text are not followed (every navigation is cancelled,
// as in the editor). The window around it (title, Open, Save As…, Escape)
// is the reader's.

using System;
using System.Runtime.InteropServices.WindowsRuntime;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.Platform;
using Malachi.Core.Presentation;
using Microsoft.UI.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media.Imaging;

namespace Malachi.App.WebViews;

/// <summary>Shows one attachment: rendered when it is a picture, a PDF or text, as a panel otherwise.</summary>
public sealed partial class PreviewWebView : HardenedWebView
{
    private const int IconPixels = 256;
    private const double IconSize = 128;

    private readonly StackPanel panelView;
    private readonly Image icon;
    private readonly FontIcon genericIcon;
    private readonly TextBlock nameText;
    private readonly TextBlock sizeText;
    private readonly TextBlock typeText;

    private (Attachment Attachment, string? ContentType, byte[] Data)? shown;

    /// <summary>An empty previewer.</summary>
    public PreviewWebView()
        : base(WebViewKind.Preview)
    {
        icon = new Image { Width = IconSize, Height = IconSize, Stretch = Microsoft.UI.Xaml.Media.Stretch.Uniform };
        genericIcon = new FontIcon { Glyph = "", FontSize = 96, Visibility = Visibility.Collapsed };
        nameText = new TextBlock
        {
            FontSize = 20,
            FontWeight = FontWeights.SemiBold,
            TextWrapping = TextWrapping.WrapWholeWords,
            TextAlignment = TextAlignment.Center,
            HorizontalAlignment = HorizontalAlignment.Center,
            Margin = new Thickness(0, 12, 0, 4),
            MaxWidth = 480,
        };
        sizeText = Secondary();
        typeText = Secondary();
        panelView = new StackPanel
        {
            HorizontalAlignment = HorizontalAlignment.Center,
            VerticalAlignment = VerticalAlignment.Center,
            Padding = new Thickness(24),
            Visibility = Visibility.Collapsed,
            Children = { icon, genericIcon, nameText, sizeText, typeText },
        };
        // The icons are decoration: Narrator reads the name, size and type.
        AutomationProperties.SetAccessibilityView(icon, AccessibilityView.Raw);
        AutomationProperties.SetAccessibilityView(genericIcon, AccessibilityView.Raw);
        Root.Children.Add(panelView);
        Web.Visibility = Visibility.Collapsed;
    }

    /// <summary>What the shell knows about file types (the panel's icon and type name); null leaves them generic.</summary>
    public IFileTypeInfo? FileTypes { get; set; }

    /// <summary>
    /// The platform's list of what it would run (FileTypePolicy: DangerousTypes
    /// and AssocIsDangerous); such a file gets the panel only, whatever its
    /// bytes are. Without it <see cref="PreviewContent.Classify"/> still
    /// applies <see cref="DangerousTypes"/>.
    /// </summary>
    public IFileTypePolicy? TypePolicy { get; set; }

    /// <summary>How the current attachment is shown; <see cref="PreviewKind.None"/> for the panel (or nothing yet).</summary>
    public PreviewKind Shown { get; private set; }

    /// <summary>The panel on display, when <see cref="Shown"/> is <see cref="PreviewKind.None"/> and an attachment is set.</summary>
    public PreviewPanel? Panel { get; private set; }

    /// <summary>
    /// Shows <paramref name="data"/>, the bytes of <paramref name="attachment"/>
    /// as <c>message.part</c> returned them with <paramref name="contentType"/>
    /// (null: the listed type).
    /// </summary>
    public void Show(Attachment attachment, string? contentType, ReadOnlyMemory<byte> data)
    {
        ArgumentNullException.ThrowIfNull(attachment);
        var bytes = data.ToArray();
        shown = (attachment, contentType, bytes);
        var claimed = contentType ?? attachment.ContentType;
        var content = TypePolicy is { } policy && policy.IsDangerous(attachment.Filename, claimed)
            ? PreviewContent.Nothing
            : PreviewContent.Classify(attachment.Filename, claimed, bytes);
        switch (content.Kind)
        {
            case PreviewKind.Image:
                ShowDocument(content.Kind, bytes, content.MediaType, PreviewDocument.MediaCsp);
                break;
            case PreviewKind.Pdf:
                // Embedded in a page of its own: a PDF served as the document
                // names the view's window with its own /Title.
                Shown = content.Kind;
                Panel = null;
                panelView.Visibility = Visibility.Collapsed;
                Web.Visibility = Visibility.Visible;
                LoadDocument(uri => Encoding.UTF8.GetBytes(PreviewDocument.PdfPage(uri)), "text/html; charset=utf-8",
                    PreviewDocument.PdfCsp, bytes, content.MediaType);
                break;
            case PreviewKind.Text:
                ShowDocument(content.Kind, Encoding.UTF8.GetBytes(PreviewDocument.Text(content.Text)), "text/html; charset=utf-8", PreviewDocument.Csp);
                break;
            default:
                ShowPanel(attachment, bytes.LongLength);
                break;
        }
    }

    /// <summary>
    /// Shows the panel of <paramref name="attachment"/> without rendering it
    /// (its bytes could not be fetched, or are not wanted);
    /// <paramref name="size"/> is their length when known.
    /// </summary>
    public void ShowPanel(Attachment attachment, long? size)
    {
        ArgumentNullException.ThrowIfNull(attachment);
        DropDocument();
        Shown = PreviewKind.None;
        var panel = PreviewPanel.For(attachment, size, FileTypes?.TypeName(PreviewPanel.IconExtensionOf(attachment.Filename)));
        Panel = panel;
        nameText.Text = panel.Name;
        sizeText.Text = panel.SizeText;
        typeText.Text = panel.TypeText;
        SetIcon(FileTypes?.Icon(panel.IconExtension, IconPixels));
        Web.Visibility = Visibility.Collapsed;
        panelView.Visibility = Visibility.Visible;
    }

    /// <summary>Shows nothing (the window is hidden for reuse).</summary>
    public void Clear()
    {
        shown = null;
        DropDocument();
        Shown = PreviewKind.None;
        Panel = null;
        Web.Visibility = Visibility.Collapsed;
        panelView.Visibility = Visibility.Collapsed;
    }

    private protected override void OnUnavailable()
    {
        if (shown is { } s)
        {
            ShowPanel(s.Attachment, s.Data.LongLength);
        }
    }

    private protected override void OnRendererLost()
    {
        if (shown is { } s && Shown != PreviewKind.None)
        {
            Show(s.Attachment, s.ContentType, s.Data);
        }
    }

    private protected override void OnWebViewReplaced(WebView2 web) =>
        web.Visibility = Shown == PreviewKind.None ? Visibility.Collapsed : Visibility.Visible;

    private void ShowDocument(PreviewKind kind, byte[] bytes, string mediaType, string csp)
    {
        Shown = kind;
        Panel = null;
        panelView.Visibility = Visibility.Collapsed;
        Web.Visibility = Visibility.Visible;
        LoadDocument(bytes, mediaType, csp);
    }

    private void SetIcon(FileIcon? fileIcon)
    {
        if (fileIcon is null || fileIcon.Width <= 0 || fileIcon.Height <= 0
            || fileIcon.Pixels.Length != fileIcon.Width * fileIcon.Height * 4)
        {
            icon.Source = null;
            icon.Visibility = Visibility.Collapsed;
            genericIcon.Visibility = Visibility.Visible;
            return;
        }
        var bitmap = new WriteableBitmap(fileIcon.Width, fileIcon.Height);
        using (var pixels = bitmap.PixelBuffer.AsStream())
        {
            pixels.Write(fileIcon.Pixels.Span);
        }
        bitmap.Invalidate();
        var size = Math.Min(IconSize, fileIcon.Width);
        icon.Width = size;
        icon.Height = size;
        icon.Source = bitmap;
        icon.Visibility = Visibility.Visible;
        genericIcon.Visibility = Visibility.Collapsed;
    }

    private static TextBlock Secondary() => new()
    {
        Opacity = 0.7,
        TextAlignment = TextAlignment.Center,
        HorizontalAlignment = HorizontalAlignment.Center,
        TextWrapping = TextWrapping.WrapWholeWords,
        MaxWidth = 480,
    };
}
