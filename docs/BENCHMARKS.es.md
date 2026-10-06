[← Volver al README](../README.md)

# Benchmarks

> Las tablas de resultados históricos de este documento no son evidencia de release
> salvo que incluyan un comando reproducible, fixture/versión, commit y entorno.
> Usa el gate offline de abajo como contrato de verificación actual del repositorio.

## Resultados medidos: pre-cambio vs post-cambio (judge fijo)

> **Clasificación de evidencia:** puntuaciones de answer-judge medidas y reproducidas
> localmente publicadas como artefactos `eval-comparison/v1` bajo `bench/reports/`.
> **Identidad de evidencia:** cada fila de abajo se lee de un fichero de informe con
> nombre que registra el commit del producto, la configuración del judge, el timestamp
> del run y el conteo de preguntas.
> **Clasificación del evaluador:** las puntuaciones son aceptabilidad de respuesta con
> judge fijo (`fixed_judge_accuracy`), no relevancia de retrieval etiquetada por
> stable-ID/span.
> **Comparabilidad:** las filas pre-cambio y post-cambio solo son comparables entre
> sí: mismo judge, mismos datasets, mismo límite de preguntas, misma generación de
> harness. No aparece en esta página ninguna puntuación de sistema de terceros.

### Protocolo de evaluación

| Parámetro | Valor |
|---|---|
| Judge | `qwen2.5:7b-instruct` servido por el runtime local de Ollama |
| Configuración | `OLLAMA_ENDPOINT`, `OLLAMA_JUDGE_MODEL`, `temperature=0`, `seed=42`, `format=json` (protocolo `fixed-judge/ollama/v1`) |
| Límite de preguntas | 100 preguntas por benchmark por run (`limit=100`), el mismo slice para cada run comparado aquí |
| Datasets | LOCOMO (CC BY-NC 4.0, cinco tipos de pregunta) y LongMemEval (slices de ability tal como los publica el runner: IE, MR) |
| Métrica | `fixed_judge_accuracy` = respuestas aceptadas / preguntas evaluadas, reportado por slice y global |

Dos propiedades del harness hacen que estas cifras sean publicables en absoluto:

- **Contrato de no-fabricación.** Los runners fallan en modo cerrado con
  `common.BlockedError` (código de salida 2) cuando el endpoint del judge es
  inaccesible o no puede producirse una puntuación de confianza. Un run fallido no
  publica puntuación en lugar de una puntuación fallback o adivinada.
- **Chaining de baseline.** `common.EvalBaselineFromReport` carga un informe previo
  en `Config.Baseline`; cada informe registra entonces `baseline_status`
  (`not_recorded` en la primera publicación, `recorded` después) y por slice
  `before` / `after` / `delta` contra gates `min_delta`. El chaining solo compara
  contra el informe con el que se alimenta.

### Comparación medida

| Métrica | Pre-cambio (commit de producto `3bf0f411`) | Post-cambio | Δ absoluto | Δ relativo |
|---|---|---|---|---|
| LOCOMO global | 0.26 (26/100) | 0.56 (56/100) | +0.30 | +115% |
| LOCOMO single-hop | 0.25 | 0.625 | +0.375 | +150% |
| LOCOMO multi-hop | 0.081 | 0.324 | +0.243 | +300% |
| LOCOMO temporal | 0.692 | 0.769 | +0.077 | — |
| LOCOMO open-domain | 0.333 | 0.778 | +0.445 | — |
| LongMemEval global | 0.06 (6/100) | 0.08 (8/100) | +0.02 | — |
| LongMemEval IE | 0.057 | 0.086 | +0.029 | — |
| LongMemEval MR | 0.067 | 0.067 | 0.000 | estable |

Artefactos: pre-cambio `bench/reports/prechange-baseline-locomo.json` y
`bench/reports/prechange-baseline-longmemeval.json`; post-cambio
`bench/reports/first-eval-locomo.json` y `bench/reports/first-eval-longmemeval.json`,
remevidos por `bench/reports/second-eval-locomo.json` y
`bench/reports/second-eval-longmemeval.json`; filas actuales de
`bench/reports/third-eval-locomo.json` (las columnas de LongMemEval no cambian en
`bench/reports/third-eval-longmemeval.json`).

