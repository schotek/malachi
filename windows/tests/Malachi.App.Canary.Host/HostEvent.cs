// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// One thing a view did during a canary run (HostResults.Events).

namespace Malachi.App.Canary.Host;

/// <summary>An event the host saw.</summary>
public sealed record HostEvent
{
    /// <summary>What happened (<see cref="Kinds"/>).</summary>
    public required string Kind { get; init; }

    /// <summary>The view it happened in.</summary>
    public string View { get; init; } = "";

    /// <summary>The phase of the run (the last step's <see cref="HostStep.Phase"/>).</summary>
    public string Phase { get; init; } = "";

    /// <summary>The URL concerned, when there is one.</summary>
    public string? Uri { get; init; }

    /// <summary>Whether the view cancelled (a navigation, a download) or handled (a new window) it.</summary>
    public bool? Stopped { get; init; }

    /// <summary>A status (an HTTP status the gate answered, a process kind).</summary>
    public string? Detail { get; init; }

    /// <summary>Milliseconds since the run started.</summary>
    public long Ms { get; init; }

    /// <summary>The <see cref="Detail"/> of a <see cref="Kinds.Navigation"/> WebView2 calls user-initiated.</summary>
    public const string UserInitiated = "IsUserInitiated=True";

    /// <summary>The <see cref="Detail"/> of a <see cref="Kinds.Navigation"/> WebView2 does not call user-initiated.</summary>
    public const string NotUserInitiated = "IsUserInitiated=False";

    /// <summary>The event kinds.</summary>
    public static class Kinds
    {
        /// <summary>The view is initialised.</summary>
        public const string Ready = "ready";

        /// <summary>The view reported itself unavailable.</summary>
        public const string Unavailable = "unavailable";

        /// <summary>
        /// NavigationStarting; <see cref="Stopped"/>: cancelled;
        /// <see cref="Detail"/>: <see cref="UserInitiated"/> or <see cref="NotUserInitiated"/>.
        /// </summary>
        public const string Navigation = "navigation";

        /// <summary>NavigationCompleted; <see cref="Uri"/>: the view's source then.</summary>
        public const string Completed = "completed";

        /// <summary>SourceChanged; <see cref="Uri"/>: the new source.</summary>
        public const string Source = "source";

        /// <summary>FrameNavigationStarting; <see cref="Stopped"/>: cancelled.</summary>
        public const string Frame = "frame";

        /// <summary>NewWindowRequested; <see cref="Stopped"/>: handled without a window.</summary>
        public const string NewWindow = "newWindow";

        /// <summary>DownloadStarting; <see cref="Stopped"/>: cancelled.</summary>
        public const string Download = "download";

        /// <summary>LaunchingExternalUriScheme; <see cref="Stopped"/>: cancelled.</summary>
        public const string ExternalScheme = "externalScheme";

        /// <summary>A request the gate answered; <see cref="Detail"/>: its status.</summary>
        public const string Request = "request";

        /// <summary>The viewer or a card handed on a link; <see cref="Detail"/>: the attribute as written.</summary>
        public const string Link = "link";

        /// <summary>A conversation card measured its document; <see cref="Detail"/>: the height in CSS pixels, then "viewport" or "-".</summary>
        public const string Size = "size";

        /// <summary>The viewer's hover label changed.</summary>
        public const string Hover = "hover";

        /// <summary>A WebView2 process failed; <see cref="Detail"/>: which.</summary>
        public const string ProcessFailed = "processFailed";

        /// <summary>A visible top-level window of the browser process appeared.</summary>
        public const string Window = "window";

        /// <summary>A visible window of the browser process at the end; <see cref="Detail"/>: class | title | process.</summary>
        public const string Title = "title";

        /// <summary>A file appeared in the download folder.</summary>
        public const string DownloadedFile = "downloadedFile";

        /// <summary>The editor's bridge: <see cref="Detail"/> ready, key …, crashed (the host then reloads its text).</summary>
        public const string Bridge = "bridge";

        /// <summary>A flush of the editor ended; <see cref="Detail"/>: its HTML then.</summary>
        public const string Flushed = "flushed";

        /// <summary>Files dropped on the editor; <see cref="Detail"/>: their paths, | between.</summary>
        public const string Dropped = "dropped";

        /// <summary>A probe script's result; <see cref="Detail"/>: its JSON.</summary>
        public const string Probe = "probe";

        /// <summary>The DevTools call that crashed a renderer ended; <see cref="Detail"/>: how.</summary>
        public const string Crash = "crash";

        /// <summary>A view logged (Information and above); <see cref="View"/>: its kind; <see cref="Detail"/>: level and message.</summary>
        public const string Log = "log";

        /// <summary>Something of the harness failed; <see cref="Detail"/>: what.</summary>
        public const string Error = "error";
    }
}
