// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// The native gateway enforces policy before the model sees any built-in tools,
/// and before an SSE tool envelope reaches Codex. The child never gets OAuth.
public struct CodexPolicy: Sendable {
    public static let frameLimit = 16 << 20
    public let tools: Set<String>
    public init(allowed: [String]) {
        tools = Set(allowed.map { $0.hasPrefix("mcp__malachi__") ? String($0.dropFirst(14)) : $0 })
    }

    /// The gate a session passes before it starts anything (Go
    /// `toolPolicy`, C# `Assistant.PolicyAllows`): only the application's
    /// known capability sets and complete, immutable bridge argument forms.
    /// Exactly three shapes of `tools.bridgeArgs` pass: none (the panel's
    /// read and draft tools), `--reply-only <id>` (a suggested reply's
    /// tools), and `--allow-triage --triage-run <id> --triage-max <n>` with
    /// `n` in `Assistant.triageMaxRange` (the triage's tools); every tool
    /// must carry the bridge's prefix, be one of that set's and appear once.
    /// Anything else, `--allow-modify` and `--allow-send` included, throws
    /// `chatgpt_invalid_tool_policy`: fails closed. No tools at all (nil)
    /// pass with no bridge.
    public static func check(_ tools: AssistantRequest.Tools?) throws {
        guard let tools else { return }
        let refused = ChatGPTFailure("chatgpt_invalid_tool_policy")
        let args = tools.bridgeArgs
        let known: [String]
        if args.isEmpty {
            known = Assistant.allowedTools
        } else if args.count == 2, args[0] == "--reply-only", validIdentifier(args[1]) {
            known = Assistant.suggestReplyTools
        } else if args.count == 5, args[0] == "--allow-triage", args[1] == "--triage-run", validIdentifier(args[2]),
                  args[3] == "--triage-max"
        {
            // Digits only, as Go's strconv.Atoi takes them (no sign, no
            // spaces), and within the bridge's range.
            guard !args[4].isEmpty, args[4].utf8.count <= 4, args[4].utf8.allSatisfy({ $0 >= 0x30 && $0 <= 0x39 }),
                  let n = Int(args[4]), Assistant.triageMaxRange.contains(n)
            else { throw refused }
            known = Assistant.triageTools
        } else {
            throw refused
        }
        let bare = Set(known.map { String($0.dropFirst(prefix.count)) })
        var seen = Set<String>()
        for name in tools.allowed {
            guard name.hasPrefix(prefix) else { throw refused }
            let tool = String(name.dropFirst(prefix.count))
            guard bare.contains(tool), seen.insert(tool).inserted else { throw refused }
        }
    }

    /// The bridge's prefix of the tools' names.
    private static let prefix = "mcp__malachi__"

