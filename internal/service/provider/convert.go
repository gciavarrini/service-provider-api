package provider

import (
	"encoding/json"

	providerserver "github.com/dcm-project/service-provider-manager/internal/api/server/provider"
	"github.com/dcm-project/service-provider-manager/internal/service"
	"github.com/dcm-project/service-provider-manager/internal/store/model"
)

// ModelToProvider converts a database model to an API response type.
func ModelToProvider(m *model.Provider) *providerserver.Provider {
	p := &providerserver.Provider{
		Id:            &m.ID,
		Name:          m.Name,
		ServiceType:   m.ServiceType,
		SchemaVersion: m.SchemaVersion,
		Endpoint:      m.Endpoint,
		DisplayName:   m.DisplayName,
		HealthStatus:  m.HealthStatus.StringPtr(),
		CreateTime:    service.PtrTime(m.CreateTime),
		UpdateTime:    service.PtrTime(m.UpdateTime),
	}
	if m.Metadata != nil {
		b, err := json.Marshal(m.Metadata)
		if err == nil {
			var meta providerserver.ProviderMetadata
			if err := json.Unmarshal(b, &meta); err == nil {
				p.Metadata = &meta
			}
		}
	}
	if opBytes, err := json.Marshal(m.Operations); err == nil && string(opBytes) != "null" {
		var ops []string
		if err := json.Unmarshal(opBytes, &ops); err == nil {
			p.Operations = &ops
		}
	}
	return p
}

// ModelToProviderWithStatus converts a database model to an API response with status.
func ModelToProviderWithStatus(m *model.Provider, status providerserver.ProviderStatus) *providerserver.Provider {
	p := ModelToProvider(m)
	p.Status = &status
	return p
}

// ProviderToModel converts an API request to a database model.
func ProviderToModel(req *providerserver.Provider, id string) model.Provider {
	m := model.Provider{
		ID:            id,
		Name:          req.Name,
		ServiceType:   req.ServiceType,
		SchemaVersion: req.SchemaVersion,
		Endpoint:      req.Endpoint,
		DisplayName:   req.DisplayName,
	}
	if req.Metadata != nil {
		if metaMap, err := providerMetadataToMap(req.Metadata); err == nil {
			m.Metadata = metaMap
		}
	}
	if req.Operations != nil {
		m.Operations = *req.Operations
	}
	return m
}

func applyProviderRequestToModel(dest *model.Provider, req *providerserver.Provider) {
	dest.Name = req.Name
	dest.ServiceType = req.ServiceType
	dest.SchemaVersion = req.SchemaVersion
	dest.Endpoint = req.Endpoint
	dest.DisplayName = req.DisplayName
	dest.Metadata = nil
	if req.Metadata != nil {
		if metaMap, err := providerMetadataToMap(req.Metadata); err == nil {
			dest.Metadata = metaMap
		}
	}
	dest.Operations = nil
	if req.Operations != nil {
		dest.Operations = *req.Operations
	}
}

func providerMetadataToMap(meta *providerserver.ProviderMetadata) (map[string]interface{}, error) {
	b, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}
