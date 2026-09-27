// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// docs/windows-port.md §8, §9: the settings facade has the keys, types,
// defaults and ranges of data/io.github.schotek.Malachi.gschema.xml, which
// the GTK UI reads through GSettings and macOS mirrors in Settings.swift. A
// key added to the gschema, or a default changed there, fails here until
// SettingsStore follows; the only key the gschema lacks is the Windows-only
// ctrl-r (macOS's command-r).

using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Linq;
using System.Text.RegularExpressions;
using System.Xml.Linq;
using Malachi.Core;
using Malachi.Core.Settings;
using Xunit;

namespace Malachi.Conventions.Tests;

public sealed partial class GschemaCoverageTests
{
    private static readonly Lazy<Gschema> Parsed = new(Gschema.Load);

    [Fact]
    public void TheSchemaIsTheApp()
    {
        Assert.Equal(AppIdentity.AppId, Parsed.Value.Id);
        // A wrong file would pass vacuously.
        Assert.True(Parsed.Value.Keys.Count >= 20);
    }

    [Fact]
    public void EveryGschemaKeyIsInTheFacadeWithItsTypeDefaultAndRange()
    {
        var problems = new List<string>();
        foreach (var key in Parsed.Value.Keys)
        {
            if (!SettingsStore.TryParseKey(key.Name, out var facadeKey))
            {
                problems.Add($"{key.Name}: not in SettingsStore.Schema");
                continue;
            }
            var info = SettingsStore.Info(facadeKey);
            if (info.Name != key.Name)
            {
                problems.Add($"{key.Name}: the facade spells it {info.Name}");
            }
            if (info.WindowsOnly)
            {
                problems.Add($"{key.Name}: marked Windows-only, but the gschema has it");
            }
            if (info.Type != key.Type)
            {
                problems.Add($"{key.Name}: type {info.Type}, the gschema says {key.Type}");
            }
            if (!key.Choices.SequenceEqual(info.Choices))
            {
                problems.Add($"{key.Name}: nicks [{string.Join(", ", info.Choices)}], the gschema says [{string.Join(", ", key.Choices)}]");
            }
            if (!SameDefault(info.Default, key.Default))
            {
                problems.Add($"{key.Name}: default {Show(info.Default)}, the gschema says {Show(key.Default)}");
            }
            if (info.Minimum != key.Minimum || info.Maximum != key.Maximum)
            {
                problems.Add($"{key.Name}: range {info.Minimum}..{info.Maximum}, the gschema says {key.Minimum}..{key.Maximum}");
            }
        }
        Assert.True(problems.Count == 0, "the settings facade differs from the gschema:\n" + string.Join('\n', problems));
    }

    [Fact]
    public void OnlyWindowsOnlyKeysAreMissingFromTheGschema()
    {
        var names = Parsed.Value.Keys.Select(k => k.Name).ToHashSet(StringComparer.Ordinal);
        var extra = SettingsStore.Schema.Where(k => !names.Contains(k.Name)).ToList();
        Assert.All(extra, k => Assert.True(k.WindowsOnly, k.Name + " is neither in the gschema nor Windows-only"));
        Assert.Equal(["ctrl-r"], extra.Select(k => k.Name));
        var ctrlR = SettingsStore.Info(SettingsKey.CtrlR);
        Assert.Equal(("s", "reply"), (ctrlR.Type, (string)ctrlR.Default));
        Assert.Equal(["reply", "refresh"], ctrlR.Choices);
    }

    private static bool SameDefault(object facade, object gschema) => (facade, gschema) switch
    {
        (IReadOnlyList<string> a, IReadOnlyList<string> b) => a.SequenceEqual(b, StringComparer.Ordinal),
        _ => Equals(facade, gschema),
    };

    private static string Show(object value) =>
        value is IReadOnlyList<string> list ? "[" + string.Join(", ", list) + "]" : Convert.ToString(value, CultureInfo.InvariantCulture) ?? "";

    [GeneratedRegex(@"'((?:[^'\\]|\\.)*)'", RegexOptions.CultureInvariant)]
    private static partial Regex QuotedPattern();

    private sealed record GschemaKey(string Name, string Type, object Default, int? Minimum, int? Maximum, IReadOnlyList<string> Choices);

    private sealed record Gschema(string Id, IReadOnlyList<GschemaKey> Keys)
    {
        public static Gschema Load()
        {
            var path = Path.Combine(RepositoryTree.Root, "data", "io.github.schotek.Malachi.gschema.xml");
            var root = XDocument.Load(path).Root!;
            var enums = root.Elements("enum").ToDictionary(
                e => (string)e.Attribute("id")!,
                e => (IReadOnlyList<string>)[.. e.Elements("value")
                    .OrderBy(v => int.Parse((string)v.Attribute("value")!, CultureInfo.InvariantCulture))
                    .Select(v => (string)v.Attribute("nick")!)]);
            var schema = root.Elements("schema").Single();
            var keys = new List<GschemaKey>();
            foreach (var key in schema.Elements("key"))
            {
                var name = (string)key.Attribute("name")!;
                var enumId = (string?)key.Attribute("enum");
                var type = enumId is null ? (string)key.Attribute("type")! : "s";
                var choices = enumId is null ? [] : enums[enumId];
                var text = ((string)key.Element("default")!).Trim();
                var range = key.Element("range");
                keys.Add(new GschemaKey(
                    name,
                    type,
                    ParseDefault(type, text),
                    range is null ? null : int.Parse((string)range.Attribute("min")!, CultureInfo.InvariantCulture),
                    range is null ? null : int.Parse((string)range.Attribute("max")!, CultureInfo.InvariantCulture),
                    choices));
            }
            return new Gschema((string)schema.Attribute("id")!, keys);
        }

        // The GVariant text of a default: true/false, a number, 'nick', or
        // ['a', 'b'].
        private static object ParseDefault(string type, string text) => type switch
        {
            "b" => text switch
            {
                "true" => true,
                "false" => false,
                _ => throw new FormatException("a boolean default: " + text),
            },
            "i" => int.Parse(text, NumberStyles.AllowLeadingSign, CultureInfo.InvariantCulture),
            "s" => QuotedPattern().Match(text) is { Success: true } m ? m.Groups[1].Value : throw new FormatException("a string default: " + text),
            "as" => (IReadOnlyList<string>)[.. QuotedPattern().Matches(text).Select(m => m.Groups[1].Value)],
            _ => throw new FormatException("a default of type " + type),
        };
    }
}
