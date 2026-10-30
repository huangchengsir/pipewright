import { beforeEach, describe, expect, it, vi } from "vitest";
import { listRuns } from "./runs";
const api = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("./http", () => ({ http: api }));
describe("run list query contract", () => {
  beforeEach(() => vi.clearAllMocks());
  it("encodes literal search, timestamp and server pagination and passes cancellation", async () => {
    const controller = new AbortController();
    await listRuns(
      {
        q: "release_%!&",
        projectId: "p+1",
        status: "waiting_approval",
        after: "2026-10-30T01:00:00Z",
        page: 3,
        pageSize: 20,
      },
      { signal: controller.signal },
    );
    const url = new URL(api.get.mock.calls[0][0], "http://localhost");
    expect(Object.fromEntries(url.searchParams)).toEqual({
      q: "release_%!&",
      after: "2026-10-30T01:00:00Z",
      projectId: "p+1",
      status: "waiting_approval",
      page: "3",
      pageSize: "20",
    });
    expect(api.get.mock.calls[0][1].signal).toBe(controller.signal);
  });
});
