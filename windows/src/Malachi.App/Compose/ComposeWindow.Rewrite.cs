// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Assistant/ComposeRewriteViewController.swift
// and ComposeWindowController.swift (the Assistant button); GTK:
// ui/internal/compose/rewrite.go (wireRewrite, available, sync, close,
// clicked, fetchTarget, rewriterFor, present, hasPassage, start,
// customActivated, render, apply) and compose.blp rewrite_button,
// rewrite_popover. The compose window's rewrite (ui/internal/assistant
// rewrite.go, the In App target): the header's ✦ button while the
// one-shot requests can run (AssistantController.CanRunInApp), and its
// flyout, which rewrites the selection or the user's own text above the
// quoted original (what the editor holds before the attribution line of
// ComposeParams.Attribution) with the user's Claude Code, and puts the
// answer in its place or below it as plain text through the editor bridge,
// one step the editor's undo takes back. The first request ever asks for
// consent on this window (the panel's question, assistant-consent).
// Closing the flyout or the window ends a running request. The passage is
// never shown here; the answer is plain text in a read-only text box.
//
// Windows differences: GTK's popover is a flyout; its instruction field
// sends on Enter as GTK's entry does on activate.

using System;
using System.Collections.Generic;
using Malachi.Core.Assistants;
using Malachi.Core.Controllers;
using Malachi.Core.Html;
using Malachi.Core.I18n;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Input;
using WinUIKey = Windows.System.VirtualKey;

namespace Malachi.App.Compose;

/// <summary>The compose window's rewrite.</summary>
public sealed partial class ComposeWindow
{
    private readonly List<Button> rewritePresets = [];
    private ComposeRewriteController? rewriter;
    private RewriteTarget rewriteTarget = new();

    // The button was clicked and the flyout is on its way (consent, the
    // editor's passage).
    private bool rewriteOpening;

    private EventHandler? rewriteAssistantChanged;
    private EventHandler? rewriteProviderChanged;

    private void WireRewrite()
    {
        var t = Assistant.ComposeTexts();
        var label = Assistant.Texts().Assistant;
        ToolTipService.SetToolTip(RewriteButton, label);
        AutomationProperties.SetName(RewriteButton, label);
        foreach (var r in Assistant.Rewrites)
        {
            var button = new Button { Content = Assistant.RewriteLabel(r) };
            button.Click += (_, _) => StartRewrite(r, "");
            RewritePresets.Children.Add(button);
            rewritePresets.Add(button);
        }
        RewriteCustom.PlaceholderText = t.Custom;
        AutomationProperties.SetName(RewriteCustom, t.Custom);
        RewriteCustom.KeyDown += OnRewriteCustomKeyDown;
        RewriteStatusLabel.Text = t.Rewriting;
        RewriteDiscard.Content = Mnemonic.Strip(L10n.T("_Discard"));
        RewriteDiscard.Click += (_, _) => RewriteFlyout.Hide();
        RewriteInsert.Content = t.InsertBelow;
        RewriteInsert.Click += (_, _) => ApplyRewrite(below: true);
        RewriteReplace.Content = t.Replace;
        RewriteReplace.Click += (_, _) => ApplyRewrite(below: false);
        // The flyout went (Discard, a click elsewhere, Escape, an apply): a
        // running request ends.
        RewriteFlyout.Closed += (_, _) => rewriter?.Cancel();
        // With a passage the instruction field takes the keyboard once the
        // flyout is up (a focus asked for before it opens is lost).
        RewriteFlyout.Opened += (_, _) =>
        {
            if (HasPassage)
            {
                RewriteCustom.Focus(FocusState.Programmatic);
            }
        };

        // Whether Claude Code is there is looked up now (a few file checks),
        // so the button reflects it from the start.
        state.Assistant.RefreshHandlers();
        rewriteAssistantChanged = (_, _) => SyncRewrite();
        state.Assistant.Changed += rewriteAssistantChanged;
        rewriteProviderChanged = (_, _) =>
        {
            RewriteFlyout.Hide();
            rewriter?.Cancel();
            if (rewriter is { } active) active.Request.Provider = state.InAppProvider;
            SyncRewrite();
        };
        state.InAppProviderChanged += rewriteProviderChanged;
        SyncRewrite();
    }

    private bool RewriteAvailable => state.Assistant.CanRunInApp;

    private bool HasPassage => rewriteTarget.Text.Trim().Length > 0;

    // The button while the rewrite can run; the flyout closes when it cannot.
    private void SyncRewrite()
    {
        if (closing)
        {
            return;
        }
        var ok = RewriteAvailable;
        RewriteButton.Visibility = ok ? Visibility.Visible : Visibility.Collapsed;
        if (!ok && RewriteFlyout.IsOpen)
        {
            RewriteFlyout.Hide();
        }
    }

