# Plan normativo standalone R1: Vector Hydration y C32

**Estado:** plan local, no certificación ni autorización de publicación. **Corte:** 2026-08-21. Los términos MUST/SHALL/SHOULD/MAY se interpretan según RFC 2119.

## 1. Objetivo y autoridad

Este documento define una remediación reproducible desde L0 hasta L7 para R1 (rendimiento/retrieval), con dos contratos normativos independientes: **Vector Hydration** y **C32**. Una ejecución parcial, FAIL, BLOCKED o INCONCLUSIVE MUST NOT presentarse como PASS.

El código y las pruebas del repositorio son autoridad técnica; `AGENTS.md` es autoridad para comandos y estados; la evidencia externa sellada es autoridad sólo para los hechos que identifica; Cortex conserva el expediente histórico. Este plan es la especificación de trabajo y no sustituye evidencia ejecutable.

## 2. Alcance, límites y estado

### 2.1 Alcance

Incluye `bench/vectorhydration/**`, su identidad de fuente/herramienta/binario/protocolo, una campaña Vector Hydration, la calificación C32, revisión independiente y el DAG local L0-L7.

### 2.2 No-alcance

No autoriza producto fuera del ámbito, campañas no autorizadas, reruns selectivos, borrar outliers, modificar historia, cambiar B1.1, inventar identidades ni publicar resultados. ForgeSpec **no es una dependencia operativa** de L0-L7. El workspace/package observado está dirty y contiene cambios no rastreados; no hay campañas Vector Hydration en esta remediación.

### 2.3 Restricciones destructivas

MUST detenerse ante necesidad de otro archivo, dato no verificable, autoridad expirada, hash inconsistente o dependencia ausente. Está prohibido sin autorización explícita: SQL destructivo (`DROP`, `DELETE FROM`, `TRUNCATE`), borrar recursos/directorios o archivos, desinstalar paquetes, `git clean`, `git reset --hard`, `git push`, deploy/publicación, reescribir ledgers/baselines, eliminar outliers o ejecutar una campaña fuera de su autorización. No se almacenan credenciales, tokens o DSN.

## 3. Precedencia de fuentes

En caso de conflicto se aplica, en este orden:

1. Código y pruebas actuales; 2. `AGENTS.md`; 3. evidencia externa íntegra; 4. Cortex; 5. este plan. Las contradicciones se conservan como pendientes o bloqueos, nunca se resuelven inventando datos.

Una afirmación no respaldada por una fuente de mayor precedencia MUST quedar
como pendiente, hipótesis o bloqueo. Credenciales, tokens y DSN privados MUST
NOT aparecer en documentos ni reportes.

## 4. Evidencia de contexto

Las siguientes referencias son históricas o de baseline y no sustituyen verificación ejecutable:

| Referencia | Uso | Resultado conservado |
|---|---|---|
| Cortex 1376 | baseline externo L0 (hecho revalidado) | snapshot read-only; HEAD `8e930c2b2d541443612432244d5a891f51683ac0`; manifest SHA-256 `08938140c18779c5afb6df656f7c5e1d076c04d25b3842f28116aef52c46d07b` |
| Cortex 1384 | decisión local (histórico) | L0 restaurado; L1 incompleto; L2-L7 sin iniciar |
| Cortex 1195 / 1224 / 1225 / 1226 | historial C32 (histórico) | preflights BLOCKED; amendment RTT12 prospectivo; sin PASS calificante |

El baseline externo de comparación, sólo lectura, está en `C:\Users\USRLUI~1\AppData\Local\Temp\opencode\vectorhydration-l0-baseline-20260821-v2`. Su manifest tiene el SHA indicado arriba. Estos son hechos externos sellados, no evidencia local reejecutada; el HEAD y estado dirty son observaciones separadas.

## 5. Historia y gates L0-L7

El contrato B1.1 permanece protegido. L0 retiró claims de identidad incompletos y está COMPLETE/GREEN. L1 refactorizó parcialmente identidad de fuente, pero la revisión encontró commit forgeable, selección no atómica de herramienta/build, extracción insegura y tests no mutation-sensitive; por ello está PARTIAL/FAIL. L2-L7 están BLOCKED. No hay campañas Vector Hydration, ni PASS calificante C32.

## 6. Contrato normativo: Vector Hydration

### 6.1 Campaña y estadística

La campaña MUST contener exactamente **3 celdas `GOMAXPROCS` 1/2/4**. Cada celda MUST contener **20 bloques pareados**, 10 AB y 10 BA, y cada bloque MUST ejecutar exactamente una vez el camino legacy y una vez el camino batch. Esto produce **120 resultados de proceso**: 3 × 20 × 2. No se permiten reruns, reemplazo de celdas ni eliminación de outliers. AB y BA MUST conservarse.

