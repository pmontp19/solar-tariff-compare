# solar-tariff-compare

CLI en Go (binario único, agent-first) que compara ofertas de electricidad usando tu **curva de consumo horaria real** (CCH de e-distribución o CSV de Datadis) contra el API pública del **Comparador de la CNMC**, y modela la **producción FV** (PVGIS o CSV real) y los **excedentes** (e-sios) para simular esquemas de compensación (regulada, indexada, **batería virtual**) y producir un **ranking neto** por comercializadora.

Diseñada para que un agente (opencode, Claude Code, Cursor) pueda conducirla: la skill
[`.claude/skills/solar-tariff-compare`](.claude/skills/solar-tariff-compare/SKILL.md)
guía el flujo completo (recopilar datos del usuario → ejecutar → interpretar);
[`AGENTS.md`](AGENTS.md) es la guía de desarrollo del proyecto.

A diferencia del comparador web de la CNMC, esta herramienta:

- Lee directamente tu CSV de curva horaria (CCH de e-distribución o Datadis) y agrega el consumo en los períodos P1/P2/P3 de la tarifa **2.0TD**, teniendo en cuenta festivos nacionales y fines de semana.
- Estima la producción fotovoltaica con [PVGIS](https://re.jrc.ec.europa.eu/) (Comisión Europea) y calcula el autoconsumo hora a hora sobre tu consumo real.
- Muestra las ofertas ordenadas por `importePrimerAnio` (coste bruto del primer año).
- Además, produce un **ranking neto** que atribuye a cada oferta la compensación de excedentes que le aplica su comercializadora (registro editable en `solartrack/ranking.go`) y ordena por coste neto `importePrimerAnio − compensación + cuota`.

## ⚠️ Limitaciones

- **Autoconsumo**: modelado correctamente (la parte FV consumida in situ reduce la factura).
- **Excedentes** (compensación de lo que viertes a la red) y **batería virtual**: **NO** los contempla el API de la CNMC. Esta herramienta los modela aparte con precios de e-sios (véase "Simulación de excedentes" y "Ranking neto con excedentes" más abajo). Es una estimación del **término de energía** (sin fijos).
- La herramienta **no** elige por ti: te da los números para que tú (o un agente) toméis la decisión.

## Instalación

```bash
go install github.com/pmontp19/solar-tariff-compare/cmd/solar-tariff-compare@latest
```

O descarga un binario precompilado de [Releases](../../releases), o compílalo:

```bash
git clone https://github.com/pmontp19/solar-tariff-compare
cd solar-tariff-compare
go build -o solar-tariff-compare ./cmd/solar-tariff-compare
```

## Uso

### Comparativa sólo con consumo real

```bash
solar-tariff-compare -consum curva.csv -cp 8001 -top 15
```

### Con estimación FV (PVGIS) + simulación de excedentes (la decisión clave)

```bash
solar-tariff-compare \
  -consum curva.csv -cp 8001 \
  -kwp 4.1 -lat 41.38 -lon 2.17 -angle 35 -aspect 0 \
  -sim -sin-solar -top 15
```

### Con producción FV real (CSV de 7 columnas, más preciso que PVGIS)

```bash
solar-tariff-compare -consum consumo.csv -prod produccion.csv -cp 8001 -sim
```

### Salida JSON

```bash
solar-tariff-compare -consum curva.csv -cp 8001 -kwp 4.1 -json > resultado.json
```

El JSON (`schema_version: 2`) incluye todo lo que un agente necesita sin leer stderr:
`consumption_summary` (kWh anuales y por período, filas, huecos, % estimado, rango de
fechas, excedentes reales), `top_offers`, `suspect_offers` (ofertas apartadas por
importe no comparable), `price_source` (si los precios e-sios son histórico `token` o
un día representativo `latest`), `surplus_schemes` (con `-sim`) y `ranking_net`.

## Flags

| Flag | Por defecto | Descripción |
|---|---|---|
| `-consum` | — | CSV de la curva horaria (CCH e-distribución o Datadis). **Obligatorio** |
| `-cp` | — | Código postal (acepta `08001` y `8001`). **Obligatorio** |
| `-potencia` | `3.45` | Potencia contratada en kW (2.0TD) |
| `-top` | `20` | Número de ofertas a mostrar |
| `-kwp` | `0` | Potencia FV instalada (pico) en kW. Si >0 activa PVGIS y se envía como `potenciaAutoconsumo` a la CNMC. Si la curva ya trae excedentes reales, PVGIS se omite (los datos reales mandan) y `-kwp` sólo informa la potencia instalada |
| `-prod` | — | CSV de producción real (7 columnas con `AS_KWh`/`AE_AUTOCONS_kWh`). Sustituye a PVGIS |
| `-lat`, `-lon` | `41.38`, `2.17` | Coordenadas para PVGIS |
| `-angle` | `35` | Inclinación de los paneles (grados) |
| `-aspect` | `0` | Orientación (`0`=sur, `-90`=este, `90`=oeste) |
| `-loss` | `14` | Pérdidas del sistema FV (%) |
| `-sim` | `false` | Con FV: simula esquemas de excedentes (regulada/indexada/batería virtual) con e-sios |
| `-sin-solar` | `false` | Con FV: muestra también la comparativa sin FV |
| `-json` | `false` | Salida en JSON (para agentes) |

## Simulación de excedentes (`-sim`)

Con FV y `-sim`, la herramienta descarga los precios horarios de **e-sios** (Red Eléctrica):

- **Indicador 1001**: PVPC 2.0TD horario (precio de consumo).
- **Indicador 1739**: precio regulado de excedentes de autoconsumo horario.

y simula la factura de energía hora a hora para tres esquemas:

| Esquema | Precio excedentes | Techo | Ejemplo |
|---|---|---|---|
| **Regulada** | precio excedentes (1739) | mensual | compensación simplificada por defecto; Som Energia (0,03 €/kWh fijo) |
| **Indexada** | precio excedentes × (1+coef) + prima, o fijo pactado | mensual | TotalEnergies (0,07), Nabalia (0,095) |
| **Batería virtual / wallet** | **precio de consumo** (PVPC) o fijo, con saldo | **anual** (lleva saldo) | Holaluz Cloud, Naturgy, Repsol Vivit, Octopus Solar Wallet |

Los precios horarios con valor **0 son reales** (el indicador 1739 vale 0 en muchas
horas solares desde 2024) y se respetan; el perfil medio sólo sustituye horas sin dato.
Un precio negativo nunca genera compensación negativa (se recorta a 0).

**Salida**: término de energía neto anual por esquema (`net_energy_eur`), compensación usada y perdida (por la regla no-negativo). Sin términos fijos (comunes a todas las ofertas).

## Ranking neto con excedentes (atribuido por comercializadora)

Más allá de la simulación genérica por esquema, la herramienta produce un **ranking neto** que atribuye a cada oferta de la CNMC la compensación de excedentes según el `RetailerRegistry` de `solartrack/ranking.go` (editable; precios y condiciones revisados en julio 2026). Cada entrada captura:

- `Price`: €/kWh fijo (o 0 si usa precio horario / precio de consumo).
- `CeilingAnnual`: `true` para baterías virtuales y wallets (arrastra saldo entre meses y compensa la factura completa); `false` para compensación simplificada regulada.
- `MonthlyFee`: cuota mensual (Repsol 1,99 €, Endesa 2 €, …).
- `ExpiryMonths`: caducidad del saldo (Naturgy 60, Iberdrola 24). METADATA informativa; no recorta el número del primer año.
- `ThrottleFraction`: tope tipo Repsol (sólo compensa a tarifa plena hasta el 40 % del consumo anual).

Si una oferta no encaja con ninguna entrada del registro, se aplica `DefaultSurplusTerms` (compensación simplificada regulada).

**Salida**: ranking ordenado por coste neto `importePrimerAnio − compensación + cuota`, con la compensación aplicada y la cuota anual. En JSON bajo la clave `ranking_net`.

### `ESIOS_TOKEN` y `curl`

- La descarga de e-sios se hace vía **`curl`** (e-sios bloquea por fingerprint TLS el cliente HTTP de Go). `curl` viene por defecto en macOS/Linux/Windows 10+.
- Sin `ESIOS_TOKEN` sólo se puede obtener **el último día** de precios (`source: latest`), usado como perfil representativo del año — orientativo. Para una simulación precisa con histórico de un año, registra un token gratuito en https://api.esios.ree.es y define `ESIOS_TOKEN`.

## Formato del CSV (CCH o Datadis)

### CCH de e-distribución

El formato estándar que puedes descargar de tu oficina virtual de la distribuidora (e-distribución, etc.):

```
CUPS;Fecha;Hora;AE_kWh;REAL/ESTIMADO
ES0031400000000000TF0F;15/01/2025;1;0,168;R
ES0031400000000000TF0F;15/01/2025;2;0,182;R
...
```

- `Fecha` en formato `DD/MM/YYYY`
- `Hora` de `1` a `24` (1 = intervalo 00:00–01:00); el día del cambio horario de octubre llega a `25`
- `AE_kWh` con coma decimal española (`0,168`)

También acepta la variante de 7 columnas con excedentes (`AS_KWh`) y autoconsumo (`AE_AUTOCONS_kWh`), que aparecen una vez tienes placas.

### Datadis

La herramienta **auto-detecta** el formato del CSV de [Datadis](https://datadis.es) (API `get-consumption-data`, `measurementType=0`):

```
cups,date,time,consumptionKWh,obtainMethod,surplusEnergyKWh,generationEnergyKWh,selfConsumptionEnergyKWh
ES0031000000000000XX0X,2025/07/01,01:00,0.317,Real,0.0,,
ES0031000000000000XX0X,2025/07/01,12:00,0.011,Real,2.092,,
...
```

- `date` = `YYYY/MM/DD`; `time` = `01:00`..`24:00` (hora al final del intervalo; `25:00` el día del cambio horario de octubre).
- `consumptionKWh` ya es **neto de red** y `surplusEnergyKWh` son los excedentes reales. Con Datadis, `energiaAutoconsumo=0` en la consulta CNMC (la factura ya es neta).
- Las columnas `generationEnergyKWh` y `selfConsumptionEnergyKWh` suelen venir vacías (Datadis no las publica).

## Cómo funciona

1. **Parsea** la curva horaria (auto-detecta Datadis vs CCH) y clasifica cada hora en P1 (Punta), P2 (Llano) o P3 (Valle) según la Circular 3/2020 de la CNMC, contando los festivos nacionales **no sustituibles** (Año Nuevo, Viernes Santo calculado por Pascua, 1 de mayo, Asunción, 12 de octubre, Todos los Santos, Constitución, Inmaculada, Navidad — Reyes es sustituible y no cuenta).
2. **Si hay FV**, descarga la serie horaria de PVGIS y calcula, hora a hora, el autoconsumo `min(producción, consumo)` y los excedentes.
3. **Consulta el API pública de la CNMC** (`/api/publico/ofertas/electricidad`) con el consumo agregado y el autoconsumo. Aparta las ofertas "artefacto" de la CNMC (p. ej. "PVPC Histórico de referencia") cuyo importe no escala con el consumo vía `PartitionSuspectOffers`.
4. **Ordena** las ofertas por importe del primer año.
5. **Con `-sim`**: descarga precios horarios de e-sios y simula los esquemas de excedentes (regulada, indexada, batería virtual) sobre el término de energía.
6. **Ranking neto**: atribuye a cada oferta la compensación de sus excedentes (según el registro por comercializadora) y ordena por coste neto anual.

Véase [`docs/CNMC-API.md`](docs/CNMC-API.md) para el reverse engineering del API y [`docs/REVIEW-autoconsum.md`](docs/REVIEW-autoconsum.md) para el razonamiento del modelo de excedentes.

## Desarrollo

```bash
go test ./... -skip _Live               # tests rápidos (sin red)
SOLARTRACK_SKIP_LIVE=1 go test ./...    # equivalente, vía variable de entorno
go test ./...                           # TODOS, incluidos integración (red: CNMC + PVGIS + e-sios)
```

Ojo: los tests de integración (`*_Live`) se ejecutan **por defecto**; hay que
saltarlos explícitamente con una de las dos primeras formas.

## Licencia

MIT. Véase `LICENSE`.

## Agradecimientos

Datos de ofertas: [CNMC — Comparador de Ofertas de Energía](https://comparador.cnmc.gob.es/).
Estimación FV: [PVGIS](https://re.jrc.ec.europa.eu/pvg_tools/en/) de la Comisión Europea.
Este proyecto no está afiliado a la CNMC ni a la Comisión Europea.
