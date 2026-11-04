// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Printing/PrintController.swift
// (PrintJob: the off-screen view, run, start, print, finish); GTK: the GTK
// port follows (docs/plans/toolbar-customization.md §8, WebKitPrintOperation).
//
// One print of a window: the document Core's PrintComposer built, loaded
// into a MessageWebView, the reader's locked view (script off, the same
// CSP, no network, the request gate, malachi-cid: through the cache), kept
// out of sight: under the window's content at a page's size, transparent,
// out of the hit test, the keyboard's tab order and UI Automation. Once the
// document finished (its pictures included) WebView2 lays it out as a PDF
// in memory, with the view's own policy, so nothing is fetched (A4, or
// Letter in a region of inches, with the page header and footer), and
// PrintPages gives its pages to Windows' print dialog with its preview.
//
// Windows: each window keeps its view and loads the next print into it
// (it closes with the window), one print at a time.
//
// DEVELOPMENT AID: with MALACHI_PRINT_PDF=<path> the document is saved as
// a PDF there (with the page header and footer) instead of the dialog,
// never on a printer.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Runtime.CompilerServices;
using System.Threading.Tasks;
using Malachi.App.Reader;
using Malachi.App.WebViews;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Printing;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls;
using Windows.Data.Pdf;

namespace Malachi.App.Printing;

/// <summary>The print view of one window (macOS <c>PrintJob</c>).</summary>
internal sealed partial class PrintJob
{
    // A4 at 96 DIP per inch; WebView2 lays the document out again for the
    // paper chosen.
    private const double PageWidth = 794;
    private const double PageHeight = 1123;

    private static readonly ConditionalWeakTable<Window, PrintJob> Jobs = [];

    private readonly MessageWebView view;
    private readonly ILogger logger;
    private readonly PrintPages pages;
    private PrintDocument? pending;

    private PrintJob(Window window, Panel root, MessageCache cache, ILogger logger)
    {
        this.logger = logger;
        view = new MessageWebView
        {
            Width = PageWidth,
            Height = PageHeight,
            Opacity = 0,
            IsHitTestVisible = false,
            IsTabStop = false,
        };
        AutomationProperties.SetAccessibilityView(view, AccessibilityView.Raw);
        view.UseCache(cache);
        view.DocumentLoaded += (_, _) => Loaded();
        view.Unavailable += (_, _) =>
        {
            if (pending is not null)
            {
                pending = null;
                LogUnavailable(logger);
            }
        };
        // A zero-sized canvas under everything: the view takes no room of
        // the window's layout.
        var host = new Canvas { Width = 0, Height = 0, IsHitTestVisible = false };
        AutomationProperties.SetAccessibilityView(host, AccessibilityView.Raw);
        host.Children.Add(view);
        // The sheets of the print dialog (PrintPages), transparent as the view.
        var sheets = new Canvas { Opacity = 0, IsHitTestVisible = false };
        AutomationProperties.SetAccessibilityView(sheets, AccessibilityView.Raw);
        host.Children.Add(sheets);
        pages = new PrintPages(window, sheets, logger);
        root.Children.Insert(0, host);
        window.Closed += (_, _) => view.Close();
    }

    /// <summary>
    /// Prints <paramref name="messages"/> in <paramref name="layout"/> from
    /// <paramref name="window"/> (a selection, a conversation, a message
    /// window's message; macOS <c>printMessages</c>).
    /// </summary>
    public static void Messages(Window window, ReaderServices services, IReadOnlyList<MessageSummary> messages, Print.Layout layout)
    {
        ArgumentNullException.ThrowIfNull(messages);
        _ = MakeAndRunAsync(window, services, c => c.MessagesAsync(messages, layout));
    }

    /// <summary>
    /// P8: prints <paramref name="calendar"/>'s selected event, else the day
    /// its view stands on, from <paramref name="window"/> (macOS <c>printCalendar</c>).
    /// </summary>
    public static void Calendar(Window window, ReaderServices services, CalendarController calendar)
    {
        ArgumentNullException.ThrowIfNull(calendar);
        _ = MakeAndRunAsync(window, services, c => c.CalendarAsync(calendar));
    }

