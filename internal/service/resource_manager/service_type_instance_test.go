package resource_manager_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/dcm-project/service-provider-manager/api/v1alpha1/resource_manager"
	"github.com/dcm-project/service-provider-manager/internal/service"
	rmsvc "github.com/dcm-project/service-provider-manager/internal/service/resource_manager"
	"github.com/dcm-project/service-provider-manager/internal/store"
	"github.com/dcm-project/service-provider-manager/internal/store/model"
	"github.com/go-resty/resty/v2"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var _ = Describe("InstanceService", func() {
	var (
		db              *gorm.DB
		dataStore       store.Store
		instanceService *rmsvc.InstanceService
		ctx             context.Context
		mockProvider    *httptest.Server
		providerCalled  bool
		deleteRequested bool
	)

	BeforeEach(func() {
		var err error
		db, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(db.AutoMigrate(&model.Provider{}, &model.ServiceTypeInstance{})).To(Succeed())

		// Create a mock provider server
		providerCalled = false
		deleteRequested = false
		mockProvider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				deleteRequested = true
				w.WriteHeader(http.StatusNoContent)
				return
			}
			providerCalled = true
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"id":     uuid.New().String(),
				"status": "PROVISIONING",
			})
		}))

		// Create a provider in the database
		provider := model.Provider{
			ID:           uuid.New().String(),
			Name:         "test-provider",
			ServiceType:  "vm",
			Endpoint:     mockProvider.URL,
			HealthStatus: model.HealthStatusReady,
		}
		Expect(db.Create(&provider).Error).NotTo(HaveOccurred())

		dataStore = store.NewStore(db)
		instanceService = rmsvc.NewInstanceService(dataStore, resty.New().
			SetTimeout(5*time.Second).
			SetRetryCount(0))
		ctx = context.Background()
	})

	AfterEach(func() {
		mockProvider.Close()
		_ = dataStore.Close()
	})

	Describe("CreateInstance", func() {
		It("creates a new instance", func() {
			req := &resource_manager.ServiceTypeInstance{
				ProviderName: "test-provider",
				Spec:         map[string]interface{}{"cpu": 2, "memory": "4GB", "service_type": "vm"},
			}

			result, err := instanceService.CreateInstance(ctx, req, nil)

			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.Id).NotTo(BeNil())
			Expect(result.ProviderName).To(Equal("test-provider"))
			Expect(providerCalled).To(BeTrue())
		})

		It("sets service_type from spec", func() {
			req := &resource_manager.ServiceTypeInstance{
				ProviderName: "test-provider",
				Spec:         map[string]interface{}{"cpu": 2, "service_type": "vm"},
			}

			result, err := instanceService.CreateInstance(ctx, req, nil)

			Expect(err).NotTo(HaveOccurred())
			var dbInstance model.ServiceTypeInstance
			Expect(db.Where("id = ?", *result.Id).First(&dbInstance).Error).NotTo(HaveOccurred())
			Expect(dbInstance.ServiceType).To(Equal("vm"))
		})

		It("creates instance with specified ID", func() {
			specifiedID := uuid.New().String()
			req := &resource_manager.ServiceTypeInstance{
				ProviderName: "test-provider",
				Spec:         map[string]interface{}{"cpu": 1, "service_type": "vm"},
			}

			result, err := instanceService.CreateInstance(ctx, req, &specifiedID)

			Expect(err).NotTo(HaveOccurred())
			Expect(*result.Id).To(Equal(specifiedID))
		})

		It("returns conflict error for duplicate ID", func() {
			specifiedID := uuid.New().String()
			req := &resource_manager.ServiceTypeInstance{
				ProviderName: "test-provider",
				Spec:         map[string]interface{}{"cpu": 1, "service_type": "vm"},
			}

			// First creation should succeed
			_, err := instanceService.CreateInstance(ctx, req, &specifiedID)
			Expect(err).NotTo(HaveOccurred())

			// Second creation with same ID should fail
			_, err = instanceService.CreateInstance(ctx, req, &specifiedID)

			Expect(err).To(HaveOccurred())
			var svcErr *service.ServiceError
			Expect(err).To(BeAssignableToTypeOf(svcErr))
			errors.As(err, &svcErr)
			Expect(svcErr.Code).To(Equal(service.ErrCodeConflict))
		})

		It("returns not found error for non-existent provider", func() {
			req := &resource_manager.ServiceTypeInstance{
				ProviderName: "non-existent-provider",
				Spec:         map[string]interface{}{"cpu": 1, "service_type": "vm"},
			}

			_, err := instanceService.CreateInstance(ctx, req, nil)

			Expect(err).To(HaveOccurred())
			var svcErr *service.ServiceError
			Expect(err).To(BeAssignableToTypeOf(svcErr))
			errors.As(err, &svcErr)
			Expect(svcErr.Code).To(Equal(service.ErrCodeNotFound))
		})

		It("returns provider error when provider exists but is not ready", func() {
			// Create a provider with HealthStatus = NotReady
			notReadyProvider := model.Provider{
				ID:           uuid.New().String(),
				Name:         "not-ready-provider",
				ServiceType:  "vm",
				Endpoint:     mockProvider.URL,
				HealthStatus: model.HealthStatusNotReady,
			}
			Expect(db.Create(&notReadyProvider).Error).NotTo(HaveOccurred())

			req := &resource_manager.ServiceTypeInstance{
				ProviderName: "not-ready-provider",
				Spec:         map[string]interface{}{"cpu": 1, "service_type": "vm"},
			}

			_, err := instanceService.CreateInstance(ctx, req, nil)

			Expect(err).To(HaveOccurred())
			var svcErr *service.ServiceError
			Expect(err).To(BeAssignableToTypeOf(svcErr))
			errors.As(err, &svcErr)
			Expect(svcErr.Code).To(Equal(service.ErrCodeProviderError))
			Expect(svcErr.Message).To(ContainSubstring("not in ready state"))
		})

		It("returns provider error when provider endpoint fails", func() {
			// Create a provider with a bad endpoint
			badProvider := model.Provider{
				ID:          uuid.New().String(),
				Name:        "bad-provider",
				ServiceType: "vm",
				Endpoint:    "http://localhost:1", // Invalid port
			}
			Expect(db.Create(&badProvider).Error).NotTo(HaveOccurred())

			req := &resource_manager.ServiceTypeInstance{
				ProviderName: "bad-provider",
				Spec:         map[string]interface{}{"cpu": 1, "service_type": "vm"},
			}

			_, err := instanceService.CreateInstance(ctx, req, nil)

			Expect(err).To(HaveOccurred())
			var svcErr *service.ServiceError
			Expect(err).To(BeAssignableToTypeOf(svcErr))
			errors.As(err, &svcErr)
			Expect(svcErr.Code).To(Equal(service.ErrCodeProviderError))
		})

		It("returns provider error when provider responds with 4xx HTTP error", func() {
			// Create a mock server that returns 400
			mockProvider4xx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error": "bad request"}`))
			}))
			defer mockProvider4xx.Close()

			provider4xx := model.Provider{
				ID:           uuid.New().String(),
				Name:         "provider-4xx",
				ServiceType:  "vm",
				Endpoint:     mockProvider4xx.URL,
				HealthStatus: model.HealthStatusReady,
			}
			Expect(db.Create(&provider4xx).Error).NotTo(HaveOccurred())

			req := &resource_manager.ServiceTypeInstance{
				ProviderName: "provider-4xx",
				Spec:         map[string]interface{}{"cpu": 1, "service_type": "vm"},
			}

			_, err := instanceService.CreateInstance(ctx, req, nil)

			Expect(err).To(HaveOccurred())
			var svcErr *service.ServiceError
			Expect(err).To(BeAssignableToTypeOf(svcErr))
			errors.As(err, &svcErr)
			Expect(svcErr.Code).To(Equal(service.ErrCodeProviderError))
			Expect(svcErr.Message).To(ContainSubstring("provider returned error"))
		})

		It("returns provider error when provider responds with 5xx HTTP error", func() {
			// Create a mock server that returns 500
			mockProvider5xx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error": "internal server error"}`))
			}))
			defer mockProvider5xx.Close()

			provider5xx := model.Provider{
				ID:           uuid.New().String(),
				Name:         "provider-5xx",
				ServiceType:  "vm",
				Endpoint:     mockProvider5xx.URL,
				HealthStatus: model.HealthStatusReady,
			}
			Expect(db.Create(&provider5xx).Error).NotTo(HaveOccurred())

			req := &resource_manager.ServiceTypeInstance{
				ProviderName: "provider-5xx",
				Spec:         map[string]interface{}{"cpu": 1, "service_type": "vm"},
			}

			_, err := instanceService.CreateInstance(ctx, req, nil)

			Expect(err).To(HaveOccurred())
			var svcErr *service.ServiceError
			Expect(err).To(BeAssignableToTypeOf(svcErr))
			errors.As(err, &svcErr)
			Expect(svcErr.Code).To(Equal(service.ErrCodeProviderError))
			Expect(svcErr.Message).To(ContainSubstring("provider returned error"))
		})

		It("returns validation error when spec is missing service_type", func() {
			req := &resource_manager.ServiceTypeInstance{
				ProviderName: "test-provider",
				Spec:         map[string]interface{}{"cpu": 2},
			}

			_, err := instanceService.CreateInstance(ctx, req, nil)

			Expect(err).To(HaveOccurred())
			var svcErr *service.ServiceError
			Expect(err).To(BeAssignableToTypeOf(svcErr))
			errors.As(err, &svcErr)
			Expect(svcErr.Code).To(Equal(service.ErrCodeValidation))
			Expect(svcErr.Message).To(ContainSubstring("spec.service_type is required"))
			Expect(providerCalled).To(BeFalse())
		})

		It("returns validation error when spec.service_type is not a string", func() {
			req := &resource_manager.ServiceTypeInstance{
				ProviderName: "test-provider",
				Spec:         map[string]interface{}{"cpu": 2, "service_type": 42},
			}

			_, err := instanceService.CreateInstance(ctx, req, nil)

			Expect(err).To(HaveOccurred())
			var svcErr *service.ServiceError
			Expect(err).To(BeAssignableToTypeOf(svcErr))
			errors.As(err, &svcErr)
			Expect(svcErr.Code).To(Equal(service.ErrCodeValidation))
			Expect(svcErr.Message).To(ContainSubstring("spec.service_type is required"))
			Expect(providerCalled).To(BeFalse())
		})

		It("returns validation error when spec.service_type is an empty string", func() {
			req := &resource_manager.ServiceTypeInstance{
				ProviderName: "test-provider",
				Spec:         map[string]interface{}{"cpu": 2, "service_type": ""},
			}

			_, err := instanceService.CreateInstance(ctx, req, nil)

			Expect(err).To(HaveOccurred())
			var svcErr *service.ServiceError
			Expect(err).To(BeAssignableToTypeOf(svcErr))
			errors.As(err, &svcErr)
			Expect(svcErr.Code).To(Equal(service.ErrCodeValidation))
			Expect(svcErr.Message).To(ContainSubstring("must not be empty"))
			Expect(providerCalled).To(BeFalse())
		})

		It("returns validation error when spec.service_type is whitespace only", func() {
			req := &resource_manager.ServiceTypeInstance{
				ProviderName: "test-provider",
				Spec:         map[string]interface{}{"cpu": 2, "service_type": " "},
			}

			_, err := instanceService.CreateInstance(ctx, req, nil)

			Expect(err).To(HaveOccurred())
			var svcErr *service.ServiceError
			Expect(err).To(BeAssignableToTypeOf(svcErr))
			errors.As(err, &svcErr)
			Expect(svcErr.Code).To(Equal(service.ErrCodeValidation))
			Expect(svcErr.Message).To(ContainSubstring("must not be empty"))
			Expect(providerCalled).To(BeFalse())
		})

		It("returns internal error with instance ID when DB insert fails", func() {
			var instanceID string
			var providerCallCount int
			mockProviderWithID := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				providerCallCount++
				instanceID = uuid.New().String()

				if providerCallCount == 1 {
					sqlDB, _ := db.DB()
					_ = sqlDB.Close()
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"id":     instanceID,
					"status": "PROVISIONING",
				})
			}))
			defer mockProviderWithID.Close()

			providerWithID := model.Provider{
				ID:           uuid.New().String(),
				Name:         "provider-db-fail",
				ServiceType:  "vm",
				Endpoint:     mockProviderWithID.URL,
				HealthStatus: model.HealthStatusReady,
			}
			Expect(db.Create(&providerWithID).Error).NotTo(HaveOccurred())

			req := &resource_manager.ServiceTypeInstance{
				ProviderName: "provider-db-fail",
				Spec:         map[string]interface{}{"cpu": 2, "service_type": "vm"},
			}

			_, err := instanceService.CreateInstance(ctx, req, nil)

			Expect(err).To(HaveOccurred())
			var svcErr *service.ServiceError
			Expect(err).To(BeAssignableToTypeOf(svcErr))
			errors.As(err, &svcErr)
			Expect(svcErr.Code).To(Equal(service.ErrCodeInternal))
			Expect(svcErr.Message).To(ContainSubstring("failed to create database record"))
			Expect(svcErr.Message).To(ContainSubstring(instanceID))
		})
	})

	Describe("GetInstance", func() {
		It("returns an instance", func() {
			// Create an instance first
			req := &resource_manager.ServiceTypeInstance{
				ProviderName: "test-provider",
				Spec:         map[string]interface{}{"cpu": 2, "service_type": "vm"},
			}
			created, _ := instanceService.CreateInstance(ctx, req, nil)

			result, err := instanceService.GetInstance(ctx, *created.Id, false)

			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(*result.Id).To(Equal(*created.Id))
			Expect(result.ProviderName).To(Equal("test-provider"))
		})

		It("returns not found error for non-existent instance", func() {
			_, err := instanceService.GetInstance(ctx, uuid.New().String(), false)

			Expect(err).To(HaveOccurred())
			var svcErr *service.ServiceError
			Expect(err).To(BeAssignableToTypeOf(svcErr))
			errors.As(err, &svcErr)
			Expect(svcErr.Code).To(Equal(service.ErrCodeNotFound))
		})
	})

	Describe("ListInstances", func() {
		It("returns empty list when no instances exist", func() {
			result, err := instanceService.ListInstances(ctx, nil, nil, false, nil, nil)

			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(*result.Instances).To(BeEmpty())
		})

		It("returns all instances", func() {
			// Create instances
			for i := 0; i < 3; i++ {
				req := &resource_manager.ServiceTypeInstance{
					ProviderName: "test-provider",
					Spec:         map[string]interface{}{"cpu": i + 1, "service_type": "vm"},
				}
				_, err := instanceService.CreateInstance(ctx, req, nil)
				Expect(err).NotTo(HaveOccurred())
			}

			result, err := instanceService.ListInstances(ctx, nil, nil, false, nil, nil)

			Expect(err).NotTo(HaveOccurred())
			Expect(*result.Instances).To(HaveLen(3))
		})

		It("respects max page size and returns next page token", func() {
			// Create 5 instances
			for i := 0; i < 5; i++ {
				req := &resource_manager.ServiceTypeInstance{
					ProviderName: "test-provider",
					Spec:         map[string]interface{}{"cpu": i + 1, "service_type": "vm"},
				}
				_, err := instanceService.CreateInstance(ctx, req, nil)
				Expect(err).NotTo(HaveOccurred())
			}

			maxPageSize := 2
			result, err := instanceService.ListInstances(ctx, nil, nil, false, &maxPageSize, nil)

			Expect(err).NotTo(HaveOccurred())
			Expect(*result.Instances).To(HaveLen(2))
			Expect(result.NextPageToken).NotTo(BeNil())
			Expect(*result.NextPageToken).NotTo(BeEmpty())

			// Get second page using token
			secondPage, err := instanceService.ListInstances(ctx, nil, nil, false, &maxPageSize, result.NextPageToken)

			Expect(err).NotTo(HaveOccurred())
			Expect(*secondPage.Instances).To(HaveLen(2))
			Expect(secondPage.NextPageToken).NotTo(BeNil())

			// Verify instances are different between pages
			firstIDs := make(map[string]bool)
			for _, inst := range *result.Instances {
				firstIDs[*inst.Id] = true
			}
			for _, inst := range *secondPage.Instances {
				Expect(firstIDs[*inst.Id]).To(BeFalse(), "Instance should not appear in both pages")
			}

			// Get third page (last page with 1 item)
			thirdPage, err := instanceService.ListInstances(ctx, nil, nil, false, &maxPageSize, secondPage.NextPageToken)
			Expect(err).NotTo(HaveOccurred())
			Expect(*thirdPage.Instances).To(HaveLen(1))
			Expect(thirdPage.NextPageToken).To(BeNil())
		})

		It("filters instances by service type", func() {
			containerProvider := model.Provider{
				ID:           uuid.New().String(),
				Name:         "container-provider",
				ServiceType:  "container",
				Endpoint:     mockProvider.URL,
				HealthStatus: model.HealthStatusReady,
			}
			Expect(db.Create(&containerProvider).Error).NotTo(HaveOccurred())

			// Create vm instances with service_type in spec
			for i := 0; i < 2; i++ {
				req := &resource_manager.ServiceTypeInstance{
					ProviderName: "test-provider",
					Spec:         map[string]interface{}{"cpu": i + 1, "service_type": "vm"},
				}
				_, err := instanceService.CreateInstance(ctx, req, nil)
				Expect(err).NotTo(HaveOccurred())
			}

			// Create container instances with service_type in spec
			for i := 0; i < 3; i++ {
				req := &resource_manager.ServiceTypeInstance{
					ProviderName: "container-provider",
					Spec:         map[string]interface{}{"image": "nginx", "service_type": "container"},
				}
				_, err := instanceService.CreateInstance(ctx, req, nil)
				Expect(err).NotTo(HaveOccurred())
			}

			vmType := "vm"
			result, err := instanceService.ListInstances(ctx, nil, &vmType, false, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(*result.Instances).To(HaveLen(2))

			containerType := "container"
			result, err = instanceService.ListInstances(ctx, nil, &containerType, false, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(*result.Instances).To(HaveLen(3))
		})

		It("filters instances by provider name", func() {
			// Create a second provider
			secondProvider := model.Provider{
				ID:           uuid.New().String(),
				Name:         "second-provider",
				ServiceType:  "vm",
				Endpoint:     mockProvider.URL,
				HealthStatus: model.HealthStatusReady,
			}
			Expect(db.Create(&secondProvider).Error).NotTo(HaveOccurred())

			// Create instances for different providers
			for i := 0; i < 2; i++ {
				req := &resource_manager.ServiceTypeInstance{
					ProviderName: "test-provider",
					Spec:         map[string]interface{}{"cpu": i + 1, "service_type": "vm"},
				}
				_, err := instanceService.CreateInstance(ctx, req, nil)
				Expect(err).NotTo(HaveOccurred())
			}

			for i := 0; i < 3; i++ {
				req := &resource_manager.ServiceTypeInstance{
					ProviderName: "second-provider",
					Spec:         map[string]interface{}{"cpu": i + 1, "service_type": "vm"},
				}
				_, err := instanceService.CreateInstance(ctx, req, nil)
				Expect(err).NotTo(HaveOccurred())
			}

			// Filter by first provider
			filterProvider := "test-provider"
			result, err := instanceService.ListInstances(ctx, &filterProvider, nil, false, nil, nil)

			Expect(err).NotTo(HaveOccurred())
			Expect(*result.Instances).To(HaveLen(2))
			for _, inst := range *result.Instances {
				Expect(inst.ProviderName).To(Equal("test-provider"))
			}

			// Filter by second provider
			filterProvider = "second-provider"
			result, err = instanceService.ListInstances(ctx, &filterProvider, nil, false, nil, nil)

			Expect(err).NotTo(HaveOccurred())
			Expect(*result.Instances).To(HaveLen(3))
			for _, inst := range *result.Instances {
				Expect(inst.ProviderName).To(Equal("second-provider"))
			}
		})
	})

	Describe("DeleteInstance", func() {
		It("deletes an instance", func() {
			// Create an instance first
			req := &resource_manager.ServiceTypeInstance{
				ProviderName: "test-provider",
				Spec:         map[string]interface{}{"cpu": 2, "service_type": "vm"},
			}
			created, _ := instanceService.CreateInstance(ctx, req, nil)

			err := instanceService.DeleteInstance(ctx, *created.Id, false)

			Expect(err).NotTo(HaveOccurred())
			Expect(deleteRequested).To(BeTrue())

			// Verify it's deleted
			_, err = instanceService.GetInstance(ctx, *created.Id, false)
			var svcErr *service.ServiceError
			Expect(err).To(BeAssignableToTypeOf(svcErr))
			errors.As(err, &svcErr)
			Expect(svcErr.Code).To(Equal(service.ErrCodeNotFound))
		})

		It("returns not found error for non-existent instance", func() {
			err := instanceService.DeleteInstance(ctx, uuid.New().String(), false)

			Expect(err).To(HaveOccurred())
			var svcErr *service.ServiceError
			Expect(err).To(BeAssignableToTypeOf(svcErr))
			errors.As(err, &svcErr)
			Expect(svcErr.Code).To(Equal(service.ErrCodeNotFound))
		})

		It("returns error when provider is missing and deferred is false", func() {
			req := &resource_manager.ServiceTypeInstance{
				ProviderName: "test-provider",
				Spec:         map[string]interface{}{"cpu": 2, "service_type": "vm"},
			}
			created, _ := instanceService.CreateInstance(ctx, req, nil)

			// Delete the provider from the database
			Expect(db.Delete(&model.Provider{}, "name = ?", "test-provider").Error).NotTo(HaveOccurred())

			err := instanceService.DeleteInstance(ctx, *created.Id, false)

			Expect(err).To(HaveOccurred())
			var svcErr *service.ServiceError
			Expect(err).To(BeAssignableToTypeOf(svcErr))
			errors.As(err, &svcErr)
			Expect(svcErr.Code).To(Equal(service.ErrCodeProviderError))
		})

		It("defers deletion without contacting provider", func() {
			req := &resource_manager.ServiceTypeInstance{
				ProviderName: "test-provider",
				Spec:         map[string]interface{}{"cpu": 2, "service_type": "vm"},
			}
			created, _ := instanceService.CreateInstance(ctx, req, nil)

			// Deferred delete should succeed without calling the provider
			err := instanceService.DeleteInstance(ctx, *created.Id, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(deleteRequested).To(BeFalse())

			// Instance should be marked for deletion, not visible in default list
			result, err := instanceService.ListInstances(ctx, nil, nil, false, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(*result.Instances).To(BeEmpty())

			// But visible with show_deleted
			result, err = instanceService.ListInstances(ctx, nil, nil, true, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(*result.Instances).To(HaveLen(1))
			Expect(string(*(*result.Instances)[0].DeletionStatus)).To(Equal("SCHEDULED"))
		})

		It("defers deletion without contacting provider even when provider is missing", func() {
			req := &resource_manager.ServiceTypeInstance{
				ProviderName: "test-provider",
				Spec:         map[string]interface{}{"cpu": 2, "service_type": "vm"},
			}
			created, _ := instanceService.CreateInstance(ctx, req, nil)

			// Delete the provider from the database
			Expect(db.Delete(&model.Provider{}, "name = ?", "test-provider").Error).NotTo(HaveOccurred())

			// Deferred delete should succeed without attempting provider call
			err := instanceService.DeleteInstance(ctx, *created.Id, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(deleteRequested).To(BeFalse())

			// Verify marked as SCHEDULED
			result, err := instanceService.ListInstances(ctx, nil, nil, true, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(*result.Instances).To(HaveLen(1))
			Expect(string(*(*result.Instances)[0].DeletionStatus)).To(Equal("SCHEDULED"))
		})

		It("resets retry count when deleting a FAILED instance with deferred=true", func() {
			req := &resource_manager.ServiceTypeInstance{
				ProviderName: "test-provider",
				Spec:         map[string]interface{}{"cpu": 2, "service_type": "vm"},
			}
			created, _ := instanceService.CreateInstance(ctx, req, nil)

			// Manually mark instance as FAILED with high retry count
			failed := "FAILED"
			Expect(db.Model(&model.ServiceTypeInstance{}).Where("id = ?", *created.Id).Updates(map[string]interface{}{
				"deletion_status": failed,
				"retry_count":     10,
			}).Error).NotTo(HaveOccurred())

			// Deferred delete should succeed and reset retry count without contacting provider
			err := instanceService.DeleteInstance(ctx, *created.Id, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(deleteRequested).To(BeFalse())

			// Verify retry count was reset and status is SCHEDULED
			var instance model.ServiceTypeInstance
			Expect(db.Where("id = ?", *created.Id).First(&instance).Error).NotTo(HaveOccurred())
			Expect(instance.RetryCount).To(Equal(0))
			Expect(*instance.DeletionStatus).To(Equal("SCHEDULED"))
		})

		It("returns 204 when deleting an already-pending instance and SP succeeds", func() {
			req := &resource_manager.ServiceTypeInstance{
				ProviderName: "test-provider",
				Spec:         map[string]interface{}{"cpu": 2, "service_type": "vm"},
			}
			created, _ := instanceService.CreateInstance(ctx, req, nil)

			// Manually mark instance as SCHEDULED
			scheduled := "SCHEDULED"
			Expect(db.Model(&model.ServiceTypeInstance{}).Where("id = ?", *created.Id).Updates(map[string]interface{}{
				"deletion_status": scheduled,
			}).Error).NotTo(HaveOccurred())

			// Delete should succeed (SP is available) and hard-delete the record
			err := instanceService.DeleteInstance(ctx, *created.Id, false)
			Expect(err).NotTo(HaveOccurred())
			Expect(deleteRequested).To(BeTrue())

			// Verify it's fully gone
			_, err = instanceService.GetInstance(ctx, *created.Id, false)
			var svcErr *service.ServiceError
			Expect(err).To(BeAssignableToTypeOf(svcErr))
			errors.As(err, &svcErr)
			Expect(svcErr.Code).To(Equal(service.ErrCodeNotFound))
		})
	})
})
