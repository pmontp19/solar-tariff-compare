# Interpretar la salida y recomendar

Ejecuta siempre con `-json`. Estructura (`schema_version: 2`):

```jsonc
{
  "schema_version": 2,
  "consumption_summary": {
    "annual_kwh": 4313, "p1_kwh": 1447, "p2_kwh": 1155, "p3_kwh": 1711,
    "self_consumed_kwh": 0, "surplus_kwh": 1820,
    "rows": 8760, "holes": 12, "estimated_pct": 3.2,
    "first_date": "2025-07-01", "last_date": "2026-06-30"
  },
  "autoconsumption": { "production_kwh": ..., "self_consumed_kwh": ..., "surplus_kwh": ..., "coverage": ... },
  "num_offers": 92,
  "top_offers": [ { "comercializadora": ..., "oferta": ..., "importePrimerAnio": 780.12, ... } ],
  "suspect_offers": [ ... ],            // apartadas por importe no comparable (artefactos CNMC)
  "price_source": { "pvpc": "token", "surplus": "token" },  // o "latest" sin token
  "surplus_schemes": [ { "scheme": {...}, "net_energy_eur": ..., "used_compensation_eur": ..., "lost_compensation_eur": ... } ],
  "ranking_net": [ { "offer": {...}, "surplus_terms": ..., "surplus_credit_eur": ..., "annual_fee_eur": ..., "net_annual_eur": ... } ]
}
```

## 1. Valida la calidad de los datos (antes de nada)

Mira `consumption_summary`:
- `holes` alto (cientos+) o `estimated_pct` alto → curva incompleta o muy estimada; el
  resultado pierde fiabilidad. Dilo.
- `first_date`..`last_date` debe cubrir ~365 días. Un rango corto sesga P1/P2/P3 y los
  excedentes estacionales.
- `annual_kwh` fuera de ~2.000–6.000 kWh para una vivienda normal → sospecha de curva
  parcial (o de un perfil atípico; pregúntalo).
- `p1_kwh + p2_kwh + p3_kwh` debe cuadrar con `annual_kwh`.

## 2. El ranking principal

- **Con excedentes reales (hay placas)** → usa **`ranking_net`**. Ordena por
  `net_annual_eur = importePrimerAnio − surplus_credit_eur + annual_fee_eur`. Es el único
  ranking que compara ofertas contando *sus* excedentes con *su* esquema de compensación.
- **Sin placas** → usa **`top_offers`** (ordenado por `importePrimerAnio`, ya incluye
  energía + potencia + fijos + impuestos).

`top_offers` con placas es el coste *bruto* (sin compensar excedentes); no lo presentes
como "el más barato" si hay `ranking_net`.

## 3. Por qué gana un esquema (`surplus_schemes`, con -sim)

Contraste por arquetipo, no por oferta. Ordenados por `net_energy_eur` (menor = mejor).
Es **sólo término de energía**, sin fijos.

- `used_compensation_eur`: compensación aprovechada. `lost_compensation_eur`: la que se
  pierde por el techo (mensual en regulada/indexada; alta en verano con indexada).
- Regla práctica: con **mucho excedente** (autoconsumo < 40 %, cobertura < 50 %) la
  **batería virtual** suele ganar: valora el excedente al precio de consumo y arrastra
  saldo anual, así no pierde el sobrante de los meses de verano.
- Con poco excedente o consumo nocturno dominante, la diferencia entre esquemas es pequeña.

## 4. Componer el coste total anual (aprox.)

`surplus_schemes` mueve sólo el término de energía; los fijos (potencia, alquiler, IE, IVA)
ya están en `importePrimerAnio`. Si necesitas un total con un esquema concreto:

```
coste_total ≈ importePrimerAnio − (net_energy_sin_FV − net_energy_con_esquema)
```

donde `net_energy_sin_FV` es la simulación con producción 0. Para comparar ofertas
concretas, **prefiere `ranking_net`**, que ya hace esta atribución por oferta y evita
mezclar bases de precios.

## 5. Cómo presentarlo

Top 3 de `ranking_net`, en lenguaje llano:

```
1. <comercializadora> — <oferta>
   Coste neto: <net_annual_eur> €/año  (bruto <importePrimerAnio> − compensación <surplus_credit_eur> + cuota <annual_fee_eur>)
   Excedentes: <surplus_terms>
2. ...
3. ...
```

Explica el trade-off del 1º frente al 2º (p.ej. "la batería virtual de X compensa más pero
cobra 24 €/año de cuota; a partir de tus 1.800 kWh de excedente le sale a cuenta"). Cierra
con una recomendación clara y los caveats obligatorios (registro aproximado, verificar en
la web, `latest` = orientativo, no es la factura final).

## Errores y cómo reaccionar

- `CNMC HTTP 400`: consumo con decimales (la herramienta redondea; no debería pasar).
- `e-sios 403` o `price_source: latest` inesperado: falta `ESIOS_TOKEN` o `curl`.
- Sin `ranking_net` aunque haya placas: no se pudieron descargar precios e-sios; reporta el
  aviso de stderr, no inventes compensaciones.
