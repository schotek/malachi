// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first Codex counterpart of ClaudeCodeLocator.cs;
// GTK reference: ui/internal/assistantpanel/locator.go; no Swift Codex port yet.

using System;
using System.Collections.Generic;
using System.IO;
using System.Reflection.PortableExecutable;
using System.Runtime.InteropServices;
using System.Text;
using System.Text.RegularExpressions;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Assistants;

namespace Malachi.Core.Platform;

/// <summary>Finds a native Windows executable, never a shell shim or a WSL command.</summary>
public static partial class CodexExecutable
{
    /// <summary>The official installation instructions; no automatic download.</summary>
    public const string InstallUrl = "https://developers.openai.com/codex/cli/";

    /// <summary>An explicit selection is authoritative, including an invalid selection.</summary>
    public static string? Resolve(string selectedPath) => selectedPath.Length == 0
        ? AutomaticPath()
        : IsExecutableFile(selectedPath) ? Path.GetFullPath(selectedPath) : null;

    /// <summary>Known user installation locations, then PATH; no command interpreter.</summary>
    public static string? AutomaticPath()
    {
        var home = Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
        var local = Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData);
        var candidates = new List<string>
        {
            Path.Combine(home, ".local", "bin", "codex.exe"),
            Path.Combine(home, "scoop", "shims", "codex.exe"),
            Path.Combine(local, "Microsoft", "WinGet", "Links", "codex.exe"),
        };
        foreach (var part in (Environment.GetEnvironmentVariable("PATH") ?? "").Split(';', StringSplitOptions.RemoveEmptyEntries))
        {
            var directory = part.Trim().Trim('"');
            if (Path.IsPathFullyQualified(directory)) candidates.Add(Path.Combine(directory, "codex.exe"));
        }
        foreach (var path in candidates)
        {
            if (IsExecutableFile(path)) return Path.GetFullPath(path);
        }
        return null;
    }

    /// <summary>Regular PE executable matching this process's supported architecture.</summary>
    public static bool IsExecutableFile(string path)
    {
        if (Assistant.CleanWindowsPath(path) is null || !path.EndsWith(".exe", StringComparison.OrdinalIgnoreCase)) return false;
        try
        {
            if (!File.Exists(path) || (File.GetAttributes(path) & FileAttributes.Directory) != 0) return false;
            using var file = File.OpenRead(path);
            using var image = new PEReader(file);
            var expected = RuntimeInformation.ProcessArchitecture switch
            {
                Architecture.X64 => Machine.Amd64,
                Architecture.Arm64 => Machine.Arm64,
                _ => Machine.Unknown,
            };
            return expected != Machine.Unknown && image.PEHeaders.PEHeader is not null
                && image.PEHeaders.CoffHeader.Machine == expected
                && (image.PEHeaders.CoffHeader.Characteristics & Characteristics.ExecutableImage) != 0
                && (image.PEHeaders.CoffHeader.Characteristics & Characteristics.Dll) == 0;
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException or BadImageFormatException or ArgumentException or NotSupportedException)
        {
            return false;
        }
    }

    /// <summary>A bounded, validated version line; never stderr or arbitrary process output.</summary>
    public static async Task<string?> VersionAsync(string path, CancellationToken cancellationToken = default)
    {
        if (!IsExecutableFile(path)) return null;
        var environment = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
        foreach (var key in new[] { "SystemRoot", "WINDIR", "TEMP", "TMP", "USERPROFILE", "LOCALAPPDATA", "PATH" })
        {
            if (Environment.GetEnvironmentVariable(key) is { } value) environment[key] = value;
        }
        try
        {
            var output = await new BridgeRunner().RunAsync(path, ["--version"], TimeSpan.FromSeconds(10), environment,
                Path.GetDirectoryName(path), cancellationToken).ConfigureAwait(false);
            return SafeVersion(output.Stdout, output.Status);
        }
        catch (BridgeRunnerException)
        {
            return null;
        }
    }

    /// <summary>Validates a version response before displaying any process output.</summary>
    public static string? SafeVersion(ReadOnlyMemory<byte> output, int status)
    {
        if (status != 0 || output.Length > 256) return null;
        var line = Encoding.UTF8.GetString(output.Span).Trim();
        return VersionLine().IsMatch(line) ? line : null;
    }

    [GeneratedRegex(@"\Acodex(?:-cli)? [0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?\z", RegexOptions.CultureInvariant)]
    private static partial Regex VersionLine();
}
