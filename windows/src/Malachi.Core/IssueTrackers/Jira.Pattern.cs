// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Jira/JiraPattern.swift; GTK:
// ui/internal/jira/settings.go (PatternError), which asks Go's
// regexp.Compile.
//
// Whether a pattern of the "Hidden Lines" list is a regular expression of
// the daemon, and if not, why. The daemon's expressions are RE2 (Go's
// regexp); .NET's Regex is not (it takes look-around and back-references,
// which RE2 refuses, and refuses some of what RE2 takes, NonBacktracking
// included), so this follows the parser of Go's regexp/syntax (parse.go of
// Go 1.26, flags syntax.Perl) as far as it decides whether a pattern is
// valid, and answers with the same reasons in the same words, as the Swift
// port does. Nothing is matched here; the pattern is only read.
//
// The daemon checks every pattern again when the account is saved, so this
// is immediate feedback, as the other checks of the page. Where this
// reading is more lenient than regexp/syntax, the daemon's answer is what
// the user gets on Save: a Unicode class is looked up by its name without
// regard to case, spaces, hyphens and underscores, in the tables of
// Unicode 15 with the long names of the categories; and regexp/syntax
// refuses an expression that is too large or nests too deep by what it
// happened to allocate while parsing, where here the expression itself is
// measured. Windows: the parser answers through bool results and a reason
// instead of Swift's throws, and a lone surrogate is Go's invalid UTF-8,
// which a Swift string cannot hold.

using System;
using System.Buffers;
using System.Collections.Frozen;
using System.Collections.Generic;
using System.Linq;
using System.Text;

namespace Malachi.Core.IssueTrackers;

public static partial class Jira
{
    /// <summary>
    /// jira.PatternError: why <paramref name="pattern"/> is not a regular
    /// expression of the daemon (RE2), the reason in the words of Go's
    /// regexp/syntax ("missing closing )"), technical English; "" for a
    /// valid pattern.
    /// </summary>
    public static string PatternError(string pattern)
    {
        ArgumentNullException.ThrowIfNull(pattern);
        for (var k = 0; k < pattern.Length;)
        {
            if (Rune.DecodeFromUtf16(pattern.AsSpan(k), out _, out var used) != OperationStatus.Done)
            {
                return PatternProblem.InvalidUtf8;
            }
            k += used;
        }
        return new PatternParser(pattern).Check() ?? "";
    }

    /// <summary>syntax.ErrorCode: why a pattern was refused, in the words of regexp/syntax.</summary>
    private static class PatternProblem
    {
        public const string InternalError = "regexp/syntax: internal error";
        public const string InvalidCharRange = "invalid character class range";
        public const string InvalidEscape = "invalid escape sequence";
        public const string InvalidNamedCapture = "invalid named capture";
        public const string InvalidPerlOp = "invalid or unsupported Perl syntax";
        public const string InvalidRepeatOp = "invalid nested repetition operator";
        public const string InvalidRepeatSize = "invalid repeat count";
        public const string InvalidUtf8 = "invalid UTF-8";
        public const string MissingBracket = "missing closing ]";
        public const string MissingParen = "missing closing )";
        public const string MissingRepeatArgument = "missing argument to repetition operator";
        public const string TrailingBackslash = "trailing backslash at end of expression";
        public const string UnexpectedParen = "unexpected )";
        public const string NestingDepth = "expression nests too deeply";
        public const string Large = "expression too large";
    }

    /// <summary>
    /// What the parser keeps of a parsed piece: enough to tell what a
    /// repetition repeats, how large the compiled expression would be and
    /// how deep it nests.
    /// </summary>
    private sealed class PatternNode
    {
        public PatternNode(NodeOp op, IReadOnlyList<PatternNode>? subs = null, int min = 0, int max = 0, bool capturing = false)
        {
            Op = op;
            Subs = subs ?? [];
            Min = min;
            Max = max;
            Capturing = capturing;
            var sum = Subs.Sum(n => n.Size);
            var size = op switch
            {
                NodeOp.Capture or NodeOp.Star => 2 + sum,
                NodeOp.Plus or NodeOp.Quest => 1 + sum,
                NodeOp.Concat => sum,
                NodeOp.Alternate => sum + Math.Max(Subs.Count - 1, 0),
                NodeOp.Repeat when max == -1 => min == 0 ? 2 + sum : 1 + (min * sum),
                NodeOp.Repeat => (max * sum) + (max - min),
                _ => 1,
            };
            Size = Math.Max(1, size);
            Height = 1 + (Subs.Count == 0 ? 0 : Subs.Max(n => n.Height));
        }

