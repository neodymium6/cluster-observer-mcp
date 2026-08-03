package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/neodymium6/cluster-observer-mcp/internal/observer"
)

type resourceKind struct {
	name string
	kind observer.WorkloadKind
}

var controllerResources = []resourceKind{
	{name: "deployments", kind: observer.WorkloadKindDeployment},
	{name: "statefulsets", kind: observer.WorkloadKindStatefulSet},
	{name: "daemonsets", kind: observer.WorkloadKindDaemonSet},
}

// ClusterHealth returns bounded readiness totals from fixed API resources.
func (c *Client) ClusterHealth(ctx context.Context) (observer.ClusterHealthOutput, error) {
	result := observer.ClusterHealthOutput{
		Target:     c.targetID,
		ObservedAt: c.now().UTC(),
		Status:     observer.HealthStatusHealthy,
		Warnings:   []observer.HealthWarning{},
	}

	nodes, partial, err := c.listNodes(ctx)
	if err != nil {
		return observer.ClusterHealthOutput{}, err
	}
	result.Nodes.Total = len(nodes.Items)
	for _, item := range nodes.Items {
		if nodeReady(item) {
			result.Nodes.Ready++
		}
	}
	if result.Nodes.Ready != result.Nodes.Total {
		result.Status = observer.HealthStatusDegraded
		result.Warnings = append(result.Warnings, observer.HealthWarning{
			Code:  observer.WarningNodesNotReady,
			Count: result.Nodes.Total - result.Nodes.Ready,
		})
	}

	scopes, err := c.selectedScopes("")
	if err != nil {
		return observer.ClusterHealthOutput{}, err
	}
	for _, scope := range scopes {
		for _, resource := range controllerResources {
			controllers, listPartial, listErr := c.listControllers(ctx, scope, resource)
			if listErr != nil {
				return observer.ClusterHealthOutput{}, listErr
			}
			partial = partial || listPartial
			for _, item := range controllers.Items {
				result.Workloads.Total++
				ready, _, _, _ := controllerHealth(item, resource.kind)
				if ready {
					result.Workloads.Ready++
				} else {
					result.Workloads.Unhealthy++
				}
			}
		}
	}

	if result.Workloads.Unhealthy > 0 {
		result.Status = observer.HealthStatusDegraded
		result.Warnings = append(result.Warnings, observer.HealthWarning{
			Code:  observer.WarningWorkloadsNotReady,
			Count: result.Workloads.Unhealthy,
		})
	}
	if partial {
		result.Partial = true
		result.Status = observer.HealthStatusUnknown
		result.Warnings = append(result.Warnings, observer.HealthWarning{
			Code:  observer.WarningPartialObservation,
			Count: 1,
		})
	}

	if err := observer.CheckResultSize(result); err != nil {
		return observer.ClusterHealthOutput{}, err
	}
	return result, nil
}

