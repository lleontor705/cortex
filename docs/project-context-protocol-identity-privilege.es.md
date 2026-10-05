# Project Context Protocol — Identidad, privilegio y runbook de rollout (IDP-T05)

Alcance: las migraciones emparejadas de artefactos del Project Context que
llevan el contrato de mediación de identidad/privilegio — el follow-up SQLite
**003** (versión de registry **2003**,
`migrations/v2/003_project_artifacts.sql`) y PostgreSQL **106**
(`migrations/v2/106_project_artifacts.sql`). Este runbook define el preflight de
checksum, la aplicación, los checks post-aplicación, el orden de release y la
política de rollback no destructiva. Es procedimiento de operador, no código de
aplicación: nada aquí muta un ledger ni sobreescribe un checksum.

## 1. Modelo de identidad de checksum

- Los follow-ups de SQLite se registran en `cortex_v2_migrations`
  (`version`, `name`, `checksum`); la identidad de la línea base 001 queda
  congelada en `cortex_meta` y los follow-ups nunca la tocan.
- Las migraciones de servidor PostgreSQL 100-106 se registran en
  `cortex_server_migrations` (`version`, `name`, `checksum`).
- El ledger almacena el SHA-256 exacto del SQL embebido en el momento de
  aplicar (PostgreSQL hashhea el SQL normalizado a LF). Un checksum registrado en
  el ledger es evidencia inmutable: `Apply` trata un checksum registrado que no
  coincide como `ErrSchemaTampered` y se niega; nada reescribe nunca una fila del
  ledger.
- Pines de inmutabilidad: `v2_test.go` fija 2001/2002 (enviados; byte-idénticos
  para siempre) y 2003 (no enviado; el pin se mueve con los bytes revisados hasta
  que su release lo congela). `postgres_test.go` fija 100-105 (inmutables para
  siempre) y 106 (no enviado, se mueve hasta el release). Editar SQL enviado
  rompe cada base de datos aplicada — los pins hacen que ese drift falle
  ruidosamente en los tests unitarios en cada plataforma.

### 1.1 Ruta de migración (b): conjunto embebido congelado, 110 vivo, 111 muerto-pero-embebido

El conjunto de SQL embebido en `migrations/v2/` está congelado (REQ-SH-020): ningún
cambio edita, mueve o borra ningún fichero bajo él, ninguna entrada de ledger,
ningún pin de checksum ni la historia retirada `migrations/001-014` de la raíz. Las
versiones 100-105 son byte-idénticas para siempre; 106-112 llevan pins revisados
que solo se mueven con bytes revisados hasta el release. La migración **111**
(`ServerMultiTenantVerifierSQL`) sigue embebida pero es dead code: después de que
`MultiTenantTokenPrincipalVerifier` fue borrado no tiene caller Go, y sus únicas
referencias son la declaración embed en `migrations/v2/embed.go` y el plumbing que
registra la versión 111 en `internal/migration/postgres.go`. El verificador
single-tenant vivo es el `cortex_verify_token_principal_v2` de la versión **110**;
el head del runtime es la versión **112** (`static_bind_contract`), que es aditiva
y deja el 111 en su sitio. Una base de datos cuyo ledger registre una versión más
allá del head del runtime falla en modo cerrado con `ErrFutureMigration` — fue
escrita por un runtime más nuevo y debe seguir ejecutando ese runtime — así que
las bases de datos con ledger que activen el guard siguen funcionando como se
documenta en la sección 5. Dropear físicamente el 111 muerto requiere una
migración compensatoria en un head futuro; ese trabajo sigue siendo **solo
roadmap** y nunca se crea ni se aplica como parte de este train.

## 2. Preflight de ledger de solo lectura

API (en `internal/migration`):

- SQLite 003: `(*V2Baseline).PreflightFollowUp(ctx, db, 2003)`
- PostgreSQL 106: `(*PostgresServerMigration).Preflight(ctx, db)` sobre la
  migración de versión 106 de `NewPostgresServerMigrations()`

Ambas devuelven un snapshot `LedgerPreflight` y su `Verdict()`. El preflight es
estrictamente de solo lectura: sondea la presencia del ledger (`sqlite_master` /
`to_regclass`), lee como máximo dos filas, nunca crea el ledger, nunca escribe y
no toma advisory locks. Los tests unitarios demuestran cero mutación vía snapshots
de esquema; la cobertura PostgreSQL conductual corre en el suite
`postgres_integration`.

