// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/windows-port.md §11.5; spikes2/INPUT-SPIKES.md
// §5, recommendations 1 and 4): the keyboard of one window, the
// counterpart of GTK's accelerators (app.SetAccelsForAction, the windows'
// shortcut controllers) and of macOS's key equivalents and EscapeCloser.
// The keys come from two places and run the same commands:
//
// - XAML KeyboardAccelerators on the window's root, while XAML has the
//   focus. A single key (Delete, A, J, U, S) runs nothing while a text
//   input has the focus, where it types (the accelerator fires and the
//   character is typed all the same). A TextBox consumes Ctrl+Q before any
//   accelerator sees it, so the root's PreviewKeyDown, which sees every
//   key first, runs Quit there.
// - The island's InputPreTranslateKeyboardSource, while a WebView2 has
//   the focus (no XAML key event fires then), or, where the island gives
//   none, a thread-local WH_KEYBOARD hook (ThreadKeyboardHook, the
//   measured fallback): the key down of a mapped key is swallowed and its
//   command runs once the message is handled, and the key up of that
//   press is swallowed too when it comes to the same WebView2 (Core's
//   SwallowedKeys; a command that moves the focus sends it elsewhere); the
//   browser's own keys (reload, find, print, developer tools) are
//   swallowed even when they run nothing. An auto-repeated key runs its
//   command once, on the first press. The compose editor keeps its single
//   keys and Escape (its bridge handles Escape, Ctrl+B/I/U and Ctrl+K).
//
// Nothing runs while one of the window's dialogs is up. The map itself is
// Core's (ShortcutMap), with the ctrl-r setting read at every key.

using System;
using Malachi.Core.Presentation;
using Malachi.Core.Settings;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Input;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Input;
using Windows.UI.Core;
using WinUIKey = Windows.System.VirtualKey;
using WinUIModifiers = Windows.System.VirtualKeyModifiers;

namespace Malachi.App.Commands;

/// <summary>Runs a window's commands for its keys.</summary>
internal sealed partial class CommandRouter : IDisposable
{
    private readonly WindowKind kind;
    private readonly WindowCommands commands;
    private readonly SettingsStore settings;
    private readonly nint windowHandle;
    private readonly DispatcherQueue dispatcher;
    private readonly Func<bool> dialogUp;
    private readonly ILogger logger;
    private readonly PreTranslateKeyboard webViewKeys = new();
    private readonly SwallowedKeys swallowed = new();
    private ThreadKeyboardHook? hook;
    private UIElement? root;

    /// <summary>A router for a window of <paramref name="kind"/> over <paramref name="commands"/>.</summary>
    /// <param name="kind">Which keys the window has.</param>
    /// <param name="commands">What they run.</param>
    /// <param name="settings">The ctrl-r setting.</param>
    /// <param name="windowHandle">The window's handle (the WebView2s inside it, for the keyboard hook).</param>
    /// <param name="dispatcher">The window's UI thread, where a WebView2's keys run their commands.</param>
    /// <param name="dialogUp">Whether one of the window's dialogs is up (then no key runs anything).</param>
    /// <param name="logger">Where a failure to see a WebView2's keys is reported.</param>
    public CommandRouter(
        WindowKind kind,
        WindowCommands commands,
        SettingsStore settings,
        nint windowHandle,
        DispatcherQueue dispatcher,
        Func<bool> dialogUp,
        ILogger logger)
    {
        this.kind = kind;
        this.commands = commands;
        this.settings = settings;
        this.windowHandle = windowHandle;
        this.dispatcher = dispatcher;
        this.dialogUp = dialogUp;
        this.logger = logger;
    }

    /// <summary>The window's kind.</summary>
    public WindowKind Kind => kind;

    /// <summary>
    /// Installs the keys on <paramref name="element"/>, the window's root:
    /// its accelerators now, the WebView2 keys once it is loaded.
    /// </summary>
    public void Attach(UIElement element)
    {
        ArgumentNullException.ThrowIfNull(element);
        root = element;
        // The accelerators run commands; they are no tooltips of the root.
        element.KeyboardAcceleratorPlacementMode = KeyboardAcceleratorPlacementMode.Hidden;
        foreach (var chord in ShortcutMap.Chords(kind))
        {
            var accelerator = new KeyboardAccelerator
            {
                Key = (WinUIKey)chord.Key,
                Modifiers = ToWinUI(chord.Modifiers),
            };
            accelerator.Invoked += (_, e) => e.Handled = RunNow(chord);
            element.KeyboardAccelerators.Add(accelerator);
        }
        element.AddHandler(UIElement.PreviewKeyDownEvent, new KeyEventHandler(OnPreviewKeyDown), handledEventsToo: true);
        if (element.XamlRoot is not null)
        {
            InstallWebViewKeys(element.XamlRoot);
        }
        else if (element is FrameworkElement fe)
        {
            fe.Loaded += OnLoaded;
        }
    }

    /// <summary>Removes the keyboard hook, if the window has one (the window closed).</summary>
    public void Dispose()
    {
        hook?.Dispose();
        hook = null;
    }

    /// <summary>Runs the command of <paramref name="chord"/> as a key press would; true when one ran.</summary>
    public bool RunNow(KeyChord chord)
    {
        if (Resolve(chord) is not { } command)
        {
            return false;
        }
        return commands.For(command).TryExecute();
    }

