package solartrack

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// IsDatadisCSV detecta si un fichero tiene formato Datadis (cabecera con
// consumptionKWh y separador coma) frente a la CCH de e-distribución (separador ;).
// Permite auto-detectar el formato sin flags.
func IsDatadisCSV(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	head := strings.ToLower(string(buf[:n]))
	return strings.Contains(head, "consumptionkwh") ||
		(strings.Contains(head, ",") && strings.Contains(head, "surplusenergykwh"))
}

// ParseConsumption carga la curva de consumo auto-detectando el formato (Datadis o
// CCH de e-distribución).
func ParseConsumption(path string, holidays HolidayCalendar) (*CCHInfo, error) {
	if IsDatadisCSV(path) {
		return ParseDatadis(path, holidays)
	}
	return ParseCCH(path, holidays)
}

// ParseDatadis lee un CSV de consumo horario descargado del API de Datadis
// (get-consumption-data, measurementType=0).
//
// A diferencia de la CCH de e-distribución, Datadis entrega directamente el consumo
// NETO de red (columna consumptionKWh, ya descontado el autoconsumo instantáneo) y
// los excedentes vertidos (surplusEnergyKWh). Las columnas de producción total y
// autoconsumo (generationEnergyKWh, selfConsumptionEnergyKWh) suelen venir VACÍAS
// (Datadis no las publica para la mayoría de puntos), por lo que NO se puede derivar
// el autoconsumo real sólo con Datadis: haría falta la producción del inversor.
//
// Cabecera esperada (orden flexible, se localiza por nombre):
//
//	cups,date,time,consumptionKWh,obtainMethod,surplusEnergyKWh,generationEnergyKWh,selfConsumptionEnergyKWh
//
//   - date en formato YYYY/MM/DD
//   - time "HH:MM" de "01:00" a "24:00" (01:00 = intervalo 00:00-01:00, hora-inicio = HH-1)
//   - decimales con punto (0.317)
//   - obtainMethod: "Real" o "Estimada"/"Estimado"
//
// Devuelve un *CCHInfo equivalente al de ParseCCH para reutilizar el resto del
// pipeline. Consumption = consumo neto de red; Surplus = excedentes reales;
// SelfConsumed queda vacío salvo que Datadis lo informe.
func ParseDatadis(path string, holidays HolidayCalendar) (*CCHInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseDatadisReader(f, holidays)
}

func ParseDatadisReader(r io.Reader, holidays HolidayCalendar) (*CCHInfo, error) {
	cr := csv.NewReader(r)
	cr.Comma = ','
	cr.FieldsPerRecord = -1 // tolerar filas con distinto número de columnas

	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("leyendo cabecera: %w", err)
	}
	idx := indexDatadisHeader(header)
	if idx.date < 0 || idx.time < 0 || idx.cons < 0 {
		return nil, fmt.Errorf("cabecera Datadis no reconocida (faltan date/time/consumptionKWh): %v", header)
	}

	loc, _ := time.LoadLocation("Europe/Madrid")
	info := &CCHInfo{Curve: LoadCurve{
		Consumption:  map[time.Time]float64{},
		Surplus:      map[time.Time]float64{},
		SelfConsumed: map[time.Time]float64{},
	}}
	var estimado, total int

	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("leyendo fila: %w", err)
		}
		if len(rec) <= idx.cons {
			continue
		}
		dt, ok := parseDatadisDateTime(rec[idx.date], rec[idx.time], loc)
		if !ok {
			continue
		}
		consumo, _ := parseDotFloat(rec[idx.cons])
		if consumo > 0 {
			info.Curve.Consumption[dt] += consumo
			p := PeriodFor(dt, holidays)
			info.ConsumptionSummary.Annual += consumo
			switch p {
			case PeriodPunta:
				info.ConsumptionSummary.P1 += consumo
			case PeriodLlano:
				info.ConsumptionSummary.P2 += consumo
			default:
				info.ConsumptionSummary.P3 += consumo
			}
		}
		if idx.surplus >= 0 && len(rec) > idx.surplus {
			if v, ok := parseDotFloat(rec[idx.surplus]); ok && v > 0 {
				info.Curve.Surplus[dt] += v
			}
		}
		if idx.self >= 0 && len(rec) > idx.self {
			if v, ok := parseDotFloat(rec[idx.self]); ok && v > 0 {
				info.Curve.SelfConsumed[dt] += v
				info.ConsumptionSummary.SelfConsumedKWh += v
			}
		}
		if info.Curve.First.IsZero() || dt.Before(info.Curve.First) {
			info.Curve.First = dt
		}
		if dt.After(info.Curve.Last) {
			info.Curve.Last = dt
		}
		info.Rows++
		total++
		if idx.method >= 0 && len(rec) > idx.method {
			if m := strings.ToUpper(strings.TrimSpace(rec[idx.method])); strings.HasPrefix(m, "E") {
				estimado++
			}
		}
	}
	if total > 0 {
		info.EstimatedPct = 100 * float64(estimado) / float64(total)
	}
	info.Holes = countHoles(info.Curve.First, info.Curve.Last, info.Curve.Consumption, holidays)
	return info, nil
}

// datadisIdx localiza las columnas relevantes de la cabecera de Datadis por nombre.
type datadisIdx struct {
	date, time, cons, surplus, self, method int
}

func indexDatadisHeader(header []string) datadisIdx {
	idx := datadisIdx{date: -1, time: -1, cons: -1, surplus: -1, self: -1, method: -1}
	for i, h := range header {
		switch strings.ToLower(strings.TrimSpace(h)) {
		case "date", "fecha":
			idx.date = i
		case "time", "hora":
			idx.time = i
		case "consumptionkwh", "consumokwh", "ae_kwh":
			idx.cons = i
		case "surplusenergykwh", "as_kwh", "excedente", "excedentekwh":
			idx.surplus = i
		case "selfconsumptionenergykwh", "ae_autocons_kwh", "autoconsumokwh":
			idx.self = i
		case "obtainmethod", "metodo", "real/estimado":
			idx.method = i
		}
	}
	return idx
}

// parseDatadisDateTime interpreta ("YYYY/MM/DD", "HH:MM") -> instante (inicio de hora)
// en la zona indicada. Datadis usa "01:00".."24:00" con la hora al FINAL del
// intervalo, por lo que la hora de inicio es HH-1 (y "24:00" -> 23:00 del mismo día).
func parseDatadisDateTime(fecha, hora string, loc *time.Location) (time.Time, bool) {
	fecha = strings.TrimSpace(fecha)
	hora = strings.TrimSpace(hora)
	// fecha admite / o -
	fecha = strings.ReplaceAll(fecha, "-", "/")
	parts := strings.Split(fecha, "/")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	y, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	d, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil || m < 1 || m > 12 || d < 1 || d > 31 {
		return time.Time{}, false
	}
	hh := hora
	if i := strings.Index(hh, ":"); i >= 0 {
		hh = hh[:i]
	}
	h, err := strconv.Atoi(hh)
	if err != nil || h < 1 || h > 24 {
		return time.Time{}, false
	}
	return time.Date(y, time.Month(m), d, h-1, 0, 0, 0, loc), true
}

// parseDotFloat parsea un número con punto decimal (formato Datadis). Tolera coma
// por si acaso.
func parseDotFloat(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	s = strings.ReplaceAll(s, ",", ".")
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
