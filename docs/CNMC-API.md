# Comparador CNMC — Reverse Engineering de l'API

Documentació de com funciona l'API pública del comparador d'ofertes d'electricitat
de la CNMC, que aquesta eina fa servir. Obtinguda per observació del tràfic de
`https://comparador.cnmc.gob.es/` (maig 2026, versió de l'app v2.1.3).

## Com funciona el comparador

- App **Nuxt.js SPA** a `https://comparador.cnmc.gob.es/`.
- Flux: home → formulari → `/comparador/listado/{TOKEN}` (resultats) → `/comparador/detalle/{TOKEN}` (oferta).
- L'estat del formulari es codifica en un **token hex xifrat (AES-GCM, ~232 bytes)** a la URL. No cal desxifrar-lo: l'API REST és directa.
- Tot el càlcul de preus és **server-side**.

## L'API (pública, sense auth, sense cookies)

### Endpoint principal — ofertes

```
GET https://comparador.cnmc.gob.es/api/publico/ofertas/electricidad
```

Retorna `~110 ofertes` amb `importePrimerAnio`, `comercializadora`, `oferta`,
`tipo` (fixe/indexat), `tipoRevision`, `verde`, `penalizacion`, `autoconsumo`, etc.

⚠️ **Els kWh de consum han de ser enters** (valors amb decimals → HTTP 400).
La potència sí admet decimals.

### Paràmetres clau (query string, GET)

| Paràmetre | Exemple | Significat |
|---|---|---|
| `tipoSuministro` | `E` | Electricitat |
| `codigoPostal` | `8001` | CP (sense el 0 inicial) |
| `potencia` / `potenciaPrimeraFranja`…`SextaFranja` | `3.45` | kW contractats per període (2.0TD → P1, P2, P3) |
| `consumoAnualE` | `4313` | kWh de l'any (o del període, si mensual) |
| `consumoAnualEOrig` | `4313` | kWh any originals |
| `consumoPrimeraFranja`/`Segunda`/`Tercera` | `1447,1155,1711` | kWh per P1/P2/P3 |
| `tarifa` | `4` | Peatge d'accés (4 = 2.0TD) |
| **`autoconsumo`** | `true`/`false` | Marca que hi ha autoconsum FV |
| **`energiaAutoconsumo`** | `600` | **kWh autoconsumits (energia FV consumida in situ)** |
| `potenciaAutoconsumo` | `3.45` | kW de la instal·lació FV |
| `revisionPrecios` | `2` | Filtre revisió de preus |
| `serviciosAdicionales` | `2` | Filtre serveis addicionals |
| `permanencia` | `2` | Filtre permanència |
| `perfilConsumo` | `13` | Perfil (13 = Estàndard 2.0TD) |
| `dateInicio` / `dateFin` | `1704067200000` | Epoch ms (període de la consulta) |
| `*Qr`, `*Orig`, `tc`, `bs`, `exc`, `reg`, `imp*`, `pr*`, `cf*`, … | `0` | Camps auxiliars |

⚠️ Cal incloure **tots** els camps auxiliars (`*Qr`, `*Orig`, `imp*`, etc.) a `0`,
o l'API retorna HTTP 500.

### Altres endpoints útils

- `GET /api/publico/preciosPVPC/ultimaFechaConTodo` → darrera data amb preus PVPC.
- `GET /api/publico/listadoPerfiles` → perfils de consum disponibles.
- `GET /api/publico/logo/{id}` → logo d'una comercialitzadora.

## Limitacions per al cas d'ús solar

- ✅ **Autoconsum** (energia que consumeixes de les teves plaques): modelat via
  `energiaAutoconsumo`. Redueix la factura correctament.
- ❌ **Excedents** (energia que bolques a la xarxa i et compensen): **NO** està al
  comparador de la CNMC. És específic de cada comercialitzadora.
- ❌ **Bateria virtual**: tampoc. És un producte comercial propi de cada marca.

Aquesta eina modela excedents i bateria virtual a part amb preus d'**e-sios**
(indicadors 1001 i 1739); vegeu el README, secció "Simulació d'excedents".
