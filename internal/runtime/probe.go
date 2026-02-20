// Package runtime provides utilities for detecting available runtimes
// on the host system, used to guide Agent tool selection at startup.
package runtime

import (
	"bytes"
	"log"
	"os/exec"
	"sync/atomic"
)

// NodeRuntimeInfo holds the result of runtime detection for Node.js / tsx.
// It is populated by ProbeNodeRuntime and should be treated as read-only
// after that call returns.
type NodeRuntimeInfo struct {
	// NodeAvailable reports whether `node` was found in PATH (synchronous check).
	NodeAvailable bool

	// TsxAvailable reports whether `tsx` was found in PATH at startup (synchronous check).
	// If true, TsxReady is nil (no background install was needed).
	TsxAvailable bool

	// TsxReady is non-nil only when tsx was absent at startup but node was present,
	// triggering a background global install. Poll TsxReady.Load() to check whether
	// the install has completed. Nil means either tsx was already available (check
	// TsxAvailable) or node is absent (no install attempted).
	TsxReady *atomic.Bool
}

// IsTsxReady returns true when tsx is usable: either it was already installed
// at startup, or the background install has since completed successfully.
func (n *NodeRuntimeInfo) IsTsxReady() bool {
	if n.TsxAvailable {
		return true
	}
	if n.TsxReady != nil {
		return n.TsxReady.Load()
	}
	return false
}

// StatusString returns a human-readable status for injection into the
// [运行时环境] block of mcp_server_guide.md.
func (n *NodeRuntimeInfo) StatusString() string {
	nodeStatus := "不可用"
	if n.NodeAvailable {
		nodeStatus = "可用"
	}

	var tsxStatus string
	switch {
	case n.TsxAvailable:
		tsxStatus = "可用"
	case n.TsxReady != nil && n.TsxReady.Load():
		tsxStatus = "可用（已安装）"
	case n.TsxReady != nil:
		tsxStatus = "安装中（请稍候）"
	default:
		tsxStatus = "不可用"
	}

	return "Node.js: " + nodeStatus + "\ntsx (全局): " + tsxStatus
}

// ProbeNodeRuntime detects available Node.js runtimes synchronously, and
// installs tsx in the background if node is present but tsx is missing.
//
// Stage 1 (synchronous, milliseconds): uses exec.LookPath to detect node and tsx.
// Stage 2 (async, only when tsx absent + node available): runs
// `npm install -g tsx` in a background goroutine; output is captured to a
// buffer to avoid interleaving with HTTP server log output.
//
// The caller should invoke this before starting the HTTP server. The returned
// NodeRuntimeInfo can be queried at any time; IsTsxReady() is goroutine-safe.
func ProbeNodeRuntime() NodeRuntimeInfo {
	info := NodeRuntimeInfo{}

	// Stage 1: synchronous PATH checks (millisecond-level, non-blocking).
	if _, err := exec.LookPath("node"); err == nil {
		info.NodeAvailable = true
	}
	if _, err := exec.LookPath("tsx"); err == nil {
		info.TsxAvailable = true
		return info // tsx already ready, no background install needed
	}

	// Stage 2: tsx absent but node present — install tsx globally in background.
	if info.NodeAvailable {
		ready := &atomic.Bool{}
		info.TsxReady = ready
		log.Println("[Runtime] tsx not found, installing globally in background...")
		go func() {
			cmd := exec.Command("npm", "install", "-g", "tsx")
			// Capture to buffer to avoid npm progress output interleaving with
			// structured HTTP server logs.
			var buf bytes.Buffer
			cmd.Stdout = &buf
			cmd.Stderr = &buf
			if err := cmd.Run(); err != nil {
				log.Printf("[Runtime] WARNING: tsx global install failed: %v\nOutput: %s", err, buf.String())
			} else {
				ready.Store(true)
				log.Println("[Runtime] tsx installed successfully")
			}
		}()
	}

	return info
}
