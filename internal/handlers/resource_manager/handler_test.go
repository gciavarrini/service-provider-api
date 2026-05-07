package resource_manager_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	server "github.com/dcm-project/service-provider-manager/internal/api/server/resource_manager"
	rmhandlers "github.com/dcm-project/service-provider-manager/internal/handlers/resource_manager"
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

var _ = Describe("Resource Manager Handler", func() {
	var (
		db             *gorm.DB
		handler        *rmhandlers.Handler
		ctx            context.Context
		mockProvider   *httptest.Server
		providerCalled bool
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
		mockProvider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
			ID:          uuid.New().String(),
			Name:        "test-provider",
			ServiceType: "vm",
			Endpoint:    mockProvider.URL,
		}
		Expect(db.Create(&provider).Error).NotTo(HaveOccurred())

		dataStore := store.NewStore(db)
		instanceService := rmsvc.NewInstanceService(dataStore, resty.New().
			SetTimeout(5*time.Second).
			SetRetryCount(0))
		handler = rmhandlers.NewHandler(instanceService)
		ctx = context.Background()
	})

	AfterEach(func() {
		mockProvider.Close()
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})

	Describe("GetHealth", func() {
		It("returns ok", func() {
			resp, err := handler.GetHealth(ctx, server.GetHealthRequestObject{})

			Expect(err).NotTo(HaveOccurred())
			jsonResp, ok := resp.(server.GetHealth200JSONResponse)
			Expect(ok).To(BeTrue())
			Expect(*jsonResp.Status).To(Equal("ok"))
		})
	})

	Describe("CreateInstance", func() {
		It("creates and returns 201", func() {
			req := server.CreateInstanceRequestObject{
				Body: &server.ServiceTypeInstance{
					ProviderName: "test-provider",
					Spec:         map[string]interface{}{"cpu": 2, "memory": "4GB", "service_type": "vm"},
				},
			}

			resp, err := handler.CreateInstance(ctx, req)

			Expect(err).NotTo(HaveOccurred())
			jsonResp, ok := resp.(server.CreateInstance201JSONResponse)
			Expect(ok).To(BeTrue())
			Expect(jsonResp.ProviderName).To(Equal("test-provider"))
			Expect(jsonResp.Id).NotTo(BeNil())
			Expect(providerCalled).To(BeTrue())
		})

		It("creates with specified ID", func() {
			specifiedID := uuid.New().String()
			req := server.CreateInstanceRequestObject{
				Params: server.CreateInstanceParams{Id: &specifiedID},
				Body: &server.ServiceTypeInstance{
					ProviderName: "test-provider",
					Spec:         map[string]interface{}{"cpu": 1, "service_type": "vm"},
				},
			}

			resp, err := handler.CreateInstance(ctx, req)

			Expect(err).NotTo(HaveOccurred())
			jsonResp, ok := resp.(server.CreateInstance201JSONResponse)
			Expect(ok).To(BeTrue())
			Expect(*jsonResp.Id).To(Equal(specifiedID))
		})

		It("returns 409 for duplicate ID", func() {
			specifiedID := uuid.New().String()
			req := server.CreateInstanceRequestObject{
				Params: server.CreateInstanceParams{Id: &specifiedID},
				Body: &server.ServiceTypeInstance{
					ProviderName: "test-provider",
					Spec:         map[string]interface{}{"cpu": 1, "service_type": "vm"},
				},
			}

			// First creation should succeed
			resp1, err := handler.CreateInstance(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			_, ok := resp1.(server.CreateInstance201JSONResponse)
			Expect(ok).To(BeTrue())

			// Second creation with same ID should fail
			resp2, err := handler.CreateInstance(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			_, ok = resp2.(server.CreateInstance409ApplicationProblemPlusJSONResponse)
			Expect(ok).To(BeTrue())
		})

		It("returns 404 for non-existent provider", func() {
			req := server.CreateInstanceRequestObject{
				Body: &server.ServiceTypeInstance{
					ProviderName: "non-existent-provider",
					Spec:         map[string]interface{}{"cpu": 1, "service_type": "vm"},
				},
			}

			resp, err := handler.CreateInstance(ctx, req)

			Expect(err).NotTo(HaveOccurred())
			_, ok := resp.(server.CreateInstance404ApplicationProblemPlusJSONResponse)
			Expect(ok).To(BeTrue())
		})

		It("returns 400 when spec is missing service_type", func() {
			req := server.CreateInstanceRequestObject{
				Body: &server.ServiceTypeInstance{
					ProviderName: "test-provider",
					Spec:         map[string]interface{}{"cpu": 2},
				},
			}

			resp, err := handler.CreateInstance(ctx, req)

			Expect(err).NotTo(HaveOccurred())
			_, ok := resp.(server.CreateInstance400ApplicationProblemPlusJSONResponse)
			Expect(ok).To(BeTrue())
			Expect(providerCalled).To(BeFalse())
		})

		It("returns 400 when spec.service_type is not a string", func() {
			req := server.CreateInstanceRequestObject{
				Body: &server.ServiceTypeInstance{
					ProviderName: "test-provider",
					Spec:         map[string]interface{}{"cpu": 2, "service_type": 42},
				},
			}

			resp, err := handler.CreateInstance(ctx, req)

			Expect(err).NotTo(HaveOccurred())
			_, ok := resp.(server.CreateInstance400ApplicationProblemPlusJSONResponse)
			Expect(ok).To(BeTrue())
			Expect(providerCalled).To(BeFalse())
		})

		It("returns 400 when spec.service_type is an empty string", func() {
			req := server.CreateInstanceRequestObject{
				Body: &server.ServiceTypeInstance{
					ProviderName: "test-provider",
					Spec:         map[string]interface{}{"cpu": 2, "service_type": ""},
				},
			}

			resp, err := handler.CreateInstance(ctx, req)

			Expect(err).NotTo(HaveOccurred())
			_, ok := resp.(server.CreateInstance400ApplicationProblemPlusJSONResponse)
			Expect(ok).To(BeTrue())
			Expect(providerCalled).To(BeFalse())
		})
	})

	Describe("GetInstance", func() {
		It("returns instance", func() {
			// Create an instance first
			createReq := server.CreateInstanceRequestObject{
				Body: &server.ServiceTypeInstance{
					ProviderName: "test-provider",
					Spec:         map[string]interface{}{"cpu": 2, "service_type": "vm"},
				},
			}
			createResp, _ := handler.CreateInstance(ctx, createReq)
			created := createResp.(server.CreateInstance201JSONResponse)

			req := server.GetInstanceRequestObject{
				InstanceId: *created.Id,
			}

			resp, err := handler.GetInstance(ctx, req)

			Expect(err).NotTo(HaveOccurred())
			jsonResp, ok := resp.(server.GetInstance200JSONResponse)
			Expect(ok).To(BeTrue())
			Expect(jsonResp.ProviderName).To(Equal("test-provider"))
		})

		It("returns 404 for non-existent instance", func() {
			req := server.GetInstanceRequestObject{
				InstanceId: uuid.New().String(),
			}

			resp, err := handler.GetInstance(ctx, req)

			Expect(err).NotTo(HaveOccurred())
			_, ok := resp.(server.GetInstance404ApplicationProblemPlusJSONResponse)
			Expect(ok).To(BeTrue())
		})
	})

	Describe("ListInstances", func() {
		It("returns empty list initially", func() {
			req := server.ListInstancesRequestObject{}

			resp, err := handler.ListInstances(ctx, req)

			Expect(err).NotTo(HaveOccurred())
			jsonResp, ok := resp.(server.ListInstances200JSONResponse)
			Expect(ok).To(BeTrue())
			Expect(*jsonResp.Instances).To(BeEmpty())
		})

		It("returns instances", func() {
			// Create instances first
			for i := 0; i < 3; i++ {
				createReq := server.CreateInstanceRequestObject{
					Body: &server.ServiceTypeInstance{
						ProviderName: "test-provider",
						Spec:         map[string]interface{}{"cpu": i + 1, "service_type": "vm"},
					},
				}
				_, err := handler.CreateInstance(ctx, createReq)
				Expect(err).NotTo(HaveOccurred())
			}

			resp, err := handler.ListInstances(ctx, server.ListInstancesRequestObject{})

			Expect(err).NotTo(HaveOccurred())
			jsonResp, ok := resp.(server.ListInstances200JSONResponse)
			Expect(ok).To(BeTrue())
			Expect(*jsonResp.Instances).To(HaveLen(3))
		})

		It("filters by service_type", func() {
			containerProvider := model.Provider{
				ID:          uuid.New().String(),
				Name:        "container-provider",
				ServiceType: "container",
				Endpoint:    mockProvider.URL,
			}
			Expect(db.Create(&containerProvider).Error).NotTo(HaveOccurred())

			// Create vm instances with service_type in spec
			for i := 0; i < 2; i++ {
				createReq := server.CreateInstanceRequestObject{
					Body: &server.ServiceTypeInstance{
						ProviderName: "test-provider",
						Spec:         map[string]interface{}{"cpu": i + 1, "service_type": "vm"},
					},
				}
				_, err := handler.CreateInstance(ctx, createReq)
				Expect(err).NotTo(HaveOccurred())
			}

			// Create container instance with service_type in spec
			createReq := server.CreateInstanceRequestObject{
				Body: &server.ServiceTypeInstance{
					ProviderName: "container-provider",
					Spec:         map[string]interface{}{"image": "nginx", "service_type": "container"},
				},
			}
			_, err := handler.CreateInstance(ctx, createReq)
			Expect(err).NotTo(HaveOccurred())

			vmType := "vm"
			resp, err := handler.ListInstances(ctx, server.ListInstancesRequestObject{
				Params: server.ListInstancesParams{ServiceType: &vmType},
			})
			Expect(err).NotTo(HaveOccurred())
			jsonResp, ok := resp.(server.ListInstances200JSONResponse)
			Expect(ok).To(BeTrue())
			Expect(*jsonResp.Instances).To(HaveLen(2))

			containerType := "container"
			resp, err = handler.ListInstances(ctx, server.ListInstancesRequestObject{
				Params: server.ListInstancesParams{ServiceType: &containerType},
			})
			Expect(err).NotTo(HaveOccurred())
			jsonResp, ok = resp.(server.ListInstances200JSONResponse)
			Expect(ok).To(BeTrue())
			Expect(*jsonResp.Instances).To(HaveLen(1))
		})

		It("respects max page size and returns next page token", func() {
			// Create 5 instances
			for i := 0; i < 5; i++ {
				createReq := server.CreateInstanceRequestObject{
					Body: &server.ServiceTypeInstance{
						ProviderName: "test-provider",
						Spec:         map[string]interface{}{"cpu": i + 1, "service_type": "vm"},
					},
				}
				_, err := handler.CreateInstance(ctx, createReq)
				Expect(err).NotTo(HaveOccurred())
			}

			// First page: request 2 items
			maxPageSize := 2
			req := server.ListInstancesRequestObject{
				Params: server.ListInstancesParams{MaxPageSize: &maxPageSize},
			}

			resp, err := handler.ListInstances(ctx, req)

			Expect(err).NotTo(HaveOccurred())
			jsonResp, ok := resp.(server.ListInstances200JSONResponse)
			Expect(ok).To(BeTrue())
			Expect(*jsonResp.Instances).To(HaveLen(2))
			Expect(jsonResp.NextPageToken).NotTo(BeNil())
			Expect(*jsonResp.NextPageToken).NotTo(BeEmpty())

			// Second page: use the page token
			req2 := server.ListInstancesRequestObject{
				Params: server.ListInstancesParams{
					MaxPageSize: &maxPageSize,
					PageToken:   jsonResp.NextPageToken,
				},
			}

			resp2, err := handler.ListInstances(ctx, req2)

			Expect(err).NotTo(HaveOccurred())
			jsonResp2, ok := resp2.(server.ListInstances200JSONResponse)
			Expect(ok).To(BeTrue())
			Expect(*jsonResp2.Instances).To(HaveLen(2))
			Expect(jsonResp2.NextPageToken).NotTo(BeNil())

			// Third page: should have 1 item and no next token
			req3 := server.ListInstancesRequestObject{
				Params: server.ListInstancesParams{
					MaxPageSize: &maxPageSize,
					PageToken:   jsonResp2.NextPageToken,
				},
			}

			resp3, err := handler.ListInstances(ctx, req3)

			Expect(err).NotTo(HaveOccurred())
			jsonResp3, ok := resp3.(server.ListInstances200JSONResponse)
			Expect(ok).To(BeTrue())
			Expect(*jsonResp3.Instances).To(HaveLen(1))
			Expect(jsonResp3.NextPageToken).To(BeNil())
		})
	})

	Describe("DeleteInstance", func() {
		It("deletes instance and returns 204", func() {
			// Create an instance first
			createReq := server.CreateInstanceRequestObject{
				Body: &server.ServiceTypeInstance{
					ProviderName: "test-provider",
					Spec:         map[string]interface{}{"cpu": 2, "service_type": "vm"},
				},
			}
			createResp, _ := handler.CreateInstance(ctx, createReq)
			created := createResp.(server.CreateInstance201JSONResponse)

			req := server.DeleteInstanceRequestObject{
				InstanceId: *created.Id,
			}

			resp, err := handler.DeleteInstance(ctx, req)

			Expect(err).NotTo(HaveOccurred())
			_, ok := resp.(server.DeleteInstance204Response)
			Expect(ok).To(BeTrue())

			// Verify it's deleted
			getResp, _ := handler.GetInstance(ctx, server.GetInstanceRequestObject{InstanceId: *created.Id})
			_, ok = getResp.(server.GetInstance404ApplicationProblemPlusJSONResponse)
			Expect(ok).To(BeTrue())
		})

		It("returns 404 for non-existent instance", func() {
			req := server.DeleteInstanceRequestObject{
				InstanceId: uuid.New().String(),
			}

			resp, err := handler.DeleteInstance(ctx, req)

			Expect(err).NotTo(HaveOccurred())
			_, ok := resp.(server.DeleteInstance404ApplicationProblemPlusJSONResponse)
			Expect(ok).To(BeTrue())
		})
	})
})
