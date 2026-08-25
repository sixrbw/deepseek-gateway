package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// ConfigEvent 配置变更事件
type ConfigEvent struct {
	Type string      // "models", "policies", "all"
	Data interface{} // 可选的事件数据
}

// ConfigManager 配置管理器 - 管理配置的加载、保存和热重载
type ConfigManager struct {
	cfg      *Config
	path     string
	mu       sync.RWMutex
	watchers []chan<- ConfigEvent
	watchMu  sync.Mutex
}

// NewManager 创建新的配置管理器
func NewManager(cfg *Config, path string) *ConfigManager {
	return &ConfigManager{
		cfg:      cfg,
		path:     path,
		watchers: make([]chan<- ConfigEvent, 0),
	}
}

// GetConfig 获取当前配置的只读副本
func (cm *ConfigManager) GetConfig() *Config {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.deepCopyConfig(cm.cfg)
}

// GetModels 获取模型配置列表
func (cm *ConfigManager) GetModels() []ModelConfig {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	models := make([]ModelConfig, len(cm.cfg.Models))
	for i, m := range cm.cfg.Models {
		models[i] = cm.deepCopyModel(m)
	}
	return models
}

// GetPolicies 获取配额策略列表
func (cm *ConfigManager) GetPolicies() []PolicyConfig {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	policies := make([]PolicyConfig, len(cm.cfg.Policies))
	copy(policies, cm.cfg.Policies)
	return policies
}

// Save 原子保存当前配置到文件
func (cm *ConfigManager) Save() error {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if err := cm.cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	return cm.saveToFile(cm.cfg)
}

func (cm *ConfigManager) saveToFile(cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	dir := filepath.Dir(cm.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	tmpPath := cm.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := os.Rename(tmpPath, cm.path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to commit config: %w", err)
	}

	return nil
}

// Subscribe 订阅配置变更事件
func (cm *ConfigManager) Subscribe() <-chan ConfigEvent {
	cm.watchMu.Lock()
	defer cm.watchMu.Unlock()

	ch := make(chan ConfigEvent, 10)
	cm.watchers = append(cm.watchers, ch)
	return ch
}

// Unsubscribe 取消订阅
func (cm *ConfigManager) Unsubscribe(ch <-chan ConfigEvent) {
	cm.watchMu.Lock()
	defer cm.watchMu.Unlock()

	for i, watcher := range cm.watchers {
		if interface{}(watcher) == interface{}(ch) {
			cm.watchers = append(cm.watchers[:i], cm.watchers[i+1:]...)
			close(watcher)
			break
		}
	}
}

func (cm *ConfigManager) notify(eventType string, data interface{}) {
	cm.watchMu.Lock()
	defer cm.watchMu.Unlock()

	event := ConfigEvent{Type: eventType, Data: data}
	for _, ch := range cm.watchers {
		select {
		case ch <- event:
		default:
		}
	}
}

// UpdateAndNotify 核心更新逻辑
func (cm *ConfigManager) updateAndNotify(eventType string, data interface{}, updateFn func(*Config) error) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// 1. 在临时副本上尝试更新并校验
	newCfg := cm.deepCopyConfig(cm.cfg)
	if err := updateFn(newCfg); err != nil {
		return err
	}

	if err := newCfg.Validate(); err != nil {
		return fmt.Errorf("update validation failed: %w", err)
	}

	// 2. 持久化
	if err := cm.saveToFile(newCfg); err != nil {
		return err
	}

	// 3. 应用到内存
	cm.cfg = newCfg

	// 4. 异步通知
	go cm.notify(eventType, data)

	return nil
}

// --- RESTORED CRUD METHODS WITH EXACT ORIGINAL BEHAVIOR ---

func (cm *ConfigManager) AddModel(model ModelConfig) error {
	return cm.updateAndNotify("models", cm.cfg.Models, func(c *Config) error {
		for _, m := range c.Models {
			if m.ID == model.ID {
				return fmt.Errorf("model %s already exists", model.ID)
			}
		}
		c.Models = append(c.Models, model)
		return nil
	})
}

func (cm *ConfigManager) UpdateModel(model ModelConfig) error {
	return cm.updateAndNotify("models", cm.cfg.Models, func(c *Config) error {
		for i, m := range c.Models {
			if m.ID == model.ID {
				c.Models[i] = model
				return nil
			}
		}
		return fmt.Errorf("model %s not found", model.ID)
	})
}

func (cm *ConfigManager) DeleteModel(modelID string) error {
	return cm.updateAndNotify("models", nil, func(c *Config) error {
		found := false
		newModels := make([]ModelConfig, 0, len(c.Models))
		for _, m := range c.Models {
			if m.ID == modelID {
				found = true
				continue
			}
			newModels = append(newModels, m)
		}
		if !found {
			return fmt.Errorf("model %s not found", modelID)
		}
		c.Models = newModels
		return nil
	})
}

