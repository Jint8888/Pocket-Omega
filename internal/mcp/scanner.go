// Package mcp provides MCP (Model Context Protocol) client support,
// including server config loading, stdio/SSE transport, tool adapters,
// and a security scanner for agent-created skill scripts (Python, TypeScript, JavaScript).
package mcp

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
)

// ScanSeverity indicates how serious a scanner finding is.
type ScanSeverity string

const (
	SeverityCritical ScanSeverity = "critical"
	SeverityWarn     ScanSeverity = "warn"
)

// ScanFinding represents a single security issue found during script scanning.
type ScanFinding struct {
	Rule     string
	Severity ScanSeverity
	Line     int    // 0 for full-source rules
	Snippet  string // trimmed line or "(full-source match)"
}

// lineRule checks individual lines against a regex pattern.
type lineRule struct {
	name     string
	severity ScanSeverity
	pattern  *regexp.Regexp
}

// sourceRule checks the entire source content; contextPattern (if set) must
// also match for the finding to be recorded (AND logic).
type sourceRule struct {
	name           string
	severity       ScanSeverity
	pattern        *regexp.Regexp
	contextPattern *regexp.Regexp // optional secondary match
}

// ── Python rules ──

// lineRules are applied to each line of Python scripts.
// sys.stdin / sys.stdout are intentionally not covered — they are legitimate
// for MCP stdio communication and would create false positives.
var lineRules = []lineRule{
	{
		name:     "dangerous-exec",
		severity: SeverityCritical,
		// Matches subprocess calls, os.system, os.popen — dynamic process execution.
		pattern: regexp.MustCompile(`\b(subprocess\.|os\.system\s*\(|os\.popen\s*\(|commands\.getoutput\s*\()`),
	},
	{
		name:     "dynamic-code",
		severity: SeverityCritical,
		// exec/eval/compile are dynamic code execution vectors in Python.
		pattern: regexp.MustCompile(`\b(exec|eval|compile)\s*\(`),
	},
	{
		name:     "dynamic-import",
		severity: SeverityCritical,
		// __import__ and importlib allow loading arbitrary modules at runtime.
		pattern: regexp.MustCompile(`\b(__import__|importlib\.import_module)\s*\(`),
	},
}

// sourceRules are applied against the full Python file content (multi-line context).
var sourceRules = []sourceRule{
	{
		name:     "env-harvesting",
		severity: SeverityCritical,
		// os.environ access combined with network I/O is suspicious.
		pattern:        regexp.MustCompile(`os\.environ`),
		contextPattern: regexp.MustCompile(`\b(requests\.|urllib\.|httpx\.|socket\.connect|aiohttp\.)`),
	},
	{
		name:     "potential-exfil",
		severity: SeverityWarn,
		// File read combined with outbound network call.
		pattern:        regexp.MustCompile(`\bopen\s*\([^)]*['"rb]`),
		contextPattern: regexp.MustCompile(`\b(requests\.|urllib\.|httpx\.|socket\.connect|aiohttp\.)`),
	},
	{
		name:     "obfuscated-code",
		severity: SeverityWarn,
		// base64 decoding combined with dynamic execution is a common obfuscation pattern.
		pattern:        regexp.MustCompile(`\bbase64\b`),
		contextPattern: regexp.MustCompile(`\b(exec|eval)\s*\(`),
	},
}

// ── TypeScript / JavaScript rules ──

// tsLineRules are applied to each line of TypeScript/JavaScript scripts.
var tsLineRules = []lineRule{
	{
		name:     "dangerous-exec",
		severity: SeverityCritical,
		// child_process spawn/exec, execSync — dynamic process execution.
		pattern: regexp.MustCompile(`\b(child_process|execSync|execFileSync|spawnSync)\b`),
	},
	{
		name:     "dynamic-code",
		severity: SeverityCritical,
		// eval, Function constructor, vm.runInNewContext — dynamic code execution.
		pattern: regexp.MustCompile(`\b(eval\s*\(|new\s+Function\s*\(|vm\.run)`),
	},
}

// tsSourceRules are applied against the full TypeScript/JavaScript file content.
var tsSourceRules = []sourceRule{
	{
		name:     "env-harvesting",
		severity: SeverityCritical,
		// process.env access combined with network I/O (fetch/http/https/axios).
		pattern:        regexp.MustCompile(`process\.env`),
		contextPattern: regexp.MustCompile(`\b(fetch\s*\(|https?\.|axios\.|node-fetch)`),
	},
	{
		name:     "potential-exfil",
		severity: SeverityWarn,
		// fs.readFile combined with outbound network call.
		pattern:        regexp.MustCompile(`\bfs\.(readFile|readFileSync|createReadStream)\b`),
		contextPattern: regexp.MustCompile(`\b(fetch\s*\(|https?\.|axios\.|node-fetch)`),
	},
}

// ScanScript performs a static security scan on a script file.
// Supports .py, .ts, and .js files; other file types return (nil, nil).
//
// Critical findings should block script activation.
// Warn findings are logged but allow activation to continue.
func ScanScript(filePath string) ([]ScanFinding, error) {
	var lRules []lineRule
	var sRules []sourceRule
	var isPython bool

	switch {
	case strings.HasSuffix(filePath, ".py"):
		lRules, sRules = lineRules, sourceRules
		isPython = true
	case strings.HasSuffix(filePath, ".ts"), strings.HasSuffix(filePath, ".js"):
		lRules, sRules = tsLineRules, tsSourceRules
		isPython = false
	default:
		return nil, nil
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("scanner: read %q: %w", filePath, err)
	}

	source := string(data)
	var findings []ScanFinding

	// Per-line rules
	scanner := bufio.NewScanner(strings.NewReader(source))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Skip comment-only lines (language-aware prefix to avoid false skips:
		// Python uses `#`, JS/TS uses `//`).
		stripped := strings.TrimSpace(line)
		if (isPython && strings.HasPrefix(stripped, "#")) ||
			(!isPython && strings.HasPrefix(stripped, "//")) {
			continue
		}

		for _, rule := range lRules {
			if rule.pattern.MatchString(line) {
				findings = append(findings, ScanFinding{
					Rule:     rule.name,
					Severity: rule.severity,
					Line:     lineNum,
					Snippet:  stripped,
				})
				// Do NOT break — allow every rule to match this line independently.
			}
		}
	}

	// Full-source rules
	for _, rule := range sRules {
		if !rule.pattern.MatchString(source) {
			continue
		}
		if rule.contextPattern != nil && !rule.contextPattern.MatchString(source) {
			continue
		}
		findings = append(findings, ScanFinding{
			Rule:     rule.name,
			Severity: rule.severity,
			Line:     0,
			Snippet:  "(full-source match)",
		})
	}

	return findings, nil
}

// HasCritical returns true if any finding has critical severity.
func HasCritical(findings []ScanFinding) bool {
	for _, f := range findings {
		if f.Severity == SeverityCritical {
			return true
		}
	}
	return false
}

// LogFindings writes all findings to the standard logger.
func LogFindings(serverName string, findings []ScanFinding) {
	for _, f := range findings {
		if f.Line > 0 {
			log.Printf("[MCP/Scanner] %s server=%q rule=%s line=%d: %s",
				strings.ToUpper(string(f.Severity)), serverName, f.Rule, f.Line, f.Snippet)
		} else {
			log.Printf("[MCP/Scanner] %s server=%q rule=%s: %s",
				strings.ToUpper(string(f.Severity)), serverName, f.Rule, f.Snippet)
		}
	}
}
