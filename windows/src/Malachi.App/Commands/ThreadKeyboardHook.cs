// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/windows-port.md §11.5; spikes2/INPUT-SPIKES.md
// §1.2 and §5, measured): the fallback of PreTranslateKeyboard, for a
// window whose island gives no pre-translate source. A thread-local
// WH_KEYBOARD hook on the UI thread (no DLL, no global hook) sees every key
// message the thread takes from its queue before it is dispatched,
// including those of a focused WebView2's Chrome_WidgetWin_0 focus window,
// which lives in this process on the UI thread; returning 1 swallows the
// message, and no WM_CHAR follows. Every window of the app shares the UI
// thread, so a window's hook acts only while the thread's focus window is a
// Chrome_WidgetWin_0 inside that window, and hands the key to the same
// callback the pre-translate source would. A key that is only peeked at
// (HC_NOREMOVE) is left alone: it comes again when it is taken.

using System;
using System.Diagnostics.CodeAnalysis;
using Windows.Win32;
using Windows.Win32.Foundation;
using Windows.Win32.UI.WindowsAndMessaging;

namespace Malachi.App.Commands;

/// <summary>A thread-local keyboard hook for the WebView2s of one window.</summary>
internal sealed class ThreadKeyboardHook : IDisposable
{
    // The keystroke flags of a WH_KEYBOARD lParam.
    private const long TransitionUp = 0x80000000L;
    private const long PreviouslyDown = 0x40000000L;
    private const long AltDown = 0x20000000L;

    private const int VkShift = 0x10;
    private const int VkControl = 0x11;
    private const int VkMenu = 0x12;

    private HWND window;
    private Func<WebViewKey, bool>? onKey;

    // Kept alive for as long as the hook is installed.
    private HOOKPROC? proc;
    private UnhookWindowsHookExSafeHandle? hook;

    /// <summary>
    /// Installs the hook on the calling (UI) thread for the WebView2s inside
    /// <paramref name="windowHandle"/>; <paramref name="callback"/> returns
    /// true to swallow a key. False when the hook could not be installed.
    /// </summary>
    public bool Install(nint windowHandle, Func<WebViewKey, bool> callback)
    {
        ArgumentNullException.ThrowIfNull(callback);
        Dispose();
        window = (HWND)windowHandle;
        onKey = callback;
        proc = OnHook;
        hook = PInvoke.SetWindowsHookEx(WINDOWS_HOOK_ID.WH_KEYBOARD, proc, null, PInvoke.GetCurrentThreadId());
        if (!hook.IsInvalid)
        {
            return true;
        }
        Dispose();
        return false;
    }

    /// <summary>Removes the hook.</summary>
    public void Dispose()
    {
        hook?.Dispose();
        hook = null;
        proc = null;
        onKey = null;
    }

    // FSHIFT, FCONTROL and FALT, as the pre-translate source reports them.
    private static uint Modifiers()
    {
        uint result = 0;
        if (PInvoke.GetKeyState(VkShift) < 0)
        {
            result |= 0x4;
        }
        if (PInvoke.GetKeyState(VkControl) < 0)
        {
            result |= 0x8;
        }
        if (PInvoke.GetKeyState(VkMenu) < 0)
        {
            result |= 0x10;
        }
        return result;
    }

    [SuppressMessage("Design", "CA1031", Justification = "Nothing may escape into the input pipeline.")]
    private LRESULT OnHook(int code, WPARAM wParam, LPARAM lParam)
    {
        try
        {
            if (code == (int)PInvoke.HC_ACTION && onKey is { } callback)
            {
                var focus = PInvoke.GetFocus();
                if (!focus.IsNull && PInvoke.IsChild(window, focus) && PreTranslateKeyboard.IsWebViewFocusWindow(focus))
                {
                    long flags = lParam.Value;
                    var up = (flags & TransitionUp) != 0;
                    var alt = (flags & AltDown) != 0;
                    var message = (up, alt) switch
                    {
                        (false, false) => PInvoke.WM_KEYDOWN,
                        (false, true) => PInvoke.WM_SYSKEYDOWN,
                        (true, false) => PInvoke.WM_KEYUP,
                        (true, true) => PInvoke.WM_SYSKEYUP,
                    };
                    var repeat = !up && (flags & PreviouslyDown) != 0;
                    var key = new WebViewKey(message, (int)(nuint)wParam.Value, Modifiers(), repeat, focus);
                    if (callback(key))
                    {
                        return (LRESULT)1;
                    }
                }
            }
        }
        catch (Exception)
        {
            // A key that cannot be judged goes to the WebView2.
        }
        return PInvoke.CallNextHookEx(default(HHOOK), code, wParam, lParam);
    }
}
