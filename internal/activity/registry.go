// Copyright (C) 2026 Techdelight BV

package activity

// DetectorRegistry maps runner names to their activity detectors.
// Unknown runners fall back to NullDetector (always idle).
type DetectorRegistry struct {
	detectors map[string]RunnerActivityDetector
	fallback  RunnerActivityDetector
}

// NewDetectorRegistry creates an empty registry with NullDetector as fallback.
func NewDetectorRegistry() *DetectorRegistry {
	return &DetectorRegistry{
		detectors: make(map[string]RunnerActivityDetector),
		fallback:  &NullDetector{},
	}
}

// Register adds a detector for the named runner.
func (dr *DetectorRegistry) Register(name string, d RunnerActivityDetector) {
	dr.detectors[name] = d
}

// Get returns the detector for the named runner, or the fallback for unknown runners.
func (dr *DetectorRegistry) Get(name string) RunnerActivityDetector {
	if d, ok := dr.detectors[name]; ok {
		return d
	}
	return dr.fallback
}
