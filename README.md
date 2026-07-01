# solar-tariff-compare

CLI en Go (binario único, agent-first) que compara ofertas de electricidad usando tu **curva de consumo horaria real** (CCH de e-distribución) contra el API pública del **Comparador de la CNMC**, y modela la **producción FV** (PVGIS o CSV real) y los **excedentes** (e-sios) para simular esquemas de compensación (regulada, indexada, **batería virtual**).

Diseñada para que un agente (opencode, Claude Code, Cursor) pueda conducirla: ver [`AGENTS.md`](AGENTS.md).

A diferencia del comparador web de la CNMC, esta herramienta:

- Lee directamente tu CSV de curva horaria (CCH) y agrega el consumo en los períodos P1/P2/P3 de la tarifa **2.0TD**, teniendo en cuenta festivos nacionales y fines de semana.
- Estima la producción fotovoltaica con [PVGIS](https://re.jrc.ec.europa.eu/) (Comisión Europea) y calcula el autoconsumo hora a hora sobre tu consumo real.
- Muestra las ofertas ordenadas de más barata a más cara, con comparativa con/sin FV.

## ⚠️ Limitaciones

- **Autoconsumo**: modelado correctamente (la parte FV consumida in situ reduce la factura).
- **Excedentes** (compensación de lo que viertes a la red) y **batería virtual**: **NO** los contempla el API de la CNMC. Esta herramienta los modela aparte con precios de e-sios (véase "Simulación de excedentes" más abajo). Es una estimación del **término de energía** (sin fijos).
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

## Flags

| Flag | Por defecto | Descripción |
|---|---|---|
| `-consum` | — | CSV de la curva horaria (CCH e-distribución). **Obligatorio** |
| `-cp` | — | Código postal sin el 0 inicial (`08001` → `8001`). **Obligatorio** |
| `-potencia` | `3.45` | Potencia contratada en kW (2.0TD) |
| `-top` | `20` | Número de ofertas a mostrar |
| `-kwp` | `0` | Potencia FV de pico en kW. Si >0 activa PVGIS |
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
| **Regulada** | precio excedentes (1739) | mensual | compensación simplificada por defecto |
| **Indexada** | precio excedentes × (1+coef) + prima | mensual | Octopus, Som |
| **Batería virtual** | **precio de consumo** (PVPC) | **anual** (lleva saldo) | Holaluz Sun, Repsol, Núcleo |

**Salida**: término de energía neto anual por esquema (`net_energy_eur`), compensación usada y perdida (por la regla no-negativo). Sin términos fijos (comunes a todas las ofertas).

### `ESIOS_TOKEN` y `curl`

- La descarga de e-sios se hace vía **`curl`** (e-sios bloquea por fingerprint TLS el cliente HTTP de Go). `curl` viene por defecto en macOS/Linux/Windows 10+.
- Sin `ESIOS_TOKEN` sólo se puede obtener **el último día** de precios (`source: latest`), usado como perfil representativo del año — orientativo. Para una simulación precisa con histórico de un año, registra un token gratuito en https://api.esios.ree.es y define `ESIOS_TOKEN`.

## Formato del CSV (CCH)

El formato estándar que puedes descargar de tu oficina virtual de la distribuidora (e-distribución, etc.):

```
CUPS;Fecha;Hora;AE_kWh;REAL/ESTIMADO
ES0031400000000000TF0F;15/01/2025;1;0,168;R
ES0031400000000000TF0F;15/01/2025;2;0,182;R
...
```

- `Fecha` en formato `DD/MM/YYYY`
- `Hora` de `1` a `24` (1 = intervalo 00:00–01:00)
- `AE_kWh` con coma decimal española (`0,168`)

También acepta la variante de 7 columnas con excedentes (`AS_KWh`) y autoconsumo (`AE_AUTOCONS_kWh`), que aparecen una vez tienes placas.

## Cómo funciona

1. **Parsea** la curva horaria y clasifica cada hora en P1 (Punta), P2 (Llano) o P3 (Valle) según el [RD 1484/2021](https://www.boe.es/), incluyendo festivos nacionales (Año Nuevo, Reyes, Viernes Santo calculado por Pascua, 1 de mayo, 12 de octubre, Todos los Santos, Constitución, Inmaculada, Navidad).
2. **Si hay FV**, descarga la serie horaria de PVGIS y calcula, hora a hora, el autoconsumo `min(producción, consumo)` y los excedentes.
3. **Consulta el API pública de la CNMC** (`/api/publico/ofertas/electricidad`) con el consumo agregado y el autoconsumo.
4. **Ordena** las ofertas por importe del primer año.

Véase [`docs/CNMC-API.md`](docs/CNMC-API.md) para el reverse engineering del API.

## Desarrollo

```bash
go test ./...                      # tests (saltando los de integración)
SOLARTRACK_SKIP_LIVE= go test ./...  # tests de integración (llamada real a CNMC + PVGIS)
```

## Licencia

MIT. Véase `LICENSE`.

## Agradecimientos

Datos de ofertas: [CNMC — Comparador de Ofertas de Energía](https://comparador.cnmc.gob.es/).
Estimación FV: [PVGIS](https://re.jrc.ec.europa.eu/pvg_tools/en/) de la Comisión Europea.
Este proyecto no está afiliado a la CNMC ni a la Comisión Europea.
