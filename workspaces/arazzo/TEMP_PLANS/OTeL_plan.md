# OTeL_plan.md: Phase 1 live tracing bridge for workflow progress

## Summary
Build phase 1 as a **real OpenTelemetry-instrumented Go runner** plus a **local VS Code tracer server** that receives lightweight span lifecycle events and logs/normalizes them for future UI updates.

Chosen defaults:
- Transport: **custom local JSON bridge**
- Scope: **workflow + step + HTTP spans**
- Payload detail: **metadata only**
- Endpoint shape: **fixed path**, **dynamic localhost port**
- Phase 1 success bar: tracer server starts with the MCP server, spans arrive for start/end of workflow/step/API calls, and nothing breaks if tracing is unavailable

Important correction to the initial idea:
- For a live “animation started” signal, do **not** rely only on a span exporter.
- In OpenTelemetry, exporters receive finished spans, but live start/end hooks belong on a **SpanProcessor**.
- So phase 1 should use:
  - real OTel spans in the Go runner
  - a custom **SpanProcessor** to emit `start` and `end` events
  - an async HTTP sink that posts those events to `tracer_server.ts`

## Key changes
### 1. Extension-side tracer server
- Add `tracer_server.ts` in the extension MCP area, next to [mcpServerRunner.ts](c:/Users/Himeth%20Walgampaya/Important/vs_plugin_testing/official_wso2/vscode-extensions/workspaces/arazzo/arazzo-designer-extension/src/mcp/mcpServerRunner.ts).
- Start it when the MCP server starts, and stop it when the MCP server stops or the extension deactivates.
- Bind only to `127.0.0.1`.
- Use a **dynamic port** from `portfinder`, but keep a **fixed route** such as `/span-events`.
- Add a small `/health` route for verification.
- Create a dedicated output channel such as `Arazzo Trace Server`.
- On startup, log the chosen endpoint and health URL.
- On each incoming event, log a short line with: `traceId`, `spanId`, `kind`, `workflowId`, `stepId`, `lifecycle`, `status`.

### 2. CLI and runner wiring
- Extend the CLI launched by the extension so it can receive a trace endpoint, preferably via a new flag like `--trace-endpoint`.
- When the extension spawns the Go CLI, pass the tracer server endpoint into it together with the existing MCP args.
- Add telemetry config into runtime params so the runner can enable or disable tracing without hardcoding anything.
- Keep tracing **non-fatal**:
  - if no endpoint is provided, runner executes normally with tracing off
  - if trace delivery fails, workflow execution still succeeds or fails exactly as before

### 3. Go tracing architecture
- In the runner package, add a telemetry module that initializes:
  - a `TracerProvider`
  - always-on sampling for local development
  - resource attributes like `service.name=arazzo-runner`
  - a custom live span processor
- Keep the public runner entry stable by wrapping context handling internally:
  - keep the existing external `ExecuteWorkflow(workflowID, inputs)` shape
  - add an internal context-aware helper for trace propagation
- Instrument three levels:
  - workflow span: around one workflow execution
  - step span: around each step execution
  - HTTP span: around each outbound API request in [http_executor.go](c:/Users/Himeth%20Walgampaya/Important/vs_plugin_testing/official_wso2/vscode-extensions/workspaces/arazzo/arazzo-designer-cli/internal/httpexec/http_executor.go)
- Start the root workflow span in [runner.go](c:/Users/Himeth%20Walgampaya/Important/vs_plugin_testing/official_wso2/vscode-extensions/workspaces/arazzo/arazzo-designer-cli/internal/runner/runner.go), pass child context into step execution, and pass step context into the HTTP executor.

### 4. Event model for phase 1
Use one normalized event shape on the wire from Go to TS:

`TraceEvent`
- `lifecycle`: `start` | `end`
- `traceId`
- `spanId`
- `parentSpanId?`
- `spanName`
- `spanKind`: `workflow` | `step` | `http`
- `timestamp`
- `durationMs?`
- `status`: `unset` | `ok` | `error`
- `errorMessage?`
- `attributes`

