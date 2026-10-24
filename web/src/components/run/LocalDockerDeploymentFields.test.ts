import { describe, expect, it } from "vitest";
import { mount } from "@vue/test-utils";
import LocalDockerDeploymentFields from "./LocalDockerDeploymentFields.vue";
import AppSelect from "../ui/AppSelect.vue";

describe("manual local Docker deployment fields", () => {
  it("requires explicit opt-in and preserves the rest of the configuration", async () => {
    const wrapper = mount(LocalDockerDeploymentFields, {
      props: {
        modelValue: { custom: "keep" },
        artifactType: "archive",
        strategy: "rolling",
      },
    });
    expect(
      wrapper.find('[data-config-key="localDockerPrepareCommand"]').exists(),
    ).toBe(false);
    wrapper.findComponent(AppSelect).vm.$emit("update:modelValue", "true");
    await wrapper.vm.$nextTick();
    const value = wrapper.emitted("update:modelValue")![0][0] as Record<
      string,
      string
    >;
    expect(value).toEqual({ custom: "keep", localDocker: "true" });
    await wrapper.setProps({ modelValue: value });
    expect(wrapper.find('[data-config-key="containerName"]').exists()).toBe(
      true,
    );
    expect(wrapper.text()).toContain("不需要镜像仓库");
    expect(wrapper.text()).toContain("请明确填写本次部署的容器名");
    await wrapper
      .get('[data-config-key="localDockerPrepareCommand"] textarea')
      .setValue("mvn package");
    expect(
      (
        wrapper.emitted("update:modelValue")!.at(-1)![0] as Record<
          string,
          string
        >
      ).localDockerPrepareCommand,
    ).toBe("mvn package");
    await wrapper.setProps({ strategy: "blue_green" });
    expect(wrapper.text()).toContain("暂不支持蓝绿部署");
    wrapper.unmount();
  });
});