// ListUnhealthyWorkloads returns only normalized unhealthy controllers from
// configured namespaces.
func (c *Client) ListUnhealthyWorkloads(
	ctx context.Context,
	input observer.ListUnhealthyWorkloadsInput,
) (observer.ListUnhealthyWorkloadsOutput, error) {
	input, err := input.Normalize()
	if err != nil {
		return observer.ListUnhealthyWorkloadsOutput{}, err
	}
	if input.Target != c.targetID {
		return observer.ListUnhealthyWorkloadsOutput{}, errors.New("Kubernetes target is not configured")
	}

	scopes, err := c.selectedScopes(input.Scope)
	if err != nil {
		return observer.ListUnhealthyWorkloadsOutput{}, err
	}
	result := observer.ListUnhealthyWorkloadsOutput{
		Target:     c.targetID,
		ObservedAt: c.now().UTC(),
		Workloads:  []observer.WorkloadHealth{},
	}

	for _, scope := range scopes {
		for _, resource := range controllerResources {
			controllers, partial, listErr := c.listControllers(ctx, scope, resource)
			if listErr != nil {
				return observer.ListUnhealthyWorkloadsOutput{}, listErr
			}
			result.Truncated = result.Truncated || partial
			for _, item := range controllers.Items {
				ready, readyCount, desired, reasons := controllerHealth(item, resource.kind)
				if ready {
					continue
				}
				if !validObjectName(item.Metadata.Name) {
					result.Truncated = true
					continue
				}
				result.Workloads = append(result.Workloads, observer.WorkloadHealth{
					Scope:   scope.ID,
					Kind:    resource.kind,
					Name:    item.Metadata.Name,
					Ready:   readyCount,
					Desired: desired,
					Reasons: reasons,
				})
			}
		}
	}

	slices.SortFunc(result.Workloads, func(a, b observer.WorkloadHealth) int {
		return strings.Compare(
			a.Scope+"\x00"+string(a.Kind)+"\x00"+a.Name,
			b.Scope+"\x00"+string(b.Kind)+"\x00"+b.Name,
		)
	})
	if len(result.Workloads) > input.Limit {
		result.Workloads = result.Workloads[:input.Limit]
		result.Truncated = true
	}

	if err := observer.CheckResultSize(result); err != nil {
		return observer.ListUnhealthyWorkloadsOutput{}, err
	}
	return result, nil
}

func (c *Client) listNodes(ctx context.Context) (nodeList, bool, error) {
	body, err := c.get(ctx, "/api/v1/nodes")
	if err != nil {
		return nodeList{}, false, fmt.Errorf("observe nodes: %w", err)
	}

	var result nodeList
	if err := json.Unmarshal(body, &result); err != nil {
		return nodeList{}, false, ErrInvalidSourceResponse
	}
	return result, result.Metadata.Continue != "", nil
}

func (c *Client) listControllers(
	ctx context.Context,
	scope Scope,
	resource resourceKind,
) (controllerList, bool, error) {
	path := "/apis/apps/v1/namespaces/" + url.PathEscape(scope.Namespace) + "/" + resource.name
	body, err := c.get(ctx, path)
	if err != nil {
		return controllerList{}, false, fmt.Errorf("observe %s: %w", resource.name, err)
	}

	var result controllerList
	if err := json.Unmarshal(body, &result); err != nil {
		return controllerList{}, false, ErrInvalidSourceResponse
	}
	return result, result.Metadata.Continue != "", nil
}

func nodeReady(item node) bool {
	for _, condition := range item.Status.Conditions {
		if condition.Type == "Ready" {
			return condition.Status == "True"
		}
	}
	return false
}

func controllerHealth(
	item controller,
	kind observer.WorkloadKind,
) (bool, int, int, []observer.WorkloadReason) {
	desired := 1
	ready := item.Status.ReadyReplicas
	if item.Spec.Replicas != nil {
		desired = *item.Spec.Replicas
	}
	if kind == observer.WorkloadKindDaemonSet {
		desired = item.Status.DesiredNumberScheduled
		ready = item.Status.NumberReady
	}
	if ready >= desired {
		return true, ready, desired, []observer.WorkloadReason{}
	}

	reasons := make([]observer.WorkloadReason, 0, len(item.Status.Conditions))
	seen := make(map[observer.WorkloadReason]struct{})
	for _, condition := range item.Status.Conditions {
		if condition.Reason == "" {
			continue
		}
		reason := observer.NormalizeWorkloadReason(condition.Reason)
		if _, exists := seen[reason]; exists {
			continue
		}
		seen[reason] = struct{}{}
		reasons = append(reasons, reason)
	}
	if len(reasons) == 0 {
		reasons = append(reasons, observer.WorkloadReasonNotReady)
	}
	slices.Sort(reasons)
	return false, ready, desired, reasons
}

func validObjectName(name string) bool {
	if len(name) == 0 || len(name) > 253 {
		return false
	}
	parts := strings.Split(name, ".")
	for _, part := range parts {
		if !namespacePattern.MatchString(part) {
			return false
		}
	}
	return true
}
