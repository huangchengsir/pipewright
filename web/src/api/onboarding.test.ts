import { describe, expect, it, vi } from "vitest";
import { http } from "./http";
import { getOnboardingStatus, isSnapshot } from "./onboarding";
import { snapshot } from "../composables/onboardingFixtures.test-helper";
vi.mock("./http", () => ({ http: { get: vi.fn() } }));
describe("read-only onboarding DTO", () => {
  it("encodes selection and forwards AbortSignal to same-origin GET", async () => {
    vi.mocked(http.get).mockResolvedValue(snapshot());
    const signal = new AbortController().signal;
    expect(await getOnboardingStatus("a/b ?", signal)).toEqual(snapshot());
    expect(http.get).toHaveBeenCalledWith(
      "/api/onboarding/status?projectId=a%2Fb%20%3F",
      { signal, throwOnUnauthorized: true },
    );
  });
  it.each([
    [],
    null,
    {},
    { ...snapshot(), projectCount: -1 },
    { ...snapshot(), pipeline: null },
  ])("rejects malformed response without raw contents", async (value) => {
    expect(isSnapshot(value)).toBe(false);
    vi.mocked(http.get).mockResolvedValue(value);
    await expect(getOnboardingStatus()).rejects.toThrow(
      "Invalid onboarding snapshot",
    );
  });
});

it("forwards an explicit fixed team context without dropping the legacy signal parameter", async () => {
  vi.mocked(http.get).mockResolvedValue(snapshot());
  const signal = new AbortController().signal;
  await getOnboardingStatus("project", signal, {
    headers: { "X-Pipewright-Team": "team-a" },
  });
  expect(http.get).toHaveBeenLastCalledWith(
    "/api/onboarding/status?projectId=project",
    {
      signal,
      headers: { "X-Pipewright-Team": "team-a" },
      throwOnUnauthorized: true,
    },
  );
});
