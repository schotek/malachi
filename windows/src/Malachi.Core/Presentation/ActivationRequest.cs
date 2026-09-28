// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/windows-port.md §10, "Single instance and
// activation"): what one activation asks of the app, the counterpart of
// ui/main.go's activate (present the main window), open (mailto: URIs to a
// compose window only, U4) and --gapplication-service (start without a
// window), and of macOS's AppDelegate (showMainWindow, application(_:open:),
// LoginItemService.launchedAsLoginItem). The first launch and every
// redirected one go through here:
//
// - a launch without arguments, a click on a notification, or any other
//   kind shows the main window;
// - a mailto: argument opens a compose window and nothing else, as the GTK
//   open signal does (a cold start shows no main window). Only the first
//   one of an activation does: the ProgID's "%1" passes one link, and a
//   caller that splits a link with a quote in it across words, or a
//   command line that lists hundreds, would otherwise open a compose
//   window, with its WebView2 editor, for every one (GTK and macOS open
//   each). The others join the ignored arguments, logged by count;
// - --background (the Run key's launch at login) starts the app with no
//   window, as --gapplication-service does; a second launch with it does
//   nothing;
// - ----AppNotificationActivated: and -Embedding, which COM adds when a
//   notification's click starts the app, are ignored;
// - anything else is ignored and logged by the caller (GTK logs "ignoring
//   non-mailto URI").

using System;
using System.Collections.Generic;
using System.Linq;

namespace Malachi.Core.Presentation;

/// <summary>What one activation of the app asks for.</summary>
public sealed record ActivationRequest
{
    /// <summary>Starts the app with no window (the launch at login).</summary>
    public const string BackgroundOption = "--background";

    /// <summary>What COM puts on the command line of a start by a notification's click.</summary>
    public const string NotificationOption = "----AppNotificationActivated:";

    /// <summary>What COM adds for an out-of-process server.</summary>
    public const string EmbeddingOption = "-Embedding";

    private const string MailtoScheme = "mailto:";

    /// <summary>The kind of activation.</summary>
    public ActivationKind Kind { get; init; }

    /// <summary>Whether it came from a second launch (AppInstance.Activated) rather than this process's start.</summary>
    public bool Redirected { get; init; }

    /// <summary>Whether the main window is to be shown (and brought to the front).</summary>
    public bool ShowMainWindow { get; init; }

    /// <summary>The first launch asked to run in the background with no window (<see cref="BackgroundOption"/>).</summary>
    public bool StartHidden { get; init; }

    /// <summary>
    /// The <c>mailto:</c> URI to open a compose window for: at most one, the
    /// activation's first (<see cref="FromArguments"/>).
    /// </summary>
    public IReadOnlyList<string> MailtoUris { get; init => field = value ?? []; } = [];

    /// <summary>
    /// The arguments nothing here knows, and the <c>mailto:</c> URIs after
    /// the first: logged (by count: they may be addresses) and dropped.
    /// </summary>
    public IReadOnlyList<string> Ignored { get; init => field = value ?? []; } = [];

    /// <summary>
    /// The request of a whole command line (LaunchActivatedEventArgs.Arguments
    /// of an unpackaged app, which starts with the program): split as the C
    /// runtime splits it, the program's word dropped.
    /// </summary>
    public static ActivationRequest FromCommandLine(ActivationKind kind, string? commandLine, bool redirected)
    {
        var words = CommandLine.Split(commandLine);
        var arguments = words.Count > 0 && IsProgram(words[0]) ? words.Skip(1) : words;
        return FromArguments(kind, arguments, redirected);
    }

    /// <summary>
    /// The request of the arguments, without the program: the first
    /// <c>mailto:</c> URI in <see cref="MailtoUris"/>, later ones in
    /// <see cref="Ignored"/>.
    /// </summary>
    public static ActivationRequest FromArguments(ActivationKind kind, IEnumerable<string> arguments, bool redirected)
    {
        ArgumentNullException.ThrowIfNull(arguments);
        var mailto = new List<string>();
        var ignored = new List<string>();
        var background = false;
        foreach (var argument in arguments)
        {
            if (string.IsNullOrEmpty(argument))
            {
                continue;
            }
            if (argument.StartsWith(NotificationOption, StringComparison.OrdinalIgnoreCase)
                || argument.Equals(EmbeddingOption, StringComparison.OrdinalIgnoreCase))
            {
                continue;
            }
            if (argument.Equals(BackgroundOption, StringComparison.Ordinal))
            {
                background = true;
                continue;
            }
            if (argument.StartsWith(MailtoScheme, StringComparison.OrdinalIgnoreCase))
            {
                // One composer per activation; the rest are only counted.
                if (mailto.Count == 0)
                {
                    mailto.Add(argument);
                }
                else
                {
                    ignored.Add(argument);
                }
                continue;
            }
            ignored.Add(argument);
        }
        var show = kind switch
        {
            ActivationKind.Notification => true,
            _ => mailto.Count == 0 && !background,
        };
        return new ActivationRequest
        {
            Kind = kind,
            Redirected = redirected,
            ShowMainWindow = show,
            StartHidden = background && !redirected && kind != ActivationKind.Notification,
            MailtoUris = mailto,
            Ignored = ignored,
        };
    }

    // The first word of an unpackaged app's activation arguments is the
    // program. Checked rather than assumed, so that arguments without it
    // (another host's) lose nothing: a program ends in .exe, an option or a
    // mailto: URI does not start like one.
    private static bool IsProgram(string word) =>
        word.EndsWith(".exe", StringComparison.OrdinalIgnoreCase)
        && !word.StartsWith(MailtoScheme, StringComparison.OrdinalIgnoreCase)
        && !word.StartsWith('-');
}
