// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiKeychain/main.swift; GTK: none (on Linux the
// daemon keeps secrets in the Secret Service itself). malachi-credentials,
// the keyring helper malachid runs on Windows (MALACHI_KEYRING=helper,
// backend/internal/auth/helper). One operation per process:
// `malachi-credentials get|set|delete`, one JSON line on stdin, the answer
// on stdout, the outcome in the exit status. The value reaches stdout only
// as the answer to get; stderr carries a diagnostic in fixed words, never
// data. It is a GUI (WinExe) program so that no console window appears,
// whatever started the daemon; the pipes the daemon passes are its standard
// handles all the same, read and written as bytes (UTF-8, no BOM).

using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Security.Cryptography;
using System.Text;

namespace Malachi.Credentials;

/// <summary>malachi-credentials get|set|delete.</summary>
internal static class Program
{
    /// <summary>The longest diagnostic written to stderr, in characters.</summary>
    internal const int MaxDiagnostic = 1024;

    private const string Name = "malachi-credentials";

    internal static int Main(string[] args)
    {
        using var input = Console.OpenStandardInput();
        using var output = Console.OpenStandardOutput();
        using var error = Console.OpenStandardError();
        return (int)Run(args, input, output, error, new CredentialStore(new Win32CredentialManager()));
    }

    /// <summary>
    /// One run of the helper over the given streams and store: what
    /// <see cref="Main"/> does, and what the tests drive.
    /// </summary>
    internal static HelperExit Run(IReadOnlyList<string> args, Stream input, Stream output, Stream error, CredentialStore store)
    {
        try
        {
            if (args.Count != 1 || Operation.FromRawValue(args[0]) is not { } operation)
            {
                return Fail(error, HelperExit.BadRequest, $"usage: {Name} get|set|delete (one JSON line on stdin)");
            }
            return Serve(operation, input, output, error, store);
        }
#pragma warning disable CA1031 // The last line of defence: the runtime's report of an exception could quote data.
        catch (Exception e)
#pragma warning restore CA1031
        {
            return Fail(error, HelperExit.Failure, $"internal error ({e.GetType().Name})");
        }
    }

    private static HelperExit Serve(Operation operation, Stream input, Stream output, Stream error, CredentialStore store)
    {
        // One byte more than a request may have tells a request that is too
        // long from one that fits exactly.
        var buffer = new byte[Request.MaxInput + 1];
        Request? request = null;
        try
        {
            var length = ReadInput(input, buffer);
            if (length > Request.MaxInput)
            {
                return Fail(error, HelperExit.BadRequest, string.Create(CultureInfo.InvariantCulture, $"request exceeds {Request.MaxInput} bytes"));
            }
            if (!Request.TryParse(operation.RawValue, buffer.AsSpan(0, length), out request))
            {
                return Fail(error, HelperExit.BadRequest, $"malformed {operation.RawValue} request");
            }
            // The request holds its own copy of the value now.
            CryptographicOperations.ZeroMemory(buffer.AsSpan(0, length));
            return Perform(operation, request, output, error, store);
        }
        finally
        {
            // All of it: a read that failed half-way leaves no count behind.
            CryptographicOperations.ZeroMemory(buffer);
            request?.ZeroValue();
        }
    }

    private static HelperExit Perform(Operation operation, Request request, Stream output, Stream error, CredentialStore store)
    {
        byte[]? answer = null;
        try
        {
            CredentialFailure? failure;
            switch (operation)
            {
                case Operation.Get:
                    failure = store.Get(request, out var value);
                    if (value is not null)
                    {
                        try
                        {
                            answer = Request.ValueLine(value);
                        }
                        finally
                        {
                            CryptographicOperations.ZeroMemory(value);
                        }
                    }
                    break;
                case Operation.Set:
                    // TryParse guarantees a value for set.
                    failure = store.Set(request, request.Value);
                    answer = failure is null ? Done() : null;
                    break;
                default:
                    failure = store.Delete(request);
                    answer = failure is null ? Done() : null;
                    break;
            }
            if (failure is not null)
            {
                return failure.Kind == CredentialFailureKind.NotFound
                    ? HelperExit.NotFound
                    : Fail(error, HelperExit.Failure, $"{operation.RawValue}: {failure.Message}");
            }
            try
            {
                output.Write(answer);
                output.Flush();
            }
            catch (IOException)
            {
                // The daemon stopped listening (its timeout); nobody reads a word.
                return HelperExit.Failure;
            }
            return HelperExit.Ok;
        }
        finally
        {
            if (answer is not null)
            {
                CryptographicOperations.ZeroMemory(answer);
            }
        }
    }

    // The answer to set and delete: an empty object on one line, as on macOS.
    private static byte[] Done() => "{}\n"u8.ToArray();

    // Reads stdin to its end or until the buffer is full; the count read.
    private static int ReadInput(Stream input, byte[] buffer)
    {
        var length = 0;
        while (length < buffer.Length)
        {
            var read = input.Read(buffer, length, buffer.Length - length);
            if (read == 0)
            {
                break;
            }
            length += read;
        }
        return length;
    }

    private static HelperExit Fail(Stream error, HelperExit exit, string message)
    {
        if (message.Length > MaxDiagnostic)
        {
            message = message[..MaxDiagnostic];
        }
        try
        {
            error.Write(Encoding.UTF8.GetBytes($"{Name}: {message}\n"));
            error.Flush();
        }
        catch (IOException)
        {
            // Nobody reads stderr either; the status still tells.
        }
        return exit;
    }
}