`attributes` must include only metadata:
- `arazzo.document.path`
- `arazzo.workflow.id`
- `arazzo.parent.workflow.id?`
- `arazzo.step.id?`
- `arazzo.step.attempt?`
- `arazzo.operation.id?`
- `arazzo.operation.path?`
- `arazzo.source.name?`
- `http.request.method?`
- `http.url?`
- `http.status_code?`
- `arazzo.input.keys?`
- `arazzo.output.keys?`
- `arazzo.request.path_keys?`
- `arazzo.request.query_keys?`
- `arazzo.request.header_keys?`
- `arazzo.request.body.present?`
- `arazzo.response.body.present?`
- `arazzo.success?`

Do **not** send:
- raw input values
- request or response bodies
- auth header values
- bearer tokens
- API keys
- full headers beyond safe key names

### 5. Interfaces explained
Use interfaces in two places:

TypeScript interfaces:
- define the JSON contract the tracer server accepts and normalizes
- prevent `any`-shaped event drift between the Go sender and TS receiver
- later become the source shape for the webview RPC notification

Go interfaces:
- define **behavior**, not data shape
- recommended split:
  - `SpanEventSink`: “something that can accept start/end span events”
  - `TraceLifecycleProcessor`: OTel `SpanProcessor` implementation that converts spans into your app’s event shape
- this lets the runner depend on a contract instead of direct HTTP code
- later you can swap the sink from local HTTP to OTLP or a Collector without touching runner logic

OTel interfaces:
- use OTel’s `SpanProcessor` for `OnStart` and `OnEnd`
- do not put live UI signaling responsibility only in an exporter

### 6. Delivery mechanics
- `OnStart` and `OnEnd` must not block workflow execution.
- The processor should only:
  - build a small event object
  - enqueue it into a buffered channel
- A background worker goroutine should:
  - batch lightly or send one event at a time
  - POST to the tracer server
  - retry minimally or drop with logging
- If the queue fills:
  - drop oldest or newest by policy
  - increment a dropped counter
  - log a warning
- Never fail a workflow because telemetry could not be delivered.

### 7. Phase 2 alignment without building phase 2 yet
- Inside the extension, normalize incoming events into one in-memory event bus plus a bounded cache.
- Phase 1 only logs and stores them.
- Phase 2 will subscribe to that bus and forward normalized events over the existing extension-to-webview RPC channel.
- Keep the normalized event type stable so the animation layer can reuse it directly later.

## Public API / contract additions
- New CLI flag: `--trace-endpoint`
- New runtime telemetry config inside runner params
- New TS event interfaces for tracer ingress and normalized trace state
- New internal Go contracts for the async trace sink and span processor bridge

## Test plan
### Happy path
- Start MCP server and confirm tracer server startup is logged.
- Run a normal workflow and confirm this sequence appears:
  - workflow start
  - step start
  - HTTP start
  - HTTP end
  - step end
  - workflow end
- Confirm all events share one `traceId`.

### Failure and control flow
- Workflow with failed API call emits:
  - HTTP end with `status=error`
  - step end with `status=error`
  - workflow end with `status=error`
- Retry workflow emits repeated step and HTTP spans with `step.attempt`.
- Nested workflow emits a child workflow span with the same trace and correct parent span.
- Goto flow still produces ordered start/end events for the actually executed steps.

### Safety and reliability
- Stop tracer server and confirm workflow execution still works.
- Send malformed payload to tracer server and confirm it logs rejection without crashing.
- Run multiple workflows back-to-back and confirm separate traces.
- Confirm no secrets or raw bodies appear in logs.
- Confirm startup/shutdown does not leave orphaned servers or ports.

## Acceptance criteria
- Starting the MCP server also starts the tracer server automatically.
- Running a workflow produces visible trace events in the new output channel.
- Events include enough metadata to map them to `workflowId` and `stepId`.
- Start events arrive before the API call finishes.
- End events include final status and safe metadata.
- Runner behavior and workflow results stay unchanged when tracing is enabled.

## Assumptions and defaults
- “Fixed endpoint” is interpreted as a fixed route like `/span-events`, not a hardcoded port.
- Phase 1 does not yet send webview RPC messages or animate nodes.
- Phase 1 uses metadata-only telemetry by design.
- Local development uses always-on sampling.
- A real OTLP Collector can be added later, but phase 1 intentionally avoids building a full OTLP receiver inside the extension.

