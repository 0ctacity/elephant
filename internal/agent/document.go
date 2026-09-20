package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"elephant/internal/model"
)

// readState reads the Elephant entry from configuration content. A blank or
// absent file is a valid empty state; malformed content is an error so callers
// never overwrite configuration they cannot parse.
func readState(target Target, path, content string, existed bool) (State, error) {
	state := State{Agent: target.Name, ConfigPath: path, Exists: existed}
	if strings.TrimSpace(content) == "" {
		return state, nil
	}
	if target.Format == JSON {
		doc, err := decodeJSON(content)
		if err != nil {
			return state, fmt.Errorf("%w: %s is not valid JSON: %v", model.ErrInvalidInput, path, err)
		}
		mcp, ok := doc["mcp"].(map[string]any)
		if !ok {
			return state, nil
		}
		entry, ok := mcp[ServerName].(map[string]any)
		if !ok {
			return state, nil
		}
		state.Configured = true
		state.Detail = "mcp." + ServerName
		if list, ok := entry["command"].([]any); ok {
			for _, value := range list {
				if text, ok := value.(string); ok {
					state.Command = append(state.Command, text)
				}
			}
		}
		if environment, ok := entry["environment"].(map[string]any); ok {
			if actor, ok := environment["ELEPHANT_ACTOR_ID"].(string); ok {
				state.ActorID = actor
			}
		}
		return state, nil
	}
	if len(tomlBlock(content, "mcp_servers."+ServerName)) == 0 {
		return state, nil
	}
	state.Configured = true
	state.Detail = "mcp_servers." + ServerName
	keys := tomlValues(tomlBlock(content, "mcp_servers."+ServerName))
	if command := keys["command"]; command != "" {
		state.Command = append(state.Command, command)
	}
	state.Command = append(state.Command, tomlArray(keys["args"])...)
	state.ActorID = tomlValues(tomlBlock(content, "mcp_servers."+ServerName+".env"))["ELEPHANT_ACTOR_ID"]
	return state, nil
}

func decodeJSON(content string) (map[string]any, error) {
	doc := map[string]any{}
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&doc); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("expected one JSON document")
	}
	return doc, nil
}

func edit(target Target, content string, command []string, actor string) (string, error) {
	if target.Format == JSON {
		return editJSON(content, command, actor)
	}
	return setTOMLBlock(content, "mcp_servers."+ServerName, tomlServerBlock(command, actor)), nil
}

func editJSON(content string, command []string, actor string) (string, error) {
	doc := map[string]any{}
	if strings.TrimSpace(content) != "" {
		parsed, err := decodeJSON(content)
		if err != nil {
			return "", fmt.Errorf("%w: existing configuration is not valid JSON (%v); configure it manually", model.ErrInvalidInput, err)
		}
		doc = parsed
	}
	mcp := map[string]any{}
	if existing, ok := doc["mcp"]; ok {
		value, ok := existing.(map[string]any)
		if !ok {
			return "", fmt.Errorf("%w: the existing \"mcp\" setting is not an object; configure it manually", model.ErrInvalidInput)
		}
		mcp = value
	}
	encoded := make([]any, 0, len(command))
	for _, value := range command {
		encoded = append(encoded, value)
	}
	mcp[ServerName] = map[string]any{
		"type":        "local",
		"command":     encoded,
		"enabled":     true,
		"environment": map[string]string{"ELEPHANT_ACTOR_ID": actor},
	}
	doc["mcp"] = mcp
	updated, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", fmt.Errorf("%w: encode configuration: %v", model.ErrInvalidInput, err)
	}
	return string(updated) + "\n", nil
}

// Manual returns a copy-ready configuration example for an agent Elephant does
// not configure automatically.
func Manual(name, executable, database, actor string) string {
	command := launchCommand(executable, database)
	quoted := make([]string, 0, len(command))
	for _, value := range command {
		quoted = append(quoted, tomlString(value))
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Elephant does not configure %s automatically. Start an MCP server named %q with:\n\n  %s\n\n", name, ServerName, strings.Join(command, " "))
	out.WriteString("Add that command to the agent's MCP configuration, for example:\n\n")
	fmt.Fprintf(&out, "[mcp_servers.%s]\ncommand = %s\nargs = [%s]\n\n", ServerName, tomlString(executable), strings.Join(quoted[1:], ", "))
	fmt.Fprintf(&out, "[mcp_servers.%s.env]\nELEPHANT_ACTOR_ID = %s\n", ServerName, tomlString(actor))
	return out.String()
}
