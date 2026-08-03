package flux

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/neodymium6/cluster-observer-mcp/internal/observer"
	"github.com/neodymium6/cluster-observer-mcp/internal/sourcehttp"
)

const resourceListLimit = "500"

type resourceKind struct {
	group    string
	version  string
	resource string
	kind     observer.ReconciliationKind
}

var reconciliationResources = []resourceKind{
	{
		group:    "kustomize.toolkit.fluxcd.io",
		version:  "v1",
		resource: "kustomizations",
		kind:     observer.ReconciliationKindKustomization,
	},
	{
		group:    "helm.toolkit.fluxcd.io",
		version:  "v2",
		resource: "helmreleases",
		kind:     observer.ReconciliationKindHelmRelease,
	},
	{
		group:    "source.toolkit.fluxcd.io",
		version:  "v1",
		resource: "gitrepositories",
		kind:     observer.ReconciliationKindGitRepository,
	},
}

type resourceList struct {
	Metadata struct {
		Continue string `json:"continue"`
	} `json:"metadata"`
	Items []resourceObject `json:"items"`
}

type resourceObject struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Status struct {
		Conditions []condition `json:"conditions"`
	} `json:"status"`
}

type condition struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	Reason             string `json:"reason"`
	LastTransitionTime string `json:"lastTransitionTime"`
}

// ListUnhealthyReconciliations returns only normalized non-ready Flux objects
// from configured namespaces.
func (c *Client) ListUnhealthyReconciliations(
	ctx context.Context,
	input observer.ListUnhealthyReconciliationsInput,
) (observer.ListUnhealthyReconciliationsOutput, error) {
	input, err := input.Normalize()
	if err != nil {
		return observer.ListUnhealthyReconciliationsOutput{}, err
	}
	if input.Target != c.targetID {
		return observer.ListUnhealthyReconciliationsOutput{}, errors.New("Flux target is not configured")
	}
	scopes, err := c.selectedScopes(input.Scope)
	if err != nil {
		return observer.ListUnhealthyReconciliationsOutput{}, err
	}

	result := observer.ListUnhealthyReconciliationsOutput{
		Target:          c.targetID,
		ObservedAt:      c.now().UTC(),
		Reconciliations: []observer.UnhealthyReconciliation{},
	}
	for _, scope := range scopes {
		for _, resource := range reconciliationResources {
			list, err := c.listResources(ctx, scope, resource)
			if err != nil {
				return observer.ListUnhealthyReconciliationsOutput{}, err
			}
			result.Truncated = result.Truncated || list.Metadata.Continue != ""
			for _, item := range list.Items {
				ready, state, reason, transitionedAt := reconciliationHealth(item)
				if ready {
					continue
				}
				if !validObjectName(item.Metadata.Name) {
					result.Truncated = true
					continue
				}
				result.Reconciliations = append(result.Reconciliations, observer.UnhealthyReconciliation{
					Scope:          scope.ID,
					Kind:           resource.kind,
					Name:           item.Metadata.Name,
					State:          state,
					Reason:         reason,
					TransitionedAt: transitionedAt,
				})
			}
		}
	}

	slices.SortFunc(result.Reconciliations, func(a, b observer.UnhealthyReconciliation) int {
		return strings.Compare(
			a.Scope+"\x00"+string(a.Kind)+"\x00"+a.Name,
			b.Scope+"\x00"+string(b.Kind)+"\x00"+b.Name,
		)
	})
	if len(result.Reconciliations) > input.Limit {
		result.Reconciliations = result.Reconciliations[:input.Limit]
		result.Truncated = true
	}
	if err := observer.CheckResultSize(result); err != nil {
		return observer.ListUnhealthyReconciliationsOutput{}, err
	}
	return result, nil
}

func (c *Client) listResources(
	ctx context.Context,
	scope Scope,
	resource resourceKind,
) (resourceList, error) {
	path := "/apis/" + resource.group + "/" + resource.version + "/namespaces/" +
		url.PathEscape(scope.Namespace) + "/" + resource.resource
	body, err := c.http.Get(ctx, path, url.Values{"limit": {resourceListLimit}})
	if err != nil {
		return resourceList{}, err
	}
	var result resourceList
	if err := json.Unmarshal(body, &result); err != nil {
		return resourceList{}, sourcehttp.ErrInvalidSourceResponse
	}
	return result, nil
}

func reconciliationHealth(
	item resourceObject,
) (bool, observer.ReadinessState, observer.ReconciliationReason, *time.Time) {
	for _, condition := range item.Status.Conditions {
		if condition.Type != "Ready" {
			continue
		}
		if condition.Status == "True" {
			return true, "", "", nil
		}
		state := observer.ReadinessStateUnknown
		if condition.Status == "False" {
			state = observer.ReadinessStateFalse
		}
		var transitionedAt *time.Time
		if parsed, err := time.Parse(time.RFC3339Nano, condition.LastTransitionTime); err == nil {
			parsed = parsed.UTC()
			transitionedAt = &parsed
		}
		return false, state, observer.NormalizeReconciliationReason(condition.Reason), transitionedAt
	}
	return false, observer.ReadinessStateUnknown, observer.ReconciliationReasonUnknown, nil
}

func validObjectName(name string) bool {
	if len(name) == 0 || len(name) > 253 {
		return false
	}
	for _, part := range strings.Split(name, ".") {
		if !namespacePattern.MatchString(part) {
			return false
		}
	}
	return true
}