func (cm *ConfigManager) AddBackend(modelID string, backend BackendConfig) error {
	return cm.updateAndNotify("models", cm.cfg.Models, func(c *Config) error {
		for i, m := range c.Models {
			if m.ID == modelID {
				for _, b := range m.Backends {
					if b.ID == backend.ID {
						return fmt.Errorf("backend %s already exists", backend.ID)
					}
				}
				c.Models[i].Backends = append(c.Models[i].Backends, backend)
				return nil
			}
		}
		return fmt.Errorf("model %s not found", modelID)
	})
}

func (cm *ConfigManager) UpdateBackend(modelID string, backend BackendConfig) error {
	return cm.updateAndNotify("models", cm.cfg.Models, func(c *Config) error {
		for i, m := range c.Models {
			if m.ID == modelID {
				for j, b := range m.Backends {
					if b.ID == backend.ID {
						c.Models[i].Backends[j] = backend
						return nil
					}
				}
				return fmt.Errorf("backend %s not found", backend.ID)
			}
		}
		return fmt.Errorf("model %s not found", modelID)
	})
}

func (cm *ConfigManager) DeleteBackend(modelID, backendID string) error {
	return cm.updateAndNotify("models", cm.cfg.Models, func(c *Config) error {
		for i, m := range c.Models {
			if m.ID == modelID {
				found := false
				newBackends := make([]BackendConfig, 0, len(m.Backends))
				for _, b := range m.Backends {
					if b.ID == backendID {
						found = true
						continue
					}
					newBackends = append(newBackends, b)
				}
				if !found {
					return fmt.Errorf("backend %s not found", backendID)
				}
				c.Models[i].Backends = newBackends
				return nil
			}
		}
		return fmt.Errorf("model %s not found", modelID)
	})
}

func (cm *ConfigManager) DeleteBackends(modelID string, backendIDs []string) error {
	return cm.updateAndNotify("models", cm.cfg.Models, func(c *Config) error {
		if len(backendIDs) == 0 {
			return fmt.Errorf("no backends specified")
		}

		targets := make(map[string]struct{}, len(backendIDs))
		for _, backendID := range backendIDs {
			targets[backendID] = struct{}{}
		}

		for i, m := range c.Models {
			if m.ID != modelID {
				continue
			}

			found := make(map[string]struct{}, len(targets))
			newBackends := make([]BackendConfig, 0, len(m.Backends))
			for _, b := range m.Backends {
				if _, ok := targets[b.ID]; ok {
					found[b.ID] = struct{}{}
					continue
				}
				newBackends = append(newBackends, b)
			}

			if len(found) != len(targets) {
				var missing []string
				for backendID := range targets {
					if _, ok := found[backendID]; !ok {
						missing = append(missing, backendID)
					}
				}
				return fmt.Errorf("backend(s) not found: %s", strings.Join(missing, ", "))
			}

			c.Models[i].Backends = newBackends
			return nil
		}

		return fmt.Errorf("model %s not found", modelID)
	})
}

func (cm *ConfigManager) AddPolicy(policy PolicyConfig) error {
	return cm.updateAndNotify("policies", cm.cfg.Policies, func(c *Config) error {
		for _, p := range c.Policies {
			if p.Name == policy.Name {
				return fmt.Errorf("policy %s already exists", policy.Name)
			}
		}
		c.Policies = append(c.Policies, policy)
		return nil
	})
}

func (cm *ConfigManager) UpdatePolicy(policy PolicyConfig) error {
	return cm.updateAndNotify("policies", cm.cfg.Policies, func(c *Config) error {
		for i, p := range c.Policies {
			if p.Name == policy.Name {
				c.Policies[i] = policy
				return nil
			}
		}
		return fmt.Errorf("policy %s not found", policy.Name)
	})
}

func (cm *ConfigManager) DeletePolicy(name string) error {
	return cm.updateAndNotify("policies", cm.cfg.Policies, func(c *Config) error {
		found := false
		newPolicies := make([]PolicyConfig, 0, len(c.Policies))
		for _, p := range c.Policies {
			if p.Name == name {
				found = true
				continue
			}
			newPolicies = append(newPolicies, p)
		}
		if !found {
			return fmt.Errorf("policy %s not found", name)
		}
		c.Policies = newPolicies
		return nil
	})
}

// --- READ METHODS ---

func (cm *ConfigManager) GetModelByID(id string) *ModelConfig {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	for _, m := range cm.cfg.Models {
		if m.ID == id {
			copy := cm.deepCopyModel(m)
			return &copy
		}
	}
	return nil
}

