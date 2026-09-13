import SwiftUI
import AppKit

private let lime = Color(red: 0.66, green: 1, blue: 0.31)
private let appBackground = Color(red: 0.035, green: 0.055, blue: 0.045)

@main struct ModelUplinkApp: App {
    @StateObject private var model = UplinkModel()
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    var body: some Scene {
        Window("Model Uplink", id: "main") {
            UplinkView(model: model)
                .onAppear {
                    delegate.model = model; NSApp.activate(ignoringOtherApps: true)
                }
        }
        .defaultSize(width: 460, height: 680)
        .windowResizability(.contentSize)
        .windowStyle(.hiddenTitleBar)
        Settings { DesktopSettings(model: model) }
        MenuBarExtra(
            "Model Uplink",
            systemImage: model.connected
                ? "antenna.radiowaves.left.and.right" : "antenna.radiowaves.left.and.right.slash"
        ) {
            MenuContents(model: model)
        }
    }
}

@MainActor final class AppDelegate: NSObject, NSApplicationDelegate {
    weak var model: UplinkModel?
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        guard model?.busy == true else { return .terminateNow }
        let alert = NSAlert()
        alert.messageText = "Finishing up"
        alert.informativeText = "Please let this step finish before quitting Model Uplink."
        alert.addButton(withTitle: "OK")
        alert.runModal()
        return .terminateCancel
    }
}

private struct MenuContents: View {
    @ObservedObject var model: UplinkModel
    @Environment(\.openWindow) private var openWindow
    var body: some View {
        Text(model.connected ? "Your model is online" : "Model Uplink")
        Button("Open Model Uplink") {
            openWindow(id: "main"); NSApp.activate(ignoringOtherApps: true)
        }
        Button("Stop sharing") {
            openWindow(id: "main"); NSApp.activate(ignoringOtherApps: true)
            Task { await model.stopAll() }
        }.disabled(model.working)
        SettingsLink()
        Button("Downloads / updates") { model.updates() }
        if let source = Bundle.main.object(forInfoDictionaryKey: "ModelUplinkSourceURL") as? String,
            let url = URL(string: source)
        {
            Link("View source", destination: url)
        }
        Button("Dashboard") { model.dashboard() }
        Button(model.paid ? "Manage billing" : "Subscribe to Agent") { model.billing() }
        Divider()
        Button("Quit Model Uplink") { NSApp.terminate(nil) }
    }
}

private struct DesktopSettings: View {
    @ObservedObject var model: UplinkModel
    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            Text("Background sharing").font(.headline)
            Text(
                "Closing the window or quitting the app keeps sharing running until you sign out of macOS. Automatic startup can resume active connections when you sign in again. Keep this Mac awake and online."
            )
            Toggle(
                "Start sharing when I sign in to this Mac",
                isOn: Binding(
                    get: { model.startupEnabled },
                    set: { enabled in Task { await model.setStartup(enabled) } }
                )
            ).disabled(model.working)
            Text(
                "This changes future automatic starts. Current sharing keeps running. Connections you stop stay stopped until you start them again."
            )
            Text(
                "Stop sharing disconnects remote access; externally managed model apps keep running. It stays stopped until you start it again."
            )
            Button("Stop sharing") { Task { await model.stopAll() } }.disabled(model.working)
            if model.working { ProgressView(model.message) }
            if !model.error.isEmpty { Text(model.error).foregroundStyle(.orange) }
            Button("Downloads / updates") { model.updates() }
            if let source = Bundle.main.object(forInfoDictionaryKey: "ModelUplinkSourceURL")
                as? String,
                let url = URL(string: source)
            {
                Link("View source", destination: url)
            }
            Text(
                "Model Uplink \(Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "dev")"
            ).foregroundStyle(.secondary)
        }.padding(24).frame(width: 390)
    }
}

