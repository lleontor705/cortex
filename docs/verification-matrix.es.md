# Matriz de verificación de Cortex v2

## Resumen

Este documento define los gates de verificación, los comandos de ejecución, los
prerrequisitos, los criterios de paso, la trazabilidad a la fuente y el estado
ambiental para Cortex v2.

### Autoridad y jerarquía de verificación

1. **CI autoritativo (`.github/workflows/ci.yml`)**: los workflows de GitHub
   Actions que corren en `ubuntu-latest`, `windows-latest` y `macos-latest`
   proporcionan el registro final y autoritativo de pass/fail.
2. **Verificación local pre-push**: secuencia local de gates impuesta antes del
   push (`golangci-lint run ./...` seguido de `go test -v ./...`).
3. **Comprobaciones condicionadas por entorno**: gates que requieren
   infraestructura específica (PostgreSQL 16 con credenciales de bootstrap, el
   daemon de Docker o CGO/gcc) que reportan `BLOCKED` en local cuando faltan los
   prerrequisitos.

> [!IMPORTANT]
> **Reporte veraz y restricciones de sesión**:
> Por las instrucciones de dispatch de esta sesión de mantenimiento
> (*"no test/build/install, no source/config edits, no commits"*), **no se ejecutó
> ningún suite de tests durante esta sesión de documentación**. Todos los estados
> de gate siguientes reflejan contratos de repositorio documentados, definiciones
> de pipeline y dependencias de toolchain. Cada estado de gate se documenta como
> `Unexecuted in Session (Authority: CI)` o `ND/pending`. No se hacen afirmaciones
> `PASS` de runtime no ejecutado.

---

## 1. Gates de verificación y matriz de ejecución

