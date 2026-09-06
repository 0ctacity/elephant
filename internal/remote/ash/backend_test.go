package ash

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

type executorFunc func(context.Context, string, []string, io.Writer, io.Writer) error

func (f executorFunc) Run(ctx context.Context, name string, args []string, stdout, stderr io.Writer) error {
	return f(ctx, name, args, stdout, stderr)
}

func TestExchangeInvocationAndRetryPreservePayload(t *testing.T) {
	payload := []byte(`{"message_id":"same-id","title":"O'Reilly $(touch /tmp/nope); ` + "`id`" + `"}`)
	var commands []string
	b := Backend{Executor: executorFunc(func(ctx context.Context, name string, args []string, out, errout io.Writer) error {
		if name != "ash" || !reflect.DeepEqual(args[:5], []string{"--config", "/a config.toml", "exec", "fedora", "--"}) {
			t.Fatalf("unexpected invocation: %s %q", name, args)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("missing bounded deadline")
		}
		commands = append(commands, args[5])
		_, err := io.WriteString(out, `{"ok":true}`)
		return err
	})}
	for range 2 {
		got, err := b.Exchange(context.Background(), Config{Host: "fedora", ConfigPath: "/a config.toml"}, payload)
		if err != nil || string(got) != `{"ok":true}` {
			t.Fatalf("%s: %v", got, err)
		}
	}
	if commands[0] != commands[1] {
		t.Fatal("retry changed request")
	}
	if !strings.HasSuffix(commands[0], " | 'elephant' receive") {
		t.Fatalf("wrong receiver: %s", commands[0])
	}
	if runtime.GOOS == "windows" {
		return
	}
	// Exercise the actual POSIX quoting, replacing only the receiving executable.
	command := strings.TrimSuffix(commands[0], " | 'elephant' receive")
	got, err := exec.Command("sh", "-c", command).Output()
	if err != nil || string(got) != string(payload) {
		t.Fatalf("payload changed: %q %v", got, err)
	}
}

func TestExchangeRejectsInvalidInputAndOutput(t *testing.T) {
	for _, tc := range []struct {
		name              string
		config            Config
		payload, response string
		runErr            error
	}{
		{"missing host", Config{}, `{}`, `{}`, nil},
		{"NUL config", Config{Host: "h", ElephantPath: "x\x00y"}, `{}`, `{}`, nil},
		{"invalid payload", Config{Host: "h"}, `not json`, `{}`, nil},
		{"large payload", Config{Host: "h"}, `"` + strings.Repeat("x", MaxCommandSize) + `"`, `{}`, nil},
		{"invalid response", Config{Host: "h"}, `{}`, `not json`, nil},
		{"multiple responses", Config{Host: "h"}, `{}`, `{} {}`, nil},
		{"large response", Config{Host: "h"}, `{}`, `"` + strings.Repeat("x", MaxResponseSize+1) + `"`, nil},
		{"transport failure", Config{Host: "h"}, `{}`, `{}`, errors.New("SSH unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := Backend{Executor: executorFunc(func(_ context.Context, _ string, _ []string, out, errout io.Writer) error {
				_, err := io.WriteString(out, tc.response)
				if err != nil {
					return err
				}
				return tc.runErr
			})}
			if _, err := b.Exchange(context.Background(), tc.config, []byte(tc.payload)); err == nil {
				t.Fatal("accepted invalid exchange")
			}
		})
	}
}

func TestCanceledExchangeDoesNotExecute(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := Backend{Executor: executorFunc(func(context.Context, string, []string, io.Writer, io.Writer) error {
		t.Fatal("executed canceled request")
		return nil
	})}
	if _, err := b.Exchange(ctx, Config{Host: "h"}, []byte(`{}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
}
