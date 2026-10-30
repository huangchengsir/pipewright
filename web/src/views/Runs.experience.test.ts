import { beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { createMemoryHistory, createRouter, RouterView } from "vue-router";
import Runs from "./Runs.vue";
const api = vi.hoisted(() => ({ list: vi.fn(), projects: vi.fn(), trigger: vi.fn() }));
vi.mock("../api/runs", () => ({ listRuns: api.list, triggerManual: api.trigger }));
vi.mock("../api/projects", () => ({ listProjects: api.projects }));
vi.mock('../api/parameters', async original => ({ ...(await original<typeof import('../api/parameters')>()), getParameters: vi.fn().mockResolvedValue([]) }))
const row = (id: string) => ({
  id,
  projectId: "p1",
  projectName: "Project",
  status: "failed",
  trigger: {
    type: "manual",
    branch: "release/test",
    commit: "abc",
    actor: "admin",
  },
  createdAt: "2026-10-30T00:00:00Z",
  durationMs: 1000,
});
async function setup(path: string) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: "/runs", name: "runs", component: Runs },
      {
        path: "/runs/:id",
        name: "run-detail",
        component: { template: "<div>detail</div>" },
      },
    ],
  });
  await router.push(path);
  await router.isReady();
  const w = mount(RouterView, { global: { plugins: [router], stubs: { Teleport: true } } });
  await flushPromises();
  return { w, router };
}
describe("server-backed run list context", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.list.mockResolvedValue({ items: [row("current")], total: 65, page: 2 });
    api.projects.mockResolvedValue([{ id: "p1", name: "Project" }]);
    vi.spyOn(window, "scrollTo").mockImplementation(() => {});
  });
  it("hydrates URL filters, passes real pagination, and keeps returnTo on a native link", async () => {
    const { w, router } = await setup(
      "/runs?projectId=p1&status=failed&q=release%2F&page=2&after=2026-10-01T00:00:00Z&period=30",
    );
    expect(api.list.mock.calls[0][0]).toMatchObject({
      projectId: "p1",
      status: "failed",
      q: "release/",
      page: 2,
      pageSize: 20,
      after: "2026-10-01T00:00:00.000Z",
    });
    const link = w.find("a.run-row");
    expect(link.attributes("href")).toContain("returnTo=");
    await link.trigger("click", { button: 0 });
    await flushPromises();
    expect(router.currentRoute.value.name).toBe("run-detail");
    expect(router.currentRoute.value.query.returnTo).toContain("page=2");
    w.unmount();
  });
  it("changes pages through URL and browser back restores the previous query", async () => {
    const { w, router } = await setup("/runs?q=first&page=2");
    await w.findAll(".run-pagination button")[1]!.trigger("click");
    await flushPromises();
    expect(router.currentRoute.value.query.page).toBe("3");
    expect(api.list.mock.lastCall?.[0].page).toBe(3);
    router.back();
    await flushPromises();
    expect(router.currentRoute.value.query.page).toBe("2");
    expect(w.find<HTMLInputElement>("input[type=search]").element.value).toBe(
      "first",
    );
    w.unmount();
  });
  it("ignores a superseded late query response and aborts its transport", async () => {
    let resolve!: (value: unknown) => void;
    api.list.mockReturnValueOnce(
      new Promise((r) => {
        resolve = r;
      }),
    );
    const { w, router } = await setup("/runs?q=old");
    await router.push("/runs?q=new");
    await flushPromises();
    expect(api.list.mock.calls[0][1].signal.aborted).toBe(true);
    resolve({ items: [row("stale")], total: 1, page: 1 });
    await flushPromises();
    expect(w.find("a.run-row").attributes("data-run-id")).toBe("current");
    w.unmount();
  });
  it('keeps the time-range label consistent with the applied server boundary', async () => {
    const first = await setup('/runs?period=7&after=invalid')
    expect(first.w.findAll<HTMLSelectElement>('.run-query select')[1]!.element.value).toBe('all')
    expect(api.list.mock.lastCall?.[0].after).toBeUndefined()
    first.w.unmount()
    const second = await setup('/runs?after=2026-10-01T00:00:00Z')
    expect(second.w.findAll<HTMLSelectElement>('.run-query select')[1]!.element.value).toBe('custom')
    expect(second.w.text()).toContain('2026-10-01')
    expect(api.list.mock.lastCall?.[0].after).toBe('2026-10-01T00:00:00.000Z')
    second.w.unmount()
  })
  it('paginates the applied URL query without submitting a new search draft', async () => {
    const { w, router } = await setup('/runs?q=old&page=2')
    await w.find('input[type=search]').setValue('unsubmitted')
    await w.findAll('.run-pagination button')[1]!.trigger('click'); await flushPromises()
    expect(router.currentRoute.value.query).toMatchObject({ q: 'old', page: '3' })
    expect(api.list.mock.lastCall?.[0].q).toBe('old')
    await w.find('input[type=search]').setValue('submitted')
    await w.find('.run-query').trigger('submit'); await flushPromises()
    expect(router.currentRoute.value.query).toMatchObject({ q: 'submitted' }); expect(router.currentRoute.value.query.page).toBeUndefined(); w.unmount()
  })
  it('does not submit a search draft during an automatic out-of-range correction', async () => {
    let resolve!: (value: unknown) => void
    api.list.mockReturnValueOnce(new Promise(r => { resolve = r }))
    const { w, router } = await setup('/runs?q=old&page=9')
    await w.find('input[type=search]').setValue('unsubmitted')
    resolve({ items: [], total: 21, page: 9 }); await flushPromises()
    expect(router.currentRoute.value.query).toMatchObject({ q: 'old', page: '2' }); expect(api.list.mock.lastCall?.[0].q).toBe('old'); w.unmount()
  })
  it('retains the applied list return path when a manual run opens its detail', async () => {
    const back = '/runs?q=old&page=2'
    api.trigger.mockResolvedValueOnce({ id: 'manual-created' })
    const { w, router } = await setup(back)
    await w.find('.btn-run').trigger('click'); await flushPromises()
    await w.find('#trigger-project').setValue('p1'); await flushPromises()
    await w.find('#trigger-branch').setValue('main')
    await w.find('.modal-form').trigger('submit'); await flushPromises()
    expect(router.currentRoute.value.params.id).toBe('manual-created'); expect(router.currentRoute.value.query.returnTo).toBe(back)
    expect(api.trigger).toHaveBeenCalledWith('p1', { branch: 'main' }); w.unmount()
  })
});
