// Package docker implements the --docker packaging mode for the Arazzo CLI.
// It cross-compiles a Linux binary, assembles a self-contained Docker build
// context, builds the image, and prints the resulting "docker run" command.
// No server is started; the function returns immediately after the build.
package docker

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/wso2/arazzo-designer-cli/internal/loader"
	"github.com/wso2/arazzo-designer-cli/internal/models"
)

// BuildConfig holds the parameters for Docker image packaging.
type BuildConfig struct {
	// ArazzoFilePath is the path to the Arazzo YAML/JSON file to bundle.
	ArazzoFilePath string
	// Port is the port the Arazzo server will listen on inside the container.
	Port int
	// OutputDir is an optional directory where build artifacts are kept after
	// the Docker image is built. When empty a temporary directory is used and
	// deleted automatically. When set the directory is created if absent, files
	// are written into it, and it is never deleted by the CLI.
	OutputDir string
}

// BuildImage cross-compiles a Linux binary, assembles a Docker build context,
// builds the image, and prints the resulting docker run command.
// All other flags (bearer-token, api-key, etc.) are NOT baked into the image;
// they can be passed at "docker run" time as environment variables or CLI args
// once the user adapts the run command for their needs.
func BuildImage(cfg BuildConfig) error {
	// ── 1. Verify Docker is available before doing any work ──────────────────
	if err := checkDockerAvailable(); err != nil {
		return err
	}

	// ── 2. Resolve the Arazzo file and parse it to discover local sources ────
	absArazzo, err := filepath.Abs(cfg.ArazzoFilePath)
	if err != nil {
		return fmt.Errorf("failed to resolve arazzo file path: %w", err)
	}
	doc, err := loader.LoadArazzoDoc(absArazzo)
	if err != nil {
		return fmt.Errorf("failed to parse arazzo file: %w", err)
	}

	// ── 3. Locate the Go module root for cross-compilation ───────────────────
	moduleRoot, err := findModuleRoot()
	if err != nil {
		return fmt.Errorf(
			"cannot locate Go module root: %w\n\nRun 'arazzo-designer-cli serve --docker' from the CLI source directory (the folder containing go.mod)",
			err,
		)
	}

	// ── 4. Resolve the Docker build context directory ────────────────────────
	// When -o/--output-dir is provided we write artifacts into that folder and
	// keep them after the build so the user can inspect or reuse them.
	// When it is absent we create a temporary directory that is cleaned up
	// automatically once the build finishes (success or failure).
	buildDir, cleanup, err := resolveBuildDir(cfg.OutputDir)
	if err != nil {
		return err
	}
	defer cleanup()

	// ── 5. Cross-compile a CGO-free Linux binary into the build context ──────
	log.Printf("Cross-compiling linux/%s binary...", targetArch())
	linuxBin := filepath.Join(buildDir, "arazzo-designer-cli")
	if err := crossCompileLinux(moduleRoot, linuxBin); err != nil {
		return fmt.Errorf("cross-compilation failed: %w", err)
	}

	// ── 6. Assemble the workspace directory inside the build context ─────────
	// Contains the Arazzo file and every local (non-HTTP) source description.
	workspaceDir := filepath.Join(buildDir, "workspace")
	// Remove and recreate workspace/ so stale files from previous -o runs
	// are never included in the Docker build context.
	if err := os.RemoveAll(workspaceDir); err != nil {
		return fmt.Errorf("failed to clean workspace directory: %w", err)
	}
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		return fmt.Errorf("failed to create workspace directory in build context: %w", err)
	}
	arazzoDir := filepath.Dir(absArazzo)
	arazzoFileName := filepath.Base(absArazzo)

	if err := copyFile(absArazzo, filepath.Join(workspaceDir, arazzoFileName)); err != nil {
		return fmt.Errorf("failed to copy arazzo file into build context: %w", err)
	}
	if err := copyLocalSourceDescriptions(doc, arazzoDir, workspaceDir); err != nil {
		return fmt.Errorf("failed to copy source description files into build context: %w", err)
	}

	// ── 7. Write the Dockerfile ───────────────────────────────────────────────
	imageName := sanitizeImageName(doc.Info.Title)
	dockerfile := generateDockerfile(arazzoFileName, cfg.Port)
	if err := os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte(dockerfile), 0644); err != nil {
		return fmt.Errorf("failed to write Dockerfile: %w", err)
	}

	// ── 8. Build the Docker image ─────────────────────────────────────────────
	log.Printf("Building Docker image '%s'...", imageName)
	buildCmd := exec.Command("docker", "build", "-t", imageName, ".")
	buildCmd.Dir = buildDir
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		return fmt.Errorf("docker build failed: %w", err)
	}

	// ── 9. Persist the docker run command when an output dir was requested ───
	runCmd := buildRunCommand(imageName, cfg.Port)
	if cfg.OutputDir != "" {
		if err := writeRunCommand(buildDir, runCmd); err != nil {
			return fmt.Errorf("failed to write run-command.txt: %w", err)
		}
	}

	// ── 10. Print the success summary ─────────────────────────────────────────
	printSummary(imageName, cfg.Port, cfg.OutputDir, buildDir, runCmd)
	return nil
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// resolveBuildDir decides where to place the Docker build context.
// Returns the directory path and a cleanup function.
// For a temporary directory the cleanup function removes it.
// For a user-provided output directory the cleanup function is a no-op,
// because user data must never be deleted by the CLI.
func resolveBuildDir(outputDir string) (string, func(), error) {
	noop := func() {}
	if outputDir != "" {
		abs, err := filepath.Abs(outputDir)
		if err != nil {
			return "", noop, fmt.Errorf("failed to resolve output directory path: %w", err)
		}
		if err := os.MkdirAll(abs, 0755); err != nil {
			return "", noop, fmt.Errorf("failed to create output directory %q: %w", outputDir, err)
		}
		return abs, noop, nil
	}

	// No output dir — use a temp folder under ~/.arazzo-cli/.tmp/
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", noop, fmt.Errorf("failed to determine home directory: %w", err)
	}
	baseDir := filepath.Join(homeDir, ".arazzo-cli", ".tmp")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return "", noop, fmt.Errorf("failed to create build cache directory: %w", err)
	}
	tmpDir, err := os.MkdirTemp(baseDir, "docker-build-*")
	if err != nil {
		return "", noop, fmt.Errorf("failed to create temporary build directory: %w", err)
	}
	cleanup := func() { os.RemoveAll(tmpDir) }
	return tmpDir, cleanup, nil
}