## References
- OpenTelemetry Trace SDK spec: https://opentelemetry.io/docs/specs/otel/trace/sdk/
- OpenTelemetry Go exporters guidance: https://opentelemetry.io/docs/languages/go/exporters/
- OpenTelemetry components / Collector overview: https://opentelemetry.io/docs/concepts/components/

# README-only phase 1 OTel plan

## Summary
Document phase 1 in the existing extension README at [README.md](c:/Users/Himeth%20Walgampaya/Important/vs_plugin_testing/official_wso2/vscode-extensions/workspaces/arazzo/arazzo-designer-extension/README.md) instead of creating a new `OTeL_plan.md`.

The README addition should describe only phase 1:
- start a local tracer server inside the extension
- instrument the Go runner with OTel
- emit workflow, step, and HTTP span lifecycle events
- prove spans are arriving correctly before building any visual animations

## README changes
Add one new section near the MCP workflow runner part, titled something like `Workflow Progress Tracing (Phase 1)`.

That section should explain:

1. Goal
- show live workflow execution progress later in the visualizer
- phase 1 only focuses on tracing and delivery, not UI animation

2. Architecture
- VS Code extension starts two local services:
  - the existing MCP server
  - a new local tracer server
- the Go runner creates OTel spans while workflows run
- the runner sends span lifecycle events to the tracer server
- the tracer server logs and normalizes them
- phase 2 will forward those normalized events to the webview over the existing RPC path

3. Why OTel here
- workflow execution naturally maps to nested spans
- workflow span = whole workflow run
- step span = each Arazzo step
- HTTP span = outbound API call
- this gives parent/child timing and status cleanly

4. Important implementation note
- use a real OTel `SpanProcessor` for live `start` and `end` handling
- do not rely only on an exporter, because exporters mainly see ended spans
- the custom processor should push lightweight JSON events to the tracer server

5. Phase 1 event scope
- workflow start/end
- step start/end
- HTTP start/end

6. Metadata to include
- `workflowId`
- `stepId`
- `operationId`
- `operationPath`
- source description name
- HTTP method
- URL
- status code
- success or error status
- trace ID, span ID, parent span ID
- input and output key names only

7. Metadata to exclude
- bearer tokens
- API keys
- raw auth headers
- full request bodies
- full response bodies
- full workflow input values

8. Extension-side pieces
- new `tracer_server.ts`
- start and stop lifecycle tied to the current MCP server lifecycle
- dedicated output channel for trace logs
- local endpoint on `127.0.0.1`
- fixed route such as `/span-events`
- health route such as `/health`

9. Go-side pieces
- telemetry config passed from CLI into runner
- root workflow span in runner execution
- child step spans in step executor
- child HTTP spans in HTTP executor
- async non-blocking delivery to tracer server
- tracing failures must never fail workflow execution

10. Why interfaces are needed
- in TypeScript:
  - define the JSON event shape the tracer server accepts
  - avoid `any` and keep phase 2 RPC payloads stable
- in Go:
  - define a sink contract for sending trace events
  - keep runner logic separate from HTTP posting logic
- in OTel:
  - implement the SDK’s span processor interface to hook start/end events cleanly

11. Verification checklist
- start MCP server and confirm tracer server starts too
- run a workflow and confirm trace events appear in order
- confirm one trace ID is shared across workflow, step, and HTTP spans
- confirm failed API calls produce error status spans
- confirm tracing can fail silently without breaking workflow execution

## Acceptance criteria
The README update is complete when a new engineer can read that section and understand:
- what phase 1 is
- why OTel is being added
- what components must be built
- what metadata is sent
- why interfaces are needed
- how to verify the tracing path before starting phase 2

## Assumptions
- “the README” means the extension README already open in your editor: [README.md](c:/Users/Himeth%20Walgampaya/Important/vs_plugin_testing/official_wso2/vscode-extensions/workspaces/arazzo/arazzo-designer-extension/README.md)
- no separate `OTeL_plan.md` will be created
- no phase 2 UI animation work is documented beyond a short forward-looking note