| Gate de verificación | Alcance y propósito | Comando / invocación | Prerrequisitos y toolchain | Criterios de paso e invariantes | Trazabilidad a la fuente | Clasificación del gate | Estado de sesión |
|---|---|---|---|---|---|---|---|
| <a id="gate-go-compilation"></a>**Compilación Go**<br>`gate_id: go-compilation` | Compila todos los paquetes en modo default zero-CGO | `go build ./...`<br>*(`make build`)* | Go 1.26.5 | Compilación limpia con código de salida 0; produce `bin/cortex`. | [`.github/workflows/ci.yml:35`](../.github/workflows/ci.yml#L35), [`Makefile:60`](../Makefile#L60) | CI obligatorio / Local default | Unexecuted in Session (Authority: CI) |
| <a id="gate-architecture-boundary"></a>**Frontera de arquitectura**<br>`gate_id: architecture-boundary` | Hace cumplir zero-CGO y aislamiento de imports en los paquetes locales | `go test -v -count=1 ./internal/app -run '^TestArchitectureBoundaries$'` | Go 1.26.5 | Zero-CGO; sin imports de PostgreSQL, authz/identidad, Qdrant/pgvector ni composición de servidor en paquetes locales. | [`internal/app/arch_test.go`](../internal/app/arch_test.go), [`AGENTS.md`](../AGENTS.md) | CI obligatorio / Local default | Unexecuted in Session (Authority: CI) |
| <a id="gate-go-unit-tests"></a>**Tests unitarios Go**<br>`gate_id: go-unit-tests` | Valida la lógica de negocio core, los servicios de dominio y la persistencia SQLite | `go test -v -count=1 ./...`<br>*(`make test`)* | Go 1.26.5 | Todos los tests unitarios pasan con código de salida 0; sin panics ni fallos de test. | [`.github/workflows/ci.yml:53`](../.github/workflows/ci.yml#L53), [`Makefile:71`](../Makefile#L71) | CI obligatorio / Local default | Unexecuted in Session (Authority: CI) |
| <a id="gate-vector-enabled-tests"></a>**Tests con vectores habilitados**<br>`gate_id: vector-enabled-tests` | Ejercita la implementación de vectores coseno-scan BLOB de SQLite | `go test -v -count=1 -tags cortex_vectors ./...` | Go 1.26.5; tag `cortex_vectors` | Los tests de vectores BLOB de SQLite pasan sin regresión; ruta de vectores equivalente a release verificada. | [`.github/workflows/ci.yml:76`](../.github/workflows/ci.yml#L76), [`internal/vector/sqlite_blob/adapter.go`](../internal/vector/sqlite_blob/adapter.go) | CI obligatorio / Local con tag | Unexecuted in Session (Authority: CI) |
| <a id="gate-schema-ledger-migration"></a>**Esquema y migración de ledger**<br>`gate_id: schema-ledger-migration` | Valida los checksums de la línea base SQLite y el ledger de servidor PostgreSQL | `go test -v -count=1 ./internal/migration` | Go 1.26.5 | El checksum de la línea base SQLite coincide con `cortex_meta`; los checksums de migración de servidor PostgreSQL hasta el runtime head fijado 112 se verifican; los preflights pasan. | [`internal/migration/v2_test.go`](../internal/migration/v2_test.go), [`internal/migration/postgres_test.go`](../internal/migration/postgres_test.go) | CI obligatorio / Local default | Unexecuted in Session (Authority: CI) |
| <a id="gate-obsidian-path-portability"></a>**Portabilidad de rutas Obsidian**<br>`gate_id: obsidian-path-portability` | Valida la seguridad del filesystem entre sistemas operativos | `go test -v -count=1 ./internal/projection/obsidian -run 'Test(SafeSlug\|WindowsDeviceNameNearMisses\|CanonicalPathKey\|ExportCanonicalCollision\|ExportRejectsCaseInsensitiveCollision)'` | Go 1.26.5; corre en runners `windows-latest` y `macos-latest` | Rechaza nombres de dispositivo Windows (`CON`, `PRN`, `AUX`, `NUL`, `COM1-9`, `LPT1-9`); detecta colisiones insensibles a mayúsculas. | [`.github/workflows/ci.yml:145`](../.github/workflows/ci.yml#L145), [`internal/projection/obsidian/writer_test.go`](../internal/projection/obsidian/writer_test.go) | CI matriz (Windows/macOS) | Unexecuted in Session (Authority: CI) |
| <a id="gate-offline-retrieval-baseline"></a>**Baseline de retrieval offline**<br>`gate_id: offline-retrieval-baseline` | Valida la matemática de retrieval determinista y los contratos de aislamiento | `go test -v -count=1 ./bench ./bench/common ./bench/cortex ./bench/fixtures/cortex-native ./bench/cortex/cmd/baseline`<br>*(`make test-baseline`)* | Go 1.26.5; cero red externa, embeddings o descargas de dataset | Verifica `cortex.retrieval-corpus/v1` y `retrieval-evidence-report/v1`; cero violaciones de aislamiento; coincidencia exacta de filtros. | [`.github/workflows/ci.yml:228`](../.github/workflows/ci.yml#L228), [`Makefile:90`](../Makefile#L90), [`docs/BENCHMARKS.md`](BENCHMARKS.md) | CI obligatorio / Gate offline | Unexecuted in Session (Authority: CI) |
| <a id="gate-static-analysis-lint"></a>**Análisis estático (lint)**<br>`gate_id: static-analysis-lint` | Comprueba estilo de código, patrones de bug y convenciones | `golangci-lint run ./...`<br>*(`make lint`)* | `golangci-lint` fijado a v2.11.4 en CI | Cero issues o warnings de linter reportados. | [`.github/workflows/ci.yml:295`](../.github/workflows/ci.yml#L295), [`Makefile:119`](../Makefile#L119) | CI obligatorio / Gate pre-push | Unexecuted in Session (Authority: CI) |
| <a id="gate-race-detector"></a>**Detector de race**<br>`gate_id: race-detector` | Detecta data races en stores concurrentes y en el runtime MCP | `go test -race -count=1 ./internal/store/search ./internal/store/bundle ./internal/mcp` | Go 1.26.5, CGO habilitado con `gcc` funcional | Cero data races reportadas bajo ejecución concurrente. | [`.github/workflows/ci.yml:312`](../.github/workflows/ci.yml#L312), [`AGENTS.md`](../AGENTS.md) | CI obligatorio / Condicionado por entorno | Unexecuted in Session (Authority: CI) |
| <a id="gate-postgresql-integration"></a>**Integración PostgreSQL**<br>`gate_id: postgresql-integration` | Testea el runtime de servidor, las migraciones 100–112 y el RLS de tenant constante | `go test -v -count=1 -tags "integration postgres_integration" ./...`<br>*(`make test-integration`)* | PostgreSQL 16+, `scripts/postgres/bootstrap-authz.sql` aplicado, 3 DSNs distintos configurados | Todas las operaciones de base de datos pasan bajo el tenant configurado constante; RLS y los suites de sonda de explotación RLS retenidos (`authz_negative_integration_test.go`, `workspace_security_integration_test.go`) deniegan el acceso escapado a través de los lotes `postgres_integration` reworkeados. Falla si faltan los DSNs. | [`.github/workflows/ci.yml:271`](../.github/workflows/ci.yml#L271), [`Makefile:82`](../Makefile#L82), [`AGENTS.md`](../AGENTS.md) | CI obligatorio / Condicionado por entorno | Unexecuted in Session (Authority: CI) |
| <a id="gate-whole-project-go-coverage"></a>**Cobertura Go de todo el proyecto**<br>`gate_id: whole-project-go-coverage` | Hace cumplir el umbral mínimo de cobertura de código para el codebase Go | `cover_pkgs="$(go list ./... \| grep -v '/node_modules/' \| paste -sd, -)"`<br>`go test -tags postgres_integration -covermode=atomic -coverpkg="$cover_pkgs" -coverprofile=coverage.out ./...`<br>*(`make test-postgres-coverage`)* | PostgreSQL 16+, Go 1.26.5 | El `-coverpkg` calculado conserva cada paquete de cortex y excluye solo los `/node_modules/` de terceros; la cobertura atómica de todo el proyecto es al menos **80.0%**; falla el build si es `< 80.0%`. | [`.github/workflows/ci.yml:184`](../.github/workflows/ci.yml#L184), [`docs/COVERAGE.md:11`](COVERAGE.md#L11) | CI obligatorio / Condicionado por entorno | Unexecuted in Session (Authority: CI) |
| <a id="gate-web-client-core-v8-coverage"></a>**Cobertura V8 del núcleo cliente web**<br>`gate_id: web-client-core-v8-coverage` | Verifica la lógica cliente pure TypeScript en la sala de control web | `npm --prefix web run test:coverage` | Node >= 24, npm | Cobertura de `web/src/lib/**/*.ts`: sentencias $\ge 70\%$, líneas $\ge 70\%$, branches $\ge 60\%$, funciones $\ge 55\%$. | [`.github/workflows/ci.yml:107`](../.github/workflows/ci.yml#L107), [`docs/COVERAGE.md:12`](COVERAGE.md#L12) | CI obligatorio / Local default | Unexecuted in Session (Authority: CI) |
| <a id="gate-web-production-build"></a>**Build de producción web**<br>`gate_id: web-production-build` | Compila la aplicación frontend Next.js | `npm --prefix web run build` | Node >= 24, npm | Build de producción Next.js limpio sin errores de TypeScript ni de bundling. | [`.github/workflows/ci.yml:117`](../.github/workflows/ci.yml#L117) | CI obligatorio / Local default | Unexecuted in Session (Authority: CI) |
| <a id="gate-opencode-plugin-contract"></a>**Contrato del plugin OpenCode**<br>`gate_id: opencode-plugin-contract` | Testea los event hooks y la lógica del plugin TypeScript de OpenCode | `npm --prefix plugin/opencode ci && npm --prefix plugin/opencode test` | Node >= 24, npm, Vitest 4.1.10 | Todos los tests de contrato Vitest pasan; verifica lifecycle de sesiones, supresión de subagentes y captura de prompts. | [`.github/workflows/ci.yml:335`](../.github/workflows/ci.yml#L335), [`plugin/opencode`](../plugin/opencode) | CI obligatorio / Local default | Unexecuted in Session (Authority: CI) |
| <a id="gate-claude-code-plugin-harness"></a>**Harness del plugin Claude Code**<br>`gate_id: claude-code-plugin-harness` | Testea los lifecycle hooks de Claude Code usando stubs deterministas | `bash plugin/claude-code/scripts/hooks_test.sh` | bash, jq, python3, coreutils timeout | El harness de contrato verifica 5 lifecycle hooks; sale con 127 si falta alguna herramienta requerida del harness. | [`.github/workflows/ci.yml:359`](../.github/workflows/ci.yml#L359), [`docs/PLUGINS.md:114`](PLUGINS.md#L114) | CI obligatorio / Condicionado por entorno | Unexecuted in Session (Authority: CI) |
| <a id="gate-docker-compose-smoke-test"></a>**Smoke test Docker Compose**<br>`gate_id: docker-compose-smoke-test` | Valida la orquestación del stack contenedorizado y la salud de la UI embebida | `docker compose up --build -d`<br>`curl -fsS http://localhost:7438/health`<br>`curl -fsS http://localhost:7438/ \| grep -qi '<!DOCTYPE html>\|<html'` | Daemon de Docker, Docker Compose | El servidor, Postgres y el contenedor de la UI embebida se vuelven healthy en 3 minutos; `/health` devuelve 200 OK en `:7438` y `/` sirve el shell de la UI embebida. | [`.github/workflows/ci.yml:390`](../.github/workflows/ci.yml#L390) | CI obligatorio / Condicionado por entorno | Unexecuted in Session (Authority: CI) |
| <a id="gate-docker-compose-e2e-boundary"></a>**Frontera E2E Docker Compose**<br>`gate_id: docker-compose-e2e-boundary` | Testea el bootstrap single-tenant, sondas solo-bearer y búsqueda sobre el stack Docker | `go test -v -count=1 -tags docker_e2e ./e2e`<br>*(`make test-e2e-docker`)* | Stack Docker en marcha, Go 1.26.5 | `/api/me` es solo-bearer (401 sin autenticar, el id de workspace configurado cuando autenticado); shell de la UI embebida servido en `:7438`; búsqueda y persistencia tras reinicio validadas bajo el tenant constante. | [`.github/workflows/ci.yml:280`](../.github/workflows/ci.yml#L280), [`docs/COVERAGE.md:13`](COVERAGE.md#L13) | CI obligatorio / Condicionado por entorno | Unexecuted in Session (Authority: CI) |
| <a id="gate-playwright-embedded-web-e2e"></a>**E2E de web embebida con Playwright**<br>`gate_id: playwright-embedded-web-e2e` | Arranca el binario real `cortex serve` y dirige los flujos de la web embebida | `cd tools/e2e-web && npm ci && npx playwright install --with-deps chromium && npx playwright test` | Node >= 24, Chromium, Go 1.26.5, `make web-build` para assets frescos | Desbloqueo con clave de primer arranque, dashboard/memoria/búsqueda, selector de ajustes y flujos de graph-mount pasan contra la UI embebida servida desde el binario Go. | [`.github/workflows/ci.yml:397`](../.github/workflows/ci.yml#L397), [`tools/e2e-web/playwright.config.ts`](../tools/e2e-web/playwright.config.ts) | CI obligatorio / Condicionado por entorno | Unexecuted in Session (Authority: CI) |
| <a id="gate-qdrant-vector-integration"></a>**Integración de vectores Qdrant**<br>`gate_id: qdrant-vector-integration` | Valida las operaciones del adaptador externo Qdrant | `go test -v -count=1 -tags qdrant_integration ./internal/vector/qdrant` | Servicio Qdrant en marcha, Go 1.26.5 | Indexado y búsqueda de vectores Qdrant pasan dentro de la frontera de celda de tenant. | [`internal/vector/qdrant`](../internal/vector/qdrant), [`AGENTS.md`](../AGENTS.md) | Opt-in / Condicionado por entorno | Unexecuted in Session (Authority: CI) |
| <a id="gate-pgvector-integration"></a>**Integración pgvector**<br>`gate_id: pgvector-integration` | Valida el adaptador externo pgvector de PostgreSQL | `go test -v -count=1 -tags pgvector_integration ./internal/vector/pgvector` | PostgreSQL con extensión `vector`, Go 1.26.5 | Indexado pgvector y queries de distancia coseno pasan dentro de la frontera de celda de tenant. | [`internal/vector/pgvector`](../internal/vector/pgvector), [`AGENTS.md`](../AGENTS.md) | Opt-in / Condicionado por entorno | Unexecuted in Session (Authority: CI) |
| <a id="gate-build-identity-provenance"></a>**Gate de identidad de build y procedencia**<br>`gate_id: build-identity-provenance` | Valida la identidad binaria estadística y los bindings de publicación | `go test -v -count=1 ./bench/vectorhydration -run '^Test(IdentityValidation\|PublicationBinding)'` | Go 1.26.5 | Hace cumplir `binary-identity/v1`, `ApprovedBuildIdentity = "go-test-c-trimpath-v1"` y el sellado de digests binario/árbol SHA-256. | [`bench/vectorhydration/provenance_test.go`](../bench/vectorhydration/provenance_test.go) | CI obligatorio / Gate offline | Unexecuted in Session (Authority: CI) |
| <a id="gate-privacy-domain-component"></a>**Gate de dominio de privacidad y componentes**<br>`gate_id: privacy-domain-component` | Valida parsing de marcadores privados, redacción y no interferencia AST | `go test -v -count=1 ./internal/domain/privacy` | Go 1.26.5 | Rechaza marcadores mal formados y contenido residual vacío; sustituye `<private>...</private>` por `[REDACTED]`; el AST nunca se muta (`REQ-PRIV-007`). | [`internal/domain/privacy/privacy_test.go`](../internal/domain/privacy/privacy_test.go), [`internal/domain/privacy/privacy.go`](../internal/domain/privacy/privacy.go) | CI obligatorio / Local default | Unexecuted in Session (Authority: CI) |
| <a id="gate-log-ledger-retention"></a>**Gate de retención de logs y ledger**<br>`gate_id: log-ledger-retention` | Hace cumplir historial de ledger inmutable y soft deletes no destructivos | `go test -v -count=1 ./internal/domain/projectprotocol -run 'TestProjectProtocolStoreDefinesNoDestructivePort'` | Go 1.26.5 | El store define CERO métodos de hard-delete/purge; los ledgers de migración hacen cumplir retención indefinida (`REQ-RET-001`). | [`internal/domain/projectprotocol/ports_test.go`](../internal/domain/projectprotocol/ports_test.go), [`internal/domain/projectprotocol/doc.go`](../internal/domain/projectprotocol/doc.go) | CI obligatorio / Local default | Unexecuted in Session (Authority: CI) |
| <a id="gate-synthetic-fixture-safety"></a>**Gate de seguridad de fixtures sintéticas**<br>`gate_id: synthetic-fixture-safety` | Verifica identidad del corpus sintético, garantía de cero-PII y aislamiento | `go test -v -count=1 ./bench/common -run '^TestCorpus'` | Go 1.26.5 | El corpus especifica origen `cortex-authored-synthetic`; la revisión de privacidad confirma cero prompts reales/filas de vendors/PII. | [`bench/common/corpus_test.go`](../bench/common/corpus_test.go), [`bench/evidence/cortex-native/v1/corpus.json`](../bench/evidence/cortex-native/v1/corpus.json) | CI obligatorio / Gate offline | Unexecuted in Session (Authority: CI) |
| <a id="gate-retrieval-evidence-preregistration"></a>**Gate de evidencia de retrieval y preregistración**<br>`gate_id: retrieval-evidence-preregistration` | Valida invariantes de registro de gates de release y cero fugas de aislamiento | `go test -v -count=1 ./bench/common -run '^TestGateRegistry'` | Go 1.26.5 | Cero violaciones de aislamiento; gates preregistrados antes de los resultados candidatos; splits held-out nunca reutilizados. | [`bench/common/gates_test.go`](../bench/common/gates_test.go), [`docs/BENCHMARKS.md`](BENCHMARKS.md) | CI obligatorio / Gate offline | Unexecuted in Session (Authority: CI) |
| <a id="gate-test-run-record-evidence"></a>**Gate de registros de ejecución de tests y evidencia**<br>`gate_id: test-run-record-evidence` | Hace cumplir registros de ejecución de tests auditables, conformidad de esquema, procedencia fuente/binario e integridad del placeholder NOT_RUN | `git diff --check`<br>*(validación de esquema y registro)* | Agnóstico de toolchain / Test-Run Schema v1.0 | Esquema de registro de test-run válido (`schema_version: "test-run-record/v1"`); inputs de fuente explícitos, identidad binaria, metadatos de run, artefactos, retención, conteos de tests y detección de no-match; gates no ejecutados registrados estrictamente como `NOT_RUN` con unknowns null; cero PASS fake; cero fuga de secretos; check de diff limpio. | [`bench/vectorhydration/provenance.go`](../bench/vectorhydration/provenance.go), [`docs/verification-matrix.md`](verification-matrix.md#4-especificacion-del-registro-de-ejecucion-de-tests-del-producto) | Calidad obligatorio / Gate local | Unexecuted in Session (Authority: CI) |

---

## 2. Prerrequisitos ambientales y política de no-skip

### CI obligatorio vs disponibilidad en la estación de trabajo

| Prerrequisito | Requisito canónico en CI | Estado en la estación de trabajo | Política de acción si falta |
|---|---|---|---|
| **Toolchain Go** | `go1.26.5 linux/amd64` | `go1.26.5 windows/amd64` | Fallar el build si la toolchain no coincide. |
| **golangci-lint** | `v2.11.4` (fijado en CI) | Disponible (`v2.12.2`) | No afirmar equivalencia de gate cuando las versiones locales divergen; CI es autoritativo. |
| **Node.js y npm** | `node v24`, `npm` con ficheros lockeados | `v24.20.0`, `npm 11.19.0` | Ejecución local permitida para `web` y `plugin/opencode`. |
| **PostgreSQL 16** | Contenedor de servicio en CI con bootstrap RLS | No presente en local | Reportar `BLOCKED`; no saltarse en silencio los suites de integración PostgreSQL. |
| **DSNs de PostgreSQL** | 3 DSNs distintos: `CORTEX_TEST_POSTGRES_DSN`, `CORTEX_TEST_POSTGRES_MIGRATION_DSN`, `CORTEX_TEST_POSTGRES_AUTHZ_ADMIN_DSN` | No configurados | Los DSNs ausentes fallan explícitamente en lugar de saltarse (`AGENTS.md`). |
| **CGO / GCC** | `gcc` instalado en los runners de Linux | No encontrado | El detector de race no puede correr en local; el gate se reporta `BLOCKED` en local y se hace cumplir en CI. |
| **Bash, jq, Python3** | Preinstalados en los runners de Linux | Parcial / sin confirmar | El harness de hooks de Claude Code sale con 127 cuando falta alguna herramienta; se reporta `BLOCKED` en local. |
| **Daemon de Docker** | Disponible en los runners de CI | Disponible | Requerido para los smoke tests de `docker compose` y los suites de frontera E2E. |

### Invariantes de diagnóstico y no-skip

1. **Sin saltos silenciosos**: los tests que dependen de fixtures o servicios
   externos DEBEN fallar o reportar `BLOCKED` cuando los prerrequisitos no están
   disponibles. Está estrictamente prohibido pasar en silencio cuando falta un
   motor.
2. **Sanitización de credenciales**: los comandos de verificación NUNCA deben
   loguear credenciales, strings de conexión DSN con contraseñas, tokens de
   autorización ni claves de cifrado.
3. **Baselines deterministas**: los contratos de baseline de retrieval
   (`./bench/...`) son totalmente offline y deterministas; se ejecutan sin modelos
   externos, conexiones de red ni descargas de datasets.

---

## 3. Invariantes de identidad de build, retención, privacidad y evidencia

### 3.1 Identidad de build y reproducibilidad (`build-identity`)
- **Esquema y flags fijados**: fijados por `BinaryIdentity` (`binary-identity/v1`)
  en [`bench/vectorhydration/provenance.go`](../bench/vectorhydration/provenance.go).
  La configuración de build aprobada es
  `ApprovedBuildIdentity = "go-test-c-trimpath-v1"`.
- **Sellado de digests**: el SHA-256 del binario, el SHA-1 del commit de fuente,
  el SHA-256 del árbol, el SHA-256 del tool y el SHA-256 del argv deben ser
  strings hex no-cero en minúsculas válidos.
- **Binding de publicación**: vincula la identidad binaria con la identidad del
  protocolo; el unmarshaling hace cumplir `DisallowUnknownFields` y rechaza claves
  JSON duplicadas.

### 3.2 Política de retención de logs y ledger (`log-retention`)
- **Retención indefinida (`REQ-RET-001`)**: los registros con ledger (revisiones
  de skills y reglas, activaciones, eventos de auditoría) son inmutables y se
  conservan indefinidamente en SQLite (`003_project_artifacts.sql`) y PostgreSQL
  (`106_project_artifacts.sql`).
- **Borrado no destructivo**: la borrera es exclusivamente una transición de
  estado soft-delete. No se exponen sentencias SQL de hard-delete, cascade ni
  purge en las tablas de artefactos.
- **Logging zero-secret**: conforme a
  [`internal/config/config.go`](../internal/config/config.go) y
  [`docs/ARCHITECTURE.md`](ARCHITECTURE.md), las contraseñas de DSN, los tokens
  bearer, los secretos de API y el contenido crudo de etiquetas `<private>` se
  eliminan antes de la emisión a stdout o a logs.

### 3.3 Matriz de privacidad y cumplimiento por componente (`privacy`)
- **Sintaxis de marcadores**: etiquetas `<private>...</private>` exactas o
  insensibles a mayúsculas parseadas por
  [`internal/domain/privacy/privacy.go`](../internal/domain/privacy/privacy.go).
- **Requisito de residual público**: si la redacción deja el texto residual público
  vacío, las operaciones fallan en modo cerrado con `ErrCodeRequiredEmpty`
  (`privacy_required_empty`).
- **Errores sanitizados**: los structs de error nunca filtran el payload privado;
  solo transmiten el código de error, la posición y el conteo de marcadores.
- **No interferencia AST (`REQ-PRIV-007`)**: las estructuras AST, los resúmenes de
  doc, las relaciones y el reasoning son componentes estructurales del codebase y
  NUNCA deben mutarse ni redactarse con las rutinas de privacidad.

### 3.4 Fixtures seguros y sintéticas (`synthetic-fixture` / `safe-fixture`)
- **Integridad sintética**: regida por
  [`bench/evidence/cortex-native/v1/corpus.json`](../bench/evidence/cortex-native/v1/corpus.json)
  y validada por [`bench/common/corpus_test.go`](../bench/common/corpus_test.go).
- **Contrato de cero filtración**: `privacy_review` afirma
  `synthetic-only-no-real-prompts-vendor-rows-private-data-or-secrets`. Todas las
  fixtures contienen solo datos sintéticos; sin prompts reales, sin filas
  propietarias de vendors, sin credenciales.
- **Determinismo offline**: las fixtures validan colisiones de aislamiento
  cross-project, filtros temporales y decoys de ranking sin dependencias de red
  externa ni de LLM.

### 3.5 Límites de evidencia y bounds de gates (`evidence-limit`)
- **Blockers de release universales**: cualquier filtración de aislamiento
  tenant/workspace o cross-project (violaciones de aislamiento no-cero) bloquea el
  release inmediatamente.
- **Anclaje al ground truth**: se requieren los stable-IDs relevantes de episodios
  y facts para la evaluación de retrieval; las métricas a nivel de respuesta
  (F1, ROUGE-L) no pueden sustituir la relevancia por stable-ID.
- **Disciplina de preregistración**: los gates de release, tamaños de muestra,
  métricas y umbrales de varianza deben registrarse antes de las ejecuciones de
  evaluación candidatas; los splits de test held-out no pueden reutilizarse para
  calibración.

### 3.6 Registros de ejecución de tests del producto e invariantes de auditoría (`test-run-record`)
- **Esquema canónico (`schema_version: "test-run-record/v1"`)**: las ejecuciones
  de verificación de gates de producto generan registros de ejecución de tests
  estructurados y auditables. Cada registro vincula el enlace de fila de la
  matriz, el gate ID, los inputs de fuente, la procedencia binaria, los metadatos
  de run, las métricas de ejecución, los conteos de tests, el estado de no-match,
  los artefactos generados y la política de retención.
- **Estado veraz y disciplina NOT_RUN**: los gates no ejecutados deben reportarse
  como `status: "NOT_RUN"`. Cada unknown dependiente de runtime (código de salida,
  timestamps, duración, conteos de tests, flag de no-match, digest del binario,
  artefactos de log) debe ser explícitamente `null`. Sintetizar veredictos `PASS`
  falsos o fabricar historial de ejecución para suites no ejecutadas está
  estrictamente prohibido.
- **Inputs de fuente y sellado binario**: los runs ejecutados vinculan el SHA del
  commit de fuente exacto, el SHA-256 del árbol de fuente, la versión del tool
  (`go1.26.5`) y la identidad de build
  (`ApprovedBuildIdentity = "go-test-c-trimpath-v1"` según
  [`bench/vectorhydration/provenance.go`](../bench/vectorhydration/provenance.go)).
- **Conteo de tests y detección de no-match**: los registros de run rastrean
  explícitamente conteos discretos de tests (`total`, `passed`, `failed`,
  `skipped`, `blocked`). El booleano `no_match` indica explícitamente si los
  filtros de test o los patrones de paquete coincidieron con cero targets de test
  ejecutables, previniendo pases de suite false-green silenciosos.
- **Sellado de artefactos e retención indefinida**: los artefactos de salida
  (logs de test, perfiles de cobertura, informes de evidencia de benchmarks) se
  registran con sus rutas relativas y digests SHA-256, regidos por retención
  indefinida no destructiva (`REQ-RET-001`).
- **Eliminación de secretos**: los registros de test-run y los logs adjuntos
  nunca deben contener credenciales de DSN PostgreSQL, tokens bearer, claves API
  ni payloads crudos de marcadores `<private>...</private>`.
- **Vinculación con filas de la matriz**: cada registro de test-run debe vincular
  explícitamente con su fila de gate padre en
  [Sección 1](#1-gates-de-verificacion-y-matriz-de-ejecucion).

---

## 4. Especificación del registro de ejecución de tests del producto

### 4.1 Contrato canónico del registro de test-run (`test-run-record/v1`)

Cada ejecución de gate de verificación produce o referencia un registro de
ejecución de tests inmutable conforme a la especificación `test-run-record/v1`.
El esquema establece procedencia end-to-end entre la definición del gate en la
Sección 1 y la evidencia de verificación de runtime.

#### Campos de especificación de nivel superior

| Nombre de campo | Tipo | Valores permitidos / formato | Descripción e invariantes |
|---|---|---|---|
| `schema_version` | string | `"test-run-record/v1"` | Identificador del esquema canónico de registro de test-run. |
| `gate_id` | string | Slug de gate que coincide con la [Sección 1](#1-gates-de-verificacion-y-matriz-de-ejecucion) | Identificador único del gate de verificación (p. ej. `offline-retrieval-baseline`). |
| `matrix_row_link` | string | Enlace de ancla markdown relativo | Enlace explícito a la fila de gate correspondiente en la matriz de verificación (p. ej. `verification-matrix.md#gate-offline-retrieval-baseline`). |
| `run_id` | string \| null | UUID / string de run con timestamp o `null` | Identificador único de run asignado al ejecutar; estrictamente `null` para `NOT_RUN`. |
| `status` | string | `"PASS"`, `"FAIL"`, `"BLOCKED"`, `"NOT_RUN"` | Estado de ejecución de alto nivel del gate. |
| `command` | string | String de comando CLI no vacío | La invocación exacta de línea de comandos especificada por la matriz de verificación. |
| `execution` | object \| null | Objeto de metadatos de ejecución o `null` | Metadatos detallados de ejecución de runtime; estrictamente `null` cuando `status` es `"NOT_RUN"`. |
| `source_inputs` | object | Objeto de metadatos de inputs de fuente | Sellado del SHA del commit git, digest del árbol de fuente, digest del manifiesto y ficheros de entrada. |
| `binary_identity` | object \| null | Objeto de identidad binaria o `null` | Procedencia de binario y compilador según `bench/vectorhydration/provenance.go`; `null` cuando `status` es `"NOT_RUN"`. |
| `counts` | object \| null | Objeto de desglose de conteos de tests o `null` | Conteos discretos de resultados de test; estrictamente `null` cuando `status` es `"NOT_RUN"`. |
| `no_match` | boolean \| null | `true`, `false` o `null` | `true` si el filtro de test coincidió con cero targets de test; `false` si hubo coincidencias; `null` para `NOT_RUN`. |
| `artifacts` | array de objects \| null | Lista de objetos de artefacto o `null` | Rutas, tipos de artefacto y digests SHA-256 de logs/informes generados; `null` para `NOT_RUN`. |
| `retention` | object | Objeto de contrato de retención | Política que rige la durabilidad del registro, la inmutabilidad, la ubicación de almacenamiento y la eliminación de secretos. |
| `notes` | string | Texto descriptivo | Notas operativas, razón del bloqueo o justificación del estado no ejecutado. |

#### Definiciones detalladas de objetos

##### 1. Objeto `execution` (metadatos de ejecución de runtime)
- `timestamp_start` (string | null): timestamp UTC ISO-8601 cuando comenzó la
  ejecución del comando.
- `timestamp_end` (string | null): timestamp UTC ISO-8601 cuando terminó la
  ejecución del comando.
- `duration_ms` (integer | null): duración total de ejecución en milisegundos.
- `exit_code` (integer | null): código de salida del proceso devuelto por el
  comando (`0` para éxito).
- `environment` (object): metadatos del host de ejecución con `os`
  (p. ej. `"linux"`, `"windows"`), `arch` (p. ej. `"amd64"`, `"arm64"`) y
  `runner` (p. ej. `"github-actions-ubuntu-latest"`, `"local-workstation"`).

##### 2. Objeto `source_inputs` (sellado de inputs)
- `source_commit` (string): SHA de commit Git hexadecimal de 40 caracteres o
  `"HEAD"`.
- `source_tree_sha256` (string | null): digest SHA-256 hexadecimal en minúsculas
  de 64 caracteres del árbol de fuente.
- `manifest_sha256` (string | null): digest SHA-256 de 64 caracteres de la
  configuración de test de entrada o del manifiesto.
- `input_files` (array de strings): rutas relativas explícitas de los ficheros
  fuente y fixture principales con ámbito en el gate.

##### 3. Objeto `binary_identity` (procedencia binaria)
- `binary_sha256` (string | null): digest SHA-256 de 64 caracteres del binario de
  test o de producto compilado.
- `build_identity` (string): configuración de identidad de build fijada
  (`"go-test-c-trimpath-v1"` según
  [`bench/vectorhydration/provenance.go`](../bench/vectorhydration/provenance.go)).
- `tool_version` (string): string de versión de la toolchain (p. ej.
  `"go1.26.5"`).
- `tool_sha256` (string | null): digest SHA-256 de 64 caracteres del binario de
  la toolchain de compilación.
- `argv_sha256` (string | null): digest SHA-256 de 64 caracteres del array
  exacto de argumentos pasado al builder.

##### 4. Objeto `counts` (desglose de ejecución de tests)
- `total` (integer | null): número total de casos de test evaluados.
- `passed` (integer | null): número de casos de test que pasaron.
- `failed` (integer | null): número de casos de test que fallaron.
- `skipped` (integer | null): número de casos de test omitidos intencionadamente
  por el runner.
- `blocked` (integer | null): número de casos de test bloqueados por
  prerrequisitos ausentes.

##### 5. Campo booleano `no_match` (detección de cero targets)
- `true`: el filtro de comando, la regex o la ruta de paquete coincidió con cero
  funciones de test ejecutables (p. ej. `[no tests to run]` o `no test files`).
  Tales runs NO deben marcarse como `status: "PASS"` a menos que la definición del
  gate lo permita explícitamente con targets de test vacíos.
- `false`: el filtro coincidió con una o más funciones de test ejecutables.
- `null`: el gate no se ejecutó (`status: "NOT_RUN"`).

##### 6. Array `artifacts` de objects (salidas de evidencia)
- `path` (string): ruta relativa al workspace del artefacto generado (p. ej.
  `coverage/coverage.out`).
- `artifact_type` (string): tipo semántico (`"stdout_log"`, `"coverage_profile"`,
  `"evidence_report"`).
- `sha256` (string): digest SHA-256 hexadecimal en minúsculas de 64 caracteres
  que sella el contenido del artefacto.

##### 7. Objeto `retention` (política de durabilidad y almacenamiento)
- `policy` (string): política de ciclo de vida de retención
  (`"indefinite"` según `REQ-RET-001`).
- `immutable` (boolean): `true` indicando persistencia append-only sin mutación
  in-place.
- `storage` (string): ledger de persistencia autoritativo
  (`"cortex_meta / migration_ledger"`).
- `secret_scrubbed` (boolean): `true` confirmando que la salida de verificación ha
  pasado por una eliminación automatizada de credenciales.

---

### 4.2 Requisitos de registro NOT_RUN auditable

Cuando un gate de verificación no se ejecuta durante una sesión de mantenimiento,
de documentación o restringida, su registro de run debe adherirse a reglas
estrictas de auditabilidad:

1. **Estado NOT_RUN explícito**: el estado DEBE establecerse en `"NOT_RUN"`.
   Estados ambiguos como `"PENDING_PASS"`, `"ASSUMED_OK"` o strings vacíos están
   estrictamente prohibidos.
2. **Unknowns null obligatorios**: todos los campos dependientes de runtime que
   requieren ejecución real del proceso DEBEN ser explícitamente `null`. Esto
   incluye:
   - `run_id`: `null` (ningún identificador de run asignado).
   - `execution`: `null` (sin timestamps de inicio/fin, duración ni código de
     salida).
   - `binary_identity`: `null` (sin binario de test compilado ni sonda de
     toolchain de runtime).
   - `counts`: `null` (los conteos NUNCA deben poblarse con `0` o enteros
     placeholder, lo que implicaría falsamente que cero tests corrieron y
     pasaron).
   - `no_match`: `null` (el matching de filtros no puede evaluarse sin
     ejecución).
   - `artifacts`: `null` (no existen logs, perfiles ni informes de salida).
3. **Prohibición de afirmaciones PASS ficticias y reutilización de historial**:
   - Los suites no ejecutados nunca deben claimar `status: "PASS"`.
   - Los registros no deben reutilizar ni copiar timestamps, hashes o resultados
     de commits previos o jobs de CI pasados para simular la verificación actual.
   - La inspección de código, el type checking estático o las revisiones de docs
     no pueden sustituir la ejecución determinista de tests.
4. **Preservación de la procedencia estática**: incluso sin ejecutarse, el
   registro debe capturar el contexto estático:
   - `schema_version: "test-run-record/v1"`
   - `gate_id` y `matrix_row_link` vinculando directamente con la fila de la
     matriz de verificación.
   - `command` declarando la invocación exacta y reproducible.
   - `source_inputs` identificando el commit activo y los ficheros principales
     con ámbito.
   - `retention` confirmando la política de gobierno.
   - `notes` documentando por qué el gate no se ejecutó (p. ej. restricciones de
     sesión, prerrequisitos de entorno).

---

### 4.3 Sanitización de secretos y disciplina de privacidad

Los registros de ejecución de tests, los artefactos y los logs adjuntos están
sujetos a eliminación automatizada de credenciales y de privacidad:

1. **Sanitización de credenciales**: los strings de conexión DSN con contraseñas,
   los tokens bearer de autenticación, los secretos de API y las claves privadas
   deben eliminarse o sustituirse por `[REDACTED]`.
2. **Eliminación de etiquetas privadas**: cualquier payload sensible encerrado en
   etiquetas `<private>...</private>` parseadas según
   [`internal/domain/privacy/privacy.go`](../internal/domain/privacy/privacy.go)
   debe sustituirse por `[REDACTED]` antes de la serialización del artefacto.
3. **No interferencia AST (`REQ-PRIV-007`)**: las estructuras AST, los resúmenes
   de doc, las relaciones de código y el reasoning del grafo son componentes
   estructurales del codebase y NUNCA deben mutarse ni redactarse con las rutinas
   de privacidad.

---

### 4.4 Ejemplo de registro NOT_RUN solo-placeholder

El siguiente registro demuestra un registro `NOT_RUN` auditable solo-placeholder
para el gate [Baseline de retrieval offline](#gate-offline-retrieval-baseline).
Se adhiere a todos los requisitos de unknowns null, no contiene historial de
ejecución fabricado y vincula explícitamente con su fila de la matriz:

```json
{
  "schema_version": "test-run-record/v1",
  "gate_id": "offline-retrieval-baseline",
  "matrix_row_link": "verification-matrix.md#gate-offline-retrieval-baseline",
  "run_id": null,
  "status": "NOT_RUN",
  "command": "go test -v -count=1 ./bench ./bench/common ./bench/cortex ./bench/fixtures/cortex-native ./bench/cortex/cmd/baseline",
  "execution": null,
  "source_inputs": {
    "source_commit": "03d74b2dff145c373eb3d6f4c22f0045685c9091",
    "source_tree_sha256": null,
    "manifest_sha256": null,
    "input_files": [
      "bench/evidence/cortex-native/v1/corpus.json",
      "bench/common/report.go",
      "bench/cortex/cmd/baseline/main.go"
    ]
  },
  "binary_identity": null,
  "counts": null,
  "no_match": null,
  "artifacts": null,
  "retention": {
    "policy": "indefinite",
    "immutable": true,
    "storage": "cortex_meta / migration_ledger",
    "secret_scrubbed": true
  },
  "notes": "Gate not executed in current session; session constrained to documentation maintenance without test/build/install execution. Placeholder record with null unknowns and no historical run reuse."
}
```