    private static async Task MakeAndRunAsync(Window window, ReaderServices services, Func<PrintComposer, Task<PrintDocument?>> make)
    {
        ArgumentNullException.ThrowIfNull(window);
        ArgumentNullException.ThrowIfNull(services);
        var logger = services.State.Logs.CreateLogger<PrintJob>();
        var composer = new PrintComposer(services.Cache, services.Router.FolderRoleOf, logger);
        try
        {
            if (await make(composer) is { } document)
            {
                Run(window, document, services.Cache, logger);
            }
        }
#pragma warning disable CA1031 // The print's top: nothing else hears of a failure, so it is logged.
        catch (Exception e)
#pragma warning restore CA1031
        {
            LogFailed(logger, e.GetType().Name, e.HResult);
        }
    }

    /// <summary>
    /// Prints <paramref name="document"/> from <paramref name="window"/>: the
    /// system's print dialog over it once the document is loaded.
    /// </summary>
    public static void Run(Window window, PrintDocument document, MessageCache cache, ILogger logger)
    {
        ArgumentNullException.ThrowIfNull(window);
        ArgumentNullException.ThrowIfNull(document);
        ArgumentNullException.ThrowIfNull(cache);
        ArgumentNullException.ThrowIfNull(logger);
        if (window.Content is not Panel root)
        {
            LogNoRoot(logger);
            return;
        }
        var job = Jobs.GetValue(window, w => new PrintJob(w, root, cache, logger));
        if (job.pages.Busy)
        {
            // One print at a time in a window: its dialog is still open, or
            // its job still on the way to the printer.
            return;
        }
        job.pending = document;
        job.view.Load(document.Fragment, reload: true);
    }

    private void Loaded()
    {
        if (pending is not { } document)
        {
            return;
        }
        pending = null;
        _ = PrintAsync(document);
    }

    // The paper WebView2 lays the print out on: A4, or Letter where the
    // region measures in inches; the dialog's sheets scale it to theirs.
    private static (double Width, double Height) Paper() =>
        RegionInfo.CurrentRegion.IsMetric
            ? (8.27, 11.69)
            : (8.5, 11);

    private async Task PrintAsync(PrintDocument document)
    {
        var (width, height) = Paper();
        var path = Environment.GetEnvironmentVariable("MALACHI_PRINT_PDF");
        if (!string.IsNullOrEmpty(path))
        {
            var saved = await view.PrintToPdfAsync(path, document.Title, width, height);
            LogSaved(logger, saved, path);
            return;
        }
        // The PDF reads its stream while the dialog asks for pages: kept with it.
        var stream = await view.PrintToPdfStreamAsync(document.Title, width, height);
        if (stream is null)
        {
            LogUnavailable(logger);
            return;
        }
        try
        {
            var pdf = await PdfDocument.LoadFromStreamAsync(stream);
            await pages.ShowAsync(pdf, document.Title);
        }
#pragma warning disable CA1031 // The print's top: nothing else hears of a failure, so it is logged.
        catch (Exception e)
#pragma warning restore CA1031
        {
            LogFailed(logger, e.GetType().Name, e.HResult);
        }
    }

    [LoggerMessage(Level = LogLevel.Error, Message = "print: failed: {Kind} 0x{HResult:X8}")]
    private static partial void LogFailed(ILogger logger, string kind, int hResult);

    [LoggerMessage(Level = LogLevel.Error, Message = "print: the window has no panel to hold the print view")]
    private static partial void LogNoRoot(ILogger logger);

    [LoggerMessage(Level = LogLevel.Error, Message = "print: the locked view is unavailable")]
    private static partial void LogUnavailable(ILogger logger);

    [LoggerMessage(Level = LogLevel.Information, Message = "LAYOUT: print saved {Saved} to {Path}")]
    private static partial void LogSaved(ILogger logger, bool saved, string path);
}
