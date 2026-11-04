// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/plans/toolbar-customization.md §8): the pages of
// a print handed to Windows' own print dialog. WebView2 lays the print out
// as a PDF in memory (PrintJob), Windows.Data.Pdf draws its pages, and a
// XAML PrintDocument gives them to the dialog through the window's
// PrintManager (PrintManagerInterop), which shows the preview and the
// application's name. WebView2's own dialogs do neither: the system one
// is the WebView2 process's ("Microsoft Edge WebView2 – Print", no
// preview), the browser one draws over the view, which is hidden. The
// pages are pictures: the preview's at the screen's resolution, the
// printer's at PrintDpi, drawn one by one as the dialog asks for them.
// One PrintManager per window, and one print at a time in it.

using System;
using System.Runtime.InteropServices;
using System.Threading.Tasks;
using Microsoft.Extensions.Logging;
using Microsoft.UI;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.Media.Imaging;
using Microsoft.UI.Xaml.Printing;
using Windows.Data.Pdf;
using Windows.Graphics.Imaging;
using Windows.Graphics.Printing;
using Windows.Storage.Streams;
using WinRT.Interop;
using XamlPrintDocument = Microsoft.UI.Xaml.Printing.PrintDocument;

namespace Malachi.App.Printing;

/// <summary>The window's print dialog over the pages of a PDF.</summary>
internal sealed partial class PrintPages
{
    // The printer's pictures: sharp text without a page's picture growing
    // past ~30 MB (A4 at 200 dpi is 1654 × 2339).
    private const double PrintDpi = 200;
    private const double ScreenDpi = 96;

    private readonly IntPtr hwnd;
    private readonly ILogger logger;
    private readonly DispatcherQueue dispatcher;
    private readonly Panel holder;
    private PdfDocument? pdf;
    private string title = "";
    private XamlPrintDocument? document;
    private IPrintDocumentSource? source;
    private PrintTaskOptions? options;

    /// <param name="window">The window whose print dialog it is.</param>
    /// <param name="holder">A transparent panel in the window's tree: the sheets are laid out there while the dialog shows them (XAML prints only elements with a layout).</param>
    /// <param name="logger">The log.</param>
    public PrintPages(Window window, Panel holder, ILogger logger)
    {
        ArgumentNullException.ThrowIfNull(window);
        ArgumentNullException.ThrowIfNull(holder);
        this.holder = holder;
        this.logger = logger;
        dispatcher = DispatcherQueue.GetForCurrentThread();
        hwnd = WindowNative.GetWindowHandle(window);
        PrintManagerInterop.GetForWindow(hwnd).PrintTaskRequested += OnTaskRequested;
    }

    /// <summary>Whether a print of this window is in its dialog or on its way to the printer.</summary>
    public bool Busy => pdf is not null;

    /// <summary>Shows the print dialog for <paramref name="pages"/>, the job named <paramref name="jobTitle"/>.</summary>
    public async Task ShowAsync(PdfDocument pages, string jobTitle)
    {
        ArgumentNullException.ThrowIfNull(pages);
        pdf = pages;
        title = jobTitle;
        document = new XamlPrintDocument();
        source = document.DocumentSource;
        document.Paginate += OnPaginate;
        document.GetPreviewPage += OnPreviewPage;
        document.AddPages += OnAddPages;
        try
        {
            if (!await PrintManagerInterop.ShowPrintUIForWindowAsync(hwnd))
            {
                Clear();
            }
        }
        catch (COMException e)
        {
            LogDialogFailed(logger, e.HResult);
            Clear();
        }
    }

    private void OnTaskRequested(PrintManager sender, PrintTaskRequestedEventArgs args)
    {
        if (source is not { } s)
        {
            return;
        }
        var task = args.Request.CreatePrintTask(title, r => r.SetSource(s));
        // Printed, cancelled or failed; the dialog closed without a job
        // ends ShowAsync instead (or both).
        task.Completed += (_, _) => dispatcher.TryEnqueue(Clear);
    }

