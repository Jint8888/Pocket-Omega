package skill

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pocketomega/pocket-omega/internal/tool"
)

// ── Helpers ──────────────────────────────────────────────────────────────────

// writeSkillYAML writes a skill.yaml into skillDir (which must already exist).
func writeSkillYAML(t *testing.T, skillDir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(skillDir, "skill.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("writeSkillYAML: %v", err)
	}
}

// makeSkillDir creates <workspace>/skills/<name>/ and returns the skill dir path.
func makeSkillDir(t *testing.T, workspace, name string) string {
	t.Helper()
	d := filepath.Join(workspace, "skills", name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatalf("makeSkillDir: %v", err)
	}
	return d
}

// ── validateDef ──────────────────────────────────────────────────────────────

func TestValidateDef_MissingName(t *testing.T) {
	def := &SkillDef{Description: "d", Runtime: "python", Entry: "main.py"}
	if err := validateDef(def, "mypkg"); err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Errorf("expected name-required error, got: %v", err)
	}
}

func TestValidateDef_MissingDescription(t *testing.T) {
	def := &SkillDef{Name: "mypkg_do", Runtime: "python", Entry: "main.py"}
	if err := validateDef(def, "mypkg"); err == nil || !strings.Contains(err.Error(), "description is required") {
		t.Errorf("expected description-required error, got: %v", err)
	}
}

func TestValidateDef_MissingRuntime(t *testing.T) {
	def := &SkillDef{Name: "mypkg_do", Description: "d", Entry: "main.py"}
	if err := validateDef(def, "mypkg"); err == nil || !strings.Contains(err.Error(), "runtime is required") {
		t.Errorf("expected runtime-required error, got: %v", err)
	}
}

func TestValidateDef_MissingEntry(t *testing.T) {
	def := &SkillDef{Name: "mypkg_do", Description: "d", Runtime: "python"}
	if err := validateDef(def, "mypkg"); err == nil || !strings.Contains(err.Error(), "entry is required") {
		t.Errorf("expected entry-required error, got: %v", err)
	}
}

func TestValidateDef_UnknownRuntime(t *testing.T) {
	def := &SkillDef{Name: "mypkg_do", Description: "d", Runtime: "ruby", Entry: "main.rb"}
	if err := validateDef(def, "mypkg"); err == nil || !strings.Contains(err.Error(), "unknown runtime") {
		t.Errorf("expected unknown-runtime error, got: %v", err)
	}
}

func TestValidateDef_BadNamePrefix(t *testing.T) {
	def := &SkillDef{Name: "other_do", Description: "d", Runtime: "python", Entry: "main.py"}
	if err := validateDef(def, "mypkg"); err == nil || !strings.Contains(err.Error(), "prefix") {
		t.Errorf("expected prefix error, got: %v", err)
	}
}

func TestValidateDef_ExactNameMatch(t *testing.T) {
	// Name == dirName (no underscore suffix) is allowed.
	def := &SkillDef{Name: "mypkg", Description: "d", Runtime: "python", Entry: "main.py"}
	if err := validateDef(def, "mypkg"); err != nil {
		t.Errorf("unexpected error for exact name match: %v", err)
	}
}

