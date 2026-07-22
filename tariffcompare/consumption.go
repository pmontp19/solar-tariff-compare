package tariffcompare

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// LoadCurve es la curva horaria agregada en sus componentes. El CSV CCH puede
// venir en dos variantes (5 o 7 columnas); las columnas adicionales traen
// excedentes (AS_KWh) y autoconsumo (AE_AUTOCONS_kWh).
type LoadCurve struct {
	Consumption  map[time.Time]float64 // instante (inicio de hora) -> kWh consumidos
	Surplus      map[time.Time]float64 // kWh vertidos a la red
	SelfConsumed map[time.Time]float64 // kWh FV consumidos in situ
	First, Last  time.Time
}

// CCHInfo es el resultado de cargar y validar un fichero CCH.
type CCHInfo struct {
	Curve              LoadCurve
	ConsumptionSummary ConsumptionSummary // consumo agregado P1/P2/P3
	Rows               int
	Holes              int // horas faltantes dentro del rango (huecos)
	EstimatedPct       float64
}

// ParseCCH lee un CSV de curva horaria (formato e-distribución CCH_CONS).
//
// Cabecera esperada: CUPS;Fecha;Hora;AE_kWh;[AS_KWh;AE_AUTOCONS_kWh;]REAL/ESTIMADO
//   - Fecha en formato DD/MM/YYYY
//   - Hora 1..24 (1 = intervalo 00:00-01:00, por tanto hora-inicio = Hora-1)
//   - decimales con coma española (0,168)
//
// Tolerante: ignora la coma decimal y acepta ambas variantes de columnas.
func ParseCCH(path string, holidays HolidayCalendar) (*CCHInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseCCHReader(f, holidays)
}

func ParseCCHReader(r io.Reader, holidays HolidayCalendar) (*CCHInfo, error) {
	cr := csv.NewReader(r)
	cr.Comma = ';'
	cr.FieldsPerRecord = -1 // tolerar 5 o 7 columnas

	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("leyendo cabecera: %w", err)
	}
	idx := indexHeader(header)

	loc, _ := time.LoadLocation("Europe/Madrid")
	info := &CCHInfo{Curve: LoadCurve{
		Consumption: map[time.Time]float64{}, Surplus: map[time.Time]float64{}, SelfConsumed: map[time.Time]float64{},
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
		if len(rec) < 4 {
			continue
		}
		dt, ok := parseDateTime(rec[idx.fecha], rec[idx.hora], loc)
		if !ok {
			continue
		}
		consumo, _ := parseSpanishFloat(rec[idx.ae])
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
		if idx.as >= 0 && len(rec) > idx.as {
			if v, ok := parseSpanishFloat(rec[idx.as]); ok && v > 0 {
				info.Curve.Surplus[dt] += v
			}
		}
		if idx.auto >= 0 && len(rec) > idx.auto {
			if v, ok := parseSpanishFloat(rec[idx.auto]); ok && v > 0 {
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
		if idx.est >= 0 && len(rec) > idx.est && strings.HasPrefix(strings.ToUpper(rec[idx.est]), "E") {
			estimado++
		}
	}
	if total > 0 {
		info.EstimatedPct = 100 * float64(estimado) / float64(total)
	}
	info.Holes = countHoles(info.Curve.First, info.Curve.Last, info.Curve.Consumption, holidays)
	return info, nil
}

type colIndex struct{ fecha, hora, ae, as, auto, est int }

func indexHeader(h []string) colIndex {
	idx := colIndex{fecha: -1, hora: -1, ae: -1, as: -1, auto: -1, est: -1}
	for i, c := range h {
		switch strings.ToLower(strings.TrimSpace(c)) {
		case "fecha":
			idx.fecha = i
		case "hora":
			idx.hora = i
		case "ae_kwh":
			idx.ae = i
		case "as_kwh":
			idx.as = i
		case "ae_autocons_kwh":
			idx.auto = i
		case "real/estimado":
			idx.est = i
		}
	}
	if idx.fecha < 0 {
		idx.fecha = 1
	}
	if idx.hora < 0 {
		idx.hora = 2
	}
	if idx.ae < 0 {
		idx.ae = 3
	}
	return idx
}

func parseDateTime(fecha, hora string, loc *time.Location) (time.Time, bool) {
	dt, err := time.ParseInLocation("02/01/2006", strings.TrimSpace(fecha), loc)
	if err != nil {
		return time.Time{}, false
	}
	h, err := strconv.Atoi(strings.TrimSpace(hora))
	if err != nil {
		return time.Time{}, false
	}
	return hourStart(dt.Year(), dt.Month(), dt.Day(), h, loc)
}

// hourStart mapea la etiqueta Hora 1..24 (25 el día del cambio horario de octubre)
// al instante de inicio del intervalo. Se construye sumando horas absolutas desde
// la medianoche local: así la hora repetida del cambio de octubre produce dos
// instantes distintos (time.Date con la hora local sería ambiguo) y el día de
// marzo (23 horas) rechaza la etiqueta 24.
func hourStart(y int, m time.Month, d, h int, loc *time.Location) (time.Time, bool) {
	if h < 1 || h > 25 {
		return time.Time{}, false
	}
	day := time.Date(y, m, d, 0, 0, 0, 0, loc)
	if maxH := int(day.AddDate(0, 0, 1).Sub(day).Hours()); h > maxH {
		return time.Time{}, false // etiqueta 25 fuera del día de 25 horas (o 24 en el de 23)
	}
	return day.Add(time.Duration(h-1) * time.Hour), true
}

// parseSpanishFloat convierte "0,168" -> 0.168. Devuelve 0, false si vacío/no válido.
func parseSpanishFloat(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	s = strings.Replace(s, ",", ".", 1)
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// countHoles estima las horas faltantes en el rango (una curva completa tiene
// ~8760 horas/año).
func countHoles(first, last time.Time, consumo map[time.Time]float64, holidays HolidayCalendar) int {
	if first.IsZero() || last.IsZero() {
		return 0
	}
	holes := 0
	for t := first; !t.After(last); t = t.Add(time.Hour) {
		if _, ok := consumo[t]; !ok {
			holes++
		}
	}
	return holes
}
