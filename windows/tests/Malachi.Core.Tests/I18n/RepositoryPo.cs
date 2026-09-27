// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The repository's po/ for the catalogue tests: the counterpart of the
// MALACHI_LOCALE_DIR that make test-macos exports and of build/locale that
// ui/internal/i18n's TestCzechCatalogue reads, without a generation step.
// The test binary lives under build\windows\artifacts, so the root is found
// by walking up to the directory that holds go.work.

using System;
using System.IO;

namespace Malachi.Core.Tests.I18n;

internal static class RepositoryPo
{
    private static readonly Lazy<string> RootPath = new(FindRoot);

    /// <summary>The repository root.</summary>
    public static string Root => RootPath.Value;

    /// <summary>po/ of the repository.</summary>
    public static string Directory => Path.Combine(Root, "po");

    /// <summary>po/cs.po.</summary>
    public static string CsPo => Path.Combine(Directory, "cs.po");

    /// <summary>po/malachi.pot.</summary>
    public static string Pot => Path.Combine(Directory, "malachi.pot");

    /// <summary>po/LINGUAS.</summary>
    public static string Linguas => Path.Combine(Directory, "LINGUAS");

    private static string FindRoot()
    {
        for (var dir = new DirectoryInfo(AppContext.BaseDirectory); dir is not null; dir = dir.Parent)
        {
            if (File.Exists(Path.Combine(dir.FullName, "go.work")))
            {
                return dir.FullName;
            }
        }
        throw new InvalidOperationException("the repository root (the directory with go.work) was not found from " + AppContext.BaseDirectory);
    }
}
