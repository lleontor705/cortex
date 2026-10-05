# Plugins

Cortex proporciona integración profunda con agentes mediante plugins nativos para
Claude Code y OpenCode.

## Plugin de Claude Code

El plugin de Claude Code añade lifecycle hooks que automatizan la gestión de la
memoria.

### MCP desnudo vs plugin

| Característica | MCP desnudo | Plugin |
|---------|----------|--------|
| Herramientas de memoria | sí | sí |
| Auto-seguimiento de sesión | - | sí |
| Recuperación tras compaction | - | sí |
| Nudge de guardado (15 min) | - | sí |
| Captura pasiva | - | sí |
| Inyección del Memory Protocol | - | sí |
| Auto-carga de ToolSearch | - | sí |

### Estructura del plugin

```
plugin/claude-code/
  .claude-plugin/plugin.json    Plugin descriptor
  hooks/hooks.json              5 lifecycle hooks
  scripts/
    _helpers.sh                 Shared helpers (project detection)
    session-start.sh            Load memory context on startup
    post-compaction.sh          Recover context after compaction
    user-prompt-submit.sh       First-message tool loading + save nudge
    subagent-stop.sh            Passive capture from subagent output
    session-stop.sh             Mark session as ended
  skills/memory/SKILL.md        Memory Protocol for the agent
```

### Lifecycle hooks

#### SessionStart (startup | clear)
1. Asegura que el servidor HTTP de cortex esté en marcha
2. Crea la sesión a través de la API HTTP
3. Obtiene el contexto de memoria del proyecto
4. Inyecta Memory Protocol + contexto en Claude, detallando las herramientas CORE
   (`cortex_save`, `cortex_search`, `cortex_context`, `cortex_session_summary`,
   `cortex_get_observation`, `cortex_update`) y las herramientas diferidas vía
   `ToolSearch`.

#### SessionStart (compact)
1. Asegura que la sesión exista
2. Inyecta Memory Protocol + instrucciones de recuperación tras compaction
3. Indica al agente que: guarde el resumen compactado → cargue el contexto → y
   continúe

#### UserPromptSubmit
- **Primer mensaje**: inyecta la selección de `ToolSearch` para cargar las
  herramientas core y las herramientas de contexto inicial (`cortex_save`,
  `cortex_search`, `cortex_context`, `cortex_session_summary`,
  `cortex_get_observation`, `cortex_update`, `cortex_relate`, `cortex_graph`,
  `cortex_get_agent_context`, `cortex_revision_history`).
- **Siguientes**: si han pasado > 15 min desde el último guardado y la sesión
  tiene > 5 min de antigüedad, incita al agente a guardar

#### SubagentStop (async)
- Captura la salida del subagente y la guarda como observación pasiva

#### Stop (async)
- Marca la sesión como terminada vía API HTTP

Todos los hooks usan `CORTEX_HTTP_PORT` (por defecto `7438`). `CORTEX_PORT` no
está soportado.

### Memory Protocol y perfiles

El fichero `skills/memory/SKILL.md` define comportamientos obligatorios para el
agente en los perfiles soportados:

- **Perfiles soportados**: `agent` (22 herramientas, por defecto), `dev`
  (11 herramientas) y `minimal` (5 herramientas). Las operaciones administrativas
  y temporales se gestionan vía CLI/REST, manteniendo el contexto del agente
  zero-bloat.
- **Guardados proactivos**: después de decisiones, bugfixes, descubrimientos,
  patrones, preferencias.
- **Disparadores de búsqueda**: cuando el usuario recuerda, al empezar trabajo
  relacionado, en el primer mensaje (`auto`, `direct`, `semantic`, `multi_hop`).
- **Grafo de conocimiento**: usa `cortex_relate` y `cortex_graph_path` para mapear
  y recorrer relaciones.
- **AST e impacto del codebase**: consulta símbolos vía
  `cortex_get_code_symbols`, mapea tests impactados con `cortex_code_tests` y
  evalúa el blast radius con `cortex_get_blast_radius`.
- **Cierre de sesión**: `cortex_session_summary` obligatorio con
  Goal/Discoveries/Accomplished/Next Steps/Relevant Files.
- **Recuperación tras compaction**: protocolo obligatorio de 4 pasos.

## Plugin de OpenCode

