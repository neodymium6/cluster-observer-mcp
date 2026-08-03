package observer

import (
	"fmt"
	"slices"
)

// Catalog contains the public, endpoint-free target inventory.
type Catalog struct {
	targets map[string]Target
}

// NewCatalog validates and copies a bounded target inventory.
func NewCatalog(targets []Target) (*Catalog, error) {
	if len(targets) > MaxTargets {
		return nil, fmt.Errorf("target catalog: maximum is %d", MaxTargets)
	}

	catalog := &Catalog{targets: make(map[string]Target, len(targets))}
	for _, target := range targets {
		if err := validateTarget(target); err != nil {
			return nil, err
		}
		if _, exists := catalog.targets[target.ID]; exists {
			return nil, fmt.Errorf("target catalog: duplicate target identifier")
		}

		copied := target
		copied.Capabilities = slices.Clone(target.Capabilities)
		slices.Sort(copied.Capabilities)
		catalog.targets[copied.ID] = copied
	}

	return catalog, nil
}

func validateTarget(target Target) error {
	if err := ValidateIdentifier("target", target.ID); err != nil {
		return err
	}
	if target.Kind != TargetKindKubernetes && target.Kind != TargetKindMonitoring &&
		target.Kind != TargetKindFlux {
		return &ValidationError{Field: "kind", Reason: "is not supported"}
	}
	if len(target.Capabilities) == 0 {
		return &ValidationError{Field: "capabilities", Reason: "must not be empty"}
	}

	seen := make(map[Capability]struct{}, len(target.Capabilities))
	for _, capability := range target.Capabilities {
		if !capabilityMatchesKind(target.Kind, capability) {
			return &ValidationError{Field: "capabilities", Reason: "contains an unsupported value"}
		}
		if _, exists := seen[capability]; exists {
			return &ValidationError{Field: "capabilities", Reason: "contains a duplicate value"}
		}
		seen[capability] = struct{}{}
	}

	return nil
}

func capabilityMatchesKind(kind TargetKind, capability Capability) bool {
	switch kind {
	case TargetKindKubernetes:
		return capability == CapabilityKubernetesClusterHealth ||
			capability == CapabilityKubernetesUnhealthyWorkloads
	case TargetKindMonitoring:
		return capability == CapabilityMonitoringActiveAlerts ||
			capability == CapabilityMonitoringScrapeHealth
	case TargetKindFlux:
		return capability == CapabilityFluxUnhealthyReconciliations
	default:
		return false
	}
}

// List returns a sorted copy of the public target inventory.
func (c *Catalog) List() ListTargetsOutput {
	targets := make([]Target, 0, len(c.targets))
	for _, target := range c.targets {
		copied := target
		copied.Capabilities = slices.Clone(target.Capabilities)
		targets = append(targets, copied)
	}
	slices.SortFunc(targets, func(a, b Target) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})

	return ListTargetsOutput{Targets: targets}
}

// Lookup resolves an opaque identifier without exposing target configuration.
func (c *Catalog) Lookup(id string) (Target, bool) {
	target, ok := c.targets[id]
	if !ok {
		return Target{}, false
	}
	target.Capabilities = slices.Clone(target.Capabilities)
	return target, true
}
