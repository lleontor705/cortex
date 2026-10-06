# Especificación de alineación de plugins

## Objetivo

Mantener las integraciones de Claude Code y OpenCode compatibles con el
almacenamiento local-first de Cortex, la sincronización bidireccional, la
configuración HTTP actual y los contratos de identidad MCP local/servidor.

## Alcance

- Los hooks de Claude usan `CORTEX_HTTP_PORT`, con `7438` por defecto.
- El HTTP local expone `GET /api/sessions/{id}` y `POST /api/prompts`.
- El acceso local desde navegador aplica orígenes exactos de
  `http.allowed_origins` sin debilitar la autenticación de la API.
- OpenCode almacena los mensajes de usuario en `user_prompts`, termina las
  sesiones primarias al borrar y preserva las observaciones pasivas.
- `cortex setup opencode` instala la configuración MCP y el plugin de eventos de
  TypeScript desde cualquier binario de Cortex.
- Los IDs numéricos locales de observación/gráfico, los IDs de sesión locales
  opacos y los UUID del servidor se documentan como contratos distintos.

## Diseño

- Reutiliza `session.Store.GetByID` y `prompt.Store.Save`; sin SQL directo ni
  cambios de esquema.
- CORS envuelve la auth local para que los preflights `OPTIONS` permitidos
  devuelvan `204`, mientras que las llamadas reales a `/api/*` siguen
  autenticadas.
- `plugin/opencode/cortex.ts` sigue siendo la única fuente TypeScript.
  `plugin/opencode/embed.go` incrusta ese fichero directamente para la
  instalación en releases.
- El setup requiere exactamente un marcador de fallback de binario, lo parchea
  con el ejecutable resuelto, y falla en lugar de reportar éxito parcial.
- Las llamadas de sesión y prompt de OpenCode siguen siendo best-effort y
  conservan el redactado y la truncación de etiquetas privadas.

## Criterios de aceptación

1. Los cinco scripts de Claude contienen `CORTEX_HTTP_PORT` y ninguna referencia
   obsoleta a `CORTEX_PORT`.
2. Las sesiones locales existentes devuelven `200`; las sesiones inexistentes
   devuelven `404`; la auth configurada sigue siendo requerida.
3. Los prompts válidos devuelven `201`, persisten en `user_prompts` y no crean
   observaciones.
4. JSON/campos de prompt inválidos devuelven `400`; las sesiones inexistentes
   devuelven `404`.
5. OpenCode usa `/api/prompts`, mantiene la captura pasiva de Task en
   `/api/observations` y termina las sesiones primarias conocidas antes de limpiar
   el estado.
6. Un setup de OpenCode solo con binario escribe exactamente dos ficheros y no
   contiene ningún fallback sin resolver `return "cortex"`.
7. Un preflight CORS permitido devuelve cabeceras de origen exacto; los orígenes
   no permitidos no reciben ninguna; las peticiones reales autenticadas siguen
   protegidas.
8. La documentación distingue los enteros/cadenas de sesión locales de los UUID
   del servidor y describe la instalación determinista del plugin.
9. No se requieren cambios de migración.
10. El build zero-CGO por defecto, los tests completos, el lint y los tests de
    contrato del plugin pasan.

## Verificación

```bash
go test -count=1 ./internal/http ./internal/setup ./plugin/opencode ./plugin/claude-code
go test -count=1 ./...
golangci-lint run ./...
go build ./cmd/cortex
cortex setup opencode
```

El plugin instalado debe existir en `~/.config/opencode/plugins/cortex.ts` y
contener los contratos actuales de prompt/sesión.
