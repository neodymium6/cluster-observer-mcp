package observer

import (
	"reflect"
	"testing"
)

func TestCatalog(t *testing.T) {
	t.Parallel()

	targets := []Target{
		{
			ID:           "cluster-b",
			Kind:         TargetKindKubernetes,
			Capabilities: []Capability{CapabilityKubernetesUnhealthyWorkloads},
		},
		{
			ID:   "cluster-a",
			Kind: TargetKindKubernetes,
			Capabilities: []Capability{
				CapabilityKubernetesUnhealthyWorkloads,
				CapabilityKubernetesClusterHealth,
			},
		},
	}

	catalog, err := NewCatalog(targets)
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}

	got := catalog.List()
	wantIDs := []string{"cluster-a", "cluster-b"}
	gotIDs := []string{got.Targets[0].ID, got.Targets[1].ID}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("List() IDs = %v, want %v", gotIDs, wantIDs)
	}

	got.Targets[0].Capabilities[0] = "changed"
	target, ok := catalog.Lookup("cluster-a")
	if !ok {
		t.Fatal("Lookup() did not find cluster-a")
	}
	if target.Capabilities[0] != CapabilityKubernetesClusterHealth {
		t.Fatal("List() exposed mutable catalog state")
	}

	if _, ok := catalog.Lookup("unknown"); ok {
		t.Fatal("Lookup() unexpectedly found unknown target")
	}
}

func TestCatalogRejectsInvalidTargets(t *testing.T) {
	t.Parallel()

	valid := Target{
		ID:           "cluster-a",
		Kind:         TargetKindKubernetes,
		Capabilities: []Capability{CapabilityKubernetesClusterHealth},
	}

	tests := []struct {
		name    string
		targets []Target
	}{
		{name: "duplicate target", targets: []Target{valid, valid}},
		{
			name: "duplicate capability",
			targets: []Target{{
				ID:   "cluster-a",
				Kind: TargetKindKubernetes,
				Capabilities: []Capability{
					CapabilityKubernetesClusterHealth,
					CapabilityKubernetesClusterHealth,
				},
			}},
		},
		{
			name: "unsupported kind",
			targets: []Target{{
				ID:           "cluster-a",
				Kind:         "generic",
				Capabilities: []Capability{CapabilityKubernetesClusterHealth},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewCatalog(tt.targets); err == nil {
				t.Fatal("NewCatalog() unexpectedly succeeded")
			}
		})
	}
}
