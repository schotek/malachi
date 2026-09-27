// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The helpers of macos/Tests/MalachiCoreTests that SyncStatusTests and
// AccountsPageTests share: testAccount (MailModelTests.swift), tlsError
// (CertTrustTests.swift), tlsState and statusNow (SyncStatusTests.swift);
// GTK: ui/internal/window/sync_test.go (tlsState, now).

using System;
using System.Text.Json;
using Malachi.Core.Api;

namespace Malachi.Core.Tests.Model;

internal static class SyncStatusFixtures
{
    /// <summary>An account of the tests (MailModelTests.swift <c>testAccount</c>).</summary>
    public static Account TestAccount(string id, bool enabled = true, string name = "", string email = "", SyncState? state = null) =>
        new()
        {
            Id = new AccountId(id),
            Config = new AccountConfig { Name = name, Email = email },
            Enabled = enabled,
            State = state ?? new SyncState { AccountId = new AccountId(id), Status = SyncStatus.Idle },
        };

    /// <summary>A tlsError carrying these details, as the client decodes it (CertTrustTests.swift <c>tlsError</c>).</summary>
    public static RpcError TlsError(TlsErrorData? d) => new()
    {
        Code = ErrorCode.TlsError,
        Message = "x509: certificate signed by unknown authority",
        Data = d is null ? null : JsonSerializer.SerializeToElement(d, ApiJsonContext.Wire.TlsErrorData),
    };

    /// <summary>A tlsError whose details carry only <paramref name="reason"/>.</summary>
    public static RpcError TlsError(string reason) => TlsError(new TlsErrorData { Reason = reason });

    /// <summary>
    /// An account state after a tlsError with the given reason, as the client
    /// decodes it (sync_test.go <c>tlsState</c>).
    /// </summary>
    public static SyncState TlsState(string acc, string status, string reason) =>
        new() { AccountId = new AccountId(acc), Status = status, Error = TlsError(reason) };

    /// <summary>
    /// 2026-09-02 15:30 in the current time zone (sync_test.go and
    /// status_test.go <c>now</c>).
    /// </summary>
    public static DateTimeOffset StatusNow() => At(new DateTime(2026, 9, 2, 15, 30, 0));

    /// <summary>A wall-clock time of the current time zone.</summary>
    public static DateTimeOffset At(DateTime local)
    {
        var zone = TimeZoneInfo.Local;
        return new DateTimeOffset(local, zone.GetUtcOffset(local));
    }
}
