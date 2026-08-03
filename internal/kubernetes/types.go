package kubernetes

type listMetadata struct {
	Continue string `json:"continue"`
}

type objectMetadata struct {
	Name string `json:"name"`
}

type condition struct {
	Type   string `json:"type"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type nodeList struct {
	Metadata listMetadata `json:"metadata"`
	Items    []node       `json:"items"`
}

type node struct {
	Status struct {
		Conditions []condition `json:"conditions"`
	} `json:"status"`
}

type controllerList struct {
	Metadata listMetadata `json:"metadata"`
	Items    []controller `json:"items"`
}

type controller struct {
	Metadata objectMetadata `json:"metadata"`
	Spec     struct {
		Replicas *int `json:"replicas"`
	} `json:"spec"`
	Status struct {
		ReadyReplicas          int         `json:"readyReplicas"`
		DesiredNumberScheduled int         `json:"desiredNumberScheduled"`
		NumberReady            int         `json:"numberReady"`
		Conditions             []condition `json:"conditions"`
	} `json:"status"`
}
