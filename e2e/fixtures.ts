/**
 * TestApiClient — lightweight API helper for E2E test data setup/teardown.
 *
 * Uses raw fetch so E2E tests have zero build-time coupling to the web app.
 */

import "./env";
import pg from "pg";

// `||` (not `??`) so an empty `NEXT_PUBLIC_API_URL=` in .env still falls
// back to localhost. dotenv sets unset-vs-empty both as "" — treating them
// the same matches user intent.
const API_BASE = process.env.NEXT_PUBLIC_API_URL || `http://localhost:${process.env.PORT || "8080"}`;
const DATABASE_URL = process.env.DATABASE_URL ?? "postgres://multica:multica@localhost:5432/multica?sslmode=disable";

interface TestWorkspace {
  id: string;
  name: string;
  slug: string;
}

export class TestApiClient {
  private token: string | null = null;
  private workspaceSlug: string | null = null;
  private workspaceId: string | null = null;
  private email: string | null = null;
  private userId: string | null = null;
  private createdIssueIds: string[] = [];
  private createdAutopilotIds: string[] = [];
  private createdAgentIds: string[] = [];
  private createdRuntimeIds: string[] = [];

  async login(email: string, name: string) {
    const client = new pg.Client(DATABASE_URL);
    await client.connect();
    try {
      // Keep each E2E login isolated so previous test runs do not trip the
      // per-email send-code rate limit.
      await client.query("DELETE FROM verification_code WHERE email = $1", [email]);

      // Step 1: Send verification code
      const sendRes = await fetch(`${API_BASE}/auth/send-code`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email }),
      });
      if (!sendRes.ok) {
        throw new Error(`send-code failed: ${sendRes.status}`);
      }

      // Step 2: Read code from database
      const result = await client.query(
        "SELECT code FROM verification_code WHERE email = $1 AND used = FALSE AND expires_at > now() ORDER BY created_at DESC LIMIT 1",
        [email],
      );
      if (result.rows.length === 0) {
        throw new Error(`No verification code found for ${email}`);
      }

      const configuredDevCode = process.env.MULTICA_DEV_VERIFICATION_CODE?.trim();
      const code = configuredDevCode || result.rows[0].code;

      // Step 3: Verify code to get JWT
      const verifyRes = await fetch(`${API_BASE}/auth/verify-code`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, code }),
      });
      if (!verifyRes.ok) {
        throw new Error(`verify-code failed: ${verifyRes.status}`);
      }
      const data = await verifyRes.json();

      this.token = data.token;
      this.email = email;
      this.userId = data.user?.id ?? null;

      // Update user name if needed
      if (name && data.user?.name !== name) {
        await this.authedFetch("/api/me", {
          method: "PATCH",
          body: JSON.stringify({ name }),
        });
      }

      await client.query("DELETE FROM verification_code WHERE email = $1", [email]);

      return data;
    } finally {
      await client.end();
    }
  }

  async getWorkspaces(): Promise<TestWorkspace[]> {
    const res = await this.authedFetch("/api/workspaces");
    return res.json();
  }

  setWorkspaceId(id: string) {
    this.workspaceId = id;
  }

  setWorkspaceSlug(slug: string) {
    this.workspaceSlug = slug;
  }

  async ensureWorkspace(name = "E2E Workspace", slug = "e2e-workspace") {
    const workspaces = await this.getWorkspaces();
    const workspace = workspaces.find((item) => item.slug === slug) ?? workspaces[0];
    if (workspace) {
      this.workspaceId = workspace.id;
      this.workspaceSlug = workspace.slug;
      return workspace;
    }

    const res = await this.authedFetch("/api/workspaces", {
      method: "POST",
      body: JSON.stringify({ name, slug }),
    });
    if (res.ok) {
      const created = (await res.json()) as TestWorkspace;
      this.workspaceId = created.id;
      this.workspaceSlug = created.slug;
      return created;
    }

    const refreshed = await this.getWorkspaces();
    const created = refreshed.find((item) => item.slug === slug) ?? refreshed[0];
    if (created) {
      this.workspaceId = created.id;
      this.workspaceSlug = created.slug;
      return created;
    }

    throw new Error(`Failed to ensure workspace ${slug}: ${res.status} ${res.statusText}`);
  }

  async markUserOnboarded() {
    if (!this.email) {
      throw new Error("Cannot mark E2E user onboarded before login");
    }

    const client = new pg.Client(DATABASE_URL);
    await client.connect();
    try {
      const result = await client.query(
        `
          UPDATE "user"
          SET
            onboarded_at = COALESCE(onboarded_at, now()),
            onboarding_questionnaire = COALESCE(onboarding_questionnaire, '{}'::jsonb)
              || '{"source":["friends_colleagues"],"source_other":null,"source_skipped":false}'::jsonb
          WHERE email = $1
        `,
        [this.email],
      );
      if (result.rowCount !== 1) {
        throw new Error(`Failed to mark E2E user onboarded: ${this.email}`);
      }
    } finally {
      await client.end();
    }
  }

  async createIssue(title: string, opts?: Record<string, unknown>) {
    const res = await this.authedFetch("/api/issues", {
      method: "POST",
      body: JSON.stringify({ title, ...opts }),
    });
    const issue = await res.json();
    this.createdIssueIds.push(issue.id);
    return issue;
  }

  async deleteIssue(id: string) {
    await this.authedFetch(`/api/issues/${id}`, { method: "DELETE" });
  }

  /**
   * Create an agent (with its own runtime) for tests that need a real
   * assignee — e.g. autopilots, which require a non-null agent_id.
   * Seeded directly via SQL: creating a real runtime/agent through the API
   * needs a live daemon connection, which E2E doesn't have.
   */
  async createAgent(name: string): Promise<string> {
    if (!this.workspaceId) {
      throw new Error("Cannot create agent before a workspace is set");
    }
    if (!this.userId) {
      throw new Error("Cannot create agent before login");
    }
    const client = new pg.Client(DATABASE_URL);
    await client.connect();
    try {
      const runtimeRes = await client.query(
        `INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, status, device_info, metadata, owner_id)
         VALUES ($1, $2, 'cloud', 'codex', 'online', '', '{}'::jsonb, $3)
         RETURNING id`,
        [this.workspaceId, `${name} runtime`, this.userId],
      );
      const runtimeId = runtimeRes.rows[0].id;
      this.createdRuntimeIds.push(runtimeId);

      const agentRes = await client.query(
        `INSERT INTO agent (workspace_id, name, runtime_mode, runtime_config, runtime_id, visibility,
            max_concurrent_tasks, owner_id, instructions, custom_env, custom_args)
         VALUES ($1, $2, 'cloud', '{}'::jsonb, $3, 'workspace', 1, $4, '', '{}'::jsonb, '[]'::jsonb)
         RETURNING id`,
        [this.workspaceId, name, runtimeId, this.userId],
      );
      const agentId = agentRes.rows[0].id;
      this.createdAgentIds.push(agentId);
      return agentId;
    } finally {
      await client.end();
    }
  }

  async createAutopilot(
    title: string,
    assigneeId: string,
    opts?: Record<string, unknown>,
  ) {
    const res = await this.authedFetch("/api/autopilots", {
      method: "POST",
      body: JSON.stringify({
        title,
        assignee_type: "agent",
        assignee_id: assigneeId,
        execution_mode: "create_issue",
        ...opts,
      }),
    });
    if (!res.ok) {
      throw new Error(`createAutopilot failed: ${res.status} ${await res.text()}`);
    }
    const autopilot = await res.json();
    this.createdAutopilotIds.push(autopilot.id);
    return autopilot;
  }

  async deleteAutopilot(id: string) {
    await this.authedFetch(`/api/autopilots/${id}`, { method: "DELETE" });
  }

  /**
   * Insert an autopilot_run row directly so a test can simulate "N runs
   * already in flight" without waiting on a real agent to execute one.
   * Mirrors the seeding pattern in server/internal/service/autopilot_concurrency_test.go.
   */
  async seedAutopilotRun(autopilotId: string, status: "issue_created" | "running" = "running") {
    const client = new pg.Client(DATABASE_URL);
    await client.connect();
    try {
      const res = await client.query(
        `INSERT INTO autopilot_run (autopilot_id, source, status) VALUES ($1, 'manual', $2) RETURNING id`,
        [autopilotId, status],
      );
      return res.rows[0].id as string;
    } finally {
      await client.end();
    }
  }

  /** Clean up all issues/autopilots/agents/runtimes created during this test. */
  async cleanup() {
    for (const id of this.createdIssueIds) {
      try {
        await this.deleteIssue(id);
      } catch {
        /* ignore — may already be deleted */
      }
    }
    this.createdIssueIds = [];

    for (const id of this.createdAutopilotIds) {
      try {
        await this.deleteAutopilot(id);
      } catch {
        /* ignore — may already be deleted; cascades to seeded runs */
      }
    }
    this.createdAutopilotIds = [];

    if (this.createdAgentIds.length > 0 || this.createdRuntimeIds.length > 0) {
      const client = new pg.Client(DATABASE_URL);
      await client.connect();
      try {
        for (const id of this.createdAgentIds) {
          await client.query(`DELETE FROM agent WHERE id = $1`, [id]);
        }
        for (const id of this.createdRuntimeIds) {
          await client.query(`DELETE FROM agent_runtime WHERE id = $1`, [id]);
        }
      } finally {
        await client.end();
      }
    }
    this.createdAgentIds = [];
    this.createdRuntimeIds = [];
  }

  getToken() {
    return this.token;
  }

  getEmail() {
    if (!this.email) {
      throw new Error("Test API client is not logged in");
    }
    return this.email;
  }

  private async authedFetch(path: string, init?: RequestInit) {
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
      ...((init?.headers as Record<string, string>) ?? {}),
    };
    if (this.token) headers["Authorization"] = `Bearer ${this.token}`;
    if (this.workspaceSlug) headers["X-Workspace-Slug"] = this.workspaceSlug;
    else if (this.workspaceId) headers["X-Workspace-ID"] = this.workspaceId;
    return fetch(`${API_BASE}${path}`, { ...init, headers });
  }
}
