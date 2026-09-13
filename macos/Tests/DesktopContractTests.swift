import Foundation

struct ContractCase: Decodable {
    let name: String
    let wire: String
    let accept: Bool
    let stages: [String]
}
actor StopFailureHelper: DesktopHelper {
    var actions: [String] = []
    func request(_ fields: [String: String], onProgress: @Sendable (SetupProgress) async -> Void) async throws -> Reply {
        actions.append(fields["action"] ?? "")
        if fields["action"] == "stop_all" {
            return try JSONDecoder().decode(Reply.self, from: Data("{\"error\":\"Sharing stopped, but model memory release needs attention.\"}".utf8))
        }
        return try JSONDecoder().decode(Reply.self, from: Data("{}".utf8))
    }
}
actor StartupHelper: DesktopHelper {
    let fail: Bool
    var actions: [[String: String]] = []
    init(fail: Bool) { self.fail = fail }
    func request(_ fields: [String: String], onProgress: @Sendable (SetupProgress) async -> Void) async throws -> Reply {
        actions.append(fields)
        let body = fail ? "{\"error\":\"Startup could not be changed.\"}" : "{\"startup_enabled\":false}"
        return try JSONDecoder().decode(Reply.self, from: Data(body.utf8))
    }
}
actor SourceHelper: DesktopHelper {
 var actions: [[String:String]] = []
 func request(_ fields: [String:String],onProgress: @Sendable (SetupProgress) async -> Void) async throws -> Reply {
  actions.append(fields)
  var r=Reply()
  r.models=["friends-model"]
  r.source=LocalServer(url:"http://127.0.0.1:1234/v1",status:fields["action"] == "test_source" ? "ready" : "server_available",message:"Checked",models:["friends-model"],ownership:"external")
  r.ready=fields["action"] == "test_source"
  if fields["action"] == "connect" {r.endpoint=Endpoint(id:"ep_test",slug:"friend-gpu",url:"https://friend-gpu.example.invalid/v1",online:true)}
  return r
 }
}
@main struct DesktopContractTests {
    @MainActor static func main() async throws {
        let data = try Data(contentsOf: URL(fileURLWithPath: CommandLine.arguments[1]))
        let cases = try JSONDecoder().decode([ContractCase].self, from: data)
        let rawCases = try JSONSerialization.jsonObject(with: data) as! [[String: Any]]
        for (i, c) in cases.enumerated() {
            var stream = HelperStream()
            var stages: [String] = []
            let reply: Reply
            do {
                for byte in c.wire.utf8 { stages += try stream.append(Data([byte])).map(\.stage) }
                reply = try stream.finish()
            } catch {
                precondition(!c.accept, "Rejected \(c.name): \(error)")
                continue
            }
            precondition(c.accept, "Accepted invalid stream \(c.name)")
            precondition(stages == c.stages, "Lost progress in \(c.name)")
            let decoded = try JSONSerialization.jsonObject(with: JSONEncoder().encode(reply))
            check(decoded, rawCases[i]["expected"]!, c.name)
            let model = UplinkModel()
            model.startupEnabled = c.name == "startup_disabled"
            model.apply(reply)
            switch c.name {
            case "startup_disabled": precondition(!model.startupEnabled)
            case "startup_enabled": precondition(model.startupEnabled)
            case "trial_exhausted": precondition(model.trialFinished && !model.connected)
            case "disconnected": precondition(!model.connected)
            case "paid_connection_limit":
                precondition(model.connectionLimit?.id == "ep_existing" && model.endpoints.isEmpty && model.otherTrial == nil)
                model.apply(Reply())
                precondition(model.connectionLimit == nil, "Refresh retained a cleared connection limit")
            case "trial_transfer_required": precondition(model.otherTrial != nil && !model.preparingMove)
            case "memory_release_pending": precondition(model.paused.contains("fixture") && model.memoryReleasePending.contains("fixture") && !model.connected)
            default: break
            }
        }
        let helper = StopFailureHelper()
        let model = UplinkModel(bridge: helper)
        model.loading = false
        await model.stopAll()
        precondition(model.error.contains("memory release needs attention"), "Refresh erased stop failure")
        let actions = await helper.actions
        precondition(actions == ["stop_all", "state"] && !model.working)
        for fail in [false, true] {
            let helper = StartupHelper(fail: fail)
            let model = UplinkModel(bridge: helper)
            model.loading = false
            model.busy = true
            await model.setStartup(false)
            let blockedActions = await helper.actions
            precondition(blockedActions.isEmpty, "Startup changed while busy")
            model.busy = false
            await model.setStartup(false)
            let actions = await helper.actions
            precondition(actions == [["action": "startup", "enabled": "false"]])
            precondition(model.startupEnabled == fail && !model.working)
            precondition(model.error.isEmpty != fail)
        }
        let sourceHelper=SourceHelper()
        let sourceModel=UplinkModel(bridge:sourceHelper);sourceModel.loading=false
        precondition(!sourceModel.managedSetup && !sourceModel.sourceReady)
        sourceModel.localKey="local-secret"
        await sourceModel.inspectSource("inspect_source")
        precondition(sourceModel.localModel == "friends-model" && !sourceModel.sourceReady)
        await sourceModel.inspectSource("test_source")
        precondition(sourceModel.sourceReady)
        sourceModel.localURL="http://127.0.0.1:8000/v1"
        precondition(!sourceModel.sourceReady,"Changing the server retained readiness")
        sourceModel.localURL="http://127.0.0.1:1234/v1"
        await sourceModel.connect()
        let requests=await sourceHelper.actions
        precondition(requests.last?["local_key"] == "local-secret" && requests.last?["model"] == "friends-model")
        precondition(sourceModel.localKey.isEmpty && sourceModel.endpoints.count == 1)
        let exactIDs = ["team/model, Q4", " spaced ID "]
        let updated = await sourceModel.updateSharing(sourceModel.endpoints[0], models: exactIDs)
        precondition(updated)
        let sharedRequests = await sourceHelper.actions
        let encodedIDs = sharedRequests.last!["model_ids"]!
        let receivedIDs = try JSONDecoder().decode([String].self, from: Data(encodedIDs.utf8))
        precondition(receivedIDs == exactIDs, "Sharing changed the source model IDs")
        print("Desktop contract: \(cases.count) shared cases and Stop failure passed")
    }
    static func check(_ got: Any, _ wanted: Any, _ path: String) {
        if let expected = wanted as? [String: Any] {
            guard let actual = got as? [String: Any] else { fatalError("\(path): not an object") }
            for (key,value) in expected {
                guard let child = actual[key] else { fatalError("\(path).\(key): missing") }
                check(child, value, "\(path).\(key)")
            }
        } else if let expected = wanted as? [Any] {
            guard let actual = got as? [Any], actual.count == expected.count else { fatalError("\(path): different items") }
            for i in expected.indices { check(actual[i],expected[i],"\(path)[\(i)]") }
        } else { precondition((got as? NSObject) == (wanted as? NSObject), "\(path): changed value") }
    }
}