Cada comparación MUST usar **100 000 resamples BCa**, intervalo unilateral del **95 %**, y estadísticas finitas/JSON estrictas. El target normativo es **5.00**; la activación sólo puede ocurrir si el guard es **5.10**. El gate antiguo MUST permanecer vigente hasta que exista activación válida; no se sustituye retroactivamente. La fuente, herramienta, binario, protocolo, flags, dataset, semilla, OS/arquitectura, CPU, reloj y orden MUST quedar sellados antes de recolectar.

### 6.2 Escenarios Vector Hydration

* **Happy (REQ-VH-001):** Given tres celdas 1/2/4, 20 bloques por celda, 10 AB+10 BA y legacy+batch una vez por bloque; When se cierra la campaña; Then hay exactamente 120 resultados, 100000 BCa por análisis, 95 % unilateral y sólo puede avanzar si el target/guard se satisfacen.
* **Edge (REQ-VH-002):** Given outlier, orden AB/BA invertido o resultado igual a 5.00/5.10; When se analiza; Then se conserva el dato, se aplican exactamente target 5.00 y activation guard 5.10, y el gate viejo sigue hasta activación; no hay rerun ni borrado.
* **Error (REQ-VH-003):** Given identidad ausente, celda/bloque repetido o faltante, NaN/Infinity o BCa incompleto; When inicia collect/analyze; Then falla cerrado y queda FAIL/BLOCKED sin resultado calificable.

### 6.3 Identidad y confianza Vector Hydration

Cada resultado MUST estar ligado criptográficamente al binario ejecutado y al envelope portable. La autoridad del registry MUST venir de una fuente confiable, no del caller. Archive/extracción MUST rechazar traversal, aliases, junctions y colisiones OS-neutralmente. BCa degenerado MUST serializarse como null, nunca NaN/Infinity. La publicación MUST ser versionada, encadenada y verificable.

## 7. Contrato normativo: C32

### 7.1 Diseño de campaña

Una campaña C32 MUST fijar código, fuente, binario, herramienta, protocolo, OS/arquitectura, CPU, flags, dataset, semilla, orden, reloj y límites. MUST probar **direct y pooler**, **5 repeticiones**, **2 bloques same y 2 distinct**, en orden alternado. Cada combinación MUST usar **32 workers × 12 iteraciones** y conservar exactamente **15 360 muestras**; el reporte MUST documentar el conteo y no aceptar muestras faltantes. El p95 MUST ser nearest-rank.

Los gates son: ratio de throughput **>= 0.80**; same p95 absoluto **<= 25 ms**; tail relativo **<= 1.25 × distinct + 5 ms**; **cero auth failures y cero lock waits**. Un preflight dedicado MUST medir cada RTT y exigir **cada RTT <= 12 ms**, con límite inclusivo. MUST preservarse direct/pooler, same/distinct y ambos órdenes.

La campaña MUST ser una sola ejecución pre-registrada: no se permiten
reruns selectivos, reemplazo de celdas, eliminación de outliers, cherry-pick
de muestras ni cambio post hoc de semilla o límites. Una celda inválida
produce FAIL o BLOCKED, según la causa; no se repara silenciosamente.

### 7.2 RTT12 e historial

RTT12 es prospectivo e inclusivo (`x <= 12 ms`); no reinterpreta ni mejora historia. La corrida histórica con **11.326737 ms** contra el contrato de 10 ms queda **BLOCKED**. El amendment inclusivo a 12 ms es no retroactivo. La única corrida autorizada corregida midió **18.803803 ms**, quedó **BLOCKED**, produjo cero muestras de protocolo y cero observer report; no hubo rerun automático.

### 7.3 Criterio y escenarios C32

No existe qualifying PASS: todas las repeticiones, bloques, muestras, hashes, preflight, métricas y revisión independiente MUST pasar. El global es el mínimo de gates; FAIL/BLOCKED no se omite.

* **Happy (REQ-C32-001):** Given direct+pooler, 5 reps, 2 same+2 distinct alternados y 32×12 por combinación; When se cierra; Then hay 15360 muestras retained, nearest-rank p95 y pasan ratio, absolutos, tail, auth y locks.
* **Edge (REQ-C32-002):** Given RTT exactamente 12 ms o métricas en el límite; When se evalúa una campaña nueva; Then el límite es inclusivo, se aplican <=25 ms, >=0.80 y tail <=1.25×distinct+5 ms; historia no cambia.
* **Error (REQ-C32-003):** Given RTT >12 ms, bloque faltante, auth failure, lock wait, observer no iniciado o cero muestras; When se ejecuta preflight/cierre; Then queda BLOCKED/FAIL, sin PASS ni rerun automático.

## 8. Requisitos trazables y reglas comunes

