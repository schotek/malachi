// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/I18n/GettextFormat.swift; GTK: fmt.Sprintf
// over the msgids of ui/internal/i18n (T, N, C).
//
// The catalogues keep the GTK UI's printf formats (Go's fmt: %s, %d, %.1f,
// %%). macOS converts them to Foundation's syntax; Windows has no such
// format, so this formats the gettext pattern itself, as fmt.Sprintf does in
// the GTK UI: a port of Go's doPrintf for the verbs s, d, f, v and %, with
// the flags, width, precision and explicit argument indexes ([n]) of Go, and
// the POSIX positional form %N$ that gettext translators write (Go does not
// know it; macOS does). Numbers use the invariant culture (1.5, never 1,5)
// and %f rounds the exact binary value half to even, as Go's strconv does.
//
// It never throws: a verb that does not fit its argument, a missing or extra
// argument, a bad index and a pattern that ends in '%' produce the markers
// Go writes (%!d(string=x), %!s(MISSING), %!(EXTRA int=1), %!s(BADINDEX),
// %!(NOVERB)); verbs other than s, d, f, v and % are reported like a verb
// that does not fit. Directives() lists what a pattern consumes, so that the
// catalogue can drop a translation whose directives differ from its msgid.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Numerics;
using System.Text;

namespace Malachi.Core.I18n;

/// <summary>
/// Formats gettext patterns (Go's printf syntax) and compares their
/// directives.
/// </summary>
public static class GettextFormat
{
    // Go's fmt rejects widths and precisions beyond a million (tooLarge).
    private const int MaxNumber = 1_000_000;

    // Every fractional digit of a double beyond the 1074th is zero, so %f
    // computes at most this many digits exactly and pads the rest.
    private const int MaxExactFractionDigits = 1100;

    /// <summary>
    /// Formats <paramref name="pattern"/> with <paramref name="args"/> as Go's
    /// <c>fmt.Sprintf</c> does (<c>%[N$][flags][width][.prec]s|d|f|v|%</c> and
    /// Go's <c>[n]</c> indexes), in the invariant culture. Never throws.
    /// </summary>
    public static string Format(string pattern, params object?[]? args) => Format(pattern, args, reportExtra: true);

    /// <summary>
    /// <see cref="Format(string, object?[])"/>, but arguments the pattern does
    /// not use are left out silently instead of being reported as
    /// <c>%!(EXTRA …)</c>: for plural forms, which may leave out the count
    /// (as <c>String(format:)</c> ignores it on macOS).
    /// </summary>
    internal static string Format(string pattern, object?[]? args, bool reportExtra)
    {
        ArgumentNullException.ThrowIfNull(pattern);
        var printer = new Printer();
        printer.Print(pattern, args ?? [], reportExtra);
        return printer.ToString();
    }

