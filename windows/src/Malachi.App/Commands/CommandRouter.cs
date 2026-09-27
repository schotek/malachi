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
//   the focus (no XAML key event fires then): the key down of a mapped
//   key is swallowed and its command runs once the message is handled,
//   the matching key up is swallowed too; the browser's own keys (reload,
//   find, print, developer tools) are swallowed even when they run
//   nothing. An auto-repeated key runs its command once, on the first
//   press. The compose editor keeps its single keys and Escape (its bridge
//   handles Escape, Ctrl+B/I/U and Ctrl+K).
//
// Nothing runs while one of the window's dialogs is up. The map itself is
// Core's (ShortcutMap), with the ctrl-r setting read at every key.

using System;
using System.Collections.Generic;
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
internal sealed partial class CommandRouter
{
    private readonly WindowKind kind;
    private readonly WindowCommands commands;
    private readonly SettingsStore settings;
    private readonly DispatcherQueue dispatcher;
    private readonly Func<bool> dialogUp;
    private readonly ILogger logger;
    private readonly PreTranslateKeyboard webViewKeys = new();
    private readonly HashSet<int> swallowed = [];
    private UIElement? root;

    /// <summary>A router for a window of <paramref name="kind"/> over <paramref name="commands"/>.</summary>
    /// <param name="kind">Which keys the window has.</param>
    /// <param name="commands">What they run.</param>
    /// <param name="settings">The ctrl-r setting.</param>
    /// <param name="dispatcher">The window's UI thread, where a WebView2's keys run their commands.</param>
    /// <param name="dialogUp">Whether one of the window's dialogs is up (then no key runs anything).</param>
    /// <param name="logger">Where a failure to see a WebView2's keys is reported.</param>
    public CommandRouter(
        WindowKind kind, WindowCommands commands, SettingsStore settings, DispatcherQueue dispatcher, Func<bool> dialogUp, ILogger logger)
    {
        this.kind = kind;
        this.commands = commands;
        this.settings = settings;
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
        if (!webViewKeys.Install(xamlRoot, OnWebViewKey))
        {
            LogNoPreTranslate(logger, kind);
        }
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

    // A key of a focused WebView2 (PreTranslateKeyboard): true swallows it.
    private bool OnWebViewKey(WebViewKey key)
    {
        if (!key.IsDown)
        {
            return swallowed.Remove(key.VirtualKey);
        }
        var chord = new KeyChord(key.VirtualKey, FromSource(key.Modifiers));
        var command = Resolve(chord);
        if (command is null && !ShortcutMap.IsBrowserKey(chord))
        {
            return false;
        }
        swallowed.Add(key.VirtualKey);
        if (command is { } c && !key.Repeat)
        {
            // After the message: a command may show a dialog or close the window.
            dispatcher.TryEnqueue(() => commands.For(c).TryExecute());
        }
        return true;
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "the keys of a focused WebView2 cannot be seen in a {Kind} window")]
    private static partial void LogNoPreTranslate(ILogger logger, WindowKind kind);
}
