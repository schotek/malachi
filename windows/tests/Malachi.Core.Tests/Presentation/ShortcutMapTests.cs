// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ShortcutMap (Core/Presentation): the key map of docs/windows-port.md
// §11.5 over GTK's accelerators (ui/main.go, window.go MessageAccels,
// message_window.go, the Escape controllers, compose.blp,
// accounts_reorder.go) and macOS's command-r table (MainMenu.swift) and
// bareKeyActions (Actions.swift).

using System;
using System.Linq;
using Malachi.Core.Presentation;
using Malachi.Core.Settings;
using Xunit;
using C = Malachi.Core.Presentation.ShortcutCommand;
using K = Malachi.Core.Presentation.VirtualKey;

namespace Malachi.Core.Tests.Presentation;

public sealed class ShortcutMapTests
{
    private static readonly ShortcutContext Main = new(WindowKind.Main);

    private static C? Resolve(KeyChord chord, ShortcutContext? context = null) => ShortcutMap.Resolve(chord, context ?? Main);

    [Fact]
    public void TheApplicationsKeysWorkInEveryWindow()
    {
        foreach (var window in Enum.GetValues<WindowKind>())
        {
            var context = new ShortcutContext(window, TextInputFocused: true);
            Assert.Equal(C.NewMessage, ShortcutMap.Resolve(KeyChord.Ctrl(K.N), context));
            Assert.Equal(C.Preferences, ShortcutMap.Resolve(KeyChord.Ctrl(K.Comma), context));
            Assert.Equal(C.Quit, ShortcutMap.Resolve(KeyChord.Ctrl(K.Q), context));
        }
    }

    [Fact]
    public void CtrlRRepliesByDefaultAndChecksForNewMailWhenSetSo()
    {
        Assert.Equal(C.Reply, Resolve(KeyChord.Ctrl(K.R)));
        Assert.Equal(C.CheckForNewMail, Resolve(KeyChord.Ctrl(K.R), Main with { CtrlR = CtrlR.Refresh }));
        // F5 checks for new mail either way.
        Assert.Equal(C.CheckForNewMail, Resolve(KeyChord.Bare(K.F5)));
        Assert.Equal(C.CheckForNewMail, Resolve(KeyChord.Bare(K.F5), Main with { CtrlR = CtrlR.Refresh }));
    }

    [Fact]
    public void AMessageWindowRepliesButHasNoCheckForNewMail()
    {
        var message = new ShortcutContext(WindowKind.Message);
        Assert.Equal(C.Reply, Resolve(KeyChord.Ctrl(K.R), message));
        Assert.Null(Resolve(KeyChord.Ctrl(K.R), message with { CtrlR = CtrlR.Refresh }));
        Assert.Null(Resolve(KeyChord.Bare(K.F5), message));
        Assert.Equal(C.ReplyAll, Resolve(KeyChord.CtrlShift(K.R), message));
        Assert.Equal(C.Forward, Resolve(KeyChord.CtrlShift(K.F), message));
        Assert.Equal(C.CloseWindow, Resolve(KeyChord.Bare(K.Escape), message));
        Assert.Equal(C.CloseWindow, Resolve(KeyChord.Ctrl(K.W), message));
    }

    [Fact]
    public void TheMainWindowsKeys()
    {
        Assert.Equal(C.ReplyAll, Resolve(KeyChord.CtrlShift(K.R)));
        Assert.Equal(C.Forward, Resolve(KeyChord.CtrlShift(K.F)));
        Assert.Equal(C.Search, Resolve(KeyChord.Ctrl(K.F)));
        Assert.Equal(C.Search, Resolve(KeyChord.Ctrl(K.E)));
        Assert.Equal(C.Trash, Resolve(KeyChord.Bare(K.Delete)));
        Assert.Equal(C.Archive, Resolve(KeyChord.Bare(K.A)));
        Assert.Equal(C.Junk, Resolve(KeyChord.Bare(K.J)));
        Assert.Equal(C.MarkUnread, Resolve(KeyChord.Bare(K.U)));
        Assert.Equal(C.ToggleFlag, Resolve(KeyChord.Bare(K.S)));
        // The main window is not closed by Escape or Ctrl+W (the search box
        // takes Escape; closing it is the caption's).
        Assert.Null(Resolve(KeyChord.Bare(K.Escape)));
        Assert.Null(Resolve(KeyChord.Ctrl(K.W)));
    }

