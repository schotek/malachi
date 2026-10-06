// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Security/protocol fixtures for docs/chatgpt-integration.md §5–6. Claude
// tests remain the Go/Swift reference; these cover the new native boundary.

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Net;
using System.Net.Http;
using System.Text;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Assistants;
using Malachi.Core.ChatGPT;
using Xunit;

namespace Malachi.Core.Tests.Assistants;

public sealed class CodexPolicyTests
{
    [Fact]
    public void CleanupKeepsActiveSessionsUnrelatedFilesAndLinks()
    {
        var root = Path.Combine(Path.GetTempPath(), "malachi-codex-sweep-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(root);
        var stale = Path.Combine(root, "session-" + Guid.NewGuid().ToString("N"));
        var active = Path.Combine(root, "session-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(stale);
        Directory.CreateDirectory(active);
        var unleased = Path.Combine(root, "session-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(unleased);
        File.WriteAllText(Path.Combine(stale, "lease"), "");
        File.WriteAllText(Path.Combine(stale, "private"), "synthetic old state");
        File.WriteAllText(Path.Combine(root, "unrelated"), "keep");
        try
        {
            using (var lease = new FileStream(Path.Combine(active, "lease"), FileMode.CreateNew, FileAccess.ReadWrite, FileShare.Read))
            {
                CodexSessionDirectories.Sweep(root);
                Assert.False(Directory.Exists(stale));
                Assert.True(Directory.Exists(active));
                Assert.True(Directory.Exists(unleased));
                Assert.True(File.Exists(Path.Combine(root, "unrelated")));
            }
            CodexSessionDirectories.Sweep(root);
            Assert.False(Directory.Exists(active));
            Assert.True(Directory.Exists(unleased));
            if (!OperatingSystem.IsWindows())
            {
                var linked = Path.Combine(root, "session-" + Guid.NewGuid().ToString("N"));
                Directory.CreateSymbolicLink(linked, root);
                CodexSessionDirectories.Sweep(root);
                Assert.True(Directory.Exists(linked));
                Directory.Delete(linked);
            }
        }
        finally { Directory.Delete(root, recursive: true); }
    }

    [Fact]
    public async Task UpstreamCatalogContainsOnlyDeclaredMailToolsAndNeverStorage()
    {
        await using var gate = new CodexInferenceGate(new Tokens(), ["read_message"], new ResponseHandler());
        using var filtered = JsonDocument.Parse(gate.FilterRequest(Encoding.UTF8.GetBytes("""
            {"model":"fixture","stream":false,"store":true,"tool_choice":"required","tools":[
              {"type":"web_search"},{"type":"function","name":"exec_command"},
              {"type":"namespace","name":"malachi","tools":[
                {"type":"function","name":"read_message","parameters":{"type":"object"}},
                {"type":"function","name":"send_message","parameters":{"type":"object"}}]},
              {"type":"namespace","name":"other","tools":[{"type":"function","name":"read_message"}]}]}
            """)));
        Assert.True(filtered.RootElement.GetProperty("stream").GetBoolean());
        Assert.False(filtered.RootElement.GetProperty("store").GetBoolean());
        Assert.False(filtered.RootElement.GetProperty("parallel_tool_calls").GetBoolean());
        var tools = filtered.RootElement.GetProperty("tools");
        Assert.Equal(1, tools.GetArrayLength());
        Assert.Equal("malachi", tools[0].GetProperty("name").GetString());
        Assert.Equal(1, tools[0].GetProperty("tools").GetArrayLength());
        Assert.Equal("read_message", tools[0].GetProperty("tools")[0].GetProperty("name").GetString());
        Assert.DoesNotContain("send_message", filtered.RootElement.GetRawText(), StringComparison.Ordinal);
    }

    [Fact]
    public async Task AdditionalToolCatalogIsFilteredAndRemovedFromHistory()
    {
        await using var gate = new CodexInferenceGate(new Tokens(), ["read_message", "create_draft"], new ResponseHandler());
        using var filtered = JsonDocument.Parse(gate.FilterRequest(Encoding.UTF8.GetBytes("""
            {"tools":[{"type":"namespace","name":"malachi","tools":[{"type":"function","name":"read_message"}]}],
             "input":[{"type":"message","role":"user","content":"fixture"},
              {"type":"additional_tools","tools":[null,{"type":"tool_search"},
               {"type":"namespace","name":"other","tools":[{"type":"function","name":"create_draft"}]},
               {"type":"namespace","name":"malachi","tools":[null,{"type":"function","name":"create_draft"},{"type":"function","name":"send_message"}]}]},
              {"type":"function_call_output","call_id":"fixture","output":"safe"}]}
            """)));
        var root = filtered.RootElement;
        Assert.Equal(2, root.GetProperty("input").GetArrayLength());
        Assert.Equal("message", root.GetProperty("input")[0].GetProperty("type").GetString());
        Assert.Equal("function_call_output", root.GetProperty("input")[1].GetProperty("type").GetString());
        Assert.DoesNotContain("additional_tools", root.GetRawText(), StringComparison.Ordinal);
        Assert.DoesNotContain("tool_search", root.GetRawText(), StringComparison.Ordinal);
        Assert.DoesNotContain("send_message", root.GetRawText(), StringComparison.Ordinal);
        var names = new List<string>();
        foreach (var entry in root.GetProperty("tools").EnumerateArray())
        {
            Assert.Equal("malachi", entry.GetProperty("name").GetString());
            foreach (var tool in entry.GetProperty("tools").EnumerateArray()) { names.Add(tool.GetProperty("name").GetString()!); }
        }
        Assert.Equal(["read_message", "create_draft"], names);
    }

    [Theory]
    [InlineData("null", "codex_invalid_tool_catalog")]
    [InlineData("{}", "codex_invalid_tool_catalog")]
    [InlineData("[]", "codex_required_tools_missing")]
    [InlineData("[{\"type\":\"namespace\",\"name\":\"malachi\",\"tools\":[{\"type\":\"function\",\"name\":\"read_message\"}]}]", "codex_duplicate_tool")]
    public async Task AdditionalCatalogCannotOmitOrDuplicateRequiredTools(string extra, string code)
    {
        await using var gate = new CodexInferenceGate(new Tokens(), ["read_message", "create_draft"], new ResponseHandler());
        var body = "{\"tools\":[{\"type\":\"namespace\",\"name\":\"malachi\",\"tools\":[{\"type\":\"function\",\"name\":\"read_message\"}]}],\"input\":[{\"type\":\"additional_tools\",\"tools\":" + extra + "}]}";
        Assert.Equal(code, Assert.Throws<AssistantProviderException>(() => gate.FilterRequest(Encoding.UTF8.GetBytes(body))).Code);
    }

    [Fact]
    public async Task MissingCatalogAndHostedToolInputFailBeforeForwarding()
    {
        await using var gate = new CodexInferenceGate(new Tokens(), ["read_message"], new ResponseHandler());
        foreach (var body in new[] { "{\"tools\":[]}", "{\"input\":[{\"type\":\"additional_tools\"}]}", "{\"previous_response_id\":\"old\"}", "{\"conversation\":\"old\"}" })
        {
            Assert.Throws<AssistantProviderException>(() => gate.FilterRequest(Encoding.UTF8.GetBytes(body)));
        }
    }

    [Theory]
    [InlineData("function_call", "other", "read_message")]
    [InlineData("function_call", "malachi", "create_draft")]
    [InlineData("function_call", "", "exec_command")]
    [InlineData("custom_tool_call", "malachi", "read_message")]
    [InlineData("web_search_call", "", "")]
    [InlineData("computer_call", "", "")]
    [InlineData("image_generation_call", "", "")]
    [InlineData("mcp_call", "malachi", "read_message")]
    public async Task EveryToolEnvelopeIsCheckedBeforeStreaming(string type, string toolNamespace, string name)
    {
        await using var gate = new CodexInferenceGate(new Tokens(), ["read_message"], new ResponseHandler());
        var item = "{\"type\":\"" + type + "\",\"namespace\":\"" + toolNamespace + "\",\"name\":\"" + name + "\"}";
        Assert.Throws<AssistantProviderException>(() => gate.ValidateResponseEvent("{\"type\":\"response.output_item.added\",\"item\":" + item + "}"));
        Assert.Throws<AssistantProviderException>(() => gate.ValidateResponseEvent("{\"type\":\"response.completed\",\"response\":{\"output\":[" + item + "]}}"));
        gate.ValidateResponseEvent("{\"item\":{\"type\":\"function_call\",\"namespace\":\"malachi\",\"name\":\"read_message\"}}");
    }

    /// <summary>
    /// A triage session's gate passes the read and triage tools of its
    /// spec and nothing else: no create_draft for an automatic run, never
    /// send, modify or a tool of another namespace; a missing triage tool
    /// fails before inference.
    /// </summary>
    [Fact]
    public async Task TriageGateHoldsTheSpecsTools()
    {
        var spec = new AssistantSessionSpec
        {
            SystemPrompt = "S",
            ToolPolicy = AssistantToolPolicy.Triage,
            BridgeArgs = Assistant.TriageBridgeArgs("run_1", 5),
        };
        var tools = Assistant.SessionTools(spec);
        Assert.Contains("annotate_case", tools);
        Assert.DoesNotContain("create_draft", tools);
        await using var gate = new CodexInferenceGate(new Tokens(), tools, new ResponseHandler());
        var catalog = string.Join(",", tools.Append("create_draft").Append("send_message").Append("transition_issue")
            .Select(t => "{\"type\":\"function\",\"name\":\"" + t + "\"}"));
        using var filtered = JsonDocument.Parse(gate.FilterRequest(Encoding.UTF8.GetBytes(
            "{\"tools\":[{\"type\":\"namespace\",\"name\":\"malachi\",\"tools\":[" + catalog + "]}]}")));
        var names = filtered.RootElement.GetProperty("tools")[0].GetProperty("tools").EnumerateArray()
            .Select(t => t.GetProperty("name").GetString()!).ToHashSet(StringComparer.Ordinal);
        Assert.True(names.SetEquals(tools));
        foreach (var name in new[] { "create_draft", "send_message", "transition_issue" })
        {
            Assert.Throws<AssistantProviderException>(() => gate.ValidateResponseEvent(
                "{\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"namespace\":\"malachi\",\"name\":\"" + name + "\"}}"));
        }
        gate.ValidateResponseEvent("{\"item\":{\"type\":\"function_call\",\"namespace\":\"malachi\",\"name\":\"annotate_case\"}}");
        var withoutAnnotate = "{\"tools\":[{\"type\":\"namespace\",\"name\":\"malachi\",\"tools\":["
            + string.Join(",", tools.Where(t => t != "annotate_case").Select(t => "{\"type\":\"function\",\"name\":\"" + t + "\"}")) + "]}]}";
        Assert.Equal("codex_required_tools_missing",
            Assert.Throws<AssistantProviderException>(() => gate.FilterRequest(Encoding.UTF8.GetBytes(withoutAnnotate))).Code);
    }

    /// <summary>
    /// The provider refuses a spec outside its policy, and a board session
    /// without the board consent (the panel's does not stand for it), before
    /// any process exists; Available and Connected report the runtime and
    /// the account.
    /// </summary>
    [Fact]
    public async Task ProviderChecksPolicyAndBoardConsentFirst()
    {
        var directory = Path.Combine(Path.GetTempPath(), "malachi-codex-policy-" + Guid.NewGuid().ToString("N"));
        var boardConsent = false;
        var connected = false;
        var provider = new CodexAssistantProvider(new CodexAssistantOptions
        {
            Executable = () => null,
            Bridge = "",
            Socket = "",
            Directory = directory,
            Environment = new Dictionary<string, string>(),
            Model = () => "m",
            HasConsent = () => true,
            AcceptConsent = () => { },
            HasBoardConsent = () => boardConsent,
            AcceptBoardConsent = () => boardConsent = true,
            Connected = () => connected,
        }, new Tokens());
        Assert.False(provider.Available);
        Assert.False(provider.Connected);
        connected = true;
        Assert.True(provider.Connected);
        Assert.False(provider.HasBoardConsent);
        var outside = new AssistantSessionSpec { SystemPrompt = "S", ToolPolicy = AssistantToolPolicy.ReplyOnly, BridgeArgs = ["--reply-only", "m", "--allow-send"] };
        Assert.Equal("chatgpt_invalid_tool_policy",
            (await Assert.ThrowsAsync<AssistantProviderException>(() => provider.OpenAsync(outside, TestContext.Current.CancellationToken))).Code);
        var board = new AssistantSessionSpec { SystemPrompt = "S", ToolPolicy = AssistantToolPolicy.ReplyOnly, BridgeArgs = Assistant.SuggestReplyBridgeArgs("m"), BoardConsent = true };
        Assert.Equal("chatgpt_consent_required",
            (await Assert.ThrowsAsync<AssistantProviderException>(() => provider.OpenAsync(board, TestContext.Current.CancellationToken))).Code);
        provider.AcceptBoardConsent();
        Assert.True(provider.HasBoardConsent);
        // With the board consent it gets as far as the missing runtime.
        Assert.Equal("codex_not_found",
            (await Assert.ThrowsAsync<AssistantProviderException>(() => provider.OpenAsync(board, TestContext.Current.CancellationToken))).Code);
        Assert.False(Directory.Exists(directory));
    }

    [Fact]
    public void MailNamespaceRemainsDirectForCodeModeModels()
    {
        var method = typeof(CodexAssistantSession).GetMethod("Arguments", System.Reflection.BindingFlags.NonPublic | System.Reflection.BindingFlags.Static)!;
        var args = (List<string>)method.Invoke(null, ["http://127.0.0.1:1/v1", "/private/fixture"])!;
        Assert.Contains("features.code_mode.direct_only_tool_namespaces=[\"malachi\"]", args);
        Assert.Contains("features.shell_tool=false", args);
    }

    [Theory]
    [InlineData("gpt-6.1", false, false)]
    [InlineData("gpt-6.1", true, false)]
    [InlineData("gpt-6.1", true, true)]
    [InlineData("gpt-6-sol", false, false)]
    [InlineData("gpt-6-sol", true, false)]
    [InlineData("gpt-5.6-sol", false, false)]
    [InlineData("gpt-5.6-sol", true, false)]
    [InlineData("", false, false)]
    [InlineData("", true, false)]
    public async Task NativeAppServerCanary(string model, bool withTools, bool failAfterDraft)
    {
        var executable = Environment.GetEnvironmentVariable("MALACHI_TEST_CODEX") ?? "/usr/lib/chatgpt/resources/codex";
        Assert.SkipUnless(File.Exists(executable), "native Codex is required for this optional compatibility canary");
        var bridge = Environment.GetEnvironmentVariable("MALACHI_TEST_MCP") ?? "/tmp/malachi-codex-canary-mcp";
        Assert.SkipUnless(!withTools || File.Exists(bridge), "build the synthetic MCP fixture for the optional tool canary");
        var directory = Path.Combine(Path.GetTempPath(), "malachi-codex-canary-" + Guid.NewGuid().ToString("N"));
        var handler = new ResponseHandler(withTools, failAfterDraft);
        var provider = new CodexAssistantProvider(new CodexAssistantOptions
        {
            Executable = () => executable,
            Bridge = withTools ? bridge : "",
            Socket = "",
            Directory = directory,
            Environment = new Dictionary<string, string> { ["SystemRoot"] = @"C:\Windows" },
            Model = () => model,
            HasConsent = () => true,
            AcceptConsent = () => { },
            InferenceHandler = () => handler,
        }, new Tokens());
        using var deadline = new CancellationTokenSource(TimeSpan.FromSeconds(45), TimeProvider.System);
        IAssistantSession? session = null;
        try
        {
            session = await provider.OpenAsync(new AssistantSessionSpec { SystemPrompt = "Answer the fixture", ToolPolicy = withTools ? AssistantToolPolicy.Panel : AssistantToolPolicy.None }, deadline.Token);
            var called = new List<string>();
            var result = new TaskCompletionSource<AssistantEvent>(TaskCreationOptions.RunContinuationsAsynchronously);
            session.EventsReceived += (_, events) =>
            {
                foreach (var value in events)
                {
                    if (value.Kind == AssistantEventKind.Result) { result.TrySetResult(value); }
                    if (value.Kind == AssistantEventKind.ToolUse) { called.Add(value.Tool); }
                }
            };
            session.Exited += (_, exit) => result.TrySetException(new InvalidOperationException(exit.Description));
            await session.SubmitAsync("CANARY INPUT", deadline.Token);
            var answer = await result.Task.WaitAsync(deadline.Token);
            Assert.Equal(!failAfterDraft, answer.Success);
            Assert.Equal(failAfterDraft ? "chatgpt_inference_refused" : "safe answer", answer.ResultText);
            Assert.NotNull(handler.Body);
            using var request = JsonDocument.Parse(handler.Body);
            Assert.Equal(withTools ? 1 : 0, request.RootElement.GetProperty("tools").GetArrayLength());
            if (withTools)
            {
                Assert.Equal("malachi", request.RootElement.GetProperty("tools")[0].GetProperty("name").GetString());
                Assert.Equal(7, request.RootElement.GetProperty("tools")[0].GetProperty("tools").GetArrayLength());
                Assert.Equal([failAfterDraft ? "create_draft" : "read_message"], called);
                Assert.Equal(2, handler.Requests);
            }
            Assert.False(request.RootElement.GetProperty("store").GetBoolean());
            Assert.True(handler.HadToken);
            foreach (var file in Directory.EnumerateFiles(directory, "*", SearchOption.AllDirectories))
            {
                using var input = new FileStream(file, FileMode.Open, FileAccess.Read, FileShare.ReadWrite | FileShare.Delete);
                if (input.Length > CodexJsonRpc.FrameLimit) { throw new InvalidOperationException("unbounded native canary state"); }
                using var buffer = new MemoryStream();
                await input.CopyToAsync(buffer, deadline.Token);
                Assert.DoesNotContain("CANARY INPUT", Encoding.UTF8.GetString(buffer.ToArray()), StringComparison.Ordinal);
                Assert.DoesNotContain("safe answer", Encoding.UTF8.GetString(buffer.ToArray()), StringComparison.Ordinal);
                Assert.DoesNotContain("SYNTHETIC TOOL RESULT", Encoding.UTF8.GetString(buffer.ToArray()), StringComparison.Ordinal);
            }
        }
        finally
        {
            session?.Terminate();
            if (session is not null) { await session.Completion.WaitAsync(deadline.Token); }
            if (Directory.Exists(directory)) { Directory.Delete(directory, recursive: true); }
        }
    }

    [Theory]
    [InlineData(null, ": heartbeat\n\ndata: {\"type\":\"response.created\",\"response\":{\"output\":[]}}\n\n", true)]
    [InlineData("Text/Event-Stream; charset=utf-8", "data: {\"type\":\"response.created\"}\n\n", true)]
    [InlineData(null, "data: {broken}\n\n", false)]
    [InlineData("text/event-stream", "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"namespace\":\"other\",\"name\":\"exec_command\"}}\n\n", false)]
    [InlineData(null, "<html>hostile</html>\n\n", false)]
    [InlineData(null, "{\"type\":\"response.created\"}", false)]
    [InlineData(null, ": heartbeat\n\n", false)]
    [InlineData("application/json", "data: {\"type\":\"response.created\"}\n\n", false)]
    [InlineData(null, "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"namespace\":\"other\",\"name\":\"exec_command\"}}\n\n", false)]
    public async Task MissingMimeRequiresValidatedResponsesFrameBeforeSuccess(string? mime, string body, bool accepted)
    {
        var handler = new StreamShapeHandler(mime, body);
        await using var gate = new CodexInferenceGate(new Tokens(), [], handler);
        using var local = new HttpClient();
        using var request = new HttpRequestMessage(HttpMethod.Post, gate.BaseUrl + "/responses");
        request.Headers.Authorization = new System.Net.Http.Headers.AuthenticationHeaderValue("Bearer", gate.Credential);
        request.Content = new StringContent("{\"input\":[{\"role\":\"user\",\"content\":\"synthetic\"}],\"tools\":[]}", Encoding.UTF8, "application/json");
        using var response = await local.SendAsync(request, TestContext.Current.CancellationToken);
        var output = await response.Content.ReadAsStringAsync(TestContext.Current.CancellationToken);
        Assert.True(handler.AcceptedSse);
        Assert.Equal(accepted ? HttpStatusCode.OK : HttpStatusCode.BadGateway, response.StatusCode);
        if (accepted) { Assert.Contains("response.created", output, StringComparison.Ordinal); }
        else
        {
            Assert.Equal("{\"error\":{\"message\":\"ChatGPT inference refused\"}}", output);
            Assert.DoesNotContain("hostile", output, StringComparison.Ordinal);
            Assert.DoesNotContain("exec_command", output, StringComparison.Ordinal);
        }
    }

    private sealed class StreamShapeHandler(string? mime, string body) : HttpMessageHandler
    {
        public bool AcceptedSse { get; private set; }
        protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            AcceptedSse = request.Headers.Accept.ToString() == "text/event-stream";
            var content = new ByteArrayContent(Encoding.UTF8.GetBytes(body));
            if (mime is not null) { content.Headers.TryAddWithoutValidation("Content-Type", mime); }
            return Task.FromResult(new HttpResponseMessage(HttpStatusCode.OK) { Content = content });
        }
    }

    private sealed class Tokens : IChatGptAccessTokenSource
    {
        public CancellationToken SessionCancellation => CancellationToken.None;
        public Task<string> GetAccessTokenAsync(CancellationToken cancellationToken = default) => Task.FromResult("fixture-token-never-real");
    }

    private sealed class ResponseHandler(bool withTools = false, bool failAfterDraft = false) : HttpMessageHandler
    {
        public byte[]? Body { get; private set; }
        public bool HadToken { get; private set; }
        public int Requests { get; private set; }
        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            Requests++;
            Body = await request.Content!.ReadAsByteArrayAsync(cancellationToken);
            HadToken = request.Headers.Authorization?.Parameter == "fixture-token-never-real";
            Assert.Equal("https://api.openai.com/v1/responses", request.RequestUri!.AbsoluteUri);
            if (failAfterDraft && Requests > 1)
            {
                return new HttpResponseMessage(HttpStatusCode.ServiceUnavailable) { Content = new StringContent("upstream unavailable") };
            }
            var item = "{\"type\":\"message\",\"id\":\"msg_canary\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"safe answer\",\"annotations\":[]}]}";
            if (withTools && Requests == 1)
            {
                item = "{\"type\":\"function_call\",\"id\":\"fc_canary\",\"call_id\":\"call_canary\",\"namespace\":\"malachi\",\"name\":\"" + (failAfterDraft ? "create_draft" : "read_message") + "\",\"arguments\":\"{}\",\"status\":\"completed\"}";
            }
            var response = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_canary\",\"status\":\"in_progress\",\"output\":[]}}\n\n"
                + "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":" + item + "}\n\n"
                + (withTools && Requests == 1 ? "" : "data: {\"type\":\"response.output_text.delta\",\"item_id\":\"msg_canary\",\"output_index\":0,\"content_index\":0,\"delta\":\"safe answer\"}\n\n")
                + "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":" + item + "}\n\n"
                + "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_canary\",\"status\":\"completed\",\"output\":[" + item + "],\"usage\":{\"input_tokens\":1,\"output_tokens\":2,\"total_tokens\":3}}}\n\n";
            return new HttpResponseMessage(HttpStatusCode.OK) { Content = new StringContent(response, Encoding.UTF8, "text/event-stream") };
        }
    }
}