struct UplinkView: View {
    @ObservedObject var model: UplinkModel
    @State private var removing: Endpoint?
    @State private var moving: Endpoint?
    @State private var editingModels: Endpoint?
    @FocusState private var codeFocused: Bool
    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Text("MU").font(.system(size: 12, weight: .black, design: .monospaced))
                    .foregroundStyle(appBackground)
                    .padding(9).background(lime, in: RoundedRectangle(cornerRadius: 8))
                Text("Model Uplink").font(.system(size: 15, weight: .bold))
                Spacer()
                if model.account != nil {
                    Menu {
                        SettingsLink()
                        Button("Downloads / updates") { model.updates() }
                        if let source = Bundle.main.object(
                            forInfoDictionaryKey: "ModelUplinkSourceURL") as? String,
                            let url = URL(string: source)
                        {
                            Link("View source", destination: url)
                        }
                        Button("Dashboard") { model.dashboard() }
                        Button("Sign out (connections keep running)") {
                            Task { await model.signOut() }
                        }
                    } label: {
                        Image(systemName: "ellipsis")
                    }
                    .menuStyle(.borderlessButton).menuIndicator(.hidden)
                    .frame(width: 28, height: 28).contentShape(Rectangle())
                    .accessibilityLabel("Account menu").help("Dashboard and account")
                    .disabled(model.working)
                }
            }.padding(.bottom, 24)
            ScrollView {
                VStack(spacing: 22) {
                    if !model.connecting
                        && (model.account == nil || !model.endpoints.isEmpty || model.managedSetup)
                    {
                        Orbit(online: model.connected, busy: model.working)
                            .scaleEffect(model.endpoints.isEmpty ? 1 : 0.55)
                            .frame(height: model.endpoints.isEmpty ? 154 : 84)
                    }
                    if model.account != nil && !model.connecting && !model.unavailable
                        && (!model.endpoints.isEmpty || model.managedSetup
                            || model.otherTrial != nil)
                    {
                        billingStatus
                    }
                    if model.loading && model.account == nil {
                        Text("Waking up your uplink…").foregroundStyle(.secondary)
                    } else if model.account == nil {
                        signIn
                    } else if model.unavailable {
                        Text("You’re signed in.").font(
                            .system(size: 25, weight: .bold, design: .rounded))
                        primary("Refresh connections") { await model.refresh() }
                    } else if model.connecting {
                        setupStatus
                    } else if let other = model.otherTrial, !model.preparingMove {
                        otherComputer(other)
                    } else if model.endpoints.isEmpty, let existing = model.connectionLimit {
                        connectionLimit(existing)
                    } else if model.endpoints.isEmpty {
                        setup
                    } else {
                        connections
                    }
                    ForEach(model.retired) { endpoint in
                        Text(
                            "Sharing stopped on this Mac, but model memory release is not confirmed."
                        )
                        .font(.system(size: 12)).foregroundStyle(.orange).multilineTextAlignment(
                            .center)
                        primary("Release model memory") {
                            await model.act("pause", endpoint: endpoint)
                        }
                    }
                    if !model.error.isEmpty {
                        Text(model.error).font(.system(size: 12)).foregroundStyle(
                            Color.orange.opacity(0.95)
                        )
                        .multilineTextAlignment(.center).textSelection(.enabled)
                    }
                    if model.working && !model.connecting {
                        ProgressView().controlSize(.small).tint(lime)
                        if !model.message.isEmpty {
                            Text(model.message).font(.system(size: 12)).foregroundStyle(.secondary)
                        }
                    }
                }.padding(.horizontal, 3).padding(.bottom, 12)
            }.scrollIndicators(.hidden)
            Spacer(minLength: 12)
            HStack {
                Circle().fill(model.connected ? lime : Color.gray).frame(width: 5, height: 5)
                Text(model.copied.isEmpty ? "Your model. Your Mac. Anywhere." : model.copied)
                    .font(.system(size: 11)).foregroundStyle(.secondary)
                Spacer()
                Button {
                    Task { await model.refresh() }
                } label: {
                    Image(systemName: "arrow.clockwise")
                }
                .buttonStyle(.plain).help("Refresh").disabled(model.working)
            }.padding(.top, 14)
        }
        .padding(30).padding(.top, 12)
        .frame(width: 460, height: 680)
        .background(appBackground).foregroundStyle(Color.white.opacity(0.94))
        .preferredColorScheme(.dark).tint(lime)
        .task {
            await model.refresh()
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(2))
                if Task.isCancelled { break }
                if model.account != nil { await model.refresh(quiet: true) }
            }
        }
        .alert(
            "Move sharing to this Mac?",
            isPresented: Binding(get: { moving != nil }, set: { if !$0 { moving = nil } })
        ) {
            Button("Keep using that computer", role: .cancel) { moving = nil }
            Button("Move sharing") {
                if let target = moving { Task { await model.connect(moveTrialID: target.id) } };
                moving = nil
            }
        } message: {
            Text(
                "We’ll prepare your selected model first, then stop sharing on the other computer. You’ll receive a new address and API key. Your remaining trial requests, transfer allowance, and expiry stay the same."
            )
        }
        .sheet(item: $editingModels) { endpoint in SharingEditor(model: model, endpoint: endpoint) }
        .onChange(of: model.challenge) { codeFocused = model.challenge != nil }
        .alert(
            "Remove this connection?",
            isPresented: Binding(get: { removing != nil }, set: { if !$0 { removing = nil } })
        ) {
            Button("Cancel", role: .cancel) { removing = nil }
            Button("Remove", role: .destructive) {
                if let endpoint = removing {
                    Task { await model.act("delete", endpoint: endpoint) }
                }; removing = nil
            }
        } message: {
            Text(
                "Its address and API keys will stop working. Your downloaded models stay on this Mac."
            )
        }
    }

    private var signIn: some View {
        VStack(spacing: 16) {
            Text(model.challenge == nil ? "Let’s give your model wings." : "You’ve got a code.")
                .font(.system(size: 25, weight: .bold, design: .rounded)).multilineTextAlignment(
                    .center)
            Text(
                model.challenge == nil
                    ? "Connect your Mac. Use your models anywhere."
                    : "Enter the six digits we emailed to \(model.email).\nThe code expires in 10 minutes."
            )
            .font(.system(size: 13)).foregroundStyle(.secondary).multilineTextAlignment(.center)
            if model.challenge == nil {
                TextField("Email address", text: $model.email).textContentType(.emailAddress)
                    .textFieldStyle(.roundedBorder)
                    .onSubmit { Task { await model.sendCode() } }.disabled(model.working)
                primary("Send sign-in code", disabled: !model.email.contains("@")) {
                    await model.sendCode()
                }
            } else {
                TextField("000000", text: $model.code).textContentType(.oneTimeCode)
                    .font(.system(size: 27, weight: .medium, design: .monospaced))
                    .multilineTextAlignment(.center)
                    .textFieldStyle(.plain).padding(12).frame(width: 158)
                    .background(Color.white.opacity(0.04), in: RoundedRectangle(cornerRadius: 12))
                    .overlay(RoundedRectangle(cornerRadius: 12).stroke(lime.opacity(0.6)))
                    .focused($codeFocused).disabled(model.working)
                    .onChange(of: model.code) {
                        model.code = String(
                            model.code.filter { $0.isASCII && $0.isNumber }.prefix(6))
                        if model.code.count == 6 && !model.working { Task { await model.verify() } }
                    }
                    .onSubmit { if model.code.count == 6 { Task { await model.verify() } } }
                    .accessibilityLabel("Six-digit sign-in code")
                primary("Sign in", disabled: model.code.count != 6) { await model.verify() }
                HStack {
                    Button("Send a new code") { Task { await model.sendCode() } }
                    Spacer()
                    Button("Different email") {
                        model.challenge = nil; model.error = ""; model.code = ""
                    }
                }.font(.system(size: 11)).buttonStyle(.plain).foregroundStyle(.secondary).disabled(
                    model.working)
            }
        }
    }
    private var setupStatus: some View {
        VStack(alignment: .leading, spacing: 18) {
            Text("Giving your model wings.")
                .font(.system(size: 24, weight: .bold, design: .rounded))
            Text(model.selectedModel).font(.system(size: 12, design: .monospaced)).foregroundStyle(
                lime)
            if let progress = model.progress {
                let stages = [
                    "account", "prepare", "download", "modelcheck", "reserve", "start", "verify",
                ]
                let titles = [
                    "Check account", "Prepare this Mac", "Get model ready", "Test model response",
                    "Reserve address", "Start connection", "Check public connection",
                ]
                let current = stages.firstIndex(of: progress.stage) ?? 0
                VStack(alignment: .leading, spacing: 12) {
                    ForEach(stages.indices, id: \.self) { index in
                        HStack(spacing: 10) {
                            Image(
                                systemName: index < current
                                    ? "checkmark.circle.fill"
                                    : index == current ? "circle.inset.filled" : "circle"
                            )
                            .foregroundStyle(index <= current ? lime : Color.gray)
                            Text(titles[index]).font(
                                .system(size: 12, weight: index == current ? .semibold : .regular)
                            )
                            .foregroundStyle(index <= current ? Color.white : Color.gray)
                        }
                    }
                }
                Text(progress.message).font(.system(size: 12)).fixedSize(
                    horizontal: false, vertical: true)
                if let fraction = progress.fraction, let total = progress.total {
                    ProgressView(value: fraction).tint(lime)
                        .accessibilityLabel("Current model file download")
                        .accessibilityValue("\(Int(fraction * 100)) percent")
                    HStack {
                        Text(
                            "\(ByteCountFormatter.string(fromByteCount: progress.completed ?? 0, countStyle: .file)) of \(ByteCountFormatter.string(fromByteCount: total, countStyle: .file))"
                        )
                        Spacer()
                        Text("\(Int(fraction * 100))%")
                    }.font(.system(size: 11, design: .monospaced)).foregroundStyle(.secondary)
                    Text("Progress for the current model file.").font(.system(size: 10))
                        .foregroundStyle(.secondary)
                } else {
                    ProgressView().controlSize(.small).tint(lime)
                }
                TimelineView(.periodic(from: .now, by: 1)) { context in
                    if context.date.timeIntervalSince(model.progressUpdatedAt) >= 30 {
                        Text(
                            "No new update for \(Int(context.date.timeIntervalSince(model.progressUpdatedAt))) seconds. Waiting for this step to respond…"
                        )
                        .font(.system(size: 11)).foregroundStyle(.secondary).fixedSize(
                            horizontal: false, vertical: true)
                    }
                }
            }
            Text("Keep this Mac awake and the app open while we finish setup.")
                .font(.system(size: 11)).foregroundStyle(.secondary).fixedSize(
                    horizontal: false, vertical: true)
        }.frame(maxWidth: .infinity, alignment: .leading)
            .padding(18).background(
                Color.white.opacity(0.035), in: RoundedRectangle(cornerRadius: 14))
    }
    private func connectionLimit(_ endpoint: Endpoint) -> some View {
        VStack(spacing: 18) {
            Text("You already have a connection")
                .font(.system(size: 24, weight: .bold, design: .rounded)).multilineTextAlignment(
                    .center)
            Text(
                "Your account has reached its connection limit. Manage your existing connections before sharing from this computer."
            )
            .font(.system(size: 13)).foregroundStyle(.secondary).multilineTextAlignment(.center)
            Text(endpoint.url).font(.system(size: 12, design: .monospaced)).textSelection(.enabled)
            Text(
                endpoint.online
                    ? "This connection is online. You can keep using its address and existing API keys."
                    : "This connection is offline. Its address is still reserved and counts toward your limit."
            )
            .font(.system(size: 13)).foregroundStyle(.secondary).multilineTextAlignment(.center)
            primary("Open dashboard") { model.dashboard() }
            primary("Copy existing address") { model.copy(endpoint.url, label: "Address") }
            primary("Refresh connections") { await model.refresh() }
            Text(
                "Removing a connection disables its address and API keys. Stopping sharing leaves the address reserved."
            )
            .font(.system(size: 12)).foregroundStyle(.secondary).multilineTextAlignment(.center)
        }
    }
    private func otherComputer(_ endpoint: Endpoint) -> some View {
        VStack(spacing: 18) {
            Text(
                model.keepingOtherComputer
                    ? "Sharing stays on your other computer."
                    : "Your trial is already sharing a model from another computer."
            )
            .font(.system(size: 24, weight: .bold, design: .rounded)).multilineTextAlignment(
                .center)
            Text(
                endpoint.online
                    ? "That connection is online." : "That connection is currently offline."
            )
            .font(.system(size: 13)).foregroundStyle(.secondary)
            if let source = model.localSources[endpoint.slug] {
                Text(source.url).font(.system(size: 11, design: .monospaced)).foregroundStyle(
                    .secondary)
                Text(source.message ?? source.status).font(.system(size: 11)).foregroundStyle(
                    .secondary)
            }
            Button("Manage friends’ access") { model.friendAccess() }.buttonStyle(
                UplinkActionStyle())
            Text(
                "Give each friend a separate named key in the dashboard. The GPU owner can access requests processed by their runtime."
            ).font(.system(size: 11)).foregroundStyle(.secondary)
            Text(endpoint.url).font(.system(size: 12, design: .monospaced)).textSelection(.enabled)
            Text(model.allowance).font(.system(size: 11)).foregroundStyle(.secondary)
            if !model.keepingOtherComputer {
                primary("Keep using that computer") { model.keepingOtherComputer = true }
            } else {
                primary("Copy address") { model.copy(endpoint.url, label: "Address") }
                Text("Use its existing API key. Keep the other computer awake and online.")
                    .font(.system(size: 12)).foregroundStyle(.secondary).multilineTextAlignment(
                        .center)
            }
            primary("Move sharing to this computer", disabled: model.trialFinished) {
                model.preparingMove = true
            }
            Button("Open dashboard") { model.dashboard() }.buttonStyle(.plain).foregroundStyle(lime)
        }
    }
    private var setup: some View {
        VStack {
            if model.managedSetup {
                managedSetup
                Button("Connect an existing server") { model.managedSetup = false }.disabled(
                    model.working)
            } else {
                attachedSetup
            }
        }
    }
    private var attachedSetup: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Connect a model server").font(.system(size: 25, weight: .bold, design: .rounded))
            Text("Run models in your favorite app. Share access from this Mac.").font(
                .system(size: 13)
            ).foregroundStyle(.secondary)
            Button("Find local servers") { Task { await model.inspectSource("discover_servers") } }
            if !model.servers.isEmpty {
                Menu("Detected API servers") {
                    ForEach(model.servers, id: \.url) { server in
                        Button(server.url) {
                            model.localKey = ""; model.testedSource = "";
                            model.localURL = server.url;
                            Task { await model.inspectSource("inspect_source") }
                        }
                    }
                }
            }
            TextField("Local API URL", text: $model.localURL).textFieldStyle(.roundedBorder)
                .onChange(of: model.localURL) {
                    model.localKey = ""; model.testedSource = ""
                }
            SecureField("Local server API key (if required)", text: $model.localKey).textFieldStyle(
                .roundedBorder)
            Text(
                "Enable the API server in your runtime app. Its local key stays on this Mac and is separate from the key you share with friends."
            ).font(.system(size: 11)).foregroundStyle(.secondary)
            Button("Find models") { Task { await model.inspectSource("inspect_source") } }
            Picker("Model to share", selection: $model.localModel) {
                Text("Choose a model").tag("")
                ForEach(model.localModels, id: \.self) { name in Text(name).tag(name) }
            }
            Text(
                "Test sends: “Reply with the word ready.” Your runtime may load the model. Chat readiness does not verify tools, vision or local-only execution."
            ).font(.system(size: 11)).foregroundStyle(.secondary)
            Button("Test connection") { Task { await model.inspectSource("test_source") } }
                .disabled(model.localModel.isEmpty)
            if !model.sourceMessage.isEmpty {
                Text(model.sourceMessage).font(.system(size: 12)).foregroundStyle(lime)
            }
            if model.paid {
                TextField("Endpoint name", text: $model.endpointName).textFieldStyle(.roundedBorder)
            }
            primary(
                model.trialFinished
                    ? "Subscribe to Agent"
                    : model.preparingMove ? "Move sharing to this Mac" : "Start sharing",
                disabled: !model.sourceReady && !model.trialFinished
            ) {
                if model.trialFinished {
                    model.billing()
                } else if model.preparingMove {
                    moving = model.otherTrial
                } else {
                    await model.connect()
                }
            }
            Button("Help me set up a model with Ollama") { model.managedSetup = true }
            Text(model.allowance).font(.system(size: 11)).foregroundStyle(.secondary)
            Button(model.paid ? "Manage billing" : "Subscribe to Agent") { model.billing() }
            Text(
                "Keep your model app and this Mac running. Stop sharing disconnects remote access and leaves your app alone."
            ).font(.system(size: 11)).foregroundStyle(.secondary)
            if model.preparingMove {
                Button("Keep using that computer") {
                    model.preparingMove = false; model.keepingOtherComputer = true
                }
            }
        }.disabled(model.working)
    }
    private var managedSetup: some View {
        VStack(spacing: 16) {
            Text("Small app. Big uplink.").font(.system(size: 26, weight: .bold, design: .rounded))
            Text("Pick a model. We’ll take care of the rest.").font(.system(size: 13))
                .foregroundStyle(.secondary)
            if model.paid {
                VStack(alignment: .leading, spacing: 10) {
                    Text("ENDPOINT NAME").font(
                        .system(size: 10, weight: .semibold, design: .monospaced)
                    ).foregroundStyle(lime)
                    TextField("atlas or home-rover", text: $model.endpointName)
                        .textFieldStyle(.roundedBorder).disabled(model.working)
                    Menu("Try a suggested name") {
                        ForEach(model.nameSuggestions, id: \.self) { suggestion in
                            Button(suggestion) { model.endpointName = suggestion }
                        }
                    }.disabled(model.working || model.nameSuggestions.isEmpty)
                    Text(
                        "Choose your own or use a suggestion. We’ll pair it with an available planet, such as atlas.mlup-mars.com."
                    )
                    .font(.system(size: 11)).foregroundStyle(.secondary).fixedSize(
                        horizontal: false, vertical: true)
                }.padding(18).background(
                    Color.white.opacity(0.035), in: RoundedRectangle(cornerRadius: 14))
            }
            VStack(alignment: .leading, spacing: 10) {
                Text("MODEL TO SHARE").font(
                    .system(size: 10, weight: .semibold, design: .monospaced)
                ).foregroundStyle(lime)
                Picker("Model", selection: $model.selectedModel) {
                    if !model.models.isEmpty {
                        Section("On this Mac · no download") {
                            ForEach(model.models.sorted(), id: \.self) { name in
                                Text(name + " · on this Mac").tag(name)
                            }
                        }
                    }
                    Section("Download a model") {
                        ForEach(
                            ["llama3.2:3b", "gemma3:1b", "qwen2.5:7b"].filter {
                                !model.models.contains($0)
                            }, id: \.self
                        ) { name in
                            Text(name + " · download").tag(name)
                        }
                    }
                }.labelsHidden().frame(maxWidth: .infinity).disabled(model.working)
                Text("Only this model will be shared. You can enable more models after connecting.")
                    .font(.system(size: 11)).foregroundStyle(.secondary).fixedSize(
                        horizontal: false, vertical: true)
                Text(
                    model.models.contains(model.selectedModel)
                        ? "Already on this Mac. We’ll use your existing model without downloading it again."
                        : "Ollama is set up automatically. This model needs a download that may be several GB."
                )
                .font(.system(size: 11)).foregroundStyle(.secondary).fixedSize(
                    horizontal: false, vertical: true)
            }.padding(18).background(
                Color.white.opacity(0.035), in: RoundedRectangle(cornerRadius: 14))
            primary(
                model.trialFinished
                    ? "Subscribe to Agent"
                    : model.preparingMove ? "Move sharing to this Mac" : "Connect my model"
            ) {
                if model.trialFinished {
                    model.billing()
                } else if model.preparingMove {
                    moving = model.otherTrial
                } else {
                    await model.connect()
                }
            }
            if model.preparingMove {
                Button("Keep using that computer") {
                    model.preparingMove = false; model.keepingOtherComputer = true
                }
                .disabled(model.working).buttonStyle(.plain)
            }
            Text(model.allowance).font(.system(size: 11)).foregroundStyle(.secondary)
        }
    }
    private var billingStatus: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(
                model.account?.billing_state == "grace"
                    ? "Payment needs attention"
                    : model.paid
                        ? "Agent subscription"
                        : model.trialFinished ? "Your free trial has finished" : "Free trial"
            )
            .font(.system(size: 18, weight: .bold, design: .rounded))
            Text(
                model.account?.billing_state == "grace"
                    ? "Open billing to update your payment details through Link."
                    : model.paid
                        ? "Your existing address and API keys keep working."
                        : model.trialFinished
                            ? "New API requests are blocked. Subscribe to restore access with the same address and key. Stop sharing below if you want to release model memory."
                            : model.allowance
                                + ". You can subscribe at any time without finishing the trial."
            )
            .font(.system(size: 12)).foregroundStyle(.secondary).fixedSize(
                horizontal: false, vertical: true)
            primary(model.paid ? "Manage billing" : "Subscribe to Agent") { model.billing() }
        }.padding(18).frame(maxWidth: .infinity, alignment: .leading)
            .background(lime.opacity(0.06), in: RoundedRectangle(cornerRadius: 14))
    }
    private var connections: some View {
        VStack(spacing: 18) {
            Text(model.connected ? "Hello, world." : "Your uplink is here.").font(
                .system(size: 28, weight: .bold, design: .rounded))
            Text(
                model.trialFinished
                    ? "Your address and API keys are saved for when you subscribe."
                    : model.connected
                        ? "Your model is ready to go places." : "Connect when you’re ready."
            ).font(.system(size: 13)).foregroundStyle(.secondary)
            ForEach(model.endpoints) { endpoint in
                VStack(alignment: .leading, spacing: 20) {
                    HStack {
                        Circle().fill(
                            endpoint.online && !model.paused.contains(endpoint.slug)
                                && !model.trialFinished ? lime : Color.orange
                        ).frame(width: 6, height: 6)
                        Text(model.connectionStatus(endpoint))
                            .font(.system(size: 10, weight: .semibold, design: .monospaced))
                        Spacer()
                        Menu {
                            Button("Remove connection…", role: .destructive) { removing = endpoint }
                        } label: {
                            Image(systemName: "ellipsis")
                        }
                        .menuStyle(.borderlessButton).menuIndicator(.hidden).frame(
                            width: 28, height: 28
                        )
                        .accessibilityLabel("Connection options")
                    }
                    if endpoint.online && !model.paused.contains(endpoint.slug)
                        && !model.trialFinished
                    {
                        Text("You can close this window. Your model stays online.")
                            .font(.system(size: 12)).foregroundStyle(.secondary).fixedSize(
                                horizontal: false, vertical: true)
                    }
                    VStack(alignment: .leading, spacing: 8) {
                        Text(
                            model.sharedModels[endpoint.slug]?.joined(separator: ", ")
                                ?? "All installed models are shared"
                        )
                        .font(.system(size: 12, weight: .medium)).fixedSize(
                            horizontal: false, vertical: true)
                        if let activity = model.activity[endpoint.slug],
                            endpoint.online && !model.paused.contains(endpoint.slug)
                                && !model.trialFinished
                        {
                            Text(
                                activity.running > 0
                                    ? "Serving \(activity.model) · \(activity.running) running · \(activity.waiting) waiting"
                                    : "Ready for requests · \(activity.waiting) waiting"
                            )
                            .font(.system(size: 11)).foregroundStyle(.secondary)
                        }
                        Button("Choose shared models…") { editingModels = endpoint }
                            .buttonStyle(.plain).foregroundStyle(lime).font(.system(size: 12))
                    }
                    if let source = model.localSources[endpoint.slug] {
                        Text(source.url).font(.system(size: 11, design: .monospaced))
                            .foregroundStyle(.secondary)
                        Text(source.message ?? source.status).font(.system(size: 11))
                            .foregroundStyle(.secondary)
                    }
                    Button("Manage friends’ access") { model.friendAccess() }.buttonStyle(
                        UplinkActionStyle())
                    Text(
                        "Give each friend a separate named key in the dashboard. The GPU owner can access requests processed by their runtime."
                    ).font(.system(size: 11)).foregroundStyle(.secondary)
                    Text(endpoint.url).font(.system(size: 12, design: .monospaced)).textSelection(
                        .enabled
                    ).fixedSize(horizontal: false, vertical: true)
                    HStack(spacing: 16) {
                        Button("Copy address") { model.copy(endpoint.url, label: "Address") }
                        Button("Copy API key") { Task { await model.copyKey(endpoint) } }
                    }.buttonStyle(UplinkActionStyle()).font(.system(size: 12))
                    if model.paused.contains(endpoint.slug) {
                        Text(
                            model.localSources[endpoint.slug]?.ownership == "external"
                                ? "Sharing stopped · your model app is unchanged"
                                : model.memoryReleasePending.contains(endpoint.slug)
                                    ? "Sharing stopped · memory release needs attention"
                                    : "Sharing stopped · model memory released"
                        )
                        .font(.system(size: 11)).foregroundStyle(.secondary)
                    }
                    if model.memoryReleasePending.contains(endpoint.slug) {
                        Button("Release model memory") {
                            Task { await model.act("pause", endpoint: endpoint) }
                        }.buttonStyle(UplinkActionStyle())
                    }
                    Button(
                        model.paused.contains(endpoint.slug)
                            ? (model.trialFinished
                                ? "Subscribe to resume sharing" : "Start sharing") : "Stop sharing"
                    ) {
                        if model.paused.contains(endpoint.slug) && model.trialFinished {
                            model.billing()
                        } else {
                            Task {
                                await model.act(
                                    model.paused.contains(endpoint.slug) ? "resume" : "pause",
                                    endpoint: endpoint)
                            }
                        }
                    }
                    .buttonStyle(UplinkActionStyle()).font(.system(size: 12)).foregroundStyle(lime)
                }.padding(18).background(
                    Color.white.opacity(0.035), in: RoundedRectangle(cornerRadius: 14)
                ).disabled(model.working)
            }
            Text(
                "Keep this Mac awake and online. Your connection keeps running when you close the app. Stopping sharing doesn’t cancel a subscription."
            )
            .font(.system(size: 11)).foregroundStyle(.secondary).multilineTextAlignment(.center)
            Button("Open dashboard") { model.dashboard() }.buttonStyle(.plain).foregroundStyle(lime)
                .font(.system(size: 12))
        }
    }
    private func primary(
        _ title: String, disabled: Bool = false, action: @escaping () async -> Void
    ) -> some View {
        Button {
            Task { await action() }
        } label: {
            Text(title).font(.system(size: 14, weight: .bold)).frame(maxWidth: .infinity).padding(
                .vertical, 13
            ).contentShape(Rectangle())
        }.buttonStyle(.plain).foregroundStyle(appBackground)
            .background(
                lime.opacity(disabled || model.working ? 0.4 : 1),
                in: RoundedRectangle(cornerRadius: 12)
            )
            .disabled(disabled || model.working)
    }
}