    [Fact]
    public void F10OpensThePrimaryMenuOfTheMainWindowOnly()
    {
        // GtkWindow's F10 pops up window.blp's primary MenuButton, which
        // only the main window has, from anywhere in it: a text input
        // types nothing for F10.
        Assert.Equal(C.MainMenu, Resolve(KeyChord.Bare(K.F10)));
        Assert.Equal(C.MainMenu, Resolve(KeyChord.Bare(K.F10), Main with { TextInputFocused = true }));
        Assert.False(ShortcutMap.IsSingleKey(C.MainMenu));
        Assert.Contains(KeyChord.Bare(K.F10), ShortcutMap.Chords(WindowKind.Main));
        foreach (var window in Enum.GetValues<WindowKind>().Where(w => w != WindowKind.Main))
        {
            Assert.Null(ShortcutMap.Resolve(KeyChord.Bare(K.F10), new ShortcutContext(window)));
            Assert.DoesNotContain(KeyChord.Bare(K.F10), ShortcutMap.Chords(window));
        }
        // Shift+F10 is the context menu's key, never the primary menu's.
        Assert.Null(Resolve(new KeyChord(K.F10, KeyModifiers.Shift)));
    }

    [Fact]
    public void SingleKeysTypeWhileATextInputHasTheFocus()
    {
        var typing = Main with { TextInputFocused = true };
        foreach (var key in new[] { K.Delete, K.A, K.J, K.U, K.S })
        {
            Assert.Null(Resolve(KeyChord.Bare(key), typing));
            Assert.Null(Resolve(KeyChord.Bare(key), new ShortcutContext(WindowKind.Message, TextInputFocused: true)));
        }
        // Ctrl combinations still work there.
        Assert.Equal(C.Reply, Resolve(KeyChord.Ctrl(K.R), typing));
        Assert.Equal(C.Search, Resolve(KeyChord.Ctrl(K.F), typing));
        Assert.Equal(C.CheckForNewMail, Resolve(KeyChord.Bare(K.F5), typing));
    }

    [Fact]
    public void ALetterWithAModifierIsNoSingleKey()
    {
        Assert.Null(Resolve(new KeyChord(K.A, KeyModifiers.Shift)));
        Assert.Null(Resolve(KeyChord.Ctrl(K.A)));
        Assert.Null(Resolve(new KeyChord(K.Delete, KeyModifiers.Shift)));
    }

    [Fact]
    public void ComposeSendsSavesAndClosesButEscapeInTheEditorIsTheBridges()
    {
        var compose = new ShortcutContext(WindowKind.Compose, TextInputFocused: true);
        Assert.Equal(C.Send, Resolve(KeyChord.Ctrl(K.Enter), compose));
        Assert.Equal(C.SaveDraft, Resolve(KeyChord.Ctrl(K.S), compose));
        Assert.Equal(C.CloseWindow, Resolve(KeyChord.Bare(K.Escape), compose));
        Assert.Equal(C.CloseWindow, Resolve(KeyChord.Ctrl(K.W), compose));
        var editor = compose with { EditorFocused = true };
        Assert.Null(Resolve(KeyChord.Bare(K.Escape), editor));
        Assert.Equal(C.CloseWindow, Resolve(KeyChord.Ctrl(K.W), editor));
        Assert.Equal(C.Send, Resolve(KeyChord.Ctrl(K.Enter), editor));
        // Nothing of the reader: a letter types, Delete deletes.
        Assert.Null(Resolve(KeyChord.Bare(K.A), compose with { TextInputFocused = false }));
        Assert.Null(Resolve(KeyChord.Ctrl(K.R), compose));
        // The editor's formatting keys stay in its bridge.
        Assert.Null(Resolve(KeyChord.Ctrl(K.U), editor));
        Assert.Null(Resolve(KeyChord.Ctrl(K.I), editor));
    }

