// Package main provides the CLI entry point for the Arazzo Designer CLI.
// It exposes a "serve" command that starts the MCP server for an Arazzo file.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/wso2/arazzo-designer-cli/internal/docker"
	"github.com/wso2/arazzo-designer-cli/internal/mcpserver"
	"github.com/wso2/arazzo-designer-cli/internal/models"
	"github.com/wso2/arazzo-designer-cli/internal/telemetry"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "serve":
		serveCmd(os.Args[2:])
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func serveCmd(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	filePath := fs.String("f", "", "Path to the Arazzo YAML file (required)")
	port := fs.Int("p", 8080, "Port to listen on")
	bearerToken := fs.String("bearer-token", "", "Bearer token for API authentication")
	apiKey := fs.String("api-key", "", "API key for authentication")
	apiKeyHeader := fs.String("api-key-header", "X-API-Key", "Header name for API key")
	traceEndpoint := fs.String("trace-endpoint", "", "URL of the local tracer server to receive span events (e.g. http://127.0.0.1:59600/span-events)")
	otlpEndpoint := fs.String("otlp-endpoint", "", "Base URL of an OTLP/HTTP trace backend (e.g. http://localhost:4318 for Jaeger/Honeycomb)")
	disableTLS := fs.Bool("disable-tls", false, "Disable TLS certificate verification for outbound HTTP requests (development only)")
	dockerMode := fs.Bool("docker", false, "Package the Arazzo server into a Docker image instead of starting it locally")
	outputDir := fs.String("o", "", "Output folder for Docker build artifacts; only valid with --docker")
	fs.StringVar(outputDir, "output-dir", "", "Output folder for Docker build artifacts; only valid with --docker")

	fs.Parse(args)

	if *filePath == "" {
		fmt.Fprintln(os.Stderr, "Error: -f flag (Arazzo file path or folder) is required")
		fs.Usage()
		os.Exit(1)
	}

	// Resolve -f: accept both a direct file path and a folder.
	resolvedPath, err := resolveArazzoFilePath(*filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	*filePath = resolvedPath

	// -o / --output-dir is only meaningful with --docker.
	if *outputDir != "" && !*dockerMode {
		fmt.Fprintln(os.Stderr, "Error: -o / --output-dir can only be used with --docker")
		fs.Usage()
		os.Exit(1)
	}

	// --docker mode: package the server into a Docker image and exit.
	// None of the server-specific flags are used in this path.
	if *dockerMode {
		if err := docker.BuildImage(docker.BuildConfig{
			ArazzoFilePath: *filePath,
			Port:           *port,
			OutputDir:      *outputDir,
		}); err != nil {
			log.Fatalf("Docker packaging failed: %v", err)
		}
		return
	}

	// Build runtime params
	if *disableTLS {
		log.Println("WARNING: TLS certificate verification is disabled")
	}
	runtimeParams := &models.RuntimeParams{
		BearerToken:            *bearerToken,
		APIKey:                 *apiKey,
		APIKeyHeader:           *apiKeyHeader,
		AuthHeaders:            make(map[string]string),
		DisableTLSVerification: *disableTLS,
	}

	// Create trace sink — combine whichever endpoints are configured.
	// VS Code plugin always passes --trace-endpoint (local custom JSON sink).
	// Standalone users can pass --otlp-endpoint to reach Jaeger, Honeycomb, etc.
	// Both flags may be provided simultaneously.
	var sink telemetry.SpanEventSink
	var sinks []telemetry.SpanEventSink
	if *traceEndpoint != "" {
		log.Printf("Local tracing enabled → %s", *traceEndpoint)
		sinks = append(sinks, telemetry.NewHTTPSink(*traceEndpoint))
	}
	if *otlpEndpoint != "" {
		log.Printf("OTLP tracing enabled → %s/v1/traces", *otlpEndpoint)
		sinks = append(sinks, telemetry.NewOTLPSink(*otlpEndpoint))
	}
	switch len(sinks) {
	case 0:
		sink = &telemetry.NoopSink{}
	case 1:
		sink = sinks[0]
	default:
		sink = telemetry.NewMultiSink(sinks...)
	}
	defer sink.Shutdown()

	// Create and start MCP server
	srv, err := mcpserver.NewMCPServer(*filePath, *port, runtimeParams, sink)
	if err != nil {
		log.Fatalf("Failed to create MCP server: %v", err)
	}

	log.Printf("Starting Arazzo MCP server for: %s", *filePath)
	if err := srv.Start(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

// resolveArazzoFilePath accepts either a path to an Arazzo file or a folder.
// When given a folder it scans for exactly one .yaml/.yml file containing the
// top-level "arazzo" key and returns its path. Returns an error if the path
// does not exist, is a folder with zero or multiple Arazzo files, or if a
// direct file path does not exist.
func resolveArazzoFilePath(input string) (string, error) {
	info, err := os.Stat(input)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("path does not exist: %s", input)
		}
		return "", fmt.Errorf("failed to access path: %w", err)
	}

	if !info.IsDir() {
		// Direct file path — use as-is.
		return input, nil
	}

	// Folder — scan for an Arazzo file.
	entries, err := os.ReadDir(input)
	if err != nil {
		return "", fmt.Errorf("failed to read folder %q: %w", input, err)
	}

	var matches []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.ToLower(e.Name())
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		candidate := filepath.Join(input, e.Name())
		if isArazzoFile(candidate) {
			matches = append(matches, candidate)
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no Arazzo file found in folder %q\n\nAn Arazzo file must be a .yaml or .yml file containing the top-level 'arazzo' key", input)
	case 1:
		fmt.Printf("Auto-detected Arazzo file: %s\n", matches[0])
		return matches[0], nil
	default:
		return "", fmt.Errorf("multiple Arazzo files found in folder %q:\n  %s\n\nPlease specify the exact file using -f <file>", input, strings.Join(matches, "\n  "))
	}
}

// isArazzoFile returns true if the YAML file contains the top-level "arazzo" key.
func isArazzoFile(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return false
	}
	_, ok := raw["arazzo"]
	return ok
}

