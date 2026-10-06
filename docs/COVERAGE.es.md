# Política de cobertura

Cortex valida el comportamiento en tres capas complementarias. Un porcentaje es
un guardia de regresión, no un sustituto de los contratos de autorización,
migración y composición que ejercitan los suites de integración.

## Gates obligatorios de CI

| Capa | Comando | Evidencia requerida |
| --- | --- | --- |
| Servicio Go y persistencia | `make test-postgres-coverage` | La cobertura atómica de todo el proyecto, incluyendo los tests de integración de PostgreSQL, es al menos 70%. |
| Núcleo del cliente web | `npm --prefix web run test:coverage` | Cobertura V8 de `web/src/lib/**/*.ts`: sentencias y líneas al menos 70%, branches al menos 60%, funciones al menos 55%. |
| Frontera Compose | `make test-e2e-docker` | El servidor, la aplicación web, las credenciales de bootstrap, la frontera tenant/workspace, la búsqueda y la persistencia tras reinicio funcionan juntos en un proyecto Compose aislado. |

Ambos workflows de GitHub suben el perfil/log de Go y los informes V8 de la web
como artefactos. Los informes locales están deliberadamente ignorados por Git:
Go escribe en `coverage/`; Vitest escribe en `web/coverage/`.

## Por qué el alcance web es explícito

El entorno de tests web es intencionalmente basado en Node y actualmente ejercita
el núcleo del cliente TypeScript puro sensible a la seguridad: la política de
transporte bearer, el handshake de autenticación, el reducer de streaming de
agentes, la codificación de la API, las preferencias y los exportadores de
configuración. El rendering React/TSX no se cuenta como si estuviera cubierto;
sigue protegido por la validación del build de producción y el test de frontera
Compose. Añade un entorno de tests DOM y tests de interacción de componentes
antes de ampliar el conjunto de inclusión V8 a ficheros `tsx`.

## Ejecución local

Ejecuta primero las comprobaciones baratas de web y Go:

```text
npm --prefix web run test:coverage
make test-coverage
```

El gate de PostgreSQL requiere los tres DSN `CORTEX_TEST_POSTGRES_*` y la
bootstrap de autorización descrita en `AGENTS.md`; CI es el ejecutor
autoritativo cuando estos no están disponibles. La comprobación de frontera
Compose requiere Docker:

```text
make test-postgres-coverage
make test-e2e-docker
```
