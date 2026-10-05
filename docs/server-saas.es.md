# Despliegue de servidor self-hosted

`cortex --mode server` es una composición PostgreSQL single-tenant. Un despliegue
posee exactamente un tenant y un workspace, ambos fijados por configuración de
administrador de confianza; no hay selección de tenant o workspace en tiempo de
petición, no hay API de creación de tenant o workspace y no hay plano de control
alojado (hosted). Cada petición autenticada actúa como un único principal de
servicio sintético ensamblado a partir de esas constantes configuradas.

Para la guía general del servidor — imágenes de contenedor, etapas de rollout,
configuración del agente y superficies de cliente — consulta
[SERVER.md](SERVER.md).

## Constantes de configuración

El arranque valida la configuración y falla en modo cerrado (fail closed),
nombrando la clave ausente o mal formada, antes de que exista cualquier pool de
base de datos o handler HTTP. Las tres constantes de identidad son requeridas y
deben ser UUIDs válidos.

| Clave de configuración | Variable de entorno | Propósito |
| --- | --- | --- |
| `server.tenant_id` | `CORTEX_SERVER_TENANT_ID` | UUID del tenant que posee este despliegue; se instala como tenant de row-level-security para cada transacción. |
| `server.workspace_id` | `CORTEX_SERVER_WORKSPACE_ID` | UUID del workspace que posee este despliegue; el ámbito de workspace por defecto y único. |
| `server.principal_subject` | `CORTEX_SERVER_PRINCIPAL_SUBJECT` | UUID subject del principal de servicio sintético como el que actúa cada petición autenticada. |
| `server.grant_digest` | `CORTEX_SERVER_GRANT_DIGEST` | Opcional y obsoleto. La integridad de grants se deriva dentro de PostgreSQL a partir de la fila de actor bootstrapada; un valor configurado solo se acepta por compatibilidad hacia atrás. |
| `http.token` | `CORTEX_HTTP_TOKEN` | El único bearer estático. Requerido, gestionado como secreto, canónico (sin espacios iniciales ni finales y sin caracteres de control), y de al menos 12 caracteres. |

