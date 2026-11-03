import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import KubeDialog from "./KubeDialog.vue";
let w: VueWrapper | undefined;
beforeEach(() => {
  Object.defineProperty(HTMLDialogElement.prototype, "showModal", {
    configurable: true,
    value() {
      this.setAttribute("open", "");
    },
  });
  Object.defineProperty(HTMLDialogElement.prototype, "close", {
    configurable: true,
    value() {
      this.removeAttribute("open");
    },
  });
});
afterEach(() => {
  w?.unmount();
  document.body.innerHTML = "";
});
describe("Kubernetes dialog accessibility", () => {
  it("names each dialog, focuses its input and restores connected trigger focus", async () => {
    const trigger = document.createElement("button");
    document.body.append(trigger);
    trigger.focus();
    w = mount(KubeDialog, {
      attachTo: document.body,
      props: { open: true, title: "Identity" },
      slots: { default: '<input type="password" />' },
    });
    await flushPromises();
    const dialog = w.get("dialog"),
      id = dialog.attributes("aria-labelledby");
    expect(w.get("#" + id).text()).toBe("Identity");
    expect(document.activeElement).toBe(w.get("input").element);
    await w.setProps({ open: false });
    expect(document.activeElement).toBe(trigger);
  });
  it("does not allow Escape to abandon an in-flight mutation", async () => {
    w = mount(KubeDialog, {
      props: { open: true, title: "Write", busy: true },
    });
    await flushPromises();
    await w.get("dialog").trigger("cancel");
    expect(w.emitted("close")).toBeUndefined();
    await w.setProps({ busy: false });
    await w.get("dialog").trigger("cancel");
    expect(w.emitted("close")).toHaveLength(1);
  });
});

it("keeps password-dialog keyboard events out of the surrounding node-log modal", async () => {
  const outer = vi.fn();
  document.addEventListener("keydown", outer);
  try {
    w = mount(KubeDialog, {
      attachTo: document.body,
      props: { open: true, title: "Password" },
      slots: { default: '<input type="password" />' },
    });
    await flushPromises();
    await w.get("input").trigger("keydown", { key: "Escape" });
    expect(outer).not.toHaveBeenCalled();
  } finally {
    document.removeEventListener("keydown", outer);
  }
});
