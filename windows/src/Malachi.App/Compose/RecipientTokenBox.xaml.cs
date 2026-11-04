// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the RecipientTokenField and RecipientEditorView of
// macos/Sources/MalachiMail/Compose/RecipientTokenField.swift; GTK:
// ui/internal/compose/recipient_field.go (the recipient row of badges),
// ui/internal/recipients/tokens.go (the model, Core's RecipientTokens).
//
// A To, Cc or Bcc row: badges (RecipientBadge) and the box the rest is typed
// into, in lines. The control holds a RecipientTokens and no rule of its
// own: every change of the box goes through SetPending, and when that says
// the tokens changed the box is reset to Pending and the badges are built
// again; Enter, Tab and losing the focus Commit, a picked suggestion Adds,
// a paste Pastes. Keys: Backspace in an empty box selects the last badge, a
// second Backspace or Delete removes it, Left and Right walk across the
// badges and back into the box, Ctrl+C and Ctrl+X copy a selected badge's
// address. Text is the value the TextBox had (the model's Text), so that
// prefill, the draft and send read the same string; Resolved is what
// everything that must not re-parse it reads. Names and addresses are only
// ever put into TextBlock.Text, tooltips and automation names.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Runtime.InteropServices;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Microsoft.UI.Input;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Automation.Provider;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Windows.ApplicationModel.DataTransfer;
using Windows.UI.Core;
using WinUIKey = Windows.System.VirtualKey;

namespace Malachi.App.Compose;

/// <summary>A To, Cc or Bcc row: finished addresses as badges and the box for the rest.</summary>
public sealed partial class RecipientTokenBox : UserControl
{
    private RecipientTokens model = new();

    // The tokens the badges show now.
    private IReadOnlyList<RecipientTokens.Token> shown = [];
    private readonly List<RecipientBadge> badges = [];
    private int selected = -1;

    // The window is closing: no edit, no commit, no event any more.
    private bool frozen;

    /// <summary>An empty row.</summary>
    public RecipientTokenBox()
    {
        InitializeComponent();
        Loaded += (_, _) => TakeAccessibleName();
    }

    /// <summary>
    /// The value changed by the user (a badge added, removed or edited, text
    /// typed, pasted or committed) so that <see cref="Text"/> differs from
    /// before. Setting <see cref="Text"/> raises nothing; committing text
    /// into a badge that leaves <see cref="Text"/> as it was raises nothing.
    /// </summary>
    public event EventHandler? Changed;

    /// <summary>
    /// The text being typed changed by the user (what completion follows),
    /// whether <see cref="Text"/> changed or not.
    /// </summary>
    public event EventHandler? PendingChanged;

    /// <summary>
    /// Asked when the box loses the keyboard; true holds the commit back
    /// (the user is clicking a suggestion, which takes the typed text).
    /// </summary>
    public Func<bool>? HoldsCommit { get; set; }

    /// <summary>
    /// The row's value as the TextBox had it: <see cref="AddressList.Format"/>
    /// of the badges plus the unfinished text. Setting it makes badges of
    /// every address in it, silently.
    /// </summary>
    public string Text
    {
        get => model.Text;
        set
        {
            ArgumentNullException.ThrowIfNull(value);
            model = new RecipientTokens(value);
            selected = -1;
            Sync();
            ShowPending(caret: null);
        }
    }

    /// <summary>The text being typed after the last badge.</summary>
    public string Pending => model.Pending;

    /// <summary>Whether a badge is no address.</summary>
    public bool HasInvalid => model.HasInvalid;

    /// <summary>
    /// The recipients as the row would hold them were the typed text
    /// committed (<see cref="RecipientTokens.Resolved"/>): what send and the
    /// draft read, never <see cref="Text"/> parsed again.
    /// </summary>
    public (IReadOnlyList<Address> Addresses, IReadOnlyList<string> Invalid) Resolved() => model.Resolved();

    /// <summary>A picked suggestion becomes a badge and the typed text goes.</summary>
    public void Add(Address address)
    {
        ArgumentNullException.ThrowIfNull(address);
        selected = -1;
        Operate(() =>
        {
            model.Add(address);
            return null;
        });
    }

    /// <summary>The keyboard to the box.</summary>
    public void FocusInput() => InputBox.Focus(FocusState.Programmatic);

    /// <summary>
    /// The window is closing: from now on the row changes nothing and says
    /// nothing, so that the commit of a focus lost on the way out cannot
    /// make the cleaned-up draft dirty again.
    /// </summary>
    public void Freeze() => frozen = true;

    /// <inheritdoc/>
    protected override AutomationPeer OnCreateAutomationPeer() => new BoxPeer(this);

    // UIA's Value of the row (the tests and Narrator's edit commands): the
    // whole text, set as the user's own edit.
    private void SetTextFromAutomation(string value)
    {
        Operate(() =>
        {
            model = new RecipientTokens(value);
            selected = -1;
            return null;
        });
    }

