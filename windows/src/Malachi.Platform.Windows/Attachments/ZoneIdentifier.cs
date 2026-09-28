// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the Mark of the Web itself, the Zone.Identifier alternate
// data stream, as Attachment Services writes it; the counterpart of the
// read-back of the quarantine attribute in macos/Sources/MalachiMail/
// Attachments/AttachmentActions.swift (quarantine).

using System;
using System.Globalization;
using System.IO;
using System.Text;

namespace Malachi.Platform.Windows.Attachments;

/// <summary>
/// Reads and writes a file's <c>Zone.Identifier</c> stream:
/// <c>[ZoneTransfer]</c> with <c>ZoneId=</c> 3 (Internet) or 4 (Restricted
/// sites), which SmartScreen, Office and the Attachment Manager read.
/// </summary>
public static class ZoneIdentifier
{
    /// <summary>The name of the alternate data stream.</summary>
    public const string StreamName = "Zone.Identifier";

    /// <summary>The zone of the internet.</summary>
    public const int Internet = 3;

    /// <summary>The zone of restricted sites, the default of mail.</summary>
    public const int Restricted = 4;

    // The stream is a few lines; anything longer is not one we wrote.
    private const int MaxStreamBytes = 64 * 1024;

    /// <summary>
    /// The zone <paramref name="path"/>'s stream names, null when it has
    /// none (no stream, no <c>ZoneId</c>, a file system without streams, a
    /// file that is gone).
    /// </summary>
    public static int? Read(string path)
    {
        ArgumentException.ThrowIfNullOrEmpty(path);
        try
        {
            using var stream = new FileStream(StreamPath(path), FileMode.Open, FileAccess.Read, FileShare.ReadWrite | FileShare.Delete);
            if (stream.Length > MaxStreamBytes)
            {
                return null;
            }
            using var reader = new StreamReader(stream, Encoding.UTF8, detectEncodingFromByteOrderMarks: true);
            return Parse(reader.ReadToEnd());
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException or NotSupportedException)
        {
            return null;
        }
    }

    /// <summary>
    /// Writes the stream directly, as Attachment Services would for
    /// <paramref name="zoneId"/> (and <paramref name="hostUrl"/>, the
    /// source it was told, if any), replacing one that was there.
    /// </summary>
    public static void Write(string path, int zoneId, string? hostUrl = null)
    {
        ArgumentException.ThrowIfNullOrEmpty(path);
        var text = new StringBuilder("[ZoneTransfer]\r\n")
            .Append("ZoneId=").Append(zoneId.ToString(CultureInfo.InvariantCulture)).Append("\r\n");
        if (!string.IsNullOrEmpty(hostUrl))
        {
            text.Append("HostUrl=").Append(hostUrl).Append("\r\n");
        }
        File.WriteAllText(StreamPath(path), text.ToString(), Encoding.ASCII);
    }

    /// <summary>
    /// The <c>ZoneId</c> of a stream's text: the first one in the
    /// <c>[ZoneTransfer]</c> section, keys and section names compared
    /// without regard to case; null without one or when it is not a number.
    /// </summary>
    public static int? Parse(string? text)
    {
        var inSection = false;
        foreach (var raw in (text ?? "").Split('\n'))
        {
            var line = raw.Trim().TrimStart('﻿');
            if (line.StartsWith('['))
            {
                inSection = string.Equals(line, "[ZoneTransfer]", StringComparison.OrdinalIgnoreCase);
                continue;
            }
            if (!inSection)
            {
                continue;
            }
            var equals = line.IndexOf('=', StringComparison.Ordinal);
            if (equals <= 0 || !string.Equals(line[..equals].Trim(), "ZoneId", StringComparison.OrdinalIgnoreCase))
            {
                continue;
            }
            return int.TryParse(line[(equals + 1)..].Trim(), NumberStyles.Integer, CultureInfo.InvariantCulture, out var zone)
                ? zone
                : null;
        }
        return null;
    }

    private static string StreamPath(string path) => path + ":" + StreamName;
}
