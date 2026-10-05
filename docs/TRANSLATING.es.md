# Traducción de la documentación

La documentación de Cortex es bilingüe: inglés (EN) y español (es-ES). Este documento describe la convención de ficheros, la política de idioma y el flujo de trabajo para añadir o revisar traducciones.

## Convención de sufijo (suffix mode)

El sitio se genera con [mkdocs-static-i18n](https://ultrabug.github.io/mkdocs-static-i18n/) en modo `suffix` (configurado en `mkdocs.yml`):

- La página en inglés es el fichero fuente: `docs/<nombre>.md`.
- La traducción al español es un fichero adyacente: `docs/<nombre>.es.md`.
- Cada idioma se publica en su propio árbol de URLs: `/` para EN y `/es/` para español.
- El selector de idioma del tema Material (enCabecera, `extra.alternate`) alterna entre ambas versiones de la página actual.

Ejemplo: `docs/INSTALLATION.md` → `docs/INSTALLATION.es.md` → `/es/INSTALLATION/`.

## El inglés es la fuente de verdad

- La página EN (`docs/<nombre>.md`) es la fuente de verdad: el contenido, la estructura, los enlaces y los bloques de código se originan allí.
- Una traducción es una derivación fiel; nunca introduce funcionalidad, comandos o rutas que la página EN no tenga.
- Si EN y ES divergen, primero se corrige EN y después se actualiza ES para que vuelva a ser fiel.
- Las páginas EN **nunca** se editan como parte de una traducción.

## Comportamiento de fallback (páginas sin traducir)

- `fallback_to_default: true` está habilitado en `mkdocs.yml`: una página sin `<nombre>.es.md` publica **contenido EN** bajo la URL `/es/...`.
- El build estricto (`mkdocs build --strict`) no falla por páginas sin traducir; la cobertura incompleta es un estado válido y gradual.
- El selector de idioma sigue funcionando en páginas sin traducción (ambos enlaces resuelven; el `/es/` muestra el EN).

## Cómo añadir una traducción

1. Elige la página EN en `docs/`.
2. Crea el fichero adyacente `docs/<nombre>.es.md` (mismo nombre exacto más el sufijo `.es.md`).
3. Traduce el contenido al español neutro es-ES:
   - **No se traducen**: bloques de código y comandos, rutas de ficheros, flags de CLI (`--tools=dev`), identificadores de configuración (`server.storage.dsn`), variables de entorno (`CORTEX_HTTP_TOKEN`), nombres de herramientas MCP (`cortex_save`), ni la primera columna (clave) de las tablas de configuración.
   - **Sí se traducen**: prosa, encabezados, descripciones de tabla y comentarios dentro de bloques de código cuando aportan claridad (sin alterar las órdenes).
   - La terminología técnica de uso consagrado en inglés se conserva: MCP, AST, FTS5, RRF, embedding, bearer, tenant, workspace, blast radius, hook.
   - Conserva exactamente los bloques de código, tablas y enlaces relativos de la página EN.
4. No hace falta tocar `mkdocs.yml`: el plugin descubre los `.es.md` automáticamente.
5. Verifica (ver más abajo).

## Cómo comprobar la cobertura

```bash
# Listado rápido de páginas EN sin traducción (.es.md ausente)
for f in docs/*.md; do
  case "$f" in *.es.md|*/README.md|*/TRANSLATING.md) continue ;; esac
  base="${f%.md}"
  [ -f "$base.es.md" ] || echo "missing: $base.es.md"
done
```

Cuando exista, `scripts/check-doc-i18n.sh` ejecuta esta comprobación como paso no bloqueante de CI (sale con 0 aunque falten traducciones).

Verificación local del sitio:

```bash
pip install -r docs/requirements.txt
mkdocs build --strict
mkdocs serve
```

Comprueba en el sitio generado:

- `site/es/<nombre>/index.html` existe para cada página traducida.
- `site/es/<nombre>/index.html` muestra contenido EN para una página sin traducción (fallback).
- El selector de idioma alterna entre `/` y `/es/` en la página actual.
