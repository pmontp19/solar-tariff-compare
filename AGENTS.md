# AGENTS.md — solar-tariff-compare

Guía para agentes (opencode, Claude Code, Cursor, ...) para conducir esta
herramienta y ayudar a un usuario a decidir su comercializadora de electricidad con
autoconsumo FV.

## Qué hace la herramienta

CLI en Go (binario único) que, dada la curva de consumo horaria real (CCH) y
opcionalmente la producción FV (PVGIS estimado o CSV real):

1. **Compara ofertas** contra el API pública del comparador de la **CNMC**
   (consumo agregado en períodos P1/P2/P3 de la tarifa 2.0TD, con festivos).
2. **Simula excedentes** hora a hora con precios de **e-sios** (PVPC horario +
   precio de excedentes regulado), comparando esquemas: regulada, indexada y
   **batería virtual**.

## Cuándo usarla

- El usuario tiene placas (o las planea) y quiere elegir comercializadora.
- El usuario tiene su CSV de curva horaria (CCH de e-distribución).
- Quieres comparar excedentes indexados vs batería virtual para su perfil.

## Invocación típica

```bash
# 1. Sólo consumo (sin FV): ranking CNMC
solar-tariff-compare -consum curva.csv -cp 8001 -top 20

# 2. Con FV estimada (PVGIS) + comparativa de excedentes (la decisión clave)
solar-tariff-compare -consum curva.csv -cp 8001 -kwp 4.1 -sim -top 10

# 3. Con producción real (CSV de 7 columnas): más preciso que PVGIS
solar-tariff-compare -consum consumo.csv -prod produccion.csv -cp 8001 -sim

# 4. Salida JSON (para parsear programáticamente)
solar-tariff-compare -consum curva.csv -cp 8001 -kwp 4.1 -sim -json
```

## Leer la salida (para recomendar)

La salida tiene tres bloques:

1. **Resumen consumo/FV**: `Consumo: X kWh/año (P1..P2..P3..)` y `Producción FV: ... | autoconsumo | excedentes | cobertura`.
2. **Ranking CNMC** (`top_offers`): ofertas por `importePrimerAnio` (incluye todo: energía, potencia, fijos, impuestos). Pero **sin compensar excedentes** — es el coste del consumo neto.
3. **Simulación de excedentes** (`surplus_schemes`, sólo con `-sim`): `net_energy_eur` = término de energía anual **después** de compensar excedentes. **Sólo término de energía** (sin fijos: potencia, alquiler, impuestos — comunes a todas las ofertas y a añadir apart).

### Lógica de decisión

- El esquema con **menos `net_energy_eur`** es el mejor para el perfil del usuario.
- Para perfiles FV con **mucho excedente** (autoconsumo < 40%, cobertura < 50%), la
  **batería virtual** suele ganar porque valora los excedentes al precio de consumo y
  tiene techo anual (lleva saldo entre meses).
- Para perfiles con **poco excedente** o consumo nocturno dominante, la diferencia es menor.
- `lost_compensation_eur` alta = dinero "perdido" por el techo (sobre todo en verano
  con indexada mensual).

### Componer el coste total anual aproximado

```
coste_total ≈ importePrimerAnio (del ranking CNMC, ya con todos los fijos)
              − (net_energy_sin_FV − net_energy_con_esquema)
```
Donde "net_energy_sin_FV" es el término de energía sin excedentes (simulación con
producción 0). Los términos fijos (~potencia + alquiler + IE + IVA) ya vienen dentro
de `importePrimerAnio`; la simulación de excedentes sólo mueve el término de energía.

## Tokens y dependencias

- **e-sios**: sin `ESIOS_TOKEN` sólo se puede obtener **el último día** de precios
  (`source: "latest"`), que se usa como perfil horario representativo de todo el
  año. Para precisión (histórico de un año), pide al usuario un token gratuito en
  https://api.esios.ree.es y define `ESIOS_TOKEN`. Si la salida muestra
  `source: latest`, advierte que los absolutos son orientativos pero la jerarquía
  de esquemas suele mantenerse.
- **curl**: la descarga de e-sios se hace vía `curl` (e-sios bloquea por fingerprint
  TLS el cliente de Go). `curl` viene por defecto en macOS/Linux/Windows 10+.

## Limitaciones a comunicar al usuario

- El comparador CNMC **no contempla excedentes ni batería virtual**; sólo los modela
  esta simulación.
- Los términos fijos no se modelan esquema a esquema (son comunes); las cifras
  absolutas de la simulación son término de energía, no factura final.
- Los coeficientes/primas de cada comercializadora son aproximados y cambian; el
  registro (`solartrack/excedents.go` `SchemesRegistry`) es editable.

## Tests

```bash
go test ./... -skip _Live        # rápidos (sin red)
SOLARTRACK_SKIP_LIVE= go test ./...  # con integración (CNMC + PVGIS + e-sios)
```

## Errores frecuentes

- `CNMC HTTP 400`: los kWh deben ser enteros (la herramienta ya redondea; si aparece,
  revisa que no se inyecten decimales externos).
- `e-sios 403`: sin token y filtrando por geo. La herramienta ya evita el filtro geo;
  si persiste, hace falta `ESIOS_TOKEN`.
