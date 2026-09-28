// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The HTML parts of backend/testdata/mime/*.eml, raw: decoded from their
// transfer encoding and charset but never sanitised, so the canary proves
// that layer 2 holds on its own (docs/windows-port.md §12). The corpus is
// hostile on purpose (broken boundaries, bad charsets, invalid base64, deep
// nesting), so this walker is lenient: whatever it cannot decode it passes
// on as it is, and a file it cannot read at all yields nothing. It is test
// infrastructure, not a MIME parser of the client (the daemon parses mail).

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text;

namespace Malachi.App.Canary;

/// <summary>The HTML parts of the MIME test corpus.</summary>
internal static class MimeCorpus
{
    private const int MaxDepth = 12;

    /// <summary>Every HTML part of every <c>.eml</c> in <paramref name="directory"/>, with its file's name.</summary>
    public static IReadOnlyList<(string File, string Html)> HtmlParts(string directory)
    {
        var parts = new List<(string, string)>();
        foreach (var path in Directory.EnumerateFiles(directory, "*.eml").Order(StringComparer.Ordinal))
        {
            var name = Path.GetFileName(path);
            byte[] bytes;
            try
            {
                bytes = File.ReadAllBytes(path);
            }
            catch (IOException)
            {
                continue;
            }
            foreach (var html in Walk(Latin1(bytes), 0))
            {
                parts.Add((name, html));
            }
        }
        return parts;
    }

    // The entity's HTML parts: itself when it is text/html, its parts'
    // when it is a multipart, the enclosed message's when it is one.
    private static IEnumerable<string> Walk(string entity, int depth)
    {
        if (depth > MaxDepth)
        {
            yield break;
        }
        var (headers, body) = Split(entity);
        var contentType = Header(headers, "content-type") ?? "text/plain";
        var type = contentType.Split(';')[0].Trim().ToLowerInvariant();
        if (type.StartsWith("multipart/", StringComparison.Ordinal))
        {
            var boundary = Parameter(contentType, "boundary");
            if (boundary is null)
            {
                yield break;
            }
            foreach (var part in Parts(body, boundary))
            {
                foreach (var html in Walk(part, depth + 1))
                {
                    yield return html;
                }
            }
        }
        else if (type == "message/rfc822")
        {
            foreach (var html in Walk(body, depth + 1))
            {
                yield return html;
            }
        }
        else if (type == "text/html")
        {
            var encoding = (Header(headers, "content-transfer-encoding") ?? "").Trim().ToLowerInvariant();
            var bytes = encoding switch
            {
                "base64" => Base64(body),
                "quoted-printable" => QuotedPrintable(body),
                _ => Encoding.Latin1.GetBytes(body),
            };
            yield return Charset(Parameter(contentType, "charset")).GetString(bytes);
        }
    }

    // Headers (unfolded) and body of an entity; a missing blank line leaves
    // everything to the headers.
    private static (List<(string Name, string Value)> Headers, string Body) Split(string entity)
    {
        var text = entity.Replace("\r\n", "\n", StringComparison.Ordinal);
        var end = text.IndexOf("\n\n", StringComparison.Ordinal);
        var head = end < 0 ? text : text[..end];
        var body = end < 0 ? "" : text[(end + 2)..];
        var headers = new List<(string, string)>();
        foreach (var line in head.Split('\n'))
        {
            if (line.Length > 0 && (line[0] == ' ' || line[0] == '\t') && headers.Count > 0)
            {
                var (n, v) = headers[^1];
                headers[^1] = (n, v + " " + line.Trim());
                continue;
            }
            var colon = line.IndexOf(':', StringComparison.Ordinal);
            if (colon > 0)
            {
                headers.Add((line[..colon].Trim().ToLowerInvariant(), line[(colon + 1)..].Trim()));
            }
        }
        return (headers, body);
    }

    private static string? Header(List<(string Name, string Value)> headers, string name) =>
        headers.FirstOrDefault(h => h.Name == name).Value;

    private static string? Parameter(string header, string name)
    {
        foreach (var piece in header.Split(';').Skip(1))
        {
            var eq = piece.IndexOf('=', StringComparison.Ordinal);
            if (eq > 0 && piece[..eq].Trim().Equals(name, StringComparison.OrdinalIgnoreCase))
            {
                return piece[(eq + 1)..].Trim().Trim('"');
            }
        }
        return null;
    }

    // The parts between "--boundary" lines, up to "--boundary--" or the end.
    private static IEnumerable<string> Parts(string body, string boundary)
    {
        var delimiter = "--" + boundary;
        var lines = body.Split('\n');
        StringBuilder? current = null;
        foreach (var line in lines)
        {
            var trimmed = line.TrimEnd();
            if (trimmed == delimiter + "--")
            {
                break;
            }
            if (trimmed == delimiter)
            {
                if (current is not null)
                {
                    yield return current.ToString();
                }
                current = new StringBuilder();
                continue;
            }
            current?.Append(line).Append('\n');
        }
        if (current is not null)
        {
            yield return current.ToString();
        }
    }

    private static byte[] Base64(string body)
    {
        var clean = new string(body.Where(c => char.IsAsciiLetterOrDigit(c) || c is '+' or '/').ToArray());
        clean = clean[..(clean.Length - (clean.Length % 4))];
        try
        {
            return Convert.FromBase64String(clean);
        }
        catch (FormatException)
        {
            return Encoding.Latin1.GetBytes(body);
        }
    }

    private static byte[] QuotedPrintable(string body)
    {
        var output = new List<byte>(body.Length);
        for (var i = 0; i < body.Length; i++)
        {
            var c = body[i];
            if (c == '=' && i + 1 < body.Length && body[i + 1] == '\n')
            {
                i++;
                continue;
            }
            if (c == '=' && i + 2 < body.Length && Uri.IsHexDigit(body[i + 1]) && Uri.IsHexDigit(body[i + 2]))
            {
                output.Add(Convert.ToByte(body.Substring(i + 1, 2), 16));
                i += 2;
                continue;
            }
            output.Add((byte)c);
        }
        return [.. output];
    }

    private static Encoding Charset(string? name)
    {
        if (!string.IsNullOrWhiteSpace(name))
        {
            try
            {
                return Encoding.GetEncoding(name.Trim());
            }
            catch (ArgumentException)
            {
                if (CodePagesEncodingProvider.Instance.GetEncoding(name.Trim()) is { } legacy)
                {
                    return legacy;
                }
            }
        }
        return Encoding.UTF8;
    }

    // Bytes as characters one to one, so the transfer decoding sees the raw
    // octets of an 8-bit body.
    private static string Latin1(byte[] bytes) => Encoding.Latin1.GetString(bytes);
}
