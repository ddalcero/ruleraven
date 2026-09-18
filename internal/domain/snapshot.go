package domain

import "time"

// Snapshot is the bounded, allowlisted representation used by rules and providers.
type Snapshot struct {
	Source      Source            `json:"source"`
	ObservedAt  time.Time         `json:"observedAt"`
	Labels      map[string]string `json:"labels,omitempty"`
	Conditions  []Condition       `json:"conditions,omitempty"`
	Containers  []ContainerState  `json:"containers,omitempty"`
	Workload    *WorkloadState    `json:"workload,omitempty"`
	Job         *JobState         `json:"job,omitempty"`
	Event       *EventState       `json:"event,omitempty"`
	ContentHash string            `json:"contentHash,omitempty"`
}

type Condition struct {
	Type               string    `json:"type"`
	Status             string    `json:"status"`
	Reason             string    `json:"reason,omitempty"`
	Message            string    `json:"message,omitempty"`
	LastTransitionTime time.Time `json:"lastTransitionTime,omitempty"`
}

type ContainerState struct {
	Name         string `json:"name"`
	Ready        bool   `json:"ready"`
	RestartCount int32  `json:"restartCount"`
	State        string `json:"state"`
	Reason       string `json:"reason,omitempty"`
	ExitCode     int32  `json:"exitCode,omitempty"`
}

type WorkloadState struct {
	Desired     int32 `json:"desired"`
	Current     int32 `json:"current"`
	Ready       int32 `json:"ready"`
	Available   int32 `json:"available"`
	Updated     int32 `json:"updated"`
	Unavailable int32 `json:"unavailable"`
}

type JobState struct {
	Active       int32 `json:"active"`
	Succeeded    int32 `json:"succeeded"`
	Failed       int32 `json:"failed"`
	BackoffLimit int32 `json:"backoffLimit"`
}

type EventState struct {
	Type         string    `json:"type"`
	Reason       string    `json:"reason"`
	Message      string    `json:"message,omitempty"`
	RegardingUID string    `json:"regardingUid,omitempty"`
	Count        int32     `json:"count"`
	LastObserved time.Time `json:"lastObserved,omitempty"`
}