func TestValidateDef_Valid(t *testing.T) {
	def := &SkillDef{Name: "mypkg_read", Description: "d", Runtime: "go", Entry: "main.go"}
	if err := validateDef(def, "mypkg"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// ── ScanDir ──────────────────────────────────────────────────────────────────

func TestScanDir_NoSkillsDir(t *testing.T) {
	ws := t.TempDir()
	defs, errs := ScanDir(ws)
	if len(defs) != 0 || len(errs) != 0 {
		t.Errorf("expected empty result for missing skills dir, got defs=%d errs=%d", len(defs), len(errs))
	}
}

func TestScanDir_EmptySkillsDir(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	defs, errs := ScanDir(ws)
	if len(defs) != 0 || len(errs) != 0 {
		t.Errorf("expected empty result for empty skills dir, got defs=%d errs=%d", len(defs), len(errs))
	}
}

func TestScanDir_SkipsNonDirs(t *testing.T) {
	ws := t.TempDir()
	skillsDir := filepath.Join(ws, "skills")
	os.MkdirAll(skillsDir, 0o755)
	// Create a plain file in skills/ (should be ignored)
	os.WriteFile(filepath.Join(skillsDir, "readme.txt"), []byte("hi"), 0o644)
	defs, errs := ScanDir(ws)
	if len(defs) != 0 || len(errs) != 0 {
		t.Errorf("expected empty result, got defs=%d errs=%d", len(defs), len(errs))
	}
}

func TestScanDir_SkipsNoYAML(t *testing.T) {
	ws := t.TempDir()
	makeSkillDir(t, ws, "myskill") // no skill.yaml
	defs, errs := ScanDir(ws)
	if len(defs) != 0 || len(errs) != 0 {
		t.Errorf("expected empty result when skill.yaml missing, got defs=%d errs=%d", len(defs), len(errs))
	}
}

func TestScanDir_InvalidYAML(t *testing.T) {
	ws := t.TempDir()
	d := makeSkillDir(t, ws, "bad")
	writeSkillYAML(t, d, ":::not valid yaml:::")
	_, errs := ScanDir(ws)
	if len(errs) == 0 {
		t.Error("expected parse error for invalid YAML")
	}
}

func TestScanDir_ValidationError(t *testing.T) {
	ws := t.TempDir()
	d := makeSkillDir(t, ws, "mypkg")
	writeSkillYAML(t, d, `
name: wrong_name
description: "test"
runtime: python
entry: main.py
`)
	_, errs := ScanDir(ws)
	if len(errs) == 0 {
		t.Error("expected validation error for wrong name prefix")
	}
}

func TestScanDir_ValidSkill(t *testing.T) {
	ws := t.TempDir()
	d := makeSkillDir(t, ws, "greet")
	writeSkillYAML(t, d, `
name: greet_hello
description: "向用户打招呼，返回问候语。测试 Skill。"
runtime: python
entry: main.py
parameters:
  - name: name
    type: string
    required: true
    description: "用户名称。示例：Alice"
`)
	defs, errs := ScanDir(ws)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(defs) != 1 {
		t.Fatalf("expected 1 def, got %d", len(defs))
	}
	def := defs[0]
	if def.Name != "greet_hello" {
		t.Errorf("unexpected Name: %q", def.Name)
	}
	if def.Dir != d {
		t.Errorf("unexpected Dir: %q, want %q", def.Dir, d)
	}
	if len(def.Parameters) != 1 || def.Parameters[0].Name != "name" {
		t.Errorf("unexpected Parameters: %+v", def.Parameters)
	}
}

func TestScanDir_MultipleSkills(t *testing.T) {
	ws := t.TempDir()
	for _, name := range []string{"alpha", "beta", "gamma"} {
		d := makeSkillDir(t, ws, name)
		writeSkillYAML(t, d, `name: `+name+`_run
description: "测试 skill"
runtime: python
entry: main.py
`)
	}
	// Add one directory without skill.yaml (should be skipped)
	makeSkillDir(t, ws, "delta")

	defs, errs := ScanDir(ws)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(defs) != 3 {
		t.Errorf("expected 3 defs, got %d", len(defs))
	}
}

// ── BinaryName ───────────────────────────────────────────────────────────────

func TestBinaryName(t *testing.T) {
	name := BinaryName()
	if runtime.GOOS == "windows" {
		if name != "skill.exe" {
			t.Errorf("expected skill.exe on Windows, got %q", name)
		}
	} else {
		if name != "skill" {
			t.Errorf("expected skill on non-Windows, got %q", name)
		}
	}
}

// ── SkillTool ─────────────────────────────────────────────────────────────────

func TestSkillTool_NameDescriptionSchema(t *testing.T) {
	def := &SkillDef{
		Name:        "calc_add",
		Description: "计算两数之和。",
		Runtime:     "python",
		Entry:       "main.py",
		Parameters: []SkillParam{
			{Name: "a", Type: "number", Required: true, Description: "第一个数。示例：1"},
			{Name: "b", Type: "number", Required: false, Default: 0, Description: "第二个数，默认 0。示例：2"},
		},
	}
	st := NewSkillTool(def)
	if st.Name() != "calc_add" {
		t.Errorf("unexpected Name: %q", st.Name())
	}
	if st.Description() != "计算两数之和。" {
		t.Errorf("unexpected Description: %q", st.Description())
	}
	// Schema should be valid JSON with both parameters.
	var schema map[string]any
	if err := json.Unmarshal(st.InputSchema(), &schema); err != nil {
		t.Fatalf("InputSchema is not valid JSON: %v", err)
	}
	props, _ := schema["properties"].(map[string]any)
	if _, ok := props["a"]; !ok {
		t.Error("schema missing parameter 'a'")
	}
	if _, ok := props["b"]; !ok {
		t.Error("schema missing parameter 'b'")
	}
	required, _ := schema["required"].([]any)
	if len(required) != 1 || required[0] != "a" {
		t.Errorf("unexpected required list: %v", required)
	}
}

func TestSkillTool_InitClose(t *testing.T) {
	st := NewSkillTool(&SkillDef{Name: "x", Description: "y", Runtime: "python", Entry: "main.py"})
	if err := st.Init(context.Background()); err != nil {
		t.Errorf("Init should be no-op, got: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Errorf("Close should be no-op, got: %v", err)
	}
}

func TestSkillTool_Execute_BadJSON(t *testing.T) {
	st := NewSkillTool(&SkillDef{Name: "x", Description: "y", Runtime: "python", Entry: "main.py"})
	result, err := st.Execute(context.Background(), []byte(`not json`))
	if err != nil {
		t.Fatalf("unexpected go error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "参数解析失败") {
		t.Errorf("expected parse-failed error, got: %+v", result)
	}
}

// ── Run / runner (binary runtime) ────────────────────────────────────────────

// writeBinarySkill writes a tiny Go program as a precompiled binary skill
// and returns the SkillDef. Used to test the binary runtime without needing
// python/node to be installed.
//
// The script echoes back the "msg" argument wrapped in the protocol envelope.
func writeBinaryGoSkill(t *testing.T, dir string) *SkillDef {
	t.Helper()
	// Build a tiny Go source in a sub-temp dir, then copy binary to dir.
	src := `package main
import ("bufio";"encoding/json";"os")
type Req struct { Arguments map[string]any ` + "`json:\"arguments\"`" + `}
type Resp struct { Output string ` + "`json:\"output\"`" + `; Error string ` + "`json:\"error\"`" + `}
func main() {
	sc := bufio.NewScanner(os.Stdin)
	sc.Scan()
	var req Req
	json.Unmarshal(sc.Bytes(), &req)
	msg, _ := req.Arguments["msg"].(string)
	json.NewEncoder(os.Stdout).Encode(Resp{Output: "hello " + msg})
}`
	buildDir := filepath.Join(t.TempDir(), "build")
	os.MkdirAll(buildDir, 0o755)
	if err := os.WriteFile(filepath.Join(buildDir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("writeBinaryGoSkill: write main.go: %v", err)
	}
	// Create a minimal go.mod so go build works in isolation.
	goMod := "module testskill\ngo 1.21\n"
	if err := os.WriteFile(filepath.Join(buildDir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("writeBinaryGoSkill: write go.mod: %v", err)
	}
	binName := BinaryName()
	binPath := filepath.Join(dir, binName)
	if err := CompileGoSkill(buildDir); err != nil {
		t.Skipf("skipping binary skill test: go build unavailable: %v", err)
	}
	// Move compiled binary to the skill dir.
	compiledBin := filepath.Join(buildDir, binName)
	data, err := os.ReadFile(compiledBin)
	if err != nil {
		t.Fatalf("writeBinaryGoSkill: read compiled binary: %v", err)
	}
	if err := os.WriteFile(binPath, data, 0o755); err != nil {
		t.Fatalf("writeBinaryGoSkill: write binary: %v", err)
	}
	return &SkillDef{
		Name: "echo_msg", Description: "echo", Runtime: "binary", Entry: binName, Dir: dir,
	}
}

func TestRun_Binary_Success(t *testing.T) {
	dir := t.TempDir()
	def := writeBinaryGoSkill(t, dir)

	out, errMsg := Run(context.Background(), def, map[string]any{"msg": "world"})
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if !strings.Contains(out, "hello world") {
		t.Errorf("unexpected output: %q", out)
	}
}

func TestRun_Binary_Error(t *testing.T) {
	dir := t.TempDir()
	def := writeBinaryGoSkill(t, dir)

	// Pass no msg → output is "hello "
	out, errMsg := Run(context.Background(), def, map[string]any{})
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if !strings.Contains(out, "hello ") {
		t.Errorf("unexpected output: %q", out)
	}
}

func TestRun_MissingBinary(t *testing.T) {
	def := &SkillDef{
		Name: "ghost_run", Description: "d", Runtime: "binary",
		Entry: "nonexistent_binary", Dir: t.TempDir(),
	}
	_, errMsg := Run(context.Background(), def, nil)
	if errMsg == "" {
		t.Error("expected error for missing binary")
	}
}

func TestRun_UnknownRuntime(t *testing.T) {
	def := &SkillDef{
		Name: "x_y", Description: "d", Runtime: "fortran", Entry: "main.f90", Dir: t.TempDir(),
	}
	_, errMsg := Run(context.Background(), def, nil)
	if errMsg == "" || !strings.Contains(errMsg, "未知 runtime") {
		t.Errorf("expected unknown-runtime error, got: %s", errMsg)
	}
}

// TestRun_GoRuntime_BinaryExists verifies the "go" runtime path in Run() and buildCmd().
// It pre-places the compiled binary in the skill dir so ensureCompiled takes the fast path.
func TestRun_GoRuntime_BinaryExists(t *testing.T) {
	dir := t.TempDir()
	def := writeBinaryGoSkill(t, dir)
	// Switch to "go" runtime — binary is already present, so ensureCompiled is a no-op.
	def.Runtime = "go"
	def.Entry = "" // Go runtime ignores Entry; uses BinaryName() directly

	out, errMsg := Run(context.Background(), def, map[string]any{"msg": "gopher"})
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if !strings.Contains(out, "hello gopher") {
		t.Errorf("unexpected output: %q", out)
	}
}

// TestSkillTool_Execute_BinarySuccess tests the full Execute→Run pipeline.
func TestSkillTool_Execute_BinarySuccess(t *testing.T) {
	dir := t.TempDir()
	def := writeBinaryGoSkill(t, dir)
	st := NewSkillTool(def)

	args, _ := json.Marshal(map[string]string{"msg": "skill"})
	result, err := st.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected go error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "hello skill") {
		t.Errorf("unexpected output: %q", result.Output)
	}
}

// ── Manager ──────────────────────────────────────────────────────────────────

func TestManager_LoadAll_EmptyWorkspace(t *testing.T) {
	ws := t.TempDir()
	reg := tool.NewRegistry()
	mgr := NewManager(ws)
	n, errs := mgr.LoadAll(context.Background(), reg)
	if n != 0 || len(errs) != 0 {
		t.Errorf("expected 0 loaded and 0 errors, got n=%d errs=%v", n, errs)
	}
}

func TestManager_LoadAll_ValidSkill(t *testing.T) {
	ws := t.TempDir()
	dir := makeSkillDir(t, ws, "greet")
	writeSkillYAML(t, dir, `
name: greet_hi
description: "打招呼。测试用。"
runtime: binary
entry: skill.exe
`)
	// Write a binary so the loader doesn't fail at execution (not called in LoadAll)
	// We just need the binary to be present so ensureCompiled passes for non-go runtime.
	// For binary runtime, no compilation is needed.

	reg := tool.NewRegistry()
	mgr := NewManager(ws)
	n, errs := mgr.LoadAll(context.Background(), reg)
	// binary runtime: no compilation; should load fine
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if n != 1 {
		t.Fatalf("expected 1 loaded, got %d", n)
	}
	if _, ok := reg.Get("greet_hi"); !ok {
		t.Error("greet_hi should be registered")
	}
}

func TestManager_Reload_AddsRemovesSkills(t *testing.T) {
	ws := t.TempDir()
	reg := tool.NewRegistry()
	mgr := NewManager(ws)

	// Initial load: two skills.
	for _, name := range []string{"alpha", "beta"} {
		d := makeSkillDir(t, ws, name)
		writeSkillYAML(t, d, `name: `+name+`_run
description: "测试 skill"
runtime: binary
entry: dummy
`)
	}
	mgr.LoadAll(context.Background(), reg)

	// Reload: remove "alpha", add "gamma".
	alphaSkillDir := filepath.Join(ws, "skills", "alpha")
	os.RemoveAll(alphaSkillDir)

	d := makeSkillDir(t, ws, "gamma")
	writeSkillYAML(t, d, `name: gamma_run
description: "测试 skill"
runtime: binary
entry: dummy
`)

	summary := mgr.Reload(context.Background(), reg)
	if !strings.Contains(summary, "+1") {
		t.Errorf("expected +1 added in summary, got: %q", summary)
	}
	if !strings.Contains(summary, "-1") {
		t.Errorf("expected -1 removed in summary, got: %q", summary)
	}
	if _, ok := reg.Get("alpha_run"); ok {
		t.Error("alpha_run should have been unregistered")
	}
	if _, ok := reg.Get("gamma_run"); !ok {
		t.Error("gamma_run should have been registered")
	}
}

// ── ReloadTool ────────────────────────────────────────────────────────────────

func TestReloadTool_Metadata(t *testing.T) {
	rt := NewReloadTool(NewManager(t.TempDir()), tool.NewRegistry())
	if rt.Name() != "skill_reload" {
		t.Errorf("unexpected Name: %q", rt.Name())
	}
	if rt.Description() == "" {
		t.Error("Description should not be empty")
	}
	var schema map[string]any
	if err := json.Unmarshal(rt.InputSchema(), &schema); err != nil {
		t.Fatalf("InputSchema invalid JSON: %v", err)
	}
}

func TestReloadTool_InitClose(t *testing.T) {
	rt := NewReloadTool(NewManager(t.TempDir()), tool.NewRegistry())
	if err := rt.Init(context.Background()); err != nil {
		t.Errorf("Init should be no-op, got: %v", err)
	}
	if err := rt.Close(); err != nil {
		t.Errorf("Close should be no-op, got: %v", err)
	}
}

func TestReloadTool_Execute(t *testing.T) {
	ws := t.TempDir()
	reg := tool.NewRegistry()
	mgr := NewManager(ws)
	rt := NewReloadTool(mgr, reg)

	result, err := rt.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected go error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}
	// Empty workspace → should mention 0 added, 0 removed.
	if result.Output == "" {
		t.Error("expected non-empty output")
	}
}
