// Copyright (C) 2026 Techdelight BV

package web

import (
	"encoding/json"
	"net/http"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/foreman"
	"github.com/techdelight/daedalus/internal/mcpclient"
	"github.com/techdelight/daedalus/internal/programme"
)

// handleForemanStatus returns the current Foreman state.
func (ws *WebServer) handleForemanStatus(w http.ResponseWriter, r *http.Request) {
	if ws.foreman == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(core.ForemanStatus{State: core.ForemanIdle, Message: "not configured"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ws.foreman.Status())
}

// handleForemanStart starts the Foreman for a given programme.
func (ws *WebServer) handleForemanStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Programme string `json:"programme"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Programme == "" {
		http.Error(w, "request body must include \"programme\" field", http.StatusBadRequest)
		return
	}

	// Create Foreman if needed
	if ws.foreman == nil {
		progStore := programme.New(ws.cfg.ProgrammesDir())
		mcpClient := mcpclient.New()
		obs := foreman.NewDefaultObserver(ws.observer)
		cfg := core.ForemanConfig{Programme: req.Programme, PollSeconds: 30}
		ws.foreman = foreman.New(cfg, progStore, ws.registry, mcpClient, obs)
	}

	if err := ws.foreman.Start(); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "started", "programme": req.Programme})
}

// handleForemanStop stops the Foreman.
func (ws *WebServer) handleForemanStop(w http.ResponseWriter, r *http.Request) {
	if ws.foreman == nil {
		http.Error(w, "foreman is not running", http.StatusConflict)
		return
	}
	ws.foreman.Stop()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "stopped"})
}