        public enum NodeOp
        {
            // One character: a literal, a class, "." (parse.go isCharClass).
            Single,

            // An anchor, a word boundary, the empty match.
            Other,
            Capture,
            Star,
            Plus,
            Quest,
            Repeat,
            Concat,
            Alternate,

            // The pseudo-operators of the stack: "(" and "|".
            LeftParen,
            VerticalBar,
        }

        public NodeOp Op { get; }

        public IReadOnlyList<PatternNode> Subs { get; }

        // Of Repeat: the bounds, Max -1 for none.
        public int Min { get; }

        public int Max { get; }

        // Of LeftParen: the group captures.
        public bool Capturing { get; }

        // parse.go calcSize, calcHeight.
        public int Size { get; }

        public int Height { get; }

        public bool IsPseudo => Op is NodeOp.LeftParen or NodeOp.VerticalBar;
    }

    /// <summary>parse.go parser, reading the pattern's UTF-8 as Go does.</summary>
    private sealed class PatternParser(string pattern)
    {
        // parse.go maxSize, maxHeight and the most a repetition repeats.
        private const int MaxSize = (128 << 20) / 40;
        private const int MaxHeight = 1000;
        private const int MaxRepeat = 1000;

        private readonly byte[] s = Encoding.UTF8.GetBytes(pattern);
        private readonly List<PatternNode> stack = [];

        // Where the rest of the pattern starts (Go's t).
        private int i;

        // Why the pattern was refused, once it was.
        private string? problem;

        // syntax.Parse, for its error alone.
        public string? Check() => Parse() ? null : problem ?? PatternProblem.InternalError;

        private bool Fail(string reason)
        {
            problem = reason;
            return false;
        }

        private bool Parse()
        {
            var lastRepeat = false;
            while (i < s.Length)
            {
                var repeated = false;
                switch (s[i])
                {
                    case (byte)'(':
                        if (i + 1 < s.Length && s[i + 1] == (byte)'?')
                        {
                            // Flag changes and non-capturing groups.
                            if (!ParsePerlFlags())
                            {
                                return false;
                            }
                        }
                        else
                        {
                            if (!Push(new PatternNode(PatternNode.NodeOp.LeftParen, capturing: true)))
                            {
                                return false;
                            }
                            i++;
                        }
                        break;
                    case (byte)'|':
                        if (!ParseVerticalBar())
                        {
                            return false;
                        }
                        i++;
                        break;
                    case (byte)')':
                        if (!ParseRightParen())
                        {
                            return false;
                        }
                        i++;
                        break;
                    case (byte)'^' or (byte)'$':
                        if (!Push(new PatternNode(PatternNode.NodeOp.Other)))
                        {
                            return false;
                        }
                        i++;
                        break;
                    case (byte)'.':
                        if (!Push(new PatternNode(PatternNode.NodeOp.Single)))
                        {
                            return false;
                        }
                        i++;
                        break;
                    case (byte)'[':
                        if (!ParseClass())
                        {
                            return false;
                        }
                        break;
                    case (byte)'*' or (byte)'+' or (byte)'?':
                        var op = s[i] switch
                        {
                            (byte)'*' => PatternNode.NodeOp.Star,
                            (byte)'+' => PatternNode.NodeOp.Plus,
                            _ => PatternNode.NodeOp.Quest,
                        };
                        i++;
                        if (!RepeatTop(op, 0, 0, lastRepeat))
                        {
                            return false;
                        }
                        repeated = true;
                        break;
                    case (byte)'{':
                        if (ParseRepeat(i) is not { } r)
                        {
                            // A repeat that cannot be parsed: "{" is a literal.
                            if (!Push(new PatternNode(PatternNode.NodeOp.Single)))
                            {
                                return false;
                            }
                            i++;
                            break;
                        }
                        if (r.Min < 0 || r.Min > MaxRepeat || r.Max > MaxRepeat || (r.Max >= 0 && r.Min > r.Max))
                        {
                            return Fail(PatternProblem.InvalidRepeatSize);
                        }
                        i = r.Next;
                        if (!RepeatTop(PatternNode.NodeOp.Repeat, r.Min, r.Max, lastRepeat))
                        {
                            return false;
                        }
                        repeated = true;
                        break;
                    case (byte)'\\':
                        if (!ParseBackslash())
                        {
                            return false;
                        }
                        break;
                    default:
                        i += RuneAt(i).Size;
                        if (!Push(new PatternNode(PatternNode.NodeOp.Single)))
                        {
                            return false;
                        }
                        break;
                }
                lastRepeat = repeated;
            }

            if (!Concat())
            {
                return false;
            }
            if (SwapVerticalBar())
            {
                stack.RemoveAt(stack.Count - 1);
            }
            if (!Alternate())
            {
                return false;
            }
            return stack.Count == 1 || Fail(PatternProblem.MissingParen);
        }

