// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows addition to the ported API tests: the tables of the API layer
// against backend/pkg/api and the GTK UI as they are in the tree, read at
// test time (the Swift tests compare with copies, ported in
// ApiCodingTests). A change of the Go contract that the C# layer has not
// followed fails here rather than on the wire.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Reflection;
using Malachi.Core.Api;
using Malachi.Core.Platform;
using Xunit;

namespace Malachi.Core.Tests.Api;

public sealed class GoContractTests
{
    [Fact]
    public void MethodTableIsAllMethodsOfMethodsGo()
    {
        var source = GoContract.ApiSource("methods.go");
        var constants = GoContract.StringConstants(source);
        var goMethods = GoContract.SliceIdentifiers(source, "AllMethods").Select(name => constants[name]).ToArray();
        Assert.Equal(53, goMethods.Length);
        Assert.Equal(goMethods, API.AllMethods);
        Assert.Equal(goMethods, API.Methods.Select(m => m.Name));
        Assert.Equal(ApiCodingTests.GoMethods, goMethods); // the Swift test's copy is current
    }

    [Fact]
    public void NotificationsAreAllNotificationsOfMethodsGo()
    {
        var source = GoContract.ApiSource("methods.go");
        var constants = GoContract.StringConstants(source);
        var goNotifications = GoContract.SliceIdentifiers(source, "AllNotifications").Select(name => constants[name]);
        Assert.Equal(goNotifications, API.AllNotifications);
        Assert.Equal(API.Notify.NewMessage, constants["NotifyNewMessage"]);
        Assert.Equal(API.Notify.SyncState, constants["NotifySyncState"]);
        Assert.Equal(API.Notify.AuthRequired, constants["NotifyAuthRequired"]);
        Assert.Equal(API.Notify.AccountsChanged, constants["NotifyAccountsChanged"]);
        Assert.Equal(API.Notify.MessagesChanged, constants["NotifyMessagesChanged"]);
    }

    [Fact]
    public void ErrorCodesAreThoseOfErrorsGo()
    {
        var go = GoContract.ErrorCodes();
        Assert.Equal(go.Select(c => c.Code), ErrorCode.All.Select(c => c.Value));
        Assert.Equal(go.Select(c => c.Name), ErrorCode.All.Select(c => c.Name));
        Assert.Equal(ApiCodingTests.GoCodes, go); // the Swift test's copy is current
    }

    [Fact]
    public void EveryMethodAndErrorCodeIsDocumented()
    {
        var doc = GoContract.Source("docs", "api.md");
        foreach (var method in API.AllMethods)
        {
            Assert.Contains($"#### `{method}`", doc, StringComparison.Ordinal);
        }
        foreach (var notification in API.AllNotifications)
        {
            Assert.Contains($"| `{notification}` |", doc, StringComparison.Ordinal);
        }
        foreach (var code in ErrorCode.All)
        {
            Assert.Contains($"| {code.Value} | {code.Name} |", doc, StringComparison.Ordinal);
        }
    }

    public static TheoryData<string, string, string> WireEnums => new()
    {
        { nameof(Security), "types.go", "Security" },
        { nameof(AuthMethod), "types.go", "AuthMethod" },
        { nameof(OAuth2Source), "types.go", "OAuth2Source" },
        { nameof(OAuth2Provider), "types.go", "OAuth2Provider*" },
        { nameof(AccountKind), "types.go", "AccountKind" },
        { nameof(GraphSource), "types.go", "GraphSource" },
        { nameof(DiscoverSource), "types.go", "DiscoverSource" },
        { nameof(OAuthSessionStatus), "types.go", "OAuthSessionStatus" },
        { nameof(FolderRole), "types.go", "FolderRole" },
        { nameof(Flag), "types.go", "Flag" },
        { nameof(OutboxState), "types.go", "OutboxState" },
        { nameof(SortOrder), "types.go", "SortOrder" },
        { nameof(MessageFilter), "types.go", "MessageFilter" },
        { nameof(RemoteContentPolicy), "types.go", "RemoteContentPolicy" },
        { nameof(BodyState), "types.go", "BodyState" },
        { nameof(ComposeMode), "types.go", "ComposeMode" },
        { nameof(QuoteForm), "types.go", "QuoteForm" },
        { nameof(SyncStatus), "types.go", "SyncStatus" },
        { nameof(KnownSenderSource), "types.go", "KnownSenderSource*" },
        { nameof(ContactSource), "types.go", "ContactSource" },
        { nameof(StorageConversion), "types.go", "StorageConversion" },
        { nameof(Capability), "types.go", "AccountCapability" },
        { nameof(JiraDeployment), "types.go", "JiraDeployment" },
        { nameof(VirtualFolder), "types.go", "VirtualFolder" },
        { nameof(NotificationMailMode), "types.go", "NotificationMailMode" },
        { nameof(IssueStatusCategory), "types.go", "IssueStatusCategory" },
        { nameof(IssueItemKind), "types.go", "IssueItemKind" },
        { nameof(CommentVisibility), "types.go", "CommentVisibility" },
        { nameof(IssueField), "types.go", "IssueField" },
        { nameof(TlsErrorReason), "tls.go", "TLSErrorReason" },
    };