Las columnas relativas se calculan a partir de los valores publicados:
0.30 / 0.26 = +115%, 0.375 / 0.25 = +150% y 0.243 / 0.081 = +300%. Las demás filas
se reportan como deltas absolutos.

### Procedencia de la baseline pre-cambio

Las filas pre-cambio se midieron contra el commit de producto `3bf0f411`, el último
commit main antes de la ola de cambios de retrieval del 2026-10-02, en un worktree
desprendible y desechable fuera del checkout principal.

- El harness `eval-comparison/v1` es posterior a `3bf0f411`, así que el harness
  actual de `bench/` más `go.mod` / `go.sum` se copiaron en ese worktree y se
  ejecutaron contra el árbol de producto pre-cambio. El `bench/` commiteado en
  `3bf0f411` no puede publicar `eval-comparison/v1`, así que la reutilización fue
  obligatoria.
- Compilar el harness reutilizado contra la superficie de API pre-cambio requirió
  exactamente tres adaptaciones, solo en las copias del worktree: eliminar el
  literal `retrieval.AdaptiveSearchOptions.FusionScores` en
  `bench/locomo/runner.go` y `bench/longmemeval/runner.go`, y eliminar la
  asignación `opts.QueryVector` en `bench/locomo/runner.go`. Esas opciones no
  existen en `3bf0f411`. No se modificó ningún fichero de fuente del checkout
  principal.
- La decisión de reutilización del harness y las tres adaptaciones se registran
  verbatim en el array `limitations` de ambos ficheros de informe pre-cambio.

### Reproducibilidad

- El run 2 post-cambio reprodujo el run 1 exactamente: el payload canónico `score`
  (métrica, global, cada slice, `total_questions`, `correct`) es byte-idéntico entre
  `first-eval-*.json` y `second-eval-*.json`. Compruébalo con:

```bash
diff <(jq -S .score bench/reports/first-eval-locomo.json) <(jq -S .score bench/reports/second-eval-locomo.json)
diff <(jq -S .score bench/reports/first-eval-longmemeval.json) <(jq -S .score bench/reports/second-eval-longmemeval.json)
```

- `temperature=0` y `seed=42` hacen determinista al judge para una entrada fija; la
  determinismo no es lo mismo que la precisión, y la repetición de arriba cubre una
  sola máquina y un solo judge.
- **Por qué los deltas del harness leen 0.000.** La baseline encadenada dentro de
  los informes post-cambio del primer y segundo pass fue `first-eval`, que ya es
  posterior a cada cambio de retrieval, así que sus filas por tarea comparan 0.35
  con 0.35 y reportan `delta: 0`. Esa cadena no puede expresar el lift pre-cambio;
  la tabla de comparación de arriba se calcula en cambio a través de los artefactos
  pre-cambio y post-cambio publicados por separado. El actual
  `third-eval-locomo.json` encadena contra `second-eval`, así que sus filas por
  tarea sí reportan movimiento (0.35 → 0.56, `delta: 0.21` en la fila global).

### Limitaciones

1. **Sin fila de vector en la baseline.** La baseline pre-cambio y los runs
   post-cambio con embeddings deshabilitados no llevan ninguna fila vectorial
   `rag-t07` porque no había ningún modelo de embedding disponible cuando corrió la
   baseline. Los runs con embeddings habilitados se registran por separado en
   `bench/reports/eval-embeddings-locomo.json` (y
   `bench/reports/eval-embeddings-longmemeval.json` cuando se publiquen), con el
   modelo de embedding en la procedencia; no tienen contraparte pre-cambio y por
   tanto no forman parte de la tabla before/after.
2. **Sin cifras de competidores.** Las puntuaciones de otros sistemas están
   deliberadamente ausentes. Judges, prompts, backbones de modelo, subconjuntos de
   preguntas y métricas distintos no son comparables, así que importarlas aquí
   produciría un ranking falso.