    private void OnPaginate(object sender, PaginateEventArgs e)
    {
        options = e.PrintTaskOptions;
        if (document is { } d && pdf is { } p)
        {
            d.SetPreviewPageCount((int)p.PageCount, PreviewPageCountType.Final);
        }
    }

    private void OnPreviewPage(object sender, GetPreviewPageEventArgs e) => _ = PreviewAsync(e.PageNumber);

    private async Task PreviewAsync(int number)
    {
        if (document is not { } d || pdf is not { } p || number < 1 || number > p.PageCount)
        {
            return;
        }
        var page = await PageAsync(p, (uint)number - 1, ScreenDpi);
        if (ReferenceEquals(d, document))
        {
            d.SetPreviewPage(number, page);
        }
    }

    private void OnAddPages(object sender, AddPagesEventArgs e) => _ = AddAllAsync();

    private async Task AddAllAsync()
    {
        if (document is not { } d || pdf is not { } p)
        {
            return;
        }
        try
        {
            for (uint i = 0; i < p.PageCount; i++)
            {
                d.AddPage(await PageAsync(p, i, PrintDpi));
            }
        }
        catch (COMException e)
        {
            LogPageFailed(logger, e.HResult);
        }
        finally
        {
            d.AddPagesComplete();
        }
    }

    // One page of the PDF as a picture on a sheet of the paper chosen, inside
    // the part the printer prints, scaled to it.
    private async Task<UIElement> PageAsync(PdfDocument p, uint index, double dpi)
    {
        using var page = p.GetPage(index);
        var scale = dpi / ScreenDpi;
        using var stream = new InMemoryRandomAccessStream();
        await page.RenderToStreamAsync(stream, new PdfPageRenderOptions
        {
            DestinationWidth = (uint)Math.Max(1, Math.Round(page.Size.Width * scale)),
            DestinationHeight = (uint)Math.Max(1, Math.Round(page.Size.Height * scale)),
            BackgroundColor = Colors.White,
        });
        stream.Seek(0);
        // A BitmapImage, decoded before the sheet is handed on: XAML's print
        // draws no SoftwareBitmapSource (a composition surface), measured.
        var picture = new BitmapImage();
        var opened = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        picture.ImageOpened += (_, _) => opened.TrySetResult();
        picture.ImageFailed += (_, _) => opened.TrySetResult();

        var size = page.Size;
        var imageable = new Windows.Foundation.Rect(0, 0, size.Width, size.Height);
        if (options is { } o)
        {
            var sheet = o.GetPageDescription(index + 1);
            size = sheet.PageSize;
            imageable = sheet.ImageableRect;
        }
        var image = new Image { Source = picture, Stretch = Stretch.Uniform, Width = imageable.Width, Height = imageable.Height };
        Canvas.SetLeft(image, imageable.X);
        Canvas.SetTop(image, imageable.Y);
        var sheetView = new Canvas { Width = size.Width, Height = size.Height, Background = new SolidColorBrush(Colors.White) };
        sheetView.Children.Add(image);
        holder.Children.Add(sheetView);
        await picture.SetSourceAsync(stream);
        await opened.Task.WaitAsync(TimeSpan.FromSeconds(10));
        sheetView.UpdateLayout();
        return sheetView;
    }

    private void Clear()
    {
        if (document is { } d)
        {
            d.Paginate -= OnPaginate;
            d.GetPreviewPage -= OnPreviewPage;
            d.AddPages -= OnAddPages;
        }
        document = null;
        source = null;
        options = null;
        pdf = null;
        holder.Children.Clear();
    }

    [LoggerMessage(Level = LogLevel.Error, Message = "print: the print dialog failed: 0x{HResult:X8}")]
    private static partial void LogDialogFailed(ILogger logger, int hResult);

    [LoggerMessage(Level = LogLevel.Error, Message = "print: a page could not be drawn: 0x{HResult:X8}")]
    private static partial void LogPageFailed(ILogger logger, int hResult);
}