    /// <summary>
    /// Every value of a Go string type (or, for the untyped constants
    /// OAuth2Provider* and KnownSenderSource*, of the constants with that
    /// prefix) is a constant of the C# wire enum, and nothing else is.
    /// </summary>
    [Theory]
    [MemberData(nameof(WireEnums))]
    public void WireEnumHasTheValuesOfTheGoType(string type, string file, string goType)
    {
        var source = GoContract.ApiSource(file);
        var go = goType.EndsWith('*')
            ? GoContract.StringConstants(source).Where(c => c.Key.StartsWith(goType[..^1], StringComparison.Ordinal)).Select(c => c.Value).ToArray()
            : [.. GoContract.TypedStringConstants(source, goType)];
        Assert.NotEmpty(go);
        Assert.Equal(go.Order(StringComparer.Ordinal), ConstValues(WireEnumType(type)).Order(StringComparer.Ordinal));
    }

    /// <summary>LinkedAccount.provider has no Go constants; docs/api.md names its values.</summary>
    [Fact]
    public void LinkedProviderHasTheValuesOfApiMd()
    {
        const string marker = "LinkedAccount { \"provider\": \"";
        var doc = GoContract.Source("docs", "api.md");
        var start = doc.IndexOf(marker, StringComparison.Ordinal) + marker.Length;
        var values = doc[start..doc.IndexOf('"', start)].Split('|');
        Assert.Equal(values.Order(StringComparer.Ordinal), ConstValues(typeof(LinkedProvider)).Order(StringComparer.Ordinal));
    }

    [Fact]
    public void TlsErrorReasonsAreThoseOfTlsGoInItsOrder()
    {
        Assert.Equal(GoContract.TypedStringConstants(GoContract.ApiSource("tls.go"), "TLSErrorReason"), TlsErrorReason.All.Select(r => r.Value));
    }

    [Fact]
    public void LimitsAreTheConstantsOfTypesGo()
    {
        var go = GoContract.IntConstants(GoContract.ApiSource("types.go"));
        var limits = typeof(API.Limits).GetFields(BindingFlags.Public | BindingFlags.Static).Where(f => f.IsLiteral).ToArray();
        Assert.Equal(32, limits.Length);
        foreach (var limit in limits)
        {
            // Go spells the identifier "IDs"; C# "Ids".
            var goName = limit.Name.Replace("Ids", "IDs", StringComparison.Ordinal);
            Assert.True(go.ContainsKey(goName), $"types.go has no {goName}");
            Assert.Equal((limit.Name, go[goName]), (limit.Name, (long)(int)limit.GetRawConstantValue()!));
        }
        Assert.Equal(API.ProtocolVersion, GoContract.IntConstants(GoContract.ApiSource("doc.go"))["ProtocolVersion"]);
    }