// writeRunCommand writes the docker run command string to run-command.txt
// inside the given directory so the user can easily reference it later.
func writeRunCommand(dir, runCmd string) error {
	content := fmt.Sprintf("%s\n", runCmd)
	return os.WriteFile(filepath.Join(dir, "run-command.txt"), []byte(content), 0644)
}

// checkDockerAvailable verifies that the Docker CLI and daemon are reachable.
func checkDockerAvailable() error {
	cmd := exec.Command("docker", "info")
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Docker is not available or not running.\nPlease install and start Docker before using --docker")
	}
	return nil
}

// findModuleRoot walks upward from the current working directory to find a
// go.mod file. If CWD yields no result it retries from the executable path.
func findModuleRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if root := walkUpForGoMod(cwd); root != "" {
		return root, nil
	}
	// Retry from the directory containing the running binary.
	if exe, err := os.Executable(); err == nil {
		if root := walkUpForGoMod(filepath.Dir(exe)); root != "" {
			return root, nil
		}
	}
	return "", fmt.Errorf("go.mod not found starting from %s", cwd)
}

// walkUpForGoMod ascends the directory tree starting at start and returns the
// first directory that contains a go.mod file. Returns "" if none is found.
func walkUpForGoMod(start string) string {
	dir := filepath.Clean(start)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "" // reached filesystem root
		}
		dir = parent
	}
}

// targetArch returns the GOARCH value for the Linux binary.
// Mac/ARM hosts target arm64 so the image runs natively; everything else gets amd64.
func targetArch() string {
	if runtime.GOARCH == "arm64" {
		return "arm64"
	}
	return "amd64"
}