    /// <summary>
    /// The directives of <paramref name="pattern"/> that consume an argument,
    /// as <c>"index:verb"</c> with the 1-based argument index (a <c>*</c>
    /// width or precision is verb <c>*</c>), plus <c>"BADINDEX:verb"</c> for an
    /// index that cannot be right and <c>"NOVERB"</c> for a pattern ending in
    /// the middle of a directive. <c>%%</c> consumes nothing and is not listed.
    /// </summary>
    public static IReadOnlyList<string> Directives(string pattern)
    {
        ArgumentNullException.ThrowIfNull(pattern);
        var result = new List<string>();
        var end = pattern.Length;
        var argNum = 0;
        var i = 0;
        while (i < end)
        {
            while (i < end && pattern[i] != '%')
            {
                i++;
            }
            if (i >= end)
            {
                break;
            }
            i++;
            var good = true;
            (argNum, i, good) = PosixIndex(pattern, i, argNum, int.MaxValue, good, out _);
            while (i < end && IsFlag(pattern[i]))
            {
                i++;
            }
            bool afterIndex;
            (argNum, i, afterIndex, good) = BracketIndex(pattern, i, argNum, int.MaxValue, good, out _);
            if (i < end && pattern[i] == '*')
            {
                i++;
                result.Add(Entry(argNum, "*"));
                argNum++;
                afterIndex = false;
            }
            else
            {
                (_, var present, i) = ParseNumber(pattern, i, end);
                if (afterIndex && present)
                {
                    good = false;
                }
            }
            if (i + 1 < end && pattern[i] == '.')
            {
                i++;
                if (afterIndex)
                {
                    good = false;
                }
                (argNum, i, afterIndex, good) = BracketIndex(pattern, i, argNum, int.MaxValue, good, out _);
                if (i < end && pattern[i] == '*')
                {
                    i++;
                    result.Add(Entry(argNum, "*"));
                    argNum++;
                    afterIndex = false;
                }
                else
                {
                    (_, _, i) = ParseNumber(pattern, i, end);
                }
            }
            if (!afterIndex)
            {
                (argNum, i, _, good) = BracketIndex(pattern, i, argNum, int.MaxValue, good, out _);
            }
            if (i >= end)
            {
                result.Add("NOVERB");
                break;
            }
            var verb = ReadRune(pattern, ref i);
            if (verb == '%')
            {
                continue;
            }
            if (!good)
            {
                result.Add("BADINDEX:" + RuneText(verb));
                continue;
            }
            result.Add(Entry(argNum, RuneText(verb)));
            argNum++;
        }
        return result;
    }

    /// <summary>
    /// Whether two patterns consume the same arguments with the same verbs,
    /// in any order and however often (a translation may reorder with
    /// <c>%2$s</c> or <c>%[2]s</c> and change flags, width and precision).
    /// </summary>
    public static bool SameDirectives(string a, string b) =>
        new HashSet<string>(Directives(a), StringComparer.Ordinal).SetEquals(Directives(b));

    private static string Entry(int argNum, string verb) =>
        (argNum + 1).ToString(CultureInfo.InvariantCulture) + ":" + verb;

    private static bool IsFlag(char c) => c is '#' or '0' or '+' or '-' or ' ';

    // The POSIX positional form: digits and '$' right after the '%'.
    private static (int ArgNum, int I, bool Good) PosixIndex(
        string format, int i, int argNum, int numArgs, bool good, out bool found)
    {
        found = false;
        var j = i;
        while (j < format.Length && format[j] is >= '0' and <= '9')
        {
            j++;
        }
        if (j == i || j >= format.Length || format[j] != '$')
        {
            return (argNum, i, good);
        }
        found = true;
        var (n, ok, newi) = ParseNumber(format, i, j);
        if (ok && newi == j && n >= 1 && n <= numArgs)
        {
            return (n - 1, j + 1, good);
        }
        return (argNum, j + 1, false);
    }

    // Go's argNumber: a bracketed one-based index such as [2].
    private static (int ArgNum, int I, bool Found, bool Good) BracketIndex(
        string format, int i, int argNum, int numArgs, bool good, out bool seen)
    {
        seen = false;
        if (format.Length <= i || format[i] != '[')
        {
            return (argNum, i, false, good);
        }
        seen = true;
        var (index, width, ok) = ParseArgNumber(format, i);
        if (ok && index >= 0 && index < numArgs)
        {
            return (index, i + width, true, good);
        }
        return (argNum, i + width, ok, false);
    }

    // Go's parseArgNumber: the zero-based index, the characters to consume
    // and whether the number parsed.
    private static (int Index, int Width, bool Ok) ParseArgNumber(string format, int start)
    {
        if (format.Length - start < 3)
        {
            return (0, 1, false);
        }
        for (var k = start + 1; k < format.Length; k++)
        {
            if (format[k] == ']')
            {
                var (n, ok, newi) = ParseNumber(format, start + 1, k);
                if (!ok || newi != k)
                {
                    return (0, k - start + 1, false);
                }
                return (n - 1, k - start + 1, true);
            }
        }
        return (0, 1, false);
    }

