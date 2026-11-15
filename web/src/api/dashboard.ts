import { http, type HttpOptions } from "./http";
import type { RunListResponse } from "./runs";
import type { Server, ServerMetrics } from "./servers";
import type { DoraMetrics, MetricsProject } from "./metrics";
import type { AnomalyAlert } from "./anomaly";
import type { DiagnosisStats } from "./settings";
import type { EnvDeployment } from "./environments";
export type DashboardServer = Pick<Server, "id" | "name" | "host">;
export type DashboardAlert = Pick<
  AnomalyAlert,
  "id" | "metric" | "message" | "serverName" | "at"
>;
export type DashboardDiagnosis = Pick<
  DiagnosisStats,
  "totalFeedback" | "thumbsUp" | "thumbsDown" | "accuracy"
>;
export interface DashboardEnvironment {
  projectId: string;
  projectName: string;
  environment: string;
  active: Pick<EnvDeployment, "commit" | "branch" | "deployedAt"> | null;
}
export interface DashboardData {
  recent: RunListResponse;
  running: RunListResponse;
  projects: MetricsProject[];
  servers: DashboardServer[];
  metrics: ServerMetrics[];
  dora: DoraMetrics;
  alerts: DashboardAlert[];
  diagnosis: DashboardDiagnosis;
  environments: DashboardEnvironment[];
}
export async function getDashboardSection<K extends keyof DashboardData>(
  section: K,
  options: HttpOptions,
): Promise<DashboardData[K]> {
  const response = await http.get<
    DashboardData[K] | { items: ServerMetrics[] }
  >(`/api/dashboard/${section}`, { ...options, throwOnUnauthorized: true });
  return (
    section === "metrics"
      ? (response as { items: ServerMetrics[] }).items
      : response
  ) as DashboardData[K];
}
