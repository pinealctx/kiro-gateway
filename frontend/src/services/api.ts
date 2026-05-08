import { getAdminKey, clearAuthCache } from "@/stores/auth";

const BASE = "";

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
): Promise<T> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
  };
  const key = getAdminKey();
  if (key) headers["Authorization"] = `Bearer ${key}`;

  const resp = await fetch(`${BASE}${path}`, {
    method,
    headers,
    body: body ? JSON.stringify(body) : undefined,
  });

  if (!resp.ok) {
    // Handle 401 Unauthorized - clear auth and redirect to login
    if (resp.status === 401) {
      clearAuthCache();
      window.location.href = "/ui/login";
      throw new ApiError(resp.status, "Unauthorized");
    }
    const data = await resp.json().catch(() => ({}));
    throw new ApiError(
      resp.status,
      (typeof data?.error === "string" ? data.error : data?.error?.message) ?? `Request failed (${resp.status})`,
    );
  }

  if (resp.status === 204) return undefined as T;
  return resp.json();
}

// --- Health ---
export const getHealth = () => request<{ status: string; version: string }>("GET", "/health");

// --- Kiro Accounts ---
export interface ProviderRecord {
  id: string;
  name: string;
  type: string;
  region: string;
  enabled: boolean;
  healthy?: boolean;
  created_at?: string;
}

export interface KiroUsageLimits {
  account?: string;
  email?: string;
  tier?: string;
  raw_subscription?: string;
  subscription_state?: string;
  profile_arn?: string;
  days_until_reset?: number;
  next_date_reset?: number;
  fetched_at: string;
  usage: {
    resource_type?: string;
    display_name?: string;
    used: number;
    limit: number;
    used_precise: number;
    limit_precise: number;
    remaining: number;
    remaining_precise: number;
    percent_used: number;
    overage_rate: number;
    overage_cap: number;
    overages: number;
    currency?: string;
  };
}

export const listProviders = () =>
  request<{ accounts: ProviderRecord[]; total: number }>("GET", "/admin/accounts");

export const verifyAdminKey = async (key: string) => {
  const resp = await fetch(`${BASE}/admin/accounts`, {
    method: "GET",
    headers: {
      "Content-Type": "application/json",
      "Authorization": `Bearer ${key}`,
    },
  });
  if (!resp.ok) {
    const data = await resp.json().catch(() => ({}));
    throw new ApiError(
      resp.status,
      (typeof data?.error === "string" ? data.error : data?.error?.message) ?? `Request failed (${resp.status})`,
    );
  }
  return resp.json() as Promise<{ accounts: ProviderRecord[]; total: number }>;
};

export const getProvider = (id: string) =>
  request<ProviderRecord>("GET", `/admin/accounts/${id}`);

export const createProvider = (data: Partial<ProviderRecord>) =>
  request<ProviderRecord>("POST", "/admin/accounts", { ...data, type: "kiro" });

export const updateProvider = (id: string, data: Partial<ProviderRecord>) =>
  request<ProviderRecord>("PUT", `/admin/accounts/${id}`, data);

export const deleteProvider = (id: string) =>
  request<{ deleted: boolean }>("DELETE", `/admin/accounts/${id}`);

export const getKiroUsageLimits = (provider?: string) => {
  const qs = provider ? `?provider=${encodeURIComponent(provider)}` : "";
  return request<KiroUsageLimits>("GET", `/admin/kiro/usage-limits${qs}`);
};

export interface KiroModelsResponse {
  provider: string;
  models: KiroModelInfo[];
  total: number;
}

export interface KiroModelInfo {
  model_id: string;
  model_name?: string;
  description?: string;
  rate_multiplier: number;
  rate_unit?: string;
  supported_input_types?: string[];
  token_limits?: {
    max_input_tokens: number;
    max_output_tokens: number;
  };
  prompt_caching?: {
    supports_prompt_caching: boolean;
  };
  is_default: boolean;
}

export const getKiroModels = (provider?: string) => {
  const qs = provider ? `?provider=${encodeURIComponent(provider)}` : "";
  return request<KiroModelsResponse>("GET", `/admin/kiro/models${qs}`);
};

