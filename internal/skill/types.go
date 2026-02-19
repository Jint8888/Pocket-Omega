package skill

// SkillParam describes a single parameter in skill.yaml.
type SkillParam struct {
	Name        string `yaml:"name"`
	Type        string `yaml:"type"` // "string" | "integer" | "number" | "boolean"
	Required    bool   `yaml:"required"`
	Default     any    `yaml:"default"`
	Description string `yaml:"description"`
}

// SkillExample is a usage example embedded in the skill.yaml docs section.
type SkillExample struct {
	Scenario  string         `yaml:"scenario"`
	Arguments map[string]any `yaml:"arguments"`
}

// SkillDocs is the human/LLM-readable documentation section of skill.yaml.
// Optional, but strongly recommended for agent discoverability.
type SkillDocs struct {
	WhenToUse    []string       `yaml:"when_to_use"`
	WhenNotToUse []string       `yaml:"when_not_to_use"`
	Examples     []SkillExample `yaml:"examples"`
}

// SkillDef is the parsed content of a skill.yaml file.
// One SkillDef corresponds to exactly one tool in the tool registry.
type SkillDef struct {
	Name        string       `yaml:"name"`
	Description string       `yaml:"description"`
	Runtime     string       `yaml:"runtime"` // "python" | "node" | "go" | "binary"
	Entry       string       `yaml:"entry"`
	Parameters  []SkillParam `yaml:"parameters"`
	Docs        SkillDocs    `yaml:"docs"`

	// Dir is set by the loader to the absolute path of the skill directory.
	// Not present in skill.yaml — populated after parsing.
	Dir string `yaml:"-"`
}
