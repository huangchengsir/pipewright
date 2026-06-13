export default {
  // ─── Estados del contenedor ───
  stateRunning: 'En ejecución',
  statePaused: 'En pausa',
  stateRestarting: 'Reiniciando',
  stateCreated: 'Creado',
  stateExited: 'Detenido',
  stateDead: 'Anómalo',
  stateUnknown: 'Desconocido',

  // ─── Botones de acción del ciclo de vida del contenedor ───
  actionStart: 'Iniciar',
  actionRestart: 'Reiniciar',
  actionStop: 'Detener',
  actionPause: 'Pausar',
  actionUnpause: 'Reanudar',
  actionKill: 'Kill',
  actionRm: 'Eliminar',

  // ─── Sugerencias al pasar el cursor sobre los botones de acción ───
  hintStart: 'Inicia un contenedor detenido (docker start), lanzando un proceso nuevo desde cero.',
  hintRestart: 'Reiniciar: primero detiene de forma ordenada (SIGTERM, 10 s de margen) y luego inicia (docker restart).',
  hintStop: 'Detención ordenada: envía SIGTERM y, si no sale en 10 s, añade SIGKILL (docker stop). Úsalo para detener servicios a diario, así el programa puede finalizar y volcar a disco.',
  hintPause: 'Pausar: congela mediante cgroup todos los procesos del contenedor (docker pause); la memoria se conserva tal cual y la CPU deja de asignársela. Pulsa "Reanudar" para continuar desde donde quedó. No libera memoria.',
  hintUnpause: 'Reanudar: descongela un contenedor en pausa (docker unpause); el mismo proceso continúa desde donde se detuvo.',
  hintKill: 'Kill forzado: envía SIGKILL para terminar de inmediato sin oportunidad de limpieza (docker kill); pueden perderse datos no volcados. Úsalo solo cuando "Detener" se bloquea.',
  hintRm: 'Elimina el contenedor (docker rm); si está en ejecución, primero debe detenerse. La configuración del contenedor se elimina, pero los volúmenes de datos montados no se ven afectados.',

  // ─── Confirmación de acciones destructivas ───
  dangerRestartTitle: '¿Reiniciar el contenedor {n}?',
  dangerRestartBody: 'El contenedor se detendrá y se volverá a iniciar; durante ese tiempo su servicio no estará disponible brevemente.',
  dangerRestartConfirm: 'Confirmar reinicio',
  dangerStopTitle: '¿Detener el contenedor {n}?',
  dangerStopBody: 'El contenedor se detendrá, interrumpiendo el servicio que ofrece hasta que se inicie de nuevo.',
  dangerStopConfirm: 'Confirmar detención',
  dangerKillTitle: '¿Forzar Kill del contenedor {n}?',
  dangerKillBody: 'Se enviará SIGKILL para terminar de inmediato el proceso del contenedor; pueden perderse datos no volcados.',
  dangerKillConfirm: 'Kill forzado',
  dangerRmTitle: '¿Eliminar el contenedor {n}?',
  dangerRmBody: 'Se eliminará el contenedor (si está en ejecución, primero debe detenerse). Su configuración se elimina con él; los volúmenes de datos no se ven afectados.',
  dangerRmConfirm: 'Confirmar eliminación',

  // ─── Tipos de parámetro ───
  paramTypeString: 'Texto',
  paramTypeChoice: 'Enumeración',
  paramTypeBoolean: 'Booleano',
  paramTypeNumber: 'Número',

  // ─── Validación de valores de parámetro ───
  paramRequired: 'El parámetro «{label}» es obligatorio',
  paramNotNumber: 'El parámetro «{label}» debe ser un número',
  paramNotBoolean: 'El parámetro «{label}» debe ser true/false',
  paramNotInChoice: 'El parámetro «{label}» no está entre las opciones disponibles',

  // ─── Estado de promoción ───
  promotionPromoted: 'Promovido',
  promotionPending: 'Pendiente de aprobación',
  promotionRejected: 'Rechazado',

  // ─── Validación del nombre del entorno ───
  envNameEmpty: 'El nombre del entorno no puede estar vacío',
  envNameInvalid: 'El nombre del entorno solo puede contener letras, dígitos, guiones y guiones bajos',
  envNameTooLong: 'El nombre del entorno no puede superar los 64 caracteres',

  // ─── Límite de concurrencia ───
  concurrencyNotInteger: 'El límite de concurrencia debe ser un entero',
  concurrencyTooSmall: 'El límite de concurrencia no puede ser inferior a {min}',
  concurrencyTooLarge: 'El límite de concurrencia no puede superar {max}',
  concurrencyUnlimited: 'Sin límite',

  // ─── Terminal ───
  terminalSessionEnded: 'La sesión de terminal ha finalizado',
}
