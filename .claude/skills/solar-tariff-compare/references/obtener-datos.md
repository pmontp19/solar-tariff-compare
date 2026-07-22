# Cómo obtener los datos del usuario

La herramienta necesita una **curva de consumo horaria**. Hay dos fuentes buenas. Con
placas, la de Datadis es la mejor porque trae los excedentes reales.

## Opción A — Datadis (recomendada, sobre todo con placas)

[Datadis](https://datadis.es) es la plataforma oficial de las distribuidoras. Da consumo
horario **neto de red** y, si hay autoconsumo, los **excedentes vertidos** hora a hora.

### A.1 — Descarga manual (web)

1. Registro/acceso en https://datadis.es con el **NIF del titular** del contrato.
2. Menú **"Descargar datos"** → tipo **horario** (`measurementType=0`).
3. Elige el CUPS y el rango: pide **12 meses** completos (un año estacional entero; si no,
   la clasificación P1/P2/P3 y los excedentes de verano quedan sesgados).
4. Descarga el CSV. La cabecera esperada es:

   ```
   cups,date,time,consumptionKWh,obtainMethod,surplusEnergyKWh,generationEnergyKWh,selfConsumptionEnergyKWh
   ```

   La herramienta lo **auto-detecta** (separador coma + `consumptionKWh`). `date` en
   `YYYY/MM/DD`, `time` en `01:00`..`24:00` (`25:00` el día del cambio horario de octubre).

### A.2 — Descarga automatizada (script)

`scripts/datadis-download.sh` hace login en el API de Datadis, lista los suministros y
baja la curva horaria ya en el formato CSV correcto. Necesita las credenciales del
usuario (NIF + contraseña de Datadis), que **sólo** debe introducir él:

```bash
export DATADIS_USER="12345678Z"      # NIF del titular
export DATADIS_PASS="********"        # contraseña de Datadis
./scripts/datadis-download.sh --months 12 --out datadis.csv
# Si tiene varios CUPS, el script los lista y pide elegir; o pasa --cups ES00310...
```

Las credenciales viajan sólo a `datadis.es` por HTTPS y no se guardan. Si el usuario
prefiere no dártelas, usa la descarga manual (A.1).

### Qué columnas trae Datadis y qué NO

- `consumptionKWh`: consumo **ya neto** de red (descontado el autoconsumo instantáneo).
- `surplusEnergyKWh`: excedentes reales vertidos. **Este es el dato clave** para el
  ranking neto.
- `generationEnergyKWh`, `selfConsumptionEnergyKWh`: la **producción total** y el
  **autoconsumo** casi siempre vienen **vacíos** (Datadis no los publica). Por eso, con
  sólo Datadis no se puede reconstruir el autoconsumo real; para eso haría falta la
  producción del inversor (Huawei/otros). No pasa nada: para elegir comercializadora lo
  que importa es el consumo neto y los excedentes, y esos sí están.

Con Datadis, como el consumo ya es neto, la consulta a la CNMC va con
`energiaAutoconsumo=0` (la factura ya es neta); el efecto de los excedentes lo aporta la
simulación y el ranking neto.

## Opción B — Oficina virtual de la distribuidora (CCH)

Si el usuario no usa Datadis, su distribuidora (e-distribución/Endesa, i-DE/Iberdrola,
UFD/Naturgy, e-redes...) ofrece la **curva de carga horaria (CCH)** en su oficina virtual.

Cabecera de e-distribución (separador `;`, decimal con coma):

```
CUPS;Fecha;Hora;AE_kWh;REAL/ESTIMADO
ES0031...TF0F;15/01/2025;1;0,168;R
```

- `Fecha` = `DD/MM/YYYY`; `Hora` = 1..24 (1 = intervalo 00:00–01:00; 25 el día del cambio
  de octubre); `AE_kWh` con coma decimal.
- Variante de **7 columnas** (aparece cuando ya tienes placas): añade `AS_KWh`
  (excedentes) y `AE_AUTOCONS_kWh` (autoconsumo). Si el usuario la tiene, es tan buena
  como Datadis y además trae el autoconsumo real.

## Datos complementarios a preguntar

- **Código postal** (`-cp`): el de la vivienda. Acepta `08001` u `8001`.
- **Potencia contratada** (`-potencia`): está en la factura, en kW. Valores típicos 2.0TD:
  3.45, 4.6, 5.75. Por defecto 3.45.
- **Si tiene o planea placas**:
  - **Potencia pico** (`-kwp`): suma de vatios pico de los paneles / 1000. Ej.: 10 paneles
    de 450 W = 4.5 kWp. Sólo necesario si NO hay excedentes reales en el CSV.
  - **Orientación** (`-aspect`): 0 = sur, -90 = este, 90 = oeste. **Inclinación**
    (`-angle`): grados sobre la horizontal (35 típico residencial).
  - **Ubicación** (`-lat`, `-lon`): del municipio, para PVGIS. Por defecto Barcelona.

## Privacidad

La curva horaria + el CUPS revelan cuándo hay gente en casa. Trátala como dato sensible:
el análisis es **local**, no la subas a servicios externos ni la pegues en herramientas
online. El CUPS no se envía en la salida JSON de la herramienta.
