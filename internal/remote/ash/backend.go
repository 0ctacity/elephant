// Package ash transports Elephant's JSON protocol using ASH's one-shot exec CLI.
// ASH currently does not forward stdin for exec. A POSIX printf pipeline passes
// shell-quoted JSON to the remote Elephant receiver's stdin instead. Consequently
// requests must fit in a command-line argument; no remote database is accessed.
package ash

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

const (
	// MaxCommandSize leaves space for ASH arguments on supported operating systems.
	MaxCommandSize = 24 << 10
	// MaxResponseSize bounds remote output below ASH's own stdout limit.
	MaxResponseSize = 4 << 20
)

// Config selects an ASH host and optional local config and remote executable.
// ElephantPath is a literal executable name or path, never shell code.
type Config struct {
	Host         string `json:"host"`
	ConfigPath   string `json:"config_path,omitempty"`
	ElephantPath string `json:"elephant_path,omitempty"`
}

// ValidateConfig checks execution configuration without connecting to the host.
func ValidateConfig(c Config) error {
	if strings.TrimSpace(c.Host) == "" || len(c.Host) > 256 {
		return errors.New("ASH host must contain 1 to 256 bytes")
	}
	for _, value := range []string{c.Host, c.ConfigPath, c.ElephantPath} {
		if strings.ContainsRune(value, '\x00') || len(value) > 4096 {
			return errors.New("ASH configuration values must be NUL-free and at most 4096 bytes")
		}
	}
	return nil
}

// Executor is the local process boundary. Implementations must honor ctx and
// propagate stdout/stderr writer errors. It never receives a local shell command.
type Executor interface {
	Run(ctx context.Context, name string, args []string, stdout, stderr io.Writer) error
}

// Backend executes a single request. Its zero value uses the ash binary on PATH.
// Retries belong to the caller, which must reuse the same message ID and payload.
type Backend struct {
	Executor Executor
	Binary   string
}

// Exchange returns the raw JSON emitted by remote Elephant. Protocol semantics
// (including a structured rejection) are interpreted by the application layer.
func (b Backend) Exchange(ctx context.Context, c Config, payload []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateConfig(c); err != nil {
		return nil, err
	}
	if len(payload) > MaxCommandSize || !json.Valid(payload) {
		return nil, errors.New("ASH request must be valid JSON within the command size limit")
	}
	path := c.ElephantPath
	if path == "" {
		path = "elephant"
	}
	command := "printf '%s' " + quote(string(payload)) + " | " + quote(path) + " receive"
	if len(command) > MaxCommandSize {
		return nil, fmt.Errorf("ASH request exceeds %d-byte command limit", MaxCommandSize)
	}
	args := make([]string, 0, 6)
	if c.ConfigPath != "" {
		args = append(args, "--config", c.ConfigPath)
	}
	args = append(args, "exec", c.Host, "--", command)
	binary := b.Binary
	if binary == "" {
		binary = "ash"
	}
	runner := b.Executor
	if runner == nil {
		runner = processExecutor{}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	stdout := limitedBuffer{limit: MaxResponseSize}
	stderr := limitedBuffer{limit: 16 << 10}
	err := runner.Run(ctx, binary, args, &stdout, &stderr)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if stdout.exceeded || stderr.exceeded {
		return nil, errors.New("ASH response exceeded output limit")
	}
	if err != nil {
		// Command errors intentionally omit argv, which contains project state.
		return nil, fmt.Errorf("ASH execution failed: %w: %s", err, strings.TrimSpace(stderr.buffer.String()))
	}
	if !json.Valid(stdout.buffer.Bytes()) {
		return nil, errors.New("ASH returned invalid JSON from Elephant")
	}
	return stdout.buffer.Bytes(), nil
}

func quote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

type processExecutor struct{}

func (processExecutor) Run(ctx context.Context, name string, args []string, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = time.Second
	return cmd.Run()
}

type limitedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.buffer.Len() {
		b.exceeded = true
		return 0, errors.New("output limit exceeded")
	}
	return b.buffer.Write(p)
}
