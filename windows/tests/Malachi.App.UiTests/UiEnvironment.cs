// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Where the UI tests find the app, and why they cannot run here: not
// Windows, no interactive desktop (a service, a CI runner without a
// session), the app not built (build.ps1 app assembles
// build\windows\<arch>\Malachi Mail\; MALACHI_UITEST_APP names another
// folder), or a copy started from that folder running already: the app is
// a single instance per executable, so a test's launch would only hand its
// activation to that copy, the user's.

using System;
using System.Diagnostics;
using System.IO;
using System.Linq;
using System.Reflection;
using System.Runtime.InteropServices;

namespace Malachi.App.UiTests;

/// <summary>The app under test and the reasons to skip.</summary>
internal static class UiEnvironment
{
    /// <summary>Names an app folder other than build.ps1's.</summary>
    public const string AppFolderEnv = "MALACHI_UITEST_APP";

    /// <summary>The fake keyring helper's store (Malachi.FakeKeyring).</summary>
    public const string FakeKeyringFileEnv = "MALACHI_FAKE_KEYRING_FILE";

    /// <summary>The app folder: MALACHI_UITEST_APP, else build.ps1's for this machine's architecture.</summary>
    public static string AppFolder
    {
        get
        {
            var fromEnv = Environment.GetEnvironmentVariable(AppFolderEnv);
            if (!string.IsNullOrEmpty(fromEnv))
            {
                return Path.GetFullPath(fromEnv);
            }
            var arch = RuntimeInformation.OSArchitecture == Architecture.Arm64 ? "arm64" : "x64";
            return Path.Combine(BuildPath, "windows", arch, "Malachi Mail");
        }
    }

    /// <summary>MalachiMail.exe in <see cref="AppFolder"/>.</summary>
    public static string AppExecutable => Path.Combine(AppFolder, "MalachiMail.exe");

    /// <summary>The keyring helper built beside the tests.</summary>
    public static string FakeKeyring => Path.Combine(AppContext.BaseDirectory, "Malachi.FakeKeyring.exe");

    /// <summary>Why the smoke tests cannot run here; null when they can.</summary>
    public static string? SkipReason
    {
        get
        {
            if (!OperatingSystem.IsWindows())
            {
                return "the UI tests need Windows";
            }
            if (!Environment.UserInteractive)
            {
                return "the UI tests need a desktop session (this process is not interactive)";
            }
            if (!File.Exists(AppExecutable))
            {
                return $"the app is not built: {AppExecutable} (windows\\build.ps1 app)";
            }
            if (!File.Exists(FakeKeyring))
            {
                return "the fake keyring helper was not built: " + FakeKeyring;
            }
            if (RunningCopy() is { } id)
            {
                return $"MalachiMail.exe from {AppFolder} runs already (pid {id}); the tests' launch would only activate it";
            }
            return null;
        }
    }

    // The repository's build folder baked in at build time (-BuildDir moves it).
    private static string BuildPath =>
        typeof(UiEnvironment).Assembly.GetCustomAttributes<AssemblyMetadataAttribute>().First(a => a.Key == "MalachiBuildPath").Value
        ?? throw new InvalidOperationException("no MalachiBuildPath");

    private static int? RunningCopy()
    {
        foreach (var p in Process.GetProcessesByName("MalachiMail"))
        {
            using (p)
            {
                try
                {
                    if (string.Equals(p.MainModule?.FileName, AppExecutable, StringComparison.OrdinalIgnoreCase))
                    {
                        return p.Id;
                    }
                }
                catch (Exception e) when (e is System.ComponentModel.Win32Exception or InvalidOperationException)
                {
                    // Another user's, or gone meanwhile.
                }
            }
        }
        return null;
    }
}
