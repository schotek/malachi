// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// A DEVELOPMENT AID for the `shot=` step of MALACHI_START
// (MainWindow.DevStart.cs; docs/screenshots.md §6): one window of this
// process as a PNG at the screen's scale, the counterpart of the window
// server's image the macOS client takes
// (MainWindowController+DevStart.swift developmentCapture) and of the X
// server's image the GTK driver takes. The rectangle is the frame without
// the invisible resize borders (DwmGetWindowAttribute with
// DWMWA_EXTENDED_FRAME_BOUNDS); the pixels are copied from the screen
// (BitBlt with CAPTUREBLT: what the user sees, Mica, the WebView2s, a
// ContentDialog), which needs the window in front, or, when it could not be
// brought there, rendered off screen by PrintWindow with
// PW_RENDERFULLCONTENT (an inactive caption then). Nothing of it runs
// without the variable.

using System;
using System.IO;
using System.Threading.Tasks;
using Microsoft.UI.Xaml;
using Windows.Graphics.Imaging;
using Windows.Storage.Streams;
using Windows.Win32;
using Windows.Win32.Foundation;
using Windows.Win32.Graphics.Dwm;
using Windows.Win32.Graphics.Gdi;
using Windows.Win32.Storage.Xps;

namespace Malachi.App.Shell;

/// <summary>The picture of a window, for the development aid.</summary>
internal static class WindowCapture
{
    // PW_RENDERFULLCONTENT (winuser.h): PrintWindow renders DirectComposition
    // content too (the XAML island, the WebView2s), not only what GDI drew.
    private const uint RenderFullContent = 0x00000002;

    /// <summary><see cref="BringToFront"/>: the window took the foreground by itself.</summary>
    public const string Direct = "direct";

    /// <summary><see cref="BringToFront"/>: the window took the foreground through the thread that had it.</summary>
    public const string Attached = "attached";

    /// <summary><see cref="BringToFront"/>: the window is not in front.</summary>
    public const string Behind = "behind";

    /// <summary>
    /// Shows and activates <paramref name="window"/> and tries to make it
    /// the foreground window (Windows lets a process that did not get the
    /// last input take the foreground only through the thread that has it);
    /// how it went: <see cref="Direct"/>, <see cref="Attached"/> or
    /// <see cref="Behind"/>.
    /// </summary>
    public static unsafe string BringToFront(Window window)
    {
        var hwnd = (HWND)WindowPresenter.Handle(window);
        WindowPresenter.Present(window);
        if (PInvoke.GetForegroundWindow() == hwnd)
        {
            return Direct;
        }
        var front = PInvoke.GetForegroundWindow();
        var me = PInvoke.GetCurrentThreadId();
        var theirs = front.IsNull ? 0 : PInvoke.GetWindowThreadProcessId(front, null);
        if (theirs != 0 && theirs != me && PInvoke.AttachThreadInput(theirs, me, true))
        {
            try
            {
                PInvoke.BringWindowToTop(hwnd);
                PInvoke.SetForegroundWindow(hwnd);
            }
            finally
            {
                PInvoke.AttachThreadInput(theirs, me, false);
            }
        }
        return PInvoke.GetForegroundWindow() == hwnd ? Attached : Behind;
    }

    /// <summary>The picture's size in physical pixels (<see cref="Bounds"/>).</summary>
    public static (int Width, int Height) FrameSize(Window window)
    {
        var frame = Bounds((HWND)WindowPresenter.Handle(window));
        return (frame.right - frame.left, frame.bottom - frame.top);
    }

    /// <summary>
    /// The window's rectangle, its visible frame and its client area, in
    /// physical screen pixels, for the development output.
    /// </summary>
    public static string Rectangles(Window window)
    {
        var hwnd = (HWND)WindowPresenter.Handle(window);
        PInvoke.GetWindowRect(hwnd, out var rect);
        var frame = FrameBounds(hwnd);
        var client = ClientBounds(hwnd);
        return $"window {Describe(rect)} frame {Describe(frame)} client {Describe(client)}";
    }

    /// <summary>
    /// Writes <paramref name="window"/>'s visible frame as a PNG to
    /// <paramref name="path"/>: from the screen when <paramref name="fromScreen"/>
    /// (the window is in front), else rendered by PrintWindow. Returns a
    /// line saying the size and the way.
    /// </summary>
    public static async Task<string> SavePngAsync(Window window, string path, bool fromScreen)
    {
        var hwnd = (HWND)WindowPresenter.Handle(window);
        var (pixels, width, height, method) = Capture(hwnd, fromScreen);
        using var stream = new InMemoryRandomAccessStream();
        var encoder = await BitmapEncoder.CreateAsync(BitmapEncoder.PngEncoderId, stream);
        encoder.SetPixelData(BitmapPixelFormat.Bgra8, BitmapAlphaMode.Ignore, (uint)width, (uint)height, 96, 96, pixels);
        await encoder.FlushAsync();
        var bytes = new byte[stream.Size];
        using (var reader = new DataReader(stream.GetInputStreamAt(0)))
        {
            await reader.LoadAsync((uint)stream.Size);
            reader.ReadBytes(bytes);
        }
        if (Path.GetDirectoryName(path) is { Length: > 0 } directory)
        {
            Directory.CreateDirectory(directory);
        }
        await File.WriteAllBytesAsync(path, bytes);
        return $"{width}x{height} ({method})";
    }

