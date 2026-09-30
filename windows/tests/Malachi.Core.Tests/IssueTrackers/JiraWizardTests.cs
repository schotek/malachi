// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/JiraWizardTests.swift, the
// counterpart of ui/internal/jira/wizard_test.go (TestWizardTexts,
// TestCredentialFields, TestNeedsEmail, TestCheckSiteInput, TestDetected,
// TestDefaultAccountName, TestSpaces, TestOfflineChoices, TestSetupConfig,
// TestClassify, TestFailureOf), with the English catalogue; the Czech
// SpacesTitle is in JiraTranslationTests.

using System;
using System.Linq;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Tests.Api;
using Malachi.Core.Transport;
using Xunit;

namespace Malachi.Core.Tests.IssueTrackers;

public sealed class JiraWizardTests
{
    [Fact]
    public void WizardTexts()
    {
        var s = Jira.WizardTexts();
        foreach (var p in typeof(JiraWizardStrings).GetProperties().Where(p => p.PropertyType == typeof(string)))
        {
            Assert.False(string.IsNullOrEmpty((string?)p.GetValue(s)), $"WizardStrings.{p.Name} is empty");
        }
        Assert.Equal("Add _Jira Account…", s.AddMenu);
        Assert.Equal("Spaces", s.SpacesTitle);
        Assert.Equal("Adding the account", s.Adding);
    }

    [Fact]
    public void CredentialFields()
    {
        Assert.Equal(
            new JiraCredentialPage
            {
                ShowsLogin = true,
                LoginLabel = "E-mail Address",
                TokenLabel = "API Token",
                Help = "Create an API token for Malachi Mail in your Atlassian account, then paste it here.",
                HelpButton = "Create API Token…",
                HelpUrl = "https://id.atlassian.com/manage-profile/security/api-tokens",
                TokenPrompt = "Enter the API token for this account",
                Rejected = "The Jira site rejected the token",
            },
            Jira.CredentialFields(JiraDeployment.Cloud));
        var dc = Jira.CredentialFields(JiraDeployment.Datacenter);
        Assert.False(dc.ShowsLogin);
        Assert.Equal("Personal Access Token", dc.TokenLabel);
        Assert.Equal("", dc.HelpUrl);
        Assert.Equal("", dc.HelpButton);
        Assert.Equal("Create a personal access token in your Jira profile, then paste it here.", dc.Help);
        Assert.Equal("Enter the personal access token for this account", dc.TokenPrompt);
        Assert.Equal(dc, Jira.CredentialFields("server")); // an unknown deployment gets the Data Center page
    }

    [Theory]
    [InlineData(JiraDeployment.Cloud, null, false)]
    [InlineData(JiraDeployment.Datacenter, null, true)]
    [InlineData(JiraDeployment.Datacenter, " ", true)]
    [InlineData(JiraDeployment.Datacenter, "jana@acme.example", false)]
    public void NeedsEmail(string d, string? email, bool want) =>
        Assert.Equal(want, Jira.NeedsEmail(d, new SiteUser { Name = "Jana", Email = email }));

    private const string Bad = "This is not a web address";

    [Theory]
    [InlineData("", false, "")]
    [InlineData("   ", false, "")]
    [InlineData("acme.atlassian.net", true, "")]
    [InlineData(" acme.atlassian.net ", true, "")]
    [InlineData("https://acme.atlassian.net", true, "")]
    [InlineData("HTTPS://acme.atlassian.net/", true, "")]
    [InlineData("http://jira.local:8080/jira", true, "")]
    [InlineData("jira", true, "")]
    [InlineData("jira.acme.example:8443/jira", true, "")]
    [InlineData("ftp://acme.atlassian.net", false, Bad)]
    [InlineData("javascript://acme.atlassian.net", false, Bad)]
    [InlineData("acme atlassian.net", false, Bad)]
    [InlineData("acme.atlassian.net\\x", false, Bad)]
    [InlineData("jana@acme.atlassian.net", false, Bad)]
    [InlineData("https://jana:secret@acme.atlassian.net", false, Bad)]
    [InlineData("https://", false, Bad)]
    [InlineData("https:///browse", false, Bad)]
    [InlineData("acme" + JiraTests.Zwsp + ".atlassian.net", false, Bad)]
    [InlineData("https://[bad", false, Bad)]
    [InlineData("://acme.atlassian.net", false, Bad)]
    public void CheckSiteInput(string input, bool ok, string problem) => Assert.Equal((ok, problem), Jira.CheckSiteInput(input));

