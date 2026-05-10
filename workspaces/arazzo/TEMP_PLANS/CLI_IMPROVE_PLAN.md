# Phase 1 Plan: Add `--docker` To Package The Arazzo Server

## Summary

Add an optional `--docker` flag to `arazzo-designer-cli serve`. Normal `serve` behavior remains unchanged: it starts only the Arazzo server with `/mcp`, `/run`, and `/lastResult`. VS Code continues to manage its own tracer server separately.

With `--docker`, the CLI builds a Docker image that starts the same Arazzo server when the image is run. No tracer server is bundled into the image.

## Key Changes

- Add `--docker` to the existing `serve` command in `cmd/main.go`.
- If `--docker` is absent, keep the current code path unchanged.
- If `--docker` is present:
  - validate the Arazzo file path as usual
  - create a temporary Docker build context
  - copy the Arazzo file and needed local source-description files into the image
  - include the Linux Go CLI binary
  - generate a Dockerfile and entrypoint
  - run `docker build`
  - print the image tag and `docker run` command
  - exit without starting the host-side server

## Docker Runtime Behavior

- The image runs:
  ```sh
  arazzo-designer-cli serve -f /app/workspace/<arazzo-file> -p <port>
  ```
- The container exposes only the Arazzo server port, default `8080`.
- The printed command should look like:
  ```sh
  docker run --rm -p 8080:8080 arazzo-mcp-server:<tag>
  ```
- A user who wants tracing can run a tracer server separately and pass a reachable trace endpoint into the container flow later. Phase 1 does not need to bundle or start a tracer server.

## VS Code Safety

- Do not modify VS Code extension code.
- Do not change how VS Code starts the tracer server.
- Do not make VS Code pass `--docker`.
- Existing extension behavior remains:
  - VS Code starts tracer server
  - VS Code starts `arazzo-designer-cli serve`
  - CLI starts Arazzo server only
  - CLI sends trace events to VS Code tracer through `--trace-endpoint`

## Test Plan

- Run:
  ```sh
  go test ./...
  ```
- Verify existing behavior:
  ```sh
  arazzo-designer-cli serve -f <sample.arazzo.yaml> -p 8080
  ```
- Verify Docker image generation:
  ```sh
  arazzo-designer-cli serve -f <sample.arazzo.yaml> -p 8080 --docker
  ```
- Run the printed Docker command and confirm:
  - `http://localhost:8080/mcp` is available
  - `POST http://localhost:8080/run/{workflowId}` works
  - `GET http://localhost:8080/lastResult/{workflowId}` works after a run
- Confirm VS Code still starts and traces workflows exactly as before.

## Assumptions

- Phase 1 only changes `arazzo-designer-cli`.
- No `-o` support yet.
- No tracer server is generated or bundled in Phase 1.
- Docker mode is a packaging/output mode for the existing Arazzo server, not a VS Code integration.


# Phase 2 Plan: Add `-o` For Docker Build Artifacts

## Summary

Add an optional `-o <folder>` flag to `arazzo-designer-cli serve`, but make it valid only with `--docker`.

Without `--docker`, `serve` has no intermediate files and must keep its current behavior. With `--docker`, `-o` tells the CLI to keep the Docker build artifacts in the specified folder instead of using a temporary `.wso2arazzo` build folder that is deleted afterward.

## Behavior

- `serve` without `--docker`:
  - unchanged
  - starts the Arazzo server normally
  - does not create intermediate files
- `serve --docker` without `-o`:
  - creates a temporary build folder, preferably under `.wso2arazzo`
  - generates Docker artifacts there
  - builds the image
  - deletes the temporary folder after success or failure
- `serve --docker -o <folder>`:
  - creates or reuses `<folder>`
  - writes Docker artifacts into it
  - builds the image from that folder
  - does not delete the folder
- `serve -o <folder>` without `--docker`:
  - fail fast with:
    ```txt
    Error: -o can only be used with --docker
    ```

## Output Folder Contents

For the Go CLI Docker path, the kept artifacts should be:

- `Dockerfile`
- `entrypoint.sh` or equivalent startup script
- `arazzo-designer-cli-linux-amd64`
- `workspace/` containing the copied Arazzo file and local source-description files
- optional `README.md` or `run-command.txt` with the generated `docker run` command

Do not generate Python server code. The server code is already inside the Go CLI binary.

## CLI Interface

- Add:
  ```txt
  -o <folder>
  ```
- Usage examples:
  ```sh
  arazzo-designer-cli serve -f workflow.arazzo.yaml --docker
  arazzo-designer-cli serve -f workflow.arazzo.yaml --docker -o ./docker-output
  ```
- Update CLI help to make the dependency explicit:
  ```txt
  -o  Output folder for Docker build artifacts; only valid with --docker
  ```

## Safety Rules

- Never delete a user-provided `-o` folder.
- For auto-created `.wso2arazzo` temp folders, delete only the exact folder created for the current run.
- If `<folder>` already exists, overwrite only known generated files in that folder, not unrelated files.
- Keep VS Code unchanged and never pass `-o` from the extension.

## Test Plan

- Existing behavior:
  ```sh
  go test ./...
  arazzo-designer-cli serve -f <sample.arazzo.yaml> -p 8080
  ```
- Invalid usage:
  ```sh
  arazzo-designer-cli serve -f <sample.arazzo.yaml> -o ./out
  ```
  Confirm it fails with a clear message.
- Temporary Docker artifacts:
  ```sh
  arazzo-designer-cli serve -f <sample.arazzo.yaml> --docker
  ```
  Confirm image builds and temporary artifacts are cleaned.
- Persistent Docker artifacts:
  ```sh
  arazzo-designer-cli serve -f <sample.arazzo.yaml> --docker -o ./out
  ```
  Confirm `./out` contains `Dockerfile`, startup script, CLI binary, workspace files, and run command.
- Run the printed Docker command and confirm `/mcp`, `/run`, and `/lastResult` work.

## Assumptions

- Phase 2 only changes `arazzo-designer-cli`.
- `-o` is an artifact-retention/output-folder flag, not an image output path.
- `-o` is only valid with `--docker`.
- No tracer server is bundled or generated.

