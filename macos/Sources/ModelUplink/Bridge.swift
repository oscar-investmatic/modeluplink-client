import Foundation
import CryptoKit
import Security

struct Account: Codable, Sendable {
    let email: String
    let billing_state: String
    let trial_status: String
    let trial_remaining: Int
    let trial_transfer_remaining: Int64
}
struct Endpoint: Codable, Identifiable, Sendable {
    let id: String
    let slug: String
    let url: String
    let online: Bool
}
struct ModelActivity: Codable, Sendable {
    let model: String
    let running: Int
    let waiting: Int
}
struct LocalServer: Codable, Sendable {
    let url: String
    let status: String
    var message: String?
    let models: [String]?
    let ownership: String
}
struct Reply: Codable, Sendable {
    var source: LocalServer?
    var servers: [LocalServer]?
    var local_sources: [String: LocalServer]?
    var startup_enabled: Bool?
    var other_trial: Endpoint?
    var connection_limit: Endpoint?
    var retired: [Endpoint]?
    var stopped: [String: Bool]?
    var memory_release_pending: [String: Bool]?
    var shared_models: [String: [String]?]?
    var activity: [String: ModelActivity]?
    var unavailable: Bool?
    var error: String?
    var notice: String?
    var challenge_id: String?
    var account: Account?
    var endpoints: [Endpoint]?
    var models: [String]?
    var recommendation: String?
    var name_suggestions: [String]?
    var endpoint: Endpoint?
    var api_key: String?
    var ready: Bool?
}

struct SetupProgress: Codable, Sendable {
    let event: String
    let stage: String
    let message: String
    var completed: Int64?
    var total: Int64?
    var fraction: Double? {
        guard let total, total > 0 else { return nil }
        return min(1, max(0, Double(completed ?? 0) / Double(total)))
    }
}

// Incremental parsing keeps progress visible before the helper exits.
struct HelperStream {
    private var buffer = Data()
    private(set) var reply: Reply?
    mutating func append(_ chunk: Data) throws -> [SetupProgress] {
        buffer.append(chunk)
        var updates: [SetupProgress] = []
        while let end = buffer.firstIndex(of: 10) {
            guard end < 1_048_576, reply == nil else { throw AppError.helper }
            let line = Data(buffer[..<end])
            buffer.removeSubrange(...end)
            if line.allSatisfy({ $0 == 32 || $0 == 9 || $0 == 13 }) { continue }
            let header = try JSONDecoder().decode(EventHeader.self, from: line)
            if header.event == "progress" {
                updates.append(try JSONDecoder().decode(SetupProgress.self, from: line))
            } else if header.event == nil {
                reply = try JSONDecoder().decode(Reply.self, from: line)
            } else {
                throw AppError.helper
            }
        }
        guard buffer.count < 1_048_576 else { throw AppError.helper }
        return updates
    }
    func finish() throws -> Reply {
        guard buffer.isEmpty, let reply else { throw AppError.helper }
        return reply
    }
    private struct EventHeader: Decodable { let event: String? }
}

// Serialized helper requests over anonymous pipes; no shell, localhost server,
// session tokens in argv, or terminal output exposed in the interface.
protocol DesktopHelper: Sendable {
    func request(_ fields: [String: String], onProgress: @Sendable (SetupProgress) async -> Void)
        async throws -> Reply
}