private struct UplinkActionStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label.frame(maxWidth: .infinity).padding(.horizontal, 10).padding(
            .vertical, 11
        )
        .foregroundStyle(lime).background(
            lime.opacity(configuration.isPressed ? 0.16 : 0.06),
            in: RoundedRectangle(cornerRadius: 9)
        )
        .contentShape(Rectangle())
    }
}

private struct SharingEditor: View {
    @ObservedObject var model: UplinkModel
    let endpoint: Endpoint
    @Environment(\.dismiss) private var dismiss
    @State private var selected: Set<String> = []
    @State private var multiple = false
    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            Text("Choose models to share").font(.system(size: 23, weight: .bold, design: .rounded))
            Text("API clients can use only the models you select.").font(.system(size: 13))
                .foregroundStyle(.secondary)
            Toggle("Share multiple models", isOn: $multiple)
                .onChange(of: multiple) {
                    if !multiple, let first = selected.sorted().first { selected = [first] }
                }
            ScrollView {
                VStack(spacing: 10) {
                    ForEach(model.modelsFor(endpoint).sorted(), id: \.self) { name in
                        Button {
                            if multiple {
                                if selected.contains(name) {
                                    selected.remove(name)
                                } else {
                                    selected.insert(name)
                                }
                            } else {
                                selected = [name]
                            }
                        } label: {
                            HStack {
                                Image(
                                    systemName: selected.contains(name)
                                        ? "checkmark.circle.fill" : "circle");
                                Text(name); Spacer()
                            }
                            .padding(12).frame(maxWidth: .infinity).contentShape(Rectangle())
                        }.buttonStyle(.plain).background(
                            Color.white.opacity(0.04), in: RoundedRectangle(cornerRadius: 9))
                    }
                }
            }.frame(maxHeight: 190)
            Text(
                "One request runs at a time. Up to four can wait briefly. Switching models may take time while they load."
            )
            .font(.system(size: 12)).foregroundStyle(.secondary).fixedSize(
                horizontal: false, vertical: true)
            Text("Saving checks each model with a short response and reconnects this address.")
                .font(.system(size: 11)).foregroundStyle(.secondary)
            if model.busy { ProgressView(); Text(model.message).font(.system(size: 12)) }
            if !model.error.isEmpty {
                Text(model.error).font(.system(size: 12)).foregroundStyle(.orange)
            }
            HStack(spacing: 16) {
                Button("Cancel") { dismiss() }
                Button("Save shared models") {
                    Task {
                        if await model.updateSharing(endpoint, models: selected.sorted()) {
                            dismiss()
                        }
                    }
                }
                .disabled(selected.isEmpty)
            }.buttonStyle(UplinkActionStyle())
        }.padding(28).frame(width: 440).background(appBackground).foregroundStyle(.white).tint(lime)
            .disabled(model.working).interactiveDismissDisabled(model.working)
            .onAppear {
                let existing = model.sharedModels[endpoint.slug] ?? [model.selectedModel]
                selected = Set(existing); multiple = existing.count > 1; model.error = ""
            }
    }
}

