// Package model defines database models used by the store layer.
package model

import (
	"time"
)

// HealthStatus represents the health status of a provider
type HealthStatus string

const (
	// HealthStatusReady indicates the provider is healthy and ready to serve requests
	HealthStatusReady HealthStatus = "ready"
	// HealthStatusNotReady indicates the provider is not healthy or unreachable
	HealthStatusNotReady HealthStatus = "not_ready"
)

func (h HealthStatus) StringPtr() *string {
	s := string(h)
	return &s
}

type Provider struct {
	ID             string         `gorm:"primaryKey;type:varchar(63)"`
	Name           string         `gorm:"uniqueIndex;not null"`
	ServiceType    string         `gorm:"column:service_type;not null"`
	SchemaVersion  string         `gorm:"column:schema_version;not null"`
	Endpoint       string         `gorm:"column:endpoint;not null"`
	DisplayName *string `gorm:"column:display_name"`
	Operations  []string `gorm:"column:operations;serializer:json"`
	Metadata    map[string]interface{} `gorm:"column:metadata;serializer:json"`
	CreateTime     time.Time      `gorm:"column:create_time;autoCreateTime"`
	UpdateTime     time.Time      `gorm:"column:update_time;autoUpdateTime"`

	// Health check fields
	HealthStatus        HealthStatus `gorm:"column:health_status;default:ready"`
	ConsecutiveFailures int          `gorm:"column:consecutive_failures;default:0"`
	NextHealthCheck     *time.Time   `gorm:"column:next_health_check"`
}

type ProviderList []Provider
