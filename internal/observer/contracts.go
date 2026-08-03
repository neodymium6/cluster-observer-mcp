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
	// TargetKindMonitoring identifies bounded Prometheus and Alertmanager
	// observations reached through the Kubernetes API.
	TargetKindMonitoring TargetKind = "monitoring"
	// TargetKindFlux identifies bounded Flux reconciliation observations.
	TargetKindFlux TargetKind = "flux"
)

// Capability identifies a tool family supported by a target.
type Capability string

const (
	// CapabilityKubernetesClusterHealth permits cluster health observations.
	CapabilityKubernetesClusterHealth Capability = "kubernetes.cluster-health"
	// CapabilityKubernetesUnhealthyWorkloads permits bounded unhealthy workload
	// observations.
	CapabilityKubernetesUnhealthyWorkloads Capability = "kubernetes.unhealthy-workloads"
	// CapabilityMonitoringActiveAlerts permits bounded active-alert
	// observations.
	CapabilityMonitoringActiveAlerts Capability = "monitoring.active-alerts"
	// CapabilityMonitoringScrapeHealth permits fixed Prometheus up-series
	// observations.
	CapabilityMonitoringScrapeHealth Capability = "monitoring.scrape-health"
	// CapabilityFluxUnhealthyReconciliations permits bounded Flux readiness
	// observations.
	CapabilityFluxUnhealthyReconciliations Capability = "flux.unhealthy-reconciliations"
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

// ListActiveAlertsInput selects one monitoring target and a bounded result
// limit.
type ListActiveAlertsInput struct {
	Target string `json:"target" jsonschema:"opaque configured target identifier"`
	Limit  int    `json:"limit,omitempty" jsonschema:"maximum number of alerts, from 1 to 50"`
}

// AlertSeverity is a normalized severity that cannot contain source text.
type AlertSeverity string

const (
	// AlertSeverityCritical identifies an urgent active alert.
	AlertSeverityCritical AlertSeverity = "critical"
	// AlertSeverityWarning identifies a non-critical active alert.
	AlertSeverityWarning AlertSeverity = "warning"
	// AlertSeverityInfo identifies an informational active alert.
	AlertSeverityInfo AlertSeverity = "info"
	// AlertSeverityUnknown replaces source severities outside the allowlist.
	AlertSeverityUnknown AlertSeverity = "unknown"
)

// ActiveAlert is the safe subset of one Alertmanager alert.
type ActiveAlert struct {
	Name      string        `json:"name"`
	Severity  AlertSeverity `json:"severity"`
	Component string        `json:"component,omitempty"`
	StartedAt *time.Time    `json:"startedAt,omitempty"`
}

// ListActiveAlertsOutput is a bounded active-alert result.
type ListActiveAlertsOutput struct {
	Target     string        `json:"target"`
	ObservedAt time.Time     `json:"observedAt"`
	Alerts     []ActiveAlert `json:"alerts"`
	Truncated  bool          `json:"truncated"`
}

// GetScrapeHealthInput selects one monitoring target.
type GetScrapeHealthInput struct {
	Target string `json:"target" jsonschema:"opaque configured target identifier"`
}

// ScrapeState is the normalized state of one configured Prometheus up series.
type ScrapeState string

const (
	// ScrapeStateUp means the last fixed up sample was one.
	ScrapeStateUp ScrapeState = "up"
	// ScrapeStateDown means the last fixed up sample was zero.
	ScrapeStateDown ScrapeState = "down"
	// ScrapeStateMissing means the configured identity had no exact series.
	ScrapeStateMissing ScrapeState = "missing"
)

// ScrapeHealth contains only an opaque configured identity and normalized
// fixed-query state.
type ScrapeHealth struct {
	ID        string      `json:"id"`
	State     ScrapeState `json:"state"`
	SampledAt *time.Time  `json:"sampledAt,omitempty"`
}

// GetScrapeHealthOutput is the bounded fixed-query scrape-health result.
type GetScrapeHealthOutput struct {
	Target     string         `json:"target"`
	ObservedAt time.Time      `json:"observedAt"`
	Scrapes    []ScrapeHealth `json:"scrapes"`
	Partial    bool           `json:"partial"`
}

// ListUnhealthyReconciliationsInput selects one Flux target, optional scope,
// and bounded result limit.
type ListUnhealthyReconciliationsInput struct {
	Target string `json:"target" jsonschema:"opaque configured target identifier"`
	Scope  string `json:"scope,omitempty" jsonschema:"optional configured scope identifier"`
	Limit  int    `json:"limit,omitempty" jsonschema:"maximum number of reconciliations, from 1 to 50"`
}

// ReconciliationKind is a supported Flux resource kind.
type ReconciliationKind string

const (
	// ReconciliationKindKustomization identifies a Flux Kustomization.
	ReconciliationKindKustomization ReconciliationKind = "Kustomization"
	// ReconciliationKindHelmRelease identifies a Flux HelmRelease.
	ReconciliationKindHelmRelease ReconciliationKind = "HelmRelease"
	// ReconciliationKindGitRepository identifies a Flux GitRepository.
	ReconciliationKindGitRepository ReconciliationKind = "GitRepository"
)

// ReadinessState is the normalized non-ready condition state.
type ReadinessState string

const (
	// ReadinessStateFalse means Flux explicitly reported Ready=False.
	ReadinessStateFalse ReadinessState = "false"
	// ReadinessStateUnknown means Ready was absent or not a recognized value.
	ReadinessStateUnknown ReadinessState = "unknown"
)

// ReconciliationReason is a bounded normalization of a Flux condition reason.
type ReconciliationReason string

const (
	ReconciliationReasonDependencyNotReady   ReconciliationReason = "dependency-not-ready"
	ReconciliationReasonHealthCheckFailed    ReconciliationReason = "health-check-failed"
	ReconciliationReasonReconciliationFailed ReconciliationReason = "reconciliation-failed"
	ReconciliationReasonArtifactFailed       ReconciliationReason = "artifact-failed"
	ReconciliationReasonAuthenticationFailed ReconciliationReason = "authentication-failed"
	ReconciliationReasonSourceUnavailable    ReconciliationReason = "source-unavailable"
	ReconciliationReasonSuspended            ReconciliationReason = "suspended"
	ReconciliationReasonUnknown              ReconciliationReason = "unknown"
)

// UnhealthyReconciliation is the safe subset of one non-ready Flux object.
type UnhealthyReconciliation struct {
	Scope          string               `json:"scope"`
	Kind           ReconciliationKind   `json:"kind"`
	Name           string               `json:"name"`
	State          ReadinessState       `json:"state"`
	Reason         ReconciliationReason `json:"reason"`
	TransitionedAt *time.Time           `json:"transitionedAt,omitempty"`
}

// ListUnhealthyReconciliationsOutput is a bounded Flux readiness result.
type ListUnhealthyReconciliationsOutput struct {
	Target          string                    `json:"target"`
	ObservedAt      time.Time                 `json:"observedAt"`
	Reconciliations []UnhealthyReconciliation `json:"reconciliations"`
	Truncated       bool                      `json:"truncated"`
}