3. **Judge local único.** Un solo modelo de judge en un solo runtime local: una
   única baseline pre-cambio frente a tres passes post-cambio (los dos primeros
   byte-idénticos), `n=100` por benchmark, sin análisis de varianza multi-seed ni
   cross-machine y sin intervalos de confianza. Trata los movimientos a nivel de
   slice, especialmente `multi-hop` y `open-domain`, como indicativos más que como
   concluidos.
4. **Nivel absoluto.** El lift de 0.26 a 0.56 en LOCOMO (+0.30 absoluto, +115%
   relativo) y de 0.06 a 0.08 en LongMemEval está medido, pero LOCOMO sigue
   fallando 44 de 100 preguntas; en el run actual open-domain (0.778) y temporal
   (0.769) son los slices más fuertes y multi-hop (0.324) el más débil.
5. **Entorno.** Los informes registran endpoint del judge, modelo, seed, límite y
   datasets, pero no un perfil de hardware; no se hace ninguna afirmación de
   latencia, throughput ni recursos a partir de estos runs.
6. **Reclamaciones de release.** Estas tablas siguen siendo evidencia de
   answer-judge bajo las reglas de reclamación de release de arriba: no son
   evidencia de relevancia de retrieval por stable-ID/span y no satisfacen por sí
   solas ningún gate de release.

## Lo que demuestra la baseline

Los contratos de baseline commiteados demuestran que Cortex puede validar un corpus
de retrieval con versión y etiquetas; preservar stable IDs rankeados por query;
calcular campos de calidad de retrieval, corrección, latencia, throughput y
recursos; comparar runs independientes; y preregistrar gates de release inmutables.
El contrato del corpus es `cortex.retrieval-corpus/v1`, el contrato del informe es
`retrieval-evidence-report/v1`, y cada run debe registrar su corpus, protocolo,
perfil, build y versiones de hardware exactos. Los gates universales de corrección
requieren cero violaciones de aislamiento y coincidencia exacta con el conjunto
autoritativo de stable-IDs elegibles.

Esta es una **baseline de metodología y ruta actual**, no evidencia de que un futuro
candidato de retrieval sea mejor. Un resultado sostiene una afirmación de Cortex
solo cuando su informe y el corpus referenciado están commiteados, completos,
reproducidos independientemente, y aprobados bajo un gate registrado antes de
observar los resultados candidatos.

## Lo que no demuestra la baseline

- No establece un nuevo umbral de calidad, latencia, throughput, CPU, RSS,
  almacenamiento o índice. Los umbrales numéricos siguen sin fijarse hasta que
  existan runs de baseline representativos, análisis de varianza, aprobación de
  corpus/hardware y el sign-off del reviewer.
- No convierte resultados externos de LOCOMO, DMR o LongMemEval en rendimiento
  reproducido por Cortex.
- No hace que el F1 de tokens de respuesta, ROUGE-L o la corrección del judge sean
  equivalentes a la relevancia de retrieval etiquetada por stable-ID. Esas métricas
  evalúan el texto de la respuesta o la aceptabilidad de la respuesta; la evidencia
  de retrieval de release requiere IDs de episodio o fact relevantes, o spans de
  evidencia inmutables.
- No demuestra portabilidad, paridad de proveedores vectoriales ni rendimiento a
  escala de producción. Esas reclamaciones necesitan su propia matriz de build con
  versión y de evidencia.

Las tablas y comandos del suite legacy de abajo se conservan por continuidad. Salvo
que una fila enlace un informe de evidencia completo de Cortex, trátala como
evidencia histórica o exploratoria, no como un gate de release ni como una
comparación cross-system.

## Protocolo de retrieval con versión

| Campo de evidencia | Registro requerido |
|---|---|
| Corpus | Versión de esquema y de corpus; IDs de query inmutables; clases de perfil/query; hard negatives; etiquetas de no-respuesta, temporal, aislamiento, privacidad, clasificación y lifecycle |
| Relevancia | Stable IDs de episodio/fact relevantes o spans de bytes semi-abiertos; las etiquetas ausentes dejan la evidencia de release incompleta |
| Ejecución | ID/versión de protocolo y perfil, commit de build y estado dirty, proveedor/modelo donde se use, y outputs rankeados actuales trazables |
| Entorno | Perfil de hardware con nombre, SO, arquitectura, CPU, memoria y unidades de medición explícitas |
| Informe | Resultados por query y por clase, definiciones de métricas, limitaciones y IDs de run retenidos independientemente |

