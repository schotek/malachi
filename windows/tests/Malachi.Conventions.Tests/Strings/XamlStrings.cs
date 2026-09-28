// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The XAML half of the strings check (docs/windows-port.md §9), the
// counterpart of what check-strings.py does for Swift and what xgettext
// does for the Blueprints' _() and C_(): every {l:T Msgid='…'} and
// {l:T Msgid='…', Context='…'} (and the element form <l:T Msgid="…" />)
// with the prefix bound to using:Malachi.App.Localization, and every
// literal in a WinUI text sink (Text, Content, Header, Title,
// PlaceholderText, Label, ToolTipService.ToolTip,
// AutomationProperties.Name, …, and the text inside a TextBlock or Run).
// A literal is accepted in a sink when an XML comment right before its
// element, or before an enclosing element, says "Windows-only string" (or
// "strings").

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Xml;
using System.Xml.Linq;

namespace Malachi.Conventions.Tests.Strings;

/// <summary>What the XAML files hand to {l:T} and to WinUI's text sinks.</summary>
internal sealed class XamlStrings
{
    /// <summary>The CLR namespace of the markup extension.</summary>
    public const string LocalizationNamespace = "using:Malachi.App.Localization";

    private static readonly HashSet<string> SinkAttributes = new(StringComparer.Ordinal)
    {
        "Text", "Content", "Header", "Title", "Subtitle", "PlaceholderText", "Label", "Description",
        "PrimaryButtonText", "SecondaryButtonText", "CloseButtonText", "OnContent", "OffContent", "Message",
        "ToolTipService.ToolTip", "AutomationProperties.Name", "AutomationProperties.HelpText",
    };

    private static readonly Lazy<XamlStrings> Scanned = new(() => Scan(Path.Combine(RepositoryTree.Windows, "src")));

    private XamlStrings()
    {
    }

    /// <summary>The XAML of windows/src.</summary>
    public static XamlStrings Sources => Scanned.Value;

    /// <summary>Every {l:T}.</summary>
    public List<L10nCall> Calls { get; } = [];

    /// <summary>Literals in text sinks without the Windows-only mark.</summary>
    public List<string> UnmarkedSinks { get; } = [];

    /// <summary>How many files were read.</summary>
    public int Files { get; private set; }

    /// <summary>
    /// The named arguments of a markup extension's text, <c>{p:T Msgid='a, b', Context=c}</c>
    /// → Msgid, Context; null when <paramref name="text"/> is not the
    /// extension <paramref name="name"/> with one of <paramref name="prefixes"/>.
    /// </summary>
    public static IReadOnlyDictionary<string, string>? ParseExtension(string text, IReadOnlySet<string> prefixes, string name)
    {
        var s = text.Trim();
        if (s.Length < 2 || s[0] != '{' || s[^1] != '}')
        {
            return null;
        }
        s = s[1..^1].Trim();
        var space = s.IndexOfAny([' ', '\t', '\r', '\n']);
        var head = space < 0 ? s : s[..space];
        var colon = head.IndexOf(':', StringComparison.Ordinal);
        if (colon < 0 || !prefixes.Contains(head[..colon]) || head[(colon + 1)..] != name)
        {
            return null;
        }
        var result = new Dictionary<string, string>(StringComparer.Ordinal);
        var rest = space < 0 ? "" : s[space..];
        var i = 0;
        while (i < rest.Length)
        {
            while (i < rest.Length && (char.IsWhiteSpace(rest[i]) || rest[i] == ','))
            {
                i++;
            }
            var eq = rest.IndexOf('=', i);
            if (eq < 0)
            {
                break;
            }
            var key = rest[i..eq].Trim();
            i = eq + 1;
            while (i < rest.Length && char.IsWhiteSpace(rest[i]))
            {
                i++;
            }
            string value;
            if (i < rest.Length && rest[i] is '\'' or '"')
            {
                var quote = rest[i];
                var end = rest.IndexOf(quote, i + 1);
                if (end < 0)
                {
                    end = rest.Length;
                }
                value = rest[(i + 1)..end];
                i = end + 1;
            }
            else
            {
                var end = rest.IndexOf(',', i);
                if (end < 0)
                {
                    end = rest.Length;
                }
                value = rest[i..end].Trim();
                i = end;
            }
            result[key] = value;
        }
        return result;
    }

