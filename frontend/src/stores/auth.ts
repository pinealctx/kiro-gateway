const TOKEN_KEY = "kiro_gateway_admin_key";
const LEGACY_TOKEN_KEY = "ag_admin_key";

export function getAdminKey(): string {
  const key = sessionStorage.getItem(TOKEN_KEY);
  if (key) return key;
  const legacyKey = localStorage.getItem(TOKEN_KEY) ?? localStorage.getItem(LEGACY_TOKEN_KEY) ?? "";
  if (legacyKey) {
    sessionStorage.setItem(TOKEN_KEY, legacyKey);
    localStorage.removeItem(TOKEN_KEY);
    localStorage.removeItem(LEGACY_TOKEN_KEY);
  }
  return legacyKey;
}

export function setAdminKey(key: string) {
  sessionStorage.setItem(TOKEN_KEY, key);
  localStorage.removeItem(TOKEN_KEY);
  localStorage.removeItem(LEGACY_TOKEN_KEY);
}

export function clearAdminKey() {
  sessionStorage.removeItem(TOKEN_KEY);
  localStorage.removeItem(TOKEN_KEY);
  localStorage.removeItem(LEGACY_TOKEN_KEY);
}

export function clearAuthCache() {
  clearAdminKey();
  sessionStorage.clear();
}

export function isAuthenticated(): boolean {
  return getAdminKey().length > 0;
}
