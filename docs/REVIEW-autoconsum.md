# Revisió: tractament de l'autoconsum i els excedents

Revisió centrada en per què la comparativa "no té gaire en compte el factor autoconsum" i com integrar-ho amb dades reals (Datadis / inversor Huawei). Feta sobre `solartrack/{excedents,production,consumption,cnmc}.go`, README i AGENTS.md.

## El que ja està bé

- Separació neta de mòduls; API de la CNMC ben documentada i estable.
- Classificació P1/P2/P3 amb festius i caps de setmana.
- Tres esquemes d'excedents (regulada / indexada / bateria virtual) amb sostre mensual vs anual — el concepte és correcte.
- Suport de CCH de 5 i 7 columnes (aquesta última ja porta `AS_KWh` i `AE_AUTOCONS_kWh`).

## Problemes, per prioritat

### 1. Dues bases de preus disjuntes → total fràgil (el problema de fons)

El rànquing de la CNMC (`importePrimerAnio`) usa els preus reals de cada oferta (amb fixos i impostos) **però ignora els excedents**. La simulació d'excedents, en canvi, valora tot amb **PVPC** com a referència comuna, no amb el preu de cada oferta. Compondre'ls amb la fórmula delta d'AGENTS.md barreja bases: el terme d'energia es tarifa dues vegades amb preus diferents.

Conseqüència: el benefici dels excedents **no s'atribueix a l'oferta concreta**, així que el rànquing no pot dir "quina oferta és millor un cop compto ELS MEUS excedents". Per a un perfil amb molta exportació (com aquest cas real: 4.050 kWh/any abocats), aquest és precisament el factor que capgira el rànquing.

**Proposta:** calcular una única factura anual per oferta = (energia + fixos de la CNMC) − compensació d'excedents valorada amb l'esquema d'aquella oferta. Que el rànquing integri els excedents, no com a bloc separat.

### 2. La bateria virtual es valora a PVPC, no al preu de consum de l'oferta

Una bateria virtual real valora l'excedent al **preu de consum de la teva tarifa** (o un preu fix), no al PVPC. Segons l'oferta, PVPC sobre- o infravalora. Com que la CNMC no exposa els preus unitaris per període, cal aproximar-ho: derivar un €/kWh efectiu de l'oferta, o —millor— ampliar el `SchemesRegistry` amb els **termes reals d'excedent de cada comercialitzadora** (preu €/kWh i tipus de sostre). Ara el registre és genèric i algunes entrades són placeholders.

### 3. Semàntica de l'autoconsum vs realitat de Datadis (clau per a tu)

L'app calcula l'autoconsum com `min(producció, consum)` a partir de PVGIS o d'un CCH de 7 columnes. Però **Datadis només dona el consum net de xarxa (import) i els excedents reals**; les columnes de producció i autoconsum vénen **buides**. Implicacions:

- Amb només Datadis **no es pot obtenir l'autoconsum real** — cal la producció del Huawei per reconstruir el consum brut (`brut = import_xarxa + autoconsum`) i l'autoconsum (`min(producció, brut)`).
- Si alimentes l'`import net` com a `consumo` **i** a més poses `energiaAutoconsumo`, arrisques una inconsistència: l'import de xarxa ja és net de l'autoconsum, i tornaries a restar. Cal decidir la convenció i documentar-la.

**Proposta:** afegir un camí d'ingesta de Datadis i deixar explícit que, amb només Datadis, `consumo` = import de xarxa (ja net) i `energiaAutoconsumo = 0` per a la CNMC (la factura ja és neta); els excedents es tracten per la simulació d'esquemes. La producció del Huawei s'usa només per a escenaris "què passaria si" (desplaçar càrrega, dimensionar), no per a la factura base.

### 4. Prioritzar excedents reals sobre estimats

Ara tens excedents **reals** hora a hora. PVGIS és un any tipus (TMY) i desviarà. Sempre que hi hagi corba real d'excedents (Datadis o CCH 7-col), s'ha d'usar aquesta i no PVGIS.

### 5. Model de "bateria virtual" massa restrictiu per a wallets reals

L'esquema `SchemeVirtualBattery` posa sostre a l'**energia anual**. Però productes tipus **Octopus Solar Wallet** acumulen **euros** que descompten del **total** de la factura (inclosa potència i fixos) i **no caduquen**. Per a perfils amb molta exportació, el valor anual de l'excedent pot acostar-se o superar el terme d'energia, i el model actual el retallaria abans d'hora. Convé modelar el wallet com un **saldo en euros** que compensa el total i s'arrossega, no com un topall sobre l'energia.

> Nota validada amb factura real: a Octopus, l'esquema del registre està etiquetat com "indexada (excedents al preu d'excedents)", però Octopus és de fet Solar Wallet acumulatiu. Cal corregir l'etiqueta i el model.

### 6. Absoluts orientatius sense `ESIOS_TOKEN`

Ja està ben advertit a AGENTS.md: sense token, e-sios només dona l'últim dia i els absoluts són orientatius (encara que la jerarquia d'esquemes se sol mantenir). Reforçar que per a xifres absolutes cal el token i un any d'històric.

## Pla proposat (CNMC + webs concretes)