    /// <summary>What one XAML document hands to {l:T} and to the sinks (the tests of the scanner).</summary>
    public static XamlStrings Parse(string xaml, string name = "test.xaml")
    {
        var result = new XamlStrings();
        result.Add(XDocument.Parse(xaml, LoadOptions.SetLineInfo | LoadOptions.PreserveWhitespace), name);
        return result;
    }

    private static XamlStrings Scan(string root)
    {
        var result = new XamlStrings();
        var files = Directory.EnumerateFiles(root, "*.xaml", SearchOption.AllDirectories)
            .Where(p => !p.Split(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar)
                .Any(part => part.Equals("bin", StringComparison.OrdinalIgnoreCase) || part.Equals("obj", StringComparison.OrdinalIgnoreCase)))
            .Order(StringComparer.Ordinal);
        foreach (var path in files)
        {
            var relative = Path.GetRelativePath(RepositoryTree.Root, path).Replace('\\', '/');
            result.Add(XDocument.Load(path, LoadOptions.SetLineInfo | LoadOptions.PreserveWhitespace), relative);
        }
        return result;
    }

    private void Add(XDocument doc, string relative)
    {
        Files++;
        foreach (var element in doc.Descendants())
        {
            ScanElement(element, relative);
        }
    }

    // The prefixes bound to the localization namespace where the element is.
    private static HashSet<string> PrefixesAt(XElement element)
    {
        var prefixes = new HashSet<string>(StringComparer.Ordinal);
        for (var e = element; e is not null; e = e.Parent)
        {
            foreach (var a in e.Attributes().Where(a => a.IsNamespaceDeclaration && a.Value == LocalizationNamespace))
            {
                prefixes.Add(a.Name.LocalName);
            }
        }
        return prefixes;
    }

    private static int Line(XObject o) => o is IXmlLineInfo info && info.HasLineInfo() ? info.LineNumber : 0;

    // A comment right before the element (only white space between), or
    // before an enclosing element, with the mark.
    private static bool IsMarked(XElement element)
    {
        for (var e = element; e is not null; e = e.Parent)
        {
            for (var node = e.PreviousNode; node is not null; node = node.PreviousNode)
            {
                if (node is XText text && string.IsNullOrWhiteSpace(text.Value))
                {
                    continue;
                }
                if (node is XComment comment && comment.Value.Contains("Windows-only string", StringComparison.OrdinalIgnoreCase))
                {
                    return true;
                }
                break;
            }
        }
        return false;
    }

    private void ScanElement(XElement element, string file)
    {
        var prefixes = PrefixesAt(element);
        // <l:T Msgid="…" Context="…" /> as a property element's value.
        if (element.Name.NamespaceName == LocalizationNamespace && element.Name.LocalName == "T")
        {
            var msgid = (string?)element.Attribute("Msgid");
            var context = (string?)element.Attribute("Context");
            Calls.Add(new L10nCall($"{file}:{Line(element)}", context is null ? 'T' : 'C', context, msgid, null));
            return;
        }
        foreach (var attribute in element.Attributes().Where(a => !a.IsNamespaceDeclaration))
        {
            var value = attribute.Value;
            if (ParseExtension(value, prefixes, "T") is { } args)
            {
                args.TryGetValue("Msgid", out var msgid);
                args.TryGetValue("Context", out var context);
                Calls.Add(new L10nCall($"{file}:{Line(attribute)}", string.IsNullOrEmpty(context) ? 'T' : 'C', string.IsNullOrEmpty(context) ? null : context, msgid, null));
                continue;
            }
            var name = attribute.Name.LocalName;
            if (SinkAttributes.Contains(name) && !value.TrimStart().StartsWith('{') && value.Any(char.IsLetter) && !IsMarked(element))
            {
                UnmarkedSinks.Add($"{file}:{Line(attribute)}: {name}=\"{value}\"");
            }
        }
        if (element.Name.LocalName is "TextBlock" or "Run" or "Paragraph")
        {
            var text = string.Concat(element.Nodes().OfType<XText>().Select(t => t.Value)).Trim();
            if (text.Any(char.IsLetter) && !IsMarked(element))
            {
                UnmarkedSinks.Add($"{file}:{Line(element)}: <{element.Name.LocalName}>{text}");
            }
        }
    }
}