**Estado esperado antes del rollout: UNLEDGERED.** Tabla de veredictos:

| Estado del preflight | Veredicto | Acción |
| --- | --- | --- |
| Sin fila de ledger para 2003/106 (tabla de ledger ausente o fila ausente) | `nil` | Proceder a aplicar |
| La fila registra el checksum embebido ACTUAL | `ErrPreflightStop` (ya aplicado) | Parar. Ejecutar el check post-aplicación en lugar de aplicar |
| La fila registra OTRO checksum (previo/pre-release) | `ErrPreflightStop` + `ErrSchemaTampered` | Parar y escalar. Aplicar fallaría en modo cerrado; nunca reconciliar editando el ledger |
| El ledger registra alguna versión más allá del head del runtime (p. ej. 2004/107) | `ErrPreflightStop` + `ErrFutureMigration` | Parar. La base de datos la creó un runtime más nuevo; no ejecutar por debajo de su head |

Regla de escalado: **cualquier checksum registrado para la versión destino detiene
el rollout.** No hay override de checksum, no hay `--force` y no hay vía de mutación
del ledger; un checksum previo se reconcilia solo con una decisión humana
documentada como una nueva migración revisada.

### 2.1 SQL de preflight ejecutable copy/paste (operadores)

Cada query de abajo es de solo lectura. Ejecútalas tal cual están escritas; ninguna
escribe, crea ni bloquea nada. Cada bloque incluye la salida esperada y la condición
de parada para cada forma de resultado.

#### SQLite (destino 2003)

Abre la base de datos con el modo de solo lectura ordinario para que se OBSERVE un
write-ahead log caliente (`mode=ro` prohíbe escrituras pero aun así lee a través de
un `-wal` existente). No uses immutable=1 contra la base de datos viva: immutable
le dice a SQLite que el fichero no puede cambiar, así que una base de datos WAL viva
puede devolver un estado obsoleto, pre-WAL, y malinterpretar el ledger. immutable=1
solo es seguro contra una copia snapshot tomada tras un shutdown limpio del writer
(WAL completamente checkpointeado en la copia):

```sh
DB="$HOME/.cortex/v2/cortex.db"
sqlite3 "file:${DB}?mode=ro" "SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'cortex_v2_migrations';"
```

```sql
-- Q1: ¿existe en absoluto el ledger de follow-up?
SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'cortex_v2_migrations';
-- Esperado: sin fila  → ledger AUSENTE. TERMINAR aquí con el veredicto
--                 UNLEDGERED: 2003 está sin ledger; proceder a aplicar. NO
--                 ejecutar Q2/Q3 — fallan con
--                 "no such table: cortex_v2_migrations" en la tabla ausente.
--        O: cortex_v2_migrations  (el ledger existe — ejecutar Q2 y Q3 ahora)

-- Q2 (solo cuando Q1 devolvió cortex_v2_migrations): ¿qué está registrado para
-- el destino 2003?
SELECT checksum FROM cortex_v2_migrations WHERE version = 2003;
-- Esperado: <sin fila>  → UNLEDGERED. Este es el estado requerido: proceder a aplicar.
--        O: <64-hex igual al checksum embebido>  → PARAR, ya aplicado:
--                 ejecutar el check post-aplicación en lugar de aplicar.
--        O: <cualquier otro valor, p. ej. un checksum pre-release más antiguo>
--                 → PARAR Y ESCALAR (clase tamper). Aplicar fallaría en modo
--                 cerrado; nunca reconciliar editando el ledger.
--
-- El checksum embebido (actual) está fijado en
-- internal/migration/v2_test.go (v2HistoricalChecksums[2003]) y es el SHA-256
-- de migrations/v2/003_project_artifacts.sql con finales LF (checkouts CRLF:
-- `tr -d '\r' < file | sha256sum`). El pin se mueve con los bytes revisados
-- hasta que 003 se envíe; tras el release queda congelado.

-- Q3 (solo cuando Q1 devolvió cortex_v2_migrations): ¿algún runtime más nuevo
-- registró algo más allá del head de este train (2003)?
SELECT max(version) FROM cortex_v2_migrations WHERE version > 2003;
-- Esperado: NULL (o sin fila) → proceder.
--        O: cualquier valor (p. ej. 2004) → PARAR, runtime más nuevo: esta base
--                 de datos la creó un binario MÁS NUEVO; no ejecutar por debajo
--                 de su head.
```

