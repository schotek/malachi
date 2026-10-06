// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// UI Automation helpers over System.Windows.Automation: the app's windows
// found by process (through their handles, since a window the app owns is
// no child of the desktop in the UIA tree), elements by AutomationId (the
// names are translated, the ids are not), the patterns the tests use, and
// waiting with a deadline instead of sleeping. Only patterns, no synthetic
// input: the tests run beside other windows and never need the foreground,
// except PressKey for what only a key does (the board panel's Escape).

using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Threading;
using System.Windows.Automation;

namespace Malachi.App.UiTests;

/// <summary>UI Automation over the app's windows.</summary>
internal static class Uia
{
    /// <summary>What a WinUI 3 top-level window's class is.</summary>
    public const string WinUIWindowClass = "WinUIDesktopWin32WindowClass";

    /// <summary>How long a step waits for the UI by default.</summary>
    public static readonly TimeSpan Timeout = TimeSpan.FromSeconds(20);

    private static readonly TimeSpan Poll = TimeSpan.FromMilliseconds(150);

    /// <summary>The visible top-level WinUI windows of process <paramref name="processId"/>.</summary>
    public static List<AutomationElement> Windows(int processId)
    {
        var found = new List<AutomationElement>();
        nint window = 0;
        while ((window = NativeMethods.FindWindowEx(0, window, WinUIWindowClass, null)) != 0)
        {
            NativeMethods.GetWindowThreadProcessId(window, out var owner);
            if (owner != processId || !NativeMethods.IsWindowVisible(window))
            {
                continue;
            }
            try
            {
                found.Add(AutomationElement.FromHandle(window));
            }
            catch (ElementNotAvailableException)
            {
                // Closed meanwhile.
            }
        }
        return found;
    }

    /// <summary>
    /// The first window of <paramref name="processId"/> that has an element
    /// <paramref name="automationId"/>, once there is one.
    /// </summary>
    public static AutomationElement WindowWith(int processId, string automationId, TimeSpan? timeout = null) =>
        Until(() =>
        {
            foreach (var w in Windows(processId))
            {
                if (TryFind(w, automationId) is not null)
                {
                    return w;
                }
            }
            return null;
        }, timeout, "a window with " + automationId);

    /// <summary>The element <paramref name="automationId"/> below <paramref name="root"/>, once it is there.</summary>
    public static AutomationElement Find(AutomationElement root, string automationId, TimeSpan? timeout = null) =>
        Until(() => TryFind(root, automationId), timeout, automationId);

    /// <summary>The element <paramref name="automationId"/> below <paramref name="root"/> now, or null.</summary>
    public static AutomationElement? TryFind(AutomationElement root, string automationId)
    {
        ArgumentNullException.ThrowIfNull(root);
        try
        {
            return root.FindFirst(TreeScope.Descendants, new PropertyCondition(AutomationElement.AutomationIdProperty, automationId));
        }
        catch (ElementNotAvailableException)
        {
            return null;
        }
    }

    /// <summary>Every element of <paramref name="type"/> below <paramref name="root"/>.</summary>
    public static AutomationElementCollection All(AutomationElement root, ControlType type)
    {
        ArgumentNullException.ThrowIfNull(root);
        return root.FindAll(TreeScope.Descendants, new PropertyCondition(AutomationElement.ControlTypeProperty, type));
    }

    /// <summary>Invokes a button or a menu item.</summary>
    public static void Invoke(AutomationElement element) =>
        ((InvokePattern)element.GetCurrentPattern(InvokePattern.Pattern)).Invoke();

    /// <summary>Selects a list item.</summary>
    public static void Select(AutomationElement element) =>
        ((SelectionItemPattern)element.GetCurrentPattern(SelectionItemPattern.Pattern)).Select();

    /// <summary>Sets a text field's value.</summary>
    public static void SetValue(AutomationElement element, string value) =>
        ((ValuePattern)element.GetCurrentPattern(ValuePattern.Pattern)).SetValue(value);

    /// <summary>A text field's value.</summary>
    public static string Value(AutomationElement element) =>
        ((ValuePattern)element.GetCurrentPattern(ValuePattern.Pattern)).Current.Value;

    /// <summary>Toggles a toggle button or a checkable menu item.</summary>
    public static void Toggle(AutomationElement element) =>
        ((TogglePattern)element.GetCurrentPattern(TogglePattern.Pattern)).Toggle();

    /// <summary>
    /// Presses virtual key <paramref name="key"/> with the keyboard focus on
    /// <paramref name="element"/>: the one exception to patterns only, for
    /// what only a key does (the board panel's Escape). The focus brings the
    /// element's window to the foreground; the key goes out only once that
    /// window of <paramref name="processId"/> has it, never to another one.
    /// </summary>
    public static void PressKey(AutomationElement element, int processId, byte key)
    {
        ArgumentNullException.ThrowIfNull(element);
        element.SetFocus();
        WaitFor(
            () =>
            {
                NativeMethods.GetWindowThreadProcessId(NativeMethods.GetForegroundWindow(), out var owner);
                return owner == processId && element.Current.HasKeyboardFocus;
            },
            "the keyboard focus in the app");
        NativeMethods.KeybdEvent(key, 0, 0, 0);
        NativeMethods.KeybdEvent(key, 0, NativeMethods.KeyEventKeyUp, 0);
    }

    /// <summary>Asks a window to close (its caption's Close, WM_CLOSE).</summary>
    public static void Close(AutomationElement window) =>
        ((WindowPattern)window.GetCurrentPattern(WindowPattern.Pattern)).Close();

    /// <summary>Whether <paramref name="window"/> is gone (closed or hidden).</summary>
    public static bool IsGone(AutomationElement window)
    {
        try
        {
            var handle = window.Current.NativeWindowHandle;
            return handle == 0 || !NativeMethods.IsWindowVisible(handle);
        }
        catch (ElementNotAvailableException)
        {
            return true;
        }
    }

    /// <summary>Waits until <paramref name="window"/> is gone.</summary>
    public static void WaitGone(AutomationElement window, TimeSpan? timeout = null, string what = "the window") =>
        Until(() => IsGone(window) ? window : null, timeout, what + " to close");

    /// <summary>Waits until <paramref name="condition"/> holds.</summary>
    public static void WaitFor(Func<bool> condition, string what, TimeSpan? timeout = null) =>
        Until(() => condition() ? (object)true : null, timeout, what);

    /// <summary>Polls <paramref name="probe"/> until it answers, or fails the test naming <paramref name="what"/>.</summary>
    public static T Until<T>(Func<T?> probe, TimeSpan? timeout, string what)
        where T : class
    {
        var clock = Stopwatch.StartNew();
        var limit = timeout ?? Timeout;
        while (true)
        {
            T? value = null;
            try
            {
                value = probe();
            }
            catch (ElementNotAvailableException)
            {
                // The tree changed under the probe: try again.
            }
            if (value is not null)
            {
                return value;
            }
            if (clock.Elapsed > limit)
            {
                throw new TimeoutException($"waited {limit.TotalSeconds:0} s for {what}");
            }
            Thread.Sleep(Poll);
        }
    }
}
