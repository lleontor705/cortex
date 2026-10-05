# Runbook de operador — prerequisitos de mantenimiento de Cortex-IA

Audiencia: el operador humano que mantiene esta estación de trabajo
OpenCode + Cortex-IA.
Cada comando de abajo fue verificado contra la CLI instalada
(`cortex-ia v0.5.3`) con sondas de solo lectura `--help`/`doctor`/`status`. No
trates la sintaxis recordada como autoridad: re-ejecuta `cortex-ia help` y
actualiza este runbook siempre que la superficie de la CLI cambie.

Política de secretos: las claves de firmado son inputs proporcionados por el
operador. Nunca escribas un valor secreto en ningún fichero del repositorio;
referéncialo solo como input de entorno o como valor pasado a la CLI.

---

## 1. Heartbeat del host / auto-renovación (mantenimiento de claims y leases)

### Qué se ejecuta
El plugin de OpenCode `~/.config/opencode/plugins/cortex-work.ts` (instalado por
`cortex-ia`) inicia un bucle de mantenimiento en cuanto
`cortex_ia_work_claim` tiene éxito:

- cadencia: `interval_ms: 30000`, timeout de sonda de estado del host:
  `status_timeout_ms: 5000`, corte de progreso obsoleto:
  `stale_progress_ms: 900000`, TTL renovado: `ttl: "15m"`
- cada tick prueba que la sesión del host siga ocupada y después ejecuta
  `cortex-ia work controller-renew <task-id> --owner <controller> --authority @stdin --ttl 15m`
  con el claim token y el conjunto completo de leases en stdin.

**No** existe ningún subcomando de `cortex-ia` llamado `host`, `daemon` ni
`maintenance` (verificado: `cortex-ia host --help` → `Error: unknown command`).
El heartbeat es interno al plugin y no puede activarse desde la CLI; se habilita
teniendo el plugin instalado cargado en una sesión de OpenCode viva y ligada al
workspace.

### Síntoma
- La tarea transiciona sola a `blocked` mientras el TTL del claim (15 min)
  caduca a mitad de la ejecución.
- El recibo del claim reporta
  `"maintenance":{"active":false,"reason":"host_status_unavailable_manual_renewal_required", ...}`.
- La autoridad del puente de `cortex_ia_work_status` muestra
  `write_usable: false` y
  `action: "STOP_WRITING_AND_RECONCILE"`.

### Diagnóstico (solo lectura)
```bash
cortex-ia work status <task-id>          # claim.expires_at, live leases, status, revision
cortex-ia doctor                         # plugin/TUI wiring presence + reporting warnings
cortex-ia --help                         # confirm the installed command surface
```
Vía MCP: `cortex_ia_work_status({ "task_id": "<task-id>" })` →
`bridge_authority.maintenance` devuelve
`{active, reason, interval_ms, status_timeout_ms, stale_progress_ms, ttl}`.

Motivos de parada del mantenimiento y su significado:

| reason | significado |
|---|---|
| `host_status_unavailable_manual_renewal_required` | El plugin no pudo llamar a la API `session.status` del host o la sesión no tiene directorio de workspace — normalmente un plugin obsoleto/frente a una discrepancia de versión con el host de OpenCode. |
| `host_idle_or_unknown` | El host reportó la sesión idle/unknown; la renovación se detiene intencionadamente. |
| `stale_progress` | Sin nuevas partes de mensaje del host durante `stale_progress_ms` (15 min); la renovación se detiene. |
| `maintenance_failed_manual_reconciliation_required` | Un tick de `controller-renew` falló o superó el timeout; se requiere reconciliación manual. |

### Remediación
1. Verifica que el plugin esté instalado y actualizado en la raíz de OpenCode:
   ```bash
   cortex-ia doctor
   cortex-ia sync --target opencode --dry-run   # preview drift (read-only)
   cortex-ia sync --target opencode             # apply: re-register the plugin and theme
   ```
   (`cortex-ia install --target opencode --overwrite` es la ruta equivalente de
   primera instalación; pide confirmación y captura un backup.)
2. Mantén el servidor/sesión de OpenCode vivo y ligado al directorio del workspace
   — el heartbeat solo arranca en el momento del claim, así que re-claim después
   de corregir.
3. Confirma que el siguiente recibo de claim reporta
   `"maintenance":{"active":true}` (sin `reason`).

### Mitigación en el minion (cadencia de renovación dual < 10 min)
Independientemente del bucle del host, el minion en ejecución renueva en una
cadencia estrictamente por debajo del TTL de 15 minutos:

- `cortex_ia_work_renew({ "task_id": ..., "ttl": "15m" })`
- `cortex_ia_work_lease_renew({ "task_id": ..., "path": ..., "ttl": "15m" })`

Equivalentes CLI (los tokens de claim/lease están en memoria oculta del proceso —
nunca los imprimas):
```bash
cortex-ia work renew <task-id> --claim-token <token> [--ttl <duration>]
cortex-ia work lease-renew --path <file> --lease-token <token>
cortex-ia work controller-renew <task-id> --owner <owner> --authority @stdin   # claim + all leases in one call
```
Cualquier fallo de renovación es pérdida de autoridad: deja de escribir
inmediatamente, preserva el diff y transiciona la tarea a `blocked`.

---

## 2. Secreto de firmado para reportes de error

### Síntoma
- `cortex_ia_report_error` falla con
  `authenticated reporting requires a configured signing secret: run "cortex-ia report config --secret <KEY>" or install a release binary, which embeds it`
  (cada reporte sin firmar lo rechaza el hub con HTTP 401).
- `cortex-ia report status` muestra el signing secret como no configurado.
- `cortex-ia doctor` emite: `error reporting has no signing secret ... run "cortex-ia report config --secret <KEY>"`.

### Comando
```bash
cortex-ia report config [--endpoint <url>] [--secret <key>] [--enable|--disable]
cortex-ia report status       # current endpoint/enablement/signing configuration
cortex-ia report flush        # retry queued reports after fixing the secret
```

### Generación y entrega de la clave
La clave es un secreto de firmado HMAC compartido con el hub de reportes; es un
input proporcionado por el operador:
```bash
openssl rand -hex 32
cortex-ia report config --secret <KEY>          # <KEY> is the generated value, never committed
```
Alternativa (los builds desde fuente lo persisten en la primera configuración):
exporta `CORTEX_REPORT_SECRET` (y opcionalmente `CORTEX_REPORT_ENDPOINT`) en el
entorno del operador; la CLI reporta
`error reporting signing secret persisted from CORTEX_REPORT_SECRET` cuando
bootstrapa `~/.cortex-ia/telemetry.json` desde el entorno.

### Dónde vive la clave
- `~/.cortex-ia/telemetry.json` con los campos `endpoint`, `secret`, `enabled`;
  modo de fichero `0600` dentro de un directorio `0700`.
- Los binarios de release incrustan el secreto canónico en tiempo de link, así que
  un secreto incrustado nunca se escribe en ese fichero.
- Precedencia: `CORTEX_REPORT_ENDPOINT` (entorno) sobreescribe el endpoint del
  fichero; el orden de resolución del secreto es entorno
  (`CORTEX_REPORT_SECRET`) → fichero → secreto embebido del release.
- Nunca almacenes el valor en el repositorio, en contratos de tarea ni en
  observaciones de Cortex.

### Verificación
```bash
cortex-ia report status
cortex-ia doctor
```
Sonda manual (envía un reporte real al endpoint configurado — úsala solo cuando
estés probando la entrega intencionadamente):
```bash
cortex-ia report error --code <code> --message <msg> [--details <text|@stdin>] [--task <id>] [--job <id>] [--source <source>]
```

---

## 3. Estado atascado de Ollama

### Síntoma
- Nada escuchando en `127.0.0.1:11434`; `ollama version` / `ollama list` se
  cuelgan; la app Electron de Ollama está inerte; las herramientas basadas en
  modelos agotan el timeout.

### Diagnóstico (solo lectura)
```bash
lsof -nP -iTCP:11434 -sTCP:LISTEN
pgrep -fl ollama
curl -sS -m 5 http://127.0.0.1:11434/api/tags
```

### Remediación
```bash
pkill -f ollama                 # terminate the wedged serve process
open -a Ollama                  # macOS relaunch (or: ollama serve)
curl -sS -m 5 http://127.0.0.1:11434/api/tags   # must return a JSON model list
ollama list                     # confirms models are visible again
```

### Notas de macOS
- Un relanzamiento tras `pkill` puede disparar un prompt TCC
  (ficheros/red); apruébalo o concédelo en Ajustes del Sistema → Privacidad y
  Seguridad.
- Binarios o bundles de app descargados fuera del App Store pueden estar en
  cuarentena de Gatekeeper; apruébalos en Ajustes del Sistema → Privacidad y
  Seguridad → Abrir de todos modos.

---

