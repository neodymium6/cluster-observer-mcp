package observer

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateIdentifier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{name: "short", value: "a", valid: true},
		{name: "hyphenated", value: "cluster-a", valid: true},
		{name: "maximum length", value: strings.Repeat("a", 32), valid: true},
		{name: "empty", value: "", valid: false},
		{name: "uppercase", value: "Cluster", valid: false},
		{name: "leading hyphen", value: "-cluster", valid: false},
		{name: "trailing hyphen", value: "cluster-", valid: false},
		{name: "path", value: "cluster/a", valid: false},
		{name: "URL", value: "https://example.invalid", valid: false},
		{name: "too long", value: strings.Repeat("a", 33), valid: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateIdentifier("target", tt.value)
			if tt.valid && err != nil {
				t.Fatalf("ValidateIdentifier() error = %v", err)
			}
			if !tt.valid && err == nil {
				t.Fatal("ValidateIdentifier() unexpectedly succeeded")
			}
		})
	}
}

func TestNormalizeListUnhealthyWorkloadsInput(t *testing.T) {
	t.Parallel()

	input := ListUnhealthyWorkloadsInput{Target: "cluster-a"}
	got, err := input.Normalize()
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if got.Limit != DefaultListLimit {
		t.Fatalf("Normalize().Limit = %d, want %d", got.Limit, DefaultListLimit)
	}

	for _, limit := range []int{-1, MaxListLimit + 1} {
		input.Limit = limit
		if _, err := input.Normalize(); err == nil {
			t.Fatalf("Normalize() unexpectedly accepted limit %d", limit)
		}
	}
}

func TestNormalizeWorkloadReason(t *testing.T) {
	t.Parallel()

	if got := NormalizeWorkloadReason("ProgressDeadlineExceeded"); got != WorkloadReasonProgressDeadline {
		t.Fatalf("NormalizeWorkloadReason() = %q", got)
	}
	if got := NormalizeWorkloadReason("Bearer private-value"); got != WorkloadReasonUnknown {
		t.Fatalf("unknown reason was not redacted: %q", got)
	}
}

func TestCheckResultSize(t *testing.T) {
	t.Parallel()

	if err := CheckResultSize(map[string]string{"status": "healthy"}); err != nil {
		t.Fatalf("CheckResultSize() error = %v", err)
	}

	oversized := map[string]string{"value": strings.Repeat("x", MaxEncodedResultBytes)}
	if err := CheckResultSize(oversized); !errors.Is(err, ErrResultTooLarge) {
		t.Fatalf("CheckResultSize() error = %v, want ErrResultTooLarge", err)
	}
}

func FuzzNormalizeWorkloadReason(f *testing.F) {
	for _, reason := range []string{
		"NotReady",
		"ReplicaFailure",
		"ProgressDeadlineExceeded",
		"Unschedulable",
		"source-only-text",
	} {
		f.Add(reason)
	}

	allowed := map[WorkloadReason]bool{
		WorkloadReasonNotReady:         true,
		WorkloadReasonReplicaFailure:   true,
		WorkloadReasonProgressDeadline: true,
		WorkloadReasonUnschedulable:    true,
		WorkloadReasonUnknown:          true,
	}
	f.Fuzz(func(t *testing.T, reason string) {
		if got := NormalizeWorkloadReason(reason); !allowed[got] {
			t.Fatalf("NormalizeWorkloadReason() returned non-allowlisted value %q", got)
		}
	})
}