private struct Orbit: View {
    let online: Bool
    let busy: Bool
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var float = false
    var body: some View {
        ZStack {
            Circle().stroke(lime.opacity(0.08), lineWidth: 1).frame(width: 146, height: 146)
            Circle().stroke(lime.opacity(0.16), lineWidth: 1).frame(width: 108, height: 108)
            Circle().fill(lime.opacity(online ? 0.16 : 0.06)).frame(width: 78, height: 78)
                .blur(radius: 10)
            Image(systemName: online ? "antenna.radiowaves.left.and.right" : "paperplane.fill")
                .font(.system(size: 37, weight: .light)).foregroundStyle(lime)
                .rotationEffect(.degrees(online ? 0 : -12))
                .offset(y: float && !reduceMotion ? -5 : 3)
            Circle().fill(lime).frame(width: 7, height: 7).offset(x: 59, y: -40)
            Circle().fill(Color.white.opacity(0.6)).frame(width: 3, height: 3).offset(x: -56, y: 28)
        }.frame(height: 154)
            .accessibilityHidden(true)
            .onAppear {
                withAnimation(
                    reduceMotion
                        ? nil
                        : .easeInOut(duration: busy ? 1.1 : 2.4).repeatForever(autoreverses: true)
                ) { float = true }
            }
    }
}
