// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The C# half of the strings check (docs/windows-port.md §9), the
// counterpart of the Swift lexer of macos/scripts/check-strings.py, with
// the C# parser instead of a lexer: every call of L10n.T, N and C with its
// literal msgids (a msgid built at run time cannot be checked and is
// reported as such), every literal handed to L10n.T as a format argument
// (a literal formatted into a translated sentence is untranslated text),
// every string literal of the sources (for the coverage of the template),
// and the literals handed to a WinUI text sink (Text =, Content =,
// Title =, ToolTipService.SetToolTip, ...) without going through L10n. A
// literal is accepted in a sink when it is marked "Windows-only string" in
// a comment on its line or right above its statement, or when a comment
// "Windows-only strings" above it in the same block marks the rest of that
// block.

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CSharp;
using Microsoft.CodeAnalysis.CSharp.Syntax;

namespace Malachi.Conventions.Tests.Strings;

/// <summary>What the C# sources hand to L10n and to WinUI's text sinks.</summary>
internal sealed class CSharpStrings
{
    // Properties whose string is shown to the user (WinUI and the app's own).
    private static readonly HashSet<string> SinkProperties = new(StringComparer.Ordinal)
    {
        "Text", "Content", "Header", "Title", "Subtitle", "PlaceholderText", "Label", "Description",
        "PrimaryButtonText", "SecondaryButtonText", "CloseButtonText", "OnContent", "OffContent",
        "Message", "ToolTip",
    };

    // Static setters whose second argument is shown to the user.
    private static readonly HashSet<string> SinkSetters = new(StringComparer.Ordinal)
    {
        "SetToolTip", "SetName", "SetHelpText", "SetText",
    };

    private static readonly Lazy<CSharpStrings> Scanned = new(() => Scan(Path.Combine(RepositoryTree.Windows, "src")));

    private CSharpStrings()
    {
    }

    /// <summary>The sources of windows/src.</summary>
    public static CSharpStrings Sources => Scanned.Value;

    /// <summary>Every call of L10n.T, N and C.</summary>
    public List<L10nCall> Calls { get; } = [];

    /// <summary>Every string literal's value, for the coverage of the template.</summary>
    public HashSet<string> Literals { get; } = new(StringComparer.Ordinal);

    /// <summary>Literals in text sinks without L10n and without the Windows-only mark.</summary>
    public List<string> UnmarkedSinks { get; } = [];

    /// <summary>Literal format arguments of L10n.T without the Windows-only mark.</summary>
    public List<string> LiteralArguments { get; } = [];

    /// <summary>How many files were read.</summary>
    public int Files { get; private set; }

    /// <summary>What one piece of C# hands to L10n and to the sinks (the tests of the scanner).</summary>
    public static CSharpStrings Parse(string code, string name = "test.cs")
    {
        var result = new CSharpStrings();
        result.Add(code, name);
        return result;
    }

    private static CSharpStrings Scan(string root)
    {
        var result = new CSharpStrings();
        foreach (var path in SourceFiles(root))
        {
            result.Add(File.ReadAllText(path), Path.GetRelativePath(RepositoryTree.Root, path).Replace('\\', '/'));
        }
        return result;
    }

    private void Add(string code, string relative)
    {
        Files++;
        var tree = CSharpSyntaxTree.ParseText(code, new CSharpParseOptions(LanguageVersion.Preview), relative);
        var syntax = tree.GetRoot();
        foreach (var token in syntax.DescendantTokens())
        {
            if (token.IsKind(SyntaxKind.StringLiteralToken) || token.IsKind(SyntaxKind.SingleLineRawStringLiteralToken)
                || token.IsKind(SyntaxKind.MultiLineRawStringLiteralToken) || token.IsKind(SyntaxKind.Utf8StringLiteralToken))
            {
                Literals.Add(token.ValueText);
            }
        }
        foreach (var node in syntax.DescendantNodes())
        {
            switch (node)
            {
                case InvocationExpressionSyntax call when L10nKind(call) is { } kind:
                    AddCall(call, kind, relative);
                    break;
                case AssignmentExpressionSyntax assignment when IsSinkTarget(assignment.Left) && Literal(assignment.Right) is { } text:
                    AddSink(assignment, text, relative);
                    break;
                case InvocationExpressionSyntax setter when IsSinkSetter(setter) && Literal(setter.ArgumentList.Arguments[1].Expression) is { } text:
                    AddSink(setter, text, relative);
                    break;
            }
        }
    }

    private static IEnumerable<string> SourceFiles(string root) =>
        Directory.EnumerateFiles(root, "*.cs", SearchOption.AllDirectories)
            .Where(p => !p.Split(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar)
                .Any(part => part.Equals("bin", StringComparison.OrdinalIgnoreCase) || part.Equals("obj", StringComparison.OrdinalIgnoreCase)))
            .Order(StringComparer.Ordinal);

    // L10n.T / L10n.N / L10n.C, also qualified (Malachi.Core.I18n.L10n.T).
    private static char? L10nKind(InvocationExpressionSyntax call)
    {
        if (call.Expression is not MemberAccessExpressionSyntax member
            || member.Expression is not (IdentifierNameSyntax { Identifier.ValueText: "L10n" } or MemberAccessExpressionSyntax { Name.Identifier.ValueText: "L10n" }))
        {
            return null;
        }
        return member.Name.Identifier.ValueText switch
        {
            "T" => 'T',
            "N" => 'N',
            "C" => 'C',
            _ => null,
        };
    }

