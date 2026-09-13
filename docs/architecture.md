# Code and trust boundaries

Start with `cmd/modeluplink/main.go` for CLI commands, `cmd/modeluplink/desktop.go`
for desktop-helper dispatch, and `pkg/agent/agent.go` for the relay connection.

The macOS Swift app and the Linux/Windows Fyne app invoke the bundled helper's
`_desktop` command. Each invocation sends one JSON request on stdin and receives
newline-delimited progress events followed by one result on stdout. Credentials
travel in those pipes, not command-line arguments. The helper fixtures in
`internal/desktopcontract` exercise both interfaces against the same wire examples.
This internal protocol can change with a coordinated app/helper release.

`pkg/client` calls the hosted control API for sign-in, endpoint creation, key
management, and account state. `pkg/agent` maintains the outbound relay tunnel,
terminates endpoint TLS locally, authorizes inference, and enforces model selection.
It forwards accepted requests to the configured model server. The relay transports
frames described in `proto/tunnel/v1`; `pkg/tunnel` implements their codec and header
filtering. The hosted control service and relay are not included in this repository.
They remain responsible for availability, account state, and service policy.

`internal/upstream` validates local OpenAI-compatible API URLs and disables proxy
environment variables and redirects for those requests. Explicit LAN access is an
opt-in. `internal/engine` provides the separate managed-Ollama path. Model runtimes
and downloaded weights are separate software with their own licenses.

`internal/localconfig` owns account and endpoint files. Unix files use owner-only
permissions; Windows uses DPAPI and a user/SYSTEM directory ACL. New externally
managed upstream secrets use OS keyring references. Older profiles can retain
secrets in their protected configuration files. Desktop inference keys use the OS
keyring; a newly issued key can also remain in GUI memory for immediate copying.
Do not assume every credential type uses the same storage mechanism.

`internal/service` controls native per-user services. Flatpak uses its own session
supervisor and the Background portal instead of host service management. Explicit
Stop must prevent automatic restart; closing a window can leave sharing active.
Logout may stop sharing. Inspect the platform adapters when changing lifecycle
behavior, and test in an actual graphical session.

The public `pkg/` paths support the private backend's planned pinned-module
integration. They are not yet a stable general-purpose SDK. Protocol changes need
compatibility review across deployed client and hosted-service versions. Tests
cover local behavior and synthetic wire exchanges; they do not attest the deployed
service or replace native acceptance. See RELEASING.md for remaining release work.