func printUsage() {
	fmt.Println(`Arazzo Designer CLI - Arazzo Workflow Runner & MCP Server

Usage:
  arazzo-designer-cli <command> [flags]

Commands:
  serve    Start the MCP server for an Arazzo file
  help     Show this help message

Flags (serve):
  -f                Path to the Arazzo YAML file (required)
  -p                Port to listen on (default 8080)
  --trace-endpoint  Local tracer server URL (used by the VS Code extension)
  --otlp-endpoint   OTLP/HTTP base URL for external tracing (e.g. http://localhost:4318)
  --bearer-token    Bearer token for API auth
  --api-key         API key for API auth
  --disable-tls     Disable TLS certificate verification for outbound requests (development only)
  --docker          Package the server into a Docker image instead of starting it (requires Go + Docker)
  -o, --output-dir  Output folder for Docker build artifacts; only valid with --docker

Examples:
  # VS Code plugin (automatic)
  arazzo-designer-cli serve -f workflow.arazzo.yaml --trace-endpoint http://127.0.0.1:59600/span-events

  # Standalone with Jaeger
  arazzo-designer-cli serve -f workflow.arazzo.yaml -p 8080 --otlp-endpoint http://localhost:4318

  # Both simultaneously
  arazzo-designer-cli serve -f workflow.arazzo.yaml --trace-endpoint http://127.0.0.1:59600/span-events --otlp-endpoint http://localhost:4318

  # Package into a Docker image (does not start a server)
  arazzo-designer-cli serve -f workflow.arazzo.yaml -p 8080 --docker

  # Package into a Docker image and keep build artifacts in ./docker-output
  arazzo-designer-cli serve -f workflow.arazzo.yaml -p 8080 --docker -o ./docker-output`)
}
