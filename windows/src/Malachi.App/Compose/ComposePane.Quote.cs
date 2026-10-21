// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Compose/ComposePane.swift
// (makeQuoteSection, toggleQuote, changeQuoteIncluded, quotedText, the
// quoted editor's load and crash) and ComposeEditorView.swift (readOnly);
// GTK: ui/data/ui/compose_extras.blp (quote_section) and
// ui/internal/composebody/layout.go, with the texts of
// ui/internal/assistant/compose_write.go (WriteTexts). The window layout
// shows a new reply's or forward's original, as draft.create prepared it,
// in a read-only view under the editor (BodyLayout): its disclosure only
// hides the view, its checkbox decides whether the draft and the message
// include the original (EditorHtml, EditorText), and a change of it marks
// the draft dirty. The view is the compose editor's WebView2 with a body
// that is not editable (ComposeWebView readOnly), under the same CSP,
// request gate and cid: registry; nothing it reports is an edit, files
// dropped on it are not attached, and a dead page loads the original again.
//
// Windows differences: the disclosure's state is the button's help text
// ("Expanded" / "Collapsed"), as the date groups of the message list say
// it; the view is named "Quoted Message" for Narrator and is not marked as
// an editor for the window's keys (KeyboardRouting), so Escape there is the
// window's close request; FlushEditor flushes it before the editor (see
// there).

using Malachi.App.Resources;
using Malachi.Core.Assistants;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;

namespace Malachi.App.Compose;

/// <summary>The prepared original under the editor of a compose pane.</summary>
public sealed partial class ComposePane
{
    // The view of the original is shown (the disclosure); collapsed at
    // first.
    private bool quoteExpanded;

    /// <summary>The plain text of the prepared original ("" without one).</summary>
    public string QuotedText => quoted?.Text ?? "";

    // Whether the draft and the message include the original (the
    // checkbox, checked at first).
    private bool IncludesQuote => IncludeQuote.IsChecked == true;

    // makeQuoteSection: the section of the window layout, shown with an
    // original only (BodyLayout has none inline).
    private void WireQuote()
    {
        if (quoted is not { } view)
        {
            QuoteSection.Visibility = Visibility.Collapsed;
            return;
        }
        var texts = Assistant.WriteTexts(ComposeWriteController.Mode(parameters.Kind));
        QuoteDisclosureLabel.Text = texts.Quoted;
        AutomationProperties.SetName(QuoteDisclosure, texts.Quoted);
        IncludeQuote.Content = texts.Include;
        AutomationProperties.SetName(IncludeQuote, texts.Include);
        QuotedSlot.Child = view;
        AutomationProperties.SetName(view.Web, texts.Quoted);
        AutomationProperties.SetAutomationId(view.Web, "ComposeQuoted");
        view.CoreWebViewInitialized += (_, _) =>
        {
            AutomationProperties.SetName(view.Web, texts.Quoted);
            AutomationProperties.SetAutomationId(view.Web, "ComposeQuoted");
        };
        // Escape while the view has the keyboard and the window's keys did
        // not take it: the window's close request, as the editor's.
        view.Channel.KeyPressed += (_, key) =>
        {
            if (key == "escape" && !PopupOpen)
            {
                EscapePressed?.Invoke(this, System.EventArgs.Empty);
            }
        };
        // The page died, or its document could not be loaded: the original
        // again (once per document, ComposeWebView).
        view.Crashed += (_, _) =>
        {
            if (!Gone)
            {
                view.Load(BodyLayout.QuotedHtml);
            }
        };
        RenderQuoteDisclosure();
        QuoteSection.Visibility = Visibility.Visible;
    }

    // toggleQuote: the view hidden or shown; nothing else changes.
    private void OnQuoteDisclosureClick(object sender, RoutedEventArgs e)
    {
        quoteExpanded = !quoteExpanded;
        RenderQuoteDisclosure();
    }

    private void RenderQuoteDisclosure()
    {
        QuotedSlot.Visibility = quoteExpanded ? Visibility.Visible : Visibility.Collapsed;
        QuoteDisclosureIcon.Glyph = Icons.Glyph(quoteExpanded ? "pan-down-symbolic" : "pan-end-symbolic");
        AutomationProperties.SetHelpText(QuoteDisclosure, quoteExpanded ? L10n.T("Expanded") : L10n.T("Collapsed"));
    }

    // changeQuoteIncluded: the draft's body changed.
    private void OnIncludeQuoteClick(object sender, RoutedEventArgs e)
    {
        if (!Gone)
        {
            draft.MarkDirty();
        }
    }
}
