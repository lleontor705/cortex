# Especificación de remediación de auditoría E2E

## Objetivo

Garantizar que los artefactos de release de Cortex, la aplicación web, la
sincronización autenticada y el arranque de procesos estén cubiertos por gates
deterministas que coincidan con la composición de producción.

## Alcance

### Fase 1: Gates de release

- Hacer que `reindex` evite el proceso y la gestión de modelos de Ollama cuando
  `search.ollama_auto_start` esté deshabilitado.
- Devolver éxito sin contactar con el proveedor de embeddings cuando un build con
  vectores habilitados no tiene observaciones que reindexar.
- Mantener sin cambios el guard del almacén de vectores del build zero-CGO por
  defecto.
- Hacer que la advertencia del instalador fuera de `PATH` no sea fatal.
- Ejecutar el suite completo de Go con `cortex_vectors` en CI y antes de la
  aprobación de release.
- Ejecutar `npm ci`, Vitest y el build web de producción en CI y antes de la
  aprobación de release.

### Fase 2: E2E de composición

- Añadir un round trip con `postgres_integration` usando dos bases de datos SQLite
  reales, HTTP autenticado, el `AuthorizedStore` del servidor y PostgreSQL.
- Verificar la rechazo de sincronización no autorizada, la preservación de
  identidad en push/pull y la idempotencia de reintentos.
- Añadir un smoke test de proceso en Ubuntu que construye la variante de release,
  arranca `cortex --mode server`, espera un endpoint TCP de loopback, comprueba
  `/health` y el comportamiento autenticado de la API, y después verifica un
  apagado limpio.

## Non-goals

- Cambiar los filtros intencionalmente distintos de CI, validación de PR o release.
- Ampliar la lista de paquetes del detector de race en este cambio.
- Añadir migraciones, endpoints, servicios vectoriales externos, Ollama o
  descargas de red a los tests.
- Exponer repositorios PostgreSQL crudos desde el runtime del servidor.
- Mover los comandos web al `package.json` raíz, cuyo script de test falla
  intencionalmente.

## Diseño

### Reindex

`runReindex` conserva los guards del proveedor y del almacén de vectores, analiza
el filtro de proyecto y lista las observaciones antes de la preparación específica
de Ollama del comando. Imprime el resumen normal y devuelve cero de inmediato para
una lista vacía. El arranque del proceso de Ollama y los pulls de modelos se
ejecutan solo cuando el proveedor es `ollama`, el auto-start está habilitado y
existe trabajo.

La composición de la aplicación sigue siendo responsable de su comportamiento
global de auto-start existente. Los tests que no requieren contacto con el
proveedor configuran `ollama_auto_start: false`.

### Instalador

Define `warn` junto a los helpers de salida existentes. Escribe una advertencia
sin salir. Un test de contrato de Go portable verifica que la definición precede
a cada uso; la sintaxis de shell se valida por separado.

### CI y release

Añade jobs independientes `vector-tests` y `web-tests` para que puedan ejecutarse
en paralelo. El job de vectores ejecuta
`go test -v -count=1 -tags cortex_vectors ./...`. El job web usa Node 24 con
caché de npm con raíz en `web/package-lock.json`, y después ejecuta todos los
comandos desde `web/`.

La aprobación de release depende de ambos jobs nuevos. Los filtros de branch,
gates de PostgreSQL, umbral de cobertura, contratos de baseline y alcance de race
existentes permanecen sin cambios.

### Sync E2E

El test de fase 2 vivirá bajo `internal/platform/server` con
`postgres_integration`. Abrirá la composición real del servidor, servirá en TCP
de loopback, rechazará una petición sin token, hará push desde una fuente SQLite
migrada a través de `RemoteSyncer` y hará pull en una segunda base de datos SQLite
migrada. Las aserciones usan IDs de sincronización y campos estables en lugar de
IDs numéricos locales. Un segundo round trip demuestra la idempotencia.

### Smoke de proceso

El smoke de fase 2 construirá `cmd/cortex` con `cortex_vectors`, lanzará ese
binario contra el fixture de integración de PostgreSQL, sondeará `/health` con un
deadline fijo, ejercitará una ruta protegida de la API sin y con credenciales, y
terminará el proceso con cleanup acotado.

## Criterios de aceptación

1. `scripts/install.sh` define `warn` antes de usarlo y la ruta fuera de `PATH` ya
   no falla con `command not found`.
2. Un `reindex` con vectores habilitados contra una base de datos vacía devuelve
   cero sin requerir Ollama.
3. El arranque de Ollama específico del comando y los pulls de modelos requieren
   `ollama_auto_start: true`.
4. El build por defecto sigue reportando que el almacén de vectores no está
   disponible.
5. `go test -v -count=1 -tags cortex_vectors ./...` pasa y es un gate de CI y de
   release.
6. `npm ci`, `npm test` y `npm run build` pasan bajo `web/` y son gates de CI y de
   release.
7. La aprobación de release depende explícitamente de los jobs de vectores y web.
8. Los filtros de branch existentes y el alcance del detector de race no cambian.
9. La fase 2 demuestra sincronización SQLite -> HTTP autenticado -> PostgreSQL ->
   SQLite sin mocks en esas fronteras.
10. La fase 2 demuestra arranque real de proceso, health TCP, autenticación de API
    y apagado acotado.
11. No se introducen cambios de esquema, endpoint ni de frontera de autorización
    de producción.

## Verificación

```bash
go test -v -count=1 ./scripts
go test -v -count=1 ./internal/cli -run 'Test.*Reindex'
go test -v -count=1 -tags cortex_vectors ./internal/cli -run 'Test.*Reindex'
go test -v -count=1 -tags cortex_vectors ./...
go test -v -count=1 ./...
bash -n scripts/install.sh
cd web && npm ci && npm test && npm run build
golangci-lint run ./...
```

La fase 2 requiere además el fixture de PostgreSQL 16 y los DSN documentados en
`AGENTS.md`.

## Riesgos

- El suite completo de vectores aumenta el tiempo de CI; un job paralelo separado
  limita el impacto en el camino crítico.
- Las dependencias npm que usan `latest` pueden hacer que las instalaciones
  deriven pese al lockfile; `npm ci` preserva la resolución comprometida.
- Los tests de TCP/proceso pueden volverse flaky; la fase 2 requiere puertos de
  loopback dinámicos, deadlines explícitos, logs acotados y cleanup incondicional.
- Los datos E2E de PostgreSQL pueden colisionar entre ejecuciones; la fase 2 debe
  usar identificadores únicos de tenant/workspace.

## Entrega

- La fase 1 se implementa con gates de vectores y web alineados al release, el
  ordenamiento de reindex y la validación del instalador.
- La fase 2 se implementa bajo `postgres_integration` en
  `internal/platform/server/e2e_postgres_integration_test.go`: cubre el round trip
  de sincronización autenticado y el smoke de proceso en Linux.
- Los jobs de PostgreSQL serializan paquetes con `GOFLAGS=-p=1` porque el test de
  conformidad de rollback de migraciones y los tests E2E comparten la base de
  datos de CI. El test de rollback restaura la secuencia completa de migraciones.
- La expansión de race se evalúa por separado usando datos medidos de duración y
  flake de la fase 2.
