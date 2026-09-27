// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The Go contract and the GTK UI read from the checked-out tree at test
// time, so that the C# tables are compared with backend/pkg/api and ui/
// themselves rather than with a copy that could age with them. Swift's
// APICodingTests compare with copies (goMethods, goCodes); those copies are
// ported too.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Linq;
using System.Text.RegularExpressions;

namespace Malachi.Core.Tests.Api;

/// <summary>Go sources of the repository, and the tables in them.</summary>
internal static partial class GoContract
{
    private static readonly Lazy<string> RootPath = new(FindRoot);

    /// <summary>
    /// The repository root: the first directory above the test binary (or,
    /// for a build directory moved elsewhere, above the working directory)
    /// that holds go.work.
    /// </summary>
    public static string Root => RootPath.Value;

    /// <summary>The text of a file of the repository, by its path from the root.</summary>
    public static string Source(params string[] path) => File.ReadAllText(Path.Combine([Root, .. path]));

    /// <summary>The text of a file of backend/pkg/api.</summary>
    public static string ApiSource(string file) => Source("backend", "pkg", "api", file);

    /// <summary>
    /// The string constants of a Go file: every <c>[const] name [Type] = "value"</c>,
    /// as name → value.
    /// </summary>
    public static IReadOnlyDictionary<string, string> StringConstants(string source)
    {
        var constants = new Dictionary<string, string>(StringComparer.Ordinal);
        foreach (Match m in StringConstant().Matches(source))
        {
            constants[m.Groups["name"].Value] = m.Groups["value"].Value;
        }
        return constants;
    }

    /// <summary>
    /// The values of the Go string type <paramref name="goType"/>: every
    /// <c>Name goType = "value"</c>, in the order of the source.
    /// </summary>
    public static IReadOnlyList<string> TypedStringConstants(string source, string goType) =>
        [.. StringConstant().Matches(source).Where(m => m.Groups["type"].Value == goType).Select(m => m.Groups["value"].Value)];

    /// <summary>
    /// The identifiers listed in the Go slice <c>var name = []string{…}</c>,
    /// in order.
    /// </summary>
    public static IReadOnlyList<string> SliceIdentifiers(string source, string name)
    {
        var start = source.IndexOf($"var {name} = []string{{", StringComparison.Ordinal);
        if (start < 0)
        {
            throw new InvalidOperationException($"{name} not found");
        }
        var open = source.IndexOf('{', start);
        var close = source.IndexOf('}', open);
        return [.. Identifier().Matches(source[(open + 1)..close]).Select(m => m.Value)];
    }

    /// <summary>
    /// The error codes of errors.go in the order of their declaration, with
    /// the names of codeNames.
    /// </summary>
    public static IReadOnlyList<(int Code, string Name)> ErrorCodes()
    {
        var source = ApiSource("errors.go");
        var names = CodeName().Matches(source).ToDictionary(m => m.Groups["const"].Value, m => m.Groups["name"].Value, StringComparer.Ordinal);
        return [.. CodeConstant().Matches(source).Select(m => (int.Parse(m.Groups["value"].Value, CultureInfo.InvariantCulture), names[m.Groups["const"].Value]))];
    }

    /// <summary>
    /// The integer constants of a Go file: every <c>[const] name = 50</c> or
    /// <c>name = 25 &lt;&lt; 20</c>, as name → value.
    /// </summary>
    public static IReadOnlyDictionary<string, long> IntConstants(string source)
    {
        var constants = new Dictionary<string, long>(StringComparer.Ordinal);
        foreach (Match m in IntConstant().Matches(source))
        {
            var value = long.Parse(m.Groups["value"].Value, CultureInfo.InvariantCulture);
            if (m.Groups["shift"].Success)
            {
                value <<= int.Parse(m.Groups["shift"].Value, CultureInfo.InvariantCulture);
            }
            constants[m.Groups["name"].Value] = value;
        }
        return constants;
    }

    /// <summary>The duration constant <c>name = N * time.Second</c> of a Go file.</summary>
    public static TimeSpan Seconds(string source, string name)
    {
        var m = SecondsConstant().Matches(source).FirstOrDefault(m => m.Groups["name"].Value == name)
            ?? throw new InvalidOperationException($"{name} = N * time.Second not found");
        return TimeSpan.FromSeconds(int.Parse(m.Groups["value"].Value, CultureInfo.InvariantCulture));
    }

