#!/usr/bin/env bash
# datadis-download.sh — descarga la curva horaria de consumo/excedentes desde el API
# de Datadis y la guarda en el CSV que espera solar-tariff-compare.
#
# Requiere las credenciales de Datadis del titular (NIF + contraseña), que sólo el
# usuario debe introducir. Viajan por HTTPS a datadis.es y no se guardan en disco.
#
# Uso:
#   export DATADIS_USER="12345678Z"     # NIF del titular del contrato
#   export DATADIS_PASS="********"       # contraseña de Datadis
#   ./datadis-download.sh --months 12 --out datadis.csv
#   ./datadis-download.sh --cups ES0031... --months 12 --out datadis.csv
#
# Opciones:
#   --months N     meses hacia atrás desde hoy (por defecto 12)
#   --out FILE     fichero CSV de salida (por defecto datadis.csv)
#   --cups CUPS    CUPS concreto (si el titular tiene varios; si no, se listan)
#
# Dependencias: curl y jq.
set -euo pipefail

months=12
out="datadis.csv"
cups=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --months) months="$2"; shift 2 ;;
    --out) out="$2"; shift 2 ;;
    --cups) cups="$2"; shift 2 ;;
    -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
    *) echo "opción desconocida: $1" >&2; exit 2 ;;
  esac
done

command -v curl >/dev/null 2>&1 || { echo "error: falta 'curl'." >&2; exit 1; }
command -v jq   >/dev/null 2>&1 || { echo "error: falta 'jq' (instálalo: brew install jq / apt install jq)." >&2; exit 1; }

# --fail-with-body (curl >= 7.76) muestra el cuerpo del error; si no está, usa --fail.
if curl --help all 2>/dev/null | grep -q -- '--fail-with-body'; then
  fail_flag=(--fail-with-body)
else
  fail_flag=(--fail)
fi

: "${DATADIS_USER:?exporta DATADIS_USER con el NIF del titular}"
: "${DATADIS_PASS:?exporta DATADIS_PASS con la contraseña de Datadis}"

api="https://datadis.es"

echo "autenticando en Datadis..." >&2
token="$(curl -sS "${fail_flag[@]}" -X POST "$api/nikola-auth/tokens/login" \
  --data-urlencode "username=$DATADIS_USER" \
  --data-urlencode "password=$DATADIS_PASS")" || {
  echo "error: login de Datadis fallido (revisa NIF/contraseña)." >&2; exit 1; }

# El endpoint devuelve el token como texto plano (a veces con espacios); lo limpiamos.
token="$(printf '%s' "$token" | tr -d '[:space:]')"
if [[ -z "$token" ]]; then
  echo "error: Datadis no devolvió token." >&2; exit 1
fi
auth=(-H "Authorization: Bearer $token")

echo "listando suministros..." >&2
supplies="$(curl -sS "${fail_flag[@]}" "${auth[@]}" "$api/api-private/api/get-supplies")" || {
  echo "error: no se pudieron listar los suministros." >&2; exit 1; }

n_supplies="$(printf '%s' "$supplies" | jq 'length')"
if [[ "$n_supplies" == "0" || -z "$n_supplies" ]]; then
  echo "error: el titular no tiene suministros en Datadis." >&2; exit 1
fi

# Selecciona el suministro: por --cups o, si hay varios y no se indicó, se listan.
if [[ -z "$cups" ]]; then
  if [[ "$n_supplies" == "1" ]]; then
    cups="$(printf '%s' "$supplies" | jq -r '.[0].cups')"
  else
    echo "El titular tiene varios CUPS; elige uno con --cups:" >&2
    printf '%s' "$supplies" | jq -r '.[] | "  \(.cups)  \(.address // "") \(.postalCode // "")"' >&2
    exit 2
  fi
fi

# Extrae distributorCode y pointType del suministro elegido (los necesita get-consumption-data).
supply="$(printf '%s' "$supplies" | jq -r --arg c "$cups" '.[] | select(.cups==$c)')"
if [[ -z "$supply" ]]; then
  echo "error: el CUPS $cups no está entre los suministros del titular." >&2; exit 1
fi
dist="$(printf '%s' "$supply" | jq -r '.distributorCode')"
ptype="$(printf '%s' "$supply" | jq -r '.pointType')"

# Genera la lista de meses "YYYY/MM" desde hace `months` hasta el mes actual (portátil,
# sin depender de `date -d`, que difiere entre macOS y GNU).
y=$(date +%Y); m=$((10#$(date +%m)))
declare -a monthlist=()
for ((i=months-1; i>=0; i--)); do
  yy=$y; mm=$((m - i))
  while (( mm <= 0 )); do mm=$((mm+12)); yy=$((yy-1)); done
  monthlist+=("$(printf '%04d/%02d' "$yy" "$mm")")
done
# Índice del último elemento (bash 3.2 de macOS no admite ${monthlist[-1]}).
last=$(( ${#monthlist[@]} - 1 ))

echo "descargando consumo horario de $cups (${monthlist[0]} … ${monthlist[$last]})..." >&2

# Cabecera del CSV que auto-detecta la herramienta.
printf 'cups,date,time,consumptionKWh,obtainMethod,surplusEnergyKWh,generationEnergyKWh,selfConsumptionEnergyKWh\n' > "$out"

rows=0
for period in "${monthlist[@]}"; do
  # measurementType=0 → horario. startDate y endDate en el mismo mes (YYYY/MM).
  data="$(curl -sS "${fail_flag[@]}" "${auth[@]}" -G "$api/api-private/api/get-consumption-data" \
    --data-urlencode "cups=$cups" \
    --data-urlencode "distributorCode=$dist" \
    --data-urlencode "startDate=$period" \
    --data-urlencode "endDate=$period" \
    --data-urlencode "measurementType=0" \
    --data-urlencode "pointType=$ptype" 2>/dev/null)" || {
    echo "aviso: falló la descarga de $period (se omite)." >&2; continue; }

  # Convierte el JSON de Datadis al CSV. Campos que pueden faltar → vacío/0.
  count="$(printf '%s' "$data" | jq -r '
    if type=="array" then
      (.[] | [
        (.cups // ""),
        (.date // ""),
        (.time // ""),
        (.consumptionKWh // 0),
        (.obtainMethod // ""),
        (.surplusEnergyKWh // 0),
        (.generationEnergyKWh // ""),
        (.selfConsumptionEnergyKWh // "")
      ] | @csv)
    else empty end' 2>/dev/null | tee -a "$out" | wc -l | tr -d ' ')" || count=0
  rows=$((rows + count))
  echo "  $period: $count filas" >&2
done

if [[ "$rows" == "0" ]]; then
  echo "error: no se descargó ninguna fila. ¿Rango sin datos o CUPS sin curva horaria?" >&2
  exit 1
fi

echo "listo: $rows filas en $out" >&2
echo "$out"