### Splits y evaluador

Los splits se asignan por ID de query inmutable bajo una estrategia con versión. El
split de desarrollo/calibración puede seleccionar detalles de protocolo y gates; la
evidencia de decisión held-out no debe reutilizarse después de observar los
resultados candidatos. El evaluador es la implementación Go commiteada en
`bench/common`: valida etiquetas de stable-ID/span y calcula agregados
deterministas. Los judges de respuesta opcionales pertenecen solo a los suites de
evaluación de respuesta legacy y deben revelar el modelo del judge, la clase de
endpoint, la versión de prompt/protocolo y la política de fallo/abstención. No
proporcionan las etiquetas de retrieval que faltan.

### Métricas e incertidumbre

| Métrica | Definición e interpretación |
|---|---|
| Recall@k | Fracción de stable IDs relevantes etiquetados devueltos en los primeros `k` resultados |
| MRR | Rango recíproco del primer stable ID relevante etiquetado |
| nDCG | Ganancia acumulado descontado normalizado por el orden ideal; soporta relevancia graduada |
| Recall de evidencia | Fracción de IDs de episodio/fact etiquetados o spans de evidencia cubiertos |
| No-respuesta/abstención | Si el perfil devuelve correctamente ninguna respuesta soportada para las queries etiquetadas como no-respuesta |
| Violaciones de aislamiento | Conteo de IDs devueltos no autorizados o de otro modo inelegibles; cualquier valor no-cero bloquea el release |
| Corrección de filtros | Igualdad exacta entre la elegibilidad devuelta y el conjunto autoritativo de stable-IDs filtrados |
| Latencia/throughput | Duraciones p50/p95/p99 y queries completadas por segundo, con unidades y tamaño de muestra |
| Recursos | Segundos de CPU, bytes de RSS pico, bytes de almacenamiento del corpus y bytes del índice de retrieval |

Los informes revelan el tamaño de muestra, los resultados por clase, el método de
incertidumbre y el nivel de confianza, dispersión/intervalos de confianza donde
apliquen, y outliers. Los runs independientes se comparan solo cuando las
identidades de corpus, build, hardware y protocolo coinciden. Los campos
deterministas deben coincidir exactamente; los campos medidos usan una tolerancia
preregistrada derivada de la varianza de la baseline, nunca una tolerancia
post-resultados.

### Hardware y recursos

Registra un envelope de hardware reproducible en lugar de un apodo de máquina: ID de
perfil, SO/versión, arquitectura, modelo/conteo de CPU, memoria disponible y
versiones relevantes de proveedor/modelo. Los informes deben indicar si se midieron
tiempo de CPU, RSS, almacenamiento, tamaño de índice, latencia y throughput. Ceros
placeholder de un adaptador legacy significan **sin medir**, no gratis ni
instantáneo.

### Gates de release

El registro de gates está versionado e inmutable. Los gates de corrección (cero
filtración de aislamiento y elegibilidad exacta de filtros) son universales y no
relajables. Los gates de calidad, latencia, throughput y recursos son específicos de
perfil y de clase de query. Registran dirección de la métrica, tamaño de muestra,
versiones de corpus/hardware, aprobación y política de bloqueo antes de un run
candidato. Cambiar un gate después de los resultados requiere una nueva versión de
protocolo, justificación escrita, aprobación y una evaluación held-out fresca que no
reutilice la evidencia de decisión.

## Evidencia externa y reproducida por Cortex