func (cm *ConfigManager) GetBackendByID(backendID string) *BackendConfig {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	for _, m := range cm.cfg.Models {
		for _, b := range m.Backends {
			if b.ID == backendID {
				return &b
			}
		}
	}
	return nil
}

func (cm *ConfigManager) GetBackendsByModel(modelID string) []BackendConfig {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	for _, m := range cm.cfg.Models {
		if m.ID == modelID {
			backends := make([]BackendConfig, len(m.Backends))
			copy(backends, m.Backends)
			return backends
		}
	}
	return nil
}

func (cm *ConfigManager) GetPolicyByName(name string) *PolicyConfig {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	for _, p := range cm.cfg.Policies {
		if p.Name == name {
			return &p
		}
	}
	return nil
}

func (cm *ConfigManager) UpdateSystemSettings(readTimeout, writeTimeout, idleTimeout time.Duration, frontend FrontendConfig, clientFilter ClientFilterConfig) error {
	return cm.updateAndNotify("all", nil, func(c *Config) error {
		c.Server.ReadTimeout = readTimeout
		c.Server.WriteTimeout = writeTimeout
		c.Server.IdleTimeout = idleTimeout
		c.Frontend = frontend
		c.ClientFilter = clientFilter
		return nil
	})
}

// UpdateTimeoutsAndFrontend 已废弃，保留向后兼容
func (cm *ConfigManager) UpdateTimeoutsAndFrontend(readTimeout, writeTimeout, idleTimeout time.Duration, frontend FrontendConfig) error {
	cfg := cm.GetConfig()
	return cm.UpdateSystemSettings(readTimeout, writeTimeout, idleTimeout, frontend, cfg.ClientFilter)
}

func (cm *ConfigManager) UpdateModels(models []ModelConfig) error {
	return cm.updateAndNotify("models", models, func(c *Config) error {
		c.Models = models
		return nil
	})
}

func (cm *ConfigManager) UpdatePolicies(policies []PolicyConfig) error {
	return cm.updateAndNotify("policies", policies, func(c *Config) error {
		c.Policies = policies
		return nil
	})
}

func (cm *ConfigManager) Reload() error {
	cfg, err := Load(cm.path)
	if err != nil {
		return err
	}
	cm.mu.Lock()
	cm.cfg = cfg
	cm.mu.Unlock()
	go cm.notify("all", cfg)
	return nil
}

func (cm *ConfigManager) LastModified() (time.Time, error) {
	info, err := os.Stat(cm.path)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

func (cm *ConfigManager) FileExists() bool {
	_, err := os.Stat(cm.path)
	return err == nil
}

// --- DEEP COPY ---

func (cm *ConfigManager) deepCopyConfig(cfg *Config) *Config {
	if cfg == nil {
		return nil
	}
	cp := &Config{
		Server:       cfg.Server,
		Database:     cfg.Database,
		JWT:          cfg.JWT,
		Admin:        cfg.Admin,
		Logs:         cfg.Logs,
		Frontend:     cfg.Frontend,
		SSO:          cfg.SSO,
		ClientFilter: cm.deepCopyClientFilter(cfg.ClientFilter),
	}
	if cfg.Models != nil {
		cp.Models = make([]ModelConfig, len(cfg.Models))
		for i, m := range cfg.Models {
			cp.Models[i] = cm.deepCopyModel(m)
		}
	}
	if cfg.Policies != nil {
		cp.Policies = make([]PolicyConfig, len(cfg.Policies))
		copy(cp.Policies, cfg.Policies)
	}
	return cp
}

func (cm *ConfigManager) deepCopyModel(m ModelConfig) ModelConfig {
	res := ModelConfig{
		ID:            m.ID,
		Name:          m.Name,
		Description:   m.Description,
		Enabled:       m.Enabled,
		ContextWindow: m.ContextWindow,
		FIM:           m.FIM,
		Responses:     m.Responses,
	}
	if m.ModelParams != nil {
		res.ModelParams = make(map[string]interface{})
		for k, v := range m.ModelParams {
			res.ModelParams[k] = v
		}
	}
	if m.Backends != nil {
		res.Backends = make([]BackendConfig, len(m.Backends))
		copy(res.Backends, m.Backends)
	}
	return res
}

func (cm *ConfigManager) deepCopyClientFilter(cf ClientFilterConfig) ClientFilterConfig {
	res := ClientFilterConfig{}
	if cf.Rules != nil {
		res.Rules = make([]ClientFilterRule, len(cf.Rules))
		copy(res.Rules, cf.Rules)
	}
	return res
}
