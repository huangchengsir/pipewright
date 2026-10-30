import { describe, expect, it, vi } from "vitest";
import {
  safeRunListReturn,
  runDetailLocation,
  saveRunListPosition,
  restoreRunListPosition,
} from "./runListContext";
describe("run-list return boundary", () => {
  it("retains only an internal exact runs path", () => {
    expect(safeRunListReturn("/runs?q=release%2Fa&page=3")).toBe(
      "/runs?q=release%2Fa&page=3",
    );
    for (const value of [
      "https://evil.invalid/runs",
      "//evil.invalid/runs",
      "/runs-other",
      "/runs/../settings",
      "/runs#bad",
      "/settings",
      undefined,
      ["/runs"],
    ])
      expect(safeRunListReturn(value)).toBe("/runs");
  });
  it("discards a delayed scroll callback when its view is no longer current", async () => {
    let callback!: FrameRequestCallback;
    vi.spyOn(window, "requestAnimationFrame").mockImplementation((fn) => {
      callback = fn;
      return 1;
    });
    const scroll = vi.spyOn(window, "scrollTo").mockImplementation(() => {});
    saveRunListPosition("/runs?q=a", "id");
    await restoreRunListPosition("/runs?q=a", () => false);
    callback(0);
    expect(scroll).not.toHaveBeenCalled();
    vi.restoreAllMocks();
  });
  it('preserves exact producer query fields while rejecting an external list return', () => {
    expect(runDetailLocation('producer', '/runs?status=failed&page=2', { step: '7', stepId: 'source-7' })).toEqual({ name: 'run-detail', params: { id: 'producer' }, query: { step: '7', stepId: 'source-7', returnTo: '/runs?status=failed&page=2' } })
    const externalReturn = runDetailLocation('producer', 'https://evil.invalid', { step: '7', stepId: 'source-7' })
    expect(externalReturn).toMatchObject({ query: { returnTo: '/runs' } })
  })
});