    // Go's parsenum, with its overflow rule.
    private static (int Number, bool Present, int Next) ParseNumber(string s, int start, int end)
    {
        if (start >= end)
        {
            return (0, false, end);
        }
        var number = 0;
        var present = false;
        var next = start;
        for (; next < end && s[next] is >= '0' and <= '9'; next++)
        {
            if (number > MaxNumber)
            {
                return (0, false, end);
            }
            number = number * 10 + (s[next] - '0');
            present = true;
        }
        return (number, present, next);
    }

    // One code point; a lone surrogate is U+FFFD, as Go decodes bad UTF-8.
    private static int ReadRune(string s, ref int i)
    {
        var c = s[i];
        if (char.IsHighSurrogate(c) && i + 1 < s.Length && char.IsLowSurrogate(s[i + 1]))
        {
            var rune = char.ConvertToUtf32(c, s[i + 1]);
            i += 2;
            return rune;
        }
        i++;
        return char.IsSurrogate(c) ? 0xFFFD : c;
    }

    private static string RuneText(int rune) => char.ConvertFromUtf32(rune);

    private static int RuneCount(string s)
    {
        var count = 0;
        for (var i = 0; i < s.Length; i++)
        {
            if (char.IsHighSurrogate(s[i]) && i + 1 < s.Length && char.IsLowSurrogate(s[i + 1]))
            {
                i++;
            }
            count++;
        }
        return count;
    }

    // The first n code points of s.
    private static string TruncateRunes(string s, int n)
    {
        var i = 0;
        for (var taken = 0; taken < n && i < s.Length; taken++)
        {
            i += char.IsHighSurrogate(s[i]) && i + 1 < s.Length && char.IsLowSurrogate(s[i + 1]) ? 2 : 1;
        }
        return s[..i];
    }

    // What Go's reflect.TypeOf(arg).String() says for the closest Go type.
    private static string TypeName(object arg) => arg switch
    {
        bool => "bool",
        string => "string",
        int => "int",
        long => "int64",
        short => "int16",
        sbyte => "int8",
        uint => "uint32",
        ulong => "uint64",
        ushort => "uint16",
        byte => "uint8",
        nint => "int",
        nuint => "uint",
        char => "int32",
        float => "float32",
        double => "float64",
        decimal => "decimal",
        _ => arg.GetType().Name,
    };

    // Integers as Go sees them: a sign and a magnitude.
    private static bool TryInteger(object arg, out bool negative, out BigInteger magnitude)
    {
        BigInteger value;
        switch (arg)
        {
            case int v: value = v; break;
            case long v: value = v; break;
            case short v: value = v; break;
            case sbyte v: value = v; break;
            case uint v: value = v; break;
            case ulong v: value = v; break;
            case ushort v: value = v; break;
            case byte v: value = v; break;
            case nint v: value = (long)v; break;
            case nuint v: value = (ulong)v; break;
            case char v: value = v; break;
            case Int128 v: value = (BigInteger)v; break;
            case UInt128 v: value = (BigInteger)v; break;
            case BigInteger v: value = v; break;
            case Enum e:
                value = Type.GetTypeCode(Enum.GetUnderlyingType(e.GetType())) is TypeCode.UInt64 or TypeCode.UInt32
                        or TypeCode.UInt16 or TypeCode.Byte
                    ? Convert.ToUInt64(e, CultureInfo.InvariantCulture)
                    : Convert.ToInt64(e, CultureInfo.InvariantCulture);
                break;
            default:
                negative = false;
                magnitude = BigInteger.Zero;
                return false;
        }
        negative = value.Sign < 0;
        magnitude = BigInteger.Abs(value);
        return true;
    }

    // Go's intFromArg, for a '*' width or precision.
    private static (int Number, bool IsInt, int ArgNum) IntFromArg(object?[] a, int argNum)
    {
        if (argNum >= a.Length)
        {
            return (0, false, argNum);
        }
        var arg = a[argNum];
        var isInt = false;
        var number = 0;
        if (arg is not null && TryInteger(arg, out var negative, out var magnitude) && magnitude <= int.MaxValue)
        {
            number = negative ? -(int)magnitude : (int)magnitude;
            isInt = true;
        }
        if (number > MaxNumber || number < -MaxNumber)
        {
            number = 0;
            isInt = false;
        }
        return (number, isInt, argNum + 1);
    }

