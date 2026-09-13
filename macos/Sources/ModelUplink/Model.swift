import AppKit
import SwiftUI

@MainActor final class UplinkModel: ObservableObject {
    @Published var managedSetup = false
    @Published var localURL = "http://127.0.0.1:1234/v1"
    @Published var localKey = ""
    @Published var localModel = ""
    @Published var localModels: [String] = []
    @Published var servers: [LocalServer] = []
    @Published var localSources: [String: LocalServer] = [:]
    @Published var sourceMessage = ""
    @Published var testedSource = ""
    var sourceFingerprint: String { localURL + "\n" + localKey + "\n" + localModel }
    var sourceReady: Bool { !localModel.isEmpty && testedSource == sourceFingerprint }
    @Published var startupEnabled = true
    @Published var email = ""
    @Published var code = ""
    @Published var challenge: String?
    @Published var account: Account?
    @Published var unavailable = false
    @Published var retired: [Endpoint] = []
    @Published var otherTrial: Endpoint?
    @Published var connectionLimit: Endpoint?
    @Published var keepingOtherComputer = false
    @Published var preparingMove = false
    private var revision = 0
    @Published var endpoints: [Endpoint] = []
    @Published var models: [String] = []
    @Published var sharedModels: [String: [String]] = [:]
    @Published var memoryReleasePending: Set<String> = []
    @Published var activity: [String: ModelActivity] = [:]
    @Published var selectedModel = "llama3.2:3b"
    @Published var endpointName = ""
    @Published var nameSuggestions: [String] = []
    @Published var progress: SetupProgress?
    @Published var progressUpdatedAt = Date()
    @Published var connecting = false
    @Published var busy = false
    @Published var loading = true
    @Published var message = ""
    @Published var error = ""
    @Published var copied = ""
    @Published var paused: Set<String> = Set(UserDefaults.standard.stringArray(forKey: "pausedConnections") ?? [])
    private let bridge: any DesktopHelper
    init(bridge: any DesktopHelper = Bridge()) { self.bridge = bridge }
    private var memoryKeys: [String: String] = [:]
    var connected: Bool { !trialFinished && endpoints.contains { $0.online && !paused.contains($0.slug) && (localSources[$0.slug] == nil || ["server_available", "ready"].contains(localSources[$0.slug]!.status)) } }
    var working: Bool { busy || loading }
    var paid: Bool { account?.billing_state == "active" || account?.billing_state == "grace" }
    var trialFinished: Bool { !paid && (account?.trial_status == "expired" || account?.trial_status == "exhausted") }
    var allowance: String {
        if account?.billing_state == "active" || account?.billing_state == "grace" { return "Your subscription is ready" }
        if trialFinished { return "Subscribe to keep your model connected" }
        if account?.trial_status == "active" {
            let megabytes = Double(account?.trial_transfer_remaining ?? 0) / 1_000_000
            return "\(account?.trial_remaining ?? 0) free requests · \(String(format: "%.1f", megabytes)) MB left"
        }
        return "500 requests · 250 MB · 7 days · no card needed"
    }

