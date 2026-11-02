import { beforeEach, afterEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import RunTerminal from "./RunTerminal.vue";
const api = vi.hoisted(() => ({ logs: vi.fn(), subscribe: vi.fn() }));
vi.mock("../../api/runs", () => ({
  getRunLogs: api.logs,
  subscribeRunEvents: api.subscribe,
}));
const line = (
  seq: number,
  stepOrdinal: number,
  text: string,
  stream = "stdout",
) => ({ seq, stepOrdinal, text, stream, ts: "" });
const all = [
  line(1, 0, "wrong step secret marker"),
  line(7, 1, "HTTP 503 token=[MASKED]"),
  line(11, 1, "ERROR HTTP 503", "stderr"),
  line(16, 2, "different source"),
];
const clipboard = vi.fn();
const buttons = (w: ReturnType<typeof mount>, text: string) =>
  w.findAll("button").find((b) => b.text() === text)!;
describe("current-source log workbench", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.logs.mockResolvedValue({ lines: all, complete: true, nextSeq: 17 });
    api.subscribe.mockReturnValue(vi.fn());
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText: clipboard },
      configurable: true,
    });
    clipboard.mockResolvedValue(undefined);
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });
  it("searches only the selected ordinal, navigates Shift+Enter once, and resets source-specific state", async () => {
    const w = mount(RunTerminal, {
      props: { runId: "run", live: false, filterOrdinal: 1 },
    });
    await flushPromises();
    expect(w.text()).not.toContain("wrong step");
    await w.find("input[type=search]").setValue("503");
    await flushPromises();
    expect(w.find(".term-hits").text()).toContain("1 / 2");
    await w
      .find("input[type=search]")
      .trigger("keydown", { key: "Enter", shiftKey: true });
    await flushPromises();
    expect(w.find(".term-hits").text()).toContain("2 / 2");
    expect(w.find(".term-line--active").attributes("data-log-seq")).toBe("11");
    await w.setProps({ filterOrdinal: 2 });
    await flushPromises();
    expect(w.find<HTMLInputElement>("input[type=search]").element.value).toBe(
      "",
    );
    expect(w.find(".term-hits").text()).toContain("0 / 0");
    expect(w.text()).not.toContain("HTTP 503");
    w.unmount();
  });
  it("copies original non-contiguous sequence numbers and redacted text only", async () => {
    const w = mount(RunTerminal, {
      props: { runId: "run", live: false, filterOrdinal: 1 },
    });
    await flushPromises();
    const range = w.findAll("input[type=number]");
    await range[0]!.setValue("7");
    await range[1]!.setValue("11");
    await buttons(w, "复制脱敏片段").trigger("click");
    await flushPromises();
    expect(clipboard).toHaveBeenCalledWith(
      "7: HTTP 503 token=[MASKED]\n11: ERROR HTTP 503",
    );
    expect(w.text()).toContain("已复制脱敏片段");
    w.unmount();
  });
  it("provides the exact manual excerpt after clipboard refusal and rejects invalid range", async () => {
    clipboard.mockRejectedValue(new Error("denied"));
    const w = mount(RunTerminal, {
      props: { runId: "run", live: false, filterOrdinal: 1 },
    });
    await flushPromises();
    await buttons(w, "复制脱敏片段").trigger("click");
    await flushPromises();
    expect(clipboard).not.toHaveBeenCalled();
    await w.findAll("input[type=number]")[0]!.setValue("7");
    await w.findAll("input[type=number]")[1]!.setValue("7");
    await buttons(w, "复制脱敏片段").trigger("click");
    await flushPromises();
    expect(w.find<HTMLTextAreaElement>("textarea").element.value).toBe(
      "7: HTTP 503 token=[MASKED]",
    );
    expect(w.text()).not.toContain("已复制脱敏片段");
    w.unmount();
  });
  it("keeps DOM bounded for 20,000 lines and reveals an off-window error without changing source sequence", async () => {
    api.logs.mockResolvedValue({
      lines: Array.from({ length: 20000 }, (_, i) =>
        line(i + 100, 1, i === 18001 ? "FAIL exit code 1" : `plain ${i}`),
      ),
      complete: true,
      nextSeq: 20100,
    });
    const w = mount(RunTerminal, {
      props: { runId: "large", live: false, filterOrdinal: 1 },
    });
    await flushPromises();
    expect(w.findAll(".term-line")).toHaveLength(500);
    await buttons(w, "定位错误输出").trigger("click");
    await flushPromises();
    expect(w.find(".term-line--active").attributes("data-log-seq")).toBe(
      "18101",
    );
    expect(w.findAll(".term-line").length).toBeLessThanOrEqual(500);
    await buttons(w, "自动换行").trigger("click");
    expect(w.classes()).toContain("term--wrap");
    w.unmount();
  });
  it("uses the exclusive last sequence when reading a subsequent historical page", async () => {
    api.logs
      .mockResolvedValueOnce({
        lines: [line(1, 1, "one")],
        complete: false,
        nextSeq: 2,
      })
      .mockResolvedValueOnce({
        lines: [line(2, 1, "two")],
        complete: true,
        nextSeq: 3,
      });
    const w = mount(RunTerminal, { props: { runId: "pages", live: false } });
    await flushPromises();
    expect(api.logs).toHaveBeenNthCalledWith(2, "pages", 1);
    expect(w.findAll(".term-line")).toHaveLength(2);
    w.unmount();
  });
  it('does not resume following when scrolling to the bottom of a historical live chunk', async () => {
    api.logs.mockResolvedValue({ lines: Array.from({ length: 1000 }, (_, i) => line(i + 1, 1, `line ${i + 1}`)), complete: false, nextSeq: 1001 })
    const w = mount(RunTerminal, { props: { runId: 'live', live: true, filterOrdinal: 1 } }); await flushPromises()
    expect(w.find('.term-line').attributes('data-log-seq')).toBe('501')
    await w.find('.term-pages button').trigger('click'); await flushPromises()
    const scroll = w.find('.term-scroll').element as HTMLElement
    Object.defineProperties(scroll, { scrollHeight: { configurable: true, value: 1000 }, clientHeight: { configurable: true, value: 200 } })
    scroll.scrollTop = 800; await w.find('.term-scroll').trigger('scroll')
    expect(w.find('.term-follow').exists()).toBe(true)
    api.subscribe.mock.calls[0][1].onLog(line(1001, 1, 'new live line')); await flushPromises()
    expect(w.find('.term-line').attributes('data-log-seq')).toBe('1'); expect(w.find('.term-follow').exists()).toBe(true)
    await w.find('.term-follow').trigger('click'); await flushPromises()
    expect(w.find('.term-line').attributes('data-log-seq')).toBe('502'); expect(w.findAll('.term-line').at(-1)!.attributes('data-log-seq')).toBe('1001'); w.unmount()
  })
  it('reveals a distant match on the same line horizontally and vertically with a distinct active mark', async () => {
    api.logs.mockResolvedValue({ lines: [line(7, 1, 'abc' + 'x'.repeat(1000) + 'abc')], complete: true, nextSeq: 8 })
    const w = mount(RunTerminal, { props: { runId: 'wide', live: false, filterOrdinal: 1 } }); await flushPromises()
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      const second = this.dataset.logHit === '1', wrapped = !!this.closest('.term--wrap')
      const left = second && !wrapped ? 1000 : 60, top = second && wrapped ? 900 : 20
      return (this.classList.contains('term-scroll') ? { left: 0, right: 200, top: 0, bottom: 100, width: 200, height: 100, x: 0, y: 0 } : { left, right: left + 24, top, bottom: top + 20, width: 24, height: 20, x: left, y: top }) as DOMRect
    })
    await w.find('input[type=search]').setValue('abc'); await flushPromises()
    await w.find('button[aria-label="下一个匹配"]').trigger('click'); await flushPromises()
    expect(w.find('.term-match--active').attributes('data-log-hit')).toBe('1'); expect((w.find('.term-scroll').element as HTMLElement).scrollLeft).toBeGreaterThanOrEqual(960)
    await buttons(w, '自动换行').trigger('click'); await flushPromises()
    expect((w.find('.term-scroll').element as HTMLElement).scrollTop).toBeGreaterThan(800); expect(w.find('.term-match--active').text()).toBe('abc'); w.unmount()
  })
  it('renders the complete original Unicode match and caps a million-character search at 2000 marks', async () => {
    api.logs.mockResolvedValue({ lines: [line(1, 1, 'İfoo'), line(2, 1, 'a'.repeat(1000000))], complete: true, nextSeq: 3 })
    const w = mount(RunTerminal, { props: { runId: 'huge', live: false, filterOrdinal: 1 } }); await flushPromises()
    await w.find('input[type=search]').setValue('foo'); await flushPromises(); expect(w.find('.term-match').text()).toBe('foo')
    await w.find('input[type=search]').setValue('a'); await flushPromises()
    expect(w.findAll('mark')).toHaveLength(2000); expect(w.find('.term-search-limit').text()).toContain('2000'); expect(w.find('.term-search-limit').text()).toContain('仅显示前'); w.unmount()
  })
  it('limits native search input and explains a programmatically overlong pattern', async () => {
    const w = mount(RunTerminal, { props: { runId: 'limited', live: false } }); await flushPromises()
    const input = w.find('input[type=search]'); expect(input.attributes('maxlength')).toBe('200')
    await input.setValue('a'.repeat(201)); await flushPromises(); expect(w.findAll('mark')).toHaveLength(0); expect(w.find('.term-search-limit').text()).toContain('200'); w.unmount()
  })
});
