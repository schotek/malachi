// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: hands devmail a raw message over its SMTP submission
// port (plaintext on 127.0.0.1, AUTH PLAIN as the seeded user), for the
// opt-in suite's messages the seeded corpus does not have, such as an
// invitation addressed to the account itself (DevmailTests). The smallest
// SMTP dialogue: EHLO, AUTH PLAIN, MAIL FROM, RCPT TO, DATA with the dots
// of the body's lines doubled, QUIT; any reply that is not the expected
// class fails the test with the server's line.

using System;
using System.IO;
using System.Net;
using System.Net.Sockets;
using System.Text;

namespace Malachi.App.UiTests;

/// <summary>A raw message submitted to devmail.</summary>
internal static class DevmailSmtp
{
    /// <summary>Submits <paramref name="message"/> (CRLF lines) from and to the seeded user.</summary>
    public static void Submit(DevmailServer server, string message)
    {
        ArgumentNullException.ThrowIfNull(server);
        ArgumentNullException.ThrowIfNull(message);
        using var client = new TcpClient();
        client.Connect(IPAddress.Loopback, server.SmtpPort);
        using var stream = client.GetStream();
        stream.ReadTimeout = 30_000;
        stream.WriteTimeout = 30_000;
        using var reader = new StreamReader(stream, Encoding.ASCII, leaveOpen: true);
        using var writer = new StreamWriter(stream, new UTF8Encoding(false), leaveOpen: true) { NewLine = "\r\n", AutoFlush = true };
        Expect(reader, '2');
        writer.WriteLine("EHLO uitests.invalid");
        Expect(reader, '2');
        var plain = Convert.ToBase64String(Encoding.UTF8.GetBytes("\0" + DevmailServer.User + "\0" + DevmailServer.Password));
        writer.WriteLine("AUTH PLAIN " + plain);
        Expect(reader, '2');
        writer.WriteLine("MAIL FROM:<" + DevmailServer.User + ">");
        Expect(reader, '2');
        writer.WriteLine("RCPT TO:<" + DevmailServer.User + ">");
        Expect(reader, '2');
        writer.WriteLine("DATA");
        Expect(reader, '3');
        foreach (var line in message.Replace("\r\n", "\n", StringComparison.Ordinal).Split('\n'))
        {
            writer.WriteLine(line.StartsWith('.') ? "." + line : line);
        }
        writer.WriteLine(".");
        Expect(reader, '2');
        writer.WriteLine("QUIT");
    }

    // One reply (its last line, past the continuations) of the class wanted.
    private static void Expect(StreamReader reader, char wanted)
    {
        string? line;
        do
        {
            line = reader.ReadLine() ?? throw new IOException("devmail closed the SMTP connection");
        }
        while (line.Length > 3 && line[3] == '-');
        if (line.Length == 0 || line[0] != wanted)
        {
            throw new IOException("devmail answered: " + line);
        }
    }
}