    // It all ends with the window.
    private void CloseRewrite()
    {
        if (rewriteProviderChanged is { } providerHandler)
        {
            state.InAppProviderChanged -= providerHandler;
            rewriteProviderChanged = null;
        }
        if (rewriteAssistantChanged is { } handler)
        {
            state.Assistant.Changed -= handler;
            rewriteAssistantChanged = null;
        }
        if (rewriter is { } rw)
        {
            rw.Request.Close();
            rw.Dispose();
            rewriter = null;
        }
    }

    // The Assistant button: the flyout, or it closes. The first request ever
    // asks for consent first, so that the question does not close the
    // flyout under it.
    private async void OnRewriteClick(object sender, RoutedEventArgs e)
    {
        if (RewriteFlyout.IsOpen)
        {
            RewriteFlyout.Hide();
            return;
        }
        if (!RewriteAvailable || rewriteOpening || closing)
        {
            return;
        }
        rewriteOpening = true;
        if (!await state.EnsureAssistantConsentAsync(this) || closing)
        {
            rewriteOpening = false;
            return;
        }
        pane.Editor.RewriteTarget(parameters.Attribution, target =>
        {
            rewriteOpening = false;
            if (!RewriteAvailable || closing)
            {
                return;
            }
            PresentRewrite(target);
        });
    }

    // "Send Mail to Claude?" on this window.
    private System.Threading.Tasks.Task<bool> AskConsentAsync()
    {
        return state.AskAssistantConsentAsync(this);
    }

    // The rewrite's controller, made on first use.
    private ComposeRewriteController Rewriter()
    {
        if (rewriter is { } existing)
        {
            return existing;
        }
        var request = state.NewAssistantRequest();
        request.Consent = AskConsentAsync;
        var made = new ComposeRewriteController(request);
        made.StateChanged += (_, _) => RenderRewrite();
        rewriter = made;
        return made;
    }

    // Opens the flyout on passage target.
    private void PresentRewrite(RewriteTarget target)
    {
        var rw = Rewriter();
        rw.Cancel();
        rewriteTarget = target;
        var texts = Assistant.ComposeTexts();
        RewriteTitle.Text = target.Selected ? texts.RewriteSelection : texts.RewriteText;
        RewriteCustom.Text = "";
        RenderRewrite();
        FlyoutBase.ShowAttachedFlyout(RewriteButton);
    }

    // Asks for rewrite r (custom: the user's own instruction).
    private void StartRewrite(AssistantRewrite r, string custom)
    {
        if (rewriter is not { } rw || rw.Running)
        {
            return;
        }
        rw.Start(r, custom, rewriteTarget.Text);
    }

    // Enter in the instruction field: its words are sent; with none, an
    // answer that is there replaces the passage.
    private void OnRewriteCustomKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key != WinUIKey.Enter)
        {
            return;
        }
        e.Handled = true;
        if (RewriteCustom.Text.Trim().Length > 0)
        {
            StartRewrite(AssistantRewrite.Custom, RewriteCustom.Text);
            return;
        }
        if (rewriter?.State is ComposeRewriteController.RewriteState.Done)
        {
            ApplyRewrite(below: false);
        }
    }

    // Follows the rewriter's state.
    private void RenderRewrite()
    {
        if (rewriter is not { } rw)
        {
            return;
        }
        var st = rw.State;
        var running = st is ComposeRewriteController.RewriteState.Running;
        foreach (var b in rewritePresets)
        {
            b.IsEnabled = HasPassage && !running;
        }
        RewriteCustom.IsEnabled = HasPassage && !running;
        RewriteStatus.Visibility = running ? Visibility.Visible : Visibility.Collapsed;
        var (text, answer, error) = st switch
        {
            ComposeRewriteController.RewriteState.Running r => (r.Preview, false, ""),
            ComposeRewriteController.RewriteState.Done d => (d.Text, true, ""),
            ComposeRewriteController.RewriteState.Failed f => ("", false, f.Text),
            _ => ("", false, ""),
        };
        if (RewriteResult.Text != text)
        {
            RewriteResult.Text = text;
        }
        RewriteResult.Visibility = text.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        RewriteError.Text = error;
        RewriteError.Visibility = error.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        RewriteInsert.IsEnabled = answer;
        RewriteReplace.IsEnabled = answer;
    }

    // Replace or Insert Below: the flyout goes, the editor takes the keyboard
    // (so that Ctrl+Z reaches its undo) and the answer as plain text.
    private void ApplyRewrite(bool below)
    {
        if (rewriter?.State is not ComposeRewriteController.RewriteState.Done done)
        {
            return;
        }
        RewriteFlyout.Hide();
        pane.Editor.FocusPage();
        pane.Editor.ApplyRewrite(done.Text, below);
    }
}
