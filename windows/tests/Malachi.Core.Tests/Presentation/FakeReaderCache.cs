// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only test fixture: the reader's IReaderCache answered from memory,
// its fetches held until the test settles them (MessageCacheTests keeps the
// real cache against a FakeDaemon; the reader only needs what it asks).
// PartDataAsync and EmbeddedDataAsync follow the real rules
// (Download.WithDownloadAsync) over the fake fetches and download.

using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Model;
using Malachi.Core.Transport;

namespace Malachi.Core.Tests.Presentation;
/// <summary>A cache over entries the test sets; fetches wait until the test answers them.</summary>
internal sealed class FakeReaderCache : IReaderCache
{
    private readonly Dictionary<MessageId, LoadedMessage> entries = [];
    private readonly List<(MessageSummary Summary, Action<LoadedMessage> Done)> fetches = [];

    public int FetchCalls { get; private set; }

    /// <summary>What message.part answers, by part id; a missing part fails.</summary>
    public Dictionary<string, MessagePartResult> Parts { get; } = [];

    public List<string> PartCalls { get; } = [];

    /// <summary>What message.embedded answers next (null: it fails).</summary>
    public Func<RemoteContentPolicy?, MessageEmbeddedResult?>? Embedded { get; set; }

    public List<RemoteContentPolicy?> EmbeddedCalls { get; } = [];

    /// <summary>The parts message.embedded was asked for, in order.</summary>
    public List<string> EmbeddedParts { get; } = [];

    /// <summary>A gate the embedded calls wait on, when set.</summary>
    public TaskCompletionSource? EmbeddedGate { get; set; }

    /// <summary>The messages message.download was asked for, in order.</summary>
    public List<MessageId> DownloadCalls { get; } = [];

    /// <summary>What message.download answers (the message after it); without one it fails.</summary>
    public Message? Downloaded { get; set; }

    /// <summary>What message.download fails with, when set.</summary>
    public Exception? DownloadError { get; set; }

    /// <summary>The messages whose chips show the download spinner.</summary>
    public HashSet<MessageId> Spinning { get; } = [];

    /// <summary>The parts message.part answers partNotDownloaded for until a download succeeded.</summary>
    public HashSet<string> OnServer { get; } = [];

    public LoadedMessage? Loaded(MessageId id) => entries.GetValueOrDefault(id);

    public LoadedMessage Entry(MessageId id)
    {
        if (!entries.TryGetValue(id, out var lm))
        {
            entries[id] = lm = new LoadedMessage();
        }
        return lm;
    }

    /// <summary>Puts <paramref name="lm"/> in place of the entry of <paramref name="id"/> (a newer message.get replaced it).</summary>
    public void Replace(MessageId id, LoadedMessage lm) => entries[id] = lm;

    /// <summary>The variant every fetch asked for, in order (null: the entry's).</summary>
    public List<bool?> FetchQuoted { get; } = [];

    public void Fetch(MessageSummary s, Action<LoadedMessage> done, bool? quoted = null)
    {
        FetchCalls++;
        FetchQuoted.Add(quoted);
        var lm = Entry(s.Id);
        if (quoted is { } on && lm.ShowQuoted(on) && lm.Body is null)
        {
            // The other variant has yet to come: the test answers it.
            fetches.Add((s, done));
            return;
        }
        if (lm.Complete)
        {
            done(lm);
            return;
        }
        fetches.Add((s, done));
    }

    /// <summary>Answers the waiting fetches of <paramref name="id"/> with what the entry holds now.</summary>
    public void Settle(MessageId id)
    {
        foreach (var (s, done) in fetches.ToArray())
        {
            if (s.Id == id)
            {
                done(Entry(id));
            }
        }
        if (Entry(id).Complete)
        {
            fetches.RemoveAll(f => f.Summary.Id == id);
        }
    }

    public Task<MessagePartResult> FetchAttachmentAsync(AccountId accountId, MessageId messageId, string partId, CancellationToken cancellationToken = default)
    {
        PartCalls.Add(partId);
        if (OnServer.Contains(partId))
        {
            return Task.FromException<MessagePartResult>(
                new RpcException(new RpcError { Code = ErrorCode.PartNotDownloaded, Message = "on the server" }));
        }
        return Parts.TryGetValue(partId, out var res)
            ? Task.FromResult(res)
            : Task.FromException<MessagePartResult>(new InvalidOperationException("no such part"));
    }

    public async Task<MessageEmbeddedResult> FetchEmbeddedAsync(
        AccountId accountId, MessageId messageId, string partId, RemoteContentPolicy? remote = null, CancellationToken cancellationToken = default)
    {
        EmbeddedCalls.Add(remote);
        EmbeddedParts.Add(partId);
        if (EmbeddedGate is { } gate)
        {
            await gate.Task;
        }
        return Embedded?.Invoke(remote) ?? throw new InvalidOperationException("message.embedded failed");
    }

    public Task<MessagePartResult> PartDataAsync(AccountId accountId, MessageId messageId, Attachment a, bool onServer) =>
        Download.WithDownloadAsync(
            a, onServer, p => FetchAttachmentAsync(accountId, messageId, p.PartId), async () => (Message?)await DownloadAsync(accountId, messageId));

    public Task<MessageEmbeddedResult> EmbeddedDataAsync(
        AccountId accountId, MessageId messageId, Attachment a, bool onServer, RemoteContentPolicy? policy = null) =>
        Download.WithDownloadAsync(
            a, onServer, p => FetchEmbeddedAsync(accountId, messageId, p.PartId, policy), async () => (Message?)await DownloadAsync(accountId, messageId));

    public Task<Message> DownloadAsync(AccountId accountId, MessageId id)
    {
        DownloadCalls.Add(id);
        if (DownloadError is { } error)
        {
            return Task.FromException<Message>(error);
        }
        if (Downloaded is not { } m)
        {
            return Task.FromException<Message>(new InvalidOperationException("no message to download"));
        }
        OnServer.Clear();
        return Task.FromResult(m);
    }

    public bool ShowsDownload(MessageId id) => Spinning.Contains(id);
}
