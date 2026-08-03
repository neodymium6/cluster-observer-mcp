// Package observer contains transport-independent observation contracts and
// the policy limits applied before results reach an MCP client.
package observer

import "time"

const (
	// MaxEncodedResultBytes is the maximum encoded structured tool result.
	MaxEncodedResultBytes = 64 * 1024
	// MaxTargets bounds target discovery results.
	MaxTargets = 32
	// DefaultListLimit is used when a workload list limit is omitted.
	DefaultListLimit = 20
	// MaxListLimit bounds workload list results.
	MaxListLimit = 50
	// MaxWarnings bounds summary warnings.
	MaxWarnings = 20
)

// TargetKind identifies a purpose-built observation source.
type TargetKind string

const (
	// TargetKindKubernetes identifies a Kubernetes API target.
	TargetKindKubernetes TargetKind = "kubernetes"
)

// Capability identifies a tool family supported by a target.
type Capability string

const (
	// CapabilityKubernetesClusterHealth permits cluster health observations.
	CapabilityKubernetesClusterHealth Capability = "kubernetes.cluster-health"
	// CapabilityKubernetesUnhealthyWorkloads permits bounded unhealthy workload
	// observations.
	CapabilityKubernetesUnhealthyWorkloads Capability = "kubernetes.unhealthy-workloads"
)

// Target is the public, endpoint-free description of a configured target.
type Target struct {
	ID           string       `json:"id"`
	Kind         TargetKind   `json:"kind"`
	Capabilities []Capability `json:"capabilities"`
}

// ListTargetsInput is intentionally empty.
type ListTargetsInput struct{}

// ListTargetsOutput is the bounded target discovery result.
type ListTargetsOutput struct {
	Targets []Target `json:"targets"`
}

// GetClusterHealthInput selects one configured Kubernetes target.
type GetClusterHealthInput struct {
	Target string `json:"target" jsonschema:"opaque configured target identifier"`
}

// HealthStatus is a normalized aggregate health state.
type HealthStatus string

const (
	// HealthStatusHealthy means every observed component is ready.
	HealthStatusHealthy HealthStatus = "healthy"
	// HealthStatusDegraded means at least one observed component is not ready.
	HealthStatusDegraded HealthStatus = "degraded"
	// HealthStatusUnknown means the source could not provide a complete state.
	HealthStatusUnknown HealthStatus = "unknown"
)

// NodeHealth contains bounded node readiness totals.
type NodeHealth struct {
	Total int `json:"total"`
	Ready int `json:"ready"`
}

// WorkloadSummary contains bounded workload readiness totals.
type WorkloadSummary struct {
	Total     int `json:"total"`
	Ready     int `json:"ready"`
	Unhealthy int `json:"unhealthy"`
}

// WarningCode is a normalized warning that cannot contain source text.
type WarningCode string

const (
	// WarningNodesNotReady indicates one or more non-ready nodes.
	WarningNodesNotReady WarningCode = "nodes-not-ready"
	// WarningWorkloadsNotReady indicates one or more non-ready workloads.
	WarningWorkloadsNotReady WarningCode = "workloads-not-ready"
	// WarningPartialObservation indicates an incomplete source response.
	WarningPartialObservation WarningCode = "partial-observation"
)

// HealthWarning reports a normalized warning and affected object count.
type HealthWarning struct {
	Code  WarningCode `json:"code"`
	Count int         `json:"count"`
}

// ClusterHealthOutput is the structured cluster health result.
type ClusterHealthOutput struct {
	Target     string          `json:"target"`
	ObservedAt time.Time       `json:"observedAt"`
	Status     HealthStatus    `json:"status"`
	Nodes      NodeHealth      `json:"nodes"`
	Workloads  WorkloadSummary `json:"workloads"`
	Warnings   []HealthWarning `json:"warnings"`
	Partial    bool            `json:"partial"`
}

// ListUnhealthyWorkloadsInput selects a configured target and optional scope.
type ListUnhealthyWorkloadsInput struct {
	Target string `json:"target" jsonschema:"opaque configured target identifier"`
	Scope  string `json:"scope,omitempty" jsonschema:"optional configured scope identifier"`
	Limit  int    `json:"limit,omitempty" jsonschema:"maximum number of workloads, from 1 to 50"`
}

// WorkloadKind is a supported Kubernetes controller kind.
type WorkloadKind string

const (
	// WorkloadKindDeployment identifies a Deployment.
	WorkloadKindDeployment WorkloadKind = "Deployment"
	// WorkloadKindStatefulSet identifies a StatefulSet.
	WorkloadKindStatefulSet WorkloadKind = "StatefulSet"
	// WorkloadKindDaemonSet identifies a DaemonSet.
	WorkloadKindDaemonSet WorkloadKind = "DaemonSet"
)

// WorkloadReason is a normalized reason that cannot contain source text.
type WorkloadReason string

const (
	// WorkloadReasonNotReady is the generic non-ready reason.
	WorkloadReasonNotReady WorkloadReason = "not-ready"
	// WorkloadReasonReplicaFailure indicates that replicas could not be created.
	WorkloadReasonReplicaFailure WorkloadReason = "replica-failure"
	// WorkloadReasonProgressDeadline indicates a rollout deadline failure.
	WorkloadReasonProgressDeadline WorkloadReason = "progress-deadline-exceeded"
	// WorkloadReasonUnschedulable indicates that a workload cannot be scheduled.
	WorkloadReasonUnschedulable WorkloadReason = "unschedulable"
	// WorkloadReasonUnknown replaces any source reason not on the allowlist.
	WorkloadReasonUnknown WorkloadReason = "unknown"
)

// WorkloadHealth is the safe subset of one unhealthy workload.
type WorkloadHealth struct {
	Scope   string           `json:"scope"`
	Kind    WorkloadKind     `json:"kind"`
	Name    string           `json:"name"`
	Ready   int              `json:"ready"`
	Desired int              `json:"desired"`
	Reasons []WorkloadReason `json:"reasons"`
}

// ListUnhealthyWorkloadsOutput is a bounded unhealthy workload result.
type ListUnhealthyWorkloadsOutput struct {
	Target     string           `json:"target"`
	ObservedAt time.Time        `json:"observedAt"`
	Workloads  []WorkloadHealth `json:"workloads"`
	Truncated  bool             `json:"truncated"`
}
