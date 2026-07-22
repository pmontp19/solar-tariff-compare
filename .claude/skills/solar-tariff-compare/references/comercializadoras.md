# Registro de comercializadoras y control de vigencia

La CNMC devuelve `importePrimerAnio` pero **no** los términos de excedentes. Esos viven en
`RetailerRegistry` (fichero `solartrack/ranking.go` del repo), atribuidos por nombre de
comercializadora. Es la pieza que puede quedar desactualizada.

## Control de frescura (hazlo siempre)

El registro se revisó en **julio de 2026**. Compara con la fecha de hoy:

- **< 6 meses**: úsalo con normalidad, recordando que hay que verificar en la web.
- **>= 6 meses**: **avisa expresamente** al usuario de que los precios y condiciones de
  excedentes pueden haber cambiado, y de que verifique en la web de cada compañía antes de
  contratar. Ofrece actualizar el registro si te da fuentes.

## Registro vigente (julio 2026)

Precios de compensación de excedentes por comercializadora. Confianza: A=alta, M=media.

| Comercializadora | Esquema | €/kWh | Techo | Cuota | Nota | Conf. |
|---|---|---|---|---|---|---|
| Octopus (Solar Wallet) | wallet € | 0,04 (0,07 si instala Octopus) | factura entera | — | monedero sin caducidad, compensa potencia | M-A |
| Holaluz (Cloud) | batería virtual | ~precio consumo | factura entera | — | cuota anual personalizada | M |
| Naturgy (Batería Virtual) | batería virtual | 0,06 | factura entera | gratis (opt-in) | saldo caduca a 5 años | A |
| TotalEnergies (Siempre Solar) | simplificada fija | 0,07 | mensual (energía) | — | nunca compensa fijos | A |
| Repsol (Vivit Batería Virtual) | batería virtual | 0,06 | luz+gas | **1,99 €/mes** | **tope 40 %** del consumo anual | M-A |
| Nabalia (Solar Flex) | fijo 12 meses | 0,095 | mensual (energía) | — | permanencia 12m | M |
| Endesa (Solar Plus + BV) | batería virtual | 0,06 | factura | **2 €/mes** | — | M-A |
| Iberdrola (Solar Cloud) | batería virtual | 0,06 | factura | gratis | saldo 24 meses | M |
| Gana Energía (Monedero) | wallet € | 0,06 | factura | gratis 12m (~2,1 €/mes luego) | 1r año sin cuota | M-A |
| Som Energia | simplificada cooperativa | 0,03 fijo (2.0TD, desde 1 may 2026) | mensual | — | también Generation kWh (no modelado) | M |

Comercializadoras sin cifras fiables (p.ej. Plenitude, Eleia) **no** están en el registro:
caen en `DefaultSurplusTerms` (compensación simplificada regulada) en vez de inventar un
número. Si el ranking neto muestra "Compensación simplificada (regulada)" en `surplus_terms`,
es que esa oferta no tiene términos específicos codificados.

## Matices que cambian el ranking para perfiles con muchos excedentes

- **Cuota mensual** (`annual_fee_eur` = 12 × cuota): Repsol y Endesa cobran; puede comerse
  la ventaja de compensar mejor si el excedente no es grande.
- **Tope tipo Repsol** (`ThrottleFraction` 0,40): sólo compensa a tarifa plena hasta el 40 %
  del consumo anual. Penaliza justo a los perfiles con mucha exportación, que son el usuario
  objetivo de esta herramienta. Tenlo presente al recomendar Repsol.
- **Caducidad del saldo** (`ExpiryMonths`): metadata informativa. NO recorta el número del
  primer año (una caducidad >= 12 meses no llega a morder el año 1), pero avisa del riesgo a
  partir del año 2 si el usuario acumularía mucho saldo sin gastarlo (Naturgy 5 años,
  Iberdrola 24 meses).

## Cómo actualizar el registro

Es código, no un JSON externo (aún). Para cambiar un precio: edita `RetailerRegistry` en
`solartrack/ranking.go`, ajusta el test `TestRetailerRegistry_FeeExpiryThrottle` si cambian
cuotas/topes/caducidades, y `go test ./... -skip _Live`. Fuentes primarias: la web oficial
de cada compañía (sección autoconsumo/excedentes/batería virtual). Documenta el cambio en
`docs/REVIEW-autoconsum.md` con fecha y fuente.
