package observer

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var identifierPattern = regexp.MustCompile(`^[a-z](?:[a-z0-9-]{0,30}[a-z0-9])?$`)

// ErrResultTooLarge is returned instead of an oversized observation.
var ErrResultTooLarge = errors.New("observation exceeds the encoded size limit")

// ValidationError describes an invalid public input without echoing its value.
type ValidationError struct {
	Field  string
	Reason string
}

// Error implements error.
func (e *ValidationError) Error() string {
	return fmt.Sprintf("invalid %s: %s", e.Field, e.Reason)
}

// ValidateIdentifier checks an opaque target or scope identifier.
func ValidateIdentifier(field, value string) error {
	if !identifierPattern.MatchString(value) {
		return &ValidationError{
			Field:  field,
			Reason: "must be 1 to 32 lowercase letters, digits, or hyphens",
		}
	}

	return nil
}

// Validate checks cluster health input at the application boundary.
func (in GetClusterHealthInput) Validate() error {
	return ValidateIdentifier("target", in.Target)
}

// Normalize validates workload input and applies its bounded default limit.
func (in ListUnhealthyWorkloadsInput) Normalize() (ListUnhealthyWorkloadsInput, error) {
	if err := ValidateIdentifier("target", in.Target); err != nil {
		return ListUnhealthyWorkloadsInput{}, err
	}
	if in.Scope != "" {
		if err := ValidateIdentifier("scope", in.Scope); err != nil {
			return ListUnhealthyWorkloadsInput{}, err
		}
	}

	if in.Limit == 0 {
		in.Limit = DefaultListLimit
	}
	if in.Limit < 1 || in.Limit > MaxListLimit {
		return ListUnhealthyWorkloadsInput{}, &ValidationError{
			Field:  "limit",
			Reason: "must be from 1 to 50",
		}
	}

	return in, nil
}

// Normalize validates active-alert input and applies its bounded default.
func (in ListActiveAlertsInput) Normalize() (ListActiveAlertsInput, error) {
	if err := ValidateIdentifier("target", in.Target); err != nil {
		return ListActiveAlertsInput{}, err
	}
	limit, err := normalizeListLimit(in.Limit)
	if err != nil {
		return ListActiveAlertsInput{}, err
	}
	in.Limit = limit
	return in, nil
}

// Validate checks scrape-health input at the application boundary.
func (in GetScrapeHealthInput) Validate() error {
	return ValidateIdentifier("target", in.Target)
}

// Normalize validates Flux input and applies its bounded default.
func (in ListUnhealthyReconciliationsInput) Normalize() (ListUnhealthyReconciliationsInput, error) {
	if err := ValidateIdentifier("target", in.Target); err != nil {
		return ListUnhealthyReconciliationsInput{}, err
	}
	if in.Scope != "" {
		if err := ValidateIdentifier("scope", in.Scope); err != nil {
			return ListUnhealthyReconciliationsInput{}, err
		}
	}
	limit, err := normalizeListLimit(in.Limit)
	if err != nil {
		return ListUnhealthyReconciliationsInput{}, err
	}
	in.Limit = limit
	return in, nil
}

func normalizeListLimit(limit int) (int, error) {
	if limit == 0 {
		return DefaultListLimit, nil
	}
	if limit < 1 || limit > MaxListLimit {
		return 0, &ValidationError{Field: "limit", Reason: "must be from 1 to 50"}
	}
	return limit, nil
}

// NormalizeAlertSeverity maps source severities to a small public allowlist.
func NormalizeAlertSeverity(severity string) AlertSeverity {
	switch strings.ToLower(severity) {
	case "critical":
		return AlertSeverityCritical
	case "warning", "warn":
		return AlertSeverityWarning
	case "info", "informational":
		return AlertSeverityInfo
	default:
		return AlertSeverityUnknown
	}
}

// NormalizeReconciliationReason maps Flux reasons to a small public allowlist.
func NormalizeReconciliationReason(reason string) ReconciliationReason {
	switch reason {
	case "DependencyNotReady":
		return ReconciliationReasonDependencyNotReady
	case "HealthCheckFailed":
		return ReconciliationReasonHealthCheckFailed
	case "ReconciliationFailed", "BuildFailed":
		return ReconciliationReasonReconciliationFailed
	case "ArtifactFailed":
		return ReconciliationReasonArtifactFailed
	case "AuthenticationFailed":
		return ReconciliationReasonAuthenticationFailed
	case "SourceUnavailable", "GitOperationFailed":
		return ReconciliationReasonSourceUnavailable
	case "Suspended":
		return ReconciliationReasonSuspended
	default:
		return ReconciliationReasonUnknown
	}
}

// NormalizeWorkloadReason maps source reasons to a small public allowlist.
func NormalizeWorkloadReason(reason string) WorkloadReason {
	switch reason {
	case "NotReady", "MinimumReplicasUnavailable":
		return WorkloadReasonNotReady
	case "ReplicaFailure", "FailedCreate":
		return WorkloadReasonReplicaFailure
	case "ProgressDeadlineExceeded":
		return WorkloadReasonProgressDeadline
	case "Unschedulable":
		return WorkloadReasonUnschedulable
	default:
		return WorkloadReasonUnknown
	}
}

// CheckResultSize rejects an observation whose encoded structured content is
// larger than the public result limit.
func CheckResultSize(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode observation: %w", err)
	}
	if len(encoded) > MaxEncodedResultBytes {
		return ErrResultTooLarge
	}

	return nil
}
