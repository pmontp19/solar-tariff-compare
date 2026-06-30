# AGENTS.md — solar-tariff-compare

Guia per a agents (opencode, Claude Code, Cursor, ...) per conduir aquesta eina i
ajudar un usuari a decidir la seva comercialitzadora d'electricitat amb autoconsum FV.

## Què fa l'eina

CLI en Go (binari únic) que, donada la corba de consum horària real (CCH) i
opcionalment la producció FV (PVGIS estimat o CSV real):

1. **Compara ofertes** contra l'API pública del comparador de la **CNMC**
   ( consum agregat en períodes P1/P2/P3 de la tarifa 2.0TD, amb festius).
2. **Simula excedents** hora a hora amb preus d'**e-sios** (PVPC horari + preu
   d'excedents regulat), comparant esquemes: regulada, indexada i **bateria virtual**.

## Quan fer-la servir

- L'usuari té plaques (o les planifica) i vol triar comercialitzadora.
- L'usuari té el seu CSV de corba horària (CCH d'e-distribución).
- Vols comparar excedents indexats vs bateria virtual per al seu perfil.

## Invocació típica

```bash
# 1. Només consum (sense FV): ranking CNMC
solar-tariff-compare -consum corba.csv -cp 8001 -top 20

# 2. Amb FV estimada (PVGIS) + comparativa excedents (la decisió clau)
solar-tariff-compare -consum corba.csv -cp 8001 -kwp 4.1 -sim -top 10

# 3. Amb producció real (CSV de 7 columnes): més precís que PVGIS
solar-tariff-compare -consum consum.csv -prod produccio.csv -cp 8001 -sim

# 4. Sortida JSON (per parsejar programàticament)
solar-tariff-compare -consum corba.csv -cp 8001 -kwp 4.1 -sim -json
```

## Llegir la sortida (per recomanar)

La sortida té tres blocs:

1. **Resum consum/FV**: `Consum: X kWh/any (P1..P2..P3..)` i `Producció FV: ... | autoconsum | excedents | cobertura`.
2. **Ranking CNMC** (`top_ofertes`): ofertes per `importePrimerAnio` (inclou tot: energia, potència, fixos, impostos). Però **sense compensar excedents** — és el cost del consum net.
3. **Simulació d'excedents** (`esquemes_excedents`, només amb `-sim`): `energia_neta` = terme d'energia anual **després** de compensar excedents. **Només terme d'energia** (sense fixos: potència, lloguer, impostos — comuns a totes les ofertes i cal afegir-los a part).

### Lògica de decisió

- L'esqueme amb **menys `energia_neta`** és el millor per al perfil de l'usuari.
- Per a perfils FV amb **molt excedent** (autoconsum < 40%, cobertura < 50%), la
  **bateria virtual** sol guanyar perquè valora els excedents al preu de consum i
  té sostre anual (porta saldo entre mesos).
- Per a perfils amb **poc excedent** o consum nocturn dominant, la diferència és menor.
- `compensacio_perduda` alta = diners "perduts" pel sostre (surt al COP sobretot a
  l'estiu amb indexada mensual).

### Composar el cost total anual aproximat

```
cost_total ≈ importePrimerAnio (del ranking CNMC, ja amb tots els fixos)
             − (energia_neta_sense_FV − energia_neta_amb_scheme)
```
On "energia_neta_sense_FV" és el terme d'energia sense excedents (simulació amb
producció 0). Els termes fixos (~potència + lloguer + IE + IVA) ja venen dins de
`importePrimerAnio`; la simulació d'excedents només mou el terme d'energia.

## Tokens i dependències

- **e-sios**: sense `ESIOS_TOKEN` només es pot obtenir **l'últim dia** de preus
  (`fuente: "latest"`), que es fa servir com a perfil horari representatiu de tot
  l'any. Per precisió (històric d'un any), demaneu a l'usuari un token gratuït a
  https://api.esios.ree.es i definiu `ESIOS_TOKEN`. Si la sortida mostra
  `fuente: latest`, advertiu que els absoluts són orientatius però la jerarquia
  d'esquemes sol mantenir-se.
- **curl**: la baixada d'e-sios es fa via `curl` (e-sios bloqueja per fingerprint
  TLS el client de Go). `curl` ve per defecte a macOS/Linux/Windows 10+.

## Limitacions a comunicar a l'usuari

- El comparador CNMC **no contempla excedents ni bateria virtual**; només els modela
  aquesta simulació.
- Els termes fixos no es modelen esquema a esquema (són comuns); les xifres
  absolutes de la simulació són terme d'energia, no factura final.
- Els coeficients/primes de cada comercialitzadora són aproximats i canvien; el
  registre (`solartrack/excedents.go` `SchemesRegistre`) és editable.

## Tests

```bash
go test ./... -skip _Live        # ràpids (sense xarxa)
SOLARTRACK_SKIP_LIVE= go test ./...  # amb integració (CNMC + PVGIS + e-sios)
```

## Error freqüent

- `CNMC HTTP 400`: els kWh han de ser enters (l'eina ja hi arrodoneix; si apareix,
  reviseu que no s'injectin decimals externs).
- `e-sios 403`: sense token i filtrant per geo. L'eina ja evita el filtre geo; si
  persisteix, cal `ESIOS_TOKEN`.
