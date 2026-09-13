# Contributing

Use public issues and pull requests for client bugs, build failures, and changes.
Include the OS, desktop environment, client version, and steps to reproduce.
Redact account identifiers and credentials from logs. Report vulnerabilities
privately as described in SECURITY.md.

Run `go test -race -tags ci ./...` and `go vet -tags ci ./...`. For macOS UI/helper
changes, also run `bash macos/test.sh`. Include native validation for changes to
startup, keyrings, process management, or packaging. Mock tests do not replace it.

The `pkg/` and `proto/` interfaces are consumed by the hosted service. Keep wire
changes additive and retain existing semantics until older supported clients have
been accounted for. Update schema, generated code, and compatibility tests together.
Private security fixes should appear here with the fix release and disclosure.
