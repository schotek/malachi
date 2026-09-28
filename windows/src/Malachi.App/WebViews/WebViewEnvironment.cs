// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only (docs/windows-port.md §6.1): the one CoreWebView2Environment of
// the app, the counterpart of what macOS sets per WKWebViewConfiguration
// (the non-persistent store and the dead proxy, MessageWebView.swift
// makeConfiguration) and GTK per view (html_view.blp's ephemeral network
// session, view.go's proxy). WebView2 keeps these per browser process, so
// every view of the app shares one environment, one user data folder and
// one set of arguments:
//
// - --host-resolver-rules="MAP * ~NOTFOUND": every host name and every IP
//   literal fails to resolve, so no DNS query and no connection happens,
//   whatever the page, a preconnect, a prerender, a hover or WebView2 itself
//   (its configuration service, SmartScreen) asks for. This is the kill
//   switch the canary proves (§12); the CSP and the request gate alone
//   leak (measured).
// - --proxy-server=127.0.0.1:1 with loopback no longer bypassed: an
//   independent second barrier, GTK's and macOS's dead proxy.
// - --disable-smooth-scrolling: GTK's viewer turns WebKit's scroll
//   animation off (html_view.blp enable-smooth-scrolling, "wheel steps
//   land at once"); a browser argument is the only switch WebView2 has for
//   it, so it holds for the editor and the previewer too.
// - the custom schemes, ASSIGNED as a new list (the getter returns a copy,
//   and adding to it registers nothing, measured): malachi-cid and cid
//   (pictures; secure, no authority), malachi-doc (documents; secure, with
//   an authority, so each view's documents are an origin of their own);
// - no browser extensions; crash dumps kept local (a renderer's dump can
//   hold mail).
//
// Every WEBVIEW2_* variable of the process is cleared first: WebView2 appends
// WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS to the app's arguments and honours
// the others (another browser, another data folder), so a stray one could
// re-enable a proxy or a debugging port. An administrator's WebView2 policy
// in the registry stays authoritative. The environment is created at start
// (the first creation once took 5 s) and on the first use at the latest;
// until it exists the views wait, and when it cannot be created (no
// runtime, a runtime too old for a setting, a failure) they fail closed:
// the reader shows the plain text with its hint, the editor reports a
// failure, the previewer shows its panel. A failure is not remembered: the
// next view tries again.

using System;
using System.Collections;
using System.Collections.Generic;
using System.IO;
using System.Threading.Tasks;
using Malachi.Core.Daemon;
using Malachi.Core.Html;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;
using Microsoft.Web.WebView2.Core;

namespace Malachi.App.WebViews;

/// <summary>The app's one hardened WebView2 environment.</summary>
public static partial class WebViewEnvironment
{
    /// <summary>The scheme of the editor's inline pictures (CIDSchemeHandler.scheme).</summary>
    public const string EditorPictureScheme = "cid";

    /// <summary>The browser arguments of docs/windows-port.md §6.1.</summary>
    public const string BrowserArguments =
        "--host-resolver-rules=\"MAP * ~NOTFOUND\" --proxy-server=127.0.0.1:1 --proxy-bypass-list=<-loopback>"
        + " --disable-smooth-scrolling";

    /// <summary>The folder under the data directory that holds WebView2's data.</summary>
    public const string UserDataFolderName = "WebView2";

    private static Task<CoreWebView2Environment?>? pending;
    private static string? userDataFolder;

    /// <summary>Where the views log (method names and kinds only, never content); the shell sets it at start.</summary>
    public static ILoggerFactory LoggerFactory { get; set; } = NullLoggerFactory.Instance;

    /// <summary>
    /// Canary only (docs/windows-port.md §12): a Chromium NetLog of the
    /// browser process, written to this file with every event, so a test can
    /// count DNS lookups and connections. Set before the environment is
    /// created; nothing in the app sets it.
    /// </summary>
    internal static string? NetLogPath { get; set; }

    /// <summary>The user data folder in use (or to be used); null until <see cref="Start"/> or the first use.</summary>
    public static string? UserDataFolder => userDataFolder;