    [Fact]
    public void HandshakeConstantsAreThoseOfAuthGo()
    {
        var auth = GoContract.ApiSource("auth.go");
        var handshake = GoContract.ApiSource("handshake.go");
        var strings = GoContract.StringConstants(auth);
        Assert.Equal(RpcAuth.KeyFileSuffix, strings["KeyFileSuffix"]);
        Assert.Equal(RpcAuth.Label, strings["authLabel"]);
        Assert.Equal(RpcAuth.RoleDaemon, strings["roleDaemon"]);
        Assert.Equal(RpcAuth.RoleClient, strings["roleClient"]);
        Assert.Equal(RpcAuth.KeyFileSize, GoContract.IntConstants(auth)["KeyFileSize"]);
        Assert.Equal(RpcAuth.MaxHandshakeLine, GoContract.IntConstants(handshake)["maxHandshakeLine"]);
        Assert.Equal(RpcAuth.MaxSkippedNotifications, GoContract.IntConstants(handshake)["maxHandshakeNotifications"]);
        Assert.Equal(GoContract.Seconds(auth, "HandshakeTimeout"), RpcTimeouts.Handshake);
    }

    /// <summary>RpcTimeouts are the GTK UI's constants (ui/internal), as RPCTimeouts.swift says.</summary>
    [Fact]
    public void TimeoutsAreThoseOfTheGtkUi()
    {
        var window = GoContract.Source("ui", "internal", "window", "actions.go");
        Assert.Equal(GoContract.Seconds(window, "rpcTimeout"), RpcTimeouts.Default);
        Assert.Equal(GoContract.Seconds(GoContract.Source("ui", "internal", "widget", "rpc.go"), "RPCTimeout"), RpcTimeouts.Default);
        Assert.Equal(GoContract.SecondsIn(GoContract.Source("ui", "internal", "window", "window.go"), "fetchSystemInfo"), RpcTimeouts.SystemInfo);
        Assert.Equal(GoContract.Seconds(GoContract.Source("ui", "internal", "window", "attachments.go"), "partTimeout"), RpcTimeouts.Part);
        Assert.Equal(GoContract.Seconds(GoContract.Source("ui", "internal", "editor", "cid.go"), "fetchTimeout"), RpcTimeouts.Part);
        Assert.Equal(GoContract.Seconds(GoContract.Source("ui", "internal", "window", "remote.go"), "remoteTimeout"), RpcTimeouts.Remote);
        Assert.Equal(GoContract.Seconds(GoContract.Source("ui", "internal", "window", "compose_open.go"), "composeTimeout"), RpcTimeouts.Compose);
        var wizard = GoContract.Source("ui", "internal", "accountwizard", "wizard.go");
        Assert.Equal(GoContract.Seconds(wizard, "discoverTimeout"), RpcTimeouts.Discover);
        Assert.Equal(GoContract.Seconds(wizard, "testTimeout"), RpcTimeouts.Test);
        Assert.Equal(GoContract.Seconds(wizard, "addTimeout"), RpcTimeouts.Save);
        Assert.Equal(GoContract.Seconds(wizard, "oauthStartTimeout"), RpcTimeouts.OAuthStart);
        Assert.Equal(GoContract.Seconds(wizard, "oauthWaitCallTimeout"), RpcTimeouts.OAuthWaitCall);
        Assert.Equal(GoContract.Seconds(GoContract.Source("ui", "internal", "window", "sync.go"), "signInStartTimeout"), RpcTimeouts.OAuthStart);
        // download.go counts in minutes, which GoContract.Seconds does not read.
        Assert.Contains("const downloadTimeout = 5 * time.Minute", GoContract.Source("ui", "internal", "window", "download.go"), StringComparison.Ordinal);
        Assert.Equal(TimeSpan.FromMinutes(5), RpcTimeouts.Download);
        var jiraWizard = GoContract.Source("ui", "internal", "accountwizard", "jira_flow.go");
        Assert.Equal(GoContract.Seconds(jiraWizard, "detectSiteTimeout"), RpcTimeouts.DetectSite);
        Assert.Equal(GoContract.Seconds(jiraWizard, "listSpacesTimeout"), RpcTimeouts.ListSpaces);
        Assert.Equal(GoContract.Seconds(GoContract.Source("ui", "internal", "jiraaccount", "controller.go"), "listSpacesTimeout"), RpcTimeouts.ListSpaces);
        // One value for both issue calls (issue_actions.go issueTimeout).
        var issue = GoContract.Source("ui", "internal", "window", "issue_actions.go");
        Assert.Equal(GoContract.Seconds(issue, "issueTimeout"), RpcTimeouts.Transition);
        Assert.Equal(GoContract.Seconds(issue, "issueTimeout"), RpcTimeouts.Transitions);
    }