    /// A run or message id the bridge takes as an argument: not empty, at
    /// most 512 bytes, not a flag, no NUL, line break, tab or space.
    static func validIdentifier(_ s: String) -> Bool {
        !s.isEmpty && s.utf8.count <= 512 && !s.hasPrefix("-")
            && !s.unicodeScalars.contains { ["\u{0}", "\r", "\n", "\t", " "].contains($0) }
    }
    public func filterRequest(_ data: Data) throws -> Data {
        guard var root = try JSONSerialization.jsonObject(with: data) as? [String: Any],
              root["previous_response_id"] == nil, root["conversation"] == nil else {
            throw ChatGPTFailure("codex_persistent_upstream_history_denied")
        }
        var catalog = root["tools"] as? [Any] ?? []
        if let input = root["input"] as? [Any] {
            var retained: [Any] = []
            for item in input {
                if let envelope = item as? [String: Any], envelope["type"] as? String == "additional_tools" {
                    guard let entries = envelope["tools"] as? [Any] else { throw ChatGPTFailure("codex_invalid_tool_catalog") }
                    catalog.append(contentsOf: entries)
                } else {
                    retained.append(item)
                }
            }
            root["input"] = retained
        }
        var accepted: [[String: Any]] = []
        var seen = Set<String>()
        for entry in catalog {
            guard let namespace = entry as? [String: Any] else { continue }
            guard namespace["type"] as? String == "namespace", namespace["name"] as? String == "malachi" else { continue }
            guard let entries = namespace["tools"] as? [Any] else { throw ChatGPTFailure("codex_invalid_tool_catalog") }
            for entry in entries {
                guard let tool = entry as? [String: Any] else { continue }
                guard tool["type"] as? String == "function", let name = tool["name"] as? String, tools.contains(name) else { continue }
                guard seen.insert(name).inserted else { throw ChatGPTFailure("codex_duplicate_tool") }
                accepted.append(tool)
            }
        }
        guard seen == tools else { throw ChatGPTFailure("codex_required_tools_missing") }
        root["tools"] = accepted.isEmpty ? [] : [["type": "namespace", "name": "malachi", "description": "Malachi Mail tools", "tools": accepted]]
        root["store"] = false; root["stream"] = true; root["parallel_tool_calls"] = false
        root.removeValue(forKey: "tool_choice")
        return try JSONSerialization.data(withJSONObject: root)
    }
    /// A missing upstream MIME is accepted only after a validated Responses SSE frame.
    public static func isMissingStreamType(_ raw: String?) throws -> Bool {
        guard let raw else { return true }
        let media = raw.split(separator: ";", omittingEmptySubsequences: false)[0].trimmingCharacters(in: .whitespacesAndNewlines)
        guard media.lowercased() == "text/event-stream" else { throw ChatGPTFailure("codex_gate_non_streaming_response") }
        return false
    }
    public func validateInitialStreamEvent(_ data: String) throws {
        guard let bytes = data.data(using: .utf8),
              let root = (try? JSONSerialization.jsonObject(with: bytes)) as? [String: Any],
              let type = root["type"] as? String, type.hasPrefix("response.") else {
            throw ChatGPTFailure("codex_gate_non_streaming_response")
        }
        try validateEvent(data)
    }
    public func validateEvent(_ data: String) throws {
        if data == "[DONE]" { return }
        guard let bytes = data.data(using: .utf8), let root = try JSONSerialization.jsonObject(with: bytes) as? [String: Any] else {
            throw ChatGPTFailure("codex_invalid_stream_event")
        }
        if let item = root["item"] as? [String: Any] { try validateItem(item) }
        if let response = root["response"] as? [String: Any], let output = response["output"] as? [[String: Any]] {
            for item in output { try validateItem(item) }
        }
    }
    private func validateItem(_ item: [String: Any]) throws {
        let type = item["type"] as? String ?? ""
        if ["message", "reasoning", "compaction"].contains(type) { return }
        if type == "function_call", item["namespace"] as? String == "malachi", tools.contains(item["name"] as? String ?? "") { return }
        throw ChatGPTFailure("codex_disallowed_inference_tool")
    }
    public static func arguments(baseURL: String, home: URL) -> [String] {
        func quote(_ s: String) -> String { "\"" + s.replacingOccurrences(of: "\\", with: "\\\\").replacingOccurrences(of: "\"", with: "\\\"") + "\"" }
        var settings = [
            "model_provider=\"malachi_chatgpt\"", "model_providers.malachi_chatgpt.name=\"ChatGPT plan\"",
            "model_providers.malachi_chatgpt.base_url=" + quote(baseURL),
            "model_providers.malachi_chatgpt.env_key=\"MALACHI_CODEX_GATE_CREDENTIAL\"",
            "model_providers.malachi_chatgpt.wire_api=\"responses\"",
            "model_providers.malachi_chatgpt.requires_openai_auth=false", "model_providers.malachi_chatgpt.supports_websockets=false",
            "model_providers.malachi_chatgpt.request_max_retries=0", "model_providers.malachi_chatgpt.stream_max_retries=0",
            "features.code_mode.direct_only_tool_namespaces=[\"malachi\"]",
            "web_search=\"disabled\"", "project_doc_max_bytes=0", "history.persistence=\"none\"",
            "log_dir=" + quote(home.appendingPathComponent("logs").path), "sqlite_home=" + quote(home.appendingPathComponent("state").path),
            "analytics.enabled=false", "feedback.enabled=false", "otel.log_user_prompt=false", "otel.log_agent_responses=false",
            "otel.log_guardian_assessments=false", "otel.exporter=\"none\"", "otel.trace_exporter=\"none\"", "otel.metrics_exporter=\"none\""
        ]
        for feature in ["shell_tool", "unified_exec", "apps", "plugins", "hooks", "multi_agent", "image_generation", "browser_use", "view_image", "skill_search", "skill_mcp_dependency_install", "goals", "sleep_tool"] {
            settings.append("features.\(feature)=false")
        }
        return ["app-server", "--listen", "stdio://"] + settings.flatMap { ["-c", $0] }
    }
    public static func environment(_ parent: [String: String]) -> [String: String] {
        var result: [String: String] = [:]
        for key in ["PATH", "LANG", "LC_ALL", "LC_CTYPE", "TMPDIR", "USER", "LOGNAME", "HOME", "SSL_CERT_FILE", "SSL_CERT_DIR"] {
            result[key] = parent[key]
        }
        return result
    }
}
