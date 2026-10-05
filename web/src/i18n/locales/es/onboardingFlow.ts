export default {
  "repairCredentialDescription": "Vuelve a elegir las credenciales de este proyecto. Solo se actualiza cuando guardas.",
  "title": "Primera ejecución correcta",
  "intro": "Crea un proyecto, prepara su pipeline y confirma la ejecución tú mismo.",
  "projectLabel": "Proyecto actual",
  "resultProjectLabel": "Proyecto de la ejecución correcta",
  "projectUnavailable": "Nombre de proyecto no disponible",
  "stepsLabel": "Pasos de configuración",
  "steps": {
    "project": "Crear un proyecto",
    "pipeline": "Preparar el pipeline",
    "run": "Ejecutar correctamente una vez"
  },
  "stepDescriptions": {
    "project": "Conecta tu repositorio. Crear un proyecto requiere actualmente credenciales del repositorio.",
    "pipeline": "Guarda la configuración aplicable y comprueba las credenciales y el entorno necesarios para las tareas reales. No se requieren servidores ajenos.",
    "run": "Envía la tarea mediante el diálogo de confirmación existente y revisa el resultado."
  },
  "stepStates": {
    "saved": "Guardado",
    "done": "Completado",
    "current": "Paso actual",
    "pending": "Pendiente",
    "unknown": "Sin confirmar"
  },
  "states": {
    "loading": "Leyendo el estado de la instancia",
    "unknown": "Todavía no se puede confirmar",
    "create": "Crea primero un proyecto",
    "queued": "Ejecución en cola",
    "running": "Pipeline en ejecución",
    "waiting_approval": "Esperando aprobación",
    "failed": "Revisa el último fallo",
    "configure": "Continúa la configuración",
    "unconfirmed": "Comprueba la configuración del pipeline",
    "repository": "Comprueba el pipeline del repositorio",
    "ready": "Puedes abrir la confirmación",
    "success": "El pipeline se ejecutó correctamente",
    "legacy": "Hay un registro de ejecución correcta",
    "stub": "La ejecución de demostración no completa la guía",
    "mixed": "La ejecución mixta no completa la guía",
    "pending": "Origen de ejecución sin confirmar"
  },
  "descriptions": {
    "loading": "Solo se leen datos de esta instancia. Puedes omitir la guía o volver al panel.",
    "unknown": "No se pudo confirmar el estado completo. Esto no significa que falten proyectos o registros correctos. Reintenta.",
    "create": "Usa el formulario existente, elige las credenciales e indica la rama predeterminada.",
    "queued": "Revisa la última tarea del proyecto seleccionado. No necesitas enviarla otra vez.",
    "running": "Consulta el progreso y el resultado de la última ejecución.",
    "waiting_approval": "Consulta la aprobación en los detalles. La guía no aprueba nada automáticamente.",
    "failed": "Consulta los registros de esta ejecución y corrige el mismo proyecto antes de repetirla.",
    "configure": "Revisa los requisitos aplicables indicados abajo. Registrar un servidor no verifica su conexión.",
    "unconfirmed": "La preparación aún no está confirmada. Comprueba la configuración o usa la ejecución manual existente.",
    "repository": "Este proyecto usa .pipewright.yml del repositorio. La guía no descarga el repositorio ni exige guardar otra configuración de interfaz.",
    "ready": "Abre la confirmación de este proyecto. Solo se ejecuta cuando envías la tarea.",
    "success": "La instancia tiene un registro correcto con evidencia de ejecución real. No demuestra además el despliegue ni la conexión del servicio.",
    "legacy": "El registro histórico cumple las reglas de compatibilidad. No demuestra además una compilación o despliegue reales.",
    "stub": "Este resultado incluye una demostración y no demuestra que funcionen las tareas reales.",
    "mixed": "La ejecución combina tareas reales y de demostración y no cuenta como primera ejecución real correcta.",
    "pending": "La ejecución terminó correctamente, pero su evidencia sigue sin confirmar. No se deduce un éxito real."
  },
  "actions": {
    "create": "Crear proyecto",
    "configure": "Continuar configuración",
    "checkPipeline": "Comprobar pipeline",
    "goRun": "Ir a ejecutar",
    "viewRun": "Ver ejecución",
    "viewFailed": "Ver registros del fallo",
    "edit": "Editar pipeline",
    "viewResult": "Ver resultado",
    "autoTrigger": "Configurar disparadores automáticos",
    "runtimeHelp": "Ayuda del entorno de ejecución",
    "retry": "Reintentar",
    "open": "Abrir este paso",
    "servers": "Registrar servidor",
    "repairCredential": "Cambiar credencial del repositorio",
    "credentials": "Administrar credenciales"
  },
  "issues": {
    "build": "Comprueba la configuración y las herramientas necesarias para este tipo de compilación.",
    "tasks": "Comprueba tipos de tarea, imágenes y comandos. El ejecutor puede no admitir algunas tareas.",
    "credentials": "Falta una referencia de credencial usada por las tareas reales.",
    "projectCredential": "Falta la credencial del repositorio. Selecciónala de nuevo en el proyecto.",
    "vault": "La bóveda no tiene una clave maestra configurada. No se puede confirmar que las credenciales sean utilizables.",
    "environment": "El entorno referido por la rama actual no está definido.",
    "noTasks": "La rama actual no tiene tareas reales ejecutables.",
    "notification": "No existe el canal referido por la tarea de notificación.",
    "pipeline": "Comprueba la estructura del pipeline.",
    "runtime": "El ejecutor seleccionado al iniciar está en modo de demostración.",
    "server": "Un nodo o ejecutor remoto refiere a un servidor inexistente.",
    "storage": "No se pudo leer la configuración relacionada. Reintenta."
  },
  "runtime": {
    "stub": "Está seleccionado un ejecutor de demostración que no puede ejecutar tareas reales.",
    "available": "Se ha seleccionado un ejecutor real y detectado la CLI local de contenedores. Esto no verifica la conexión al servicio de contenedores.",
    "unknown": "La capacidad de ejecución o la conexión remota no están confirmadas. No se realizan pruebas automáticas.",
    "instructions": "Comprueba la CLI, los permisos del servicio y la conexión del host o contenedor que ejecuta Pipewright. Los ejecutores remotos se configuran en la pestaña de variables del proyecto. Esta ayuda no instala software, prueba SSH ni envía tareas."
  }
}
