package domain

import (
	"encoding/json"
	"time"
)

type NotificationStatus string

const (
	NotificationPending    NotificationStatus = "pending"
	NotificationDelivering NotificationStatus = "delivering"
	NotificationDelivered  NotificationStatus = "delivered"
	NotificationFailed     NotificationStatus = "failed"
)

type Notification struct {
	ID            string             `json:"id"`
	EvaluationID  string             `json:"evaluationId"`
	DestinationID string             `json:"destinationId"`
	EventType     string             `json:"eventType"`
	Status        NotificationStatus `json:"status"`
	Attempts      int                `json:"attempts"`
	NextAttemptAt time.Time          `json:"nextAttemptAt"`
	Payload       json.RawMessage    `json:"payload,omitempty"`
}
