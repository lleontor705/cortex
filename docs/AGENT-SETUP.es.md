# Configuración del agente

Cortex funciona con cualquier agente de codificación AI que soporte MCP. Ejecuta `cortex setup <agent>` para la configuración automática.

```bash
# Ver agentes detectados e integraciones instaladas
cortex setup --list

# Instalar con perfiles modulares (dev: 11 herramientas, minimal: 5 herramientas, agent: 22 herramientas)
cortex setup claude-code --profile=dev
cortex setup opencode --profile=agent
```

## Claude Code

```bash
cortex setup claude-code [--profile=agent|dev|minimal]
```

Esto hace:
- Registra un servidor MCP de **ámbito usuario** llamado `cortex`
  (`cortex mcp --tools=<perfil>`) con `claude mcp add-json cortex … --scope user`. Sin
  la CLI `claude`, fusiona `mcpServers.cortex` en `~/.claude.json` conservando todo lo
  demás (un fichero que no se puede parsear no se toca y setup falla). Las
  herramientas aparecen como `mcp__cortex__*`.
- Actualiza `~/.claude/settings.json` con la lista de permitidas del perfil para
  `mcp__cortex__*` y para el espacio del plugin `mcp__plugin_cortex_cortex__*`.
- Elimina `~/.claude/mcp/cortex.json` que dejaban versiones anteriores (Claude Code
  nunca lo leía).

El servidor sigue el modo de ejecución: en modo `remote` la misma entrada
`cortex mcp` hace de proxy hacia tu Cortex server, así que no hace falta URL ni token
por agente (ver `cortex connect`).

### Plugin (opcional)

El plugin incluye su propio servidor MCP (`plugin/claude-code/.mcp.json`,
herramientas `mcp__plugin_cortex_cortex__*`, requiere `cortex` en el `PATH`). Usa
**o bien** el plugin **o bien** `cortex setup claude-code` para no tener dos copias del
mismo servidor; si instalas el plugin, quita la entrada de usuario con
`claude mcp remove cortex --scope user`.

Para la integración completa con hooks (seguimiento de sesión, recuperación tras compactación, recordatorios de guardado):

```bash
claude plugin marketplace add lleontor705/cortex
claude plugin install cortex
```

El plugin proporciona:
- **Hook SessionStart** — carga el contexto de memoria automáticamente
- **Hook Post-compaction** — recupera el contexto tras la compactación
- **Hook UserPromptSubmit** — carga de herramientas del primer mensaje + recordatorio de guardado a los 15 min
- **Hook SubagentStop** — captura pasiva de la salida del subagente
- **Hook Stop** — marca la sesión como finalizada
- **SKILL.md** — Memory Protocol inyectada en el contexto del agente

## OpenCode

```bash
cortex setup opencode [--profile=agent|dev|minimal]
```

Esto crea:
- `~/.config/opencode/cortex-mcp.json` — registro del servidor MCP
- `~/.config/opencode/plugins/cortex.ts` — plugin de TypeScript gestionado e incrustado en el binario de Cortex

El plugin de OpenCode proporciona:
- Seguimiento de sesiones mediante event hooks
- Captura de prompts del usuario
- Inyección del system prompt (Memory Protocol)
- Recuperación tras compactación
- Captura pasiva de la salida de la herramienta Task
- Supresión de sesiones de subagentes

El setup siempre escribe ambos archivos, incluso cuando Cortex se instaló desde un archivo de release o con `go install`. Vuelve a ejecutar el setup tras actualizar Cortex para instalar la versión coincidente del plugin.

## Gemini CLI

```bash
cortex setup gemini-cli
```

Esto crea o actualiza:
- `~/.gemini/settings.json` — fusiona `mcpServers.cortex`; se conservan el resto de
  ajustes y servidores MCP. Si el archivo no se puede parsear, no se toca y setup falla.
- `~/.gemini/system.md` — system prompt de Memory Protocol (solo se escribe si el
  archivo no existe o lo escribió Cortex; nunca reemplaza un prompt propio)

## Codex

```bash
cortex setup codex
```

Esto crea o actualiza:
- `~/.codex/config.toml` — reemplaza solo la tabla `[mcp_servers.cortex]` y añade las
  claves raíz `model_instructions_file` / `experimental_compact_prompt_file` si no las
  definiste; el resto del archivo se conserva. Un TOML inválido no se toca y setup falla.
- `~/.codex/cortex-instructions.md` — instrucciones de Memory Protocol
- `~/.codex/cortex-compact-prompt.md` — instrucciones de recuperación tras compactación

## VS Code (manual)

```bash
code --add-mcp '{"name":"cortex","command":"cortex","args":["mcp"]}'
```

O añade a `.vscode/mcp.json`:
```json
{
  "servers": {
    "cortex": {
      "command": "cortex",
      "args": ["mcp", "--tools=agent"]
    }
  }
}
```

## Cursor / Windsurf / cualquier agente MCP

Añade a la configuración MCP de tu agente:

```json
{
  "mcpServers": {
    "cortex": {
      "command": "cortex",
      "args": ["mcp", "--tools=agent"]
    }
  }
}
```

## Perfiles de herramientas

Controla qué herramientas se cargan:

```bash
  cortex mcp                          # Por defecto: perfil agent (22 herramientas canónicas de agente)
  cortex mcp --tools=agent            # Suite completa de agente (memoria, grafo, AST, blast radius, handoff)
  cortex mcp --tools=dev              # Perfil de desarrollo (11 herramientas: memoria + AST/blast radius/tests)
  cortex mcp --tools=minimal          # Perfil minimalista (5 herramientas esenciales de memoria para modelos rápidos)
  cortex mcp --tools=cortex_save,cortex_search  # Herramientas individuales
```

Los despliegues de servidor exponen un subconjunto autenticado del namespace nativo de Cortex a través de Streamable HTTP en `/mcp`. El servidor no carga los perfiles locales; consulta [MCP.md](MCP.md) para su catálogo exacto. Usa un token bearer y sigue el esquema vigente.
