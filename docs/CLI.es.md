# Referencia de la CLI

Esta página es un índice corto. El contrato autoritativo de la CLI — el modelo de
invocación, los códigos de salida, cada comando con sus flags, valores por defecto,
sobrescrituras por variables de entorno, requisitos de autenticación, ejemplos
prácticos, el índice de variables de entorno y el apéndice de superficie
obsoleta — vive en **[CLI-REFERENCE.md](CLI-REFERENCE.md)**.

Empieza aquí:

- [Modelo de invocación y códigos de salida](CLI-REFERENCE.md#1-modelo-de-invocacion)
- [Convenciones globales](CLI-REFERENCE.md#2-convenciones-globales)
- [Referencia de comandos](CLI-REFERENCE.md#3-referencia-de-comandos)
- [Índice de variables de entorno](CLI-REFERENCE.md#4-indice-de-variables-de-entorno)
- [Matriz de autenticación](CLI-REFERENCE.md#5-matriz-de-autenticacion)
- [Superficie obsoleta y retirada](CLI-REFERENCE.md#6-superficie-obsoleta-y-retirada)

Para claves de configuración y formatos de fichero consulta
[CONFIGURATION.md](CONFIGURATION.md); para perfiles y herramientas de MCP consulta
[MCP.md](MCP.md); para la API HTTP consulta [HTTP-API.md](HTTP-API.md); para el
despliegue del servidor consulta [SERVER.md](SERVER.md); para la interfaz de
terminal interactiva consulta [TUI-GUIDE.md](TUI-GUIDE.md).

El punto de entrada de producción es `cmd/cortex`. Ejecuta `cortex help` para la
lista de comandos y consulta [CLI-REFERENCE.md](CLI-REFERENCE.md) para el detalle
de cada comando.

La línea base local v2 es de avance únicamente (forward-only). `migrate down` no
es una operación normal soportada y las bases de datos v2 existentes no deben ser
degradadas automáticamente.
