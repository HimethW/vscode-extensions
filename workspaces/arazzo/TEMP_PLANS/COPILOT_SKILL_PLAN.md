# Copilot Skill Plan For Arazzo Extension

## Summary
Ship Copilot guidance with the VS Code extension using VS Code’s built-in Copilot customization contribution points: `chatInstructions` for automatic Arazzo knowledge, and `chatPromptFiles` for explicit user-invoked workflows. Do not ship the full internal examples as user-facing examples; instead distill them into compact patterns and templates inside the instruction/prompt files.

This gives Copilot enough context to help users create Arazzo files, start the Arazzo server, run workflows through MCP, use direct curl execution, and disable TLS verification safely.

References:
- VS Code `chatInstructions` / `chatPromptFiles`: https://code.visualstudio.com/api/references/contribution-points
- VS Code prompt files: https://code.visualstudio.com/docs/copilot/customization/prompt-files
- GitHub Copilot custom instructions: https://docs.github.com/en/copilot/how-tos/configure-custom-instructions/add-repository-instructions

## Key Changes

### Extension Packaging
- Add a new extension folder, for example:
  - `prompts/arazzo.instructions.md`
  - `prompts/create-arazzo-workflow.prompt.md`
  - `prompts/start-arazzo-server.prompt.md`
  - `prompts/disable-arazzo-tls.prompt.md`
- Update `package.json`:
  - Add `contributes.chatInstructions` pointing to `./prompts/arazzo.instructions.md`.
  - Add `contributes.chatPromptFiles` for the three task prompts.
- Keep the content small and product-focused so it fits well into Copilot context.

### Automatic Instruction File
- `arazzo.instructions.md` should teach Copilot:
  - Arazzo describes multi-step API workflows over OpenAPI source descriptions.
  - Prefer `sourceDescriptions` that point to local or remote OpenAPI files.
  - Use `workflows[].workflowId`, `steps[].operationId`, `successCriteria`, step `outputs`, and final workflow `outputs`.
  - Use `$inputs`, `$steps.<stepId>.outputs.<name>`, and `$response.body#/...` patterns.
  - Do not invent APIs; inspect existing OpenAPI files when available.
  - Keep generated Arazzo minimal, valid, and runnable by this extension.
  - Explain that the extension starts an Arazzo server exposing `/mcp` and `/run/{workflowId}`.
  - Explain TLS behavior: TLS verification is on by default; disable only for local/custom invalid certificates using the VS Code setting.

### Prompt Files
- `create-arazzo-workflow.prompt.md`:
  - Purpose: help users generate an Arazzo file from an OpenAPI file or selected operations.
  - Include one compact two-step example pattern derived from existing examples, not the full internal examples.
  - Tell Copilot to ask for missing OpenAPI path/operations only if not inferable.
- `start-arazzo-server.prompt.md`:
  - Purpose: teach users how to start the server from the extension.
  - Include: open an Arazzo file, click the play/start server command, extension writes `.vscode/mcp.json`, Copilot can then call `/mcp`.
  - Mention direct execution through generated curl against `/run/{workflowId}`.
- `disable-arazzo-tls.prompt.md`:
  - Purpose: guide safe TLS disabling.
  - Include: setting name currently used by the extension, expected checkbox behavior, restart requirement, and warning that it disables certificate verification for outbound workflow API calls only.
  - Tell Copilot not to suggest curl TLS flags for this feature, because curl only calls the local Arazzo server.

## Implementation Notes
- Use the current setting key from the codebase/package manifest, not an older name. If the current key is `arazzo.disableTLSCertificationValidation`, the skill must use that exact key.
- Keep examples generic: petstore/toolshop-style names are fine, but do not package private or large internal examples.
- Add short frontmatter metadata to each `.prompt.md` / `.instructions.md` file if VS Code expects it for name/description.
- Do not create a new custom Copilot chat participant for v1. It is heavier than needed; contributed instructions and prompt files are enough for teaching Copilot.

## Test Plan
- Build/package validation:
  - Run extension TypeScript compile.
  - Package the VSIX or run the extension host and confirm `package.json` accepts `chatInstructions` and `chatPromptFiles`.
- Copilot behavior:
  - Open an Arazzo/OpenAPI workspace and ask Copilot to create a two-step Arazzo workflow.
  - Confirm Copilot uses Arazzo concepts correctly and does not expose full internal examples.
  - Invoke the prompt file for starting the server and confirm it references the extension’s start command/play button and MCP config behavior.
  - Invoke the TLS prompt and confirm it explains the checkbox, restart requirement, and correct setting key.
- Regression:
  - Confirm existing server start, MCP config generation, direct curl flow, and TLS setting still work unchanged.

## Assumptions
- “Skill” means Copilot guidance shipped with the VS Code extension, not a separate Marketplace Copilot Extension or custom chat participant.
- Full internal examples should not be shipped as visible example files; only distilled templates/patterns should be included.
- The v1 goal is teaching and guidance, not adding new runtime commands or changing the MCP server protocol.
