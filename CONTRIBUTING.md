# Contributing

Use public issues and pull requests for client bugs, build failures, and changes.
Include the OS, desktop environment, client version, and steps to reproduce.
Redact account identifiers and credentials from logs. Report vulnerabilities
privately as described in SECURITY.md.

Run `go test -race -tags ci ./...` and `go vet -tags ci ./...`. For macOS UI/helper
changes, also run `bash macos/test.sh`. Include native validation for changes to
startup, keyrings, process management, or packaging. Mock tests do not replace it.

The `pkg/` and `proto/` interfaces are the boundary for the hosted-service cutover. Keep wire
changes additive and retain existing semantics until older supported clients have
been accounted for. Update schema, generated code, and compatibility tests together.
Private security fixes should appear here with the fix release and disclosure.

## Formatting and lint

Install the pinned Python tools with `python -m pip install -r requirements-dev.txt`,
then run `python scripts/lint.py`. It checks Go imports/formatting, Staticcheck,
Python formatting/imports, shell scripts, and GitHub workflows. Go tools are pinned
in the script. On macOS, also run `python scripts/lint.py --swift-only` using
swift-format from Xcode (reviewed with version 6.3.0).

Format Go with goimports and Python with `python -m ruff format desktop linux/flatpak scripts`.
For Swift, use `xcrun swift-format format --in-place --recursive macos`.
Generated protobuf bindings are excluded from import reformatting; regenerate them
from their schema with protoc-gen-go 1.36.12 instead of editing them by hand.

Staticcheck enables all checks except ST1005: the CLI and desktop helper intentionally
return complete user-facing sentences as errors. Swift wire structs preserve the
helper's snake_case JSON fields, so the lower-camel-case naming rule is disabled.
These exceptions do not disable correctness checks.

On Windows, install `PSScriptAnalyzer` 1.24.0 and run `./scripts/lint.ps1`.
The script uses the analyzer's `CodeFormatting` preset and checks all Windows
packaging scripts. It does not run installers or access signing certificates.

Comments should explain constraints, lifecycle ordering, protocol decisions, or
non-obvious behavior. Keep claims limited to what the code and tests demonstrate.
Update comments alongside behavior changes; avoid references to a past conversation
or repeating the statement immediately below the comment.
