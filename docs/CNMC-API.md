# Comparador CNMC — Reverse Engineering del API

Documentación de cómo funciona el API pública del comparador de ofertas de
electricidad de la CNMC, que usa esta herramienta. Obtenida por observación del
tráfico de `https://comparador.cnmc.gob.es/` (mayo 2026, versión de la app v2.1.3).

## Cómo funciona el comparador

- App **Nuxt.js SPA** en `https://comparador.cnmc.gob.es/`.
- Flujo: home → formulario → `/comparador/listado/{TOKEN}` (resultados) → `/comparador/detalle/{TOKEN}` (oferta).
- El estado del formulario se codifica en un **token hex cifrado (AES-GCM, ~232 bytes)** en la URL. No hace falta descifrarlo: el API REST es directo.
- Todo el cálculo de precios es **server-side**.

## El API (pública, sin auth, sin cookies)

### Endpoint principal — ofertas

```
GET https://comparador.cnmc.gob.es/api/publico/ofertas/electricidad
```

Devuelve `~110 ofertas` con `importePrimerAnio`, `comercializadora`, `oferta`,
`tipo` (fijo/indexado), `tipoRevision`, `verde`, `penalizacion`, `autoconsumo`, etc.

⚠️ **Los kWh de consumo deben ser enteros** (valores con decimales → HTTP 400).
La potencia sí admite decimales.

### Parámetros clave (query string, GET)

| Parámetro | Ejemplo | Significado |
|---|---|---|
| `tipoSuministro` | `E` | Electricidad |
| `codigoPostal` | `8001` | CP (sin el 0 inicial) |
| `potencia` / `potenciaPrimeraFranja`…`SextaFranja` | `3.45` | kW contratados por período (2.0TD → P1, P2, P3) |
| `consumoAnualE` | `4313` | kWh del año (o del período, si mensual) |
| `consumoAnualEOrig` | `4313` | kWh anuales originales |
| `consumoPrimeraFranja`/`Segunda`/`Tercera` | `1447,1155,1711` | kWh por P1/P2/P3 |
| `tarifa` | `4` | Peaje de acceso (4 = 2.0TD) |
| **`autoconsumo`** | `true`/`false` | Marca que hay autoconsumo FV |
| **`energiaAutoconsumo`** | `600` | **kWh autoconsumidos (energía FV consumida in situ)** |
| `potenciaAutoconsumo` | `3.45` | kW de la instalación FV |
| `revisionPrecios` | `2` | Filtro revisión de precios |
| `serviciosAdicionales` | `2` | Filtro servicios adicionales |
| `permanencia` | `2` | Filtro permanencia |
| `perfilConsumo` | `13` | Perfil (13 = Estándar 2.0TD) |
| `dateInicio` / `dateFin` | `1704067200000` | Epoch ms (período de la consulta) |
| `*Qr`, `*Orig`, `tc`, `bs`, `exc`, `reg`, `imp*`, `pr*`, `cf*`, … | `0` | Campos auxiliares |

⚠️ Hay que incluir **todos** los campos auxiliares (`*Qr`, `*Orig`, `imp*`, etc.) a `0`,
o el API devuelve HTTP 500.

### Otros endpoints útiles

- `GET /api/publico/preciosPVPC/ultimaFechaConTodo` → última fecha con precios PVPC.
- `GET /api/publico/listadoPerfiles` → perfiles de consumo disponibles.
- `GET /api/publico/logo/{id}` → logo de una comercializadora.

## Limitaciones para el caso de uso solar

- ✅ **Autoconsumo** (energía que consumes de tus placas): modelado vía
  `energiaAutoconsumo`. Reduce la factura correctamente.
- ❌ **Excedentes** (energía que viertes a la red y te compensan): **NO** está en el
  comparador de la CNMC. Es específico de cada comercializadora.
- ❌ **Batería virtual**: tampoco. Es un producto comercial propio de cada marca.

Esta herramienta modela excedentes y batería virtual aparte con precios de **e-sios**
(indicadores 1001 y 1739); véase el README, sección "Simulación de excedentes".
