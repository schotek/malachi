// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only (docs/windows-port.md §6.6): what the previewer does with the
// bytes message.part returned. Sushi and Quick Look render by the detected
// type and never run anything; so does this, with Chromium's decoders only:
//
// - a picture is judged by its bytes (the signatures of the formats
//   Chromium decodes), never by the claim alone, and served as the type its
//   bytes are; SVG, a document that can script, is never a picture;
// - a PDF is judged by its signature (%PDF- within the first KiB, where
//   readers look for it);
// - text is shown as text in the previewer's own document, escaped: HTML,
//   SVG, XML and messages (.eml) as their source, so nothing of them is
//   ever parsed as markup; the charset the sender claimed is honoured when
//   it is known, a byte-order mark wins, and bytes with a NUL are not text;
// - what the platform would run (DangerousTypes: programs, scripts,
//   shortcuts, disk images) gets the panel only, whatever its bytes are.
//
// Nothing here writes to disk: the view serves the bytes from memory.

using System;
using System.Collections.Frozen;
using System.Text;
using Malachi.Core.Html;
using Malachi.Core.Platform;

namespace Malachi.Core.Presentation;

/// <summary>How the previewer shows one attachment.</summary>
public sealed record PreviewContent
{
    private const int PdfSignatureWindow = 1024;

    private static readonly FrozenSet<string> TextTypes = FrozenSet.Create(
        StringComparer.Ordinal,
        "application/json", "application/ld+json", "application/xml", "application/xhtml+xml", "application/rss+xml",
        "application/atom+xml", "application/yaml", "application/x-yaml", "application/toml", "application/x-pem-file",
        "application/pgp-keys", "application/pgp-signature", "message/rfc822", "message/global", "image/svg+xml");

    private static readonly FrozenSet<string> TextExtensions = FrozenSet.Create(
        StringComparer.OrdinalIgnoreCase,
        "txt", "text", "log", "csv", "tsv", "md", "markdown", "rst", "json", "xml", "html", "htm", "xhtml", "svg", "eml",
        "ics", "vcf", "ini", "cfg", "conf", "yaml", "yml", "toml", "diff", "patch", "asc", "pem", "tex", "srt", "vtt",
        "sql", "properties");

    private static readonly FrozenSet<string> UntypedTypes = FrozenSet.Create(
        StringComparer.Ordinal, "", "application/octet-stream", "application/binary", "binary/octet-stream");

    /// <summary>A nothing-to-render answer.</summary>
    public static PreviewContent Nothing { get; } = new() { Kind = PreviewKind.None };

    /// <summary>How the attachment is shown.</summary>
    public required PreviewKind Kind { get; init; }

    /// <summary>For <see cref="PreviewKind.Image"/> and <see cref="PreviewKind.Pdf"/>: the type the bytes are served as.</summary>
    public string MediaType { get; init => field = value ?? ""; } = "";

    /// <summary>For <see cref="PreviewKind.Text"/>: the decoded text (for <see cref="PreviewDocument.Text"/>).</summary>
    public string Text { get; init => field = value ?? ""; } = "";

    /// <summary>
    /// The preview of <paramref name="data"/>, an attachment named
    /// <paramref name="fileName"/> that its sender called
    /// <paramref name="contentType"/>.
    /// </summary>
    public static PreviewContent Classify(string? fileName, string? contentType, ReadOnlySpan<byte> data)
    {
        if (data.IsEmpty || DangerousTypes.IsDangerous(fileName, contentType))
        {
            return Nothing;
        }
        var type = PartPath.BareMediaType(contentType ?? "");
        var extension = Extension(fileName);
        var svg = type == "image/svg+xml" || extension.Equals("svg", StringComparison.OrdinalIgnoreCase)
            || extension.Equals("svgz", StringComparison.OrdinalIgnoreCase);
        if (!svg && SniffImage(data) is { } image)
        {
            return new PreviewContent { Kind = PreviewKind.Image, MediaType = image };
        }
        if (IsPdf(data))
        {
            return new PreviewContent { Kind = PreviewKind.Pdf, MediaType = "application/pdf" };
        }
        var textual = svg || type.StartsWith("text/", StringComparison.Ordinal) || TextTypes.Contains(type)
            || TextExtensions.Contains(extension)
            || (UntypedTypes.Contains(type) && extension.Length == 0);
        if (textual && DecodeText(data, contentType) is { } text)
        {
            return new PreviewContent { Kind = PreviewKind.Text, Text = text };
        }
        return Nothing;
    }