        // Runes

        // utf8.DecodeRuneInString at `at`; past the end the replacement
        // character of size 0, as Go decodes the empty string. The bytes are
        // a string's, so they are valid UTF-8.
        private (int Value, int Size) RuneAt(int at)
        {
            if (at >= s.Length)
            {
                return (0xFFFD, 0);
            }
            var b = s[at];
            int size;
            int value;
            switch (b)
            {
                case < 0x80:
                    return (b, 1);
                case < 0xC0:
                    return (0xFFFD, 1);
                case < 0xE0:
                    size = 2;
                    value = b & 0x1F;
                    break;
                case < 0xF0:
                    size = 3;
                    value = b & 0x0F;
                    break;
                case < 0xF8:
                    size = 4;
                    value = b & 0x07;
                    break;
                default:
                    return (0xFFFD, 1);
            }
            if (at + size > s.Length)
            {
                return (0xFFFD, 1);
            }
            for (var k = 1; k < size; k++)
            {
                value = (value << 6) | (s[at + k] & 0x3F);
            }
            return (value, size);
        }

        // strings.Index of needle in the pattern from `from` on; -1 when absent.
        private int IndexOf(ReadOnlySpan<byte> needle, int from)
        {
            if (needle.IsEmpty || from > s.Length - needle.Length)
            {
                return -1;
            }
            var k = s.AsSpan(from).IndexOf(needle);
            return k < 0 ? -1 : from + k;
        }

        private static bool IsAlnum(int c) => c is (>= 0x30 and <= 0x39) or (>= 0x41 and <= 0x5A) or (>= 0x61 and <= 0x7A);

        private static bool IsOctal(byte b) => b is >= (byte)'0' and <= (byte)'7';

        private static bool IsDigit(byte b) => b is >= (byte)'0' and <= (byte)'9';

        // parse.go unhex: -1 for a character that is no hexadecimal digit.
        private static int Unhex(int c) => c switch
        {
            >= 0x30 and <= 0x39 => c - 0x30,
            >= 0x61 and <= 0x66 => c - 0x61 + 10,
            >= 0x41 and <= 0x46 => c - 0x41 + 10,
            _ => -1,
        };

        // The stack

        // parse.go push with checkLimits.
        private bool Push(PatternNode node)
        {
            if (!CheckLimits(node))
            {
                return false;
            }
            stack.Add(node);
            return true;
        }

        private bool CheckLimits(PatternNode node)
        {
            if (node.Size > MaxSize)
            {
                return Fail(PatternProblem.Large);
            }
            return node.Height <= MaxHeight || Fail(PatternProblem.NestingDepth);
        }

        // parse.go repeat: the top of the stack, repeated. The "?" of a
        // non-greedy operator goes with it.
        private bool RepeatTop(PatternNode.NodeOp op, int min, int max, bool lastRepeat)
        {
            if (i < s.Length && s[i] == (byte)'?')
            {
                i++;
            }
            if (lastRepeat)
            {
                // Perl does not stack repetition operators: a** is an error,
                // and a++ means something RE2 does not have.
                return Fail(PatternProblem.InvalidRepeatOp);
            }
            if (stack.Count == 0 || stack[^1].IsPseudo)
            {
                return Fail(PatternProblem.MissingRepeatArgument);
            }
            var node = new PatternNode(op, [stack[^1]], min, max);
            stack[^1] = node;
            if (!CheckLimits(node))
            {
                return false;
            }
            if (op == PatternNode.NodeOp.Repeat && (min >= 2 || max >= 2) && !RepeatIsValid(node, MaxRepeat))
            {
                return Fail(PatternProblem.InvalidRepeatSize);
            }
            return true;
        }

