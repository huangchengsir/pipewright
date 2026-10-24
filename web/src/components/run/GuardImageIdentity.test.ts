import { describe, expect, it } from "vitest";
import { mount } from "@vue/test-utils";
import GuardImageIdentity from "./GuardImageIdentity.vue";
import { image } from "../../test/releaseGuardFixtures";

describe("guard image identity", () => {
  it("shows a full local Image ID without turning it into a registry reference", () => {
    const digest = `sha256:${"b".repeat(64)}`;
    const wrapper = mount(GuardImageIdentity, {
      props: {
        image: {
          ...image,
          digestKind: "config_id",
          digest,
          identitySource: "script_pre_activation",
        },
      },
    });
    expect(wrapper.text()).toContain("本地 Docker Image ID");
    expect(wrapper.get("code").text()).toBe(digest);
    expect(wrapper.text()).not.toContain("registry.test/app@");
    expect(wrapper.text()).toContain("不是仓库 digest");
    wrapper.unmount();
  });
  it("discloses that an attached local plan has not yet pinned an image", () => {
    const wrapper = mount(GuardImageIdentity, {
      props: {
        image: {
          reference: "",
          repository: "",
          digestKind: "config_id",
          digest: "",
          identitySource: "script_pre_activation",
        },
      },
    });
    expect(wrapper.find("code").exists()).toBe(false);
    expect(wrapper.text()).toContain("构建后、启动前固定 Image ID");
    wrapper.unmount();
  });
  it("preserves repository digest display for standard images and escapes untrusted values", () => {
    const wrapper = mount(GuardImageIdentity, { props: { image } });
    expect(wrapper.get("code").text()).toBe(
      `${image.repository}@${image.digest}`,
    );
    wrapper.unmount();
    const untrusted = mount(GuardImageIdentity, {
      props: {
        image: {
          ...image,
          digestKind: "config_id",
          digest: "<img src=x onerror=alert(1)>",
        },
      },
    });
    expect(untrusted.find("img").exists()).toBe(false);
    expect(untrusted.text()).toContain("<img src=x onerror=alert(1)>");
    untrusted.unmount();
  });
});