    /// <summary>
    /// The picture type <paramref name="data"/> starts like (PNG, JPEG, GIF,
    /// WebP, BMP, ICO and cursors, AVIF), or null.
    /// </summary>
    public static string? SniffImage(ReadOnlySpan<byte> data)
    {
        if (data.StartsWith((ReadOnlySpan<byte>)[0x89, (byte)'P', (byte)'N', (byte)'G', 0x0D, 0x0A, 0x1A, 0x0A]))
        {
            return "image/png";
        }
        if (data.StartsWith((ReadOnlySpan<byte>)[0xFF, 0xD8, 0xFF]))
        {
            return "image/jpeg";
        }
        if (data.StartsWith("GIF87a"u8) || data.StartsWith("GIF89a"u8))
        {
            return "image/gif";
        }
        if (data.Length >= 12 && data.StartsWith("RIFF"u8) && data[8..12].SequenceEqual("WEBP"u8))
        {
            return "image/webp";
        }
        if (data.Length >= 14 && data.StartsWith("BM"u8))
        {
            return "image/bmp";
        }
        if (data.Length >= 6 && data[0] == 0 && data[1] == 0 && (data[2] == 1 || data[2] == 2) && data[3] == 0
            && (data[4] != 0 || data[5] != 0))
        {
            return "image/x-icon";
        }
        if (data.Length >= 12 && data[4..8].SequenceEqual("ftyp"u8)
            && (data[8..12].SequenceEqual("avif"u8) || data[8..12].SequenceEqual("avis"u8)))
        {
            return "image/avif";
        }
        return null;
    }

    /// <summary>Whether <paramref name="data"/> carries a PDF signature within its first KiB.</summary>
    public static bool IsPdf(ReadOnlySpan<byte> data) =>
        data[..Math.Min(data.Length, PdfSignatureWindow)].IndexOf("%PDF-"u8) >= 0;

    /// <summary>
    /// <paramref name="data"/> as text, or null when it is not text (a NUL
    /// once decoded). A byte-order mark decides; else the charset of
    /// <paramref name="contentType"/> when this system knows it; else UTF-8
    /// when the bytes are valid UTF-8, Windows-1252 otherwise. Invalid
    /// sequences become U+FFFD.
    /// </summary>
    public static string? DecodeText(ReadOnlySpan<byte> data, string? contentType)
    {
        string text;
        if (data.StartsWith((ReadOnlySpan<byte>)[0xEF, 0xBB, 0xBF]))
        {
            text = Replacing(Encoding.UTF8).GetString(data[3..]);
        }
        else if (data.StartsWith((ReadOnlySpan<byte>)[0xFF, 0xFE]))
        {
            text = Replacing(Encoding.Unicode).GetString(data[2..]);
        }
        else if (data.StartsWith((ReadOnlySpan<byte>)[0xFE, 0xFF]))
        {
            text = Replacing(Encoding.BigEndianUnicode).GetString(data[2..]);
        }
        else if (Charset(contentType) is { } declared)
        {
            text = declared.GetString(data);
        }
        else if (IsValidUtf8(data))
        {
            text = Encoding.UTF8.GetString(data);
        }
        else
        {
            text = (Named("windows-1252") ?? Encoding.Latin1).GetString(data);
        }
        return text.Contains('\0', StringComparison.Ordinal) ? null : text;
    }

    // The encoding the charset parameter of contentType names, with
    // replacement fallbacks; null when there is none or it is unknown here.
    private static Encoding? Charset(string? contentType)
    {
        if (string.IsNullOrEmpty(contentType))
        {
            return null;
        }
        foreach (var parameter in contentType.Split(';'))
        {
            var p = parameter.Trim();
            if (!p.StartsWith("charset=", StringComparison.OrdinalIgnoreCase))
            {
                continue;
            }
            var name = p["charset=".Length..].Trim().Trim('"', '\'').Trim();
            return name.Length == 0 ? null : Named(name);
        }
        return null;
    }

    // The encoding called name, built in or a Windows code page, with
    // replacement fallbacks; null when this system does not know it.
    private static Encoding? Named(string name)
    {
        try
        {
            return Encoding.GetEncoding(name, EncoderFallback.ReplacementFallback, DecoderFallback.ReplacementFallback);
        }
        catch (Exception e) when (e is ArgumentException or NotSupportedException)
        {
        }
        try
        {
            return CodePagesEncodingProvider.Instance.GetEncoding(name, EncoderFallback.ReplacementFallback, DecoderFallback.ReplacementFallback);
        }
        catch (Exception e) when (e is ArgumentException or NotSupportedException)
        {
            return null;
        }
    }

    private static Encoding Replacing(Encoding encoding) =>
        Encoding.GetEncoding(encoding.CodePage, EncoderFallback.ReplacementFallback, DecoderFallback.ReplacementFallback);

    private static bool IsValidUtf8(ReadOnlySpan<byte> data) => System.Text.Unicode.Utf8.IsValid(data);

    // The extension of the attachment's name, without its dot; "" for none.
    private static string Extension(string? fileName)
    {
        var name = (fileName ?? "").Trim();
        var dot = name.LastIndexOf('.');
        return dot < 0 || dot == name.Length - 1 ? "" : name[(dot + 1)..];
    }
}
