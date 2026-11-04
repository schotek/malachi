// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/windows-port.md §10, the show path): what a
// double click that opens a window waits for. WinUI raises DoubleTapped on
// the second press of a mouse's double click, and a window shown then comes
// to the front; the release of that press goes to the window the gesture
// started in, which takes the activation back (measured 2026-10-17 with the
// New Event window of a double click on the calendar's empty time: in front
// of the main window while the button was held, behind it once released,
// whereas "+", the menu and Ctrl+Shift+N bring it up in front). macOS
// opens its windows on the second press as well (mouseDown with clickCount
// 2), and there the pressed window does not take the activation back. The
// action runs once the press is over: the pointer the element saw pressed
// is captured, and its release (the capture's loss, a cancel) queues the
// action, which so runs after the release has been handled; with no press
// under way (a touch or pen whose DoubleTapped comes with the release), it
// is queued at once.

using System;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Input;

namespace Malachi.App.Controls;

/// <summary>Runs an action once the pointer press that asked for it is released.</summary>
internal sealed class AfterRelease
{
    private readonly UIElement element;

    // The pointer pressed over the element, until its release.
    private Pointer? pressed;

    // What runs at the release; a newer request replaces it.
    private Action? pending;

    /// <summary>Follows the presses over <paramref name="element"/> and its children.</summary>
    public AfterRelease(UIElement element)
    {
        ArgumentNullException.ThrowIfNull(element);
        this.element = element;
        // Handled ones too: the children (a ScrollViewer, a button) take
        // presses of their own.
        element.AddHandler(UIElement.PointerPressedEvent, new PointerEventHandler(OnPressed), handledEventsToo: true);
        element.AddHandler(UIElement.PointerReleasedEvent, new PointerEventHandler(OnReleased), handledEventsToo: true);
        element.AddHandler(UIElement.PointerCanceledEvent, new PointerEventHandler(OnReleased), handledEventsToo: true);
        element.AddHandler(UIElement.PointerCaptureLostEvent, new PointerEventHandler(OnCaptureLost), handledEventsToo: true);
    }

    /// <summary>
    /// Runs <paramref name="action"/> once the press under way is released,
    /// else as soon as the UI thread is free (never inside the caller).
    /// </summary>
    public void Run(Action action)
    {
        ArgumentNullException.ThrowIfNull(action);
        // Capture fails for a pointer that is not pressed any more.
        if (pressed is { } p && element.CapturePointer(p))
        {
            pending = action;
            return;
        }
        Queue(action);
    }

    private void OnPressed(object sender, PointerRoutedEventArgs e) => pressed = e.Pointer;

    private void OnReleased(object sender, PointerRoutedEventArgs e)
    {
        if (pressed is { } p && p.PointerId == e.Pointer.PointerId)
        {
            pressed = null;
        }
        if (pending is { } action)
        {
            pending = null;
            Queue(action);
        }
    }

    // Only the element's own capture: a child's, which the element's
    // capture takes over, bubbles up too.
    private void OnCaptureLost(object sender, PointerRoutedEventArgs e)
    {
        if (ReferenceEquals(e.OriginalSource, element))
        {
            OnReleased(sender, e);
        }
    }

    private void Queue(Action action) => element.DispatcherQueue.TryEnqueue(DispatcherQueuePriority.Low, () => action());
}
