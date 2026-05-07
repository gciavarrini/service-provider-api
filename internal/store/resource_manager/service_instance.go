// Package store provides database access for service type instance operations.
package store

import (
	"context"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/dcm-project/service-provider-manager/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrInstanceNotFound = errors.New("service type instance not found")

// ServiceTypeInstanceListOptions contains optional fields for listing instances.
type ServiceTypeInstanceListOptions struct {
	ProviderName *string
	ServiceType  *string
	ShowDeleted  bool
	PageSize     int
	PageToken    *string
}

// ServiceTypeInstanceListResult contains the result of a List operation.
type ServiceTypeInstanceListResult struct {
	Instances     model.ServiceTypeInstanceList
	NextPageToken *string
}

type ServiceTypeInstance interface { //nolint:interfacebloat
	List(ctx context.Context, opts *ServiceTypeInstanceListOptions) (*ServiceTypeInstanceListResult, error)
	Create(ctx context.Context, instance model.ServiceTypeInstance) (*model.ServiceTypeInstance, error)
	Get(ctx context.Context, id string, showDeleted bool) (*model.ServiceTypeInstance, error)
	ExistsByID(ctx context.Context, id string) (bool, error)
	UpdateStatus(ctx context.Context, instanceID string, status string, statusMessage string) error
	MarkForDeletion(ctx context.Context, id string) error
	ListPendingDeletions(ctx context.Context) ([]model.ServiceTypeInstance, error)
	IncrementDeletionRetry(ctx context.Context, id string) error
	MarkDeletionFailed(ctx context.Context, id string) error
	HardDelete(ctx context.Context, id string) error
	ResetRetryCount(ctx context.Context, id string) error
	MarkProviderDeletionsPendingProvider(ctx context.Context, providerName string) error
	ReactivateProviderDeletions(ctx context.Context, providerName string) error
	MarkPendingProviderIfNotReady(ctx context.Context, instanceID string) (bool, error)
}

type ServiceTypeInstanceStore struct {
	db        *gorm.DB
	retryOpts []backoff.RetryOption
}

var _ ServiceTypeInstance = (*ServiceTypeInstanceStore)(nil)

// NewServiceTypeInstance constructs the store. If no retry options are passed,
// production backoff is used for Create and HardDelete. Tests may pass custom
// options (e.g. sub-second intervals) to avoid slow retry exhaustion.
func NewServiceTypeInstance(db *gorm.DB, retryOpts ...backoff.RetryOption) ServiceTypeInstance {
	var opts []backoff.RetryOption
	if len(retryOpts) == 0 {
		opts = getRetryOptions()
	} else {
		opts = append([]backoff.RetryOption(nil), retryOpts...)
	}
	return &ServiceTypeInstanceStore{db: db, retryOpts: opts}
}

func (s *ServiceTypeInstanceStore) List(ctx context.Context, opts *ServiceTypeInstanceListOptions) (*ServiceTypeInstanceListResult, error) {
	var instances model.ServiceTypeInstanceList
	query := s.db.WithContext(ctx)

	// Default page size
	pageSize := 50
	if opts != nil && opts.PageSize > 0 {
		pageSize = opts.PageSize
	}

	// Decode page token to get offset
	offset := 0
	if opts != nil && opts.PageToken != nil && *opts.PageToken != "" {
		decoded, err := base64.StdEncoding.DecodeString(*opts.PageToken)
		if err == nil {
			if parsedOffset, err := strconv.Atoi(string(decoded)); err == nil {
				offset = parsedOffset
			}
		}
	}

	// Apply filters
	if opts != nil && opts.ProviderName != nil && *opts.ProviderName != "" {
		query = query.Where("provider_name = ?", *opts.ProviderName)
	}

	if opts != nil && opts.ServiceType != nil && strings.TrimSpace(*opts.ServiceType) != "" {
		query = query.Where("service_type = ?", *opts.ServiceType)
	}

	// By default, exclude soft-deleted instances; show_deleted includes them
	if opts == nil || !opts.ShowDeleted {
		query = query.Where("deletion_status IS NULL")
	}

	// Apply consistent ordering for pagination
	query = query.Order("create_time ASC, id ASC")

	// Query with limit+1 to detect if there are more results
	query = query.Limit(pageSize + 1).Offset(offset)

	if err := query.Find(&instances).Error; err != nil {
		return nil, err
	}

	// Generate next page token if there are more results
	result := &ServiceTypeInstanceListResult{
		Instances: instances,
	}

	if len(instances) > pageSize {
		// Trim to requested page size
		result.Instances = instances[:pageSize]
		// Encode next offset as page token
		nextOffset := offset + pageSize
		encodedNextPageToken := base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(nextOffset)))
		result.NextPageToken = &encodedNextPageToken
	}

	return result, nil
}

func (s *ServiceTypeInstanceStore) Create(ctx context.Context, instance model.ServiceTypeInstance) (*model.ServiceTypeInstance, error) {
	operation := func() (*model.ServiceTypeInstance, error) {
		if err := s.db.WithContext(ctx).Clauses(clause.Returning{}).Create(&instance).Error; err != nil {
			return nil, err
		}
		return &instance, nil
	}

	return backoff.Retry(ctx, operation, s.retryOpts...)
}

