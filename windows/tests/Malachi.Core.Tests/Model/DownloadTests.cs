// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/DownloadTests.swift, the counterpart
// of ui/internal/window/download_test.go (TestWithDownload,
// TestPartNotDownloaded, TestErrPartNotFoundText, TestMethodUnsupported):
// Download.WithDownloadAsync, the fetch that downloads a part kept on the
// mail server first, or once after the daemon said partNotDownloaded.
// TestPartAfterDownload is AttachmentsTests.PartAfterDownloadTest. Go's
// wrapped errors (fmt.Errorf %w) have no counterpart: the transport throws
// the daemon's error as it came (RpcException).

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Xunit;

namespace Malachi.Core.Tests.Model;

public sealed class DownloadTests
{
    private static readonly RpcException NotDownloaded = Daemon(ErrorCode.PartNotDownloaded, "on the server");

    [Fact]
    public async Task ALocalPartIsFetchedOnce()
    {
        var steps = new Steps();
        Assert.Equal("data of 2", await RunAsync(steps, remote: false));
        Assert.Equal(["fetch:2"], steps.Log);
    }

    [Fact]
    public async Task ARemotePartIsDownloadedFirst()
    {
        var steps = new Steps();
        Assert.Equal("data of 2", await RunAsync(steps, remote: true));
        Assert.Equal(["download", "fetch:2"], steps.Log);
    }

    [Fact]
    public async Task AFailedDownloadFetchesNothing()
    {
        var steps = new Steps { DownloadError = Daemon(ErrorCode.Offline, "no network") };
        var e = await Assert.ThrowsAsync<RpcException>(() => RunAsync(steps, remote: true));
        Assert.Equal(ErrorCode.Offline, e.Code.Value);
        Assert.Equal(["download"], steps.Log);
    }

    [Fact]
    public async Task PartNotDownloadedGetsOneDownloadAndOneRetry()
    {
        var steps = new Steps { FetchErrors = [NotDownloaded] };
        Assert.Equal("data of 2", await RunAsync(steps, remote: false));
        Assert.Equal(["fetch:2", "download", "fetch:2"], steps.Log);
    }

    [Fact]
    public async Task AFailedDownloadAfterPartNotDownloadedIsThrown()
    {
        var steps = new Steps { FetchErrors = [NotDownloaded], DownloadError = Daemon(ErrorCode.MessageGone, "gone") };
        var e = await Assert.ThrowsAsync<RpcException>(() => RunAsync(steps, remote: false));
        Assert.Equal(ErrorCode.MessageGone, e.Code.Value);
        Assert.Equal(["fetch:2", "download"], steps.Log);
    }

    [Fact]
    public async Task AnotherErrorIsNotRetried()
    {
        var steps = new Steps { FetchErrors = [Daemon(ErrorCode.PartNotFound, "no such part")] };
        var e = await Assert.ThrowsAsync<RpcException>(() => RunAsync(steps, remote: false));
        Assert.Equal(ErrorCode.PartNotFound, e.Code.Value);
        Assert.Equal(["fetch:2"], steps.Log);

        steps = new Steps { FetchErrors = [new InvalidOperationException("boom")] };
        await Assert.ThrowsAsync<InvalidOperationException>(() => RunAsync(steps, remote: false));
        Assert.Equal(["fetch:2"], steps.Log);

        // The daemon's other answers, offline included, are the caller's.
        steps = new Steps { FetchErrors = [Daemon(ErrorCode.Offline, "no network")] };
        await Assert.ThrowsAsync<RpcException>(() => RunAsync(steps, remote: false));
        Assert.Equal(["fetch:2"], steps.Log);
    }

    [Fact]
    public async Task NeverALoop()
    {
        // A second partNotDownloaded after the download is thrown as it is.
        var steps = new Steps { FetchErrors = [NotDownloaded, NotDownloaded, null] };
        var e = await Assert.ThrowsAsync<RpcException>(() => RunAsync(steps, remote: false));
        Assert.Equal(ErrorCode.PartNotDownloaded, e.Code.Value);
        Assert.Equal(["fetch:2", "download", "fetch:2"], steps.Log);

        // A remote part that is still not there after its download, too.
        steps = new Steps { FetchErrors = [NotDownloaded] };
        e = await Assert.ThrowsAsync<RpcException>(() => RunAsync(steps, remote: true));
        Assert.Equal(ErrorCode.PartNotDownloaded, e.Code.Value);
        Assert.Equal(["download", "fetch:2"], steps.Log);
    }

    /// <summary>
    /// The fetch after a download asks for the part the downloaded message
    /// lists under the same name and type (AttachmentChips.PartAfterDownload).
    /// </summary>
    [Fact]
    public async Task TheRetryFollowsAMovedPart()
    {
        var moved = Message(Part("2", filename: "logo.png", contentType: "image/png"), Part("3"));
        var steps = new Steps { Downloaded = moved };
        Assert.Equal("data of 3", await RunAsync(steps, remote: true, Part("2", remote: true)));
        Assert.Equal(["download", "fetch:3"], steps.Log);

        steps = new Steps { Downloaded = moved, FetchErrors = [NotDownloaded] };
        Assert.Equal("data of 3", await RunAsync(steps, remote: false, Part("2")));
        Assert.Equal(["fetch:2", "download", "fetch:3"], steps.Log);
    }

