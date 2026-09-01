export type User = {
  id: string;
  username: string;
  display_name: string;
  status?: "ACTIVE" | "DISABLED";
  created_at?: string;
  updated_at?: string;
};

export type Session = {
  user: User;
  password_change_required: boolean;
};

export type Operator = {
  user_id: string;
  username?: string;
  display_name?: string;
  granted_by?: string;
  granted_at: string;
};

export type Tenant = {
  id: string;
  slug: string;
  name: string;
  status: "ACTIVE" | "SUSPENDED";
  role?: "OWNER" | "MEMBER";
  owner_user_id?: string;
  created_at: string;
  updated_at: string;
};

export type Membership = {
  id: string;
  user_id: string;
  role: "OWNER" | "MEMBER";
  created_by: string;
  created_at: string;
};

export class ControlApiError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string,
    message: string,
  ) {
    super(message);
    this.name = "ControlApiError";
  }
}

const base = "/api/control";

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${base}${path}`, init ? {
    ...init,
    credentials: "include",
  } : { credentials: "include" });

  if (!response.ok) {
    const body = await response.json().catch(() => null) as {
      error?: { code?: string; message?: string };
    } | null;
    throw new ControlApiError(
      response.status,
      body?.error?.code ?? "HTTP_ERROR",
      body?.error?.message ?? `Control API returned HTTP ${response.status}`,
    );
  }

  if (response.status === 204) return undefined as T;
  return response.json() as Promise<T>;
}

function json(method: string, body: unknown): RequestInit {
  return {
    method,
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  };
}

export const controlApi = {
  login(input: { username: string; password: string }) {
    return request<Session>("/v1/auth/login", json("POST", input));
  },
  getMe() {
    return request<Session>("/v1/me");
  },
  logout() {
    return request<void>("/v1/auth/logout", { method: "POST" });
  },
  changePassword(input: { current_password: string; new_password: string }) {
    return request<void>("/v1/me/change-password", json("POST", input));
  },
  getCapabilities() {
    return request<{ capabilities: string[] }>("/v1/admin/capabilities");
  },
  listOperators() {
    return request<{ operators: Operator[] }>("/v1/admin/operators");
  },
  grantOperator(userId: string) {
    return request<Operator>("/v1/admin/operators", json("POST", { user_id: userId }));
  },
  revokeOperator(userId: string) {
    return request<void>(`/v1/admin/operators/${encodeURIComponent(userId)}`, { method: "DELETE" });
  },
  listUsers(page: { offset: number; limit: number }) {
    const query = new URLSearchParams({ offset: String(page.offset), limit: String(page.limit) });
    return request<{ users: User[]; offset: number; limit: number; total: number }>(
      `/v1/admin/users?${query}`,
    );
  },
  createUser(input: { username: string; display_name: string; temporary_password: string }) {
    return request<User>("/v1/admin/users", json("POST", input));
  },
  listAdminTenants(page: { offset: number; limit: number }) {
    const query = new URLSearchParams({ offset: String(page.offset), limit: String(page.limit) });
    return request<{ tenants: Tenant[]; offset: number; limit: number; total: number }>(
      `/v1/admin/tenants?${query}`,
    );
  },
  createTenant(input: { slug: string; name: string; owner_user_id: string }) {
    return request<Tenant>("/v1/admin/tenants", json("POST", input));
  },
  listMyTenants() {
    return request<{ tenants: Tenant[] }>("/v1/me/tenants");
  },
  getTenant(tenantId: string) {
    return request<Tenant>(`/v1/tenants/${encodeURIComponent(tenantId)}`);
  },
  listMembers(tenantId: string) {
    return request<{ members: Membership[] }>(
      `/v1/tenants/${encodeURIComponent(tenantId)}/members`,
    );
  },
  addMember(tenantId: string, userId: string) {
    return request<Membership>(
      `/v1/tenants/${encodeURIComponent(tenantId)}/members`,
      json("POST", { user_id: userId }),
    );
  },
  removeMember(tenantId: string, userId: string) {
    return request<void>(
      `/v1/tenants/${encodeURIComponent(tenantId)}/members/${encodeURIComponent(userId)}`,
      { method: "DELETE" },
    );
  },
};
