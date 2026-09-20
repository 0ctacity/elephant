package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"elephant/internal/agent"
	"elephant/internal/model"
	"elephant/internal/storage/zova"

	native "github.com/ata-sesli/zova/bindings/go"
)

func isolatedHome(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	database := filepath.Join(home, "data", "elephant.zova")
	t.Setenv("ELEPHANT_DB", database)
	return home, database
}

func TestSetupWritesAgentConfigurationInTemporaryHomes(t *testing.T) {
	home, database := isolatedHome(t)
	var out, logs bytes.Buffer
	for _, name := range []string{"codex", "opencode"} {
		out.Reset()
		if err := run(context.Background(), []string{"setup", name}, &out, &logs); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(out.String(), "configured "+name) || !strings.Contains(out.String(), database) {
			t.Fatalf("%s: %s", name, out.String())
		}
	}
	codexConfig := filepath.Join(home, "codex", "config.toml")
	opencodeConfig := filepath.Join(home, "config", "opencode", "opencode.json")
	codexContent, err := os.ReadFile(codexConfig)
	if err != nil {
		t.Fatal(err)
	}
	// TOML basic strings and JSON both escape backslashes, so a Windows
	// database path appears doubled in the configuration file.
	escapedDatabase := strings.ReplaceAll(database, `\`, `\\`)
	for _, want := range []string{"--db", escapedDatabase, "serve", "ELEPHANT_ACTOR_ID"} {
		if !strings.Contains(string(codexContent), want) {
			t.Fatalf("codex configuration missing %q:\n%s", want, codexContent)
		}
	}
	opencodeContent, err := os.ReadFile(opencodeConfig)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"type": "local"`, escapedDatabase, `"ELEPHANT_ACTOR_ID"`} {
		if !strings.Contains(string(opencodeContent), want) {
			t.Fatalf("opencode configuration missing %q:\n%s", want, opencodeContent)
		}
	}
	out.Reset()
	if err = run(context.Background(), []string{"setup", "codex"}, &out, &logs); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "already up to date") {
		t.Fatalf("repeated setup: %s", out.String())
	}
	repeated, err := os.ReadFile(codexConfig)
	if err != nil || !bytes.Equal(repeated, codexContent) {
		t.Fatalf("repeated setup rewrote the configuration: %v", err)
	}
	out.Reset()
	if err = run(context.Background(), []string{"setup", "codex", "--json", "--actor", "cline-agent"}, &out, &logs); err != nil {
		t.Fatal(err)
	}
	var result agent.Result
	if err = json.Unmarshal(out.Bytes(), &result); err != nil || result.Agent != "codex" || result.ActorID != "cline-agent" {
		t.Fatalf("%s %v", out.String(), err)
	}
	out.Reset()
	if err = run(context.Background(), []string{"setup", "zed"}, &out, &logs); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "[mcp_servers.elephant]") || !strings.Contains(out.String(), "zed") {
		t.Fatalf("unsupported agent guidance: %s", out.String())
	}
	for _, args := range [][]string{{"setup"}, {"setup", "codex", "extra"}, {"setup", "codex", "--actor", " "}} {
		if err = run(context.Background(), args, &out, &logs); !errors.Is(err, model.ErrInvalidInput) {
			t.Fatalf("%v: error=%v", args, err)
		}
	}
}

func TestDoctorReportsWarningsAndFailsOnUnusableDatabase(t *testing.T) {
	home, database := isolatedHome(t)
	t.Setenv("ELEPHANT_ACTOR_ID", "")
	ctx := context.Background()
	var out, logs bytes.Buffer
	if err := run(ctx, []string{"doctor", "--json"}, &out, &logs); err != nil {
		t.Fatalf("warnings must not fail doctor: %v", err)
	}
	report := decodeReport(t, out.Bytes())
	if report.Status != statusWarning {
		t.Fatalf("status=%s checks=%+v", report.Status, report.Checks)
	}
	if checkStatus(t, report, "database") != statusWarning || checkStatus(t, report, "git") != statusOK || checkStatus(t, report, "mcp") != statusWarning || checkStatus(t, report, "actor") != statusWarning {
		t.Fatalf("%+v", report.Checks)
	}
	out.Reset()
	if err := run(ctx, []string{"setup", "codex"}, &out, &logs); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ELEPHANT_ACTOR_ID", "cline-agent")
	out.Reset()
	if err := run(ctx, []string{"doctor"}, &out, &logs); err != nil {
		t.Fatalf("human-readable doctor: %v", err)
	}
	if !strings.Contains(out.String(), "OK") || !strings.Contains(out.String(), "codex") {
		t.Fatalf("%s", out.String())
	}
	store, err := zova.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := native.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("UPDATE elephant_meta SET value='999' WHERE key='schema_version'"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	out.Reset()
	if err = run(ctx, []string{"doctor", "--json"}, &out, &logs); !errors.Is(err, model.ErrDiagnostics) {
		t.Fatalf("incompatible schema error=%v", err)
	}
	report = decodeReport(t, out.Bytes())
	if report.Status != statusError || checkStatus(t, report, "database") != statusError {
		t.Fatalf("%+v", report)
	}
	t.Setenv("ELEPHANT_DB", filepath.Join(home, "broken.zova"))
	if err = os.WriteFile(os.Getenv("ELEPHANT_DB"), []byte("not a database"), 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err = run(ctx, []string{"doctor", "--json"}, &out, &logs); !errors.Is(err, model.ErrDiagnostics) {
		t.Fatalf("unreadable database error=%v", err)
	}
	if checkStatus(t, decodeReport(t, out.Bytes()), "database") != statusError {
		t.Fatal("expected a database error check")
	}
}

func decodeReport(t *testing.T, data []byte) diagnosticReport {
	t.Helper()
	var report diagnosticReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("%s: %v", data, err)
	}
	return report
}

func checkStatus(t *testing.T, report diagnosticReport, name string) string {
	t.Helper()
	for _, check := range report.Checks {
		if check.Name == name {
			return check.Status
		}
	}
	t.Fatalf("missing %q check in %+v", name, report.Checks)
	return ""
}
