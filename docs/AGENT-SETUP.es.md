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

Esto crea:
- `~/.claude/mcp/cortex.json` — registro del servidor MCP (duradero, sobrevive a las actualizaciones del plugin)
- Actualiza `~/.claude/settings.json` — añade listas de permitidas de herramientas específicas del perfil para la aprobación automática

### Plugin (opcional)

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

Esto crea:
- `~/.gemini/settings.json` — registro del servidor MCP
- `~/.gemini/system.md` — system prompt de Memory Protocol

## Codex

```bash
cortex setup codex
```

Esto crea:
- `~/.codex/config.toml` — registro del servidor MCP
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