    // The extended frame bounds: the window without its invisible resize
    // borders, but with its one-pixel border lines, through which (the top
    // one at least, translucent) the window behind shows.
    private static unsafe RECT FrameBounds(HWND hwnd)
    {
        RECT frame;
        var result = PInvoke.DwmGetWindowAttribute(hwnd, DWMWINDOWATTRIBUTE.DWMWA_EXTENDED_FRAME_BOUNDS, &frame, (uint)sizeof(RECT));
        if (result.Failed || !PInvoke.GetWindowRect(hwnd, out var rect))
        {
            throw new InvalidOperationException($"the window's frame could not be read (0x{result.Value:X8})");
        }
        if (frame.right <= frame.left || frame.bottom <= frame.top)
        {
            frame = rect;
        }
        return frame;
    }

    // The client area in screen pixels: what the window paints itself.
    private static RECT ClientBounds(HWND hwnd)
    {
        if (!PInvoke.GetClientRect(hwnd, out var client))
        {
            throw new InvalidOperationException("the window's client area could not be read");
        }
        var origin = new System.Drawing.Point(0, 0);
        PInvoke.ClientToScreen(hwnd, ref origin);
        return new RECT(origin.X, origin.Y, origin.X + client.right, origin.Y + client.bottom);
    }

    // The picture: the visible frame without its border lines, the part of
    // it the window paints (the frame and the client area intersected; the
    // frame alone when they do not overlap).
    private static RECT Bounds(HWND hwnd)
    {
        var frame = FrameBounds(hwnd);
        var client = ClientBounds(hwnd);
        var bounds = new RECT(
            Math.Max(frame.left, client.left), Math.Max(frame.top, client.top),
            Math.Min(frame.right, client.right), Math.Min(frame.bottom, client.bottom));
        return bounds.right > bounds.left && bounds.bottom > bounds.top ? bounds : frame;
    }

    private static string Describe(RECT r) => $"{r.left},{r.top} {r.right - r.left}x{r.bottom - r.top}";

    // The picture's pixels, top-down BGRA with an opaque alpha.
    private static unsafe (byte[] Pixels, int Width, int Height, string Method) Capture(HWND hwnd, bool fromScreen)
    {
        var frame = Bounds(hwnd);
        PInvoke.GetWindowRect(hwnd, out var rect);
        var width = frame.right - frame.left;
        var height = frame.bottom - frame.top;
        var screen = PInvoke.GetDC(HWND.Null);
        if (screen.IsNull)
        {
            throw new InvalidOperationException("no screen device context");
        }
        try
        {
            var memory = PInvoke.CreateCompatibleDC(screen);
            try
            {
                string method;
                byte[] pixels;
                if (fromScreen)
                {
                    pixels = Draw(memory, width, height, dc =>
                        PInvoke.BitBlt(dc, 0, 0, width, height, screen, frame.left, frame.top, ROP_CODE.SRCCOPY | ROP_CODE.CAPTUREBLT));
                    method = "screen";
                }
                else
                {
                    // The whole window rectangle, the invisible borders
                    // included, then the picture cut out of it.
                    var all = rect.right - rect.left;
                    var tall = rect.bottom - rect.top;
                    var whole = Draw(memory, all, tall, dc => PInvoke.PrintWindow(hwnd, dc, (PRINT_WINDOW_FLAGS)RenderFullContent));
                    pixels = new byte[width * height * 4];
                    var left = frame.left - rect.left;
                    var top = frame.top - rect.top;
                    for (var y = 0; y < height; y++)
                    {
                        System.Buffer.BlockCopy(whole, (((top + y) * all) + left) * 4, pixels, y * width * 4, width * 4);
                    }
                    method = "PrintWindow";
                }
                for (var i = 3; i < pixels.Length; i += 4)
                {
                    pixels[i] = 255;
                }
                return (pixels, width, height, method);
            }
            finally
            {
                PInvoke.DeleteDC(memory);
            }
        }
        finally
        {
            _ = PInvoke.ReleaseDC(HWND.Null, screen);
        }
    }

    // A top-down 32-bit DIB of the size selected into dc, drawn by `draw`, read out.
    private static unsafe byte[] Draw(HDC dc, int width, int height, Func<HDC, BOOL> draw)
    {
        var info = default(BITMAPINFO);
        info.bmiHeader.biSize = (uint)sizeof(BITMAPINFOHEADER);
        info.bmiHeader.biWidth = width;
        info.bmiHeader.biHeight = -height;
        info.bmiHeader.biPlanes = 1;
        info.bmiHeader.biBitCount = 32;
        void* bits;
        var dib = PInvoke.CreateDIBSection(dc, &info, DIB_USAGE.DIB_RGB_COLORS, &bits, HANDLE.Null, 0);
        if (dib.IsNull || bits is null)
        {
            throw new InvalidOperationException($"no bitmap of {width}x{height}");
        }
        try
        {
            var previous = PInvoke.SelectObject(dc, dib);
            try
            {
                if (!draw(dc))
                {
                    throw new InvalidOperationException("the window could not be drawn");
                }
                PInvoke.GdiFlush();
                return new ReadOnlySpan<byte>(bits, width * height * 4).ToArray();
            }
            finally
            {
                PInvoke.SelectObject(dc, previous);
            }
        }
        finally
        {
            PInvoke.DeleteObject(dib);
        }
    }
}