    // strconv.FormatFloat(v, 'f', prec, 64) with a sign in front ('+' for
    // positive numbers and NaN): the exact binary value, rounded half to even.
    private static string FormatFixed(double v, int prec)
    {
        if (double.IsNaN(v))
        {
            return "+NaN";
        }
        if (double.IsInfinity(v))
        {
            return v > 0 ? "+Inf" : "-Inf";
        }
        var bits = BitConverter.DoubleToInt64Bits(v);
        var negative = bits < 0;
        var exponent = (int)((bits >> 52) & 0x7FF);
        var mantissa = (ulong)bits & 0xF_FFFF_FFFF_FFFFUL;
        int binaryExponent;
        if (exponent == 0)
        {
            binaryExponent = -1074;
        }
        else
        {
            mantissa |= 1UL << 52;
            binaryExponent = exponent - 1075;
        }
        var exact = Math.Min(prec, MaxExactFractionDigits);
        BigInteger scaled;
        if (mantissa == 0)
        {
            scaled = BigInteger.Zero;
        }
        else if (binaryExponent >= 0)
        {
            scaled = (new BigInteger(mantissa) << binaryExponent) * BigInteger.Pow(10, exact);
        }
        else
        {
            var numerator = new BigInteger(mantissa) * BigInteger.Pow(10, exact);
            var denominator = BigInteger.One << -binaryExponent;
            scaled = BigInteger.DivRem(numerator, denominator, out var remainder);
            var half = (remainder << 1).CompareTo(denominator);
            if (half > 0 || (half == 0 && !scaled.IsEven))
            {
                scaled += BigInteger.One;
            }
        }
        var digits = scaled.ToString(CultureInfo.InvariantCulture);
        if (digits.Length <= exact)
        {
            digits = new string('0', exact - digits.Length + 1) + digits;
        }
        var sb = new StringBuilder(digits.Length + prec - exact + 2);
        sb.Append(negative ? '-' : '+');
        sb.Append(digits, 0, digits.Length - exact);
        if (prec > 0)
        {
            sb.Append('.');
            sb.Append(digits, digits.Length - exact, exact);
            sb.Append('0', prec - exact);
        }
        return sb.ToString();
    }

    // strconv.FormatFloat(v, 'g', -1, size) with a sign in front: the
    // shortest digits that read back as the same value, in %e form when the
    // exponent is below -4 or at least 6 (what %v prints).
    private static string FormatShortest(double v, bool single)
    {
        if (double.IsNaN(v))
        {
            return "+NaN";
        }
        if (double.IsInfinity(v))
        {
            return v > 0 ? "+Inf" : "-Inf";
        }
        var text = single
            ? ((float)v).ToString("R", CultureInfo.InvariantCulture)
            : v.ToString("R", CultureInfo.InvariantCulture);
        var negative = text.StartsWith('-');
        if (negative)
        {
            text = text[1..];
        }
        var exponent = 0;
        var e = text.IndexOfAny(['E', 'e']);
        if (e >= 0)
        {
            exponent = int.Parse(text.AsSpan(e + 1), NumberStyles.AllowLeadingSign, CultureInfo.InvariantCulture);
            text = text[..e];
        }
        var point = text.IndexOf('.', StringComparison.Ordinal);
        var digits = point < 0 ? text : text.Remove(point, 1);
        var decimalPoint = (point < 0 ? text.Length : point) + exponent;
        var lead = 0;
        while (lead < digits.Length && digits[lead] == '0')
        {
            lead++;
        }
        digits = digits[lead..].TrimEnd('0');
        decimalPoint -= lead;
        var sb = new StringBuilder(digits.Length + 8);
        sb.Append(negative ? '-' : '+');
        if (digits.Length == 0)
        {
            sb.Append('0');
            return sb.ToString();
        }
        var exp = decimalPoint - 1;
        if (exp < -4 || exp >= 6)
        {
            sb.Append(digits[0]);
            if (digits.Length > 1)
            {
                sb.Append('.').Append(digits, 1, digits.Length - 1);
            }
            sb.Append('e').Append(exp < 0 ? '-' : '+');
            var magnitude = Math.Abs(exp);
            if (magnitude < 10)
            {
                sb.Append('0');
            }
            sb.Append(magnitude.ToString(CultureInfo.InvariantCulture));
            return sb.ToString();
        }
        if (decimalPoint <= 0)
        {
            sb.Append("0.").Append('0', -decimalPoint).Append(digits);
        }
        else if (decimalPoint >= digits.Length)
        {
            sb.Append(digits).Append('0', decimalPoint - digits.Length);
        }
        else
        {
            sb.Append(digits, 0, decimalPoint).Append('.').Append(digits, decimalPoint, digits.Length - decimalPoint);
        }
        return sb.ToString();
    }

