// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/mcp.md, "Where the files are": Windows, MSIX
// package; docs/windows-port.md §10 MCP registration, §14 item 5): where
// the Claude Desktop of the Microsoft Store keeps its MCP configuration.
// The bridge knows no platform's packaging, so the Windows app tells it
// with --claude-desktop-config; neither GTK nor macOS needs this.
//
// A packaged app sees a private copy of AppData, its LocalCache under
// %LOCALAPPDATA%\Packages\<family>. Outside the package %APPDATA%\Claude
// is not the file that app reads (usually it does not exist, and where a
// classic install left it, it is the wrong file).

using System;
using System.IO;

namespace Malachi.Core.Platform;

/// <summary>
/// The MSIX package of Claude Desktop for the current user: whether it is
/// installed (its package directory exists) and the configuration file it
/// reads. Both the package family and the directory of the packages are
/// replaceable, for tests.
/// </summary>
public sealed record ClaudeDesktopPackage
{
    /// <summary>The package family name of Claude Desktop from the Microsoft Store.</summary>
    public const string ClaudePackageFamily = "Claude_pzs8sxrjxfjjc";

    /// <summary>Where the packages keep their data: <c>%LOCALAPPDATA%\Packages</c>.</summary>
    public required string PackagesDirectory { get; init; }

    /// <summary>The package family (<see cref="ClaudePackageFamily"/>).</summary>
    public string PackageFamily { get; init; } = ClaudePackageFamily;

    /// <summary>The package's data directory: it exists once the package is installed for the user.</summary>
    public string PackageDirectory => Path.Combine(PackagesDirectory, PackageFamily);

    /// <summary>The configuration file the packaged Claude Desktop reads.</summary>
    public string ConfigPath => Path.Combine(PackageDirectory, "LocalCache", "Roaming", "Claude", "claude_desktop_config.json");

    /// <summary>Whether the package is installed for the user (its data directory exists).</summary>
    public bool IsInstalled => Directory.Exists(PackageDirectory);

    /// <summary>
    /// The package of the current user: under <c>LOCALAPPDATA</c>, or the
    /// system's local application data folder when the variable is unset.
    /// </summary>
    public static ClaudeDesktopPackage ForCurrentUser()
    {
        var local = Environment.GetEnvironmentVariable("LOCALAPPDATA");
        if (string.IsNullOrEmpty(local))
        {
            local = Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData);
        }
        return new ClaudeDesktopPackage { PackagesDirectory = Path.Combine(local, "Packages") };
    }
}
