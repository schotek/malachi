// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CIDRegistryTests.swift, the
// counterpart of ui/internal/editor/cid_test.go.

using System;
using System.Text;
using System.Threading.Tasks;
using Malachi.Core.Html;
using Xunit;

namespace Malachi.Core.Tests.Html;

public sealed class CidRegistryTests
{
    // The registry resolves what was registered, by either route, and
    // nothing else; forgetting an id makes it unknown again.
    [Fact]
    public async Task Registry()
    {
        var registry = new CidRegistry();
        registry.Register("file@x", "/tmp/pic.png", "image/png");
        registry.RegisterFetcher("fetched@x", _ => Task.FromResult(new InlineImage(new byte[] { 1 }, "image/png")));

        var file = Assert.IsType<CidEntry.File>(registry.Lookup("file@x"));
        Assert.Equal("/tmp/pic.png", file.Path);
        Assert.Equal("image/png", file.ContentType);

        var fetcher = Assert.IsType<CidEntry.Fetcher>(registry.Lookup("fetched@x"));
        var fetched = await fetcher.Fetch(TestContext.Current.CancellationToken);
        Assert.Equal(new byte[] { 1 }, fetched.Data.ToArray());
        Assert.Equal("image/png", fetched.ContentType);

        foreach (var id in new[] { "", "other@x", "../file@x", "FILE@x" })
        {
            Assert.False(registry.IsRegistered(id), $"{id} resolves");
        }
        Assert.True(registry.IsRegistered("file@x"));
        registry.Unregister("fetched@x");
        Assert.False(registry.IsRegistered("fetched@x"));
        Assert.Null(registry.Lookup("fetched@x"));
        // Re-registering replaces the entry.
        registry.RegisterFetcher("file@x", _ => Task.FromResult(new InlineImage(new byte[] { 2 }, "image/gif")));
        Assert.IsType<CidEntry.Fetcher>(registry.Lookup("file@x"));
        // Unregistering an unknown id is not an error.
        registry.Unregister("never@x");
        Assert.Equal(TimeSpan.FromSeconds(60), CidRegistry.FetchTimeout);
        Assert.Equal(25 << 20, CidRegistry.MaxCidBytes);
    }

    // Only a non-empty picture within the cap is served; SVG never.
    [Fact]
    public void CheckInlineGate()
    {
        byte[] png = [0x89, 0x50, 0x4E, 0x47]; // "\x89PNG"
        CidRegistry.CheckInline(png, "image/png");
        CidRegistry.CheckInline(png, " Image/JPEG; charset=binary ");

        (string Name, byte[] Data, string ContentType, InlineImageError Want)[] bad =
        [
            ("empty", [], "image/png", InlineImageError.Empty),
            ("svg", Encoding.UTF8.GetBytes("<svg/>"), "image/svg+xml", InlineImageError.NotAPicture),
            ("svg with parameters", Encoding.UTF8.GetBytes("<svg/>"), "IMAGE/SVG+XML; charset=utf-8", InlineImageError.NotAPicture),
            ("html", Encoding.UTF8.GetBytes("<p>"), "text/html", InlineImageError.NotAPicture),
            ("no type", png, "", InlineImageError.NotAPicture),
            ("over cap", new byte[CidRegistry.MaxCidBytes + 1], "image/png", InlineImageError.TooBig),
            ("imageless", png, "imagex/png", InlineImageError.NotAPicture),
        ];
        foreach (var (name, data, contentType, want) in bad)
        {
            var e = Assert.Throws<InlineImageException>(() => CidRegistry.CheckInline(data, contentType));
            Assert.True(e.Error == want, name);
            Assert.Contains("inline image", e.Message, StringComparison.Ordinal);
        }
        CidRegistry.CheckInline(new byte[CidRegistry.MaxCidBytes], "image/png");
    }

    // cid_test.go TestCheckInline's texts: Go's errors, word for word.
    [Fact]
    public void CheckInlineTexts()
    {
        Assert.Equal("inline image is empty", new InlineImageException(InlineImageError.Empty).Message);
        Assert.Equal("inline image too big", new InlineImageException(InlineImageError.TooBig).Message);
        Assert.Equal("inline image is not a picture", new InlineImageException(InlineImageError.NotAPicture).Message);
    }

    // Go's registry is one per process behind a mutex; so is Shared, which
    // the scheme handler may ask from any thread.
    [Fact]
    public void SharedRegistryIsThreadSafe()
    {
        var id = "shared-" + Guid.NewGuid().ToString("N") + "@x";
        Parallel.For(0, 200, i =>
        {
            CidRegistry.Shared.Register(id + i, "/tmp/p.png", "image/png");
            Assert.True(CidRegistry.Shared.IsRegistered(id + i));
            CidRegistry.Shared.Unregister(id + i);
        });
        for (var i = 0; i < 200; i++)
        {
            Assert.False(CidRegistry.Shared.IsRegistered(id + i));
        }
        Assert.Same(CidRegistry.Shared, CidRegistry.Shared);
    }
}
