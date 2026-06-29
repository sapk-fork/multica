import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import React from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, fireEvent, waitFor, cleanup } from "@testing-library/react";
import type { InboxItem } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import { useInboxSortStore } from "@multica/core/inbox/store";
import enCommon from "../../locales/en/common.json";
import enInbox from "../../locales/en/inbox.json";

// ── Hoisted mock factories ────────────────────────────────────────────────────
const { listInbox } = vi.hoisted(() => ({ listInbox: vi.fn() }));

// ── Module mocks ──────────────────────────────────────────────────────────────

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    inbox: () => "/ws/inbox",
    issueDetail: (id: string) => `/ws/issues/${id}`,
  }),
}));

vi.mock("@multica/core/modals", () => ({
  useModalStore: Object.assign(
    () => ({ open: vi.fn() }),
    { getState: () => ({ open: vi.fn() }) },
  ),
}));

vi.mock("@multica/core/issues/stores/draft-store", () => ({
  useIssueDraftStore: Object.assign(
    () => ({ setDraft: vi.fn() }),
    { getState: () => ({ setDraft: vi.fn() }) },
  ),
}));

vi.mock("@multica/core/api", () => ({
  api: {
    listInbox,
    getInboxUnreadSummary: vi.fn().mockResolvedValue([]),
  },
}));

vi.mock("@multica/core/inbox/mutations", () => ({
  useMarkInboxRead: () => ({ mutate: vi.fn() }),
  useArchiveInbox: () => ({ mutate: vi.fn() }),
  useMarkAllInboxRead: () => ({ mutate: vi.fn() }),
  useArchiveAllInbox: () => ({ mutate: vi.fn() }),
  useArchiveAllReadInbox: () => ({ mutate: vi.fn() }),
  useArchiveCompletedInbox: () => ({ mutate: vi.fn() }),
}));

