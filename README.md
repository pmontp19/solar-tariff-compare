# solar-tariff-compare

CLI en Go (binari únic, agent-first) que compara ofertes d'electricitat usant la teva **corba de consum horària real** (CCH d'e-distribución) contra l'API pública del **Comparador de la CNMC**, i modela la **producció FV** (PVGIS o CSV real) i els **excedents** (e-sios) per simular esquemes de compensació (regulada, indexada, **bateria virtual**).

Dissenyada perquè un agent (opencode, Claude Code, Cursor) la pugui conduir: vegeu [`AGENTS.md`](AGENTS.md).

A diferència del comparador web de la CNMC, aquesta eina:

- Llegeix directament el teu CSV de corba horària (CCH) i agrega el consum en els períodes P1/P2/P3 de la tarifa **2.0TD**, tenint en compte festius nacionals i caps de setmana.
- Estima la producció fotovoltaica amb [PVGIS](https://re.jrc.ec.europa.eu/) (Comissió Europea) i calcula l'autoconsum hora a hora sobre el teu consum real.
- Mostra les ofertes ordenades de més barata a més cara, amb comparativa amb/sense FV.

## ⚠️ Limitacions

- **Autoconsum**: modelat correctament (la part FV consumida in situ redueix la factura).
- **Excedents** (compensació del que bolques a la xarxa) i **bateria virtual**: **NO** els contempla l'API de la CNMC. Aquesta eina els modela a part amb preus d'e-sios (vegeu "Simulació d'excedents" més avall). És una estimació del **terme d'energia** (sense fixos).
- L'eina **no** tria per tu: et dóna els números perquè tu (o un agent) prengueu la decisió.

## Instal·lació

```bash
go install github.com/pmontp19/solar-tariff-compare/cmd/solar-tariff-compare@latest
```

O baixa un binari precompilat de [Releases](../../releases), o compila'l:

```bash
git clone https://github.com/pmontp19/solar-tariff-compare
cd solar-tariff-compare
go build -o solar-tariff-compare ./cmd/solar-tariff-compare
```

## Ús

### Comparativa només amb consum real

```bash
solar-tariff-compare -consum corba.csv -cp 8001 -top 15
```

### Amb estimació FV (PVGIS) + simulació d'excedents (la decisió clau)

```bash
solar-tariff-compare \
  -consum corba.csv -cp 8001 \
  -kwp 4.1 -lat 41.38 -lon 2.17 -angle 35 -aspect 0 \
  -sim -sense-solar -top 15
```

### Amb producció FV real (CSV de 7 columnes, més precís que PVGIS)

```bash
solar-tariff-compare -consum consum.csv -prod produccio.csv -cp 8001 -sim
```

### Sortida JSON

```bash
solar-tariff-compare -consum corba.csv -cp 8001 -kwp 3.5 -json > result.json
```

## Flags

| Flag | Per defecte | Descripció |
|---|---|---|
| `-consum` | — | CSV de la corba horària (CCH e-distribución). **Obligatori** |
| `-cp` | — | Codi postal sense el 0 inicial (`08001` → `8001`). **Obligatori** |
| `-potencia` | `3.45` | Potència contractada en kW (2.0TD) |
| `-top` | `20` | Nombre d'ofertes a mostrar |
| `-kwp` | `0` | Potència FV de pic en kW. Si >0 activa PVGIS |
| `-prod` | — | CSV de producció real (7 columnes amb `AS_KWh`/`AE_AUTOCONS_kWh`). Substitueix PVGIS |
| `-lat`, `-lon` | `41.38`, `2.17` | Coordenades per PVGIS |
| `-angle` | `35` | Inclinació dels panells (graus) |
| `-aspect` | `0` | Orientació (`0`=sud, `-90`=est, `90`=oest) |
| `-loss` | `14` | Pèrdues del sistema FV (%) |
| `-sim` | `false` | Amb FV: simula esquemes d'excedents (regulada/indexada/bateria virtual) amb e-sios |
| `-sense-solar` | `false` | Amb FV: també mostra la comparativa sense FV |
| `-json` | `false` | Sortida en JSON (per agents) |

## Simulació d'excedents (`-sim`)

Amb FV i `-sim`, l'eina baixa els preus horaris d'**e-sios** (Red Eléctrica):

- **Indicador 1001**: PVPC 2.0TD horari (preu de consum).
- **Indicador 1739**: preu regulat d'excedents d'autoconsum horari.

i simula la factura d'energia hora a hora per a tres esquemes:

| Esqueme | Preu excedents | Sostre | Exemple |
|---|---|---|---|
| **Regulada** | preu excedents (1739) | mensual | compensació simplificada per defecte |
| **Indexada** | preu excedents × (1+coef) + prima | mensual | Octopus, Som |
| **Bateria virtual** | **preu de consum** (PVPC) | **anual** (porta saldo) | Holaluz Sun, Repsol, Núcleo |

**Sortida**: terme d'energia net anual per esqueme (`energia_neta`), compensació usada i perduda (per la regla no-negatiu). Sense termes fixos (comuns a totes les ofertes).

### `ESIOS_TOKEN` i `curl`

- La baixada d'e-sios es fa via **`curl`** (e-sios bloqueja per fingerprint TLS el client HTTP de Go). `curl` ve per defecte a macOS/Linux/Windows 10+.
- Sense `ESIOS_TOKEN` només es pot obtenir **l'últim dia** de preus (`fuente: latest`), usat com a perfil representatiu de l'any — orientatiu. Per a una simulació precisa amb històric d'un any, registra un token gratuït a https://api.esios.ree.es i defineix `ESIOS_TOKEN`.

## Format del CSV (CCH)

El format estàndard que pots descarregar de la teva oficina virtual de la distribuïdora (e-distribución, etc.):

```
CUPS;Fecha;Hora;AE_kWh;REAL/ESTIMADO
ES0031400000000000TF0F;15/01/2025;1;0,168;R
ES0031400000000000TF0F;15/01/2025;2;0,182;R
...
```

- `Fecha` en format `DD/MM/YYYY`
- `Hora` de `1` a `24` (1 = interval 00:00–01:00)
- `AE_kWh` amb coma decimal espanyola (`0,168`)

També accepta la variant de 7 columnes amb excedents (`AS_KWh`) i autoconsum (`AE_AUTOCONS_kWh`), que apareixen un cop tens plaques.

## Com funciona

1. **Parseja** la corba horària i classifica cada hora en P1 (Punta), P2 (Llano) o P3 (Valle) segons el [RD 1484/2021](https://www.boe.es/), incloent-hi festius nacionals (Any Nou, Reis, Divendres Sant calculat per Pasqua, 1 de maig, 12 d'octubre, Tots Sants, Constitució, Inmaculada, Nadal).
2. **Si hi ha FV**, descarrega la sèrie horària de PVGIS i calcula, hora a hora, l'autoconsum `min(producció, consum)` i els excedents.
3. **Consulta l'API pública de la CNMC** (`/api/publico/ofertas/electricidad`) amb el consum agregat i l'autoconsum.
4. **Ordena** les ofertes per import del primer any.

Vegeu `CNMC-API-INVESTIGACIO.md` per al reverse engineering de l'API.

## Desenvolupament

```bash
go test ./...                      # tests (saltant els d'integració)
SOLARTRACK_SKIP_LIVE= go test ./...  # tests d'integració (crida real a CNMC + PVGIS)
```

## Llicència

MIT. Vegeu `LICENSE`.

## Agraïments

Dades d'ofertes: [CNMC — Comparador de Ofertas de Energía](https://comparador.cnmc.gob.es/).
Estimació FV: [PVGIS](https://re.jrc.ec.europa.eu/pvg_tools/en/) de la Comissió Europea.
Aquest projecte no està afiliat a la CNMC ni a la Comissió Europea.
