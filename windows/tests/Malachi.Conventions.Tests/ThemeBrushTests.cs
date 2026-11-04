// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The colour scheme sets RequestedTheme on each window's root
// (Malachi.App/Shell/WindowTracker.cs), but a brush taken from
// Application.Current.Resources in code has the theme of Windows: in a dark
// window a card kept its light fill under white text. Code that builds
// elements assigns a style whose setters are theme resources instead
// (Resources/Surfaces.xaml, Resources/Typography.xaml, or a view's own
// resources), so the brush is resolved in the element's theme and follows
// it. This test keeps the brush lookups out of the app's C#.

using System;
using System.Collections.Generic;
using System.IO;
using System.Text.RegularExpressions;
using Xunit;

namespace Malachi.Conventions.Tests;

public sealed partial class ThemeBrushTests
{
    // A cast to a brush of a lookup in the application's resources, or a
    // lookup of a key named ...Brush.
    [GeneratedRegex(
        @"\(\s*(?:[\w.]+\.)?\w*Brush\s*\)\s*Application\.Current\??\.Resources"
        + @"|Application\.Current\??\.Resources(?:\[\s*""[^""]*Brush""|\.TryGetValue\(\s*""[^""]*Brush"")",
        RegexOptions.CultureInvariant)]
    private static partial Regex BrushLookup();

    [Fact]
    public void NoBrushIsTakenFromTheApplicationsResources()
    {
        var app = Path.Combine(RepositoryTree.Windows, "src", "Malachi.App");
        var problems = new List<string>();
        foreach (var path in Directory.EnumerateFiles(app, "*.cs", SearchOption.AllDirectories))
        {
            var relative = Path.GetRelativePath(RepositoryTree.Root, path).Replace('\\', '/');
            if (relative.Contains("/bin/", StringComparison.Ordinal) || relative.Contains("/obj/", StringComparison.Ordinal))
            {
                continue;
            }
            var lines = File.ReadAllLines(path);
            for (var i = 0; i < lines.Length; i++)
            {
                if (BrushLookup().IsMatch(lines[i]))
                {
                    problems.Add(relative + ":" + (i + 1) + ": " + lines[i].Trim());
                }
            }
        }
        Assert.True(
            problems.Count == 0,
            "brushes taken from Application.Current.Resources (use a style with ThemeResource setters):\n"
            + string.Join('\n', problems));
    }
}