actor Bridge: DesktopHelper {
    private var helper: URL?
    func request(_ fields: [String: String], onProgress: @Sendable (SetupProgress) async -> Void)
        async throws -> Reply
    {
        let executable = try prepareHelper()
        let process = Process()
        process.executableURL = executable
        process.arguments = ["_desktop"]
        let input = Pipe(), output = Pipe()
        process.standardInput = input
        process.standardOutput = output
        process.standardError = FileHandle.nullDevice
        // GUI apps do not inherit a login shell's PATH. Include the normal
        // Ollama install locations while keeping user secrets out of argv.
        var environment = ProcessInfo.processInfo.environment
        // Explicit ad-hoc preview builds retain their isolated server/profile
        // when launched from Finder. Release builds contain neither key.
        let previewSettings = [
            "MODELUPLINK_CONTROL_URL": "ModelUplinkPreviewControlURL",
            "MODELUPLINK_CONFIG_DIR": "ModelUplinkPreviewConfigDirectory",
        ]
        for (variable, key) in previewSettings where environment[variable] == nil {
            if let value = Bundle.main.object(forInfoDictionaryKey: key) as? String {
                environment[variable] = value
            }
        }
        environment["PATH"] = "/usr/bin:/bin:/usr/sbin:/sbin:/usr/local/bin:/opt/homebrew/bin"
        environment.removeValue(forKey: "MODELUPLINK_ACCOUNT_TOKEN")
        process.environment = environment
        let exit = AsyncStream<Int32>.makeStream()
        process.terminationHandler = { process in
            exit.continuation.yield(process.terminationStatus)
            exit.continuation.finish()
        }
        try process.run()
        defer {
            if process.isRunning { process.terminate() }
            try? input.fileHandleForWriting.close()
            try? output.fileHandleForReading.close()
        }
        try input.fileHandleForWriting.write(
            contentsOf: JSONSerialization.data(withJSONObject: fields))
        try input.fileHandleForWriting.close()
        var stream = HelperStream()
        while true {
            let chunk = output.fileHandleForReading.availableData
            if chunk.isEmpty { break }
            for progress in try stream.append(chunk) { await onProgress(progress) }
        }
        // An async actor may resume on a different thread after progress.
        // Await termination instead of blocking a thread-local run loop.
        for await status in exit.stream {
            guard status == 0 else { throw AppError.helper }
            return try stream.finish()
        }
        throw AppError.helper
    }

    private func prepareHelper() throws -> URL {
        if let helper { return helper }
        guard let source = Bundle.main.url(forResource: "modeluplink", withExtension: nil) else {
            throw AppError.helper
        }
        // Copy into a stable, versioned location so moving the app or ejecting
        // its installer cannot break launchd. Preserve the embedded signature.
        let bytes = try Data(contentsOf: source)
        let digest = SHA256.hash(data: bytes).map { String(format: "%02x", $0) }.joined()
        let root = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Application Support/Model Uplink/helpers/\(digest)")
        try FileManager.default.createDirectory(
            at: root, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        let destination = root.appendingPathComponent("modeluplink")
        if !FileManager.default.fileExists(atPath: destination.path) {
            try bytes.write(to: destination, options: .atomic)
            try FileManager.default.setAttributes(
                [.posixPermissions: 0o700], ofItemAtPath: destination.path)
        }
        helper = destination
        return destination
    }
}

enum AppError: LocalizedError {
    case helper, keychain
    var errorDescription: String? {
        switch self {
        case .helper: return "The connection helper couldn’t start. Try reopening Model Uplink."
        case .keychain: return "Your key couldn’t be saved in Keychain. You can still copy it now."
        }
    }
}

enum KeyStore {
    private static func query(_ id: String) -> [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: "com.modeluplink.mac.endpoint",
            kSecAttrAccount as String: id,
        ]
    }
    static func save(_ key: String, for id: String) throws {
        let attributes = [kSecValueData as String: Data(key.utf8)]
        let status = SecItemUpdate(query(id) as CFDictionary, attributes as CFDictionary)
        if status == errSecItemNotFound {
            var item = query(id)
            item[kSecValueData as String] = Data(key.utf8)
            item[kSecAttrAccessible as String] = kSecAttrAccessibleWhenUnlockedThisDeviceOnly
            guard SecItemAdd(item as CFDictionary, nil) == errSecSuccess else {
                throw AppError.keychain
            }
        } else if status != errSecSuccess {
            throw AppError.keychain
        }
    }
    static func read(_ id: String) -> String? {
        var item = query(id)
        item[kSecReturnData as String] = true
        var result: CFTypeRef?
        guard SecItemCopyMatching(item as CFDictionary, &result) == errSecSuccess,
            let data = result as? Data
        else { return nil }
        return String(data: data, encoding: .utf8)
    }
    static func remove(_ id: String) { SecItemDelete(query(id) as CFDictionary) }
}