    /// <summary>
    /// Starts creating the environment in <paramref name="folder"/>
    /// (<c>&lt;data dir&gt;\WebView2</c>) unless it exists or is being
    /// created. Call on the UI thread, at start.
    /// </summary>
    public static void Start(string folder)
    {
        ArgumentException.ThrowIfNullOrEmpty(folder);
        userDataFolder ??= folder;
        _ = GetAsync();
    }

    /// <summary>
    /// The environment, created on the first call (in the folder
    /// <see cref="Start"/> named, or <c>&lt;data dir&gt;\WebView2</c>); null
    /// when it cannot be created, and then the next call tries again. Call on
    /// the UI thread.
    /// </summary>
    public static Task<CoreWebView2Environment?> GetAsync()
    {
        if (pending is { IsCompleted: true, Result: null })
        {
            pending = null;
        }
        return pending ??= CreateAsync();
    }

    /// <summary>
    /// Forgets <paramref name="environment"/> after its browser process died
    /// (ProcessFailed, BrowserProcessExited): the next <see cref="GetAsync"/>
    /// creates a new one.
    /// </summary>
    internal static void Invalidate(CoreWebView2Environment environment)
    {
        if (pending is { IsCompletedSuccessfully: true } p && ReferenceEquals(p.Result, environment))
        {
            pending = null;
        }
    }

    private static async Task<CoreWebView2Environment?> CreateAsync()
    {
        var log = LoggerFactory.CreateLogger("Malachi.App.WebViews");
        try
        {
            ClearWebView2Variables();
            string version;
            try
            {
                version = CoreWebView2Environment.GetAvailableBrowserVersionString();
            }
            catch (Exception e) when (e is not OutOfMemoryException)
            {
                LogNoRuntime(log, e);
                return null;
            }
            if (string.IsNullOrEmpty(version))
            {
                LogNoRuntime(log, null);
                return null;
            }
            var folder = userDataFolder ??= Path.Combine(Paths.Resolve().DataDir, UserDataFolderName);
            Directory.CreateDirectory(folder);
            var environment = await CoreWebView2Environment.CreateWithOptionsAsync("", folder, Options());
            LogCreated(log, environment.BrowserVersionString);
            return environment;
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            LogCreateFailed(log, e);
            return null;
        }
    }

    private static CoreWebView2EnvironmentOptions Options()
    {
        var arguments = BrowserArguments;
        if (NetLogPath is { Length: > 0 } netLog)
        {
            arguments += " --log-net-log=\"" + netLog + "\" --net-log-capture-mode=Everything";
        }
        var options = new CoreWebView2EnvironmentOptions
        {
            AdditionalBrowserArguments = arguments,
            AreBrowserExtensionsEnabled = false,
            IsCustomCrashReportingEnabled = true,
            AllowSingleSignOnUsingOSPrimaryAccount = false,
            ExclusiveUserDataFolderAccess = true,
        };
        // Assigned, never added to: the getter hands out a copy.
        options.CustomSchemeRegistrations = new List<CoreWebView2CustomSchemeRegistration>
        {
            new(PartPath.PartScheme) { TreatAsSecure = 1, HasAuthorityComponent = false },
            new(EditorPictureScheme) { TreatAsSecure = 1, HasAuthorityComponent = false },
            new(Core.Presentation.DocumentAddress.Scheme) { TreatAsSecure = 1, HasAuthorityComponent = true },
        };
        return options;
    }

    // Every WEBVIEW2_* variable of this process: the loader reads them when
    // the environment is created.
    private static void ClearWebView2Variables()
    {
        var names = new List<string>();
        foreach (DictionaryEntry entry in Environment.GetEnvironmentVariables())
        {
            if (entry.Key is string name && name.StartsWith("WEBVIEW2_", StringComparison.OrdinalIgnoreCase))
            {
                names.Add(name);
            }
        }
        foreach (var name in names)
        {
            Environment.SetEnvironmentVariable(name, null);
        }
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "WebView2 environment created, runtime {Version}")]
    private static partial void LogCreated(ILogger logger, string version);

    [LoggerMessage(Level = LogLevel.Warning, Message = "no WebView2 runtime; HTML is shown as text")]
    private static partial void LogNoRuntime(ILogger logger, Exception? error);

    [LoggerMessage(Level = LogLevel.Error, Message = "the WebView2 environment could not be created; HTML is shown as text")]
    private static partial void LogCreateFailed(ILogger logger, Exception error);
}