    private static WinUIModifiers ToWinUI(KeyModifiers m)
    {
        var result = WinUIModifiers.None;
        if (m.HasFlag(KeyModifiers.Control))
        {
            result |= WinUIModifiers.Control;
        }
        if (m.HasFlag(KeyModifiers.Shift))
        {
            result |= WinUIModifiers.Shift;
        }
        if (m.HasFlag(KeyModifiers.Alt))
        {
            result |= WinUIModifiers.Menu;
        }
        if (m.HasFlag(KeyModifiers.Windows))
        {
            result |= WinUIModifiers.Windows;
        }
        return result;
    }

    // FSHIFT, FCONTROL and FALT of the pre-translate source.
    private static KeyModifiers FromSource(uint flags)
    {
        var result = KeyModifiers.None;
        if ((flags & 0x8) != 0)
        {
            result |= KeyModifiers.Control;
        }
        if ((flags & 0x4) != 0)
        {
            result |= KeyModifiers.Shift;
        }
        if ((flags & 0x10) != 0)
        {
            result |= KeyModifiers.Alt;
        }
        return result;
    }

    private static bool IsDown(WinUIKey key) =>
        InputKeyboardSource.GetKeyStateForCurrentThread(key).HasFlag(CoreVirtualKeyStates.Down);

    private void OnLoaded(object sender, RoutedEventArgs e)
    {
        ((FrameworkElement)sender).Loaded -= OnLoaded;
        if (root?.XamlRoot is { } xamlRoot)
        {
            InstallWebViewKeys(xamlRoot);
        }
    }

    private void InstallWebViewKeys(XamlRoot xamlRoot)
    {
        if (webViewKeys.Install(xamlRoot, OnWebViewKey))
        {
            return;
        }
        var fallback = new ThreadKeyboardHook();
        if (fallback.Install(windowHandle, OnWebViewKey))
        {
            hook = fallback;
            LogKeyboardHook(logger, kind);
            return;
        }
        LogNoWebViewKeys(logger, kind);
    }

    private ShortcutCommand? Resolve(KeyChord chord)
    {
        if (dialogUp())
        {
            return null;
        }
        var focused = root?.XamlRoot is { } xamlRoot ? FocusManager.GetFocusedElement(xamlRoot) : null;
        var context = new ShortcutContext(
            kind, settings.CtrlR, KeyboardRouting.IsTextInput(focused), KeyboardRouting.IsEditor(focused));
        return ShortcutMap.Resolve(chord, context);
    }

    // The keys of EditorKeys.BridgeHandles belong to the compose editor's
    // page while it has the focus (Ctrl+B, I, U with or without Shift,
    // Ctrl+K, Escape): no command runs, and none is swallowed as a browser
    // key (Ctrl+Shift+I is italic there, as in GTK).
    private bool EditorKeeps(KeyChord chord)
    {
        if (root?.XamlRoot is not { } xamlRoot || !KeyboardRouting.IsEditor(FocusManager.GetFocusedElement(xamlRoot)))
        {
            return false;
        }
        var m = chord.Modifiers;
        return EditorKeys.BridgeHandles(
            chord.Key, m.HasFlag(KeyModifiers.Control), m.HasFlag(KeyModifiers.Shift), m.HasFlag(KeyModifiers.Alt), m.HasFlag(KeyModifiers.Windows));
    }

    // A TextBox consumes Ctrl+Q before the accelerators: Quit from there.
    private void OnPreviewKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Handled || e.Key != WinUIKey.Q || !IsDown(WinUIKey.Control) || IsDown(WinUIKey.Shift) || IsDown(WinUIKey.Menu))
        {
            return;
        }
        var focused = root?.XamlRoot is { } xamlRoot ? FocusManager.GetFocusedElement(xamlRoot) : null;
        if (KeyboardRouting.IsTextInput(focused) && !KeyboardRouting.IsEditor(focused))
        {
            e.Handled = RunNow(KeyChord.Ctrl(Malachi.Core.Presentation.VirtualKey.Q));
        }
    }

    // A key of a focused WebView2 (PreTranslateKeyboard or the keyboard
    // hook): true swallows it.
    private bool OnWebViewKey(WebViewKey key)
    {
        if (!key.IsDown)
        {
            return swallowed.KeyUp(key.VirtualKey, key.Window);
        }
        var chord = new KeyChord(key.VirtualKey, FromSource(key.Modifiers));
        if (EditorKeeps(chord))
        {
            // The compose editor's bridge formats with these, and posts
            // Ctrl+K and Escape back to the window (§6.5).
            swallowed.KeyDown(key.VirtualKey, key.Window, false);
            return false;
        }
        var command = Resolve(chord);
        var swallow = command is not null || ShortcutMap.IsBrowserKey(chord);
        swallowed.KeyDown(key.VirtualKey, key.Window, swallow);
        if (!swallow)
        {
            return false;
        }
        if (command is { } c && !key.Repeat)
        {
            // After the message: a command may show a dialog or close the window.
            dispatcher.TryEnqueue(() => commands.For(c).TryExecute());
        }
        return true;
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "no pre-translate source in a {Kind} window: its WebView2 keys go through a keyboard hook")]
    private static partial void LogKeyboardHook(ILogger logger, WindowKind kind);

    [LoggerMessage(Level = LogLevel.Warning, Message = "the keys of a focused WebView2 cannot be seen in a {Kind} window")]
    private static partial void LogNoWebViewKeys(ILogger logger, WindowKind kind);
}