| Suite/evidencia | Clasificación | Reclamación permitida |
|---|---|---|
| Corpus de Cortex con versión + informe de evidencia completo | Reproducido por Cortex solo cuando se ejecuta con el protocolo documentado sobre el build/hardware revelado | Reclamaciones de retrieval y recursos limitadas a ese perfil, corpus, protocolo y entorno exactos |
| Adaptador LOCOMO | Evidencia de respuesta externa/legacy; dataset CC BY-NC 4.0 | Preservar fuente, split, categorías, localizadores de evidencia upstream, F1 y salida opcional del judge; no reclamar relevancia de retrieval por stable-ID sin etiquetas de Cortex |
| Adaptador DMR / MSC-Self-Instruct | Evidencia de respuesta externa/legacy; dataset Apache-2.0 | Preservar F1/ROUGE-L y semántica opcional del judge; las respuestas fuente no proporcionan IDs/spans relevantes de Cortex |
| Adaptador LongMemEval | Evidencia de respuesta externa/legacy; la distribución upstream lleva el aviso de licencia aplicable | Preservar categorías de ability, localizadores temporales, abstención, F1 y comportamiento del judge; los localizadores temporales no son etiquetas de relevancia de Cortex |

Los adaptadores son deliberadamente solo-de-reportado y preservan los runners y el
comportamiento de puntuación existentes. `cortex_reproduction: false`, etiquetas de
retrieval incompletas o un flag de elegibilidad de release falso bloquean la redacción
que presente un resultado adaptado como rendimiento de retrieval de Cortex.

## Reproducir la baseline

Ejecuta desde la raíz del repositorio sobre un build commiteado limpio. El primer
comando es el contrato de documentación ejecutable; el segundo valida los contratos
de baseline deterministas y todos los adaptadores de benchmark preservados sin
requerir descarga de dataset, embeddings, un judge ni un servicio externo.

```bash
go test -v -count=1 ./bench -run TestRetrievalBaselineDocumentationContract
go test -v -count=1 ./bench/...
```

Los runs a escala de dataset, con judge vivo, de proveedor de embeddings y de
rendimiento son evidencia opt-in por separado. Registra el comando exacto,
artefacto/versión del dataset y split, versiones de perfil/proveedor/modelo,
commit de build/estado dirty, perfil de hardware, IDs de informe y rutas de salida.
No compares informes cuyos campos de identidad difieran sin un protocolo de
normalación aprobado por separado.

## Licencias y limitaciones

- El código de Cortex y la evidencia de Cortex generada siguen regidos por la
  licencia de este repositorio; los datasets de benchmark conservan sus licencias
  y atribuciones upstream.
- LOCOMO está documentado como CC BY-NC 4.0, MSC-Self-Instruct como Apache-2.0, y
  LongMemEval con el aviso de licencia que envía la distribución upstream exacta.
  Verifica los términos contra la versión descargada antes de redistribuir.
- Las descargas de datasets, los servicios de embedding y los judges vivos no son
  requeridos para la validación determinista de contratos y pueden imponer términos,
  coste, red, no-determinismo o restricciones de privacidad por separado.
- El corpus miniatura commiteado valida contratos y aislamiento adversarial; no es
  evidencia representativa para escala de producción ni cobertura de dominio.
- Las puntuaciones de los suites legacy siguen siendo útiles para la continuidad,
  pero sus etiquetas orientadas a la respuesta, evaluadores y splits no son
  intercambiables con el protocolo de relevancia por stable-ID/span de Cortex.

Los suites legacy de Cortex de abajo ejercitan tres benchmarks estándar de memoria.
Sus resultados son reproducibles solo cuando se revelan el dataset exacto, split,
evaluador, build, perfil/proveedor, hardware y protocolo; las tablas por sí solas no
constituyen evidencia de release de Cortex.

## Resumen de resultados

> **Clasificación de evidencia:** snapshot histórico del repositorio, sin verificar.
> **Identidad de evidencia:** las filas retenidas no identifican un artefacto de
> resultado, run ID, commit de build/estado dirty, artefacto de dataset exacto y
> split, perfil de hardware ni incertidumbre. No son evidencia de release de Cortex
> reproducible.
> **Clasificación del evaluador:** las puntuaciones numéricas usan F1 de tokens de
> respuesta legacy (y, donde se indica, ROUGE-L u un answer-judge opcional), no
> relevancia de retrieval etiquetada por stable-ID/span.
> **Comparabilidad:** los valores se conservan solo por continuidad histórica. No los
> uses para conclusiones cross-provider, cross-system, de rendimiento o causales.