    func request(_ fields: [String: String]) async -> Reply? {
        do {
            let reply = try await bridge.request(fields) { [weak self] progress in
                await self?.receive(progress)
            }
            if let problem = reply.error { error = problem; return nil }
            return reply
        } catch { self.error = (error as? AppError)?.localizedDescription ?? "Something interrupted the connection. Please try again."; return nil }
    }
    private func receive(_ progress: SetupProgress) {
        self.progress = progress
        if !connecting { message = progress.message }
        progressUpdatedAt = Date()
    }
    func refresh(quiet: Bool = false) async {
        guard !busy else { return }
        let startedRevision = revision
        if !quiet { loading = true; error = "" }
        defer { loading = false }
        guard let reply = await request(["action": "state"]) else { return }
        guard startedRevision == revision, !busy else { return }
        apply(reply)
    }
    func apply(_ reply: Reply) {
        if let enabled = reply.startup_enabled { startupEnabled = enabled }
        unavailable = reply.unavailable ?? false
        account = reply.account
        endpoints = reply.endpoints ?? []
        if otherTrial?.id != reply.other_trial?.id { keepingOtherComputer = false; preparingMove = false }
        otherTrial = reply.other_trial
        connectionLimit = reply.connection_limit
        retired = reply.retired ?? []
        models = reply.models ?? []
        nameSuggestions = reply.name_suggestions ?? []
        if endpointName.isEmpty, let suggestion = nameSuggestions.first { endpointName = suggestion }
        sharedModels = (reply.shared_models ?? [:]).compactMapValues { $0 }
        activity = reply.activity ?? [:]
 localSources = reply.local_sources ?? [:]
        if let stopped = reply.stopped { for (slug,value) in stopped { if value { paused.insert(slug) } else { paused.remove(slug) } } }
        memoryReleasePending = Set((reply.memory_release_pending ?? [:]).filter { $0.value }.map { $0.key })
        if let first = models.first, !models.contains(selectedModel) { selectedModel = first }
    }
    func inspectSource(_ action: String) async {
        guard !working else { return }
        revision += 1
        busy = true; error = ""; message = "Checking the local model server…"
        defer { busy = false; message = "" }
        let fingerprint = sourceFingerprint
        guard let reply = await request(["action": action, "local_url": localURL, "local_key": localKey, "model": localModel]) else { return }
        if action == "discover_servers" {
            servers = reply.servers ?? []
            sourceMessage = servers.isEmpty ? "No local APIs found. Open your runtime app and enable its API server." : "Select a detected API address, or enter your own."
        }
        if let source = reply.source {
            localModels = source.models ?? []; sourceMessage = source.message ?? ""
            if !localModels.contains(localModel) { localModel = localModels.first ?? "" }
            testedSource = reply.ready == true ? fingerprint : ""
        }
    }
    func modelsFor(_ endpoint: Endpoint) -> [String] { localSources[endpoint.slug]?.models ?? models }
    func connectionStatus(_ endpoint: Endpoint) -> String {
        if paused.contains(endpoint.slug) { return "STOPPED" }
        if trialFinished { return "TRIAL FINISHED" }
        if !endpoint.online { return "CONNECTING" }
        if let source = localSources[endpoint.slug], !["server_available", "ready"].contains(source.status) {return "LOCAL SERVER NEEDS ATTENTION"}
        return "ONLINE"
    }
    func friendAccess() { NSWorkspace.shared.open(URL(string: "https://modeluplink.com/dashboard/#api-keys")!) }
    func sendCode() async {
        guard !working else { return }
        revision += 1
        busy = true; error = ""; message = "Sending your code…"
        defer { busy = false; message = "" }
        if let reply = await request(["action": "start_code", "email": email]) { challenge = reply.challenge_id; code = "" }
    }
    func verify() async {
        guard !working, let challenge else { return }
        revision += 1
        busy = true; error = ""; message = "Checking your code…"
        defer { busy = false; message = "" }
        if let reply = await request(["action": "verify_code", "challenge_id": challenge, "code": code]) {
            self.challenge = nil; code = ""; apply(reply)
            if let notice = reply.notice { error = notice }
        }
    }
    func connect(moveTrialID: String? = nil) async {
        guard !working else { return }
        revision += 1
        busy = true; connecting = true; error = ""; message = ""
        receive(SetupProgress(event: "progress", stage: "account", message: "Checking your account…"))
        defer { busy = false; connecting = false; message = "" }

        var fields = ["action": "connect", "model": selectedModel]
        if !managedSetup && !localModel.isEmpty {fields["local_url"] = localURL; fields["local_key"] = localKey; fields["model"] = localModel}
        if paid { fields["endpoint_name"] = endpointName }
        if let moveTrialID { fields["action"] = "move_trial"; fields["move_trial_id"] = moveTrialID }
        guard let reply = await request(fields) else { return }
        if let endpoint = reply.endpoint {
            endpoints.append(endpoint)
 localKey = ""; testedSource = ""
 if let sources = reply.local_sources {localSources.merge(sources) {_,new in new}}
            otherTrial = nil; connectionLimit = nil; preparingMove = false; keepingOtherComputer = false
            if let key = reply.api_key { saveKey(key, for: endpoint.id) }
        }
        if let notice = reply.notice { error = notice }
        if let shared = reply.shared_models { sharedModels.merge(shared.compactMapValues { $0 }) { _,new in new } }
    }
    func act(_ action: String, endpoint: Endpoint) async {
        guard !working else { return }
        revision += 1
        busy = true; error = ""; message = action == "resume" ? "Reconnecting…" : "Updating your connection…"
        defer { busy = false; message = "" }
        guard let reply = await request(["action": action, "slug": endpoint.slug]) else { return }
        if action == "pause" {
            paused.insert(endpoint.slug)
            if reply.memory_release_pending?[endpoint.slug] == false { retired.removeAll { $0.id == endpoint.id } }
            if reply.memory_release_pending?[endpoint.slug] == true { memoryReleasePending.insert(endpoint.slug) } else { memoryReleasePending.remove(endpoint.slug) }
        }
        if let notice = reply.notice { error = notice }
        if action == "resume" { paused.remove(endpoint.slug); memoryReleasePending.remove(endpoint.slug) }
        if action == "delete" {
            paused.remove(endpoint.slug); KeyStore.remove(endpoint.id); memoryKeys.removeValue(forKey: endpoint.id)
            endpoints.removeAll { $0.id == endpoint.id }
        }
        UserDefaults.standard.set(Array(paused), forKey: "pausedConnections")
        if let key = reply.api_key { saveKey(key, for: endpoint.id); copy(key, label: "API key") }
    }
    func updateSharing(_ endpoint: Endpoint, models: [String]) async -> Bool {
        guard !working, !models.isEmpty else { return false }
        revision += 1
        busy = true; error = ""; message = "Checking selected models…"
        defer { busy = false; message = "" }
        guard let data = try? JSONEncoder().encode(models), let modelIDs = String(data: data, encoding: .utf8) else { return false }
        guard let reply = await request(["action": "share_models", "slug": endpoint.slug, "model_ids": modelIDs]) else { return false }
        if let shared = reply.shared_models { sharedModels.merge(shared.compactMapValues { $0 }) { _, new in new } }
        paused.remove(endpoint.slug)
        UserDefaults.standard.set(Array(paused), forKey: "pausedConnections")
        return true
    }
    func copyKey(_ endpoint: Endpoint) async {
        if let key = memoryKeys[endpoint.id] ?? KeyStore.read(endpoint.id) { copy(key, label: "API key") }
        else { await act("new_key", endpoint: endpoint) }
    }
    private func saveKey(_ key: String, for id: String) {
        memoryKeys[id] = key
        do { try KeyStore.save(key, for: id) } catch { self.error = error.localizedDescription }
    }
    func copy(_ text: String, label: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
        copied = "\(label) copied"
        Task { try? await Task.sleep(for: .seconds(3)); if copied == "\(label) copied" { copied = "" } }
    }
    func signOut() async {
        guard !working else { return }
        revision += 1
        busy = true; error = ""
        defer { busy = false }
        if await request(["action": "sign_out"]) != nil { localKey = ""; testedSource = ""; account = nil; endpoints = []; retired = []; otherTrial = nil; connectionLimit = nil; preparingMove = false; keepingOtherComputer = false; memoryKeys = [:]; challenge = nil; code = "" }
    }
    func setStartup(_ enabled: Bool) async {
        guard !working else { return }
        revision += 1
        busy = true; error = ""; message = "Updating startup…"
        defer { busy = false; message = "" }
        if let reply = await request(["action": "startup", "enabled": enabled ? "true" : "false"]),
           let saved = reply.startup_enabled { startupEnabled = saved }
    }
    func stopAll() async {
        guard !working else { return }
        revision += 1
        busy = true; error = ""; message = "Stopping sharing…"
        _ = await request(["action": "stop_all"])
        busy = false; message = ""
        let stopError = error
        await refresh(quiet: true)
        if !stopError.isEmpty { error = stopError }
    }
    func updates() { NSWorkspace.shared.open(URL(string: "https://modeluplink.com/download/")!) }
    func dashboard() { NSWorkspace.shared.open(URL(string: "https://modeluplink.com/dashboard/")!) }
    func billing() { NSWorkspace.shared.open(URL(string: paid ? "https://modeluplink.com/dashboard/#billing-panel" : "https://modeluplink.com/dashboard/?plan=agent#billing-panel")!) }
}