    private static AccountDetectSiteResult Site(string deployment, string? title = null, string? version = null, string siteUrl = "") =>
        new() { Kind = AccountKind.Jira, SiteUrl = siteUrl, Deployment = deployment, Title = title, Version = version };

    public static TheoryData<AccountDetectSiteResult, string> DetectedCases => new()
    {
        { Site(JiraDeployment.Cloud, "Acme", "1001.0.0-SNAPSHOT"), "Found Acme" },
        { Site(JiraDeployment.Cloud), "Found Jira Cloud" },
        { Site(JiraDeployment.Datacenter, "Acme Jira", "9.12.4"), "Found Acme Jira, version 9.12.4" },
        { Site(JiraDeployment.Datacenter, version: "9.12.4"), "Found Jira Data Center, version 9.12.4" },
        { Site(JiraDeployment.Datacenter, "Acme" + JiraTests.Rlo + "\n"), "Found Acme" },
        { Site("", JiraTests.Zwsp), "Found Jira" },
    };

    [Theory]
    [MemberData(nameof(DetectedCases))]
    public void Detected(AccountDetectSiteResult res, string want) => Assert.Equal(want, Jira.Detected(res));

    public static TheoryData<AccountDetectSiteResult, string> NameCases => new()
    {
        { Site("", " Acme Jira ", siteUrl: "https://acme.atlassian.net"), "Acme Jira" },
        { Site("", siteUrl: "https://ACME.atlassian.net"), "acme.atlassian.net" },
        { Site("", JiraTests.Rlo, siteUrl: "https://jira.acme.example:8443/jira"), "jira.acme.example" },
        { Site("", siteUrl: "https://[bad"), "Jira" },
        { Site(""), "Jira" },
    };

    [Theory]
    [MemberData(nameof(NameCases))]
    public void DefaultAccountName(AccountDetectSiteResult res, string want) => Assert.Equal(want, Jira.DefaultAccountName(res));

    [Fact]
    public void DefaultAccountNameIsCapped()
    {
        var bytes = Encoding.UTF8.GetByteCount(Jira.DefaultAccountName(Site("", new string('ř', 300))));
        Assert.True(bytes is <= 256 and >= 255, $"a long title makes a name of {bytes} bytes");
    }

    private static Space Space(string key, string name, string id = "", int issues = -1, bool? serviceDesk = null) =>
        new() { Id = id, Key = key, Name = name, Issues = issues, ServiceDesk = serviceDesk };

    [Fact]
    public void Spaces()
    {
        Assert.Equal("ITSD – IT Service Desk", Jira.SpaceTitle(Space("ITSD", "IT Service Desk")));
        Assert.Equal("WEB", Jira.SpaceTitle(Space("WEB", "")));
        Assert.Equal("Mobile", Jira.SpaceTitle(Space("", "Mobile")));
        Assert.Equal("MOB – Mobile app", Jira.SpaceTitle(Space(" MOB ", "Mobile\napp" + JiraTests.Zwsp)));
        Assert.Equal("", Jira.SpaceTitle(Space("", "")));
        Assert.Equal("", Jira.ApproxCount(-1));
        Assert.Equal("about 0 issues", Jira.ApproxCount(0));
        Assert.Equal("about 1 issue", Jira.ApproxCount(1));
        Assert.Equal("about 5 issues", Jira.ApproxCount(5));
        Assert.Equal<JiraSpaceRow>(
            [
                new() { Id = "10001", Title = "ITSD – IT Service Desk", Count = "about 42 issues", ServiceDesk = true },
                new() { Id = "10002", Title = "WEB – Website" },
            ],
            Jira.SpaceRows([Space("ITSD", "IT Service Desk", "10001", 42, true), Space("WEB", "Website", "10002")]));
        Assert.Empty(Jira.SpaceRows(null));
        Assert.Equal("Select at least one space", Jira.SpacesProblem(-1));
        Assert.Equal("Select at least one space", Jira.SpacesProblem(0));
        Assert.Equal("", Jira.SpacesProblem(1));
        Assert.Equal("", Jira.SpacesProblem(API.Limits.MaxJiraSpaces));
        Assert.Equal($"Select at most {API.Limits.MaxJiraSpaces} spaces", Jira.SpacesProblem(API.Limits.MaxJiraSpaces + 1));
    }

