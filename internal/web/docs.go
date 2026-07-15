// Copyright (C) 2026 Techdelight BV

package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/techdelight/daedalus/core"
)

// docJSON is one required document plus whether the project carries it.
type docJSON struct {
	Name        string `json:"name"`
	Filename    string `json:"filename"`
	Description string `json:"description"`
	Present     bool   `json:"present"`
}

// docsJSON is the JSON response for the docs endpoint. Present/Total drive
// the sidebar badge; Missing is precomputed so the hover list does not have
// to be filtered client-side.
type docsJSON struct {
	Docs    []docJSON `json:"docs"`
	Present int       `json:"present"`
	Total   int       `json:"total"`
	Missing []string  `json:"missing"`
}

// visionJSON is the JSON response for the vision endpoint.
type visionJSON struct {
	Content string `json:"content"`
}

// handleDocs reports which of core.RequiredDocs a project carries.
//
// The document set is hardcoded; per-project opt-out is Backlog #53. Note
// that SPRINTS.md is checked literally, so a project predating the doc split
// that keeps its sprints in ROADMAP.md reads as missing SPRINTS.md even
// though readSprints would still serve it — also #53.
func (ws *WebServer) handleDocs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	entry, found, err := ws.registry.GetProject(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, fmt.Sprintf("project %q not found", name), http.StatusNotFound)
		return
	}

	required := core.RequiredDocs()
	resp := docsJSON{
		Docs:    make([]docJSON, 0, len(required)),
		Total:   len(required),
		Missing: []string{},
	}

	for _, d := range required {
		present := docPresent(entry.Directory, d.Filename)
		if present {
			resp.Present++
		} else {
			resp.Missing = append(resp.Missing, d.Name)
		}
		resp.Docs = append(resp.Docs, docJSON{
			Name:        d.Name,
			Filename:    d.Filename,
			Description: d.Description,
			Present:     present,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// docPresent reports whether dir holds a usable copy of filename.
//
// An empty file does not count: `touch VISION.md` leaves a placeholder, not
// a vision, and reporting it as present would hide exactly the gap the badge
// exists to surface. A directory named like a doc does not count either.
// Any stat error other than not-exist (a permission problem, say) is treated
// as absent — the badge is a hint, not an audit, and must not 500 the view.
func docPresent(dir, filename string) bool {
	info, err := os.Stat(filepath.Join(dir, filename))
	if err != nil {
		return false
	}
	return !info.IsDir() && info.Size() > 0
}

// handleVision returns the raw VISION.md content for a project.
//
// A project with no VISION.md is a 404 — the document genuinely is not there,
// and the caller should say so rather than render an empty panel. An empty
// file is a 404 too: `touch VISION.md` is a placeholder, not a vision, and
// docPresent already refuses to count one toward the badge, so serving it as
// 200 here would leave the two disagreeing about the same file.
//
// This deliberately does not follow handleStrategicRoadmap, which returns 200
// with an empty string and lets the frontend infer absence.
//
// The content is the file on disk, not the `vision` tagline that
// handleDashboard serves from progress.json — see Backlog #52 for
// collapsing the two.
func (ws *WebServer) handleVision(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	entry, found, err := ws.registry.GetProject(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, fmt.Sprintf("project %q not found", name), http.StatusNotFound)
		return
	}

	if !docPresent(entry.Directory, "VISION.md") {
		http.Error(w, fmt.Sprintf("project %q has no VISION.md", name), http.StatusNotFound)
		return
	}

	data, err := os.ReadFile(filepath.Join(entry.Directory, "VISION.md"))
	if err != nil {
		http.Error(w, fmt.Sprintf("reading vision: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(visionJSON{Content: string(data)})
}
