package web

import (
	"encoding/json"
	"net/http"
	"time"
)

// HealthInfo holds runtime status for the health endpoint.
type HealthInfo struct {
	LLMModel       string     // from config
	ToolCount      int        // registry.List() length
	MCPServerCount int        // from MCP manager
	SessionCount   func() int // callback to session store
}

// HealthHandler serves GET /api/health.
type HealthHandler struct {
	info      HealthInfo
	startTime time.Time
}

// NewHealthHandler creates a health handler recording the server start time.
func NewHealthHandler(info HealthInfo) *HealthHandler {
	return &HealthHandler{info: info, startTime: time.Now()}
}

type healthResponse struct {
	Status     string           `json:"status"`
	UptimeSecs int64            `json:"uptime_seconds"`
	Components healthComponents `json:"components"`
}

type healthComponents struct {
	LLM      healthLLM      `json:"llm"`
	Tools    healthTools    `json:"tools"`
	MCP      healthMCP      `json:"mcp"`
	Sessions healthSessions `json:"sessions"`
}

type healthLLM struct {
	Status string `json:"status"`
	Model  string `json:"model"`
}
type healthTools struct {
	Registered int `json:"registered"`
}
type healthMCP struct {
	Servers int `json:"servers"`
}
type healthSessions struct {
	Active int `json:"active"`
}

// ServeHTTP handles GET /api/health.
func (h *HealthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	llmStatus := "ok"
	if h.info.LLMModel == "" {
		llmStatus = "degraded"
	}

	sessionCount := 0
	if h.info.SessionCount != nil {
		sessionCount = h.info.SessionCount()
	}

	status := "ok"
	if llmStatus == "degraded" {
		status = "degraded"
	}

	resp := healthResponse{
		Status:     status,
		UptimeSecs: int64(time.Since(h.startTime).Seconds()),
		Components: healthComponents{
			LLM:      healthLLM{Status: llmStatus, Model: h.info.LLMModel},
			Tools:    healthTools{Registered: h.info.ToolCount},
			MCP:      healthMCP{Servers: h.info.MCPServerCount},
			Sessions: healthSessions{Active: sessionCount},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
