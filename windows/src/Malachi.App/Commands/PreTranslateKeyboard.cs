// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/windows-port.md §11.5, spikes2/INPUT-SPIKES.md
// §1.2-§1.3, measured): while a WebView2 has the keyboard focus no XAML
// KeyboardAccelerator and no XAML key event fires, but every key reaches
// the island's InputPreTranslateKeyboardSource once, on the UI thread,
// before TranslateMessage, and can be swallowed there. This installs a
// handler through the source's COM interop (Microsoft.UI.Input.
// InputPreTranslateSource.Interop.h of InteractiveExperiences 2.1.9; no
// projection exists) and hands the key messages of the WebView2's focus
// window (a Chrome_WidgetWin_0 child in this process) to a callback, which
// says whether to swallow each. For messages to the island's own input
// window both methods fire, so only OnTreeMessage acts, and only for that
// class. The app is not trimmed, so the built-in COM interop of the spike
// is used; a trimmed or NativeAOT build would need the source-generated
// ComWrappers instead. Where the island gives no source, ThreadKeyboardHook
// takes its place.

using System;
using System.Diagnostics.CodeAnalysis;
using System.Runtime.InteropServices;
using Microsoft.UI.Input;
using Microsoft.UI.Xaml;
using Windows.Win32;
using Windows.Win32.Foundation;
using Windows.Win32.UI.WindowsAndMessaging;

namespace Malachi.App.Commands;

/// <summary>A key message for a focused WebView2.</summary>
/// <param name="Message">WM_KEYDOWN, WM_KEYUP, WM_SYSKEYDOWN or WM_SYSKEYUP.</param>
/// <param name="VirtualKey">The virtual-key code (wParam).</param>
/// <param name="Modifiers">FSHIFT (0x4), FCONTROL (0x8) and FALT (0x10), as the source reports them.</param>
/// <param name="Repeat">An auto-repeated key down (bit 30 of lParam).</param>
/// <param name="Window">The WebView2's focus window the message is for.</param>
internal readonly record struct WebViewKey(uint Message, int VirtualKey, uint Modifiers, bool Repeat, nint Window)
{
    /// <summary>A key down (WM_KEYDOWN or WM_SYSKEYDOWN).</summary>
    public bool IsDown => Message is PInvoke.WM_KEYDOWN or PInvoke.WM_SYSKEYDOWN;
}

/// <summary>Routes the key messages of a window's focused WebView2 to a callback.</summary>
internal sealed partial class PreTranslateKeyboard
{
    private const string WebViewFocusClass = "Chrome_WidgetWin_0";

    // Kept alive for as long as the source may call it.
    private Handler? handler;

    /// <summary>
    /// Installs the handler on the island of <paramref name="root"/>'s
    /// XamlRoot. <paramref name="onKey"/> returns true to swallow the
    /// message. False when the source or its interop could not be had
    /// (logged by the caller): the WebView2 then keeps its keys.
    /// </summary>
    [SuppressMessage("Interoperability", "CA1416", Justification = "Windows 11 only (TargetPlatformMinVersion 10.0.22000).")]
    public bool Install(XamlRoot root, Func<WebViewKey, bool> onKey)
    {
        ArgumentNullException.ThrowIfNull(root);
        ArgumentNullException.ThrowIfNull(onKey);
        var island = root.ContentIsland;
        if (island is null)
        {
            return false;
        }
        var source = InputPreTranslateKeyboardSource.GetForIsland(island);
        if (source is null)
        {
            return false;
        }
        var unknown = WinRT.MarshalInspectable<object>.FromManaged(source);
        try
        {
            var iid = typeof(IInputPreTranslateKeyboardSourceInterop).GUID;
            if (Marshal.QueryInterface(unknown, in iid, out var pointer) < 0)
            {
                return false;
            }
            try
            {
                var interop = (IInputPreTranslateKeyboardSourceInterop)Marshal.GetObjectForIUnknown(pointer);
                handler = new Handler(onKey);
                return interop.SetPreTranslateHandler(handler) >= 0;
            }
            finally
            {
                Marshal.Release(pointer);
            }
        }
        finally
        {
            Marshal.Release(unknown);
        }
    }

    /// <summary>Whether <paramref name="hwnd"/> is a WebView2's focus window (its class).</summary>
    internal static unsafe bool IsWebViewFocusWindow(HWND hwnd)
    {
        Span<char> name = stackalloc char[32];
        fixed (char* p = name)
        {
            var length = PInvoke.GetClassName(hwnd, p, name.Length);
            return length > 0 && name[..length].SequenceEqual(WebViewFocusClass);
        }
    }

    // Microsoft.UI.Input.InputPreTranslateSource.Interop.h.
    [ComImport]
    [Guid("C3244A48-DCB4-416C-901A-FFC5E50C2FFA")]
    [InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    private interface IInputPreTranslateKeyboardSourceInterop
    {
        [PreserveSig]
        int SetPreTranslateHandler(IInputPreTranslateKeyboardSourceHandler handler);
    }

    // keyboardModifiers: FVIRTKEY 0x1, FSHIFT 0x4, FCONTROL 0x8, FALT 0x10;
    // handled is a C++ bool (one byte).
    [ComImport]
    [Guid("9A4B69AA-E3BE-4590-95F5-3AAA7B12B260")]
    [InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    private interface IInputPreTranslateKeyboardSourceHandler
    {
        [PreserveSig]
        int OnDirectMessage(IntPtr source, ref MSG msg, uint keyboardModifiers, ref byte handled);

        [PreserveSig]
        int OnTreeMessage(IntPtr source, ref MSG msg, uint keyboardModifiers, ref byte handled);
    }

    [ComVisible(true)]
    [ClassInterface(ClassInterfaceType.None)]
    private sealed class Handler(Func<WebViewKey, bool> onKey) : IInputPreTranslateKeyboardSourceHandler
    {
        // The island's own input window: XAML's keys, left to XAML.
        public int OnDirectMessage(IntPtr source, ref MSG msg, uint keyboardModifiers, ref byte handled) => 0;

        [SuppressMessage("Design", "CA1031", Justification = "Nothing may escape into the input pipeline.")]
        public int OnTreeMessage(IntPtr source, ref MSG msg, uint keyboardModifiers, ref byte handled)
        {
            try
            {
                if (msg.message is PInvoke.WM_KEYDOWN or PInvoke.WM_KEYUP or PInvoke.WM_SYSKEYDOWN or PInvoke.WM_SYSKEYUP
                    && IsWebViewFocusWindow(msg.hwnd))
                {
                    var repeat = ((nint)msg.lParam.Value & (1 << 30)) != 0;
                    var key = new WebViewKey(msg.message, (int)(nuint)msg.wParam.Value, keyboardModifiers, repeat, msg.hwnd);
                    if (onKey(key))
                    {
                        handled = 1;
                    }
                }
            }
            catch (Exception)
            {
                // A key that cannot be judged goes to the WebView2.
            }
            return 0;
        }
    }
}
