package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"elephant/internal/model"
)

// Input describes one setup request.
type Input struct {
	Agent      string
	Executable string
	Database   string
	ActorID    string
	ConfigPath string
}

// Result reports what setup changed for one agent.
type Result struct {
	Agent      string   `json:"agent"`
	ConfigPath string   `json:"config_path,omitempty"`
	Command    []string `json:"mcp_command,omitempty"`
	ActorID    string   `json:"actor_id,omitempty"`
	Created    bool     `json:"created"`
	Changed    bool     `json:"changed"`
	Configured bool     `json:"configured"`
	Manual     string   `json:"manual,omitempty"`
}

// State reports the Elephant entry currently present in an agent configuration.
type State struct {
	Agent      string   `json:"agent"`
	ConfigPath string   `json:"config_path,omitempty"`
	Exists     bool     `json:"exists"`
	Configured bool     `json:"configured"`
	Command    []string `json:"mcp_command,omitempty"`
	ActorID    string   `json:"actor_id,omitempty"`
	Detail     string   `json:"detail,omitempty"`
}

func launchCommand(executable, database string) []string {
	command := []string{executable}
	if database != "" {
		command = append(command, "--db", database)
	}
	return append(command, "serve")
}

// Command returns the MCP stdio launch command Elephant writes into agent
// configuration and reports from doctor.
func Command(executable, database string) []string {
	return launchCommand(strings.TrimSpace(executable), strings.TrimSpace(database))
}

func resolveExecutable(exe string) (string, error) {
	if strings.TrimSpace(exe) == "" {
		return "", fmt.Errorf("%w: Elephant executable path is required", model.ErrInvalidInput)
	}
	abs, err := filepath.Abs(exe)
	if err != nil {
		return "", fmt.Errorf("%w: resolve Elephant executable: %v", model.ErrInvalidInput, err)
	}
	return abs, nil
}

func validateActor(actor string) error {
	if strings.TrimSpace(actor) == "" || len(actor) > 300 || strings.ContainsRune(actor, 0) {
		return fmt.Errorf("%w: actor ID must be 1–300 bytes without NUL", model.ErrInvalidInput)
	}
	return nil
}

func readFile(path string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return string(data), true, nil
	}
	if os.IsNotExist(err) {
		return "", false, nil
	}
	return "", false, fmt.Errorf("%w: read %s: %v", model.ErrInvalidInput, path, err)
}

func writeFile(path, content string, existed bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("%w: create configuration directory: %v", model.ErrInvalidInput, err)
	}
	mode := os.FileMode(0600)
	if existed {
		if info, err := os.Stat(path); err == nil {
			mode = info.Mode().Perm()
		}
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		return fmt.Errorf("%w: write %s: %v", model.ErrInvalidInput, path, err)
	}
	return nil
}

// Configure registers Elephant's MCP server with the selected agent. It is
// idempotent: repeating it with the same command and actor leaves the file
// byte-identical. Unsupported agents receive a manual example instead of an
// error so the caller can still guide the user.
func Configure(in Input, env Env) (Result, error) {
	name := strings.TrimSpace(in.Agent)
	out := Result{Agent: name, ActorID: strings.TrimSpace(in.ActorID)}
	if name == "" {
		return out, fmt.Errorf("%w: agent name is required", model.ErrInvalidInput)
	}
	if in.ActorID != "" && out.ActorID == "" {
		return out, fmt.Errorf("%w: actor ID must be 1–300 bytes without NUL", model.ErrInvalidInput)
	}
	executable, err := resolveExecutable(in.Executable)
	if err != nil {
		return out, err
	}
	database := strings.TrimSpace(in.Database)
	if database != "" {
		if database, err = filepath.Abs(database); err != nil {
			return out, fmt.Errorf("%w: resolve database path: %v", model.ErrInvalidInput, err)
		}
	}
	target, err := Lookup(name)
	if err != nil {
		manualActor := out.ActorID
		if manualActor == "" {
			manualActor = "<actor-id>"
		}
		out.Manual = Manual(name, executable, database, manualActor)
		out.ActorID = ""
		return out, nil
	}
	path := strings.TrimSpace(in.ConfigPath)
	if path == "" {
		if path, err = target.Path(env); err != nil {
			return out, err
		}
	}
	if path, err = filepath.Abs(path); err != nil {
		return out, fmt.Errorf("%w: resolve configuration path: %v", model.ErrInvalidInput, err)
	}
	content, existed, err := readFile(path)
	if err != nil {
		return out, err
	}
	state, err := readState(target, path, content, existed)
	if err != nil {
		return out, err
	}
	if out.ActorID == "" {
		out.ActorID = state.ActorID
	}
	if out.ActorID == "" {
		out.ActorID = name + "-" + uuid.Must(uuid.NewV7()).String()
	}
	if err = validateActor(out.ActorID); err != nil {
		return out, err
	}
	command := launchCommand(executable, database)
	updated, err := edit(target, content, command, out.ActorID)
	if err != nil {
		return out, err
	}
	out.ConfigPath, out.Command, out.Created, out.Configured = path, command, !existed, true
	out.Changed = updated != content
	if !out.Changed {
		return out, nil
	}
	if err = writeFile(path, updated, existed); err != nil {
		return out, err
	}
	return out, nil
}

// Inspect reports whether Elephant is registered with an agent. A missing
// configuration file is reported through State.Exists instead of an error.
func Inspect(name string, env Env) (State, error) {
	target, err := Lookup(name)
	if err != nil {
		return State{Agent: strings.TrimSpace(name)}, err
	}
	path, err := target.Path(env)
	if err != nil {
		return State{Agent: target.Name}, err
	}
	content, existed, err := readFile(path)
	if err != nil {
		return State{Agent: target.Name, ConfigPath: path}, err
	}
	return readState(target, path, content, existed)
}
