// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/APICodingTests+AssistantProfile.swift
// and of backend/pkg/api/assistant_test.go (TestAssistantProfileJSON): the
// assistant profile's wire types, the result as the daemon writes it,
// absent fields, and params that send only what they carry.

using System.Linq;
using System.Text.Json;
using Malachi.Core.Api;
using Xunit;

namespace Malachi.Core.Tests.Api;

public sealed class AssistantProfileApiTests
{
    [Fact]
    public void GetResultAsGoWritesIt()
    {
        var r = JsonCoding.Decode<AssistantProfileGetResult>(
            """
            {"profile":{"text":"Píšu stručně.","gender":"masculine",
             "accounts":[{"accountId":"acc_1","note":"Podepisuj jako studio."}],"shareWithMcp":true,
             "updatedAt":"2026-10-21T08:00:00Z"},
             "proposals":[{"id":"p_1","text":"Schůzky v pondělí.","source":"panel","createdAt":"2026-10-21T08:00:00Z"}],
             "limits":{"text":2000,"accountNote":500,"proposal":300}}
            """);
        Assert.Equal("Píšu stručně.", r.Profile.Text);
        Assert.Equal(ProfileGender.Masculine, r.Profile.Gender);
        Assert.True(r.Profile.ShareWithMcp);
        Assert.NotNull(r.Profile.UpdatedAt);
        var note = Assert.Single(r.Profile.Accounts);
        Assert.Equal(("acc_1", "Podepisuj jako studio."), (note.AccountId.Value, note.Note));
        var q = Assert.Single(r.Proposals);
        Assert.Equal(ProfileSource.Panel, q.Source);
        Assert.Null(q.AccountId);
        Assert.Equal(new ProfileLimits(), r.Limits);
    }

    [Fact]
    public void AbsentFieldsReadAsDefaults()
    {
        var p = JsonCoding.Decode<AssistantProfile>("""{"accounts":null}""");
        Assert.Equal("", p.Text);
        Assert.Equal(ProfileGender.Unset, p.Gender);
        Assert.Empty(p.Accounts);
        Assert.True(p.ShareWithMcp); // sharing is on unless the daemon says otherwise
        var r = JsonCoding.Decode<AssistantProfileGetResult>("""{"profile":{},"proposals":null}""");
        Assert.Empty(r.Proposals);
        Assert.Null(r.Limits);
        Assert.Equal("other", JsonCoding.Decode<ProfileGender>("\"other\"").Value); // a newer value decodes as itself
    }

    [Fact]
    public void ParamsSendOnlyWhatTheyCarry()
    {
        Assert.Equal("{}", JsonCoding.EncodeToString(new AssistantProfileSetParams()));
        using var set = JsonDocument.Parse(JsonCoding.EncodeToString(new AssistantProfileSetParams
        {
            Gender = ProfileGender.Feminine,
            Accounts = [new ProfileAccountNote("acc_1", "")],
        }));
        Assert.Equal(["accounts", "gender"], set.RootElement.EnumerateObject().Select(m => m.Name).Order());
        Assert.Equal("feminine", set.RootElement.GetProperty("gender").GetString());
        Assert.Equal("""{"id":"p_1","accept":false}""", JsonCoding.EncodeToString(new AssistantProfileResolveParams { Id = "p_1" }));
        Assert.Equal("""{"text":"x","source":"mcp"}""", JsonCoding.EncodeToString(new AssistantProfileProposeParams { Text = "x", Source = ProfileSource.Mcp }));
    }

    [Fact]
    public void Names()
    {
        Assert.Equal("assistant.profileGet", API.AssistantProfileGet.Name);
        Assert.Equal("assistant.profileSet", API.AssistantProfileSet.Name);
        Assert.Equal("assistant.profilePropose", API.AssistantProfilePropose.Name);
        Assert.Equal("assistant.profileResolve", API.AssistantProfileResolve.Name);
        Assert.Equal(
            new DaemonNotification.AssistantProfileChanged(),
            DaemonNotification.Decode(API.Notify.AssistantProfileChanged, (JsonElement?)null));
        Assert.Equal(new ProfileGender[] { ProfileGender.Unset, ProfileGender.Masculine, ProfileGender.Feminine, ProfileGender.Neutral }, ProfileGender.All);
    }
}
