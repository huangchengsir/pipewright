import type { PipelineJob, PipelineStage } from "../../api/pipeline";
import { imageSourceChoices } from "./imageProvenance";
import { hasAnyNeeds } from "./stageDeps";
export function kubernetesSources(
  stages: PipelineStage[],
  stage: PipelineStage,
  job: PipelineJob,
) {
  return imageSourceChoices(stages, stage, job).filter(
    (source) =>
      source.job.type === "push_image" && source.stage.id !== stage.id,
  );
}
/** Convert the complete implicit chain before introducing the first explicit edge. */
export function insertKubernetesStage(
  stages: PipelineStage[],
  afterId: string,
  job: PipelineJob,
  id: string,
  name: string,
): PipelineStage[] {
  const at = stages.findIndex((s) => s.id === afterId);
  if (at < 0) return stages;
  const explicit = hasAnyNeeds(stages);
  const next = stages.map((s, i) => ({
    ...s,
    jobs: [...s.jobs],
    needs: explicit ? [...(s.needs ?? [])] : i ? [stages[i - 1].id] : [],
  }));
  for (const s of next)
    if (s.needs.includes(afterId))
      s.needs = s.needs.map((n) => (n === afterId ? id : n));
  const { needs: _needs, ...standalone } = job;
  next.splice(at + 1, 0, {
    id,
    name,
    kind: "deploy",
    needs: [afterId],
    jobs: [standalone],
  });
  return next;
}
export function isKubernetesStage(stage: PipelineStage) {
  return (
    stage.kind === "deploy" &&
    stage.jobs.length === 1 &&
    !Object.keys(stage.matrix ?? {}).length &&
    !stage.allowFailure &&
    !stage.post?.length &&
    !stage.services?.length &&
    !stage.jobs[0]?.needs?.length
  );
}
