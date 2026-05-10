# TLS Verification Toggle For Arazzo Runner

## Summary
Add a VS Code setting, `arazzo.disableTls`, that disables TLS certificate verification for outbound API calls made by the Go runner. The setting defaults to secure behavior. When changed, VS Code prompts the user to restart the running Arazzo server so the new setting is applied at server startup.

This does not require any curl flag. The curl command calls the local `/run` endpoint over HTTP; TLS verification matters inside the Go runner when it calls workflow target APIs.

## Key Changes

### VS Code Extension
- Add `arazzo.disableTls` to `package.json` under `contributes.configuration`.
  - Type: `boolean`
  - Default: `false`
  - Description: `Disables TLS verification for Arazzo API calls. Useful for custom endpoints without valid certificates. Requires a server restart.`
- Add a configuration-change listener in extension activation:
  - Listen for `event.affectsConfiguration('arazzo.disableTls')`.
  - Show: `Arazzo TLS settings have changed. The server must be restarted for this to take effect.`
  - If an Arazzo server is currently running, include `Restart Server`.
  - On `Restart Server`, restart using the current served file from `getMCPActiveFilePath()` by calling `startMCPServer(context, activeFilePath, true)`.
  - If no server is running, only show the message; the next start will use the new setting.
- In the server boot path, read:
  - `vscode.workspace.getConfiguration('arazzo').get<boolean>('disableTls', false)`
- Extend `MCPServerTaskParams` with `disableTls: boolean`.
- When spawning the Go CLI, append:
  - `--disable-tls=true`
  only when the setting is enabled.
- Print a clear task log line when enabled:
  - `TLS verification: disabled`
  so users can confirm the server started with the expected mode.

### Go CLI And Runner
- Add a `--disable-tls` boolean flag to the `serve` command in `cmd/main.go`.
- Add a runtime field to `models.RuntimeParams`, for example:
  - `DisableTLSVerification bool`
- Populate that field from the CLI flag.
- Pass the runtime params through the existing runner path; no `/mcp` or `/run` request-body change is needed.
- In `internal/httpexec/http_executor.go`, construct the outbound `http.Client` like this:
  - Default: normal `http.Client{Timeout: 30 * time.Second}`
  - If `DisableTLSVerification` is true:
    - Clone `http.DefaultTransport`
    - Set `TLSClientConfig: &tls.Config{InsecureSkipVerify: true}`
    - Use that transport on the client
- Wire this from `StepExecutor` into `httpexec.NewHTTPExecutor(...)`, using the value from `runtimeParams`.
- Keep behavior shared for both MCP-triggered runs and direct `/run` runs, because both use the same server-side runner.

## Test Plan
- TypeScript:
  - Run extension compile: `pnpm run compile` in `arazzo-designer-extension`.
  - Confirm `package.json` setting schema is valid.
- Go:
  - Run `go test ./...` in `arazzo-designer-cli`.
  - Add an `httpexec` test using `httptest.NewTLSServer`:
    - Default executor should fail against the self-signed TLS server.
    - Executor with TLS verification disabled should succeed.
- Manual VS Code scenario:
  - Start Arazzo server with default setting and confirm no `--disable-tls=true` is passed.
  - Enable `arazzo.disableTls`.
  - Confirm popup appears.
  - Click `Restart Server`.
  - Confirm server task restarts and logs `TLS verification: disabled`.
  - Run workflow through both CodeLens/webview Try and MCP Copilot flow; both should use disabled TLS verification after restart.

## Assumptions
- `arazzo.disableTls` means “disable TLS certificate verification,” not disable HTTPS/TLS itself.
- The setting applies at server startup only; existing running servers are not hot-reconfigured.
- The setting is available at both user and workspace scope through normal VS Code settings, with workspace usage recommended for project-specific insecure dev endpoints.
- No changes are needed to generated curl commands or `/run` JSON bodies.
