// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// EnvironmentPreferredLanguages (Core/I18n): what MALACHI_LANGUAGES, the
// development override of the display languages, reads as tags, the
// culture of the first, and the catalogue the tags pick from po/.

using System.Collections.Generic;
using System.Globalization;
using Malachi.Core.I18n;
using Xunit;

namespace Malachi.Core.Tests.I18n;

public sealed class EnvironmentPreferredLanguagesTests
{
    [Theory]
    [InlineData(null, new string[0])]
    [InlineData("", new string[0])]
    [InlineData("  ", new string[0])]
    [InlineData("en-GB", new[] { "en-GB" })]
    [InlineData("cs-CZ;en-GB", new[] { "cs-CZ", "en-GB" })]
    [InlineData("cs-CZ, en-GB en", new[] { "cs-CZ", "en-GB", "en" })]
    [InlineData("cs_CZ;cs-cz;CS-CZ", new[] { "cs-CZ" })]
    [InlineData("en-GB;x@y;--;en-;a.b;toolongalabel;zh-Hant-TW", new[] { "en-GB", "zh-Hant-TW" })]
    [InlineData("x y", new[] { "x", "y" })]
    [InlineData(";,;", new string[0])]
    public void ParsesTheTags(string? value, string[] expected) =>
        Assert.Equal(expected, EnvironmentPreferredLanguages.Parse(value));

    [Fact]
    public void UnsetOrEmptyIsNothing()
    {
        Assert.Null(EnvironmentPreferredLanguages.FromEnvironment(new Dictionary<string, string>()));
        Assert.Null(EnvironmentPreferredLanguages.FromEnvironment(new Dictionary<string, string> { [EnvironmentPreferredLanguages.Variable] = "" }));
        Assert.Null(EnvironmentPreferredLanguages.Of(";"));
        Assert.Equal("MALACHI_LANGUAGES", EnvironmentPreferredLanguages.Variable);
    }

    [Fact]
    public void TheFirstTagIsTheRegionalFormat()
    {
        var languages = EnvironmentPreferredLanguages.FromEnvironment(
            new Dictionary<string, string> { [EnvironmentPreferredLanguages.Variable] = "en-GB;cs-CZ" });
        Assert.NotNull(languages);
        Assert.Equal(["en-GB", "cs-CZ"], languages.Languages);
        Assert.NotNull(languages.Culture);
        Assert.Equal("en-GB", languages.Culture.Name);
        Assert.Equal("28/10/2026", new System.DateTime(2026, 10, 28).ToString("d", languages.Culture));
        var czech = EnvironmentPreferredLanguages.Of("cs-CZ");
        Assert.NotNull(czech?.Culture);
        Assert.Equal("cs-CZ", czech.Culture.Name);
    }

    [Fact]
    public void AnUnknownTagHasNoCultureButStillPicksTheCatalogue()
    {
        var languages = EnvironmentPreferredLanguages.Of("qq-QQ;cs");
        Assert.NotNull(languages);
        Assert.Null(languages.Culture);
        Assert.Equal(["qq-QQ", "cs"], languages.Languages);
        var po = RepositoryPo.Directory;
        var environment = new Dictionary<string, string> { [Catalogue.LocaleDirEnv] = po };
        Assert.Equal("cs", Catalogue.Default(environment, languages, appDirectory: po).Language);
        Assert.Equal("en", Catalogue.Default(environment, EnvironmentPreferredLanguages.Of("en-GB;cs-CZ"), appDirectory: po).Language);
    }

    [Fact]
    public void ApplySetsTheThreadsCulture()
    {
        var before = (CultureInfo.CurrentCulture, CultureInfo.CurrentUICulture, CultureInfo.DefaultThreadCurrentCulture, CultureInfo.DefaultThreadCurrentUICulture);
        try
        {
            EnvironmentPreferredLanguages.Of("en-GB")!.Apply();
            Assert.Equal("en-GB", CultureInfo.CurrentCulture.Name);
            Assert.Equal("en-GB", CultureInfo.CurrentUICulture.Name);
            Assert.Equal("en-GB", CultureInfo.DefaultThreadCurrentCulture?.Name);
            // No culture: nothing changes.
            EnvironmentPreferredLanguages.Of("qq-QQ")!.Apply();
            Assert.Equal("en-GB", CultureInfo.CurrentCulture.Name);
        }
        finally
        {
            CultureInfo.CurrentCulture = before.Item1;
            CultureInfo.CurrentUICulture = before.Item2;
            CultureInfo.DefaultThreadCurrentCulture = before.Item3;
            CultureInfo.DefaultThreadCurrentUICulture = before.Item4;
        }
    }
}
