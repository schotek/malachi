// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The command line of MalachiMail.exe (docs/windows-port.md §10, "Single
// instance and activation"): GTK's are ui/main.go's (the Background
// portal's --gapplication-service starts the app held in the background;
// the "open" signal takes mailto: URIs and logs and ignores any other URI),
// macOS's the login item launch and application(_:open:). On Windows:
//   --background       the Run value of launch at login: start hidden;
//   mailto:…           the ProgID's "exe" "%1": open a compose window;
//   ----AppNotificationActivated:  and  -Embedding
//                      what COM adds when a click on a notification starts
//                      the app (measured in APP-SPIKES §1.3): not files,
//                      not URIs, never shown to the user; the activation
//                      itself arrives through AppInstance.GetActivatedEventArgs;
//   anything else      unrecognised, for the caller to log and ignore.
// A second instance hands its whole command line (the program name first)
// to the running one through AppInstance.Activated; ParseCommandLine reads
// it with the rules the C runtime uses for argv (CommandLineToArgvW).

using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using Windows.Win32;
using Windows.Win32.Foundation;

namespace Malachi.Platform.Windows.Startup;

/// <summary>What the app was started (or re-activated) to do.</summary>
public sealed record LaunchArguments
{
    /// <summary>The argument of the launch-at-login Run value.</summary>
    public const string BackgroundArgument = LaunchAtLogin.BackgroundArgument;

    /// <summary>The first argument COM passes when a notification's click starts the app.</summary>
    public const string NotificationActivatedArgument = "----AppNotificationActivated:";

    /// <summary>The argument COM adds for an out-of-process server.</summary>
    public const string EmbeddingArgument = "-Embedding";

    /// <summary>Start hidden in the background (<c>--background</c>).</summary>
    public bool Background { get; init; }

    /// <summary>A click on a notification started the app (the COM arguments were present).</summary>
    public bool FromNotification { get; init; }

    /// <summary>The <c>mailto:</c> URIs, in order, as given.</summary>
    public IReadOnlyList<string> MailtoUris { get; init; } = [];

    /// <summary>Everything else, in order: to be logged and ignored, as GTK ignores a URI it cannot open.</summary>
    public IReadOnlyList<string> Unrecognised { get; init; } = [];

    /// <summary>The arguments of <paramref name="args"/>, without the program name (Main's <c>args</c>).</summary>
    public static LaunchArguments Parse(IEnumerable<string> args)
    {
        ArgumentNullException.ThrowIfNull(args);
        var background = false;
        var fromNotification = false;
        var mailto = new List<string>();
        var other = new List<string>();
        foreach (var arg in args)
        {
            if (string.IsNullOrWhiteSpace(arg))
            {
                continue;
            }
            if (string.Equals(arg, BackgroundArgument, StringComparison.OrdinalIgnoreCase))
            {
                background = true;
            }
            else if (arg.StartsWith(NotificationActivatedArgument, StringComparison.OrdinalIgnoreCase))
            {
                fromNotification = true;
            }
            else if (string.Equals(arg, EmbeddingArgument, StringComparison.OrdinalIgnoreCase))
            {
                // COM's; it says nothing of its own.
            }
            else if (arg.StartsWith("mailto:", StringComparison.OrdinalIgnoreCase))
            {
                mailto.Add(arg);
            }
            else
            {
                other.Add(arg);
            }
        }
        return new LaunchArguments
        {
            Background = background,
            FromNotification = fromNotification,
            MailtoUris = mailto,
            Unrecognised = other,
        };
    }

    /// <summary>
    /// The arguments of a whole command line (<c>GetCommandLineW</c>, or the
    /// <c>Arguments</c> of a redirected launch), split as the C runtime
    /// splits argv; the first token is the program name and is skipped when
    /// <paramref name="startsWithProgram"/>.
    /// </summary>
    public static LaunchArguments ParseCommandLine(string? commandLine, bool startsWithProgram = true)
    {
        var argv = Split(commandLine);
        return Parse(startsWithProgram && argv.Count > 0 ? argv.GetRange(1, argv.Count - 1) : argv);
    }

    /// <summary>
    /// <paramref name="commandLine"/> split by <c>CommandLineToArgvW</c>.
    /// The first token follows the program-name rules (quotes, no escapes);
    /// an empty line gives no tokens (the function would answer with this
    /// process's own path).
    /// </summary>
    public static unsafe List<string> Split(string? commandLine)
    {
        var tokens = new List<string>();
        if (string.IsNullOrWhiteSpace(commandLine))
        {
            return tokens;
        }
        int count;
        PWSTR* argv;
        fixed (char* line = commandLine)
        {
            argv = PInvoke.CommandLineToArgv(line, &count);
        }
        if (argv is null)
        {
            throw new ArgumentException($"the command line was not split ({Marshal.GetLastPInvokeError()})", nameof(commandLine));
        }
        try
        {
            for (var i = 0; i < count; i++)
            {
                tokens.Add(argv[i].ToString());
            }
        }
        finally
        {
            PInvoke.LocalFree(new HLOCAL(argv));
        }
        return tokens;
    }
}