| ID | Requisito | Given / When / Then |
|---|---|---|
| REQ-R1-001 | Fuente, herramienta, binario y protocolo MUST sellarse antes de collect. | Identidad ausente/mutable => cerrado, sin datos calificables. |
| REQ-R1-002 | Resultado MUST ligarse al binario exacto y envelope portable. | Hash distinto => binding FAIL. |
| REQ-R1-003 | Registry MUST derivar autoridad de fuente confiable. | Caller alterado no concede confianza. |
| REQ-R1-004 | Traversal, aliases, junctions y colisiones MUST rechazarse. | `safe/../x` y `safe\\..\\x` => rechazo. |
| REQ-R1-005 | JSON MUST ser estricto y BCa degenerado null. | Nunca NaN/Infinity; gate FAIL/BLOCKED. |
| REQ-R1-006 | Publicación MUST ser versionada, portable y encadenada. | Alteración => envelope inválido. |
| REQ-GATE-001 | Revisor independiente MUST reproducir cada decisión. | Cada gate tiene input, hash, versión y veredicto. |
| REQ-GATE-002 | Estados MUST distinguir PASS/FAIL/BLOCKED/INCONCLUSIVE. | Comando no ejecutable => no PASS. |

## 9. DAG local L0-L7

| Nivel | Estado actual | Tareas y salida obligatoria |
|---|---|---|
| L0 | **COMPLETE / GREEN** | Cuarentenar claims incompletos; preservar B1.1; runner/package verdes; registrar baseline. |
| L1 | **PARTIAL / REVIEW FAIL** | Identidad inmutable de fuente: commit ligado a bytes, archive sellado, extracción segura, argv canónico, tool/version/build y tests mutation-sensitive. |
| L2 | **BLOCKED** | Derivar y verificar identidad de protocolo; conectar source/tool/build sólo después de L1 PASS; no fabricar identidades. |
| L3 | **BLOCKED** | Binding end-to-end collector→raw→analyze→publication al binario exacto; cleanup fail-closed y no TOCTOU/junction. |
| L4 | **BLOCKED** | Qualification local del runner y envelope de evidencia; verificar mutaciones negativas y JSON estricto. |
| L5 | **BLOCKED** | Reintroducir autoridad externa de publicación/registry mediante fuente confiable y verificable; caller sólo aporta parámetros no autoritativos. |
| L6 | **BLOCKED** | Envelope portable versionado/hash-chain y BCa nullable/JSON-safe; definir gates de degeneración. |
| L7 | **BLOCKED** | Integración, suite completa, revisión final dual e informe de readiness; no ejecutar campaña si algún gate falla. |

### 9.1 Dependencias y reglas de avance

L1 depende de L0. L2 y L3 dependen de L1 PASS; L4 depende de L2-L3 PASS;
L5 y L6 pueden desarrollarse en paralelo sólo si no cambian B1.1 y terminan
antes de L7; L7 depende de todos. Una revisión FAIL reabre el nivel afectado
y bloquea descendientes. No se permite saltar niveles por un test verde no
relacionado.

## 10. Flujo de confianza y revisión

1. **Source pin:** fijar commit y bytes del archive, con digest independiente
   y prueba de que ambos corresponden.
2. **Qualification:** construir la herramienta desde la fuente fijada,
   registrar versión/flags/target y revalidar hash del ejecutable seleccionado.
3. **Activation:** activar sólo una configuración que pase identidad,
   protocolo, filesystem, límites y entorno; registrar quién/qué la activó.
4. **Campaign:** ejecutar la matriz C32 sin reruns, sustituciones ni outlier
   deletion; guardar AB/BA y cada observación necesaria.
5. **Held-out:** cualquier conjunto held-out MUST fijarse antes de mirar el
   resultado y nunca puede convertirse en entrenamiento, selección o rescate.
6. **C32 gate:** verificar RTT12 inclusivo prospectivo, BCa finito, cobertura,
   orden, conteos y todos los hashes.
7. **Final review:** un revisor independiente MUST intentar mutaciones,
   sustituciones de binary/source/tool, traversal, authority, JSON y BCa. Sólo
   un informe PASS explícito habilita una futura decisión; este expediente no
   contiene tal PASS.

## 11. Propiedad de archivos y cambios permitidos

| Área | Propietario del cambio local | Restricción |
|---|---|---|
| `bench/vectorhydration/collector*.go` | L0/L3 | sólo binding y claims definidos por el nivel |
| `source*.go` | L1/L2 | identidad sellada; no tocar B1.1 sin revisión |
| `executor*.go`, `bootstrap*.go` | L2-L4 | build/activation reproducibles |
| `gates*.go`, `analyze*.go` | L4/L6 | gates y estadística JSON-safe |
| `publication*.go`, runner | L0/L3-L6 | publicación ligada y autoridad externa verificada |
| `provenance*.go` | protegido | no modificar durante L0; cualquier cambio requiere nueva revisión |
| `spec.md` | mantenimiento de este plan | no debe alterar código |

