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
	for _, want := range []string{`"theme": "dark"`, "12345678901234567890", `"other"`, "other-server", `"servers"`, `"elephant"`, `"type": "local"`, `"environment"`, "ELEPHANT_ACTOR_ID", "opencode-session", "/opt/elephant"} {
		if !strings.Contains(content, want) {
			t.Fatalf("missing %q in:\n%s", want, content)
		}
	}
	for _, absent := range []string{`"enabled"`, `"mcp": {\n    "elephant"`} {
		if strings.Contains(content, absent) {
			t.Fatalf("legacy field %q still present in:\n%s", absent, content)
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
	wantCommand := []string{"/opt/elephant", "serve"}
	if strings.Join(state.Command, "\x00") != strings.Join(wantCommand, "\x00") {
		t.Fatalf("command=%v", state.Command)
	}
}

func TestConfigureOpenCodeMigratesLegacyEntry(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "opencode", "opencode.json")
	write(t, path, `{
  // keep this comment
  "theme": "dark",
  "mcp": {
    "other": {"type": "local", "command": ["other-server"]},
    "elephant": {
      "type": "local",
      "command": ["/old/elephant", "serve"],
      "enabled": false,
      "environment": {"ELEPHANT_ACTOR_ID": "legacy-actor"}
    }
  }
}
`)
	in := agent.Input{Agent: "opencode", Executable: "/opt/elephant", ConfigPath: path}
	first, err := agent.Configure(in, env(map[string]string{"XDG_CONFIG_HOME": root}))
	if err != nil || !first.Configured || !first.Changed {
		t.Fatalf("%+v %v", first, err)
	}
	if first.ActorID != "legacy-actor" {
		t.Fatalf("legacy actor was not reused: %+v", first)
	}
	content := read(t, path)
	for _, want := range []string{"// keep this comment", `"theme": "dark"`, `"other"`, `"servers"`, `"disabled":true`} {
		if !strings.Contains(content, want) {
			t.Fatalf("missing %q in:\n%s", want, content)
		}
	}
	if strings.Contains(content, "/old/elephant") {
		t.Fatalf("legacy entry was not migrated away:\n%s", content)
	}
	state, err := agent.Inspect("opencode", env(map[string]string{"XDG_CONFIG_HOME": root}))
	if err != nil || !state.Configured || state.ActorID != "legacy-actor" {
		t.Fatalf("%+v %v", state, err)
	}
	if got := []string{state.Command[0], state.Command[len(state.Command)-1]}; got[0] != "/opt/elephant" || got[1] != "serve" {
		t.Fatalf("command=%v", state.Command)
	}
	second, err := agent.Configure(in, env(map[string]string{"XDG_CONFIG_HOME": root}))
	if err != nil || second.Changed {
		t.Fatalf("migration was not idempotent: %+v %v", second, err)
	}
	if got := read(t, path); got != content {
		t.Fatalf("second run rewrote the file:\n%s", got)
	}
}

func TestConfigureOpenCodePreservesJSONC(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "opencode.json")
	write(t, path, "{\n  /* block comment */\n  \"mcp\": {\n    // servers live here\n    \"servers\": {}, // trailing comma\n  },\n  \"theme\": \"dark\", // keep me\n}\n")
	in := agent.Input{Agent: "opencode", Executable: "/opt/elephant", ActorID: "jsonc-actor", ConfigPath: path}
	if _, err := agent.Configure(in, env(nil)); err != nil {
		t.Fatal(err)
	}
	content := read(t, path)
	for _, want := range []string{"/* block comment */", "// servers live here", "// trailing comma", "// keep me", `"theme": "dark"`, `"elephant"`, "jsonc-actor"} {
		if !strings.Contains(content, want) {
			t.Fatalf("missing %q in:\n%s", want, content)
		}
	}
	second, err := agent.Configure(in, env(nil))
	if err != nil || second.Changed {
		t.Fatalf("JSONC setup was not idempotent: %+v %v", second, err)
	}
	if got := read(t, path); got != content {
		t.Fatalf("second run rewrote the file:\n%s", got)
	}
}

func TestConfigureOpenCodeRefusesNonObjects(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{
		"servers": `{"mcp": {"servers": []}}`,
		"entry":   `{"mcp": {"servers": {"elephant": "text"}}}`,
	} {
		path := filepath.Join(root, name, "opencode.json")
		write(t, path, body)
		before := read(t, path)
		if _, err := agent.Configure(agent.Input{Agent: "opencode", Executable: "/opt/elephant", ConfigPath: path}, env(nil)); err == nil {
			t.Fatalf("%s: non-object was accepted", name)
		}
		if got := read(t, path); got != before {
			t.Fatalf("%s: refused configuration was rewritten", name)
		}
	}
}

func TestConfigureWriteFailurePreservesConfiguration(t *testing.T) {
	root := t.TempDir()
	// A regular file where the configuration directory must go makes every
	// write fail on every platform.
	blocker := filepath.Join(root, "blocked")
	write(t, blocker, "occupied")
	path := filepath.Join(blocker, "config.toml")
	if _, err := agent.Configure(agent.Input{Agent: "codex", Executable: "/opt/elephant", ActorID: "actor", ConfigPath: path}, env(nil)); err == nil {
		t.Fatal("write into a blocked directory was accepted")
	}
	leftovers, err := filepath.Glob(filepath.Join(root, ".elephant-config-*.tmp"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("temporary files leaked: %v %v", leftovers, err)
	}
	// A failed write must not disturb an existing configuration elsewhere.
	good := filepath.Join(root, "good", "config.toml")
	write(t, good, "[mcp_servers.other]\ncommand = \"other\"\n")
	before := read(t, good)
	if _, err := agent.Configure(agent.Input{Agent: "codex", Executable: "/opt/elephant", ActorID: "actor", ConfigPath: good}, env(nil)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, good), before[:len("[mcp_servers.other]")]) {
		t.Fatal("existing configuration was disturbed")
	}
}
