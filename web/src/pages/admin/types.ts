export interface Model {
  id: string;
  name: string;
  description: string;
  enabled: boolean;
  context_window?: number;
  model_params?: Record<string, any>;
  created_at: string;
  updated_at: string;
  backend_count?: number;
}

export interface Backend {
  id: string;
  model_id: string;
  name: string;
  base_url: string;
  model_name: string;
  source_platform?: string;
  source_group?: string;
  weight: number;
  region: string;
  enabled: boolean;
  healthy: boolean;
  max_concurrency: number;
  last_check_at: string;
  created_at: string;
  updated_at: string;
}

export interface BackendHealth {
  backend_id: string;
  url: string;
  model_name: string;
  healthy: boolean;
  last_check: string;
  fail_count: number;
  latency_ms: number;
  max_concurrency?: number;
  active_concurrency?: number;
}

export interface User {
  id: string;
  email: string;
  name: string;
  role: string;
  department: string;
  quota_policy: string;
  quota_policies: string[];
  enabled: boolean;
  last_login_at?: string;
}

export interface TimeRange {
  start: string;
  end: string;
}

export interface Policy {
  name: string;
  rate_limit: number;
  rate_limit_window: number;
  request_quota_daily: number;
  available_time_ranges?: TimeRange[];
  models: string[];
  description: string;
  default_model?: string;
}

export interface ModelFormValues {
  id: string;
  name: string;
  description: string;
  enabled: boolean;
  context_window?: number;
  model_params?: string;
  base_url?: string;
  api_key?: string;
}

export interface BackendFormValues {
  id: string;
  name: string;
  base_url: string;
  model_name: string;
  source_platform?: string;
  source_group?: string;
  api_key: string;
  weight: number;
  region: string;
  enabled: boolean;
  max_concurrency: number;
}

export interface UserFormValues {
  id: string;
  email: string;
  password?: string;
  name: string;
  role: string;
  department: string;
  quota_policy: string;
  quota_policies: string[];
  enabled: boolean;
}

export interface PolicyFormValues {
  name: string;
  description: string;
  rate_limit: number;
  rate_limit_window: number;
  request_quota_daily: number;
  available_time_ranges?: TimeRange[];
  models: string[];
  default_model?: string;
}