    // Go's pp and fmt: the state of one Sprintf.
    private sealed class Printer
    {
        private readonly StringBuilder buf = new();
        private object? arg;
        private bool plus;
        private bool minus;
        private bool sharp;
        private bool space;
        private bool zero;
        private bool widPresent;
        private bool precPresent;
        private int wid;
        private int prec;
        private bool reordered;
        private bool goodArgNum;

        public override string ToString() => buf.ToString();

        // Go's doPrintf.
        public void Print(string format, object?[] a, bool reportExtra)
        {
            var end = format.Length;
            var argNum = 0;
            reordered = false;
            var i = 0;
            while (i < end)
            {
                goodArgNum = true;
                var last = i;
                while (i < end && format[i] != '%')
                {
                    i++;
                }
                if (i > last)
                {
                    buf.Append(format, last, i - last);
                }
                if (i >= end)
                {
                    break;
                }
                i++;
                ClearFlags();

                // %N$: POSIX, not Go; a bad index is reported as Go reports [n].
                (argNum, i, goodArgNum) = PosixIndex(format, i, argNum, a.Length, goodArgNum, out var posix);
                if (posix)
                {
                    reordered = true;
                }

                // Flags, and Go's fast path for a lower-case verb right after them.
                var done = false;
                for (; i < end; i++)
                {
                    var c = format[i];
                    switch (c)
                    {
                        case '#':
                            sharp = true;
                            continue;
                        case '0':
                            zero = !minus; // Only allow zero padding to the left.
                            continue;
                        case '+':
                            plus = true;
                            continue;
                        case '-':
                            minus = true;
                            zero = false; // Do not pad with zeros to the right.
                            continue;
                        case ' ':
                            space = true;
                            continue;
                    }
                    if (c is >= 'a' and <= 'z' && argNum < a.Length && goodArgNum)
                    {
                        if (c == 'v')
                        {
                            sharp = false;
                            plus = false;
                        }
                        PrintArg(a[argNum], c);
                        argNum++;
                        i++;
                        done = true;
                    }
                    break;
                }
                if (done)
                {
                    continue;
                }

                // Do we have an explicit argument index?
                (argNum, i, var afterIndex, goodArgNum) = BracketIndex(format, i, argNum, a.Length, goodArgNum, out var bracket);
                reordered |= bracket;

                // Do we have width?
                if (i < end && format[i] == '*')
                {
                    i++;
                    (wid, widPresent, argNum) = IntFromArg(a, argNum);
                    if (!widPresent)
                    {
                        buf.Append("%!(BADWIDTH)");
                    }
                    if (wid < 0)
                    {
                        wid = -wid;
                        minus = true;
                        zero = false;
                    }
                    afterIndex = false;
                }
                else
                {
                    (wid, widPresent, i) = ParseNumber(format, i, end);
                    if (afterIndex && widPresent)
                    {
                        goodArgNum = false; // "%[3]2d"
                    }
                }

                // Do we have precision?
                if (i + 1 < end && format[i] == '.')
                {
                    i++;
                    if (afterIndex)
                    {
                        goodArgNum = false; // "%[3].2d"
                    }
                    (argNum, i, afterIndex, goodArgNum) = BracketIndex(format, i, argNum, a.Length, goodArgNum, out bracket);
                    reordered |= bracket;
                    if (i < end && format[i] == '*')
                    {
                        i++;
                        (prec, precPresent, argNum) = IntFromArg(a, argNum);
                        if (prec < 0)
                        {
                            prec = 0;
                            precPresent = false;
                        }
                        if (!precPresent)
                        {
                            buf.Append("%!(BADPREC)");
                        }
                        afterIndex = false;
                    }
                    else
                    {
                        (prec, precPresent, i) = ParseNumber(format, i, end);
                        if (!precPresent)
                        {
                            prec = 0;
                            precPresent = true;
                        }
                    }
                }

                if (!afterIndex)
                {
                    (argNum, i, _, goodArgNum) = BracketIndex(format, i, argNum, a.Length, goodArgNum, out bracket);
                    reordered |= bracket;
                }

                if (i >= end)
                {
                    buf.Append("%!(NOVERB)");
                    break;
                }

                var verb = ReadRune(format, ref i);
                if (verb == '%')
                {
                    // Percent does not absorb operands and ignores width and precision.
                    buf.Append('%');
                }
                else if (!goodArgNum)
                {
                    BadArgNum(verb);
                }
                else if (argNum >= a.Length)
                {
                    MissingArg(verb);
                }
                else
                {
                    if (verb == 'v')
                    {
                        sharp = false;
                        plus = false;
                    }
                    PrintArg(a[argNum], verb);
                    argNum++;
                }
            }

            // Extra arguments, unless the pattern chose them by index.
            if (reportExtra && !reordered && argNum < a.Length)
            {
                ClearFlags();
                buf.Append("%!(EXTRA ");
                for (var k = argNum; k < a.Length; k++)
                {
                    if (k > argNum)
                    {
                        buf.Append(", ");
                    }
                    var extra = a[k];
                    if (extra is null)
                    {
                        buf.Append("<nil>");
                    }
                    else
                    {
                        buf.Append(TypeName(extra)).Append('=');
                        PrintArg(extra, 'v');
                    }
                }
                buf.Append(')');
            }
        }