    private static bool IsSinkTarget(ExpressionSyntax left) => left switch
    {
        IdentifierNameSyntax id => SinkProperties.Contains(id.Identifier.ValueText),
        MemberAccessExpressionSyntax member => SinkProperties.Contains(member.Name.Identifier.ValueText),
        _ => false,
    };

    private static bool IsSinkSetter(InvocationExpressionSyntax call) =>
        call.Expression is MemberAccessExpressionSyntax { Name.Identifier.ValueText: var name } member
        && SinkSetters.Contains(name)
        && member.Expression is IdentifierNameSyntax { Identifier.ValueText: "ToolTipService" or "AutomationProperties" or "MnemonicLabel" }
        && call.ArgumentList.Arguments.Count == 2;

    // The value of a string literal with a letter in it; null for anything
    // else (a call, a variable, an interpolation, a key-like word is still
    // a literal: "Close").
    private static string? Literal(ExpressionSyntax expression) =>
        expression is LiteralExpressionSyntax literal && literal.IsKind(SyntaxKind.StringLiteralExpression)
            && literal.Token.ValueText.Any(char.IsLetter)
            ? literal.Token.ValueText
            : null;

    // A msgid argument's value when it is a literal; null when it is built
    // at run time and cannot be checked.
    private static string? LiteralOrNull(ArgumentSyntax? argument) =>
        argument?.Expression is LiteralExpressionSyntax literal && literal.IsKind(SyntaxKind.StringLiteralExpression)
            ? literal.Token.ValueText
            : null;

    private static bool IsMarked(SyntaxNode node)
    {
        // The line itself (a trailing comment) and the lines of the
        // statement, member or initializer entry it belongs to.
        for (var n = node; n is not null; n = n.Parent)
        {
            if (HasMark(n.GetLeadingTrivia(), plural: false) || HasMark(n.GetTrailingTrivia(), plural: false)
                || HasMark(n.GetLastToken().GetNextToken().LeadingTrivia, plural: false, sameLineOnly: true))
            {
                return true;
            }
            if (n is StatementSyntax or MemberDeclarationSyntax)
            {
                break;
            }
            if (n is AssignmentExpressionSyntax { Parent: InitializerExpressionSyntax })
            {
                break;
            }
        }
        // "Windows-only strings" earlier in an enclosing block or type.
        for (var n = node.Parent; n is not null; n = n.Parent)
        {
            if (n is BlockSyntax or TypeDeclarationSyntax or InitializerExpressionSyntax)
            {
                var start = node.SpanStart;
                foreach (var trivia in n.DescendantTrivia())
                {
                    if (trivia.SpanStart < start && IsComment(trivia) && trivia.ToString().Contains("Windows-only strings", StringComparison.OrdinalIgnoreCase))
                    {
                        return true;
                    }
                }
            }
        }
        return false;
    }

    private static bool HasMark(SyntaxTriviaList trivia, bool plural, bool sameLineOnly = false)
    {
        foreach (var t in trivia)
        {
            if (sameLineOnly && t.IsKind(SyntaxKind.EndOfLineTrivia))
            {
                return false;
            }
            if (IsComment(t) && t.ToString().Contains(plural ? "Windows-only strings" : "Windows-only string", StringComparison.OrdinalIgnoreCase))
            {
                return true;
            }
        }
        return false;
    }

    private static bool IsComment(SyntaxTrivia t) =>
        t.IsKind(SyntaxKind.SingleLineCommentTrivia) || t.IsKind(SyntaxKind.MultiLineCommentTrivia);

    private static string Where(SyntaxNode node, string file) =>
        $"{file}:{node.GetLocation().GetLineSpan().StartLinePosition.Line + 1}";

    private void AddCall(InvocationExpressionSyntax call, char kind, string file)
    {
        var args = call.ArgumentList.Arguments;
        var where = Where(call, file);
        switch (kind)
        {
            case 'T':
                Calls.Add(new L10nCall(where, 'T', null, LiteralOrNull(args.ElementAtOrDefault(0)), null, IsMarked(call)));
                foreach (var argument in args.Skip(1))
                {
                    if (Literal(argument.Expression) is { } text && !IsMarked(argument))
                    {
                        LiteralArguments.Add($"{where}: \"{text}\"");
                    }
                }
                break;
            case 'N':
                Calls.Add(new L10nCall(where, 'N', null, LiteralOrNull(args.ElementAtOrDefault(0)), LiteralOrNull(args.ElementAtOrDefault(1)), IsMarked(call)));
                break;
            case 'C':
                Calls.Add(new L10nCall(where, 'C', LiteralOrNull(args.ElementAtOrDefault(0)), LiteralOrNull(args.ElementAtOrDefault(1)), null, IsMarked(call)));
                break;
        }
    }

    private void AddSink(SyntaxNode node, string text, string file)
    {
        if (!IsMarked(node))
        {
            UnmarkedSinks.Add($"{Where(node, file)}: \"{text}\"");
        }
    }
}
