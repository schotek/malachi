// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Where the checked source tree is. The test binary lives under
// build\windows\artifacts, so the build bakes the repository root into the
// assembly (AssemblyMetadata MalachiRoot); walking up from the binary is the
// fallback for a copied or relocated build.

using System;
using System.IO;
using System.Linq;
using System.Reflection;

namespace Malachi.Conventions.Tests;

/// <summary>The repository the tests check.</summary>
internal static class RepositoryTree
{
    private static readonly Lazy<string> RootPath = new(Find);

    /// <summary>The repository root (the directory holding windows\).</summary>
    public static string Root => RootPath.Value;

    /// <summary>The Windows client's directory.</summary>
    public static string Windows => Path.Combine(Root, "windows");

    private static string Find()
    {
        var baked = typeof(RepositoryTree).Assembly
            .GetCustomAttributes<AssemblyMetadataAttribute>()
            .FirstOrDefault(a => a.Key == "MalachiRoot")?.Value;
        if (!string.IsNullOrEmpty(baked) && IsRoot(baked))
        {
            return Path.GetFullPath(baked);
        }
        for (var dir = new DirectoryInfo(AppContext.BaseDirectory); dir is not null; dir = dir.Parent)
        {
            if (IsRoot(dir.FullName))
            {
                return dir.FullName;
            }
        }
        throw new InvalidOperationException(
            "the repository root (the directory with windows/Malachi.slnx) was not found from " + AppContext.BaseDirectory);
    }

    private static bool IsRoot(string dir) => File.Exists(Path.Combine(dir, "windows", "Malachi.slnx"));
}
