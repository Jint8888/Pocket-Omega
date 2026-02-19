package skill

import (
	"context"
	"encoding/json"

	"github.com/pocketomega/pocket-omega/internal/tool"
)

// SkillTool implements tool.Tool for a single workspace skill defined by skill.yaml.
// Execution is delegated to the skill subprocess via the stdio JSON protocol.
type SkillTool struct {
	def    *SkillDef
	schema json.RawMessage
}

// NewSkillTool creates a SkillTool from a parsed SkillDef.
// The JSON schema is built once at construction time for efficiency.
func NewSkillTool(def *SkillDef) *SkillTool {
	return &SkillTool{
		def:    def,
		schema: buildSchema(def.Parameters),
	}
}

func (t *SkillTool) Name() string             { return t.def.Name }
func (t *SkillTool) Description() string      { return t.def.Description }
func (t *SkillTool) InputSchema() json.RawMessage { return t.schema }

// Init is a no-op — workspace skills have no persistent connections.
func (t *SkillTool) Init(_ context.Context) error { return nil }

// Close is a no-op — each execution spawns a fresh process; no cleanup needed.
func (t *SkillTool) Close() error { return nil }

// Execute runs the skill subprocess and returns the result.
// Arguments are decoded from JSON and forwarded via the stdio protocol.
func (t *SkillTool) Execute(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
	var parsedArgs map[string]any
	if err := json.Unmarshal(args, &parsedArgs); err != nil {
		return tool.ToolResult{Error: "参数解析失败: " + err.Error()}, nil
	}
	output, errMsg := Run(ctx, t.def, parsedArgs)
	return tool.ToolResult{Output: output, Error: errMsg}, nil
}

// buildSchema converts []SkillParam into the JSON Schema expected by tool.BuildSchema.
func buildSchema(params []SkillParam) json.RawMessage {
	toolParams := make([]tool.SchemaParam, len(params))
	for i, p := range params {
		toolParams[i] = tool.SchemaParam{
			Name:        p.Name,
			Type:        p.Type,
			Description: p.Description,
			Required:    p.Required,
		}
	}
	return tool.BuildSchema(toolParams...)
}