    // The TextBox had its label for a name; the box inside has it now.
    private void TakeAccessibleName()
    {
        if (AutomationProperties.GetLabeledBy(this) is TextBlock label)
        {
            AutomationProperties.SetName(InputBox, label.Text);
        }
    }

    // Runs a change of the model: the badges and the box follow, and Changed
    // is raised when the value differs from before. The operation returns the
    // caret the box should have (null: the end after a reset, as it is
    // otherwise).
    private void Operate(Func<int?> operation)
    {
        if (frozen)
        {
            return;
        }
        var before = model.Text;
        var caret = operation();
        Sync();
        ShowPending(caret);
        if (!string.Equals(before, model.Text, StringComparison.Ordinal))
        {
            Changed?.Invoke(this, EventArgs.Empty);
        }
    }

    // The badges show the model's tokens: built again only when they
    // differ (typing changes the pending text alone).
    private void Sync()
    {
        if (!shown.SequenceEqual(model.Tokens))
        {
            Rebuild();
        }
        if (selected >= badges.Count)
        {
            selected = -1;
        }
        ApplySelection();
    }

    private void Rebuild()
    {
        // The box stays: it is the last child, and takes the keyboard with it.
        for (var i = Wrap.Children.Count - 2; i >= 0; i--)
        {
            Wrap.Children.RemoveAt(i);
        }
        badges.Clear();
        shown = [.. model.Tokens];
        for (var i = 0; i < shown.Count; i++)
        {
            var badge = new RecipientBadge(i, shown[i]);
            badge.Pressed += OnBadgePressed;
            badge.EditRequested += OnBadgeEdit;
            badge.RemoveRequested += OnBadgeRemove;
            Wrap.Children.Insert(i, badge);
            badges.Add(badge);
        }
        if (InputBox.FocusState != FocusState.Unfocused)
        {
            _ = DispatcherQueue.TryEnqueue(() => InputBox.StartBringIntoView());
        }
    }

    private void ApplySelection()
    {
        for (var i = 0; i < badges.Count; i++)
        {
            badges[i].IsSelected = i == selected;
        }
    }

    private void Select(int index)
    {
        selected = index;
        ApplySelection();
        if (index >= 0 && index < badges.Count)
        {
            badges[index].StartBringIntoView();
        }
    }

    private void Deselect() => Select(-1);

    // The box shows the pending text: reset when it differs (the badge it
    // became is no longer in it, and Undo must not bring it back), the caret
    // put where asked.
    private void ShowPending(int? caret)
    {
        var pending = model.Pending;
        if (!string.Equals(InputBox.Text, pending, StringComparison.Ordinal))
        {
            InputBox.Text = pending;
            InputBox.ClearUndoRedoHistory();
            InputBox.Select(Math.Clamp(caret ?? pending.Length, 0, pending.Length), 0);
        }
        else if (caret is { } c)
        {
            InputBox.Select(Math.Clamp(c, 0, pending.Length), 0);
        }
    }

    // The user changed the text of the box.
    private void OnInputTextChanged(object sender, TextChangedEventArgs e)
    {
        var text = InputBox.Text;
        // Our own reset of the box (it raises this after the setter), and
        // text the model already holds: nothing to take in.
        if (frozen || string.Equals(text, model.Pending, StringComparison.Ordinal))
        {
            return;
        }
        var caret = InputBox.SelectionStart;
        Deselect();
        Operate(() =>
        {
            if (!model.SetPending(text))
            {
                return null;
            }
            // The text before the caret became badges: the caret goes to the
            // start of what remains, or stays at its place in it.
            var start = text.Length - model.Pending.Length;
            return Math.Max(caret - start, 0);
        });
        PendingChanged?.Invoke(this, EventArgs.Empty);
    }

    private static bool IsDown(WinUIKey key) =>
        InputKeyboardSource.GetKeyStateForCurrentThread(key).HasFlag(CoreVirtualKeyStates.Down);

