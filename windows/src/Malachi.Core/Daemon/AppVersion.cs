// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Daemon/Version.swift (Version); the
// short form follows macos/Makefile (SHORT_VERSION). Not called Version as
// in Swift: System.Version is in scope wherever System is, and a second
// Version would make every such use ambiguous; short is ShortVersion, as
// CA1720 refuses a member named like a type. windows/build.ps1 bakes the
// git describe string into every assembly's informational version
// (Directory.Build.props: MalachiVersion), the counterpart of Info.plist's
// MalachiVersion; a build from Visual Studio says "dev".

using System.Reflection;
using System.Text.RegularExpressions;

namespace Malachi.Core.Daemon;

/// <summary>The build's version.</summary>
public static partial class AppVersion
{
    /// <summary>
    /// The full <c>git describe</c> string (Version.swift <c>full</c>);
    /// <c>dev</c> when the build was not given one.
    /// </summary>
    public static string Full { get; } = FullOf(
        typeof(AppVersion).Assembly.GetCustomAttribute<AssemblyInformationalVersionAttribute>()?.InformationalVersion);

    /// <summary>
    /// One to three integers (Version.swift <c>short</c>, the
    /// CFBundleShortVersionString of macos/Makefile).
    /// </summary>
    public static string ShortVersion { get; } = ShortOf(Full);

    /// <summary>The full version of an informational version attribute's value: "dev" when there is none.</summary>
    public static string FullOf(string? informationalVersion) =>
        string.IsNullOrWhiteSpace(informationalVersion) ? "dev" : informationalVersion.Trim();

    /// <summary>
    /// The leading one to three dot-separated integers of
    /// <paramref name="full"/>: 0.1.0-33-gabc1234-dirty gives 0.1.0, a bare
    /// commit hash or "dev" gives 0.0.0 (macos/Makefile SHORT_VERSION).
    /// </summary>
    public static string ShortOf(string full)
    {
        var match = ShortPattern().Match(full);
        return match.Success ? match.Groups[1].Value : "0.0.0";
    }

    [GeneratedRegex(@"^([0-9]+(\.[0-9]+){0,2})", RegexOptions.CultureInvariant)]
    private static partial Regex ShortPattern();
}