        private void ClearFlags()
        {
            plus = minus = sharp = space = zero = false;
            widPresent = precPresent = false;
            wid = prec = 0;
        }

        private void PrintArg(object? value, int verb)
        {
            arg = value;
            if (value is null)
            {
                if (verb == 'v')
                {
                    PadString("<nil>");
                }
                else
                {
                    BadVerb(verb);
                }
                return;
            }
            switch (value)
            {
                case bool b:
                    FormatBool(b, verb);
                    return;
                case float f:
                    FormatFloat(f, single: true, verb);
                    return;
                case double d:
                    FormatFloat(d, single: false, verb);
                    return;
                case decimal m:
                    FormatFloat((double)m, single: false, verb);
                    return;
                case string s:
                    FormatString(s, verb);
                    return;
                case Enum when verb is 's' or 'v':
                    FormatStringer(value, verb);
                    return;
            }
            if (TryInteger(value, out var negative, out var magnitude))
            {
                if (verb is 'd' or 'v')
                {
                    FormatInteger(negative, magnitude);
                }
                else
                {
                    BadVerb(verb);
                }
                return;
            }
            // Anything else is a Stringer: ToString(), culture-invariant.
            if (verb is 's' or 'v')
            {
                FormatStringer(value, verb);
            }
            else
            {
                BadVerb(verb);
            }
        }

        // ToString() may throw; Go reports a panicking String method in place
        // of the value.
        private void FormatStringer(object value, int verb)
        {
            string text;
            try
            {
                text = (value is IFormattable formattable
                    ? formattable.ToString(null, CultureInfo.InvariantCulture)
                    : value.ToString()) ?? "";
            }
            catch (Exception e) when (e is not OutOfMemoryException)
            {
                buf.Append("%!").Append(RuneText(verb)).Append("(PANIC=String method: ").Append(e.Message).Append(')');
                return;
            }
            FormatString(text, verb);
        }

