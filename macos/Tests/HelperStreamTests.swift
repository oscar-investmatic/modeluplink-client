import Foundation

@main struct HelperStreamTests {
    static func main() throws {
        var stream = HelperStream()
        let line = Data(
            "{\"event\":\"progress\",\"stage\":\"download\",\"message\":\"Downloading…\",\"completed\":25,\"total\":100}\n"
                .utf8)
        // Exercise arbitrary byte boundaries, including within UTF-8 characters.
        var updates: [SetupProgress] = []
        for byte in line { updates += try stream.append(Data([byte])) }
        precondition(updates.count == 1 && updates[0].fraction == 0.25)
        precondition(stream.reply == nil, "progress must arrive before final result")
        _ = try stream.append(
            Data(
                "{\"ready\":true,\"api_key\":\"test-key\",\"name_suggestions\":[\"quiet-rover\",\"silver-comet\",\"bold-atlas\"]}\n"
                    .utf8))
        let reply = try stream.finish()
        precondition(reply.api_key == "test-key" && reply.name_suggestions?.count == 3)
        var incomplete = HelperStream()
        _ = try incomplete.append(line)
        do { _ = try incomplete.finish(); fatalError("accepted progress without result") } catch {}
        var truncated = HelperStream()
        _ = try truncated.append(Data("{\"ready\":true}".utf8))
        do { _ = try truncated.finish(); fatalError("accepted truncated frame") } catch {}
        do { _ = try stream.append(line); fatalError("accepted events after result") } catch {}
        print("Helper stream tests passed")
    }
}