        // parse.go repeatIsValid: the repetition with those inside it makes
        // at most n copies of the innermost piece.
        private static bool RepeatIsValid(PatternNode node, int n)
        {
            if (node.Op == PatternNode.NodeOp.Repeat)
            {
                var m = node.Max;
                if (m == 0)
                {
                    return true;
                }
                if (m < 0)
                {
                    m = node.Min;
                }
                if (m > n)
                {
                    return false;
                }
                if (m > 0)
                {
                    n /= m;
                }
            }
            return node.Subs.All(sub => RepeatIsValid(sub, n));
        }

        // The pieces above the topmost "(" or "|", taken off the stack.
        private List<PatternNode> PopPieces()
        {
            var k = stack.Count;
            while (k > 0 && !stack[k - 1].IsPseudo)
            {
                k--;
            }
            var subs = stack.GetRange(k, stack.Count - k);
            stack.RemoveRange(k, stack.Count - k);
            return subs;
        }

        // parse.go concat.
        private bool Concat()
        {
            var subs = PopPieces();
            return Push(subs.Count == 0 ? new PatternNode(PatternNode.NodeOp.Other) : Collapse(subs, PatternNode.NodeOp.Concat));
        }

        // parse.go alternate.
        private bool Alternate()
        {
            var subs = PopPieces();
            return Push(subs.Count == 0 ? new PatternNode(PatternNode.NodeOp.Other) : Collapse(subs, PatternNode.NodeOp.Alternate));
        }

        // parse.go collapse: never a concatenation of a concatenation, or an
        // alternation of an alternation.
        private static PatternNode Collapse(List<PatternNode> subs, PatternNode.NodeOp op)
        {
            if (subs.Count == 1)
            {
                return subs[0];
            }
            var flat = new List<PatternNode>();
            foreach (var sub in subs)
            {
                if (sub.Op == op)
                {
                    flat.AddRange(sub.Subs);
                }
                else
                {
                    flat.Add(sub);
                }
            }
            return new PatternNode(op, flat);
        }

        // parse.go parseVerticalBar.
        private bool ParseVerticalBar() =>
            Concat() && (SwapVerticalBar() || Push(new PatternNode(PatternNode.NodeOp.VerticalBar)));

        // parse.go swapVerticalBar: the piece on top goes below the "|" under
        // it; two single characters around a "|" become one class.
        private bool SwapVerticalBar()
        {
            var n = stack.Count;
            if (n >= 3 && stack[n - 2].Op == PatternNode.NodeOp.VerticalBar && stack[n - 1].Op == PatternNode.NodeOp.Single
                && stack[n - 3].Op == PatternNode.NodeOp.Single)
            {
                stack.RemoveAt(n - 1);
                return true;
            }
            if (n >= 2 && stack[n - 2].Op == PatternNode.NodeOp.VerticalBar)
            {
                (stack[n - 2], stack[n - 1]) = (stack[n - 1], stack[n - 2]);
                return true;
            }
            return false;
        }

        // parse.go parseRightParen.
        private bool ParseRightParen()
        {
            if (!Concat())
            {
                return false;
            }
            if (SwapVerticalBar())
            {
                stack.RemoveAt(stack.Count - 1);
            }
            if (!Alternate())
            {
                return false;
            }
            var n = stack.Count;
            if (n < 2)
            {
                return Fail(PatternProblem.UnexpectedParen);
            }
            var inner = stack[n - 1];
            var paren = stack[n - 2];
            stack.RemoveRange(n - 2, 2);
            if (paren.Op != PatternNode.NodeOp.LeftParen)
            {
                return Fail(PatternProblem.UnexpectedParen);
            }
            return Push(paren.Capturing ? new PatternNode(PatternNode.NodeOp.Capture, [inner]) : inner);
        }

        // Repetitions

        // parse.go parseInt: a decimal number without leading zeros; -1 for
        // one that is too big. Null when there is none.
        private (int Value, int Next)? ParseInt(int at)
        {
            if (at >= s.Length || !IsDigit(s[at]))
            {
                return null;
            }
            if (at + 1 < s.Length && s[at] == (byte)'0' && IsDigit(s[at + 1]))
            {
                return null;
            }
            var k = at;
            var n = 0;
            while (k < s.Length && IsDigit(s[k]))
            {
                if (n >= 0)
                {
                    n = n >= 100_000_000 ? -1 : (n * 10) + (s[k] - (byte)'0');
                }
                k++;
            }
            return (n, k);
        }