#### PostgreSQL (destino 106)

Conéctate con `psql` y abre una transacción de solo lectura para que un pegado
descarriado no pueda escribir (`BEGIN TRANSACTION READ ONLY;` — alternativamente usa
un rol que tenga solo SELECT sobre el ledger):

```sql
BEGIN TRANSACTION READ ONLY;

-- Q1: ¿existe en absoluto el ledger de migraciones de servidor?
SELECT to_regclass('cortex_server_migrations');
-- Esperado: NULL → ledger AUSENTE. TERMINAR aquí con el veredicto
--                 UNLEDGERED: 106 está sin ledger; COMMIT y proceder a aplicar
--                 (apply ejecuta la línea completa 100-106). NO ejecutar Q2/Q3
--                 en esta transacción: consultar la relación ausente lanza
--                 ERROR: relation "cortex_server_migrations" does not exist
--                 y ABORTA la transacción de solo lectura.
--        O: cortex_server_migrations (el ledger existe — ejecutar Q2 y Q3 ahora)

-- Q2 (solo cuando Q1 devolvió cortex_server_migrations): ¿qué está registrado
-- para el destino 106?
SELECT checksum FROM cortex_server_migrations WHERE version = 106;
-- Esperado: <sin fila>  → UNLEDGERED. Este es el estado requerido: proceder a aplicar.
--        O: <64-hex igual al checksum embebido>  → PARAR, ya aplicado:
--                 ejecutar el check post-aplicación en lugar de aplicar.
--        O: <cualquier otro valor> → PARAR Y ESCALAR (clase tamper). Aplicar
--                 fallaría en modo cerrado; nunca reconciliar editando el ledger.
--
-- El checksum embebido (actual) está fijado en
-- internal/migration/postgres_test.go (postgresHistoricalChecksums[106]) y es
-- el SHA-256 de migrations/v2/106_project_artifacts.sql con finales LF. El pin
-- se mueve con los bytes revisados hasta que 106 se envíe.

-- Q3 (solo cuando Q1 devolvió cortex_server_migrations): ¿algún runtime más
-- nuevo registró algo más allá del head de este train (106)?
SELECT max(version) FROM cortex_server_migrations WHERE version > 106;
-- Esperado: NULL → proceder.
--        O: cualquier valor (p. ej. 107) → PARAR, runtime más nuevo: no
--                 ejecutar por debajo de su head.

COMMIT;
```

Estas queries de operador reflejan las sondas de ledger que ejecuta el preflight a
nivel de código (`V2Baseline.PreflightFollowUp` sondea `sqlite_master` y el ledger
sobre su conexión abierta; `PostgresServerMigration.Preflight` sondea
`to_regclass('cortex_server_migrations')` y el ledger), de modo que una ejecución
manual en verde y una ejecución de código en verde tienen el mismo significado. El
DSN SQLite del operador usa deliberadamente `mode=ro` plano (no la sonda de fichero
`immutable=1` de la sonda de código): los operadores hacen preflight de una base de
datos VIVA cuyo WAL debe observarse.

## 3. Aplicación y checks post-aplicación

Orden por base de datos:

1. **Preflight** (sección 2). Proceder solo con el veredicto unledgered.
2. **Aplicar**:
   - SQLite: el arranque aplica los follow-ups pendientes dentro de una
     transacción (`V2Baseline.Apply`); el DDL de 2002 y 2003 y las filas del
     ledger se commitean juntos o en absoluto.
   - PostgreSQL: `ApplyPostgresServerMigrations` aplica 100-106 en orden bajo el
     advisory lock; cada migración es una transacción con su DDL y su fila de
     ledger commiteados juntos.
3. **Check post-aplicación**:
   - SQLite 003: `(*V2Baseline).VerifyFollowUpApplied(ctx, db, 2003)` — el
     ledger debe registrar el checksum embebido exacto; después
     `VerifyIntegrity` debe pasar.
   - PostgreSQL 106: `(*PostgresServerMigration).VerifyApplied(ctx, db)` — el
     ledger debe registrar un checksum que coincida con el SQL embebido.
   - Una fila ausente significa que la aplicación no completó; un checksum con
     drift es `ErrSchemaTampered` (SQLite) o un error de mismatch (PostgreSQL).
