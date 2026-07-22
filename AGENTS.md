# AGENTS.md — solar-tariff-compare (guía de desarrollo)

Guía para agentes (Claude Code, opencode, Cursor, ...) que **desarrollan** esta
herramienta. Para **conducirla** y ayudar a un usuario a elegir comercializadora, usa
la skill [`.claude/skills/solar-tariff-compare`](.claude/skills/solar-tariff-compare/SKILL.md),
que cubre el flujo de recopilación de datos, ejecución e interpretación.

## Qué es

CLI en Go (binario único, sin estado) que, dada la curva de consumo horaria real (CCH
de e-distribución o CSV de Datadis) y opcionalmente la producción FV (PVGIS estimado o
CSV real):

1. **Compara ofertas** contra el API pública del comparador de la **CNMC** (consumo
   agregado en períodos P1/P2/P3 de la tarifa 2.0TD, con festivos).
2. **Simula excedentes** hora a hora con precios de **e-sios** (PVPC 2.0TD + precio de
   excedentes regulado), comparando esquemas: regulada, indexada y batería virtual.
3. **Ranking neto**: atribuye a cada oferta la compensación de sus excedentes según su
   comercializadora (`RetailerRegistry`) y ordena por coste neto anual. Es lo que el
   ranking crudo de la CNMC no hace.

## Estructura del código

```
cmd/solar-tariff-compare/main.go   # CLI: flags, orquestación, salida (tabla / JSON)
solartrack/
  periods.go       # clasificación P1/P2/P3, festivos nacionales no sustituibles, Pascua
  consumption.go   # parser CCH e-distribución (5 y 7 columnas); hourStart (mapeo DST)
  datadis.go       # parser Datadis (auto-detectado); reusa hourStart
  production.go     # PVGIS (perfil mes×hora), overlay autoconsumo/excedentes
  cnmc.go          # cliente del API CNMC, PartitionSuspectOffers
  esios.go         # precios horarios PVPC (1001) y excedentes (1739) vía curl
  excedents.go     # SimulateSurplus: 3 esquemas genéricos (-sim)
  ranking.go       # RetailerRegistry + RankOffersWithSurplus (ranking neto por oferta)
```

Cada `.go` tiene su `_test.go`. Los tests de integración se llaman `*_Live`.

## Convenciones que conviene respetar al tocar el código

- **Código en inglés, comentarios y docs en castellano** (ver git log; una excepción
  puntual en catalán en algún comentario del registro es tolerada pero no la norma).
- **No em dashes** en la documentación.
- **Instantes horarios**: usa `hourStart` (consumption.go) para mapear la etiqueta
  Hora 1..25 al instante de inicio. Suma horas absolutas desde medianoche local, así el
  día del cambio de octubre (25 horas) y el de marzo (23 horas) se tratan bien y la hora
  repetida produce dos instantes distintos. No uses `time.Date(..., h-1, ...)` directo.
- **`time.Time` como clave de map**: la igualdad compara el puntero `*Location`, y
  `time.LoadLocation` puede devolver punteros distintos. En tests, indexa con
  `findHour` (compara por componentes), no con `time.Date` directo. Ver el comentario
  largo en `datadis_test.go`.
- **Precio 0 vs sin dato**: `HourlySeries.Seen` marca las horas con dato real. Un precio
  0 del indicador de excedentes (frecuente al mediodía desde 2024) es real y debe
  respetarse; sólo las horas sin dato se sustituyen por el perfil medio. Usa
  `PriceAt(dia, hora)`, no `ByDay[dia][hora] == 0`.
- **kWh enteros a la CNMC**: el API devuelve HTTP 400 con decimales en el consumo
  (`buildParams` ya redondea). La potencia sí admite decimales.
- **Compensación no negativa**: ninguna comercializadora paga por verter; `surplusRate`
  y `SimulateSurplus` recortan a 0 los precios horarios negativos.

## Modelo de excedentes: dos registros distintos

- `SchemesRegistry` (excedents.go): **3 arquetipos genéricos** (regulada / indexada /
  batería virtual) que compara `-sim`. Aíslan el efecto del esquema usando PVPC como
  precio común. No son ofertas concretas.
- `RetailerRegistry` (ranking.go): **términos reales por comercializadora** (precio
  €/kWh, `CeilingAnnual`, `MonthlyFee`, `ExpiryMonths`, `ThrottleFraction`). Alimentan
  el ranking neto. Editable; precios revisados en julio 2026 (ver
  `docs/REVIEW-autoconsum.md`). Si una oferta no casa, cae en `DefaultSurplusTerms`.

`LookupSurplusTerms` casa por subcadena, insensible a mayúsculas **y acentos**
(`foldDiacritics`), probando las claves de más larga a más corta (determinista).

## Tests

```bash
go test ./... -skip _Live            # rápidos (sin red) — usa esto normalmente
SOLARTRACK_SKIP_LIVE=1 go test ./... # equivalente vía env var
go test ./...                        # TODOS, incluidos *_Live (red: CNMC + PVGIS + e-sios)
```

Los `*_Live` se ejecutan **por defecto**; hay que saltarlos explícitamente. Un valor
vacío (`SOLARTRACK_SKIP_LIVE=`) NO salta (el código sólo mira si la var no está vacía).

## Dependencias externas y red

- **CNMC**: `https://comparador.cnmc.gob.es/api/publico/ofertas/electricidad`, sin auth.
  Ver `docs/CNMC-API.md`.
- **PVGIS**: `https://re.jrc.ec.europa.eu/api/seriescalc`, sin auth.
- **e-sios**: se descarga vía `curl` (e-sios bloquea el fingerprint TLS del cliente de
  Go con 403). `curl` viene por defecto en macOS/Linux/Windows 10+. Sin `ESIOS_TOKEN`
  sólo se obtiene el último día (`source: latest`); con token, histórico de un año.
- Toda descarga se ejecuta en la máquina del usuario; un sandbox sin red no puede
  ejecutar los `*_Live`.

## Errores frecuentes al desarrollar

- `CNMC HTTP 400`: consumo con decimales. `buildParams` redondea; revisa que no se
  inyecten decimales por otra vía.
- `e-sios 403`: sin token y filtrando por geo. El código evita el filtro geo sin token;
  si persiste, hace falta `ESIOS_TOKEN`.
- Panic en `parsePVGISTime`: requiere >= 13 caracteres antes del slice; ya validado.

## Deuda técnica conocida (candidata a mejoras)

- El ranking neto usa PVPC como proxy del término de energía de cada oferta (la CNMC no
  expone precios unitarios). Es una aproximación documentada, no la factura exacta.
- El registro de comercializadoras se compila; un flag `-registry fichero.json` para
  cargarlo sin recompilar facilitaría mantenerlo al día (ver la skill).
- 2.0TD admite dos potencias (punta/valle); ahora se envía la misma a las tres franjas.