El arranque requiere además `server.storage.driver: postgres`, un DSN de runtime
y un DSN de migración usando un rol distinto (ver
[bootstrap de PostgreSQL](#bootstrap-de-postgresql-tres-dsns)).

## Autenticación por bearer estático

El servidor compone un único verificador de bearer estático:

- Compara el secreto `Authorization: Bearer` presentado contra `http.token` con
  una comparación en tiempo constante. Un mismatch, un secreto vacío o un secreto
  con relleno de espacios devuelven el mismo `401` opaco, de modo que ninguna vía
  de rechazo refleja material de credencial.
- Un secreto coincidente produce un único principal sintético construido una vez
  a partir de `tenant_id`, `workspace_id` y `principal_subject`: el subject es
  `principal_subject`, el tenant es `tenant_id`, y la lista de grants del
  workspace contiene solo `workspace_id`.
- El verificador no posee ninguna capacidad de base de datos. No hay derivación
  de tenant o workspace por petición a partir de campos de la petición;
  `tenant_id` y `workspace_id` provienen solo de la configuración, nunca de la
  entrada del cliente.

Rotar el bearer configurado es el control del operador. El verificador ensambla
su principal una vez al arranque, así que una rotación surte efecto tras un
reinicio.

La sala de control web embebida se autentica contra el servidor con el mismo
bearer estático. La clave de acceso web del primer arranque gestionada por
`cortex web key show|regenerate` es una credencial de modo local y nunca la
acepta el `/api/*` del servidor; los dos no deben confundirse.

## Bootstrap de PostgreSQL (tres DSNs)

Un despliegue self-hosted usa tres vías de conexión distintas durante el setup. Los
DSN de runtime y migración son claves de configuración; la vía administrativa se
usa una sola vez para crear los roles no-superusuario antes de que se ejecute
cualquier migración.

1. **DSN administrativo** — un login privilegiado que ejecuta el bootstrap de
   roles compartido:

   ```bash
   psql "$CORTEX_BOOTSTRAP_DSN" -f scripts/postgres/bootstrap-authz.sql
   ```

   `scripts/postgres/bootstrap-authz.sql` crea idempotentemente el rol owner
   `cortex_admin` sin login, el rol de runtime con LOGIN `cortex_test`
   (`NOSUPERUSER NOBYPASSRLS`) y el rol de migración con LOGIN
   `cortex_admin_login` (`NOSUPERUSER NOBYPASSRLS`) que hereda de `cortex_admin`.
2. **DSN de migración** — `server.storage.migration_dsn` /
   `CORTEX_SERVER_STORAGE_MIGRATION_DSN`, usando el login de migración
   privilegiado. El handle de migración aplica el esquema de servidor embebido en
   orden estricto de versiones y reconcilia el principal de servicio bootstrapado,
   y después cierra antes de servir tráfico.
3. **DSN de runtime** — `server.storage.dsn` / `CORTEX_SERVER_STORAGE_DSN`,
   usando el login de aplicación no-superusuario de larga duración sin
   `BYPASSRLS`.

Los roles de runtime y migración deben ser distintos; el arranque rechaza un
despliegue donde ambos DSNs nombren el mismo rol. El contenido de los DSN y las
contraseñas nunca se incluyen en errores de validación de frontera. Solo el
desarrollo local puede fijar `server.bootstrap_development: true` para reutilizar
el DSN de runtime en las migraciones y omitir el DSN de migración; nunca actives
ese switch en un despliegue compartido.

Al arrancar, el handle de migración reconcilia el principal de servicio
configurado, sus grants canónicos y el token de bootstrap reservado a través de
`cortex_bootstrap_service_principal`, con clave en `tenant_id`, `workspace_id`,
`principal_subject` y el bearer configurado. El bearer llega a PostgreSQL solo
como parámetro atado (bound) y nunca aparece en el texto de errores.

## Postura de row-level-security

El aislamiento de tenant lo hace cumplir PostgreSQL, no la capa HTTP:

- Cada tabla core del servidor fuerza row-level security y tiene su clave en
  `cortex_current_tenant()`, que lee el tenant atado de la transacción desde
  `cortex_tenant_context`.
- `cortex_bind_principal` instala ese contexto para el backend y la transacción
  actuales. Tras la migración 112 acepta dos formas de procedencia: la prueba
  atada a token `v1:` acuñada por la verificación de token, y la prueba
  `static:<hex>` que la composición single-tenant deriva del grant digest
  persistido del actor bootstrapado. El tenant, el actor y la versión del grant
  se releen desde la propia fila del actor bajo el advisory gate compartido, de
  modo que un caller no puede atar un tenant arbitrario, un actor distinto o una
  versión obsoleta.
- El rol de aplicación posee solo la matriz EXECUTE concedida a `cortex_app` y
  ningún privilegio directo sobre las tablas de identidad; solo el rol de
  migración reconcilia el principal de bootstrap.

Puesto que el tenant y el workspace son constantes de despliegue, las sondas de
aislamiento de tenant y workspace a nivel de SQL y la frontera EXECUTE solo-`cortex_app`
siguen siendo la postura verificada. Redesplegar el binario de runtime anterior es
la historia de rollback: la línea de migraciones es de avance únicamente
(forward-only) y ninguna fila de esquema o ledger se edita a mano.

## Ver también

- [SERVER.md](SERVER.md) — despliegue del servidor, imágenes de contenedor,
  etapas de rollout y superficies de cliente.
- [CONFIGURATION.md](CONFIGURATION.md) — la referencia completa de configuración.
- [project-context-protocol-identity-privilege.md](project-context-protocol-identity-privilege.md)
  — runbook de despliegue del ledger de migraciones.
