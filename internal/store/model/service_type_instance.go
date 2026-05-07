package model

import (
	"time"
)

type ServiceTypeInstance struct {
	ID            string         `gorm:"primaryKey;type:varchar(63)"`
	ProviderName  string         `gorm:"column:provider_name;not null"`
	ServiceType   string         `gorm:"column:service_type;not null;default:'';index"`
	Status        string         `gorm:"column:status;not null"`
	StatusMessage string         `gorm:"column:status_message"`
	InstanceName  string         `gorm:"column:instance_name;not null"`
	Spec          map[string]any `gorm:"column:spec;type:jsonb;serializer:json;not null"`
	CreateTime    time.Time      `gorm:"column:create_time;autoCreateTime"`
	UpdateTime    time.Time      `gorm:"column:update_time;autoUpdateTime"`

	// Soft-delete fields for deferred deletion (rehydration flow)
	DeletionStatus      *string    `gorm:"column:deletion_status"`
	RetryCount          int        `gorm:"column:retry_count;default:0"`
	LastDeletionAttempt *time.Time `gorm:"column:last_deletion_attempt"`
	DeletionRequestedAt *time.Time `gorm:"column:deletion_requested_at"`
}

type ServiceTypeInstanceList []ServiceTypeInstance