El plugin TypeScript (`plugin/opencode/cortex.ts`) conecta el sistema de eventos
de OpenCode con Cortex.

### Características

- **Auto-start**: detecta si el servidor de cortex está en marcha y lo arranca si
  no lo está
- **Seguimiento de sesión**: crea sesiones en eventos `session.created`
- **Ciclo de vida de sesión**: termina las sesiones primarias en
  `session.deleted`
- **Supresión de sub-agentes**: detecta los sub-agentes de Task() y omite el
  registro de sesión
- **Captura de prompts de usuario**: guarda los prompts en `user_prompts` vía
  `chat.message`
- **Seguimiento de herramientas**: cuenta las llamadas a herramientas no-Cortex
  por sesión usando una allowlist exhaustiva `CORTEX_TOOLS`
- **Captura pasiva**: extrae learnings de la salida de la herramienta Task
- **Instrucciones de memoria según modo**: inyecta dinámicamente instrucciones
  según el modo activo:
  - **Modo servidor (PostgreSQL)**: IDs de transporte UUID, gobernanza corporativa
    (`cortex_get_project_context`), skills de dominio (`cortex_list_skills`,
    `cortex_get_skill`) y resolución unificada (`cortex_resolve_query`).
  - **Modo local/híbrido (SQLite zero-CGO)**: IDs numéricos enteros,
    inteligencia AST (`cortex_get_code_symbols`, `cortex_code_map`,
    `cortex_code_tests`) y caminos del grafo de conocimiento
    (`cortex_graph_path`).
- **Recuperación tras compaction**: inyecta contexto + instrucciones vía
  `experimental.session.compacting`

### Setup y perfiles modulares

`cortex setup opencode [--profile=agent|dev|minimal]` instala tanto el registro
MCP (`~/.config/opencode/cortex-mcp.json`) como el plugin gestionado
(`~/.config/opencode/plugins/cortex.ts`).

El código TypeScript está incrustado en cada binario de Cortex, incluyendo los
archivos de release y los builds de `go install`. Ejecutar el setup de nuevo
reemplaza el plugin gestionado con la versión incrustada en el binario actual.

### Resolución de la ruta del binario

El plugin usa un fallback de 3 niveles para el binario de cortex:
1. Variable de entorno `CORTEX_BIN` (override explícito)
2. `Bun.which("cortex")` (lookup de PATH en runtime)
3. Ruta absoluta horneada (fallback headless/systemd)

### Compatibilidad con modelos locales

El system prompt se añade al último mensaje de sistema existente (no se empuja
como uno nuevo) para no romper modelos que solo permiten un único bloque de
sistema (Qwen, Mistral vía llama.cpp).

## Privacidad

El contenido envuelto en etiquetas `<private>...</private>` se elimina por el
plugin de OpenCode antes de su llamada HTTP. Este es un comportamiento específico
del plugin; otros clientes MCP/HTTP no redactan esas etiquetas automáticamente.

```
<private>API_KEY=sk-1234</private>  →  [REDACTED]
```

## Tests

Ambos plugins incluyen gates reproducibles que CI hace cumplir en cada pull
request y de nuevo antes del release.

- **OpenCode (`plugin/opencode`)**: un subpaquete npm aislado
  (`@cortex/plugin-opencode`, Node >= 24) con su propio lockfile comprometido.
  Ejecuta `npm ci && npm test` dentro del directorio; el harness Vitest
  (`vitest run`) ejercita los tests de contrato de `cortex.ts` sin un servidor
  Cortex activo ni acceso a red.
- **Claude Code (`plugin/claude-code`)**: `scripts/hooks_test.sh` es un harness
  de contrato determinista y sin red que sustituye `curl` y el binario `cortex`
  con fixtures. Requiere bash, jq, python3 y `timeout` de coreutils; si falta
  alguno sale con 127 para que el gate se reporte BLOCKED en lugar de pasar en
  silencio.

Local vs CI: en estaciones Windows sin jq (o sin una toolchain bash completa) el
harness de Claude está BLOCKED localmente — eso es esperado, no un fallo del
plugin. Los runners `ubuntu-latest` en `.github/workflows/ci.yml` instalan las
dependencias explícitamente y son el ejecutor autoritativo. Los tests de OpenCode
se ejecutan donde haya Node >= 24 disponible, incluyendo Windows.
