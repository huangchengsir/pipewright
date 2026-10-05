export default {
  "repairCredentialDescription": "Select repository credentials for this project again. The project is updated only when you save.",
  "title": "First successful run",
  "intro": "Create a project, prepare its pipeline, then confirm the run yourself.",
  "projectLabel": "Current project",
  "resultProjectLabel": "Successful run's project",
  "projectUnavailable": "Project name unavailable",
  "stepsLabel": "Setup steps",
  "steps": {
    "project": "Create a project",
    "pipeline": "Prepare the pipeline",
    "run": "Run successfully once"
  },
  "stepDescriptions": {
    "project": "Connect your repository. Creating a project currently requires repository credentials.",
    "pipeline": "Save the applicable configuration and check credentials and runtime needed by actual tasks. Unrelated servers are not required.",
    "run": "Submit through the existing confirmation dialog and inspect the pipeline result."
  },
  "stepStates": {
    "saved": "Saved",
    "done": "Done",
    "current": "Current step",
    "pending": "Not yet",
    "unknown": "Unconfirmed"
  },
  "states": {
    "loading": "Reading instance status",
    "unknown": "Unable to confirm yet",
    "create": "Create a project first",
    "queued": "Run queued",
    "running": "Pipeline running",
    "waiting_approval": "Waiting for approval",
    "failed": "Inspect the latest failure",
    "configure": "Continue pipeline setup",
    "unconfirmed": "Check pipeline configuration",
    "repository": "Check repository pipeline",
    "ready": "Ready to open run confirmation",
    "success": "Pipeline run succeeded",
    "legacy": "Existing successful run record",
    "stub": "Demo execution does not complete setup",
    "mixed": "Mixed execution does not complete setup",
    "pending": "Execution evidence unconfirmed"
  },
  "descriptions": {
    "loading": "Only this instance is being read. You can still skip or return to the dashboard.",
    "unknown": "The query could not confirm the full state. This does not mean there are no projects or successful records. Retry.",
    "create": "Use the existing project form, choose repository credentials, and enter a default branch.",
    "queued": "Inspect the selected project's latest task. No additional submission is needed.",
    "running": "Inspect progress and results of the latest run.",
    "waiting_approval": "Open run details for approval status. Setup does not approve anything automatically.",
    "failed": "Inspect this run's logs, then fix the same project before running again.",
    "configure": "Check the applicable issues below. Registering a server does not verify connectivity.",
    "unconfirmed": "Configuration readiness is not yet confirmed. Check configuration or use the existing manual run entry.",
    "repository": "This project uses repository .pipewright.yml. Setup does not fetch the repository or require saving a separate UI configuration.",
    "ready": "Open this project's confirmation dialog. Execution starts only after you submit.",
    "success": "The instance has a successful pipeline record with real execution evidence. This does not additionally prove deployment or service connectivity.",
    "legacy": "The historical success record qualifies for compatibility. It does not additionally prove a real build or deployment.",
    "stub": "This success includes demo execution and does not prove that real tasks worked.",
    "mixed": "This run includes both real and demo tasks, so it does not qualify as the first real success.",
    "pending": "The run succeeded, but its execution evidence is still unconfirmed. Real success is not inferred."
  },
  "actions": {
    "create": "Create project",
    "configure": "Continue configuration",
    "checkPipeline": "Check pipeline",
    "goRun": "Go to run",
    "viewRun": "View run",
    "viewFailed": "View failed logs",
    "edit": "Edit pipeline",
    "viewResult": "View run result",
    "autoTrigger": "Configure automatic triggers",
    "runtimeHelp": "Runtime help",
    "retry": "Retry",
    "open": "Open this step",
    "servers": "Register server",
    "repairCredential": "Change repository credential",
    "credentials": "Manage credentials"
  },
  "issues": {
    "build": "Check configuration and toolchain required by this build path.",
    "tasks": "Check task types, images, and commands. This executor may not support some tasks.",
    "credentials": "A credential reference used by actual tasks is missing.",
    "projectCredential": "The project's repository credential is missing. Select it again in the project.",
    "vault": "The credential vault has no configured master key. Existing credentials cannot be confirmed usable.",
    "environment": "The environment referenced by the current branch is undefined.",
    "noTasks": "The current branch has no executable actual tasks.",
    "notification": "The channel referenced by an actual notification task does not exist.",
    "pipeline": "Check the pipeline structure.",
    "runtime": "The executor selected at startup is in demo mode.",
    "server": "A node or remote runner references a missing server.",
    "storage": "Relevant configuration could not be read. Retry."
  },
  "runtime": {
    "stub": "A demo executor is actually selected. It cannot execute real tasks.",
    "available": "A non-demo executor is selected and a local container CLI was detected. This does not verify container daemon connectivity.",
    "unknown": "Execution capability or remote connectivity is unconfirmed. No automatic connection probes are performed.",
    "instructions": "Check the container CLI, service permissions, and connection settings on the host or container running Pipewright. Remote runners are configured in the project's variables tab. This help does not install software, test SSH, or submit tasks."
  }
}
