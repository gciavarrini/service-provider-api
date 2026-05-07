package store_test

import (
	"context"

	"github.com/dcm-project/service-provider-manager/internal/store/model"
	rmstore "github.com/dcm-project/service-provider-manager/internal/store/resource_manager"
	"github.com/dcm-project/service-provider-manager/internal/testutil"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newServiceTypeInstance(providerName, instanceName string, spec map[string]any) model.ServiceTypeInstance {
	return model.ServiceTypeInstance{
		ID:           uuid.New().String(),
		ProviderName: providerName,
		Status:       "PROVISIONING",
		InstanceName: instanceName,
		Spec:         spec,
	}
}

func newServiceTypeInstanceWithType(providerName, instanceName, serviceType string, spec map[string]any) model.ServiceTypeInstance {
	inst := newServiceTypeInstance(providerName, instanceName, spec)
	inst.ServiceType = serviceType
	return inst
}

var kubevirtProvider = "kubevirt-sp"

var _ = Describe("ServiceTypeInstance Store", func() {
	var (
		db  *gorm.DB
		s   rmstore.ServiceTypeInstance
		ctx context.Context
	)

	addInstanceToStore := func(instance model.ServiceTypeInstance) *model.ServiceTypeInstance {
		created, err := s.Create(ctx, instance)
		Expect(err).NotTo(HaveOccurred())
		return created
	}

	BeforeEach(func() {
		var err error
		db, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(db.AutoMigrate(&model.Provider{}, &model.ServiceTypeInstance{})).To(Succeed())

		s = rmstore.NewServiceTypeInstance(db, testutil.FastServiceTypeInstanceRetry()...)
		ctx = context.Background()
	})

	AfterEach(func() {
		sqlDB, err := db.DB()
		Expect(err).NotTo(HaveOccurred())
		Expect(sqlDB.Close()).To(Succeed())
	})

	Describe("Create", func() {
		It("persists the instance", func() {
			instance := newServiceTypeInstance(
				kubevirtProvider,
				"instance-1",
				map[string]any{"cpu": 2})
			created, err := s.Create(ctx, instance)

			Expect(err).NotTo(HaveOccurred())
			Expect(created.ID).To(Equal(instance.ID))
		})

		It("retries on transient DB errors", func() {
			// Close DB to simulate transient failure
			sqlDB, err := db.DB()
			Expect(err).NotTo(HaveOccurred())
			_ = sqlDB.Close()

			instance := newServiceTypeInstance(
				kubevirtProvider,
				"retry-test",
				map[string]any{"cpu": 1})

			_, err = s.Create(ctx, instance)

			// Should fail after retries exhausted
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("database is closed"))
		})
	})

	Describe("Get", func() {
		It("retrieves by ID", func() {
			seeded := newServiceTypeInstance(kubevirtProvider, "get-inst", map[string]any{"cpu": 1})
			addInstanceToStore(seeded)

			found, err := s.Get(ctx, seeded.ID, false)

			Expect(err).NotTo(HaveOccurred())
			Expect(found).NotTo(BeNil())
			Expect(found.ProviderName).To(Equal(kubevirtProvider))
			Expect(found.InstanceName).To(Equal("get-inst"))
		})

		It("returns ErrInstanceNotFound for missing ID", func() {
			_, err := s.Get(ctx, uuid.New().String(), false)
			Expect(err).To(MatchError(rmstore.ErrInstanceNotFound))
		})
	})

	Describe("List", func() {
		BeforeEach(func() {
			addInstanceToStore(newServiceTypeInstance(kubevirtProvider, "instance1", map[string]any{}))
			addInstanceToStore(newServiceTypeInstance(kubevirtProvider, "instance2", map[string]any{}))
			addInstanceToStore(newServiceTypeInstance(kubevirtProvider, "instance3", map[string]any{}))
		})

		It("returns all instances when opts is nil", func() {
			result, err := s.List(ctx, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Instances).To(HaveLen(3))
			Expect(result.NextPageToken).To(BeNil())
		})

		It("filters by provider name", func() {
			result, err := s.List(ctx, &rmstore.ServiceTypeInstanceListOptions{
				ProviderName: &kubevirtProvider,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Instances).To(HaveLen(3))
		})

		It("applies pagination with page size", func() {
			result, err := s.List(ctx, &rmstore.ServiceTypeInstanceListOptions{
				PageSize: 2,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Instances).To(HaveLen(2))
			Expect(result.NextPageToken).NotTo(BeNil())
		})

		It("filters by service type", func() {
			vmType := "vm"
			containerType := "container"
			addInstanceToStore(newServiceTypeInstanceWithType(kubevirtProvider, "vm-inst", vmType, map[string]any{}))
			addInstanceToStore(newServiceTypeInstanceWithType(kubevirtProvider, "container-inst", containerType, map[string]any{}))

			result, err := s.List(ctx, &rmstore.ServiceTypeInstanceListOptions{
				ServiceType: &vmType,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Instances).To(HaveLen(1))
			Expect(result.Instances[0].ServiceType).To(Equal("vm"))

			result, err = s.List(ctx, &rmstore.ServiceTypeInstanceListOptions{
				ServiceType: &containerType,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Instances).To(HaveLen(1))
			Expect(result.Instances[0].ServiceType).To(Equal("container"))
		})

		It("returns next page using page token", func() {
			// Get first page
			firstPage, err := s.List(ctx, &rmstore.ServiceTypeInstanceListOptions{
				PageSize: 2,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(firstPage.Instances).To(HaveLen(2))
			Expect(firstPage.NextPageToken).NotTo(BeNil())

			// Get second page using token
			secondPage, err := s.List(ctx, &rmstore.ServiceTypeInstanceListOptions{
				PageSize:  2,
				PageToken: firstPage.NextPageToken,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(secondPage.Instances).To(HaveLen(1))
			Expect(secondPage.NextPageToken).To(BeNil())
		})
	})

	Describe("HardDelete", func() {
		It("removes the instance", func() {
			instance := newServiceTypeInstance(kubevirtProvider, "to-delete", map[string]any{})
			addInstanceToStore(instance)

			Expect(s.HardDelete(ctx, instance.ID)).To(Succeed())

			_, err := s.Get(ctx, instance.ID, false)
			Expect(err).To(MatchError(rmstore.ErrInstanceNotFound))
		})

		It("returns ErrInstanceNotFound for missing ID", func() {
			err := s.HardDelete(ctx, uuid.New().String())
			Expect(err).To(MatchError(rmstore.ErrInstanceNotFound))
		})

		It("does not retry on permanent errors (not found)", func() {
			nonExistentID := uuid.New().String()
			err := s.HardDelete(ctx, nonExistentID)

			// Should return ErrInstanceNotFound immediately (permanent error)
			Expect(err).To(MatchError(rmstore.ErrInstanceNotFound))
		})
	})

	Describe("UpdateStatus", func() {
		It("updates status and status message by instance ID", func() {
			instance := newServiceTypeInstance(kubevirtProvider, "status-inst", map[string]any{"cpu": "2"})
			addInstanceToStore(instance)

			err := s.UpdateStatus(ctx, instance.ID, "RUNNING", "VM is running")
			Expect(err).NotTo(HaveOccurred())

			found, err := s.Get(ctx, instance.ID, false)
			Expect(err).NotTo(HaveOccurred())
			Expect(found.Status).To(Equal("RUNNING"))
			Expect(found.StatusMessage).To(Equal("VM is running"))
		})

		It("returns ErrInstanceNotFound for non-existent instance", func() {
			err := s.UpdateStatus(ctx, "non-existent", "RUNNING", "message")
			Expect(err).To(MatchError(rmstore.ErrInstanceNotFound))
		})
	})

	Describe("MarkForDeletion", func() {
		It("sets deletion_status to SCHEDULED", func() {
			instance := newServiceTypeInstance(kubevirtProvider, "mark-del", map[string]any{})
			addInstanceToStore(instance)

			Expect(s.MarkForDeletion(ctx, instance.ID)).To(Succeed())

			found, err := s.Get(ctx, instance.ID, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(*found.DeletionStatus).To(Equal("SCHEDULED"))
			Expect(found.RetryCount).To(Equal(0))
			Expect(found.DeletionRequestedAt).NotTo(BeNil())
		})

		It("hides instance from default Get", func() {
			instance := newServiceTypeInstance(kubevirtProvider, "mark-hidden", map[string]any{})
			addInstanceToStore(instance)

			Expect(s.MarkForDeletion(ctx, instance.ID)).To(Succeed())

			_, err := s.Get(ctx, instance.ID, false)
			Expect(err).To(MatchError(rmstore.ErrInstanceNotFound))
		})

		It("returns ErrInstanceNotFound for missing ID", func() {
			err := s.MarkForDeletion(ctx, uuid.New().String())
			Expect(err).To(MatchError(rmstore.ErrInstanceNotFound))
		})
	})

	Describe("ListPendingDeletions", func() {
		It("returns only SCHEDULED instances", func() {
			inst1 := addInstanceToStore(newServiceTypeInstance(kubevirtProvider, "pending1", map[string]any{}))
			inst2 := addInstanceToStore(newServiceTypeInstance(kubevirtProvider, "pending2", map[string]any{}))
			addInstanceToStore(newServiceTypeInstance(kubevirtProvider, "active", map[string]any{}))

			Expect(s.MarkForDeletion(ctx, inst1.ID)).To(Succeed())
			Expect(s.MarkForDeletion(ctx, inst2.ID)).To(Succeed())

			pending, err := s.ListPendingDeletions(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(pending).To(HaveLen(2))
		})

		It("excludes FAILED instances", func() {
			inst := addInstanceToStore(newServiceTypeInstance(kubevirtProvider, "failed", map[string]any{}))
			Expect(s.MarkForDeletion(ctx, inst.ID)).To(Succeed())
			Expect(s.MarkDeletionFailed(ctx, inst.ID)).To(Succeed())

			pending, err := s.ListPendingDeletions(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(pending).To(BeEmpty())
		})

		It("returns empty when no pending deletions exist", func() {
			addInstanceToStore(newServiceTypeInstance(kubevirtProvider, "active", map[string]any{}))

			pending, err := s.ListPendingDeletions(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(pending).To(BeEmpty())
		})
	})

	Describe("IncrementDeletionRetry", func() {
		It("increments retry count and sets last_deletion_attempt", func() {
			inst := addInstanceToStore(newServiceTypeInstance(kubevirtProvider, "retry-inst", map[string]any{}))
			Expect(s.MarkForDeletion(ctx, inst.ID)).To(Succeed())

			Expect(s.IncrementDeletionRetry(ctx, inst.ID)).To(Succeed())

			found, err := s.Get(ctx, inst.ID, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(found.RetryCount).To(Equal(1))
			Expect(found.LastDeletionAttempt).NotTo(BeNil())

			Expect(s.IncrementDeletionRetry(ctx, inst.ID)).To(Succeed())

			found, err = s.Get(ctx, inst.ID, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(found.RetryCount).To(Equal(2))
		})

		It("returns ErrInstanceNotFound for missing ID", func() {
			err := s.IncrementDeletionRetry(ctx, uuid.New().String())
			Expect(err).To(MatchError(rmstore.ErrInstanceNotFound))
		})
	})

	Describe("MarkDeletionFailed", func() {
		It("sets deletion_status to FAILED", func() {
			inst := addInstanceToStore(newServiceTypeInstance(kubevirtProvider, "fail-inst", map[string]any{}))
			Expect(s.MarkForDeletion(ctx, inst.ID)).To(Succeed())

			Expect(s.MarkDeletionFailed(ctx, inst.ID)).To(Succeed())

			found, err := s.Get(ctx, inst.ID, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(*found.DeletionStatus).To(Equal("FAILED"))
		})

		It("returns ErrInstanceNotFound for missing ID", func() {
			err := s.MarkDeletionFailed(ctx, uuid.New().String())
			Expect(err).To(MatchError(rmstore.ErrInstanceNotFound))
		})
	})

	Describe("ResetRetryCount", func() {
		It("resets retry count and status to SCHEDULED", func() {
			inst := addInstanceToStore(newServiceTypeInstance(kubevirtProvider, "reset-inst", map[string]any{}))
			Expect(s.MarkForDeletion(ctx, inst.ID)).To(Succeed())
			Expect(s.IncrementDeletionRetry(ctx, inst.ID)).To(Succeed())
			Expect(s.IncrementDeletionRetry(ctx, inst.ID)).To(Succeed())
			Expect(s.MarkDeletionFailed(ctx, inst.ID)).To(Succeed())

			Expect(s.ResetRetryCount(ctx, inst.ID)).To(Succeed())

			found, err := s.Get(ctx, inst.ID, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(*found.DeletionStatus).To(Equal("SCHEDULED"))
			Expect(found.RetryCount).To(Equal(0))
			Expect(found.LastDeletionAttempt).To(BeNil())
		})

		It("returns ErrInstanceNotFound for missing ID", func() {
			err := s.ResetRetryCount(ctx, uuid.New().String())
			Expect(err).To(MatchError(rmstore.ErrInstanceNotFound))
		})
	})

	Describe("Get with showDeleted", func() {
		It("returns soft-deleted instance when showDeleted is true", func() {
			inst := addInstanceToStore(newServiceTypeInstance(kubevirtProvider, "soft-del", map[string]any{}))
			Expect(s.MarkForDeletion(ctx, inst.ID)).To(Succeed())

			found, err := s.Get(ctx, inst.ID, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(found.ID).To(Equal(inst.ID))
			Expect(*found.DeletionStatus).To(Equal("SCHEDULED"))
		})

		It("returns not found for soft-deleted instance when showDeleted is false", func() {
			inst := addInstanceToStore(newServiceTypeInstance(kubevirtProvider, "soft-del2", map[string]any{}))
			Expect(s.MarkForDeletion(ctx, inst.ID)).To(Succeed())

			_, err := s.Get(ctx, inst.ID, false)
			Expect(err).To(MatchError(rmstore.ErrInstanceNotFound))
		})
	})

	Describe("List with ShowDeleted", func() {
		It("excludes soft-deleted instances by default", func() {
			addInstanceToStore(newServiceTypeInstance(kubevirtProvider, "active", map[string]any{}))
			deleted := addInstanceToStore(newServiceTypeInstance(kubevirtProvider, "deleted", map[string]any{}))
			Expect(s.MarkForDeletion(ctx, deleted.ID)).To(Succeed())

			result, err := s.List(ctx, &rmstore.ServiceTypeInstanceListOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Instances).To(HaveLen(1))
		})

		It("includes soft-deleted instances when ShowDeleted is true", func() {
			addInstanceToStore(newServiceTypeInstance(kubevirtProvider, "active", map[string]any{}))
			deleted := addInstanceToStore(newServiceTypeInstance(kubevirtProvider, "deleted", map[string]any{}))
			Expect(s.MarkForDeletion(ctx, deleted.ID)).To(Succeed())

			result, err := s.List(ctx, &rmstore.ServiceTypeInstanceListOptions{ShowDeleted: true})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Instances).To(HaveLen(2))
		})
	})

	Describe("ExistsByID", func() {
		It("returns true when instance exists", func() {
			instance := newServiceTypeInstance(kubevirtProvider, "exists", map[string]any{})
			addInstanceToStore(instance)

			exists, err := s.ExistsByID(ctx, instance.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(exists).To(BeTrue())
		})

		It("returns false when instance is missing", func() {
			exists, err := s.ExistsByID(ctx, uuid.New().String())
			Expect(err).NotTo(HaveOccurred())
			Expect(exists).To(BeFalse())
		})
	})

	Describe("MarkProviderDeletionsPendingProvider", func() {
		It("transitions PENDING and FAILED instances to PENDING_PROVIDER", func() {
			pendingInst := addInstanceToStore(newServiceTypeInstance(kubevirtProvider, "pending", map[string]any{}))
			Expect(s.MarkForDeletion(ctx, pendingInst.ID)).To(Succeed())

			failedInst := addInstanceToStore(newServiceTypeInstance(kubevirtProvider, "failed", map[string]any{}))
			Expect(s.MarkForDeletion(ctx, failedInst.ID)).To(Succeed())
			Expect(s.MarkDeletionFailed(ctx, failedInst.ID)).To(Succeed())

			activeInst := addInstanceToStore(newServiceTypeInstance(kubevirtProvider, "active", map[string]any{}))

			Expect(s.MarkProviderDeletionsPendingProvider(ctx, kubevirtProvider)).To(Succeed())

			found, err := s.Get(ctx, pendingInst.ID, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(*found.DeletionStatus).To(Equal("PENDING_PROVIDER"))

			found, err = s.Get(ctx, failedInst.ID, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(*found.DeletionStatus).To(Equal("PENDING_PROVIDER"))

			found, err = s.Get(ctx, activeInst.ID, false)
			Expect(err).NotTo(HaveOccurred())
			Expect(found.DeletionStatus).To(BeNil())
		})

		It("does not affect instances from other providers", func() {
			otherProvider := "other-provider"
			inst := addInstanceToStore(newServiceTypeInstance(otherProvider, "other-pending", map[string]any{}))
			Expect(s.MarkForDeletion(ctx, inst.ID)).To(Succeed())

			Expect(s.MarkProviderDeletionsPendingProvider(ctx, kubevirtProvider)).To(Succeed())

			found, err := s.Get(ctx, inst.ID, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(*found.DeletionStatus).To(Equal("SCHEDULED"))
		})
	})

	Describe("ReactivateProviderDeletions", func() {
		It("transitions PENDING_PROVIDER to PENDING with retry_count=0", func() {
			inst := addInstanceToStore(newServiceTypeInstance(kubevirtProvider, "marked", map[string]any{}))
			Expect(s.MarkForDeletion(ctx, inst.ID)).To(Succeed())
			Expect(s.IncrementDeletionRetry(ctx, inst.ID)).To(Succeed())
			Expect(s.MarkProviderDeletionsPendingProvider(ctx, kubevirtProvider)).To(Succeed())

			Expect(s.ReactivateProviderDeletions(ctx, kubevirtProvider)).To(Succeed())

			found, err := s.Get(ctx, inst.ID, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(*found.DeletionStatus).To(Equal("SCHEDULED"))
			Expect(found.RetryCount).To(Equal(0))
		})

		It("does not affect SCHEDULED or FAILED instances", func() {
			pendingInst := addInstanceToStore(newServiceTypeInstance(kubevirtProvider, "still-pending", map[string]any{}))
			Expect(s.MarkForDeletion(ctx, pendingInst.ID)).To(Succeed())

			failedInst := addInstanceToStore(newServiceTypeInstance(kubevirtProvider, "still-failed", map[string]any{}))
			Expect(s.MarkForDeletion(ctx, failedInst.ID)).To(Succeed())
			Expect(s.MarkDeletionFailed(ctx, failedInst.ID)).To(Succeed())

			Expect(s.ReactivateProviderDeletions(ctx, kubevirtProvider)).To(Succeed())

			found, err := s.Get(ctx, pendingInst.ID, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(*found.DeletionStatus).To(Equal("SCHEDULED"))

			found, err = s.Get(ctx, failedInst.ID, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(*found.DeletionStatus).To(Equal("FAILED"))
		})
	})

	Describe("MarkPendingProviderIfNotReady", func() {
		var addProvider func(name string, healthStatus model.HealthStatus)

		BeforeEach(func() {
			addProvider = func(name string, healthStatus model.HealthStatus) {
				provider := model.Provider{
					ID:            uuid.New().String(),
					Name:          name,
					ServiceType:   "vm",
					SchemaVersion: "v1",
					Endpoint:      "http://localhost:8080",
					HealthStatus:  healthStatus,
				}
				Expect(db.Create(&provider).Error).NotTo(HaveOccurred())
			}
		})

		It("parks instance and returns true when provider is NotReady", func() {
			addProvider(kubevirtProvider, model.HealthStatusNotReady)
			inst := addInstanceToStore(newServiceTypeInstance(kubevirtProvider, "to-park", map[string]any{}))
			Expect(s.MarkForDeletion(ctx, inst.ID)).To(Succeed())

			marked, err := s.MarkPendingProviderIfNotReady(ctx, inst.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(marked).To(BeTrue())

			found, err := s.Get(ctx, inst.ID, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(*found.DeletionStatus).To(Equal("PENDING_PROVIDER"))
		})

		It("is a no-op and returns false when provider is Ready", func() {
			addProvider(kubevirtProvider, model.HealthStatusReady)
			inst := addInstanceToStore(newServiceTypeInstance(kubevirtProvider, "healthy", map[string]any{}))
			Expect(s.MarkForDeletion(ctx, inst.ID)).To(Succeed())

			marked, err := s.MarkPendingProviderIfNotReady(ctx, inst.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(marked).To(BeFalse())

			found, err := s.Get(ctx, inst.ID, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(*found.DeletionStatus).To(Equal("SCHEDULED"))
		})

		It("is idempotent when instance is already PENDING_PROVIDER", func() {
			addProvider(kubevirtProvider, model.HealthStatusNotReady)
			inst := addInstanceToStore(newServiceTypeInstance(kubevirtProvider, "already-marked", map[string]any{}))
			Expect(s.MarkForDeletion(ctx, inst.ID)).To(Succeed())

			marked1, err := s.MarkPendingProviderIfNotReady(ctx, inst.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(marked1).To(BeTrue())

			marked2, err := s.MarkPendingProviderIfNotReady(ctx, inst.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(marked2).To(BeTrue())

			found, err := s.Get(ctx, inst.ID, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(*found.DeletionStatus).To(Equal("PENDING_PROVIDER"))
		})
	})
})