    /// <summary>The first <c>N*time.Second</c> in the body of the Go function <paramref name="function"/>.</summary>
    public static TimeSpan SecondsIn(string source, string function)
    {
        var start = source.IndexOf($"{function}() {{", StringComparison.Ordinal);
        if (start < 0)
        {
            throw new InvalidOperationException($"func {function} not found");
        }
        var m = InlineSeconds().Match(source, start);
        return TimeSpan.FromSeconds(int.Parse(m.Groups["value"].Value, CultureInfo.InvariantCulture));
    }

    /// <summary>
    /// The structs of Go files: name → their fields as (JSON name, Go type),
    /// an embedded struct as (null, its name). Fields tagged <c>json:"-"</c>
    /// are left out.
    /// </summary>
    public static IReadOnlyDictionary<string, IReadOnlyList<(string? Json, string GoType)>> Structs(params string[] sources)
    {
        var structs = new Dictionary<string, IReadOnlyList<(string?, string)>>(StringComparer.Ordinal);
        foreach (var source in sources)
        {
            foreach (Match s in Struct().Matches(source))
            {
                var fields = new List<(string?, string)>();
                foreach (Match f in StructField().Matches(s.Groups["body"].Value))
                {
                    if (f.Groups["embedded"].Success)
                    {
                        fields.Add((null, f.Groups["embedded"].Value));
                    }
                    else if (f.Groups["json"].Value != "-")
                    {
                        fields.Add((f.Groups["json"].Value, f.Groups["type"].Value));
                    }
                }
                structs[s.Groups["name"].Value] = fields;
            }
        }
        return structs;
    }

    private static string FindRoot()
    {
        foreach (var start in new[] { AppContext.BaseDirectory, Environment.CurrentDirectory })
        {
            for (var dir = new DirectoryInfo(start); dir is not null; dir = dir.Parent)
            {
                if (File.Exists(Path.Combine(dir.FullName, "go.work")))
                {
                    return dir.FullName;
                }
            }
        }
        throw new InvalidOperationException(
            $"the repository root (the directory with go.work) was not found above {AppContext.BaseDirectory} or {Environment.CurrentDirectory}");
    }

    [GeneratedRegex(@"^\s*(?:const\s+)?(?<name>[A-Za-z]\w*)\s+(?:(?<type>[A-Z]\w*)\s+)?=\s*""(?<value>[^""]*)""", RegexOptions.Multiline)]
    private static partial Regex StringConstant();

    [GeneratedRegex(@"\b[A-Z]\w*\b")]
    private static partial Regex Identifier();

    [GeneratedRegex(@"^\s*(?<const>Code\w+)\s+ErrorCode\s*=\s*(?<value>-?\d+)", RegexOptions.Multiline)]
    private static partial Regex CodeConstant();

    [GeneratedRegex(@"^\s*(?<const>Code\w+):\s*""(?<name>[^""]+)"",", RegexOptions.Multiline)]
    private static partial Regex CodeName();

    [GeneratedRegex(@"^\s*(?:const\s+)?(?<name>[A-Za-z]\w*)\s*=\s*(?<value>\d+)(?:\s*<<\s*(?<shift>\d+))?\s*(?://.*)?$", RegexOptions.Multiline)]
    private static partial Regex IntConstant();

    [GeneratedRegex(@"^\s*(?:const\s+)?(?<name>[A-Za-z]\w*)\s*=\s*(?<value>\d+)\s*\*\s*time\.Second", RegexOptions.Multiline)]
    private static partial Regex SecondsConstant();

    [GeneratedRegex(@"(?<value>\d+)\s*\*\s*time\.Second")]
    private static partial Regex InlineSeconds();

    [GeneratedRegex(@"^type\s+(?<name>\w+)\s+struct\s*\{(?:\}|(?<body>.*?)^\})", RegexOptions.Multiline | RegexOptions.Singleline)]
    private static partial Regex Struct();

    [GeneratedRegex(@"^\s*(?:(?<field>\w+)\s+(?<type>[\w.*\[\]]+)\s+`json:""(?<json>[^"",]*)[^`]*`|(?<embedded>[A-Z]\w*)\s*$)", RegexOptions.Multiline)]
    private static partial Regex StructField();
}
