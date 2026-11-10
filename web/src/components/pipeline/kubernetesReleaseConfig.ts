export const releaseKeys = [
  "version",
  "mode",
  "connectionId",
  "credentialId",
  "context",
  "namespace",
  "executor",
  "timeoutSeconds",
  "observeSeconds",
  "workload",
  "images",
  "source",
  "variables",
  "helm",
  "progressive",
  "checks",
  "coreDNS",
  "privateRef",
] as const;
export function releaseJSON<T>(value: unknown, fallback: T): T {
  if (value === undefined || value === "") return fallback;
  if (typeof value !== "string") return value as T;
  try {
    return JSON.parse(value) as T;
  } catch {
    return fallback;
  }
}
export function releaseField(value: unknown): string {
  return typeof value === "string" ? value : JSON.stringify(value);
}
export function newReleaseConfig(): Record<string, string> {
  return {
    version: "2",
    mode: "image",
    namespace: "default",
    executor: '{"kind":"local"}',
    timeoutSeconds: "300",
    observeSeconds: "30",
    workload: '{"kind":"Deployment","name":""}',
    images: "[]",
    variables: "[]",
    checks: "[]",
  };
}
export function selectReleaseMode(
  config: Record<string, string>,
  mode: string,
): Record<string, string> {
  const next: Record<string, string> = { ...config, version: "2", mode };
  delete next.source;
  delete next.images;
  delete next.workload;
  delete next.helm;
  delete next.progressive;
  delete next.coreDNS;
  if (mode === "image" || mode === "progressive") {
    next.workload = config.workload || '{"kind":"Deployment","name":""}';
    next.images = config.images || "[]";
    if (mode === "progressive")
      next.progressive =
        config.progressive ||
        '{"service":"","candidate":"","trackKey":"track","trackValue":"candidate","batches":[1]}';
  } else {
    next.source =
      config.source ||
      (mode === "helm"
        ? '{"kind":"git","path":"chart"}'
        : '{"kind":"inline","content":""}');
    if (mode === "helm")
      next.helm = config.helm || '{"release":"","hooks":"disabled"}';
  }
  return next;
}

export interface ReleaseSource {
  kind: string;
  path?: string;
  content?: string;
  artifact?: { stageId: string; jobId: string; declarationIndex: number };
}