        // parse.go parseRepeat: {min}, {min,} (max -1) or {min,max} at `at`;
        // null when the text is not of that form. A number too big makes
        // min -1.
        private (int Min, int Max, int Next)? ParseRepeat(int at)
        {
            if (at >= s.Length || s[at] != (byte)'{' || ParseInt(at + 1) is not { } first)
            {
                return null;
            }
            var min = first.Value;
            var max = min;
            var k = first.Next;
            if (k >= s.Length)
            {
                return null;
            }
            if (s[k] == (byte)',')
            {
                k++;
                if (k >= s.Length)
                {
                    return null;
                }
                if (s[k] == (byte)'}')
                {
                    max = -1;
                }
                else
                {
                    if (ParseInt(k) is not { } second)
                    {
                        return null;
                    }
                    max = second.Value;
                    k = second.Next;
                    if (max < 0)
                    {
                        min = -1;
                    }
                }
            }
            if (k >= s.Length || s[k] != (byte)'}')
            {
                return null;
            }
            return (min, max, k + 1);
        }

        // Groups

        // parse.go parsePerlFlags: a named group, a non-capturing group or
        // flags, at "(?".
        private bool ParsePerlFlags()
        {
            var t = i;
            var length = s.Length - t;
            var startsWithP = length > 4 && s[t + 2] == (byte)'P' && s[t + 3] == (byte)'<';
            var startsWithName = length > 3 && s[t + 2] == (byte)'<';
            if (startsWithP || startsWithName)
            {
                var nameStart = t + (startsWithName ? 3 : 4);
                var end = IndexOf(">"u8, t);
                if (end < 0)
                {
                    return Fail(PatternProblem.InvalidNamedCapture);
                }
                if (end <= nameStart || !s.AsSpan(nameStart, end - nameStart).ToArray().All(c => c == (byte)'_' || IsAlnum(c)))
                {
                    return Fail(PatternProblem.InvalidNamedCapture);
                }
                if (!Push(new PatternNode(PatternNode.NodeOp.LeftParen, capturing: true)))
                {
                    return false;
                }
                i = end + 1;
                return true;
            }

            var k = t + 2;
            var negated = false;
            var sawFlag = false;
            while (k < s.Length)
            {
                var c = RuneAt(k);
                k += c.Size;
                switch (c.Value)
                {
                    case 0x69 or 0x6D or 0x73 or 0x55: // i, m, s, U
                        sawFlag = true;
                        continue;
                    case 0x2D: // -
                        if (negated)
                        {
                            break;
                        }
                        negated = true;
                        sawFlag = false;
                        continue;
                    case 0x3A or 0x29: // : and )
                        if (negated && !sawFlag)
                        {
                            break;
                        }
                        if (c.Value == 0x3A && !Push(new PatternNode(PatternNode.NodeOp.LeftParen)))
                        {
                            return false;
                        }
                        i = k;
                        return true;
                    default:
                        break;
                }
                break;
            }
            return Fail(PatternProblem.InvalidPerlOp);
        }

        // Escapes

        // The "\" of the main loop: an anchor, a quoted text, a class or an
        // escaped character.
        private bool ParseBackslash()
        {
            if (i + 1 < s.Length)
            {
                switch (s[i + 1])
                {
                    case (byte)'A' or (byte)'b' or (byte)'B' or (byte)'z':
                        if (!Push(new PatternNode(PatternNode.NodeOp.Other)))
                        {
                            return false;
                        }
                        i += 2;
                        return true;
                    case (byte)'C':
                        // Any byte: not supported.
                        return Fail(PatternProblem.InvalidEscape);
                    case (byte)'Q':
                        // \Q ... \E: always literals.
                        var start = i + 2;
                        var end = IndexOf("\\E"u8, start);
                        var k = start;
                        while (k < (end < 0 ? s.Length : end))
                        {
                            k += Math.Max(RuneAt(k).Size, 1);
                            if (!Push(new PatternNode(PatternNode.NodeOp.Single)))
                            {
                                return false;
                            }
                        }
                        i = end < 0 ? s.Length : end + 2;
                        return true;
                    default:
                        break;
                }
            }
            if (!ParseUnicodeClass(i, out var afterClass))
            {
                return false;
            }
            if (afterClass is { } next)
            {
                i = next;
                return Push(new PatternNode(PatternNode.NodeOp.Single));
            }
            if (ParsePerlClass(i) is { } perl)
            {
                i = perl;
                return Push(new PatternNode(PatternNode.NodeOp.Single));
            }
            if (ParseEscape(i) is not { } escape)
            {
                return false;
            }
            i = escape.Next;
            return Push(new PatternNode(PatternNode.NodeOp.Single));
        }

