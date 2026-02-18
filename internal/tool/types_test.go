package tool

import (
	"encoding/json"
	"testing"
)

func TestBuildSchema(t *testing.T) {
	schema := BuildSchema(
		SchemaParam{Name: "command", Type: "string", Description: "Shell command", Required: true},
		SchemaParam{Name: "timeout", Type: "integer", Description: "Timeout in seconds", Required: false},
	)

	// Should be valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal(schema, &parsed); err != nil {
		t.Fatalf("BuildSchema output is not valid JSON: %v", err)
	}

	// Should have type: object
	if parsed["type"] != "object" {
		t.Errorf("type = %v, want 'object'", parsed["type"])
	}

	// Should have properties
	props, ok := parsed["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("missing 'properties' field")
	}

	// Check command property
	cmd, ok := props["command"].(map[string]interface{})
	if !ok {
		t.Fatal("missing 'command' property")
	}
	if cmd["type"] != "string" {
		t.Errorf("command.type = %v, want 'string'", cmd["type"])
	}
	if cmd["description"] != "Shell command" {
		t.Errorf("command.description = %v, want 'Shell command'", cmd["description"])
	}

	// Check timeout property
	timeout, ok := props["timeout"].(map[string]interface{})
	if !ok {
		t.Fatal("missing 'timeout' property")
	}
	if timeout["type"] != "integer" {
		t.Errorf("timeout.type = %v, want 'integer'", timeout["type"])
	}

	// Check required array
	required, ok := parsed["required"].([]interface{})
	if !ok {
		t.Fatal("missing 'required' field")
	}
	if len(required) != 1 || required[0] != "command" {
		t.Errorf("required = %v, want [command]", required)
	}
}

func TestBuildSchemaEmpty(t *testing.T) {
	schema := BuildSchema()

	var parsed map[string]interface{}
	if err := json.Unmarshal(schema, &parsed); err != nil {
		t.Fatalf("empty schema is not valid JSON: %v", err)
	}

	if parsed["type"] != "object" {
		t.Errorf("type = %v, want 'object'", parsed["type"])
	}
}

func TestRegistryBasicOps(t *testing.T) {
	reg := NewRegistry()

	// List should be empty
	if len(reg.List()) != 0 {
		t.Error("new registry should be empty")
	}

	// Get non-existent
	_, ok := reg.Get("nope")
	if ok {
		t.Error("Get on empty registry should return false")
	}
}

func TestGenerateToolsPromptEmpty(t *testing.T) {
	reg := NewRegistry()
	prompt := reg.GenerateToolsPrompt()
	if prompt != "（无可用工具）" {
		t.Errorf("empty registry prompt = %q, want '（无可用工具）'", prompt)
	}
}