vi.mock("../../issues/components", () => ({
  IssueDetail: () => null,
  StatusIcon: () => null,
  ErrorBoundary: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

vi.mock("../../common/actor-avatar", () => ({ ActorAvatar: () => null }));

vi.mock("../../navigation", () => ({
  useNavigation: () => ({
    searchParams: new URLSearchParams(),
    replace: vi.fn(),
    push: vi.fn(),
  }),
}));

vi.mock("@multica/ui/hooks/use-mobile", () => ({ useIsMobile: () => false }));

vi.mock("react-resizable-panels", () => ({
  useDefaultLayout: () => ({ defaultLayout: undefined, onLayoutChanged: () => {} }),
}));

vi.mock("@multica/ui/components/ui/resizable", () => ({
  ResizablePanelGroup: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  ResizablePanel: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  ResizableHandle: () => null,
}));

vi.mock("../../layout/page-header", () => ({
  PageHeader: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
}));

// Render each inbox item as a simple div so order in DOM is testable.
vi.mock("./inbox-list-item", () => ({
  InboxListItem: ({ item }: { item: InboxItem }) => (
    <div data-testid={`inbox-item-${item.id}`}>{item.title}</div>
  ),
  useTimeAgo: () => () => "1h",
}));

vi.mock("./inbox-detail-label", () => ({
  InboxDetailLabel: () => null,
  useTypeLabels: () => ({}),
}));

vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

// Mock the dropdown-menu with always-visible content to avoid Base UI portal
// and GroupLabel context requirements in jsdom. The test pins that the sort
// options are present and functional, not the dropdown open/close UX.
vi.mock("@multica/ui/components/ui/dropdown-menu", async () => {
  const { createContext, useContext } = await import("react");

  type RadioCtxType = ((v: string) => void) | null;
  const RadioCtx = createContext<RadioCtxType>(null);

  return {
    DropdownMenu: ({ children }: { children: React.ReactNode }) => <>{children}</>,
    DropdownMenuTrigger: ({ children }: { children: React.ReactNode }) => (
      <button type="button">{children}</button>
    ),
    DropdownMenuPortal: ({ children }: { children: React.ReactNode }) => <>{children}</>,
    DropdownMenuContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
    DropdownMenuLabel: ({ children }: { children: React.ReactNode }) => <span>{children}</span>,
    DropdownMenuGroup: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
    DropdownMenuSeparator: () => <hr />,
    DropdownMenuItem: ({ children, onClick }: { children: React.ReactNode; onClick?: () => void }) => (
      <button type="button" role="menuitem" onClick={onClick}>{children}</button>
    ),
    DropdownMenuRadioGroup: ({
      children,
      value,
      onValueChange,
    }: {
      children: React.ReactNode;
      value: string;
      onValueChange: (v: string) => void;
    }) => (
      <RadioCtx.Provider value={onValueChange}>
        <div data-sort-field={value}>{children}</div>
      </RadioCtx.Provider>
    ),
    DropdownMenuRadioItem: ({ children, value }: { children: React.ReactNode; value: string }) => {
      const onChange = useContext(RadioCtx);
      return (
        <button type="button" role="menuitemradio" data-value={value} onClick={() => onChange?.(value)}>
          {children}
        </button>
      );
    },
  };
});

// ── Import subject AFTER all vi.mock() calls ──────────────────────────────────
import { InboxPage } from "./inbox-page";

// ── Helpers ───────────────────────────────────────────────────────────────────

const TEST_RESOURCES = { en: { common: enCommon, inbox: enInbox } };

let _idCounter = 0;
function makeItem(overrides: Partial<InboxItem> & { id: string }): InboxItem {
  _idCounter++;
  return {
    id: overrides.id,
    workspace_id: "ws-1",
    recipient_type: "member",
    recipient_id: "member-1",
    actor_type: "agent",
    actor_id: "agent-1",
    type: "new_comment",
    severity: "info",
    // Each item gets a unique issue_id so deduplicateInboxItems keeps all of them.
    issue_id: `issue-${overrides.id}`,
    title: overrides.id,
    body: null,
    issue_status: null,
    issue_priority: null,
    read: false,
    archived: false,
    created_at: "2026-06-15T08:00:00Z",
    details: null,
    ...overrides,
  };
}

function renderInbox() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <QueryClientProvider client={queryClient}>
        <InboxPage />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe("InboxPage — sort dropdown", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    // Reset sort store to default between tests
    useInboxSortStore.setState({ sortField: "date", sortDirection: "desc" });
  });

  afterEach(() => {
    cleanup();
    document.body.innerHTML = "";
  });

  // sort dropdown renders in the inbox header — pin that sort options are present
  it("renders sort options (Date, Priority, Unread first) in the inbox header", async () => {
    listInbox.mockResolvedValue([
      makeItem({ id: "a", title: "Item A" }),
    ]);
    renderInbox();

    await waitFor(() => screen.getByTestId("inbox-item-a"));

    // Sort by label from the dropdown label
    expect(screen.getByText("Sort by")).toBeInTheDocument();
    // All three sort fields are available
    expect(screen.getByRole("menuitemradio", { name: "Date" })).toBeInTheDocument();
    expect(screen.getByRole("menuitemradio", { name: "Priority" })).toBeInTheDocument();
    expect(screen.getByRole("menuitemradio", { name: "Unread first" })).toBeInTheDocument();
  });

  // clicking Priority radio changes the sort field via the store
  it("clicking the Priority option in the dropdown switches the sort store to priority", async () => {
    listInbox.mockResolvedValue([
      makeItem({ id: "a", title: "Item A" }),
    ]);
    renderInbox();

    await waitFor(() => screen.getByTestId("inbox-item-a"));

    fireEvent.click(screen.getByRole("menuitemradio", { name: "Priority" }));

    expect(useInboxSortStore.getState().sortField).toBe("priority");
    expect(useInboxSortStore.getState().sortDirection).toBe("desc");
  });

  // Default (Date desc) must match the pre-change ordering
  it("default Date-desc shows newest item first without any sort interaction", async () => {
    listInbox.mockResolvedValue([
      makeItem({ id: "old", title: "Oldest", created_at: "2026-06-10T08:00:00Z" }),
      makeItem({ id: "new", title: "Newest", created_at: "2026-06-15T10:00:00Z" }),
      makeItem({ id: "mid", title: "Middle", created_at: "2026-06-12T09:00:00Z" }),
    ]);
    renderInbox();

    await waitFor(() => screen.getByTestId("inbox-item-new"));

    const items = screen.getAllByTestId(/^inbox-item-/);
    const ids = items.map((el) => el.dataset["testid"]?.replace("inbox-item-", ""));
    expect(ids).toEqual(["new", "mid", "old"]);
  });

  // switching to Priority orders urgent→…→none with null/no-issue items sinking to the bottom
  it("Priority sort orders urgent first and sinks items with no linked issue to the bottom", async () => {
    listInbox.mockResolvedValue([
      makeItem({ id: "none-prio",  title: "No priority",  issue_priority: "none",   created_at: "2026-06-15T08:00:00Z" }),
      makeItem({ id: "urgent",     title: "Urgent",       issue_priority: "urgent", created_at: "2026-06-15T08:00:00Z" }),
      makeItem({ id: "no-issue",   title: "No issue",     issue_id: null, issue_priority: null, created_at: "2026-06-15T08:00:00Z" }),
      makeItem({ id: "high",       title: "High",         issue_priority: "high",   created_at: "2026-06-15T08:00:00Z" }),
      makeItem({ id: "medium",     title: "Medium",       issue_priority: "medium", created_at: "2026-06-15T08:00:00Z" }),
      makeItem({ id: "low",        title: "Low",          issue_priority: "low",    created_at: "2026-06-15T08:00:00Z" }),
    ]);

    useInboxSortStore.setState({ sortField: "priority", sortDirection: "desc" });

    renderInbox();
    await waitFor(() => screen.getByTestId("inbox-item-urgent"));

    const items = screen.getAllByTestId(/^inbox-item-/);
    const ids = items.map((el) => el.dataset["testid"]?.replace("inbox-item-", ""));
    expect(ids).toEqual(["urgent", "high", "medium", "low", "none-prio", "no-issue"]);
  });

  // Unread-first bubbles unread up with date as secondary key
  it("Unread-first sort puts unread items at top, newest first within each group", async () => {
    listInbox.mockResolvedValue([
      makeItem({ id: "read-new",   title: "Read new",   read: true,  created_at: "2026-06-15T10:00:00Z" }),
      makeItem({ id: "unread-old", title: "Unread old", read: false, created_at: "2026-06-15T08:00:00Z" }),
      makeItem({ id: "unread-new", title: "Unread new", read: false, created_at: "2026-06-15T09:00:00Z" }),
      makeItem({ id: "read-old",   title: "Read old",   read: true,  created_at: "2026-06-15T07:00:00Z" }),
    ]);

    useInboxSortStore.setState({ sortField: "unread", sortDirection: "desc" });

    renderInbox();
    await waitFor(() => screen.getByTestId("inbox-item-unread-new"));

    const items = screen.getAllByTestId(/^inbox-item-/);
    const ids = items.map((el) => el.dataset["testid"]?.replace("inbox-item-", ""));
    expect(ids).toEqual(["unread-new", "unread-old", "read-new", "read-old"]);
  });
});