        // parse.go parseEscape: the character an escape at `at` stands for;
        // null (with the reason) when it is not one.
        private (int Value, int Next)? ParseEscape(int at)
        {
            var t = at + 1;
            if (t >= s.Length)
            {
                Fail(PatternProblem.TrailingBackslash);
                return null;
            }
            var c = RuneAt(t);
            t += c.Size;
            switch (c.Value)
            {
                case >= 0x31 and <= 0x37: // 1 to 7
                    // A single digit is a back-reference: not supported.
                    if (t < s.Length && IsOctal(s[t]))
                    {
                        return Octal(c.Value, t);
                    }
                    break;
                case 0x30:
                    return Octal(c.Value, t);
                case 0x78: // x
                    if (t >= s.Length)
                    {
                        break;
                    }
                    var d = RuneAt(t);
                    t += d.Size;
                    if (d.Value == 0x7B) // {
                    {
                        // Hexadecimal digits in braces, at least one.
                        var digits = 0;
                        var r = 0;
                        while (true)
                        {
                            if (t >= s.Length)
                            {
                                Fail(PatternProblem.InvalidEscape);
                                return null;
                            }
                            d = RuneAt(t);
                            t += d.Size;
                            if (d.Value == 0x7D) // }
                            {
                                break;
                            }
                            var v = Unhex(d.Value);
                            if (v < 0)
                            {
                                Fail(PatternProblem.InvalidEscape);
                                return null;
                            }
                            r = (r * 16) + v;
                            if (r > 0x10FFFF)
                            {
                                Fail(PatternProblem.InvalidEscape);
                                return null;
                            }
                            digits++;
                        }
                        if (digits == 0)
                        {
                            Fail(PatternProblem.InvalidEscape);
                            return null;
                        }
                        return (r, t);
                    }
                    // Two hexadecimal digits.
                    var x = Unhex(d.Value);
                    var e = RuneAt(t);
                    t += e.Size;
                    var y = Unhex(e.Value);
                    if (x >= 0 && y >= 0)
                    {
                        return ((x * 16) + y, t);
                    }
                    break;
                case 0x61:
                    return (0x07, t); // \a
                case 0x66:
                    return (0x0C, t); // \f
                case 0x6E:
                    return (0x0A, t); // \n
                case 0x72:
                    return (0x0D, t); // \r
                case 0x74:
                    return (0x09, t); // \t
                case 0x76:
                    return (0x0B, t); // \v
                default:
                    if (c.Value < 0x80 && !IsAlnum(c.Value))
                    {
                        // An escaped character that is no letter or digit is itself.
                        return (c.Value, t);
                    }
                    break;
            }
            Fail(PatternProblem.InvalidEscape);
            return null;
        }

        // Up to three octal digits, the first one read already.
        private (int Value, int Next) Octal(int first, int at)
        {
            var r = first - 0x30;
            var t = at;
            var digits = 1;
            while (digits < 3 && t < s.Length && IsOctal(s[t]))
            {
                r = (r * 8) + (s[t] - (byte)'0');
                t++;
                digits++;
            }
            return (r, t);
        }

        // Classes

        // parse.go parsePerlClassEscape: \d, \s, \w and their negations at
        // `at`; null when there is none.
        private int? ParsePerlClass(int at) =>
            at + 1 < s.Length && s[at] == (byte)'\\' && s[at + 1] is (byte)'d' or (byte)'D' or (byte)'s' or (byte)'S' or (byte)'w' or (byte)'W'
                ? at + 2
                : null;