func (s *ServiceTypeInstanceStore) Get(ctx context.Context, id string, showDeleted bool) (*model.ServiceTypeInstance, error) {
	var instance model.ServiceTypeInstance
	query := s.db.WithContext(ctx).Where("id = ?", id)
	if !showDeleted {
		query = query.Where("deletion_status IS NULL")
	}
	if err := query.First(&instance).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInstanceNotFound
		}
		return nil, err
	}
	return &instance, nil
}

func (s *ServiceTypeInstanceStore) UpdateStatus(ctx context.Context, instanceID string, status string, statusMessage string) error {
	result := s.db.WithContext(ctx).
		Model(&model.ServiceTypeInstance{}).
		Where("id = ?", instanceID).
		Updates(model.ServiceTypeInstance{
			Status:        status,
			StatusMessage: statusMessage,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrInstanceNotFound
	}
	return nil
}

func (s *ServiceTypeInstanceStore) ExistsByID(ctx context.Context, id string) (bool, error) {
	var instance model.ServiceTypeInstance
	err := s.db.WithContext(ctx).Select("id").Where("id = ?", id).Take(&instance).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

const (
	DeletionStatusScheduled       = "SCHEDULED"
	DeletionStatusFailed          = "FAILED"
	DeletionStatusPendingProvider = "PENDING_PROVIDER"
)

func (s *ServiceTypeInstanceStore) MarkForDeletion(ctx context.Context, id string) error {
	now := time.Now()
	result := s.db.WithContext(ctx).
		Model(&model.ServiceTypeInstance{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"deletion_status":       DeletionStatusScheduled,
			"deletion_requested_at": now,
			"retry_count":           0,
			"last_deletion_attempt": nil,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrInstanceNotFound
	}
	return nil
}

func (s *ServiceTypeInstanceStore) ListPendingDeletions(ctx context.Context) ([]model.ServiceTypeInstance, error) {
	var instances []model.ServiceTypeInstance
	if err := s.db.WithContext(ctx).
		Where("deletion_status = ?", DeletionStatusScheduled).
		Order("deletion_requested_at ASC").
		Find(&instances).Error; err != nil {
		return nil, err
	}
	return instances, nil
}

func (s *ServiceTypeInstanceStore) IncrementDeletionRetry(ctx context.Context, id string) error {
	now := time.Now()
	result := s.db.WithContext(ctx).
		Model(&model.ServiceTypeInstance{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"retry_count":           gorm.Expr("retry_count + 1"),
			"last_deletion_attempt": now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrInstanceNotFound
	}
	return nil
}

func (s *ServiceTypeInstanceStore) MarkDeletionFailed(ctx context.Context, id string) error {
	result := s.db.WithContext(ctx).
		Model(&model.ServiceTypeInstance{}).
		Where("id = ?", id).
		Update("deletion_status", DeletionStatusFailed)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrInstanceNotFound
	}
	return nil
}

func (s *ServiceTypeInstanceStore) HardDelete(ctx context.Context, id string) error {
	operation := func() (any, error) {
		result := s.db.WithContext(ctx).Unscoped().Where("id = ?", id).Delete(&model.ServiceTypeInstance{})
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected == 0 {
			return nil, backoff.Permanent(ErrInstanceNotFound)
		}
		return nil, nil
	}

	_, err := backoff.Retry(ctx, operation, s.retryOpts...)
	return err
}

func (s *ServiceTypeInstanceStore) ResetRetryCount(ctx context.Context, id string) error {
	result := s.db.WithContext(ctx).
		Model(&model.ServiceTypeInstance{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"deletion_status":       DeletionStatusScheduled,
			"retry_count":           0,
			"last_deletion_attempt": nil,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrInstanceNotFound
	}
	return nil
}

func (s *ServiceTypeInstanceStore) MarkProviderDeletionsPendingProvider(ctx context.Context, providerName string) error {
	return s.db.WithContext(ctx).
		Model(&model.ServiceTypeInstance{}).
		Where("provider_name = ? AND deletion_status IN ?", providerName, []string{DeletionStatusScheduled, DeletionStatusFailed}).
		Update("deletion_status", DeletionStatusPendingProvider).
		Error
}

func (s *ServiceTypeInstanceStore) ReactivateProviderDeletions(ctx context.Context, providerName string) error {
	return s.db.WithContext(ctx).
		Model(&model.ServiceTypeInstance{}).
		Where("provider_name = ? AND deletion_status = ?", providerName, DeletionStatusPendingProvider).
		Updates(map[string]any{
			"deletion_status": DeletionStatusScheduled,
			"retry_count":     0,
		}).
		Error
}

func (s *ServiceTypeInstanceStore) MarkPendingProviderIfNotReady(ctx context.Context, instanceID string) (bool, error) {
	result := s.db.WithContext(ctx).
		Model(&model.ServiceTypeInstance{}).
		Where("id = ? AND provider_name IN (?)",
			instanceID,
			s.db.Model(&model.Provider{}).Select("name").Where("health_status = ?", model.HealthStatusNotReady),
		).
		Update("deletion_status", DeletionStatusPendingProvider)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// getRetryOptions returns common retry configuration for database operations
func getRetryOptions() []backoff.RetryOption {
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = 1 * time.Second // Wait 1 second before first retry
	b.MaxInterval = 4 * time.Second     // Cap maximum wait time at 4 seconds
	b.Multiplier = 2.0                  // Double the wait time after each retry (1s, 2s, 4s)

	return []backoff.RetryOption{
		backoff.WithBackOff(b),
		backoff.WithMaxTries(4), // 1 initial attempt + 3 retries = 4 max tries
	}
}