## 4. Runbook de recuperación de TTL de claim (reconciliación del orquestador)

Secuencia para una tarea que perdió autoridad porque el TTL del claim caducó:

1. **Leer estado** (solo lectura):
   ```bash
   cortex-ia work status <task-id>    # status, revision, claim.expires_at, leases
   ```
   MCP: `cortex_ia_work_status({ "task_id": "<task-id>" })` →
   `bridge_authority.action`.
2. **Barrer la autoridad caducada**:
   ```bash
   cortex-ia work recover
   ```
   Mueve las tareas `in_progress` con claim caducado a `blocked` (evento
   `claim_expired`) y borra los leases caducados. Nota: las tareas `in_review`
   siguen siendo aprobables — solo se recuperan revisiones abandonadas más
   antiguas que la ventana de staleness.
3. **Liberar un claim huérfano pero aún vivo** (la sesión owner está muerta):
   ```bash
   cortex-ia work reconcile <task-id> --reason <text> --session <host-session-id> --revision <n> [--to ready] [--owner-session-inactive <true|false>]
   ```
   Falla cuando el claim lo tiene una sesión viva; úsalo solo para huérfanos.
4. **Reintentar con revisión CAS**:
   ```bash
   cortex-ia work retry <task-id> --revision <n>
   ```
   Precondiciones: estado `blocked`, revisión actual exacta (mismatch →
   `stale retry revision` / `task is blocked at revision X, not Y`), todas las
   dependencias `done`, sin descomposición atómica, presupuesto de intentos
   restante y menos de dos veredictos `FAIL` de review consecutivos.
   Efecto: limpia claims/leases/reviews y pone la tarea en `ready`.
5. **Re-dispatch**: el minion nuevo reclama autoridad fresca con TTL de 15
   minutos y reanuda la cadencia de renovación dual (< 10 min).
6. **Cuándo usar un BLOCKED-approve**:
   ```bash
   cortex-ia work approve <task-id> --reviewer <id> [--revision <n>] --verdict <PASS|FAIL|BLOCKED|INCONCLUSIVE> [--evidence <ref>]
   ```
   - La tarea debe estar `in_review`; cualquier veredicto distinto de `PASS` la
     mueve a `blocked` y libera sus claims/leases; `PASS` requiere `--evidence` y
     la mueve a `done`.
   - Usa `BLOCKED` cuando la review no pueda concluir legítimamente `PASS` ni
     `FAIL` (pérdida de autoridad a mitad de review, evidencia ausente,
     envío no verificable). `BLOCKED` e `INCONCLUSIVE` los salta el breaker de
     descomposición por dos `FAIL` consecutivos, así que un blocked-approve
     registra el resultado sin consumir la racha de fallos de review.
   - Tras un blocked-approve la tarea vuelve a estar `blocked`: corrige la causa
     y reanuda en el paso 4 (`retry --revision <n>` con la revisión nueva).

Disciplina de renovación dual durante todo el proceso: renueva el claim y cada
file lease juntos en una cadencia < 10 minutos; trata cualquier fallo de
renovación como pérdida inmediata de autoridad.

---

## 5. `cortex_save` degradado — "write could not be persisted"

### Síntoma
`cortex_save` devuelve el fallo de contrato `write could not be persisted`
(`internal/mcp/memorycontract/memorycontract.go`).

### Causa probable
Sesiones concurrentes compitiendo por la base de datos SQLite de Cortex
(lock/busy en el writer) o un error de escritura del store mientras otra sesión
tiene la BD.

### Remediación
- Reintenta el mismo `cortex_save` una vez tras una pausa breve; los guardados son
  upserts con clave en `topic_key`, así que un reintento es idempotente.
- Si se repite, espacia las escrituras: serializa los guardados de memoria a
  través de una única sesión o prográmalos en lugar de dispararlos desde
  subagentes paralelos en el mismo momento.
- Comprueba si hay un daemon `cortex watch` atascado u otra sesión reteniendo la
  BD antes de escalar; nunca reintentes en un bucle apretado.

---

## Nota de mantenimiento
Este runbook refleja la superficie de la CLI instalada (verificado:
`cortex-ia --help`, `cortex-ia work --help`, `cortex-ia report --help`,
`cortex-ia doctor`, `cortex-ia work status`, `cortex-ia report status`). Si una
futura versión de `cortex-ia` renombra o elimina un comando, actualiza este
fichero en el mismo cambio; las instrucciones obsoletas nunca deben tratarse
como autoridad frente a `--help`.