### LOCOMO (Long-Term Conversational Memory)

**Dataset:** 1,986 preguntas en 10 conversaciones, 5 tipos de pregunta.
**Fuente:** [snap-research/locomo](https://github.com/snap-research/locomo) (ACL 2024)

| Modo | single-hop | multi-hop | temporal | Ratio legacy mostrado en el snapshot |
|------|-----------|-----------|----------|-------------------|
| Solo FTS5 (baseline) | 0.002 | 0.001 | 0.000 | — |
| FTS5 + Ollama (nomic-embed-text) | 0.025 | 0.016 | 0.026 | **12-16x** |
| FTS5 + OpenAI (text-embedding-3-small) | 0.026 | 0.016 | 0.037 | **13-37x** |

> **Nota:** Estas puntuaciones son F1 de tokens de respuesta crudo entre el contexto
> recuperado y las respuestas gold. No miden la precisión de generación de respuesta,
> no contienen etiquetas de relevancia stable-ID/span de Cortex y no sustentan la
> precisión de un sistema externo. Los ratios son datos retenidos del snapshot
> legacy, no afirmaciones verificadas de mejora de retrieval.

### DMR (Deep Memory Retrieval)

**Dataset:** 500 conversaciones multi-sesión de MSC-Self-Instruct.
**Fuente:** [MemGPT/MSC-Self-Instruct](https://huggingface.co/datasets/MemGPT/MSC-Self-Instruct) (arXiv:2310.08560)

| Modo | Puntuación media (F1 + ROUGE-L) |
|------|--------------------------|
| Solo FTS5 | 0.000 |
| Vectores FTS5 + Ollama | Pendiente de run completo |
| Vectores FTS5 + OpenAI | Pendiente de run completo |

### Comparación de proveedores de embedding

Probado en LOCOMO (subconjunto de 50 preguntas):

| Proveedor | Modelo | Dimensiones | single-hop | multi-hop | temporal | Tiempo | Coste |
|----------|-------|-----------|-----------|-----------|----------|------|------|
| **Ollama** | nomic-embed-text | 768 | 0.025 | 0.016 | 0.026 | 12 min | $0 |
| **OpenAI** | text-embedding-3-small | 1536 | 0.026 | 0.016 | 0.037 | 21 min | ~$0.02 |
| Ninguno (FTS5) | — | — | 0.002 | 0.001 | 0.000 | 4 min | $0 |

**Límite de interpretación:** la tabla preserva puntuaciones de tokens de respuesta,
tiempos transcurridos y costes reportados de un run legacy de 50 preguntas
identificado solo parcialmente. Sin la identidad de evidencia requerida ni la
incertidumbre de runs repetidos, estas filas no sostienen equivalencia de
proveedores, conclusiones de razonamiento temporal, causalidad de dimensiones de
embedding, afirmaciones de velocidad de proveedor o de causa de red, multiplicadores
de retrieval ni una afirmación absoluta sobre la capacidad temporal de FTS5.

## Metodología

### Pipeline

Para cada pregunta de benchmark:

1. **Ingest** — Parsear las conversaciones del dataset en sesiones + observaciones
   de Cortex (almacenadas en SQLite en memoria)
2. **Embed** — Cuando la búsqueda vectorial está habilitada, generar embeddings para
   cada observación vía Ollama u OpenAI
3. **Search** — Ejecutar búsqueda de keywords FTS5 + búsqueda opcional de similitud
   coseno vectorial
4. **Fuse** — Combinar resultados FTS5 y vectoriales usando Reciprocal Rank Fusion
   (k=60)
5. **Score** — Comparar los top-5 resultados recuperados contra la respuesta gold
   usando solape de tokens F1
6. **Judge** — Opcionalmente evaluar la aceptabilidad de la respuesta con el runtime
   local de Ollama

### Puntuación

- **Solape de tokens F1:** tokenizar predicción y referencia, calcular
  precisión/recall/F1 sobre los conjuntos de tokens
- **ROUGE-L:** Longest Common Subsequence F1 entre predicción y referencia
- **Answer judge de Ollama:** el runtime commiteado es solo-Ollama y usa por defecto
  `qwen2.5:7b-instruct`, `temperature=0` y `seed=42`. Configúralo con
  `OLLAMA_ENDPOINT` y `OLLAMA_JUDGE_MODEL`. Esta puntuación opcional no es evidencia
  de retrieval y nunca reemplaza las etiquetas de relevancia por stable-ID o por
  evidence-span.
- **Umbral de correct:** F1 >= 0.3 para LOCOMO, (F1 + ROUGE-L) / 2 >= 0.3 para DMR

### Reciprocal Rank Fusion (RRF)

Cuando están disponibles tanto resultados FTS5 como vectoriales, se combinan usando
RRF:

```
RRF_score(doc) = Σ 1/(k + rank_i)  where k=60
```

Cada sistema de ranking (FTS5 por BM25, vectores por similitud coseno) contribuye
independientemente. Los documentos que aparecen en ambos rankings obtienen una
puntuación combinada mayor que cualquiera de los dos por sí solo.

### Limitaciones

1. **Evaluación solo de retrieval** — Cortex recupera memorias relevantes pero no
   genera respuestas. Una evaluación end-to-end requiere una capa LLM encima.
2. **Embedding secuencial** — Cada observación se embedde una a una. El efecto del
   embedding por lotes sobre el runtime no fue evaluado por este snapshot.
3. **Sin boost de grafo** — La expansión de vecinos del grafo de conocimiento no se
   usa en estos benchmarks; su efecto sobre las puntuaciones multi-hop no fue
   evaluado.
4. **F1 es conservador** — El solape de tokens penaliza a los sistemas de retrieval
   que devuelven contenido contextualmente correcto pero léxicamente diferente.

## Ejecutar benchmarks

### Prerrequisitos

```bash
# Build with vector search support
go build -tags cortex_vectors ./cmd/cortex

# For local embeddings (recommended)
# Install Ollama: https://ollama.com
ollama pull nomic-embed-text

# Download datasets
cd bench
chmod +x download.sh
./download.sh
```

### Ejecutar

```bash
# LOCOMO — FTS5 only (fast, no dependencies)
go test ./bench/locomo/ -run TestRunFullDataset -v -timeout 30m

# LOCOMO — with Ollama embeddings (requires Ollama running)
go test -tags cortex_vectors ./bench/locomo/ -run TestRunWithOllamaEmbeddings -v -timeout 30m

# LOCOMO — with OpenAI embeddings (requires API key)
CORTEX_EMBEDDING_API_KEY=sk-... go test -tags cortex_vectors ./bench/locomo/ -run TestRunWithOpenAIEmbeddings -v -timeout 30m

# DMR
go test ./bench/dmr/ -run TestRunFullDataset -v -timeout 30m

# All benchmarks (short mode — uses subsets)
go test ./bench/... -short -v -timeout 10m
```

### Resultados

Los resultados se guardan como JSON en `bench/results/`:

```bash
ls bench/results/
# locomo.json              — Full LOCOMO (FTS5 only)
# locomo_ollama_50.json    — LOCOMO subset with Ollama
# locomo_openai_50.json    — LOCOMO subset with OpenAI
# dmr.json                 — Full DMR
```

## Motores SOTA de RAG y retrieval de grafo (2025/2026)

Cortex integra un motor de retrieval y propagación de grafo state-of-the-art de 4
tiers en 100% Go puro (Zero-CGO):

| Motor / Algoritmo | Origen y referencia | Latencia (RAM) | Caso de uso principal |
|---|---|---|---|
| **HippoRAG (PPR)** | NeurIPS 2024 (arXiv:2405.14831) | $< 2\text{ms}$ | Propagación relacional multi-hop a través de edges de grafos de conocimiento cognitivo y AST |
| **Adaptive-RAG** | NAACL 2024 (arXiv:2403.14403) | $< 0.1\text{ms}$ | Enrutado dinámico de complejidad de queries en 4 tiers (Direct, Hybrid, Multi-Hop, Architectural) |
| **LightRAG** | EMNLP 2024 (arXiv:2410.05779) | $< 1\text{ms}$ | Resúmenes jerárquicos de modularidad de comunidades ($C = \frac{2 E_{\text{int}}}{N(N-1)}$) |
| **Corrective RAG (CRAG)**| arXiv:2401.15884 | $< 0.1\text{ms}$ | Puntuación de confianza (`high`, `medium`, `low`) y supresión de ruido de alucinación |
| **ColBERT MaxSim** | SIGIR / arXiv:2004.12832 | $< 0.5\text{ms}$ | Re-ranking de interacción tardía multi-token: $\text{MaxSim}(Q, D) = \sum_{q} \max_{d} (q \cdot d)$ |

---

## Matriz de arquitectura competitiva

| Capacidad | Cortex Server | MemPalace | Mem0 | Zep |
|---|---|---|---|---|
| **Arquitectura de runtime** | **Binario único Go (Zero-CGO)** | Python flat-file | Servicio Python | Python / LangChain |
| **Propagación de grafo** | **HippoRAG PPR en Go ($< 2\text{ms}$)** | Sin grafo | NetworkX básico | Graphiti (dependiente de Neo4j) |
| **Enrutado de retrieval** | **Adaptive-RAG (4 tiers)** | Grep de keywords | Vector fijo | Solo vector |
| **Gating de ruido** | **Gate de confianza CRAG** | Ninguno | Ninguno | LLM Reranker (lento) |
| **Multi-tenancy y auth** | **Postgres 16 RLS + SQLite local** | Usuario único | Servicio cloud | Dependiente de SaaS |
| **Monitorización en tiempo real** | **Control room Next.js + Sigma.js** | Ninguno | Web dashboard | UI cloud |

---

## Licencias de datasets

| Dataset | Licencia | Fuente |
|---------|---------|--------|
| LOCOMO | CC BY-NC 4.0 | [snap-research/locomo](https://github.com/snap-research/locomo) |
| MSC-Self-Instruct (DMR) | Apache 2.0 | [MemGPT/MSC-Self-Instruct](https://huggingface.co/datasets/MemGPT/MSC-Self-Instruct) |
| LongMemEval | MIT | [xiaowu0162/LongMemEval](https://github.com/xiaowu0162/LongMemEval) |

## Referencias

- HippoRAG: "HippoRAG: Neurobiologically Inspired Long-Term Memory for Large Language Models" (NeurIPS 2024, [arXiv:2405.14831](https://arxiv.org/abs/2405.14831))
- Adaptive-RAG: "Adaptive-RAG: Determining When to Retrieve via Dynamic Query Complexity" (NAACL 2024, [arXiv:2403.14403](https://arxiv.org/abs/2403.14403))
- LightRAG: "LightRAG: Simple and Fast Knowledge Graph RAG" (EMNLP 2024, [arXiv:2410.05779](https://arxiv.org/abs/2410.05779))
- CRAG: "Corrective Retrieval Augmented Generation" ([arXiv:2401.15884](https://arxiv.org/abs/2401.15884))
- ColBERT: "ColBERT: Efficient and Effective Passage Search via Contextualized Late Interaction over BERT" (SIGIR 2020, [arXiv:2004.12832](https://arxiv.org/abs/2004.12832))
- LOCOMO: "Evaluating Very Long-Term Conversational Memory of LLM Agents" (ACL 2024, [arXiv:2402.17753](https://arxiv.org/abs/2402.17753))
- DMR/MemGPT: "MemGPT: Towards LLMs as Operating Systems" ([arXiv:2310.08560](https://arxiv.org/abs/2310.08560))
- LongMemEval: "Benchmarking Chat Assistants on Long-Term Interactive Memory" (ICLR 2025, [arXiv:2410.10813](https://arxiv.org/abs/2410.10813))
- Engram: "Effective, Lightweight Memory Orchestration for Conversational Agents" ([arXiv:2511.12960](https://arxiv.org/abs/2511.12960))
- Zep/Graphiti: "A Temporal Knowledge Graph Architecture for Agent Memory" ([arXiv:2501.13956](https://arxiv.org/abs/2501.13956))
