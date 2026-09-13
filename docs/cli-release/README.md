# Model Uplink CLI

Build with `go build -mod=readonly -trimpath -o dist/modeluplink ./cmd/modeluplink`.
Use `modeluplink help` for commands, then `modeluplink login` to sign in.
Connect an existing server with:

```sh
modeluplink connect --url http://127.0.0.1:1234/v1 --model YOUR_MODEL
modeluplink status
modeluplink stop --help
modeluplink version --json
```

See [the public source and release verification instructions](https://github.com/oscar-investmatic/modeluplink-client/blob/main/RELEASING.md).
Model Uplink's hosted service requires an account. Independently built clients
use the same login flow and service limits. For help, contact contact@modeluplink.com.
Never publish account credentials, API keys, or sign-in codes in issues.