        // parse.go parseUnicodeClass: \pL, \p{Greek} and their negations at
        // `at`: false (with the reason) for a bad one, next null when there
        // is none.
        private bool ParseUnicodeClass(int at, out int? next)
        {
            next = null;
            if (at + 1 >= s.Length || s[at] != (byte)'\\' || s[at + 1] is not ((byte)'p' or (byte)'P'))
            {
                return true;
            }
            var t = at + 2;
            var c = RuneAt(t);
            t += c.Size;
            ReadOnlySpan<byte> name;
            if (c.Value != 0x7B) // {
            {
                // A name of one letter.
                name = s.AsSpan(at + 2, t - (at + 2));
            }
            else
            {
                var end = IndexOf("}"u8, at);
                if (end < 0)
                {
                    return Fail(PatternProblem.InvalidCharRange);
                }
                name = s.AsSpan(at + 3, end - (at + 3));
                t = end + 1;
            }
            // \p{^Han} is \P{Han}.
            if (!name.IsEmpty && name[0] == (byte)'^')
            {
                name = name[1..];
            }
            if (!UnicodeClasses.Contains(CanonicalName(name)))
            {
                return Fail(PatternProblem.InvalidCharRange);
            }
            next = t;
            return true;
        }

        // parse.go canonicalName: the name a Unicode class is looked up by: an
        // upper-case letter first, then lower case, without underscores,
        // hyphens and spaces.
        private static string CanonicalName(ReadOnlySpan<byte> name)
        {
            var output = new List<byte>(name.Length);
            var first = true;
            foreach (var b in name)
            {
                var c = b;
                if (c is (byte)'_' or (byte)'-' or (byte)' ')
                {
                    continue;
                }
                if (first)
                {
                    if (c is >= (byte)'a' and <= (byte)'z')
                    {
                        c -= 0x20;
                    }
                    first = false;
                }
                else if (c is >= (byte)'A' and <= (byte)'Z')
                {
                    c += 0x20;
                }
                output.Add(c);
            }
            return Encoding.UTF8.GetString([.. output]);
        }

        // parse.go parseClass: a class in brackets at the "[".
        private bool ParseClass()
        {
            var t = i + 1;
            if (t < s.Length && s[t] == (byte)'^')
            {
                t++;
            }
            // "]" is an ordinary character as the first one of the class.
            var first = true;
            while (t >= s.Length || s[t] != (byte)']' || first)
            {
                first = false;
                // A POSIX class, [:alnum:].
                if (s.Length - t > 2 && s[t] == (byte)'[' && s[t + 1] == (byte)':' && IndexOf(":]"u8, t + 2) is var end and >= 0)
                {
                    if (!PosixClasses.Contains(Encoding.UTF8.GetString(s, t, end + 2 - t)))
                    {
                        return Fail(PatternProblem.InvalidCharRange);
                    }
                    t = end + 2;
                    continue;
                }
                if (!ParseUnicodeClass(t, out var afterClass))
                {
                    return false;
                }
                if (afterClass is { } next)
                {
                    t = next;
                    continue;
                }
                if (ParsePerlClass(t) is { } perl)
                {
                    t = perl;
                    continue;
                }
                // A character or a range.
                if (ParseClassChar(t) is not { } lo)
                {
                    return false;
                }
                t = lo.Next;
                // [a-] is "a" or "-".
                if (s.Length - t >= 2 && s[t] == (byte)'-' && s[t + 1] != (byte)']')
                {
                    if (ParseClassChar(t + 1) is not { } hi)
                    {
                        return false;
                    }
                    t = hi.Next;
                    if (hi.Value < lo.Value)
                    {
                        return Fail(PatternProblem.InvalidCharRange);
                    }
                }
            }
            i = t + 1;
            return Push(new PatternNode(PatternNode.NodeOp.Single));
        }

        // parse.go parseClassChar: a character of a class at `at`.
        private (int Value, int Next)? ParseClassChar(int at)
        {
            if (at >= s.Length)
            {
                Fail(PatternProblem.MissingBracket);
                return null;
            }
            if (s[at] == (byte)'\\')
            {
                return ParseEscape(at);
            }
            var c = RuneAt(at);
            return (c.Value, at + c.Size);
        }

        // Tables

        // perl_groups.go posixGroup.
        private static readonly FrozenSet<string> PosixClasses = new[]
        {
            "alnum", "alpha", "ascii", "blank", "cntrl", "digit", "graph", "lower", "print", "punct", "space", "upper", "word", "xdigit",
        }.SelectMany(n => new[] { $"[:{n}:]", $"[:^{n}:]" }).ToFrozenSet(StringComparer.Ordinal);

