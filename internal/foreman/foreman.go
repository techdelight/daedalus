// Copyright (C) 2026 Techdelight BV

package foreman

import (
	"fmt"
	"sync"
	"time"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/mcpclient"
	"github.com/techdelight/daedalus/internal/programme"
	"github.com/techdelight/daedalus/internal/registry"
)

// Foreman is the AI-driven project manager that monitors programmes.
type Foreman struct {
	mu         sync.RWMutex
	cfg        core.ForemanConfig
	state      core.ForemanState
	plan       *core.ForemanPlan
	message    string
	cascadeLog []core.CascadeEventInfo
	stopped    bool
	stopCh     chan struct{}

	programmes *programme.Store
	registry   *registry.Registry
	client     *mcpclient.Client
	observer   AgentObserver
	planner    *Planner
	monitor    *Monitor
}

// New creates a new Foreman.
func New(cfg core.ForemanConfig, programmes *programme.Store, reg *registry.Registry, client *mcpclient.Client, observer AgentObserver) *Foreman {
	f := &Foreman{
		cfg:        cfg,
		state:      core.ForemanIdle,
		stopCh:     make(chan struct{}),
		programmes: programmes,
		registry:   reg,
		client:     client,
		observer:   observer,
	}
	f.planner = NewPlanner(programmes, reg, client)
	f.monitor = NewMonitor(reg, client, observer)
	return f
}

// Start begins the Foreman's main loop in a goroutine.
func (f *Foreman) Start() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state != core.ForemanIdle && f.state != core.ForemanStopped {
		return fmt.Errorf("foreman is already running (state: %s)", f.state)
	}
	f.state = core.ForemanPlanning
	f.message = "starting"
	f.stopped = false
	f.stopCh = make(chan struct{})
	go f.run()
	return nil
}

// Stop signals the Foreman to stop.
func (f *Foreman) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopped || f.state == core.ForemanIdle {
		return
	}
	f.stopped = true
	close(f.stopCh)
	f.state = core.ForemanStopped
	f.message = "stopped"
}

// Status returns the current Foreman status.
func (f *Foreman) Status() core.ForemanStatus {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return core.ForemanStatus{
		State:      f.state,
		Plan:       f.plan,
		Message:    f.message,
		CascadeLog: f.cascadeLog,
	}
}

// AppendCascadeLog adds cascade event summaries to the Foreman's log.
func (f *Foreman) AppendCascadeLog(events []core.CascadeEventInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cascadeLog = append(f.cascadeLog, events...)
}

// run is the main loop.
func (f *Foreman) run() {
	poll := time.Duration(f.cfg.PollSeconds) * time.Second
	if poll == 0 {
		poll = 30 * time.Second
	}

	// Initial planning phase
	f.setState(core.ForemanPlanning, "building plan")
	plan, err := f.planner.BuildPlan(f.cfg.Programme)
	if err != nil {
		f.setState(core.ForemanStopped, fmt.Sprintf("planning failed: %v", err))
		return
	}
	f.mu.Lock()
	f.plan = plan
	f.state = core.ForemanMonitoring
	f.message = "monitoring " + f.cfg.Programme
	f.mu.Unlock()

	// Monitoring loop
	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for {
		select {
		case <-f.stopCh:
			return
		case <-ticker.C:
			f.setState(core.ForemanMonitoring, "polling project status")
			updated, err := f.monitor.UpdatePlan(plan)
			if err != nil {
				f.setState(core.ForemanMonitoring, fmt.Sprintf("monitor error: %v", err))
				continue
			}
			f.mu.Lock()
			f.plan = updated
			f.mu.Unlock()
		}
	}
}

func (f *Foreman) setState(state core.ForemanState, msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = state
	f.message = msg
}
