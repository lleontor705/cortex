# Exportación a Obsidian

`cortex export --to-obsidian --vault PATH` crea una proyección de solo lectura
bajo `cortex/projects/`. SQLite sigue siendo la fuente de verdad; el exportador
nunca importa ni muta observaciones.

Las exportaciones se preparan (staged) y se confirman como una única
transacción. Los ficheros existentes se renombran primero a copias de seguridad
en el mismo directorio, los ficheros preparados se renombran atómicamente a su
posición y el manifiesto se confirma al final. Cualquier fallo restaura todas las
copias de seguridad. El manifiesto es byte a byte estable en una exportación
sin cambios (no-op).

Solo se descubren ficheros Markdown regulares, no simbólicos (symlink), dentro
del vault. Los componentes de ruta se normalizan y se sanean para nombres
reservados de Windows, caracteres inválidos, espacios/puntos finales,
traversal, colisiones de mayúsculas y límites de longitud. Una nota renombrada
solo se reconcilia cuando su checksum registrado sigue coincidiendo; las notas
renombradas editadas se dejan intactas y se reportan como conflicto. Los valores
de `cortex_id` duplicados son errores e incluyen ambas rutas.

El frontmatter se escapa como YAML y las observaciones relacionadas usan
wikilinks deterministas. Las observaciones personales/privadas se excluyen salvo
opt-in explícito, con una advertencia.
