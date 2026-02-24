package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
)

// LoadEnv loads environment variables from a .env file.
//
// Search order (stops at the first file found):
//  1. Explicit paths passed as arguments (legacy / test use).
//  2. Directory of the running executable  — stable after workspace migration.
//  3. Current working directory            — fallback for `go run ./cmd/omega`.
//
// If no .env is found anywhere, the program continues with system env vars.
func LoadEnv(paths ...string) {
	// Caller-supplied paths (legacy / test support).
	if len(paths) > 0 {
		if err := godotenv.Load(paths...); err != nil {
			log.Printf("[Config] No .env file at specified path(s), using system environment variables")
		}
		return
	}

	candidates := resolveEnvCandidates()
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			if err := godotenv.Load(p); err != nil {
				log.Printf("[Config] Failed to load .env from %s: %v", p, err)
			} else {
				log.Printf("[Config] Loaded .env from %s", p)
			}
			return
		}
	}

	log.Printf("[Config] No .env file found (searched: %v), using system environment variables", candidates)
}

// resolveEnvCandidates returns the ordered list of .env paths to probe.
// Exported so tests can verify path resolution without side-effects.
func resolveEnvCandidates() []string {
	var candidates []string
	seen := map[string]bool{}

	add := func(p string) {
		p = filepath.Clean(p)
		if !seen[p] {
			seen[p] = true
			candidates = append(candidates, p)
		}
	}

	// 1. Walk up from the executable directory (up to 3 levels).
	//    This lets bin/omega.exe naturally find the project-root .env
	//    without requiring users to place .env anywhere unusual.
	//    e.g.  E:\proj\bin\omega.exe  →  checks bin\.env, then E:\proj\.env  ✅
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		dir := filepath.Dir(exe)
		for i := 0; i <= 3; i++ {
			add(filepath.Join(dir, ".env"))
			parent := filepath.Dir(dir)
			if parent == dir {
				break // reached filesystem root
			}
			dir = parent
		}
	}

	// 2. Current working directory — fallback for `go run ./cmd/omega`.
	if cwd, err := os.Getwd(); err == nil {
		add(filepath.Join(cwd, ".env"))
	}

	return candidates
}

// EnvFilePath returns a human-readable description of where .env will be loaded
// from. Useful for startup log messages.
func EnvFilePath() string {
	for _, p := range resolveEnvCandidates() {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return fmt.Sprintf("(not found; searched %v)", resolveEnvCandidates())
}