Cada slice SHOULD permanecer por debajo de 350 líneas cambiadas. El package
vectorhydration actualmente se trata como un área no rastreada/sucia; antes
de cualquier integración se MUST limpiar la atribución, inspeccionar diff y
resolver el gate administrativo histórico.

## 12. Verificación y comandos

Los comandos exactos de `AGENTS.md` son:

```text
make build
go mod download
make fmt
golangci-lint run ./...
go test -v -count=1 ./...
go test -v -count=1 -tags cortex_vectors ./internal/vector/sqlite_blob
go test -v -count=1 ./bench ./bench/common ./bench/cortex ./bench/fixtures/cortex-native ./bench/cortex/cmd/baseline
go test -v -count=1 ./internal/migration
go test -race -count=1 ./internal/store/search ./internal/store/bundle ./internal/mcp
go test -v -count=1 -tags "integration postgres_integration" ./...
go test -tags postgres_integration -covermode=atomic -coverpkg=./... -coverprofile=coverage.out ./...
go test -v -count=1 -tags qdrant_integration ./internal/vector/qdrant
go test -v -count=1 -tags pgvector_integration ./internal/vector/pgvector
go test -v -count=1 ./internal/projection/obsidian -run 'Test(SafeSlug|WindowsDeviceNameNearMisses|CanonicalPathKey|ExportCanonicalCollision|ExportRejectsCaseInsensitiveCollision)'
bash plugin/claude-code/scripts/hooks_test.sh
```

`golangci-lint` MUST be v2.11.4. `make build`, `go mod download` y `make fmt` son canónicos; lint/test deben ejecutarse en ese orden. La corrida de race queda BLOCKED si falta CGO/gcc. PostgreSQL/coverage requieren PostgreSQL 16 y los tres DSN configurados; sin ellos son BLOCKED, no skip. Los adapters son opcionales y se reportan por separado. El gate Obsidian requiere Windows/macOS; el hook requiere bash, jq, python3 y timeout, y si falta una herramienta queda BLOCKED (no skip). La campaña C32 es DB-free sólo para el contrato/documentación; la campaña live es un efecto externo explícitamente autorizado y, si preflight falla, BLOCKED sin rerun.

## 13. Evidencia y stop conditions

Toda evidencia MUST registrar comando, código de salida, duración si está
disponible, versión relevante, digest de diff/artefacto y resultado resumido;
MUST preservar FAIL/BLOCKED y MUST NOT almacenar tokens, secretos o DSN.

Toda evidencia MUST registrar comando, exit code, duración, versión, digest de diff/artefacto y resumen; no tokens/secretos/DSN. Detenerse si una celda se repite/falta, RTT12 es retroactivo, JSON no es portable/finito, o aparece una mutación sin cobertura.

## 14. Estado actual verificable

- L0: completo y verde.
- L1: parcial; revisión independiente FAIL.
- L2-L7: bloqueados por la dependencia de L1 y por gates de confianza aún no
  satisfechos.
- Campañas vectoriales: ninguna ejecutada en esta remediación.
- Paquete/workspace: no rastreado y sucio; el gate histórico sigue activo.
- C32: no hay PASS calificante.
- ForgeSpec: no es dependencia operativa del plan local.

## 15. Definition of Done

La remediación sólo termina cuando L0-L7 tienen evidencia aprobada, source pin/qualification reproducibles, activation fail-closed, binding al binario exacto, autoridad externa independiente del caller, envelope portable, BCa JSON-safe y revisión independiente PASS.

**Vector Hydration MUST:** 3 celdas GOMAXPROCS 1/2/4; 20 bloques por celda; 10 AB+10 BA; legacy y batch una vez por bloque; 120 resultados; 100000 BCa unilateral 95 %; target 5.00; activation guard 5.10; gate viejo vigente hasta activación; sin reruns/outlier deletion.

**C32 MUST:** direct+pooler; 5 reps; 2 same+2 distinct alternados; 32 workers×12 iteraciones; 15360 retained samples; nearest-rank p95; ratio >=0.80; same absolute p95 <=25 ms; relative tail <=1.25×distinct+5 ms; cero auth failures/lock waits; preflight dedicado con cada RTT <=12 ms inclusivo; evidencia completa y revisión independiente PASS.

Hasta entonces, el estado permitido es pendiente, FAIL, BLOCKED o INCONCLUSIVE. Este expediente registra actualmente L0 COMPLETE/GREEN, L1 PARTIAL/REVIEW FAIL, L2-L7 BLOCKED, ninguna campaña Vector Hydration y ningún PASS calificante C32.
