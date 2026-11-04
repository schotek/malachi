// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Go: backend/pkg/api/update.go; contract: docs/api.md §4.20, §5
// (notify.updateChanged); design: docs/plans/update-check.md, the service
// docs/services.md §2. Written from the Go types beside the Swift mirror,
// which was written at the same time.
//
// The update check: the daemon asks the project's signed manifest which
// versions exist and offers the newest one for the package the build was
// installed from. It never downloads or installs anything: the client
// opens the offered package's URL in the browser. Every string is plain
// text the daemon checked; the notes and a notice's text are still shown
// as plain text only (TextBlock.Text).

using System;
using System.Text.Json.Serialization;

namespace Malachi.Core.Api;

/// <summary>api.UpdateState: what the update check shows.</summary>
[JsonConverter(typeof(StringWireValueConverter<UpdateState>))]
public readonly record struct UpdateState(string Value) : IWireEnumeration<UpdateState>
{
    /// <summary>The user turned the check off.</summary>
    public const string Disabled = "disabled";

    /// <summary>The build has no channel, or its version is not a plain MAJOR.MINOR.PATCH.</summary>
    public const string Unsupported = "unsupported";

    /// <summary>No check has finished yet.</summary>
    public const string Idle = "idle";

    /// <summary>A check is running.</summary>
    public const string Checking = "checking";

    /// <summary>The last check found nothing newer.</summary>
    public const string UpToDate = "upToDate";

    /// <summary>A newer version is offered (<see cref="UpdateStatus.Available"/>).</summary>
    public const string Available = "available";

    /// <summary>The last check failed and nothing is offered.</summary>
    public const string Failed = "failed";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator UpdateState(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.UpdateFailure: the class of a failed check.</summary>
[JsonConverter(typeof(StringWireValueConverter<UpdateFailure>))]
public readonly record struct UpdateFailure(string Value) : IWireEnumeration<UpdateFailure>
{
    /// <summary>api.UpdateFailNetwork.</summary>
    public const string Network = "network";

    /// <summary>api.UpdateFailServer.</summary>
    public const string Server = "server";

    /// <summary>api.UpdateFailSignature.</summary>
    public const string Signature = "signature";

    /// <summary>api.UpdateFailFormat.</summary>
    public const string Format = "format";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator UpdateFailure(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.UpdateDismissAction: what <c>update.dismiss</c> does with an offered version.</summary>
[JsonConverter(typeof(StringWireValueConverter<UpdateDismissAction>))]
public readonly record struct UpdateDismissAction(string Value) : IWireEnumeration<UpdateDismissAction>
{
    /// <summary>api.UpdateSkip: never offer this version again (not a security release).</summary>
    public const string Skip = "skip";

    /// <summary>api.UpdateLater: offer it again after three days (not a security release).</summary>
    public const string Later = "later";

    /// <summary>api.UpdatePrompted: a client asked about this security release; no client asks again.</summary>
    public const string Prompted = "prompted";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator UpdateDismissAction(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.UpdateInfo: the version offered.</summary>
public sealed record UpdateInfo
{
    /// <summary>The version, e.g. 0.2.1.</summary>
    [JsonPropertyName("version")]
    public required string Version { get; init; }

    /// <summary>When it was released.</summary>
    [JsonPropertyName("publishedAt")]
    public DateTimeOffset PublishedAt { get; init; }

    /// <summary>
    /// This release, or one it skips over, fixes a security problem: it is
    /// never skipped or put off, and the banner stays.
    /// </summary>
    [JsonPropertyName("security")]
    public bool Security { get; init; }

    /// <summary>The release notes in the client's language, else English; plain text.</summary>
    [JsonPropertyName("notes")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Notes { get; init => field = value ?? ""; } = "";

    /// <summary>The release's page (https on the project's hosts).</summary>
    [JsonPropertyName("infoUrl")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string InfoUrl { get; init => field = value ?? ""; } = "";

    /// <summary>The package for the build's channel (https on the project's hosts).</summary>
    [JsonPropertyName("url")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Url { get; init => field = value ?? ""; } = "";

    /// <summary>The package's size in bytes.</summary>
    [JsonPropertyName("size")]
    public long Size { get; init; }

    /// <summary>The user skipped this version (never with <see cref="Security"/>).</summary>
    [JsonPropertyName("skipped")]
    public bool Skipped { get; init; }

    /// <summary>The user asked to be reminded later; null once it passed.</summary>
    [JsonPropertyName("remindAfter")]
    public DateTimeOffset? RemindAfter { get; init; }

    /// <summary>
    /// A security release no client has asked about yet: the client asks
    /// once and calls <c>update.dismiss</c> with "prompted".
    /// </summary>
    [JsonPropertyName("prompt")]
    public bool Prompt { get; init; }
}

/// <summary>api.UpdateNotice: a message of the project to the users of this version, shown once per id.</summary>
public sealed record UpdateNotice
{
    /// <summary>The notice's id.</summary>
    [JsonPropertyName("id")]
    public required string Id { get; init; }

    /// <summary>The text, plain, in the client's language else English.</summary>
    [JsonPropertyName("text")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Text { get; init => field = value ?? ""; } = "";

    /// <summary>A page with more; null when none.</summary>
    [JsonPropertyName("url")]
    public string? Url { get; init; }
}

/// <summary>api.UpdateStatus: the whole state a client shows.</summary>
public sealed record UpdateStatus
{
    /// <summary>The user's preference (the daemon's default is true).</summary>
    [JsonPropertyName("enabled")]
    public bool Enabled { get; init; }

    /// <summary>The package the build was installed from; null for a build without one.</summary>
    [JsonPropertyName("channel")]
    public string? Channel { get; init; }

    /// <summary>The running version.</summary>
    [JsonPropertyName("current")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Current { get; init => field = value ?? ""; } = "";

    /// <summary>What the check shows.</summary>
    [JsonPropertyName("state")]
    public UpdateState State { get; init; } = UpdateState.Idle;

    /// <summary>When a check last brought an answer.</summary>
    [JsonPropertyName("checkedAt")]
    public DateTimeOffset? CheckedAt { get; init; }

    /// <summary>The class of the last check when it failed.</summary>
    [JsonPropertyName("failure")]
    public UpdateFailure? Failure { get; init; }

    /// <summary>The version offered.</summary>
    [JsonPropertyName("available")]
    public UpdateInfo? Available { get; init; }

    /// <summary>A notice not yet dismissed.</summary>
    [JsonPropertyName("notice")]
    public UpdateNotice? Notice { get; init; }
}

/// <summary>api.UpdateStatusParams: the client's locale for the texts of the status ("cs", "cs-CZ"); null: English.</summary>
public sealed record UpdateStatusParams
{
    /// <summary>The locale.</summary>
    [JsonPropertyName("locale")]
    public string? Locale { get; init; }
}

/// <summary>api.UpdateCheckParams.</summary>
public sealed record UpdateCheckParams
{
    /// <summary>The locale.</summary>
    [JsonPropertyName("locale")]
    public string? Locale { get; init; }
}

/// <summary>api.UpdateSetPrefsParams: the fields it carries change.</summary>
public sealed record UpdateSetPrefsParams
{
    /// <summary>Check automatically.</summary>
    [JsonPropertyName("enabled")]
    public bool? Enabled { get; init; }

    /// <summary>The locale.</summary>
    [JsonPropertyName("locale")]
    public string? Locale { get; init; }
}

/// <summary>api.UpdateDismissParams.</summary>
public sealed record UpdateDismissParams
{
    /// <summary>The offered version.</summary>
    [JsonPropertyName("version")]
    public required string Version { get; init; }

    /// <summary>skip, later or prompted.</summary>
    [JsonPropertyName("action")]
    public required UpdateDismissAction Action { get; init; }

    /// <summary>The locale.</summary>
    [JsonPropertyName("locale")]
    public string? Locale { get; init; }
}

/// <summary>api.UpdateDismissNoticeParams.</summary>
public sealed record UpdateDismissNoticeParams
{
    /// <summary>The notice's id.</summary>
    [JsonPropertyName("id")]
    public required string Id { get; init; }

    /// <summary>The locale.</summary>
    [JsonPropertyName("locale")]
    public string? Locale { get; init; }
}
