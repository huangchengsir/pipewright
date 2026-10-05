export default {
  "title": "Guía de pipeline",
  "locate": "Localizar control",
  "review": "Configuración introducida, continuar",
  "refresh": "Comprobar de nuevo",
  "close": "Salir de la guía",
  "unsaved": "El borrador no está guardado. Salir de la guía conserva los cambios.",
  "phases": {
    "loading": {
      "title": "Comprobando configuración",
      "description": "Solo se lee esta instancia."
    },
    "unknown": {
      "title": "Estado sin confirmar",
      "description": "Comprueba de nuevo. Desconocido no significa listo."
    },
    "repository": {
      "title": "Revisar repositorio",
      "description": "El proyecto usa .pipewright.yml. No exige guardar la UI ni descarga automática del repositorio."
    },
    "stage": {
      "title": "Añadir una etapa",
      "description": "Pulsa añadir etapa en el lienzo. Además del origen hace falta una tarea real."
    },
    "task": {
      "title": "Añadir la primera tarea",
      "description": "Pulsa el nodo en serie debajo de la etapa para abrir el selector."
    },
    "choose": {
      "title": "Elegir tipo de tarea",
      "description": "Elige uno adecuado. Un script personalizado sirve para empezar; introduce su imagen y comandos."
    },
    "configure": {
      "title": "Configurar la tarea",
      "description": "Abre la tarea y completa los campos necesarios a la derecha. Los scripts requieren imagen y comandos. Después continúa."
    },
    "save": {
      "title": "Guardar y comprobar",
      "description": "Guarda el borrador en la cabecera. Se comprobarán los requisitos; rellenar campos no basta."
    },
    "issues": {
      "title": "Guardado, faltan requisitos",
      "description": "Abre cada requisito para configurarlo. Si ninguna tarea corresponde a la rama, revisa las condiciones y guarda."
    },
    "runtime": {
      "title": "Preparar entorno de ejecución",
      "description": "La configuración está guardada pero la ejecución no está lista. Guardar otra vez no resuelve un ejecutor de demostración."
    },
    "ready": {
      "title": "Listo para confirmar ejecución",
      "description": "Configuración, referencias y ejecutor comprobados. Solo se abre la confirmación; no demuestra conectividad."
    }
  }
}
