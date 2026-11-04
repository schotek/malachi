// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: the small functions the reader's {x:Bind} calls
// ({x:Bind local:ReaderBind.Visible(Reader.HintVisible), Mode=OneWay}),
// as Resources/Bind.cs is for the shell. Nothing here formats mail data.

using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Reader;

/// <summary>Functions for the reader's x:Bind.</summary>
public static class ReaderBind
{
    /// <summary>Visible for true, collapsed for false.</summary>
    public static Visibility Visible(bool on) => on ? Visibility.Visible : Visibility.Collapsed;

    /// <summary>Collapsed for true, visible for false.</summary>
    public static Visibility Hidden(bool on) => on ? Visibility.Collapsed : Visibility.Visible;

    /// <summary>Collapsed for an empty string.</summary>
    public static Visibility VisibleIfText(string? text) => string.IsNullOrEmpty(text) ? Visibility.Collapsed : Visibility.Visible;

    /// <summary>Visible while the message stack shows the page named <paramref name="name"/>.</summary>
    public static Visibility PageVisible(ReaderPage page, string name) => Visible(page.ToString() == name);

    /// <summary>Visible while the body area shows the page named <paramref name="name"/>.</summary>
    public static Visibility BodyVisible(ReaderBodyPage page, string name) => Visible(page.ToString() == name);

    /// <summary>Whether the body area shows its spinner.</summary>
    public static bool IsLoading(ReaderBodyPage page) => page == ReaderBodyPage.Loading;

    /// <summary>The outbox banner warns when the delivery failed.</summary>
    public static InfoBarSeverity SeverityOf(bool failed) => failed ? InfoBarSeverity.Warning : InfoBarSeverity.Informational;

    /// <summary>
    /// The remote-image bar's trust button: not while the images load, never
    /// in an attached message's window (the containing message's sender
    /// decides the policy).
    /// </summary>
    public static Visibility TrustVisible(bool loading, bool offered) => Visible(!loading && offered);
}
