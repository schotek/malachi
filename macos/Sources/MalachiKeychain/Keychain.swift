// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The Keychain half of malachi-keychain: generic-password items in the
// login keychain (no data-protection keychain, so no entitlement and no
// provisioning), one per account id and key, filed under the app id as
// the service. The first read of an item created by another code identity
// (a rebuilt ad-hoc signed helper, see macos/Makefile) brings the system's
// own "wants to use your confidential information" prompt; that is the
// Keychain's access control, not ours to bypass.
import Foundation
import Security

public enum KeychainFailure: Error, Equatable, Sendable {
    case notFound
    /// Any other Security framework status; the message is the system's.
    case status(OSStatus)

    public var message: String {
        switch self {
        case .notFound:
            return "item not found"
        case .status(let status):
            let text = SecCopyErrorMessageString(status, nil).map { $0 as String } ?? "unknown error"
            return "\(text) (OSStatus \(status))"
        }
    }
}

/// Reads, writes and removes the items. Never prints anything itself.
public struct KeychainStore: Sendable {
    public var service: String

    public init(service: String = Request.service) {
        self.service = service
    }

    public func get(_ request: Request) -> Result<String, KeychainFailure> {
        var query = base(request)
        query[kSecReturnData] = true
        query[kSecMatchLimit] = kSecMatchLimitOne
        var item: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &item)
        guard status == errSecSuccess else {
            return .failure(map(status))
        }
        guard let data = item as? Data, let value = String(data: data, encoding: .utf8) else {
            return .failure(.status(errSecDecode))
        }
        return .success(value)
    }

    /// Updates the item in place, or creates it with a label.
    public func set(_ request: Request, value: String) -> Result<Void, KeychainFailure> {
        let query = base(request)
        let attributes: [CFString: Any] = [
            kSecValueData: Data(value.utf8),
            kSecAttrLabel: request.label,
        ]
        var status = SecItemUpdate(query as CFDictionary, attributes as CFDictionary)
        if status == errSecItemNotFound {
            var add = query
            add.merge(attributes) { _, new in new }
            status = SecItemAdd(add as CFDictionary, nil)
        }
        return status == errSecSuccess ? .success(()) : .failure(map(status))
    }

    /// Removes the item; a missing one is success.
    public func delete(_ request: Request) -> Result<Void, KeychainFailure> {
        let status = SecItemDelete(base(request) as CFDictionary)
        switch status {
        case errSecSuccess, errSecItemNotFound:
            return .success(())
        default:
            return .failure(map(status))
        }
    }

    private func base(_ request: Request) -> [CFString: Any] {
        [
            kSecClass: kSecClassGenericPassword,
            kSecAttrService: service,
            kSecAttrAccount: request.itemAccount,
        ]
    }

    private func map(_ status: OSStatus) -> KeychainFailure {
        status == errSecItemNotFound ? .notFound : .status(status)
    }
}