    // The keys of the row. The suggestions' popup (on this control) has had
    // its say already: its handler runs before this one.
    private void OnInputKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (frozen)
        {
            return;
        }
        var last = model.Tokens.Count - 1;
        switch (e.Key)
        {
            case WinUIKey.Enter:
                Commit();
                e.Handled = true;
                break;
            case WinUIKey.Tab:
                // The focus moves on by itself, with the text a badge.
                Commit();
                break;
            case WinUIKey.Back or WinUIKey.Delete:
                if (selected >= 0)
                {
                    RemoveBadge(selected);
                    e.Handled = true;
                }
                else if (e.Key == WinUIKey.Back && InputBox.Text.Length == 0 && last >= 0)
                {
                    Select(last);
                    e.Handled = true;
                }
                break;
            case WinUIKey.Left:
                if (selected >= 0)
                {
                    Select(Math.Max(selected - 1, 0));
                    e.Handled = true;
                }
                else if (InputBox.SelectionStart == 0 && InputBox.SelectionLength == 0 && last >= 0)
                {
                    Select(last);
                    e.Handled = true;
                }
                break;
            case WinUIKey.Right:
                if (selected >= 0)
                {
                    if (selected < last)
                    {
                        Select(selected + 1);
                    }
                    else
                    {
                        Deselect();
                        InputBox.Select(0, 0);
                    }
                    e.Handled = true;
                }
                break;
            case WinUIKey.C or WinUIKey.X when IsDown(WinUIKey.Control) && selected >= 0:
                CopySelected(cut: e.Key == WinUIKey.X);
                e.Handled = true;
                break;
            case WinUIKey.A when IsDown(WinUIKey.Control):
                Deselect();
                break;
            default:
                break;
        }
    }

    // Enter, Tab and a lost keyboard. The typed text is gone from the box
    // (and so from the suggestions' popup, which is told).
    private void Commit()
    {
        var typed = model.Pending.Length > 0;
        Operate(() =>
        {
            model.Commit();
            return null;
        });
        if (typed)
        {
            PendingChanged?.Invoke(this, EventArgs.Empty);
        }
    }

    // The box lost the keyboard: what is typed becomes a badge, unless the
    // user is just picking it from the suggestions.
    private void OnInputLostFocus(object sender, RoutedEventArgs e)
    {
        if (frozen)
        {
            return;
        }
        Deselect();
        if (HoldsCommit?.Invoke() == true)
        {
            return;
        }
        Commit();
    }

    // Plain text from the clipboard goes through the model, in place of the
    // box's selection or at its caret.
    private async void OnInputPaste(object sender, TextControlPasteEventArgs e)
    {
        e.Handled = true;
        string? pasted = null;
        try
        {
            var content = Clipboard.GetContent();
            if (content.Contains(StandardDataFormats.Text))
            {
                pasted = await content.GetTextAsync();
            }
        }
        catch (Exception error) when (error is COMException or UnauthorizedAccessException or InvalidOperationException)
        {
            return;
        }
        if (frozen || string.IsNullOrEmpty(pasted))
        {
            return;
        }
        var text = InputBox.Text;
        var start = Math.Clamp(InputBox.SelectionStart, 0, text.Length);
        var end = Math.Clamp(start + InputBox.SelectionLength, start, text.Length);
        var before = text[..start];
        var after = text[end..];
        Deselect();
        Operate(() =>
        {
            // The typed text up to the selection, the paste, the rest after it.
            model.SetPending(before);
            model.Paste(pasted);
            var caret = model.Pending.Length;
            if (after.Length > 0)
            {
                model.SetPending(model.Pending + after);
            }
            return caret;
        });
        PendingChanged?.Invoke(this, EventArgs.Empty);
    }

    // Ctrl+C and Ctrl+X of a selected badge: its whole address.
    private void CopySelected(bool cut)
    {
        if (selected < 0 || selected >= model.Tokens.Count)
        {
            return;
        }
        try
        {
            var package = new DataPackage();
            package.SetText(model.Tokens[selected].Tooltip);
            Clipboard.SetContent(package);
        }
        catch (Exception error) when (error is COMException or UnauthorizedAccessException)
        {
            // Another program holds the clipboard: nothing was copied, so
            // nothing is cut either.
            return;
        }
        if (cut)
        {
            RemoveBadge(selected);
        }
    }

    private void RemoveBadge(int index)
    {
        selected = -1;
        Operate(() =>
        {
            model.Remove(index);
            return null;
        });
        FocusInput();
    }

    private void OnBadgePressed(object? sender, EventArgs e)
    {
        if (frozen || sender is not RecipientBadge badge)
        {
            return;
        }
        FocusInput();
        Select(badge.Index);
    }

    private void OnBadgeEdit(object? sender, EventArgs e)
    {
        if (frozen || sender is not RecipientBadge badge)
        {
            return;
        }
        var index = badge.Index;
        selected = -1;
        Operate(() =>
        {
            model.Edit(index);
            return null;
        });
        FocusInput();
    }

    private void OnBadgeRemove(object? sender, EventArgs e)
    {
        if (!frozen && sender is RecipientBadge badge)
        {
            RemoveBadge(badge.Index);
        }
    }

    // A click on the empty part of the row puts the keyboard in the box.
    private void OnBackgroundPressed(object sender, PointerRoutedEventArgs e)
    {
        if (frozen)
        {
            return;
        }
        Deselect();
        FocusInput();
    }

    // The row is a group that UI Automation can read and set as one value
    // (the TextBox's), its badges and the box inside it.
    private sealed partial class BoxPeer(RecipientTokenBox owner) : FrameworkElementAutomationPeer(owner), IValueProvider
    {
        public bool IsReadOnly => false;

        public string Value => owner.Text;

        public void SetValue(string value)
        {
            ArgumentNullException.ThrowIfNull(value);
            owner.SetTextFromAutomation(value);
        }

        protected override AutomationControlType GetAutomationControlTypeCore() => AutomationControlType.Group;

        protected override string GetClassNameCore() => nameof(RecipientTokenBox);

        protected override object GetPatternCore(PatternInterface patternInterface) =>
            patternInterface == PatternInterface.Value ? this : base.GetPatternCore(patternInterface);
    }
}