4. **Re-preflight tras aplicar** (confirmación opcional): el preflight reporta
   ahora la parada de ya-aplicado — ese es el estado post-rollout correcto, no un
   fallo.

## 4. Orden de release

1. **Congelar los pins antes de etiquetar.** 003 y 106 no se han enviado: sus
   pins de checksum en `v2_test.go`/`postgres_test.go` se mueven intencionadamente
   con los bytes revisados. En el release, los bytes etiquetados se vuelven
   inmutables y cualquier edición posterior falla los tests de pin.
2. **Enviar el par juntos.** Los trains SQLite 003 y PostgreSQL 106 viajan en el
   mismo release que el código que los requiere; los runtimes nunca envían una
   migración sin la ruta de código que la usa.
3. **El orden de aplicación es el orden de versiones.** SQLite:
   2001 → 2002 → 2003. PostgreSQL: 100 → … → 106. El runner hace cumplir esto;
   nunca apliques 003/106 fuera de orden o parcialmente.
4. **Sin 107 / sin 2004 en este train.** IDP-T05 excluye explícitamente
   cualquier siguiente migración; `TestPostgresPreflight106ChecksumMatchesPin`
   y los tests de secuencia fijan los heads (106 / 2003).
5. **Gates antes del release** (CI es autoritativo):
   `go test -v -count=1 ./internal/migration`,
   `go test -v -count=1 ./...`, y el suite etiquetado
   `go test -v -count=1 -tags "integration postgres_integration" ./...`
   con los tres DSN `CORTEX_TEST_POSTGRES_*`.

## 5. Rollback no destructivo

- `Down` es incondicionalmente forward-only en ambas líneas
  (`ErrForwardOnly`): sin DDL, sin DML, sin borrar ledger, sin excepción de
  limpieza de artefactos. El rollback nunca debe destruir historial de
  artefactos, recibos ni evidencia del ledger.
- **Antes de aplicar** (parada del preflight o duda pre-deploy): simplemente no
  apliques; la base de datos queda intacta por el preflight y por un apply
  rechazado (todo-o-nada transaccional, demostrado por tests de cero mutación).
- **Después de aplicar**: el esquema es aditivo, pero un runtime MÁS VIEJO
  rechaza una base de datos con ledger más allá de su head
  (`ErrFutureMigration`, fail closed). Por tanto, el rollback después de que
  003/106 estén en el ledger significa *mantener el binario actual* (o uno más
  nuevo) — nunca redesplegar uno más viejo sobre una base de datos mejorada.
  Los cambios correctivos de esquema pasan por una NUEVA migración revisada (el
  train 107+), nunca por ediciones de ledger, overrides de checksum ni SQL
  destructivo.

## 6. Acciones de operador prohibidas

- No edites ninguna SQL de migración enviada (001, 002, 100-105) — los pins de
  checksum y las bases de datos aplicadas convierten esto en un cambio breaking.
- No hagas `INSERT`/`UPDATE`/`DELETE` contra `cortex_v2_migrations` ni
  `cortex_server_migrations`. El ledger es evidencia append-only escrita solo por
  el migrador dentro de su transacción.
- No sobreescribas ni eludes checksums. No existe ningún flag; añadir uno es una
  violación de política, no una feature.
- No ejecutes a mano DDL de 003/106 fuera del runner (los artefactos obsoletos y
  sin ledger fallen en modo cerrado por diseño), y no registres ni apliques una
  migración 107/2004 como parte de este train.

## 7. Matriz de verificación

| Gate | Comando | Notas |
| --- | --- | --- |
| Paquete de migraciones | `go test -v -count=1 ./internal/migration` | Tests de preflight/post-aplicación/cero mutación/rechazo/pin |
| Suite default | `go test -v -count=1 ./...` | Gate unitario de CI |
| Suite etiquetada | `go test -v -count=1 -tags "integration postgres_integration" ./...` | Necesita los tres DSN PG; CI autoritativo |
| Lint con pins | golangci-lint v2.11.4 según CI | `--build-tags postgres_integration` para los ficheros etiquetados |
