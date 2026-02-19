package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	skillsSubdir = "skills"
	skillYAML    = "skill.yaml"
)

// ScanDir scans <workspaceDir>/skills/ and returns all valid SkillDefs.
// Subdirectories without a skill.yaml are silently skipped.
// If the skills/ directory does not exist, an empty slice is returned (not an error).
func ScanDir(workspaceDir string) ([]*SkillDef, []error) {
	skillsDir := filepath.Join(workspaceDir, skillsSubdir)

	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // skills/ not created yet — not an error
		}
		return nil, []error{fmt.Errorf("skill: scan %q: %w", skillsDir, err)}
	}

	var defs []*SkillDef
	var errs []error

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		skillDir := filepath.Join(skillsDir, e.Name())
		yamlPath := filepath.Join(skillDir, skillYAML)

		data, err := os.ReadFile(yamlPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue // no skill.yaml → silently skip
			}
			errs = append(errs, fmt.Errorf("skill: read %q: %w", yamlPath, err))
			continue
		}

		var def SkillDef
		if err := yaml.Unmarshal(data, &def); err != nil {
			errs = append(errs, fmt.Errorf("skill: parse %q: %w", yamlPath, err))
			continue
		}

		if err := validateDef(&def, e.Name()); err != nil {
			errs = append(errs, err)
			continue
		}

		def.Dir = skillDir
		defs = append(defs, &def)
	}

	return defs, errs
}

// validateDef checks that required fields are present and that the tool name
// starts with the directory name as a domain prefix (e.g. dir "excel" → "excel_*").
func validateDef(def *SkillDef, dirName string) error {
	if def.Name == "" {
		return fmt.Errorf("skill %q: name is required", dirName)
	}
	if def.Description == "" {
		return fmt.Errorf("skill %q: description is required", dirName)
	}
	if def.Runtime == "" {
		return fmt.Errorf("skill %q: runtime is required", dirName)
	}
	if def.Entry == "" {
		return fmt.Errorf("skill %q: entry is required", dirName)
	}

	validRuntimes := map[string]bool{
		"python": true, "node": true, "go": true, "binary": true,
	}
	if !validRuntimes[def.Runtime] {
		return fmt.Errorf("skill %q: unknown runtime %q — supported: python | node | go | binary", dirName, def.Runtime)
	}

	// Tool name must start with the directory name as domain prefix.
	// Allows exact match (dir == name) or prefixed (dir + "_" + action).
	if def.Name != dirName && !strings.HasPrefix(def.Name, dirName+"_") {
		return fmt.Errorf("skill %q: tool name %q must start with %q prefix", dirName, def.Name, dirName+"_")
	}

	return nil
}
