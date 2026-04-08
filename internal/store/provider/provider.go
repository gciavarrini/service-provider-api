// Package provider provides database access for provider operations.
package provider

import (
	"context"
	"errors"
	"time"

	"github.com/dcm-project/service-provider-manager/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrProviderNotFound  = errors.New("provider not found")
	ErrProviderNameTaken = errors.New("provider name already taken")
)

// ProviderFilter contains optional fields for filtering provider queries.
// nil fields are ignored (not filtered).
type ProviderFilter struct {
	Name        *string
	ServiceType *string
}

// Pagination contains options for paginated queries.
type Pagination struct {
	Limit  int
	Offset int
}

type Provider interface {
	List(ctx context.Context, filter *ProviderFilter, pagination *Pagination) (model.ProviderList, error)
	Count(ctx context.Context, filter *ProviderFilter) (int64, error)
	Create(ctx context.Context, provider model.Provider) (*model.Provider, error)
	Delete(ctx context.Context, id string) error
	Update(ctx context.Context, provider model.Provider) (*model.Provider, error)
	Get(ctx context.Context, id string) (*model.Provider, error)
	GetByName(ctx context.Context, name string) (*model.Provider, error)
	ExistsByID(ctx context.Context, id string) (bool, error)

	// Health check methods
	ListProvidersForHealthCheck(ctx context.Context, now time.Time) (model.ProviderList, error)
	UpdateHealthStatus(ctx context.Context, id string, status model.HealthStatus, consecutiveFailures int, nextCheck time.Time) error
}

type ProviderStore struct {
	db *gorm.DB
}

var _ Provider = (*ProviderStore)(nil)

func NewProvider(db *gorm.DB) Provider {
	return &ProviderStore{db: db}
}

func (s *ProviderStore) List(ctx context.Context, filter *ProviderFilter, pagination *Pagination) (model.ProviderList, error) {
	var providers model.ProviderList
	query := s.db.WithContext(ctx)

	if filter != nil {
		if filter.Name != nil {
			query = query.Where(&model.Provider{Name: *filter.Name})
		}
		if filter.ServiceType != nil {
			query = query.Where(&model.Provider{ServiceType: *filter.ServiceType})
		}
	}

	// Apply consistent ordering for pagination
	query = query.Order("create_time ASC, id ASC")

	if pagination != nil {
		query = query.Limit(pagination.Limit).Offset(pagination.Offset)
	}

	if err := query.Find(&providers).Error; err != nil {
		return nil, err
	}
	return providers, nil
}

func (s *ProviderStore) Count(ctx context.Context, filter *ProviderFilter) (int64, error) {
	var count int64
	query := s.db.WithContext(ctx).Model(&model.Provider{})

	if filter != nil {
		if filter.Name != nil {
			query = query.Where(&model.Provider{Name: *filter.Name})
		}
		if filter.ServiceType != nil {
			query = query.Where(&model.Provider{ServiceType: *filter.ServiceType})
		}
	}

	if err := query.Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func (s *ProviderStore) Create(ctx context.Context, provider model.Provider) (*model.Provider, error) {
	if err := s.db.WithContext(ctx).Clauses(clause.Returning{}).Create(&provider).Error; err != nil {
		return nil, err
	}
	return &provider, nil
}

func (s *ProviderStore) Delete(ctx context.Context, id string) error {
	result := s.db.WithContext(ctx).Where("id = ?", id).Delete(&model.Provider{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrProviderNotFound
	}
	return nil
}

func (s *ProviderStore) Update(ctx context.Context, provider model.Provider) (*model.Provider, error) {
	// Select explicit columns so nullable JSON and *string fields can be cleared (not skipped as zero).
	result := s.db.WithContext(ctx).Model(&provider).Clauses(clause.Returning{}).
		Select(
			"Name", "ServiceType", "SchemaVersion", "Endpoint",
			"DisplayName", "Operations", "Metadata", "UpdateTime",
		).Updates(&provider)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, ErrProviderNotFound
	}
	return &provider, nil
}

func (s *ProviderStore) Get(ctx context.Context, id string) (*model.Provider, error) {
	var provider model.Provider
	if err := s.db.WithContext(ctx).Where("id = ?", id).First(&provider).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrProviderNotFound
		}
		return nil, err
	}
	return &provider, nil
}

func (s *ProviderStore) GetByName(ctx context.Context, name string) (*model.Provider, error) {
	var provider model.Provider
	if err := s.db.WithContext(ctx).Where(&model.Provider{Name: name}).First(&provider).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrProviderNotFound
		}
		return nil, err
	}
	return &provider, nil
}

func (s *ProviderStore) ExistsByID(ctx context.Context, id string) (bool, error) {
	var provider model.Provider
	err := s.db.WithContext(ctx).Select("id").Where(&model.Provider{ID: id}).Take(&provider).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ListProvidersForHealthCheck returns providers that are due for a health check.
func (s *ProviderStore) ListProvidersForHealthCheck(ctx context.Context, now time.Time) (model.ProviderList, error) {
	var providers model.ProviderList
	if err := s.db.WithContext(ctx).
		Where("next_health_check IS NULL OR next_health_check <= ?", now).
		Find(&providers).Error; err != nil {
		return nil, err
	}
	return providers, nil
}

// UpdateHealthStatus updates the health status and tracking fields for a provider.
func (s *ProviderStore) UpdateHealthStatus(ctx context.Context, id string, status model.HealthStatus, consecutiveFailures int, nextCheck time.Time) error {
	result := s.db.WithContext(ctx).Model(&model.Provider{}).Where("id = ?", id).Updates(map[string]interface{}{
		"health_status":        status,
		"consecutive_failures": consecutiveFailures,
		"next_health_check":    nextCheck,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrProviderNotFound
	}
	return nil
}
