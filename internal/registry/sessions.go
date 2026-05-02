// Copyright (C) 2026 Techdelight BV

package registry

import (
	"fmt"

	"github.com/techdelight/daedalus/core"
)

// maxSessionHistory is the maximum number of session records kept per project.
const maxSessionHistory = 50

// StartSession records a new session start for the named project.
// Returns the session ID (monotonic counter based on len(Sessions)+1).
// Caps history at maxSessionHistory by trimming oldest entries.
func (r *Registry) StartSession(projectName, resumeID string) (string, error) {
	data, err := r.read()
	if err != nil {
		return "", err
	}
	entry, ok := data.Projects[projectName]
	if !ok {
		return "", fmt.Errorf("project '%s' not found", projectName)
	}

	sessionID := fmt.Sprintf("%d", len(entry.Sessions)+1)
	rec := core.SessionRecord{
		ID:       sessionID,
		Started:  core.NowUTC(),
		ResumeID: resumeID,
	}
	entry.Sessions = append(entry.Sessions, rec)

	// Cap at maxSessionHistory
	if len(entry.Sessions) > maxSessionHistory {
		entry.Sessions = entry.Sessions[len(entry.Sessions)-maxSessionHistory:]
	}

	data.Projects[projectName] = entry
	if err := r.write(data); err != nil {
		return "", err
	}
	return sessionID, nil
}

// EndSession records the end of a session with timestamp and duration.
func (r *Registry) EndSession(projectName, sessionID string) error {
	data, err := r.read()
	if err != nil {
		return err
	}
	entry, ok := data.Projects[projectName]
	if !ok {
		return fmt.Errorf("project '%s' not found", projectName)
	}

	found := false
	for i := len(entry.Sessions) - 1; i >= 0; i-- {
		if entry.Sessions[i].ID == sessionID {
			now := core.NowUTC()
			entry.Sessions[i].Ended = now
			startTime, err := core.ParseUTC(entry.Sessions[i].Started)
			if err == nil {
				endTime, err2 := core.ParseUTC(now)
				if err2 == nil {
					entry.Sessions[i].Duration = int(endTime.Sub(startTime).Seconds())
				}
			}
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("session '%s' not found for project '%s'", sessionID, projectName)
	}

	data.Projects[projectName] = entry
	return r.write(data)
}
