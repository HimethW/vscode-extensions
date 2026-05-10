# Plan: Configure Inputs Panel For Curl Try Flow

## Summary
Add a gear-based Configure Inputs panel to the workflow webview. The panel lets users enter workflow input values, saves them in VS Code `workspaceState`, and reuses them when generating the `/run` curl command.

The webview already has the selected workflow model, so input schema discovery should happen in the webview. The extension owns persistent storage and terminal curl generation, so the webview communicates with the extension through RPC.

The RPC types are just the message contracts between the webview and extension:
- `GetWorkflowRunInputsRequest`: webview asks extension for saved inputs for one file/workflow.
- `GetWorkflowRunInputsResponse`: extension returns saved values, if any.
- `SaveWorkflowRunInputsRequest`: webview sends validated/coerced input values to save.
- `RunWorkflowRequest.inputs`: webview passes saved/coerced inputs to the existing Try command so curl can use them.

## Key UX Rules
- Add a gear icon button near the top-right workflow actions in `WorkflowTitleBar`.
- Clicking the gear opens a right-side Configure Inputs panel, similar to the existing properties panel.
- The panel contains a collapsible `Inputs` section for the current workflow.
- Each field is initialized using this exact precedence:
  1. Saved value from `workspaceState`
  2. Workflow input default value
  3. Empty field with placeholder text `ENTER`
- `ENTER` is only placeholder text, not an actual saved value and not sent in curl.
- If a required input has no saved/default value, the field remains empty and invalid until the user enters a value.
- Clicking Apply validates/coerces values, saves them to `workspaceState`, and closes the panel.
- Clicking Try with missing required saved inputs auto-opens the Configure Inputs panel and shows a short message telling the user to fill required inputs first.
- After the user fixes the values and clicks Apply from that auto-opened flow, generate the curl immediately.

## Implementation Changes
- Extend visualizer RPC types in `arazzo-designer-core`:
  - `GetWorkflowRunInputsRequest`: `{ uri: string; workflowId: string }`
  - `GetWorkflowRunInputsResponse`: `{ inputs?: Record<string, any> }`
  - `SaveWorkflowRunInputsRequest`: `{ uri: string; workflowId: string; inputs: Record<string, any> }`
  - `RunWorkflowRequest`: add `inputs?: Record<string, any>`
- Wire the new RPC methods through:
  - `arazzo-designer-rpc-client`
  - `arazzo-designer-extension/src/rpc-managers/visualizer/rpc-manager.ts`
  - `arazzo-designer-extension/src/rpc-managers/visualizer/rpc-handler.ts`
- Store inputs in extension `workspaceState` under one versioned key:
  ```ts
  const RUN_INPUTS_STATE_KEY = 'arazzo.runInputs.v1';
  ```
- Use this storage shape:
  ```ts
  {
    [fileUri: string]: {
      [workflowId: string]: {
        inputs: Record<string, any>;
        updatedAt: number;
      }
    }
  }
  ```
- `saveWorkflowRunInputs` overwrites the whole input object for that `fileUri + workflowId`.
- `getWorkflowRunInputs` returns only values for the current file/workflow.
- Phase 1 does not clean stale values when the Arazzo file changes.

## Webview Input Panel
- In `WorkflowView.tsx`, derive fields from the current `workflow`.
- Resolve `workflow.inputs` references using existing `referenceUtils.ts`.
- Support both input styles:
  - `workflow.inputs.properties`
  - workflow-level `parameters` where `in` is missing, empty, or `inputs`
- Create a small helper type:
  ```ts
  type WorkflowInputField = {
    name: string;
    type: string;
    required: boolean;
    description?: string;
    defaultValue?: any;
    schema?: any;
  };
  ```
- Required detection(show a red start near the input field):
  - `inputs.required`
  - property-level `required: true`
  - parameter-level `required: true`
- If a field exists in both `inputs.properties` and `parameters`, prefer `inputs.properties`.
- Create a new `WorkflowInputConfigPanel` component or keep it local to `WorkflowView` if smaller.
- Reuse existing `SidePanel`, `SidePanelTitleContainer`, `SidePanelBody`, `Button`, and `Codicon` patterns.
- Reuse styling ideas from `NodePropertiesPanel` 

## Validation And Coercion
- Add a small TypeScript utility for input values:
  - `buildInitialFieldValues(fields, savedInputs)`
  - `coerceInputValue(raw, type)`
  - `stringifyInputValue(value, type)`
  - `hasMissingRequiredInputs(fields, inputs)`
- Coercion rules:
  - `string`: convert typed value to string.
  - `integer`: accept whole numbers only.
  - `number`: accept valid numeric values.
  - `boolean`: accept `true` or `false`.
  - `object`: require valid JSON object.
  - `array`: require valid JSON array.
  - unknown type: treat as string.
- Invalid fields get red styling and inline error text.
- Apply is disabled while any visible field is invalid or any required field is empty.
- Do not implement full nested JSON Schema validation in phase 1; the Go `/run` endpoint remains final validation.

## Try Curl Flow
- Update `handleTryCurlWorkflow` in `WorkflowView.tsx`:
  - Load or use current saved input state.
  - If required inputs are missing, open Configure Inputs panel and set a “pending curl generation” flag.
  - If inputs are available, call `runWorkflow({ workflowId, uri: fileUri, mode: 'curl', inputs })`.
- Update `VisualizerRpcManager.runWorkflow` to pass `inputs` through to `arazzo.tryWorkflow`.
- Update `arazzo.tryWorkflow` in `extension.ts`:
  - Prefer `args.inputs` for the curl body.
  - If `args.inputs` is absent, optionally read from `workspaceState` as a fallback.
  - Keep existing server start/restart behavior unchanged.
- Update `buildRunCommand`:
  ```ts
  buildRunCommand(workflowId: string, port: number, filePath: string, configuredInputs?: Record<string, any>): string
  ```
- Curl body must be:
  ```json
  {
    "inputs": { ...configuredInputs }
  }
  ```
- Do not insert `ENTER` into generated curl.
- Existing default extraction may remain only as fallback for command invocations that do not pass configured inputs.

## Test Plan
- Unit-test input utilities:
  - saved value wins over default
  - default appears when saved value is missing
  - missing default produces empty field with `ENTER` placeholder
  - `ENTER` placeholder is not saved as a value
  - integer rejects `hello` and `2.5`
  - number rejects non-numeric text
  - boolean rejects values other than `true`/`false`
  - object/array require valid JSON and correct shape
- Manual test in VS Code:
  - Open workflow with required inputs and no saved values.
  - Click Configure: fields show saved values, defaults, or `ENTER` placeholders correctly.
  - Click Try before required values are saved: config panel opens automatically.
  - Enter invalid integer: field turns red and Apply is disabled.
  - Enter valid values and Apply: values save to `workspaceState`.
  - Curl is generated with typed JSON values.
  - Click Try again: curl is generated immediately from saved values.
  - Open another Arazzo file with same workflowId: values do not leak because storage is keyed by file URI.

## Assumptions
- Phase 1 uses `workspaceState`
- Phase 1 does not prune stale inputs after workflow schema changes.
- Phase 1 does not support named presets.
- Phase 1 does not perform full nested schema validation at the webview.
- `ENTER` is UI placeholder text only, never an actual request value.