        private void FormatBool(bool value, int verb)
        {
            if (verb is 't' or 'v')
            {
                PadString(value ? "true" : "false");
            }
            else
            {
                BadVerb(verb);
            }
        }

        private void FormatString(string value, int verb)
        {
            if (verb is 's' or 'v')
            {
                PadString(precPresent ? TruncateRunes(value, prec) : value);
            }
            else
            {
                BadVerb(verb);
            }
        }

        // Go's fmtInteger for base 10.
        private void FormatInteger(bool negative, BigInteger magnitude)
        {
            var precision = 0;
            if (precPresent)
            {
                precision = prec;
                // Precision of 0 and value of 0 means "print nothing" but padding.
                if (precision == 0 && magnitude.IsZero)
                {
                    var oldZero = zero;
                    zero = false;
                    WritePadding(wid);
                    zero = oldZero;
                    return;
                }
            }
            else if (zero && !minus && widPresent)
            {
                precision = wid;
                if (negative || plus || space)
                {
                    precision--; // leave room for sign
                }
            }
            var digits = magnitude.ToString(CultureInfo.InvariantCulture);
            var sb = new StringBuilder(Math.Max(digits.Length, precision) + 1);
            if (negative)
            {
                sb.Append('-');
            }
            else if (plus)
            {
                sb.Append('+');
            }
            else if (space)
            {
                sb.Append(' ');
            }
            if (precision > digits.Length)
            {
                sb.Append('0', precision - digits.Length);
            }
            sb.Append(digits);
            var keepZero = zero;
            zero = false;
            PadString(sb.ToString());
            zero = keepZero;
        }

        // Go's fmtFloat for %f and %v.
        private void FormatFloat(double value, bool single, int verb)
        {
            string num;
            if (verb == 'f')
            {
                num = FormatFixed(value, precPresent ? prec : 6);
            }
            else if (verb == 'v')
            {
                num = FormatShortest(value, single);
            }
            else
            {
                BadVerb(verb);
                return;
            }
            // A leading space instead of a "+" sign unless plus is set.
            if (space && num[0] == '+' && !plus)
            {
                num = " " + num[1..];
            }
            // Infinities and NaN are not padded with zeros.
            if (num[1] is 'I' or 'N')
            {
                var oldZero = zero;
                zero = false;
                if (num[1] == 'N' && !space && !plus)
                {
                    num = num[1..];
                }
                PadString(num);
                zero = oldZero;
                return;
            }
            // The sharp flag forces a decimal point.
            if (sharp && verb == 'f' && !num.Contains('.', StringComparison.Ordinal))
            {
                num += ".";
            }
            if (plus || num[0] != '+')
            {
                // Zero padding goes between the sign and the digits.
                if (zero && !minus && widPresent && wid > num.Length)
                {
                    buf.Append(num[0]);
                    WritePadding(wid - num.Length);
                    buf.Append(num, 1, num.Length - 1);
                    return;
                }
                PadString(num);
                return;
            }
            PadString(num[1..]);
        }

        private void BadVerb(int verb)
        {
            buf.Append("%!").Append(RuneText(verb)).Append('(');
            if (arg is not null)
            {
                buf.Append(TypeName(arg)).Append('=');
                PrintArg(arg, 'v');
            }
            else
            {
                buf.Append("<nil>");
            }
            buf.Append(')');
        }

        private void BadArgNum(int verb) => buf.Append("%!").Append(RuneText(verb)).Append("(BADINDEX)");

        private void MissingArg(int verb) => buf.Append("%!").Append(RuneText(verb)).Append("(MISSING)");

        // Go's padString: width counts code points, zeros only on the left.
        private void PadString(string s)
        {
            if (!widPresent || wid == 0)
            {
                buf.Append(s);
                return;
            }
            var width = wid - RuneCount(s);
            if (!minus)
            {
                WritePadding(width);
                buf.Append(s);
            }
            else
            {
                buf.Append(s);
                WritePadding(width);
            }
        }

        private void WritePadding(int n)
        {
            if (n <= 0)
            {
                return;
            }
            buf.Append(zero && !minus ? '0' : ' ', n);
        }
    }
}
