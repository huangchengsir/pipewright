import { describe, expect, it } from "vitest";
import { i18n, SUPPORTED_LOCALES } from "./index";
const modules = import.meta.glob<{ default: Record<string, unknown> }>(
  "./locales/*/kubernetes.ts",
  { eager: true },
);
function leaves(
  value: Record<string, unknown>,
  prefix = "",
): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [key, v] of Object.entries(value)) {
    if (typeof v === "string") out[prefix + key] = v;
    else
      Object.assign(
        out,
        leaves(v as Record<string, unknown>, prefix + key + "."),
      );
  }
  return out;
}
describe("Kubernetes eight-language coverage", () => {
  it("contains matching nonempty messages and interpolation arguments in every locale", () => {
    const base = leaves(modules["./locales/zh-CN/kubernetes.ts"].default);
    expect(Object.keys(base).length).toBeGreaterThan(130);
    for (const { code } of SUPPORTED_LOCALES) {
      const entries = leaves(
        modules[`./locales/${code}/kubernetes.ts`].default,
      );
      expect(Object.keys(entries).sort()).toEqual(Object.keys(base).sort());
      for (const [key, value] of Object.entries(entries)) {
        expect(value.trim()).not.toBe("");
        expect(value.match(/\{[^}]+\}/g)).toEqual(
          base[key].match(/\{[^}]+\}/g),
        );
        expect(i18n.global.te("kubernetes." + key, code)).toBe(true);
      }
    }
  });
});