    [Fact]
    public void OfflineChoices()
    {
        var labels = Jira.OfflineChoiceLabels();
        Assert.Equal(Jira.OfflineChoices.Count, labels.Count);
        Assert.Equal("1 week", labels[0]);
        Assert.Equal("1 year", labels[3]);
        Assert.All(Jira.OfflineChoices, d => Assert.InRange(d, 1, API.Limits.MaxJiraOfflineDays));
        Assert.Equal(API.Limits.DefaultJiraOfflineDays, Jira.OfflineChoices[Jira.IndexOfOfflineDays(0)]);
        foreach (var (days, want) in new[] { (-5, 1), (0, 1), (1, 0), (7, 0), (18, 0), (19, 1), (30, 1), (60, 1), (61, 2), (90, 2), (227, 2), (228, 3), (365, 3), (1000, 3) })
        {
            Assert.True(want == Jira.IndexOfOfflineDays(days), $"IndexOfOfflineDays({days}) = {Jira.IndexOfOfflineDays(days)}, want {want}");
        }
    }

    [Fact]
    public void SetupConfig()
    {
        Space[] spaces = [Space("ITSD", "IT Service Desk", "10001", 42, true), Space("WEB", "Website", "10002")];
        var cloud = new JiraSetup
        {
            Site = Site(JiraDeployment.Cloud, "Acme", siteUrl: "https://acme.atlassian.net") with { CloudId = "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0" },
            Login = " jana@acme.example ",
            Email = "ignored@acme.example",
            Spaces = spaces,
            OnlyMine = true,
            OfflineDays = 90,
        };
        ApiJson.AssertSameValue(
            new AccountConfig
            {
                Name = "Acme",
                Email = "jana@acme.example",
                Kind = AccountKind.Jira,
                Jira = new JiraConfig
                {
                    SiteUrl = "https://acme.atlassian.net",
                    Deployment = JiraDeployment.Cloud,
                    CloudId = "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0",
                    Login = "jana@acme.example",
                    Spaces = [new SpaceRef { Id = "10001", Key = "ITSD", Name = "IT Service Desk" }, new SpaceRef { Id = "10002", Key = "WEB", Name = "Website" }],
                    OnlyMine = true,
                    OfflineDays = 90,
                },
            },
            cloud.Config());

        var dc = new JiraSetup
        {
            Site = Site(JiraDeployment.Datacenter, siteUrl: "https://jira.acme.example/jira") with { CloudId = "stray" },
            Login = "jana",
            Email = " jana@acme.example ",
            Name = " Work Jira ",
            OfflineDays = 9999,
        }.Config();
        Assert.Equal("Work Jira", dc.Name);
        Assert.Equal("jana@acme.example", dc.Email);
        Assert.Equal<AccountKind?>(AccountKind.Jira, dc.Kind);
        Assert.Null(dc.Jira!.Login);
        Assert.Null(dc.Jira.CloudId);
        Assert.Equal(API.Limits.MaxJiraOfflineDays, dc.Jira.OfflineDays);
        Assert.Empty(dc.Jira.Spaces);
        Assert.Null(dc.Imap);
        Assert.Null(dc.Smtp);
        Assert.Null(dc.Graph);
        var negative = new JiraSetup { Site = Site(""), OfflineDays = -3 }.Config();
        Assert.Null(negative.Jira!.OfflineDays); // Go's 0: the default window
        Assert.Equal("Jira", negative.Name);
    }

    private static RpcException Error(int code) => new(new RpcError { Code = code, Message = "x" });

    [Fact]
    public void Classify()
    {
        Assert.Equal(JiraErrorClass.Other, Jira.Classify(null));
        Assert.Equal(JiraErrorClass.Other, Jira.Classify(new InvalidOperationException("disconnected")));
        Assert.Equal(JiraErrorClass.Invalid, Jira.Classify(Error(ErrorCode.InvalidArgument)));
        Assert.Equal(JiraErrorClass.Invalid, Jira.Classify(Error(ErrorCode.InvalidParams)));
        Assert.Equal(JiraErrorClass.Server, Jira.Classify(Error(ErrorCode.ServerError)));
        Assert.Equal(JiraErrorClass.Network, Jira.Classify(Error(ErrorCode.NetworkError)));
        Assert.Equal(JiraErrorClass.Network, Jira.Classify(Error(ErrorCode.Offline)));
        Assert.Equal(JiraErrorClass.Network, Jira.Classify(Error(ErrorCode.ServerTimeout)));
        Assert.Equal(JiraErrorClass.Tls, Jira.Classify(Error(ErrorCode.TlsError)));
        Assert.Equal(JiraErrorClass.AuthFailed, Jira.Classify(Error(ErrorCode.AuthFailed)));
        Assert.Equal(JiraErrorClass.AuthRequired, Jira.Classify(Error(ErrorCode.AuthRequired)));
        Assert.Equal(JiraErrorClass.Conflict, Jira.Classify(Error(ErrorCode.Conflict)));
        Assert.Equal(JiraErrorClass.Other, Jira.Classify(Error(ErrorCode.KeyringError)));
        Assert.Equal(JiraErrorClass.Other, Jira.Classify(Error(ErrorCode.NotImplemented)));
        Assert.Equal(JiraErrorClass.Other, Jira.ClassOf(new ErrorCode(0)));
    }

