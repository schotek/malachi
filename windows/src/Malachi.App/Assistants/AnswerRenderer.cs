// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Assistant/AssistantTranscriptViews.swift
// (the answer's attributed string from Assistant.markdown); GTK:
// ui/internal/window/assistant_panel.go (answerView: render, the tags for
// the block kinds and the spans, the link tags). An answer of the
// assistant panel is hostile text like mail (it may quote a message), so
// it reaches the screen only as Runs whose Text is set from code: the
// Markdown subset of Assistant.Markdown turned into fonts, sizes and
// margins, never markup, never HTML, never a XAML parser. A link is a
// Hyperlink without a NavigateUri, so WinUI opens nothing itself: its
// click goes to the caller, which confirms the destination first
// ("Open This Link?", as a link in a message the daemon did not list).
//
// Windows differences: a code span has the monospaced face without GTK's
// tinted background (a Run has none in WinUI), a code block the same with
// GTK's left margin; the list indent and the heading sizes are GTK's.

using System;
using System.Globalization;
using Malachi.Core.Assistants;
using Microsoft.UI.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Documents;
using Microsoft.UI.Xaml.Media;

namespace Malachi.App.Assistants;

/// <summary>Draws an answer's Markdown subset into a <see cref="RichTextBlock"/>.</summary>
internal static class AnswerRenderer
{
    // One list level's indent, in pixels (assistant_panel.go listIndent).
    private const double ListIndent = 14;

    /// <summary>
    /// Replaces the blocks of <paramref name="view"/> with
    /// <paramref name="markdown"/> drawn; a link's click calls
    /// <paramref name="openLink"/> with its URL (an http or https address,
    /// Assistant.Markdown's only links).
    /// </summary>
    public static void Render(RichTextBlock view, string markdown, Action<string> openLink)
    {
        ArgumentNullException.ThrowIfNull(view);
        ArgumentNullException.ThrowIfNull(openLink);
        view.Blocks.Clear();
        var baseSize = view.FontSize;
        var mono = (FontFamily)Application.Current.Resources["MonospaceFontFamily"];
        foreach (var block in Assistant.Markdown(markdown))
        {
            var paragraph = new Paragraph { Margin = new Thickness(0, 0, 0, 6) };
            var prefix = "";
            switch (block.Kind)
            {
                case MarkdownBlockKind.Heading:
                    paragraph.FontWeight = FontWeights.Bold;
                    paragraph.FontSize = baseSize * block.Level switch { 1 => 1.3, 2 => 1.15, _ => 1.0 };
                    paragraph.Margin = new Thickness(0, 4, 0, 4);
                    break;
                case MarkdownBlockKind.Bullet or MarkdownBlockKind.Numbered:
                    var level = Math.Min(Math.Max(block.Level, 0), 3);
                    paragraph.Margin = new Thickness((level + 1) * ListIndent, 0, 0, 3);
                    paragraph.TextIndent = -ListIndent + 2;
                    prefix = block.Kind == MarkdownBlockKind.Bullet
                        ? "•  "
                        : block.Number.ToString(CultureInfo.InvariantCulture) + ". ";
                    break;
                case MarkdownBlockKind.Code:
                    paragraph.FontFamily = mono;
                    paragraph.Margin = new Thickness(6, 0, 0, 6);
                    break;
            }
            if (prefix.Length > 0)
            {
                paragraph.Inlines.Add(new Run { Text = prefix });
            }
            foreach (var span in block.Spans)
            {
                var run = new Run { Text = span.Text };
                if (span.Bold)
                {
                    run.FontWeight = FontWeights.Bold;
                }
                if (span.Italic)
                {
                    run.FontStyle = Windows.UI.Text.FontStyle.Italic;
                }
                if (span.Code && block.Kind != MarkdownBlockKind.Code)
                {
                    run.FontFamily = mono;
                }
                if (span.Link.Length > 0)
                {
                    var link = new Hyperlink();
                    var target = span.Link;
                    link.Click += (_, _) => openLink(target);
                    link.Inlines.Add(run);
                    paragraph.Inlines.Add(link);
                }
                else
                {
                    paragraph.Inlines.Add(run);
                }
            }
            view.Blocks.Add(paragraph);
        }
    }
}
