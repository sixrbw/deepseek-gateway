package entity

import (
	"database/sql"
	"time"

	"modelgate/internal/config"
)

// Backend represents a backend instance for a model
type Backend struct {
	ID             string       `json:"id"`
	ModelID        string       `json:"model_id"`
	BaseURL        string       `json:"base_url"`
	APIKey         string       `json:"-"`       // Never return API key in JSON
	APIKeyMasked   string       `json:"api_key"` // Masked version for display
	ModelName      string       `json:"model_name"`
	SourcePlatform string       `json:"source_platform"`
	SourceGroup    string       `json:"source_group"`
	Weight         int          `json:"weight"`
	Enabled        bool         `json:"enabled"`
	Healthy        bool         `json:"healthy"`
	MaxConcurrency int          `json:"max_concurrency"` // 该后端最大并发数，0 表示不限制
	LastCheckAt    sql.NullTime `json:"last_check_at"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
}

// BackendCreateRequest represents a request to create a backend
type BackendCreateRequest struct {
	ID             string `json:"id" binding:"required"`
	BaseURL        string `json:"base_url" binding:"required,url"`
	APIKey         string `json:"api_key"`
	ModelName      string `json:"model_name"`
	SourcePlatform string `json:"source_platform"`
	SourceGroup    string `json:"source_group"`
	Weight         int    `json:"weight"`
	Enabled        bool   `json:"enabled"`
	MaxConcurrency int    `json:"max_concurrency"`
}

// BackendUpdateRequest represents a request to update a backend
type BackendUpdateRequest struct {
	BaseURL        string `json:"base_url" binding:"omitempty,url"`
	APIKey         string `json:"api_key"`
	ModelName      string `json:"model_name"`
	SourcePlatform string `json:"source_platform"`
	SourceGroup    string `json:"source_group"`
	Weight         int    `json:"weight"`
	Enabled        *bool  `json:"enabled"`
	MaxConcurrency int    `json:"max_concurrency"`
}

type BackendBatchDeleteRequest struct {
	BackendIDs []string `json:"backend_ids" binding:"required"`
}

// BackendStore handles backend configuration - now from ConfigManager
type BackendStore struct {
	cm *config.ConfigManager
}

func NewBackendStore(cm *config.ConfigManager) *BackendStore {
	return &BackendStore{cm: cm}
}

// configToBackend 将配置后端转换为数据后端
func (s *BackendStore) configToBackend(modelID string, cfg config.BackendConfig) *Backend {
	// 生成脱敏后的 API Key
	masked := ""
	if cfg.APIKey != "" {
		if len(cfg.APIKey) > 4 {
			masked = "***" + cfg.APIKey[len(cfg.APIKey)-4:]
		} else {
			masked = "***"
		}
	}
	return &Backend{
		ID:             cfg.ID,
		ModelID:        modelID,
		BaseURL:        cfg.BaseURL,
		APIKey:         cfg.APIKey,
		APIKeyMasked:   masked,
		ModelName:      cfg.ModelName,
		SourcePlatform: cfg.SourcePlatform,
		SourceGroup:    cfg.SourceGroup,
		Weight:         cfg.Weight,
		Enabled:        cfg.Enabled,
		Healthy:        true,
		MaxConcurrency: cfg.MaxConcurrency,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
}

// backendToConfig 将数据后端转换为配置后端
func (s *BackendStore) backendToConfig(backend *Backend) config.BackendConfig {
	return config.BackendConfig{
		ID:             backend.ID,
		BaseURL:        backend.BaseURL,
		APIKey:         backend.APIKey,
		ModelName:      backend.ModelName,
		SourcePlatform: backend.SourcePlatform,
		SourceGroup:    backend.SourceGroup,
		Weight:         backend.Weight,
		Enabled:        backend.Enabled,
		MaxConcurrency: backend.MaxConcurrency,
	}
}

// Create creates a new backend
func (s *BackendStore) Create(backend *Backend) error {
	cfg := s.backendToConfig(backend)
	if err := s.cm.AddBackend(backend.ModelID, cfg); err != nil {
		return err
	}
	backend.CreatedAt = time.Now()
	backend.UpdatedAt = time.Now()
	return nil
}

// GetByID retrieves a backend by ID
func (s *BackendStore) GetByID(id string) (*Backend, error) {
	models := s.cm.GetModels()
	for _, m := range models {
		for _, b := range m.Backends {
			if b.ID == id {
				return s.configToBackend(m.ID, b), nil
			}
		}
	}
	return nil, nil
}

// List retrieves all backends
func (s *BackendStore) List() ([]*Backend, error) {
	models := s.cm.GetModels()
	var backends []*Backend
	for _, m := range models {
		for _, b := range m.Backends {
			backends = append(backends, s.configToBackend(m.ID, b))
		}
	}
	return backends, nil
}

// ListByModel retrieves all backends for a specific model
func (s *BackendStore) ListByModel(modelID string) ([]*Backend, error) {
	backends := s.cm.GetBackendsByModel(modelID)
	result := make([]*Backend, len(backends))
	for i, b := range backends {
		result[i] = s.configToBackend(modelID, b)
	}
	return result, nil
}

// ListEnabled retrieves all enabled backends
func (s *BackendStore) ListEnabled() ([]*Backend, error) {
	allBackends, err := s.List()
	if err != nil {
		return nil, err
	}

	var enabled []*Backend
	for _, b := range allBackends {
		if b.Enabled {
			enabled = append(enabled, b)
		}
	}
	return enabled, nil
}

// ListEnabledByModel retrieves all enabled backends for a specific model
func (s *BackendStore) ListEnabledByModel(modelID string) ([]*Backend, error) {
	backends, err := s.ListByModel(modelID)
	if err != nil {
		return nil, err
	}

	var enabled []*Backend
	for _, b := range backends {
		if b.Enabled {
			enabled = append(enabled, b)
		}
	}
	return enabled, nil
}

// Update updates a backend
func (s *BackendStore) Update(backend *Backend) error {
	cfg := s.backendToConfig(backend)
	if err := s.cm.UpdateBackend(backend.ModelID, cfg); err != nil {
		return err
	}
	backend.UpdatedAt = time.Now()
	return nil
}

// Delete deletes a backend
func (s *BackendStore) Delete(id string) error {
	// Need to find which model this backend belongs to
	backend, err := s.GetByID(id)
	if err != nil {
		return err
	}
	if backend == nil {
		return nil
	}
	return s.cm.DeleteBackend(backend.ModelID, id)
}

func (s *BackendStore) DeleteBatch(modelID string, ids []string) error {
	return s.cm.DeleteBackends(modelID, ids)
}

// UpdateHealth updates the health status of a backend
// Note: Health status is now stored in LoadBalancer, not in config
// This method is kept for interface compatibility but does nothing
func (s *BackendStore) UpdateHealth(id string, healthy bool) error {
	// Health status is managed by LoadBalancer in memory
	// We don't persist it to config
	return nil
}

// DeleteByModel deletes all backends for a model (used when model is deleted)
func (s *BackendStore) DeleteByModel(modelID string) error {
	// When a model is deleted, its backends are automatically removed
	// This is handled by ConfigManager.DeleteModel
	return nil
}