    private const string NotJira = "This address is not a Jira site";
    private const string Rejected = "The Jira site rejected the token";
    private const string Exists = "An account for this Jira site already exists";
    private const string LookUp = "Looking up the Jira site";
    private const string Loading = "Loading the spaces";
    private const string Adding = "Adding the account";
    private const string Saving = "Saving the account";

    public static TheoryData<string, JiraWizardStep, JiraErrorClass, string, bool, JiraFailure> FailureCases => new()
    {
        { "detect: not jira", JiraWizardStep.Detect, JiraErrorClass.Server, "", false, new(JiraWizardPage.Site, NotJira, LookUp) },
        { "detect: bad address", JiraWizardStep.Detect, JiraErrorClass.Invalid, "", false, new(JiraWizardPage.Site, Bad, LookUp) },
        { "detect: network", JiraWizardStep.Detect, JiraErrorClass.Network, "", false, new(JiraWizardPage.Site, "", LookUp) },
        { "detect: tls", JiraWizardStep.Detect, JiraErrorClass.Tls, "", false, new(JiraWizardPage.Site, "", LookUp) },
        { "detect: other", JiraWizardStep.Detect, JiraErrorClass.Other, "", false, new(JiraWizardPage.Site, "", LookUp) },
        { "spaces: rejected", JiraWizardStep.Spaces, JiraErrorClass.AuthFailed, JiraDeployment.Cloud, false, new(JiraWizardPage.Credentials, Rejected, Loading) },
        { "spaces: no token (cloud)", JiraWizardStep.Spaces, JiraErrorClass.AuthRequired, JiraDeployment.Cloud, true, new(JiraWizardPage.Credentials, "Enter the API token for this account", Loading) },
        { "spaces: no token (dc)", JiraWizardStep.Spaces, JiraErrorClass.AuthRequired, JiraDeployment.Datacenter, true, new(JiraWizardPage.Credentials, "Enter the personal access token for this account", Loading) },
        { "spaces: server error is not 'not jira'", JiraWizardStep.Spaces, JiraErrorClass.Server, JiraDeployment.Cloud, false, new(JiraWizardPage.Credentials, "", Loading) },
        { "spaces: network", JiraWizardStep.Spaces, JiraErrorClass.Network, JiraDeployment.Cloud, false, new(JiraWizardPage.Credentials, "", Loading) },
        { "save: conflict", JiraWizardStep.Save, JiraErrorClass.Conflict, JiraDeployment.Cloud, false, new(JiraWizardPage.Spaces, Exists, Adding) },
        { "save: rejected", JiraWizardStep.Save, JiraErrorClass.AuthFailed, JiraDeployment.Cloud, false, new(JiraWizardPage.Credentials, Rejected, Adding) },
        { "save: other", JiraWizardStep.Save, JiraErrorClass.Other, JiraDeployment.Cloud, false, new(JiraWizardPage.Spaces, "", Adding) },
        { "edit: other", JiraWizardStep.Save, JiraErrorClass.Network, JiraDeployment.Datacenter, true, new(JiraWizardPage.Credentials, "", Saving) },
        { "edit: conflict", JiraWizardStep.Save, JiraErrorClass.Conflict, JiraDeployment.Datacenter, true, new(JiraWizardPage.Credentials, Exists, Saving) },
        { "edit: no token", JiraWizardStep.Save, JiraErrorClass.AuthRequired, JiraDeployment.Datacenter, true, new(JiraWizardPage.Credentials, "Enter the personal access token for this account", Saving) },
    };

    [Theory]
    [MemberData(nameof(FailureCases))]
    public void FailureOf(string name, JiraWizardStep step, JiraErrorClass errorClass, string d, bool editing, JiraFailure want) =>
        Assert.True(want == Jira.FailureOf(step, errorClass, d, editing), $"{name}: {Jira.FailureOf(step, errorClass, d, editing)}");
}
