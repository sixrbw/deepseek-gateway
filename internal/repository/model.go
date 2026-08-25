package entity

import (
	"time"

	"modelgate/internal/config"
)

type Model struct {
	ID            string                 `json:"id"`
	Name          string                 `json:"name"`
	Description   string                 `json:"description"`
	Enabled       bool                   `json:"enabled"`
	ContextWindow int                    `json:"context_window"`
	ModelParams   map[string]interface{} `json:"model_params"`
	CreatedAt     time.Time              `json:"created_at"`
	UpdatedAt     time.Time              `json:"updated_at"`
}

type ModelCreateRequest struct {
	ID            string                 `json:"id" binding:"required"`
	Name          string                 `json:"name" binding:"required"`
	Description   string                 `json:"description"`
	Enabled       bool                   `json:"enabled"`
	ContextWindow int                    `json:"context_window"`
	ModelParams   map[string]interface{} `json:"model_params"`
	Backends      []BackendCreateInput   `json:"backends"`
}

type ModelUpdateRequest struct {
	Name          string                 `json:"name"`
	Description   string                 `json:"description"`
	Enabled       *bool                  `json:"enabled"`
	ContextWindow *int                   `json:"context_window"`
	ModelParams   map[string]interface{} `json:"model_params"`
}

type BackendCreateInput struct {
	ID             string `json:"id" binding:"required"`
	Name           string `json:"name"`
	BaseURL        string `json:"base_url" binding:"required,url"`
	APIKey         string `json:"api_key"`
	ModelName      string `json:"model_name"`
	SourcePlatform string `json:"source_platform"`
	SourceGroup    string `json:"source_group"`
	Weight         int    `json:"weight"`
	Region         string `json:"region"`
	Enabled        bool   `json:"enabled"`
	MaxConcurrency int    `json:"max_concurrency"`
}

type GatewayImportRequest struct {
	BaseURL        string `json:"base_url" binding:"required,url"`
	APIKey         string `json:"api_key"`
	Prefix         string `json:"prefix" binding:"required"`
	SourcePlatform string `json:"source_platform"`
	SourceGroup    string `json:"source_group"`
}

// ModelStore 模型配置数据访问层 - 现在从 ConfigManager 读取
type ModelStore struct {
	cm *config.ConfigManager
}

func NewModelStore(cm *config.ConfigManager) *ModelStore {
	return &ModelStore{cm: cm}
}

// configToModel 将配置模型转换为数据模型
func (s *ModelStore) configToModel(cm config.ModelConfig) *Model {
	return &Model{
		ID:            cm.ID,
		Name:          cm.Name,
		Description:   cm.Description,
		Enabled:       cm.Enabled,
		ContextWindow: cm.ContextWindow,
		ModelParams:   cm.ModelParams,
		CreatedAt:     time.Now(), // 配置中无时间信息，使用当前时间
		UpdatedAt:     time.Now(),
	}
}

// modelToConfig 将数据模型转换为配置模型
func (s *ModelStore) modelToConfig(m *Model) config.ModelConfig {
	return config.ModelConfig{
		ID:            m.ID,
		Name:          m.Name,
		Description:   m.Description,
		Enabled:       m.Enabled,
		ContextWindow: m.ContextWindow,
		ModelParams:   m.ModelParams,
	}
}

func (s *ModelStore) Create(model *Model) error {
	cfg := s.modelToConfig(model)
	if err := s.cm.AddModel(cfg); err != nil {
		return err
	}

	// 更新时间戳
	model.CreatedAt = time.Now()
	model.UpdatedAt = time.Now()
	return nil
}

func (s *ModelStore) GetByID(id string) (*Model, error) {
	cfg := s.cm.GetModelByID(id)
	if cfg == nil {
		return nil, nil
	}

	return s.configToModel(*cfg), nil
}

func (s *ModelStore) List() ([]*Model, error) {
	models := s.cm.GetModels()
	result := make([]*Model, len(models))
	for i, m := range models {
		model := s.configToModel(m)
		result[i] = model
	}
	return result, nil
}

func (s *ModelStore) ListEnabled() ([]*Model, error) {
	models := s.cm.GetModels()
	var result []*Model
	for _, m := range models {
		if m.Enabled {
			model := s.configToModel(m)
			result = append(result, model)
		}
	}
	return result, nil
}

func (s *ModelStore) Update(model *Model) error {
	// 先获取现有配置以保留 backends
	existing := s.cm.GetModelByID(model.ID)
	if existing == nil {
		return nil
	}

	cfg := s.modelToConfig(model)
	cfg.Backends = existing.Backends // 保留后端配置

	if err := s.cm.UpdateModel(cfg); err != nil {
		return err
	}

	model.UpdatedAt = time.Now()
	return nil
}

func (s *ModelStore) Delete(id string) error {
	return s.cm.DeleteModel(id)
}