    /// <summary>
    /// Every record of the API layer has exactly the JSON members of its Go
    /// struct (types.go, tls.go, errors.go), field by field, and a Go int64
    /// is a long: the contract's names as backend/pkg/api spells them.
    /// </summary>
    [Fact]
    public void EveryRecordHasTheMembersOfItsGoStruct()
    {
        var structs = GoContract.Structs(GoContract.ApiSource("types.go"), GoContract.ApiSource("tls.go"), GoContract.ApiSource("errors.go"));
        var records = typeof(API).Assembly.GetTypes().Where(t =>
            t.Namespace == "Malachi.Core.Api" && t.IsClass && t.IsPublic && !t.IsAbstract
            && t.GetProperty("EqualityContract", BindingFlags.NonPublic | BindingFlags.Instance) is not null
            && t != typeof(EmptyParams) && t != typeof(EmptyResult)).ToArray();
        Assert.True(records.Length > 100);
        foreach (var record in records)
        {
            var goName = record.Name switch
            {
                nameof(RpcError) => "Error",
                nameof(TlsErrorData) => "TLSErrorData",
                _ => record.Name,
            };
            Assert.True(structs.ContainsKey(goName), $"backend/pkg/api has no struct {goName} for {record.Name}");
            var goFields = Flatten(structs, goName).ToArray();
            var csFields = WireMembers(record).ToArray();
            Assert.Equal(
                $"{record.Name}: {string.Join(", ", goFields.Select(f => f.Json).Order(StringComparer.Ordinal))}",
                $"{record.Name}: {string.Join(", ", csFields.Select(f => f.Json).Order(StringComparer.Ordinal))}");
            foreach (var (json, goType) in goFields.Where(f => f.GoType == "int64"))
            {
                var property = csFields.Single(f => f.Json == json).Type;
                Assert.True(property == typeof(long) || property == typeof(long?), $"{record.Name}.{json} is a Go int64, not a {property.Name}");
            }
        }
    }

    // The fields of a Go struct with its embedded structs' flattened in, as
    // encoding/json writes them.
    private static IEnumerable<(string Json, string GoType)> Flatten(
        IReadOnlyDictionary<string, IReadOnlyList<(string? Json, string GoType)>> structs, string name)
    {
        foreach (var (json, goType) in structs[name])
        {
            if (json is null)
            {
                foreach (var inner in Flatten(structs, goType))
                {
                    yield return inner;
                }
            }
            else
            {
                yield return (json, goType);
            }
        }
    }

    // The members a record writes: [JsonPropertyName] properties; Message,
    // whose converter flattens its Summary, those of both.
    private static IEnumerable<(string Json, Type Type)> WireMembers(Type record)
    {
        foreach (var property in record.GetProperties())
        {
            if (property.GetCustomAttribute<System.Text.Json.Serialization.JsonPropertyNameAttribute>() is { } name)
            {
                yield return (name.Name, property.PropertyType);
            }
        }
        if (record == typeof(Message))
        {
            foreach (var member in WireMembers(typeof(MessageSummary)))
            {
                yield return member;
            }
            foreach (var (json, property) in new[]
            {
                ("cc", nameof(Message.Cc)), ("bcc", nameof(Message.Bcc)), ("replyTo", nameof(Message.ReplyTo)),
                ("rfcMessageId", nameof(Message.RfcMessageId)), ("inReplyTo", nameof(Message.InReplyTo)),
                ("references", nameof(Message.References)), ("attachments", nameof(Message.Attachments)), ("headers", nameof(Message.Headers)),
            })
            {
                yield return (json, typeof(Message).GetProperty(property)!.PropertyType);
            }
        }
    }

    // The type of a wire enum by name.
    internal static Type WireEnumType(string name) =>
        typeof(API).Assembly.GetType($"Malachi.Core.Api.{name}", throwOnError: true)!;

    // The const string members of a wire enum, its values.
    internal static IEnumerable<string> ConstValues(Type type) =>
        type.GetFields(BindingFlags.Public | BindingFlags.Static)
            .Where(f => f.IsLiteral && f.FieldType == typeof(string))
            .Select(f => (string)f.GetRawConstantValue()!);
}
