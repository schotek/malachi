// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/windows-port.md §11.3): the owned modal window
// that stands in for an Adw.Dialog over its parent and a macOS sheet, shared
// by the account wizard, the Jira account assistant and the Jira account's
// settings. The window is fixed in size (the dialog's content size in
// effective pixels, its title bar included, as the content is extended into
// it), centred on its owner and kept on its display; it cannot be minimised
// or maximised, whatever asks (UIA exposes the caption buttons a dialog
// presenter hides). A modal window disables its owner, and Windows activates
// the next enabled window when the active one goes away: ReturnToOwner
// enables the owner again and brings it to the front before the dialog
// goes, as a Win32 dialog hands back its owner, so the keyboard returns to
// the window the dialog was opened from. AddBackKeys gives a dialog with
// pages Adw.NavigationView's other ways back: Alt+Left and the mouse's back
// button.

using System;
using Malachi.App.Shell;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Input;
using Windows.Graphics;
using Windows.Win32;
using Windows.Win32.Foundation;
using Windows.Win32.UI.WindowsAndMessaging;
using VirtualKey = Windows.System.VirtualKey;
using VirtualKeyModifiers = Windows.System.VirtualKeyModifiers;

namespace Malachi.App.Wizard;

/// <summary>An owned, modal, fixed-size window over the window it was opened from.</summary>
internal sealed class ModalDialog
{
    private readonly Window window;

    // The window the dialog is modal over.
    private HWND owner;

    /// <summary>The modal presentation of <paramref name="window"/>, not shown yet.</summary>
    public ModalDialog(Window window)
    {
        this.window = window;
        window.AppWindow.Changed += (sender, _) => KeepRestored(sender);
    }

    /// <summary>
    /// Shows the window modal over <paramref name="owner"/>, its content
    /// <paramref name="width"/>×<paramref name="height"/> effective pixels
    /// at the owner's scale, centred on it and kept on its display.
    /// </summary>
    public void Present(Window owner, int width, int height)
    {
        ArgumentNullException.ThrowIfNull(owner);
        var hwnd = (HWND)WindowPresenter.Handle(window);
        var ownerHwnd = WindowPresenter.Handle(owner);
        this.owner = (HWND)ownerHwnd;
        PInvoke.SetWindowLongPtr(hwnd, WINDOW_LONG_PTR_INDEX.GWLP_HWNDPARENT, ownerHwnd);
        var presenter = OverlappedPresenter.CreateForDialog();
        presenter.IsModal = true;
        presenter.IsResizable = false;
        presenter.IsMaximizable = false;
        presenter.IsMinimizable = false;
        var app = window.AppWindow;
        app.SetPresenter(presenter);

        var dpi = PInvoke.GetDpiForWindow((HWND)ownerHwnd);
        var scale = dpi == 0 ? 1.0 : dpi / 96.0;
        var area = DisplayArea.GetFromWindowId(owner.AppWindow.Id, DisplayAreaFallback.Nearest).WorkArea;
        // The content is extended into the title bar, whose height the
        // client size leaves out: the page and its header take the size
        // together, as the Adw.Dialog's content does.
        var caption = app.TitleBar.Height;
        app.ResizeClient(new SizeInt32(
            Math.Min((int)Math.Round(width * scale), area.Width),
            Math.Min((int)Math.Round(height * scale) - caption, area.Height)));
        var size = app.Size;
        var o = owner.AppWindow;
        var x = o.Position.X + ((o.Size.Width - size.Width) / 2);
        var y = o.Position.Y + ((o.Size.Height - size.Height) / 2);
        x = Math.Clamp(x, area.X, Math.Max(area.X, area.X + area.Width - size.Width));
        y = Math.Clamp(y, area.Y, Math.Max(area.Y, area.Y + area.Height - size.Height));
        app.Move(new PointInt32(x, y));
        WindowPresenter.Present(window);
    }

    /// <summary>
    /// The dialog is going (its Closed): its owner is enabled again first
    /// and, if the dialog had the keyboard, activated, so that Windows does
    /// not hand the activation to another window (the owner is still
    /// disabled when the modal window is destroyed). Null when it was never
    /// presented; otherwise whether the owner was activated.
    /// </summary>
    public bool? ReturnToOwner()
    {
        if (owner == HWND.Null)
        {
            return null;
        }
        var wasActive = PInvoke.GetForegroundWindow() == (HWND)WindowPresenter.Handle(window);
        PInvoke.EnableWindow(owner, true);
        if (wasActive && PInvoke.IsWindowVisible(owner))
        {
            PInvoke.SetForegroundWindow(owner);
        }
        return wasActive;
    }

    /// <summary>
    /// Adw.NavigationView's other ways back on <paramref name="root"/>:
    /// Alt+Left and the mouse's back button run <paramref name="back"/>.
    /// </summary>
    public static void AddBackKeys(UIElement root, Action back)
    {
        ArgumentNullException.ThrowIfNull(root);
        ArgumentNullException.ThrowIfNull(back);
        var accelerator = new KeyboardAccelerator { Key = VirtualKey.Left, Modifiers = VirtualKeyModifiers.Menu };
        accelerator.Invoked += (_, e) =>
        {
            e.Handled = true;
            back();
        };
        root.KeyboardAcceleratorPlacementMode = KeyboardAcceleratorPlacementMode.Hidden;
        root.KeyboardAccelerators.Add(accelerator);
        root.AddHandler(UIElement.PointerPressedEvent, new PointerEventHandler((_, e) =>
        {
            if (e.GetCurrentPoint(root).Properties.IsXButton1Pressed)
            {
                e.Handled = true;
                back();
            }
        }), handledEventsToo: true);
    }

    // A modal dialog stays as it was placed: minimised or maximised (by UIA
    // or a system command, the presenter hides those buttons only), it is
    // restored.
    private void KeepRestored(AppWindow app)
    {
        if (app.Presenter is OverlappedPresenter { State: not OverlappedPresenterState.Restored } p)
        {
            window.DispatcherQueue.TryEnqueue(() =>
            {
                if (p.State != OverlappedPresenterState.Restored)
                {
                    p.Restore();
                }
            });
        }
    }
}
