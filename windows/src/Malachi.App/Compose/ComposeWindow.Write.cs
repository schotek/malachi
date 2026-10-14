// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Compose/ComposeWindowController.swift
// (wireAssistant's bar, updateAssistant, writeWithAssistant, the bar's
// cancel in windowWillClose); GTK: ui/internal/assistantpanel/writer.go
// (Writer, which the window drives) and ui/data/ui/compose_extras.blp
// (assistant_bar). The compose window's assistant bar (the In App target):
// at the bottom of the pane's content (ComposePane.AssistantBar), shown
// while the one-shot requests can run (AssistantController.CanRunInApp, the
// rewrite's condition) and never in comment mode. It writes a reply, a
// forward's introduction or a message from an optional instruction, the
// user's own text (the editor, flushed) and the prepared original under it
// (ComposeWriteController over Assistant.WriteMessage), previews the answer
// as plain text, and only Replace puts it into the editor: the editor takes
// the keyboard with the caret at its start, the bridge notes its own text
// as the passage (the attribution line of ComposeParams.Attribution only
// when the original is in the editor, none when it is shown apart) and the
// answer replaces it as plain text, one step the page's undo takes back. The
// first request ever asks for consent on this window (the request's hook,
// the rewrite's question). A change of provider, the bar becoming
// unavailable and the window closing end a running request and clear the
// bar; the bar names the provider in effect.
//
// Windows differences: macOS compares the assistant's provider generation;
// here the application's InAppProviderChanged says the provider changed, as
// it does for the rewrite, and the request takes the new provider.

using System;
using Malachi.Core.Assistants;
using Malachi.Core.Controllers;
using Microsoft.UI.Xaml;
using RewriteState = Malachi.Core.Controllers.ComposeRewriteController.RewriteState;

namespace Malachi.App.Compose;

/// <summary>The compose window's assistant bar.</summary>
public sealed partial class ComposeWindow
{
    private ComposeWriteController? writer;
    private EventHandler? writeAssistantChanged;
    private EventHandler? writeProviderChanged;

    // wireAssistant, the bar's part.
    private void WireWrite()
    {
        var bar = pane.AssistantBar;
        bar.SetMode(ComposeWriteController.Mode(parameters.Kind));
        bar.WriteRequested += (_, instruction) => WriteWithAssistant(instruction);
        bar.CancelRequested += (_, _) => writer?.Cancel();
        bar.ApplyRequested += (_, text) => ApplyWriting(text);
        writeAssistantChanged = (_, _) => SyncWrite();
        state.Assistant.Changed += writeAssistantChanged;
        writeProviderChanged = (_, _) =>
        {
            ClearWriting();
            if (writer is { } active)
            {
                active.Request.Provider = state.InAppProvider;
            }
            SyncWrite();
        };
        state.InAppProviderChanged += writeProviderChanged;
        SyncWrite();
    }

    // updateAssistant, the bar's part: shown while the writing can run,
    // naming the provider in effect; a request under way ends when it no
    // longer can.
    private void SyncWrite()
    {
        if (closing)
        {
            return;
        }
        var bar = pane.AssistantBar;
        bar.SetProvider(chatGpt: state.Settings.AssistantProvider == AssistantProviderID.ChatGpt);
        var available = RewriteAvailable && !IsComment;
        bar.Visibility = available ? Visibility.Visible : Visibility.Collapsed;
        if (!available)
        {
            ClearWriting();
        }
    }

    // A running request ends, the bar shows nothing.
    private void ClearWriting()
    {
        writer?.Cancel();
        pane.AssistantBar.Show("", running: false);
    }

    // It all ends with the window.
    private void CloseWrite()
    {
        if (writeProviderChanged is { } providerHandler)
        {
            state.InAppProviderChanged -= providerHandler;
            writeProviderChanged = null;
        }
        if (writeAssistantChanged is { } handler)
        {
            state.Assistant.Changed -= handler;
            writeAssistantChanged = null;
        }
        if (writer is { } w)
        {
            w.Request.Close();
            w.Dispose();
            writer = null;
        }
    }

    // The writing's controller, made on first use; its request asks for
    // consent on this window (the rewrite's question).
    private ComposeWriteController Writer()
    {
        if (writer is { } existing)
        {
            return existing;
        }
        var request = state.NewAssistantRequest();
        request.Consent = AskConsentAsync;
        var made = new ComposeWriteController(request);
        made.StateChanged += (_, s) => RenderWrite(s);
        writer = made;
        return made;
    }

    // writeWithAssistant: the bar's Write. The instruction, the user's text
    // and the original go to the assistant as data, once the editor and the
    // panel reported what they hold.
    private void WriteWithAssistant(string instruction)
    {
        if (!RewriteAvailable || IsComment || closing || !pane.Editor.IsEditorReady)
        {
            return;
        }
        var w = Writer();
        pane.FlushEditor(() =>
        {
            if (closing || !RewriteAvailable)
            {
                return;
            }
            w.Start(ComposeWriteController.Mode(parameters.Kind), instruction, pane.Subject, pane.Editor.Text, pane.QuotedText);
        });
    }

    // What the writing's state shows in the bar.
    private void RenderWrite(RewriteState st)
    {
        if (closing)
        {
            return;
        }
        var bar = pane.AssistantBar;
        switch (st)
        {
            case RewriteState.Running r:
                bar.Show(r.Preview, running: true);
                break;
            case RewriteState.Done d:
                bar.Show(d.Text, running: false);
                break;
            case RewriteState.Failed f:
                bar.Show("", running: false, failure: f.Text);
                break;
            default:
                bar.Show("", running: false);
                break;
        }
    }

    // The bar's Replace: the editor takes the keyboard (so that Ctrl+Z
    // reaches its undo) with the caret at its start, its own text becomes
    // the passage, and the answer replaces it as plain text.
    private void ApplyWriting(string text)
    {
        writer?.Cancel();
        if (closing || text.Length == 0)
        {
            return;
        }
        var attribution = pane.BodyLayout.HasQuote ? "" : parameters.Attribution;
        pane.Editor.FocusStart();
        pane.Editor.RewriteTarget(attribution, _ =>
        {
            // The empty target of a bridge that does not run (macOS: nil).
            if (closing || !pane.Editor.IsEditorReady)
            {
                return;
            }
            pane.Editor.ApplyRewrite(text, below: false);
        });
    }
}
