export default {
  "title": "Pipeline walkthrough",
  "locate": "Locate control",
  "review": "Configuration entered, continue",
  "refresh": "Check again",
  "close": "Exit walkthrough",
  "unsaved": "Draft changes are not saved. Exiting the walkthrough keeps your edits.",
  "phases": {
    "loading": {
      "title": "Checking configuration",
      "description": "Reading this instance only."
    },
    "unknown": {
      "title": "Status unconfirmed",
      "description": "Check again. Unknown status does not mean ready."
    },
    "repository": {
      "title": "Review repository configuration",
      "description": "This project uses .pipewright.yml. No separate UI save or automatic repository fetch is required."
    },
    "stage": {
      "title": "Add a task stage",
      "description": "Click Add stage on the canvas. The source node needs an actual task alongside it."
    },
    "task": {
      "title": "Add the first task",
      "description": "Click the serial-node control below the stage to open the task picker."
    },
    "choose": {
      "title": "Choose a task type",
      "description": "Choose a task suitable for your project. Custom script is a simple starting point; enter its image and commands yourself."
    },
    "configure": {
      "title": "Enter task configuration",
      "description": "Locate and open a task, then fill its required configuration on the right. Script tasks need an image and commands. Continue when you have entered them."
    },
    "save": {
      "title": "Save and check",
      "description": "Click Save draft in the header. After saving, required configuration is checked again; entering fields alone is not readiness."
    },
    "issues": {
      "title": "Saved, with items to resolve",
      "description": "Follow an item to its configuration. If tasks exist but none apply to this branch, review stage conditions and branch matching, then save."
    },
    "runtime": {
      "title": "Prepare the execution environment",
      "description": "Configuration is saved but execution capability is not ready. Saving again cannot resolve a demo executor."
    },
    "ready": {
      "title": "Ready to confirm a run",
      "description": "Applicable configuration, references and executor checks passed. The run action opens confirmation only; connectivity has not been proven."
    }
  }
}