1. **Ingesta Datadis** (nou parser): consum net + excedents reals → evita PVGIS quan hi ha dades reals.
2. **Factura per oferta integrada**: una sola passada que resti els excedents a cada oferta segons el seu esquema real (registre ampliat).
3. **Registre real d'excedents**: codificar a mà els termes de les ~10-15 comercialitzadores rellevants (preu €/kWh, sostre, quota). Més fiable i legal que scrapejar; l'scraping de webs concretes queda per a camps que la CNMC no dona (preu d'excedent), executat localment.
4. **Wallet com a saldo en euros** (esquema nou) que compensa el total i acumula.
5. Recordatori d'entorn: qualsevol descàrrega (CNMC, e-sios, scraping) s'executa **a la teva màquina**; la xarxa del sandbox bloqueja llocs externs.

## Registre d'excedents revisat (juliol 2026)

Termes recopilats de webs oficials + comparadors (data de consulta 2026-07-12) i codificats a
`solartrack/ranking.go` (`RetailerRegistry`). El model actual encara **no** té un `SchemeType`
de "monedero en euros": els wallets s'aproximen amb `SchemeVirtualBattery + CeilingAnnual`, que
posa el sostre sobre `ImportePrimerAnio` de la CNMC (energia + potència + impostos) → funciona com
a tope a la factura completa. Confiança: A=alta, M=mitjana, B=baixa.

| Comercialitzadora | Esquema | €/kWh | Sostre | Caducitat | Quota | Font | Conf. |
|---|---|---|---|---|---|---|---|
| Octopus (Solar Wallet) | monedero € (VB+anual) | 0,04 (0,07 si instal·la Octopus) | factura sencera | sense | gratis | [octopusenergy.es/solar-wallet](https://octopusenergy.es/solar-wallet), tarifasgasluz, papernest | M-A |
| Holaluz (Cloud) | bateria virtual (quota anual ~ preu consum) | quota personalitzada | factura sencera | sense (excedent anual es paga en efectiu) | gratis | [holaluz.com/virtual-battery](https://www.holaluz.com/en/solar-panels/virtual-battery) | M |
| Naturgy (Batería Virtual, opt-in) | bateria virtual | 0,06 | factura sencera | **5 anys** | gratis | [naturgy.es/bateria_virtual](https://www.naturgy.es/hogar/solar/bateria_virtual) | A |
| TotalEnergies (Siempre Solar) | simplificada fixa | 0,07 | mensual (energia) | — | — | [totalenergies.es](https://www.totalenergies.es/es/hogares/autoconsumo-solar/compensacion-excedentes) | A |
| Repsol (Vivit Batería Virtual) | bateria virtual | 0,06 | llum+gas | sense | **1,99 €/mes** + tope 40% consum anual | [repsol.es/…/tarifa-bateria-virtual](https://www.repsol.es/particulares/hogar/energia-solar/tarifas/tarifa-bateria-virtual/) | M-A |
| Nabalia (Solar Flex) | preu fix 12 mesos | 0,095 | mensual (energia) | — | permanència 12m | roams.es, comparadors | M |
| Endesa (Solar Plus + BV) | bateria virtual | 0,06 | factura | — | **2 €/mes** | [endesa.com](https://www.endesa.com/es/luz-y-gas/catalogo-solar/endesa-solar-plus-bateria-virtual) | M-A |
| Iberdrola (Solar Cloud) | bateria virtual | 0,06 | factura (~1.000 kWh/mes) | **24 mesos** | gratis | tarifasgasluz | M |
| Gana Energía (Monedero) | monedero € | 0,06 | factura | sense (es paga si marxes) | gratis 12m, després ~2,1 €/mes | [ganaenergia.com](https://ganaenergia.com/blog/compensacion-excedentes-gana-energia/) | M-A |
| Som Energia | simplificada cooperativa (sense marge) | ~preu mercat h. solars | mensual | — | — | [somenergia.coop](https://www.somenergia.coop/) | M (placeholder) |

**Correccions clau vs. el registre inicial:**
- **Octopus**: era 0,03 €/kWh amb tope → **incorrecte**. És un monedero en euros sense caducitat que
  compensa la potència; preu real 0,04 (o 0,07 si instal·la Octopus). *Nota: el contracte ACTUAL de
  l'usuari és 0,03 €/kWh (tarifa antiga), infravalorat vs. el mercat actual.*
- **Repsol**: el comportament de bateria virtual requereix **1,99 €/mes** i té un **tope del 40%** del
  consum anual que penalitza justament els perfils amb molts excedents (l'usuari objectiu).
- **Nabalia**: 0,095 €/kWh és **fix** 12 mesos (Solar Flex), no indexat per hora.
- **Naturgy**: 0,06 €/kWh confirmat, però només és wallet amb l'add-on opt-in (caduca a 5 anys).
- **Plenitude i Eleia**: sense xifres fiables per comercialitzadora → cauen en `DefaultSurplusTerms`.

**El que el model encara NO captura** (mesura futura → esquema "monedero €" propi, pas 5 del HANDOFF):
quotes mensuals (Repsol 1,99 €, Endesa 2 €, Gana ~2,1 € després del 1r any), caducitat del saldo
(Naturgy 5 anys, Iberdrola 24 mesos) i topes tipus el 40% de Repsol. Per a perfils amb molta
exportació aquests factors poden capgirar el rànquing i cal afegir camps `MonthlyFee`, `ExpiryMonths`
i un hook de tope.