// --- API Keys ---
export interface ApiKey {
  id: string;
  key?: string;
  key_prefix?: string;
  name: string;
  enabled: boolean;
  kiro_accounts?: string[];
  kiro_default_account?: string;
  created_at?: string;
}

export const listKeys = () =>
  request<{ keys: ApiKey[]; total: number }>("GET", "/admin/keys");

export const getKey = (id: string) =>
  request<ApiKey>("GET", `/admin/keys/${id}`);

export const createKey = (data: Partial<ApiKey>) =>
  request<ApiKey>("POST", "/admin/keys", data);

export const updateKey = (id: string, data: Partial<ApiKey>) =>
  request<ApiKey>("PUT", `/admin/keys/${id}`, data);

export const deleteKey = (id: string) =>
  request<{ deleted: boolean }>("DELETE", `/admin/keys/${id}`);

// --- Usage ---
export interface UsageRecord {
  key_id: string;
  key_name: string;
  model?: string;
  provider?: string;
  total_requests: number;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
}

export const getUsage = (params?: {
  key_id?: string;
  model?: string;
  provider?: string;
  group_by?: "key" | "model" | "provider" | "key_model" | "key_provider";
  from?: string;
  to?: string;
}) => {
  const qs = new URLSearchParams();
  if (params?.key_id) qs.set("key_id", params.key_id);
  if (params?.model) qs.set("model", params.model);
  if (params?.provider) qs.set("provider", params.provider);
  if (params?.group_by) qs.set("group_by", params.group_by);
  if (params?.from) qs.set("from", params.from);
  if (params?.to) qs.set("to", params.to);
  const q = qs.toString();
  return request<{ usage: UsageRecord[] }>("GET", `/admin/usage${q ? `?${q}` : ""}`);
};

// --- Kiro ---
export interface KiroLoginSession {
  id: string;
  auth_url: string;
  port: number;
  status: string;
}

export interface KiroDeviceLoginSession {
  id: string;
  user_code: string;
  verification_uri?: string;
  verification_uri_complete: string;
  interval: number;
  expires_at?: string;
  status: string;
  method?: string;
}

export interface KiroStatus {
  has_login: boolean;
  has_current: boolean;
  is_external_idp: boolean;
  expires_at?: string;
}

export const startKiroLogin = (provider?: string, port?: number) => {
  const qs = provider ? `?provider=${encodeURIComponent(provider)}` : "";
  return request<KiroLoginSession>("POST", `/admin/kiro/login${qs}`, port ? { port } : undefined);
};

export const startKiroDeviceLogin = (provider?: string, data?: { method?: string; idc_region?: string; start_url?: string }) => {
  const qs = provider ? `?provider=${encodeURIComponent(provider)}` : "";
  return request<KiroDeviceLoginSession>("POST", `/admin/kiro/device-login${qs}`, data);
};

export const getKiroLoginStatus = (id: string, provider?: string) => {
  const qs = provider ? `?provider=${encodeURIComponent(provider)}` : "";
  return request<{ id: string; status: string; error?: string }>("GET", `/admin/kiro/login/${id}${qs}`);
};

export const completeKiroLogin = (id: string, provider?: string) => {
  const qs = provider ? `?provider=${encodeURIComponent(provider)}` : "";
  return request<{ message: string; provider: string }>("POST", `/admin/kiro/login/complete/${id}${qs}`);
};

export const getKiroStatus = (provider?: string) => {
  const qs = provider ? `?provider=${encodeURIComponent(provider)}` : "";
  return request<KiroStatus>("GET", `/admin/kiro/status${qs}`);
};

export const refreshKiroToken = (provider?: string) => {
  const qs = provider ? `?provider=${encodeURIComponent(provider)}` : "";
  return request<KiroStatus>("POST", `/admin/kiro/refresh${qs}`);
};

export const importKiroLocal = (provider?: string, dbPath?: string) => {
  const qs = provider ? `?provider=${encodeURIComponent(provider)}` : "";
  return request<{ message: string; provider: string; is_external_idp: boolean; has_refresh: boolean; profile_arn: string; expires_at?: string }>(
    "POST",
    `/admin/kiro/import-local${qs}`,
    dbPath ? { db_path: dbPath } : undefined
  );
};
