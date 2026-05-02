// Copyright (C) 2026 Techdelight BV

package web

import (
	"encoding/json"
	"net/http"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/programme"
)

// handleListProgrammes returns all programmes.
func (ws *WebServer) handleListProgrammes(w http.ResponseWriter, r *http.Request) {
	store := programme.New(ws.cfg.ProgrammesDir())
	progs, err := store.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if progs == nil {
		progs = []core.Programme{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(progs)
}

// handleCreateProgramme creates a new programme.
func (ws *WebServer) handleCreateProgramme(w http.ResponseWriter, r *http.Request) {
	var p core.Programme
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	store := programme.New(ws.cfg.ProgrammesDir())
	if err := store.Create(p); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(p)
}

// handleGetProgramme returns a single programme by name.
func (ws *WebServer) handleGetProgramme(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	store := programme.New(ws.cfg.ProgrammesDir())
	p, err := store.Read(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(p)
}

// handleUpdateProgramme updates an existing programme.
func (ws *WebServer) handleUpdateProgramme(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var p core.Programme
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	p.Name = name
	store := programme.New(ws.cfg.ProgrammesDir())
	if err := store.Update(p); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(p)
}

// handleDeleteProgramme deletes a programme by name.
func (ws *WebServer) handleDeleteProgramme(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	store := programme.New(ws.cfg.ProgrammesDir())
	if err := store.Remove(name); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "name": name})
}
