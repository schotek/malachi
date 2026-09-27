// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiKeychain/Keychain.swift (KeychainFailure):
// the store's failures and the words stderr gives them. A message is built
// from fixed text, the name of the Win32 function, the error number and the
// system's text for it; never from the request or the value.

using System.Globalization;
using System.Runtime.InteropServices;

namespace Malachi.Credentials;

/// <summary>A failure of <see cref="CredentialStore"/>.</summary>
internal sealed record CredentialFailure
{
    private CredentialFailure(CredentialFailureKind kind, string message, int error = 0)
    {
        Kind = kind;
        Message = message;
        Error = error;
    }

    /// <summary>No such item; the helper exits with <see cref="HelperExit.NotFound"/> and says nothing.</summary>
    public static CredentialFailure NotFound { get; } = new(CredentialFailureKind.NotFound, "item not found");

    /// <summary>A value longer than the store takes.</summary>
    public static CredentialFailure TooLarge { get; } = new(
        CredentialFailureKind.TooLarge,
        string.Create(CultureInfo.InvariantCulture, $"the value is longer than the {ChunkHeader.MaxLength} bytes an item can hold"));

    /// <summary>What went wrong.</summary>
    public CredentialFailureKind Kind { get; }

    /// <summary>The diagnostic for stderr.</summary>
    public string Message { get; }

    /// <summary>The Win32 error of <see cref="CredentialFailureKind.ApiFailed"/>, 0 otherwise.</summary>
    public int Error { get; }

    /// <summary>A Credential Manager function failed with a Win32 error.</summary>
    public static CredentialFailure ApiFailed(string function, int error) => new(
        CredentialFailureKind.ApiFailed,
        string.Create(CultureInfo.InvariantCulture, $"{function} failed: {Marshal.GetPInvokeErrorMessage(error).Trim()} (Win32 error {error})"),
        error);

    /// <summary>An item that cannot be read back; <paramref name="problem"/> is fixed text.</summary>
    public static CredentialFailure Corrupt(string problem) =>
        new(CredentialFailureKind.Corrupt, $"the stored item is corrupt: {problem}");
}
