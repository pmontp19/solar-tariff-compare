#!/usr/bin/env bash
# compare.sh — compila (si hace falta) y ejecuta el comparador solar-tariff-compare.
#
# Uso:
#   ./compare.sh -consum datadis.csv -cp 08001 -potencia 4.6 -sim -json
#   ./compare.sh --help    # ayuda del binario
#
# Localiza la raíz del repo subiendo desde este script (…/.claude/skills/solar-tariff-compare/scripts).
# Compila el binario en un cache local si no existe o si el fuente es más nuevo.
# Comprueba dependencias (go, curl) y avisa si falta ESIOS_TOKEN.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# scripts -> solar-tariff-compare -> skills -> .claude -> repo root
repo_root="$(cd "$script_dir/../../../.." && pwd)"

if [[ ! -f "$repo_root/go.mod" ]]; then
  echo "error: no encuentro la raíz del repo (go.mod) desde $repo_root" >&2
  echo "       ejecuta este script desde dentro del repo solar-tariff-compare." >&2
  exit 1
fi

command -v go >/dev/null 2>&1 || { echo "error: falta 'go' (instala Go para compilar)." >&2; exit 1; }
command -v curl >/dev/null 2>&1 || echo "aviso: falta 'curl'; e-sios no funcionará (sin -sim ni ranking neto)." >&2

if [[ -z "${ESIOS_TOKEN:-}" ]]; then
  echo "aviso: sin ESIOS_TOKEN los precios e-sios son sólo del último día (absolutos orientativos)." >&2
  echo "       regístrate gratis en https://api.esios.ree.es y exporta ESIOS_TOKEN para histórico anual." >&2
fi

bin="$repo_root/solar-tariff-compare"
src="$repo_root/cmd/solar-tariff-compare/main.go"
# Recompila si el binario no existe o cualquier fuente Go es más nueva que el binario.
needs_build=0
if [[ ! -x "$bin" ]]; then
  needs_build=1
elif [[ -n "$(find "$repo_root/cmd" "$repo_root/tariffcompare" -name '*.go' -newer "$bin" -print -quit 2>/dev/null)" ]]; then
  needs_build=1
fi
if [[ "$needs_build" == "1" ]]; then
  echo "compilando solar-tariff-compare..." >&2
  (cd "$repo_root" && go build -o "$bin" ./cmd/solar-tariff-compare)
fi

exec "$bin" "$@"