        // The names of parse.go unicodeTable in their canonical form: Any,
        // Assigned and ASCII, and of Go's package unicode (Unicode 15.0) the
        // categories, their long names and the scripts.
        private static readonly FrozenSet<string> UnicodeClasses = new[]
        {
            "Adlam", "Ahom", "Anatolianhieroglyphs", "Any", "Arabic", "Armenian", "Ascii", "Assigned", "Avestan",
            "Balinese", "Bamum", "Bassavah", "Batak", "Bengali", "Bhaiksuki", "Bopomofo", "Brahmi", "Braille", "Buginese",
            "Buhid", "C", "Canadianaboriginal", "Carian", "Casedletter", "Caucasianalbanian", "Cc", "Cf", "Chakma",
            "Cham", "Cherokee", "Chorasmian", "Closepunctuation", "Cn", "Cntrl", "Co", "Combiningmark", "Common",
            "Connectorpunctuation", "Control", "Coptic", "Cs", "Cuneiform", "Currencysymbol", "Cypriot", "Cyprominoan",
            "Cyrillic", "Dashpunctuation", "Decimalnumber", "Deseret", "Devanagari", "Digit", "Divesakuru", "Dogra",
            "Duployan", "Egyptianhieroglyphs", "Elbasan", "Elymaic", "Enclosingmark", "Ethiopic", "Finalpunctuation",
            "Format", "Georgian", "Glagolitic", "Gothic", "Grantha", "Greek", "Gujarati", "Gunjalagondi", "Gurmukhi",
            "Han", "Hangul", "Hanifirohingya", "Hanunoo", "Hatran", "Hebrew", "Hiragana", "Imperialaramaic", "Inherited",
            "Initialpunctuation", "Inscriptionalpahlavi", "Inscriptionalparthian", "Javanese", "Kaithi", "Kannada",
            "Katakana", "Kawi", "Kayahli", "Kharoshthi", "Khitansmallscript", "Khmer", "Khojki", "Khudawadi", "L", "Lao",
            "Latin", "Lc", "Lepcha", "Letter", "Letternumber", "Limbu", "Lineara", "Linearb", "Lineseparator", "Lisu",
            "Ll", "Lm", "Lo", "Lowercaseletter", "Lt", "Lu", "Lycian", "Lydian", "M", "Mahajani", "Makasar", "Malayalam",
            "Mandaic", "Manichaean", "Marchen", "Mark", "Masaramgondi", "Mathsymbol", "Mc", "Me", "Medefaidrin",
            "Meeteimayek", "Mendekikakui", "Meroiticcursive", "Meroitichieroglyphs", "Miao", "Mn", "Modi",
            "Modifierletter", "Modifiersymbol", "Mongolian", "Mro", "Multani", "Myanmar", "N", "Nabataean", "Nagmundari",
            "Nandinagari", "Nd", "Newa", "Newtailue", "Nko", "Nl", "No", "Nonspacingmark", "Number", "Nushu",
            "Nyiakengpuachuehmong", "Ogham", "Olchiki", "Oldhungarian", "Olditalic", "Oldnortharabian", "Oldpermic",
            "Oldpersian", "Oldsogdian", "Oldsoutharabian", "Oldturkic", "Olduyghur", "Openpunctuation", "Oriya", "Osage",
            "Osmanya", "Other", "Otherletter", "Othernumber", "Otherpunctuation", "Othersymbol", "P", "Pahawhhmong",
            "Palmyrene", "Paragraphseparator", "Paucinhau", "Pc", "Pd", "Pe", "Pf", "Phagspa", "Phoenician", "Pi", "Po",
            "Privateuse", "Ps", "Psalterpahlavi", "Punct", "Punctuation", "Rejang", "Runic", "S", "Samaritan",
            "Saurashtra", "Sc", "Separator", "Sharada", "Shavian", "Siddham", "Signwriting", "Sinhala", "Sk", "Sm", "So",
            "Sogdian", "Sorasompeng", "Soyombo", "Spaceseparator", "Spacingmark", "Sundanese", "Surrogate", "Sylotinagri",
            "Symbol", "Syriac", "Tagalog", "Tagbanwa", "Taile", "Taitham", "Taiviet", "Takri", "Tamil", "Tangsa",
            "Tangut", "Telugu", "Thaana", "Thai", "Tibetan", "Tifinagh", "Tirhuta", "Titlecaseletter", "Toto", "Ugaritic",
            "Unassigned", "Uppercaseletter", "Vai", "Vithkuqi", "Wancho", "Warangciti", "Yezidi", "Yi", "Z",
            "Zanabazarsquare", "Zl", "Zp", "Zs",
        }.ToFrozenSet(StringComparer.Ordinal);
    }
}