    /// <summary>
    /// A part the downloaded message no longer lists is not fetched at all:
    /// its old id names another file there (download.go
    /// <c>errPartNotFound</c>), and the toast says the attachment no longer
    /// exists.
    /// </summary>
    [Fact]
    public async Task APartGoneAfterTheDownloadIsNotFetched()
    {
        var logo = Part("2", filename: "logo.png", contentType: "image/png", size: 9_000);
        var steps = new Steps { Downloaded = Message(logo) };
        var e = await Assert.ThrowsAsync<RpcException>(() => RunAsync(steps, remote: true, Part("2", remote: true)));
        Assert.Equal(Download.PartNotFoundAfterDownload, e.Error);
        Assert.Equal(ErrorCode.PartNotFound, e.Code.Value);
        Assert.Equal(["download"], steps.Log);

        // After partNotDownloaded too: no second fetch.
        steps = new Steps { Downloaded = Message(logo), FetchErrors = [NotDownloaded] };
        e = await Assert.ThrowsAsync<RpcException>(() => RunAsync(steps, remote: false, Part("2")));
        Assert.Equal(Download.PartNotFoundAfterDownload, e.Error);
        Assert.Equal(["fetch:2", "download"], steps.Log);

        // Two candidates under the name and type, none at the old id.
        steps = new Steps { Downloaded = Message(Part("3"), Part("4")) };
        e = await Assert.ThrowsAsync<RpcException>(() => RunAsync(steps, remote: true, Part("2", remote: true)));
        Assert.Equal(Download.PartNotFoundAfterDownload, e.Error);
        Assert.Equal(["download"], steps.Log);

        foreach (var what in new[] { "Opening the attachment", "Saving the attachment", "Opening the attached message" })
        {
            Assert.Equal("The attachment no longer exists", RpcErrorText.Text(what, new RpcException(Download.PartNotFoundAfterDownload)));
        }
        Assert.False(Download.IsPartNotDownloaded(new RpcException(Download.PartNotFoundAfterDownload)));
    }

    [Fact]
    public void MethodUnsupportedTest()
    {
        Assert.True(Download.MethodUnsupported(Daemon(ErrorCode.MethodNotFound, "x")));
        Assert.True(Download.MethodUnsupported(Daemon(ErrorCode.NotImplemented, "x")));
        Exception?[] supported =
        [
            null,
            new InvalidOperationException("boom"),
            Daemon(ErrorCode.Unavailable, "x"),
            Daemon(ErrorCode.StorageError, "x"),
            new RpcClientException(ClientError.Disconnected),
        ];
        foreach (var err in supported)
        {
            Assert.False(Download.MethodUnsupported(err), $"{err}");
        }
    }

    [Fact]
    public void IsPartNotDownloadedTest()
    {
        Assert.True(Download.IsPartNotDownloaded(NotDownloaded));
        Assert.False(Download.IsPartNotDownloaded(Daemon(ErrorCode.PartNotFound, "")));
        Assert.False(Download.IsPartNotDownloaded(new RpcClientException(ClientError.Disconnected)));
        Assert.False(Download.IsPartNotDownloaded(new InvalidOperationException("boom")));
        Assert.False(Download.IsPartNotDownloaded(null));
    }

    private static RpcException Daemon(int code, string message) => new(new RpcError { Code = code, Message = message });

    private static Attachment Part(string id, string filename = "report.pdf", string contentType = "application/pdf", long size = 200_000, bool? remote = null) => new()
    {
        PartId = id,
        Filename = filename,
        ContentType = contentType,
        Size = size,
        Inline = false,
        Remote = remote,
    };

    private static Message Message(params Attachment[] atts) => new()
    {
        Summary = new MessageSummary
        {
            Id = "m",
            AccountId = "a",
            FolderId = "f",
            From = [],
            Subject = "",
            Date = DateTimeOffset.UnixEpoch,
            Snippet = "",
            Flags = [],
            HasAttachments = atts.Length > 0,
            Size = 0,
        },
        Attachments = atts,
    };

    private static Task<string> RunAsync(Steps steps, bool remote, Attachment? a = null) =>
        Download.WithDownloadAsync(a ?? Part("2"), remote, steps.FetchAsync, steps.DownloadAsync);

    /// <summary>What WithDownloadAsync did, in order: "fetch:&lt;partId&gt;" and "download".</summary>
    private sealed class Steps
    {
        public List<string> Log { get; } = [];

        /// <summary>The answers of the fetches, in order; past the end the fetch succeeds.</summary>
        public Exception?[] FetchErrors { get; init; } = [];

        public Exception? DownloadError { get; init; }

        /// <summary>The message the download answers with.</summary>
        public Message? Downloaded { get; init; }

        public Task<string> FetchAsync(Attachment a)
        {
            Log.Add("fetch:" + a.PartId);
            var i = Log.Count(l => l.StartsWith("fetch:", StringComparison.Ordinal)) - 1;
            if (i < FetchErrors.Length && FetchErrors[i] is { } err)
            {
                return Task.FromException<string>(err);
            }
            return Task.FromResult("data of " + a.PartId);
        }

        public Task<Message?> DownloadAsync()
        {
            Log.Add("download");
            return DownloadError is { } err ? Task.FromException<Message?>(err) : Task.FromResult(Downloaded);
        }
    }
}