    [Fact]
    public void PreferencesReorderWithCtrlUpAndDown()
    {
        var prefs = new ShortcutContext(WindowKind.Preferences);
        Assert.Equal(C.MoveUp, Resolve(KeyChord.Ctrl(K.Up), prefs));
        Assert.Equal(C.MoveDown, Resolve(KeyChord.Ctrl(K.Down), prefs));
        Assert.Equal(C.CloseWindow, Resolve(KeyChord.Bare(K.Escape), prefs));
        Assert.Null(Resolve(KeyChord.Ctrl(K.Up)));
    }

    [Fact]
    public void EmbeddedWizardAndOtherWindows()
    {
        var embedded = new ShortcutContext(WindowKind.Embedded);
        Assert.Equal(C.CloseWindow, Resolve(KeyChord.Bare(K.Escape), embedded));
        Assert.Null(Resolve(KeyChord.Bare(K.Delete), embedded));
        Assert.Null(Resolve(KeyChord.Ctrl(K.R), embedded));
        Assert.Equal(C.CloseWindow, Resolve(KeyChord.Ctrl(K.W), new ShortcutContext(WindowKind.Wizard)));
        // The attachment previewer: Escape and Ctrl+W close it, nothing per message.
        var other = new ShortcutContext(WindowKind.Other);
        Assert.Equal(C.CloseWindow, Resolve(KeyChord.Bare(K.Escape), other));
        Assert.Equal(C.CloseWindow, Resolve(KeyChord.Ctrl(K.W), other));
        Assert.Null(Resolve(KeyChord.Bare(K.Delete), other));
    }

    [Fact]
    public void TheChordsOfAWindowAreEveryKeyThatResolvesThere()
    {
        foreach (var window in Enum.GetValues<WindowKind>())
        {
            var chords = ShortcutMap.Chords(window);
            Assert.Equal(chords.Count, chords.Distinct().Count());
            foreach (var chord in chords)
            {
                var either = ShortcutMap.Resolve(chord, new ShortcutContext(window))
                    ?? ShortcutMap.Resolve(chord, new ShortcutContext(window, CtrlR.Refresh));
                Assert.NotNull(either);
            }
        }
        Assert.Contains(KeyChord.Ctrl(K.R), ShortcutMap.Chords(WindowKind.Main));
        Assert.DoesNotContain(KeyChord.Ctrl(K.R), ShortcutMap.Chords(WindowKind.Compose));
    }

    [Fact]
    public void TheBrowsersKeysAreKnownButNeverTheEditorsFormatting()
    {
        Assert.True(ShortcutMap.IsBrowserKey(KeyChord.Ctrl(K.P)));
        Assert.True(ShortcutMap.IsBrowserKey(KeyChord.CtrlShift(K.I)));
        Assert.True(ShortcutMap.IsBrowserKey(KeyChord.Bare(K.F5)));
        Assert.True(ShortcutMap.IsBrowserKey(KeyChord.Ctrl(K.F)));
        Assert.False(ShortcutMap.IsBrowserKey(KeyChord.Ctrl(K.I)));
        Assert.False(ShortcutMap.IsBrowserKey(KeyChord.Ctrl(K.U)));
        Assert.False(ShortcutMap.IsBrowserKey(KeyChord.Bare(K.A)));
    }

    [Fact]
    public void SingleKeyCommands()
    {
        Assert.True(ShortcutMap.IsSingleKey(C.Trash));
        Assert.True(ShortcutMap.IsSingleKey(C.ToggleFlag));
        Assert.False(ShortcutMap.IsSingleKey(C.Reply));
        Assert.False(ShortcutMap.IsSingleKey(C.CloseWindow));
    }

    [Theory]
    [InlineData(K.R, KeyModifiers.Control | KeyModifiers.Shift, "Ctrl+Shift+R")]
    [InlineData(K.Comma, KeyModifiers.Control, "Ctrl+,")]
    [InlineData(K.F5, KeyModifiers.None, "F5")]
    [InlineData(K.Delete, KeyModifiers.None, "Delete")]
    [InlineData(K.Enter, KeyModifiers.Control, "Ctrl+Enter")]
    [InlineData(K.Up, KeyModifiers.Control, "Ctrl+Up")]
    [InlineData(0x07, KeyModifiers.Alt, "Alt+0x07")]
    public void ChordsSpellAsWindowsShowsThem(int key, KeyModifiers modifiers, string text) =>
        Assert.Equal(text, new KeyChord(key, modifiers).ToString());
}
