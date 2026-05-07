//go:build e2e

package e2e_test

import (
	"context"
	"net/http"
	"os"
	"time"

	providerapi "github.com/dcm-project/service-provider-manager/api/v1alpha1/provider"
	"github.com/dcm-project/service-provider-manager/api/v1alpha1/resource_manager"
	providerclient "github.com/dcm-project/service-provider-manager/pkg/client/provider"
	rmClient "github.com/dcm-project/service-provider-manager/pkg/client/resource_manager"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Service Instance API", func() {
	var (
		rmApiClient  *rmClient.ClientWithResponses
		apiClient    *providerclient.ClientWithResponses
		ctx          context.Context
		providerID   string
		providerName string
	)

	BeforeEach(func() {
		baseURL := os.Getenv("API_URL")
		if baseURL == "" {
			baseURL = "http://localhost:8080/api/v1alpha1"
		}

		var err error
		rmApiClient, err = rmClient.NewClientWithResponses(baseURL)
		Expect(err).NotTo(HaveOccurred())

		apiClient, err = providerclient.NewClientWithResponses(baseURL)
		Expect(err).NotTo(HaveOccurred())

		ctx = context.Background()

		resetWireMock()
		stubProviderHealthEndpoint()
		stubProviderCreateInstance()
		stubProviderDeleteInstance()

		providerName = "e2e-provider-" + uuid.New().String()[:8]
		createResp, err := apiClient.CreateProviderWithResponse(ctx, nil, providerapi.Provider{
			Name:          providerName,
			Endpoint:      providerEndpoint(),
			ServiceType:   "vm",
			SchemaVersion: "v1alpha1",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(createResp.StatusCode()).To(Equal(http.StatusCreated))
		providerID = *createResp.JSON201.Id

		waitForProviderReady(apiClient, ctx, providerID)
	})

	AfterEach(func() {
		if providerID != "" {
			apiClient.DeleteProviderWithResponse(ctx, providerID)
		}
	})

	Describe("Health Check", func() {
		It("returns healthy status", func() {
			resp, err := rmApiClient.GetHealthWithResponse(ctx)

			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode()).To(Equal(http.StatusOK))
			Expect(resp.JSON200).NotTo(BeNil())
			Expect(*resp.JSON200.Status).To(Equal("ok"))
		})
	})

	Describe("Create Instance", func() {
		It("creates an instance with specified ID", func() {
			instID := uuid.New().String()
			params := &resource_manager.CreateInstanceParams{Id: &instID}
			createResp, err := rmApiClient.CreateInstanceWithResponse(ctx, params, resource_manager.ServiceTypeInstance{
				ProviderName: providerName,
				Spec:         map[string]interface{}{"cpu": 2, "memory": "4GB", "service_type": "vm"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(createResp.StatusCode()).To(Equal(http.StatusCreated))
			Expect(createResp.JSON201).NotTo(BeNil())
			Expect(*createResp.JSON201.Id).To(Equal(instID))
			Expect(createResp.JSON201.ProviderName).To(Equal(providerName))
		})

		It("creates an instance with server-generated ID", func() {
			createResp, err := rmApiClient.CreateInstanceWithResponse(ctx, nil, resource_manager.ServiceTypeInstance{
				ProviderName: providerName,
				Spec:         map[string]interface{}{"cpu": 1, "service_type": "vm"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(createResp.StatusCode()).To(Equal(http.StatusCreated))
			Expect(createResp.JSON201.Id).NotTo(BeNil())
			Expect(*createResp.JSON201.Id).NotTo(BeEmpty())
		})

		It("returns 409 for duplicate instance ID", func() {
			instID := uuid.New().String()
			params := &resource_manager.CreateInstanceParams{Id: &instID}
			body := resource_manager.ServiceTypeInstance{
				ProviderName: providerName,
				Spec:         map[string]interface{}{"cpu": 1, "service_type": "vm"},
			}

			resp1, err := rmApiClient.CreateInstanceWithResponse(ctx, params, body)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp1.StatusCode()).To(Equal(http.StatusCreated))

			resp2, err := rmApiClient.CreateInstanceWithResponse(ctx, params, body)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp2.StatusCode()).To(Equal(http.StatusConflict))
		})

		It("returns 404 for non-existent provider", func() {
			createResp, err := rmApiClient.CreateInstanceWithResponse(ctx, nil, resource_manager.ServiceTypeInstance{
				ProviderName: "non-existent-provider-" + uuid.New().String(),
				Spec:         map[string]interface{}{"cpu": 1, "service_type": "vm"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(createResp.StatusCode()).To(Equal(http.StatusNotFound))
		})

		It("returns 422 when provider health status is not Ready", func() {
			unhealthyName := "unhealthy-provider-" + uuid.New().String()[:8]
			createProviderResp, err := apiClient.CreateProviderWithResponse(ctx, nil, providerapi.Provider{
				Name:          unhealthyName,
				Endpoint:      "http://invalid-endpoint-does-not-exist.local/api",
				ServiceType:   "vm",
				SchemaVersion: "v1alpha1",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(createProviderResp.StatusCode()).To(Equal(http.StatusCreated))
			unhealthyProviderID := *createProviderResp.JSON201.Id

			defer func() {
				apiClient.DeleteProviderWithResponse(ctx, unhealthyProviderID)
			}()

			createResp, err := rmApiClient.CreateInstanceWithResponse(ctx, nil, resource_manager.ServiceTypeInstance{
				ProviderName: unhealthyName,
				Spec:         map[string]interface{}{"cpu": 2, "service_type": "vm"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(createResp.StatusCode()).To(Equal(http.StatusUnprocessableEntity))
		})
	})

	Describe("Get Instance", func() {
		It("returns 200 for existing instance", func() {
			instID := uuid.New().String()
			params := &resource_manager.CreateInstanceParams{Id: &instID}
			createResp, err := rmApiClient.CreateInstanceWithResponse(ctx, params, resource_manager.ServiceTypeInstance{
				ProviderName: providerName,
				Spec:         map[string]interface{}{"cpu": 2, "service_type": "vm"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(createResp.StatusCode()).To(Equal(http.StatusCreated))

			getResp, err := rmApiClient.GetInstanceWithResponse(ctx, instID, nil)

			Expect(err).NotTo(HaveOccurred())
			Expect(getResp.StatusCode()).To(Equal(http.StatusOK))
			Expect(getResp.JSON200).NotTo(BeNil())
			Expect(*getResp.JSON200.Id).To(Equal(instID))
			Expect(getResp.JSON200.ProviderName).To(Equal(providerName))
		})

		It("returns 404 for non-existent instance", func() {
			nonExistentID := uuid.New().String()
			getResp, err := rmApiClient.GetInstanceWithResponse(ctx, nonExistentID, nil)

			Expect(err).NotTo(HaveOccurred())
			Expect(getResp.StatusCode()).To(Equal(http.StatusNotFound))
		})
	})

	Describe("List Instances", func() {
		It("returns created instances in the list", func() {
			instID := uuid.New().String()
			params := &resource_manager.CreateInstanceParams{Id: &instID}
			createResp, err := rmApiClient.CreateInstanceWithResponse(ctx, params, resource_manager.ServiceTypeInstance{
				ProviderName: providerName,
				Spec:         map[string]interface{}{"cpu": 1, "service_type": "vm"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(createResp.StatusCode()).To(Equal(http.StatusCreated))

			listResp, err := rmApiClient.ListInstancesWithResponse(ctx, nil)

			Expect(err).NotTo(HaveOccurred())
			Expect(listResp.StatusCode()).To(Equal(http.StatusOK))
			Expect(listResp.JSON200.Instances).NotTo(BeNil())

			ids := make([]string, len(*listResp.JSON200.Instances))
			for i, inst := range *listResp.JSON200.Instances {
				ids[i] = *inst.Id
			}
			Expect(ids).To(ContainElement(instID))
		})

		It("filters by provider name", func() {
			filterProvider := providerName
			params := &resource_manager.ListInstancesParams{
				Provider: &filterProvider,
			}

			listResp, err := rmApiClient.ListInstancesWithResponse(ctx, params)

			Expect(err).NotTo(HaveOccurred())
			Expect(listResp.StatusCode()).To(Equal(http.StatusOK))
			Expect(listResp.JSON200).NotTo(BeNil())
		})

		It("respects max page size parameter", func() {
			maxPageSize := 10
			params := &resource_manager.ListInstancesParams{
				MaxPageSize: &maxPageSize,
			}

			listResp, err := rmApiClient.ListInstancesWithResponse(ctx, params)

			Expect(err).NotTo(HaveOccurred())
			Expect(listResp.StatusCode()).To(Equal(http.StatusOK))

			if listResp.JSON200.Instances != nil {
				Expect(len(*listResp.JSON200.Instances)).To(BeNumerically("<=", maxPageSize))
			}
		})

		It("returns 400 for invalid max page size", func() {
			invalidPageSize := 1000
			params := &resource_manager.ListInstancesParams{
				MaxPageSize: &invalidPageSize,
			}

			listResp, err := rmApiClient.ListInstancesWithResponse(ctx, params)

			Expect(err).NotTo(HaveOccurred())
			Expect(listResp.StatusCode()).To(Equal(http.StatusBadRequest))
		})

		It("handles invalid page token gracefully", func() {
			invalidToken := "invalid-base64-token"
			params := &resource_manager.ListInstancesParams{
				PageToken: &invalidToken,
			}

			listResp, err := rmApiClient.ListInstancesWithResponse(ctx, params)
			Expect(err).NotTo(HaveOccurred())
			Expect(listResp.StatusCode()).To(Equal(http.StatusOK))
		})
	})

	Describe("Delete Instance", func() {
		It("returns 204 and instance is removed", func() {
			instID := uuid.New().String()
			params := &resource_manager.CreateInstanceParams{Id: &instID}
			createResp, err := rmApiClient.CreateInstanceWithResponse(ctx, params, resource_manager.ServiceTypeInstance{
				ProviderName: providerName,
				Spec:         map[string]interface{}{"cpu": 2, "service_type": "vm"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(createResp.StatusCode()).To(Equal(http.StatusCreated))

			deleteResp, err := rmApiClient.DeleteInstanceWithResponse(ctx, instID, nil)

			Expect(err).NotTo(HaveOccurred())
			Expect(deleteResp.StatusCode()).To(Equal(http.StatusNoContent))

			getResp, err := rmApiClient.GetInstanceWithResponse(ctx, instID, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(getResp.StatusCode()).To(Equal(http.StatusNotFound))
		})

		It("returns 404 when deleting non-existent instance", func() {
			nonExistentID := uuid.New().String()
			deleteResp, err := rmApiClient.DeleteInstanceWithResponse(ctx, nonExistentID, nil)

			Expect(err).NotTo(HaveOccurred())
			Expect(deleteResp.StatusCode()).To(Equal(http.StatusNotFound))
		})
	})

	Describe("Deferred Deletion", func() {
		var instID string

		createInstance := func() {
			instID = uuid.New().String()
			params := &resource_manager.CreateInstanceParams{Id: &instID}
			createResp, err := rmApiClient.CreateInstanceWithResponse(ctx, params, resource_manager.ServiceTypeInstance{
				ProviderName: providerName,
				Spec:         map[string]interface{}{"cpu": 2, "service_type": "vm"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(createResp.StatusCode()).To(Equal(http.StatusCreated))
		}

		It("returns error on non-deferred delete when SP fails", func() {
			createInstance()
			clearDeleteStubAndStubFailure()

			deleteResp, err := rmApiClient.DeleteInstanceWithResponse(ctx, instID, nil)

			Expect(err).NotTo(HaveOccurred())
			Expect(deleteResp.StatusCode()).To(Equal(http.StatusInternalServerError))

			// Instance should still be active
			getResp, err := rmApiClient.GetInstanceWithResponse(ctx, instID, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(getResp.StatusCode()).To(Equal(http.StatusOK))
			Expect(getResp.JSON200.DeletionStatus).To(BeNil())
		})

		It("returns 204 on deferred delete when SP fails and marks instance SCHEDULED", func() {
			createInstance()
			clearDeleteStubAndStubFailure()

			deferred := true
			deleteResp, err := rmApiClient.DeleteInstanceWithResponse(ctx, instID, &resource_manager.DeleteInstanceParams{
				Deferred: &deferred,
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(deleteResp.StatusCode()).To(Equal(http.StatusNoContent))

			// Instance should be hidden from default GET
			getResp, err := rmApiClient.GetInstanceWithResponse(ctx, instID, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(getResp.StatusCode()).To(Equal(http.StatusNotFound))

			// Instance should be visible with show_deleted=true
			showDeleted := true
			getResp, err = rmApiClient.GetInstanceWithResponse(ctx, instID, &resource_manager.GetInstanceParams{
				ShowDeleted: &showDeleted,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(getResp.StatusCode()).To(Equal(http.StatusOK))
			Expect(getResp.JSON200.DeletionStatus).NotTo(BeNil())
			Expect(*getResp.JSON200.DeletionStatus).To(Equal(resource_manager.SCHEDULED))
		})

		It("marks instance SCHEDULED on deferred delete even when SP is available", func() {
			createInstance()

			deferred := true
			deleteResp, err := rmApiClient.DeleteInstanceWithResponse(ctx, instID, &resource_manager.DeleteInstanceParams{
				Deferred: &deferred,
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(deleteResp.StatusCode()).To(Equal(http.StatusNoContent))

			// Instance should be marked SCHEDULED, not hard-deleted
			showDeleted := true
			getResp, err := rmApiClient.GetInstanceWithResponse(ctx, instID, &resource_manager.GetInstanceParams{
				ShowDeleted: &showDeleted,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(getResp.StatusCode()).To(Equal(http.StatusOK))
			Expect(string(*getResp.JSON200.DeletionStatus)).To(Equal("SCHEDULED"))
		})

		It("excludes soft-deleted instances from default LIST", func() {
			createInstance()
			clearDeleteStubAndStubFailure()

			deferred := true
			deleteResp, err := rmApiClient.DeleteInstanceWithResponse(ctx, instID, &resource_manager.DeleteInstanceParams{
				Deferred: &deferred,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(deleteResp.StatusCode()).To(Equal(http.StatusNoContent))

			// Default LIST should not include the soft-deleted instance
			listResp, err := rmApiClient.ListInstancesWithResponse(ctx, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(listResp.StatusCode()).To(Equal(http.StatusOK))

			if listResp.JSON200.Instances != nil {
				for _, inst := range *listResp.JSON200.Instances {
					Expect(*inst.Id).NotTo(Equal(instID))
				}
			}
		})

		It("includes soft-deleted instances in LIST with show_deleted=true", func() {
			createInstance()
			clearDeleteStubAndStubFailure()

			deferred := true
			deleteResp, err := rmApiClient.DeleteInstanceWithResponse(ctx, instID, &resource_manager.DeleteInstanceParams{
				Deferred: &deferred,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(deleteResp.StatusCode()).To(Equal(http.StatusNoContent))

			// LIST with show_deleted=true should include the soft-deleted instance
			showDeleted := true
			listResp, err := rmApiClient.ListInstancesWithResponse(ctx, &resource_manager.ListInstancesParams{
				ShowDeleted: &showDeleted,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(listResp.StatusCode()).To(Equal(http.StatusOK))
			Expect(listResp.JSON200.Instances).NotTo(BeNil())

			ids := make([]string, len(*listResp.JSON200.Instances))
			for i, inst := range *listResp.JSON200.Instances {
				ids[i] = *inst.Id
			}
			Expect(ids).To(ContainElement(instID))
		})

		It("transitions to PENDING_PROVIDER when provider goes down and back to SCHEDULED on recovery", func() {
			createInstance()
			clearDeleteStubAndStubFailure()

			// Deferred delete → SCHEDULED
			deferred := true
			deleteResp, err := rmApiClient.DeleteInstanceWithResponse(ctx, instID, &resource_manager.DeleteInstanceParams{
				Deferred: &deferred,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(deleteResp.StatusCode()).To(Equal(http.StatusNoContent))

			showDeleted := true
			getResp, err := rmApiClient.GetInstanceWithResponse(ctx, instID, &resource_manager.GetInstanceParams{
				ShowDeleted: &showDeleted,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(getResp.StatusCode()).To(Equal(http.StatusOK))
			Expect(*getResp.JSON200.DeletionStatus).To(Equal(resource_manager.SCHEDULED))

			// Remove health stub → provider becomes NotReady
			resetHealthStubs()
			waitForProviderNotReady(apiClient, ctx, providerID)

			// Instance should transition to PENDING_PROVIDER
			Eventually(func() resource_manager.ServiceTypeInstanceDeletionStatus {
				resp, err := rmApiClient.GetInstanceWithResponse(ctx, instID, &resource_manager.GetInstanceParams{
					ShowDeleted: &showDeleted,
				})
				if err != nil || resp.StatusCode() != http.StatusOK || resp.JSON200 == nil || resp.JSON200.DeletionStatus == nil {
					return ""
				}
				return *resp.JSON200.DeletionStatus
			}, 30*time.Second, 1*time.Second).Should(Equal(resource_manager.PENDINGPROVIDER))

			// Restore health stub → provider recovers
			stubProviderHealthEndpoint()
			waitForProviderReady(apiClient, ctx, providerID)

			// Instance should transition back to SCHEDULED
			Eventually(func() resource_manager.ServiceTypeInstanceDeletionStatus {
				resp, err := rmApiClient.GetInstanceWithResponse(ctx, instID, &resource_manager.GetInstanceParams{
					ShowDeleted: &showDeleted,
				})
				if err != nil || resp.StatusCode() != http.StatusOK || resp.JSON200 == nil || resp.JSON200.DeletionStatus == nil {
					return ""
				}
				return *resp.JSON200.DeletionStatus
			}, 30*time.Second, 1*time.Second).Should(Equal(resource_manager.SCHEDULED))

			// Clean up: restore delete stub and hard-delete
			resetDeleteStubs()
			stubProviderDeleteInstance()

			deleteResp, err = rmApiClient.DeleteInstanceWithResponse(ctx, instID, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(deleteResp.StatusCode()).To(Equal(http.StatusNoContent))

			getResp, err = rmApiClient.GetInstanceWithResponse(ctx, instID, &resource_manager.GetInstanceParams{
				ShowDeleted: &showDeleted,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(getResp.StatusCode()).To(Equal(http.StatusNotFound))
		})

		It("can re-delete a soft-deleted instance when SP becomes available", func() {
			createInstance()
			clearDeleteStubAndStubFailure()

			// First delete: deferred, SP fails -> mark SCHEDULED
			deferred := true
			deleteResp, err := rmApiClient.DeleteInstanceWithResponse(ctx, instID, &resource_manager.DeleteInstanceParams{
				Deferred: &deferred,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(deleteResp.StatusCode()).To(Equal(http.StatusNoContent))

			// Restore SP delete stub to succeed
			resetDeleteStubs()
			stubProviderDeleteInstance()

			// Second delete: should hard-delete the SCHEDULED instance
			deleteResp, err = rmApiClient.DeleteInstanceWithResponse(ctx, instID, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(deleteResp.StatusCode()).To(Equal(http.StatusNoContent))

			// Instance should be fully gone
			showDeleted := true
			getResp, err := rmApiClient.GetInstanceWithResponse(ctx, instID, &resource_manager.GetInstanceParams{
				ShowDeleted: &showDeleted,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(getResp.StatusCode()).To(Equal(http.StatusNotFound))
		})
	})
})
