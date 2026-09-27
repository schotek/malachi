// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// CLAUDE.md: every source file starts with the SPDX header of the part it
// lies in, and windows/ is GPL-3.0-or-later (LICENSING.md). The compiler
// enforces it for C# (IDE0073); this test checks every file type of the
// Windows client in its own comment syntax. The two lines come from the
// file_header_template of windows/.editorconfig, the one place to change
// them. JSON (no comments) and Markdown carry no header, as elsewhere in
// the repository.

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text;
using Xunit;

namespace Malachi.Conventions.Tests;

public sealed class SpdxHeaderTests
{
    private const string License = "SPDX-License-Identifier: GPL-3.0-or-later";

    // The build output, the IDE's and the test adapters' state: not sources.
    private static readonly HashSet<string> SkippedDirectories = new(StringComparer.OrdinalIgnoreCase)
    {
        "bin", "obj", "TestResults", "AppPackages", "BundleArtifacts", "node_modules",
    };

    private static readonly HashSet<string> SlashComment = new(StringComparer.OrdinalIgnoreCase)
    {
        ".cs", ".js",
    };

    private static readonly HashSet<string> HashComment = new(StringComparer.OrdinalIgnoreCase)
    {
        ".ps1",
    };

    private static readonly HashSet<string> XmlComment = new(StringComparer.OrdinalIgnoreCase)
    {
        ".xaml", ".csproj", ".props", ".targets", ".slnx", ".manifest", ".xml", ".html",
        ".config", ".resw", ".appxmanifest",
    };

    private static readonly HashSet<string> BlockComment = new(StringComparer.OrdinalIgnoreCase)
    {
        ".css",
    };

    [Fact]
    public void TheTemplateIsTheHeaderOfTheGplPart()
    {
        var (copyright, license) = Template();
        Assert.StartsWith("SPDX-FileCopyrightText: ", copyright, StringComparison.Ordinal);
        Assert.Equal(License, license);
    }

    [Fact]
    public void EveryFileStartsWithTheHeader()
    {
        var (copyright, license) = Template();
        var checkedFiles = new List<string>();
        var problems = new List<string>();
        foreach (var path in SourceFiles(RepositoryTree.Windows))
        {
            var extension = Path.GetExtension(path);
            if (!IsChecked(extension))
            {
                continue;
            }
            var relative = Path.GetRelativePath(RepositoryTree.Root, path).Replace('\\', '/');
            checkedFiles.Add(relative);
            var problem = Check(extension, ReadLines(path), copyright, license);
            if (problem is not null)
            {
                problems.Add(relative + ": " + problem);
            }
        }

        // A wrong root would pass vacuously.
        Assert.Contains("windows/Malachi.slnx", checkedFiles);
        Assert.Contains("windows/build.ps1", checkedFiles);
        Assert.True(problems.Count == 0, "files without the SPDX header:\n" + string.Join('\n', problems));
    }

    private static bool IsChecked(string extension) =>
        SlashComment.Contains(extension) || HashComment.Contains(extension)
        || XmlComment.Contains(extension) || BlockComment.Contains(extension);

    // Null when the file starts as its type requires.
    private static string? Check(string extension, string[] lines, string copyright, string license)
    {
        if (SlashComment.Contains(extension))
        {
            return Lines(lines, 0, "// " + copyright, "// " + license)
                ? null
                : $"lines 1-2 must be \"// {copyright}\" and \"// {license}\"";
        }
        if (HashComment.Contains(extension))
        {
            return Lines(lines, 0, "# " + copyright, "# " + license)
                ? null
                : $"lines 1-2 must be \"# {copyright}\" and \"# {license}\"";
        }
        if (XmlComment.Contains(extension))
        {
            // After the XML declaration or the doctype, if there is one.
            var start = lines.Length > 0
                && (lines[0].StartsWith("<?xml", StringComparison.Ordinal)
                    || lines[0].StartsWith("<!DOCTYPE", StringComparison.OrdinalIgnoreCase))
                ? 1
                : 0;
            return Lines(lines, start, "<!--", copyright, license) && Closes(lines, start + 3, "-->")
                ? null
                : $"must open with a comment \"<!--\", \"{copyright}\", \"{license}\", …, \"-->\""
                    + " (after the XML declaration, if any)";
        }
        if (BlockComment.Contains(extension))
        {
            return Lines(lines, 0, "/*", copyright, license) && Closes(lines, 3, "*/")
                ? null
                : $"must open with a comment \"/*\", \"{copyright}\", \"{license}\", …, \"*/\"";
        }
        return null;
    }

    private static bool Lines(string[] lines, int start, params string[] expected) =>
        lines.Length >= start + expected.Length
        && expected.Select((line, i) => lines[start + i] == line).All(same => same);

    private static bool Closes(string[] lines, int from, string end) =>
        lines.Skip(from).Any(line => line.Contains(end, StringComparison.Ordinal));

    private static string[] ReadLines(string path)
    {
        var text = File.ReadAllText(path, new UTF8Encoding(encoderShouldEmitUTF8Identifier: false));
        return text.TrimStart('﻿').Split('\n').Select(line => line.TrimEnd('\r')).ToArray();
    }

    // The two lines of file_header_template in windows/.editorconfig.
    private static (string Copyright, string License) Template()
    {
        var editorconfig = Path.Combine(RepositoryTree.Windows, ".editorconfig");
        var setting = File.ReadAllLines(editorconfig)
            .Select(line => line.Trim())
            .FirstOrDefault(line => line.StartsWith("file_header_template", StringComparison.Ordinal));
        Assert.NotNull(setting);
        var value = setting[(setting.IndexOf('=', StringComparison.Ordinal) + 1)..].Trim();
        var lines = value.Split("\\n");
        Assert.Equal(2, lines.Length);
        return (lines[0], lines[1]);
    }

    private static IEnumerable<string> SourceFiles(string directory)
    {
        foreach (var file in Directory.EnumerateFiles(directory))
        {
            yield return file;
        }
        foreach (var sub in Directory.EnumerateDirectories(directory))
        {
            var name = Path.GetFileName(sub);
            if (name.StartsWith('.') || SkippedDirectories.Contains(name))
            {
                continue;
            }
            foreach (var file in SourceFiles(sub))
            {
                yield return file;
            }
        }
    }
}