// crossCompileLinux builds a CGO-free Linux binary of the CLI at outPath.
func crossCompileLinux(moduleRoot, outPath string) error {
	cmd := exec.Command("go", "build", "-o", outPath, "./cmd/")
	cmd.Dir = moduleRoot
	cmd.Env = append(os.Environ(),
		"GOOS=linux",
		"GOARCH="+targetArch(),
		"CGO_ENABLED=0",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// copyLocalSourceDescriptions copies every source description file referenced
// by a local (non-HTTP) URL into dstDir, preserving its relative sub-path.
// Remote URLs are intentionally skipped — the server fetches them at runtime.
// Returns an error if any local URL references a path outside dstDir, which
// would either escape the Docker build context or be unreachable at runtime.
func copyLocalSourceDescriptions(doc *models.ArazzoDoc, srcDir, dstDir string) error {
	absDst, err := filepath.Abs(dstDir)
	if err != nil {
		return fmt.Errorf("failed to resolve workspace directory: %w", err)
	}
	for _, sd := range doc.SourceDescriptions {
		url := sd.URL
		if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
			continue
		}
		srcFile := filepath.Join(srcDir, filepath.FromSlash(url))
		dstFile := filepath.Clean(filepath.Join(absDst, filepath.FromSlash(url)))
		// Reject paths that escape the workspace directory.
		if !strings.HasPrefix(dstFile, absDst+string(filepath.Separator)) {
			return fmt.Errorf(
				"source description %q resolves outside the Arazzo file directory and cannot be bundled into the Docker image.\nMove the file next to (or beneath) the Arazzo file and update its URL",
				url,
			)
		}
		if err := os.MkdirAll(filepath.Dir(dstFile), 0755); err != nil {
			return fmt.Errorf("failed to create parent directory for %s: %w", url, err)
		}
		if err := copyFile(srcFile, dstFile); err != nil {
			return fmt.Errorf("failed to copy source description %q: %w", url, err)
		}
	}
	return nil
}

// generateDockerfile returns the Dockerfile content for the Arazzo server image.
// The image uses a minimal Debian slim base and runs the CLI binary directly,
// so no language runtime or extra tooling is required inside the container.
func generateDockerfile(arazzoFileName string, port int) string {
	var b strings.Builder
	b.WriteString("FROM debian:bookworm-slim\n")
	b.WriteString("RUN apt-get update \\\n")
	b.WriteString("    && apt-get install -y --no-install-recommends ca-certificates \\\n")
	b.WriteString("    && rm -rf /var/lib/apt/lists/*\n")
	b.WriteString("WORKDIR /app\n")
	b.WriteString("COPY arazzo-designer-cli /usr/local/bin/arazzo-designer-cli\n")
	b.WriteString("RUN chmod +x /usr/local/bin/arazzo-designer-cli\n")
	b.WriteString("COPY workspace/ /app/workspace/\n")
	b.WriteString(fmt.Sprintf("EXPOSE %d\n", port))
	// Use ENTRYPOINT to fix the required arguments.
	// This ensures that even if the user passes extra flags to "docker run",
	// the container always knows which Arazzo file to use.
	b.WriteString(fmt.Sprintf(
		"ENTRYPOINT [\"arazzo-designer-cli\", \"serve\", \"-f\", \"/app/workspace/%s\", \"-p\", \"%d\"]\n",
		arazzoFileName, port,
	))
	return b.String()
}

// sanitizeImageName converts a title into a valid, lowercase Docker image name.
func sanitizeImageName(title string) string {
	name := strings.ToLower(title)
	re := regexp.MustCompile(`[^a-z0-9]+`)
	name = re.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	if name == "" {
		name = "arazzo-server"
	} else {
		name += "-arazzo-server"
	}
	return name
}

// copyFile copies a single file from src to dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// buildRunCommand constructs the minimal docker run command for the user.
// It drops flags already baked into the image (-f, -p) and build-only flags
// (--docker, -o/--output-dir). localhost/127.0.0.1 are mapped to
// host.docker.internal so the command works out-of-the-box.
func buildRunCommand(imageName string, port int) string {
	var extra []string
	args := os.Args[2:] // skip binary + "serve"
	for i := 0; i < len(args); i++ {
		a := args[i]
		flagName, _, hasEq := strings.Cut(a, "=")
		switch flagName {
		case "--docker":
			// build-only flag, no value
		case "-o", "--output-dir":
			if !hasEq {
				i++ // skip separate value
			}
		case "-f", "--file", "-p":
			if !hasEq {
				i++ // skip separate value; already baked into image CMD
			}
		default:
			a = strings.ReplaceAll(a, "localhost", "host.docker.internal")
			a = strings.ReplaceAll(a, "127.0.0.1", "host.docker.internal")
			extra = append(extra, a)
		}
	}
	cmd := fmt.Sprintf("docker run --rm -p %d:%d %s", port, port, imageName)
	if len(extra) > 0 {
		cmd += " " + strings.Join(extra, " ")
	}
	return cmd
}

// printSummary writes the post-build instructions to stdout.
// When outputDir is non-empty the path to the retained artifacts is shown.
func printSummary(imageName string, port int, outputDir, buildDir, runCmd string) {
	fmt.Println()
	fmt.Println("Docker image built successfully!")
	fmt.Println()
	fmt.Printf("  Image:  %s\n", imageName)
	fmt.Printf("  Run:    %s\n", runCmd)
	fmt.Printf("  MCP:    http://localhost:%d/mcp\n", port)
	fmt.Printf("  Run wf: POST http://localhost:%d/run/{workflowId}\n", port)
	fmt.Printf("  Result: GET  http://localhost:%d/lastResult/{workflowId}\n", port)
	if outputDir != "" {
		fmt.Println()
		fmt.Printf("  Artifacts saved to: %s\n", buildDir)
		fmt.Printf("    Dockerfile        %s/Dockerfile\n", buildDir)
		fmt.Printf("    Linux binary      %s/arazzo-designer-cli\n", buildDir)
		fmt.Printf("    Workspace files   %s/workspace/\n", buildDir)
		fmt.Printf("    Run command       %s/run-command.txt\n", buildDir)
	}
	fmt.Println()
}
