package agent_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"elephant/internal/agent"
)

func env(m map[string]string) agent.Env {
	return func(key string) string { return m[key] }
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestConfigureCodexPreservesUnrelatedSettingsAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.toml")
	original := "approval_policy = \"never\"\n\n[mcp_servers.other]\ncommand = \"other-server\"\nargs = [\"--flag\"]\n\n[projects.\"/tmp/example\"]\ntrust_level = \"trusted\"\n"
	write(t, path, original)
	in := agent.Input{Agent: "codex", Executable: "/opt/elephant/bin/elephant", Database: "/home/dev/elephant.zova", ActorID: "codex-session", ConfigPath: path}
	first, err := agent.Configure(in, env(map[string]string{"CODEX_HOME": root}))
	if err != nil {
		t.Fatal(err)
	}
	if !first.Configured || !first.Changed || first.Created || first.ConfigPath != path {
		t.Fatalf("%+v", first)
	}
	content := read(t, path)
	for _, want := range []string{"approval_policy = \"never\"", "[mcp_servers.other]", "command = \"other-server\"", "[projects.\"/tmp/example\"]", "trust_level = \"trusted\"", "[mcp_servers.elephant]", "command = \"/opt/elephant/bin/elephant\"", "args = [\"--db\", \"/home/dev/elephant.zova\", \"serve\"]", "[mcp_servers.elephant.env]", "ELEPHANT_ACTOR_ID = \"codex-session\""} {
		if !strings.Contains(content, want) {
			t.Fatalf("missing %q in:\n%s", want, content)
		}
	}
	second, err := agent.Configure(in, env(map[string]string{"CODEX_HOME": root}))
	if err != nil || second.Changed || !second.Configured {
		t.Fatalf("%+v %v", second, err)
	}
	if got := read(t, path); got != content {
		t.Fatalf("idempotent setup rewrote the file:\n%s", got)
	}
	state, err := agent.Inspect("codex", env(map[string]string{"CODEX_HOME": root}))
	if err != nil || !state.Configured || state.ActorID != "codex-session" {
		t.Fatalf("%+v %v", state, err)
	}
	want := []string{"/opt/elephant/bin/elephant", "--db", "/home/dev/elephant.zova", "serve"}
	if strings.Join(state.Command, " ") != strings.Join(want, " ") {
		t.Fatalf("command=%v", state.Command)
	}
}

func TestConfigureCodexCreatesFileAndKeepsGeneratedActor(t *testing.T) {
	root := t.TempDir()
	in := agent.Input{Agent: "codex", Executable: "/usr/local/bin/elephant"}
	first, err := agent.Configure(in, env(map[string]string{"CODEX_HOME": root}))
	if err != nil || !first.Created || !first.Changed {
		t.Fatalf("%+v %v", first, err)
	}
	if !strings.HasPrefix(first.ActorID, "codex-") {
		t.Fatalf("actor=%q", first.ActorID)
	}
	second, err := agent.Configure(in, env(map[string]string{"CODEX_HOME": root}))
	if err != nil || second.Changed || second.ActorID != first.ActorID {
		t.Fatalf("%+v %v", second, err)
	}
	if !strings.Contains(read(t, filepath.Join(root, "config.toml")), "args = [\"serve\"]") {
		t.Fatal("expected a serve-only command when no database is configured")
	}
}

func TestConfigureOpenCodePreservesUnrelatedSettings(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "opencode", "opencode.json")
	write(t, path, `{
  "$schema": "https://opencode.ai/config.json",
  "theme": "dark",
  "limit": 12345678901234567890,
  "mcp": {
    "other": {
      "type": "local",
      "command": ["other-server"]
    }
  }
}
`)
	in := agent.Input{Agent: "opencode", Executable: "/opt/elephant", ActorID: "opencode-session", ConfigPath: path}
	first, err := agent.Configure(in, env(map[string]string{"XDG_CONFIG_HOME": root}))
	if err != nil || !first.Configured || !first.Changed {
		t.Fatalf("%+v %v", first, err)
	}
	content := read(t, path)
	for _, want := range []string{`"theme": "dark"`, "12345678901234567890", `"other"`, "other-server", `"elephant"`, `"type": "local"`, `"enabled": true`, "ELEPHANT_ACTOR_ID", "opencode-session", "/opt/elephant"} {
		if !strings.Contains(content, want) {
			t.Fatalf("missing %q in:\n%s", want, content)
		}
	}
	if _, err = agent.Configure(in, env(map[string]string{"XDG_CONFIG_HOME": root})); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != content {
		t.Fatalf("idempotent setup rewrote the file:\n%s", got)
	}
	state, err := agent.Inspect("opencode", env(map[string]string{"XDG_CONFIG_HOME": root}))
	if err != nil || !state.Configured || state.ActorID != "opencode-session" {
		t.Fatalf("%+v %v", state, err)
	}
}
