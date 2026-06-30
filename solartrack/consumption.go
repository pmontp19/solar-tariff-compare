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

// Corba horària agregada en els seus components. El CSV CCH pot venir en dues
// variants (5 o 7 columnes); les columnes addicionals porten excedents (AS_KWh)
// i autoconsum (AE_AUTOCONS_kWh).
type Corba struct {
	Consum      map[time.Time]float64 // instant (inici hora) -> kWh consumits
	Excedents   map[time.Time]float64 // kWh bolcats a la xarxa
	Autoconsum  map[time.Time]float64 // kWh FV consumits in situ
	First, Last time.Time
}

// CCHInfo és el resultat de carregar i validar un fitxer CCH.
type CCHInfo struct {
	Corba         Corba
	ConsumAnalisi ConsumAnalisi // consum agregat P1/P2/P3
	Rows          int
	Holes         int // hores mancants dins el rang (forats)
	EstimatedPct  float64
}

// ParseCCH llegeix un CSV de corba horària (format e-distribución CCH_CONS).
//
// Capçalera esperada: CUPS;Fecha;Hora;AE_kWh;[AS_KWh;AE_AUTOCONS_kWh;]REAL/ESTIMADO
//   - Fecha en format DD/MM/YYYY
//   - Hora 1..24 (1 = interval 00:00-01:00, per tant hora-inici = Hora-1)
//   - decimals amb coma espanyola (0,168)
//
// Tolerant: ignora la coma decimal i accepta les dues variants de columnes.
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
	cr.FieldsPerRecord = -1 // tolerar 5 o 7 columnes

	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("llegint capçalera: %w", err)
	}
	idx := indexHeader(header)

	loc, _ := time.LoadLocation("Europe/Madrid")
	info := &CCHInfo{Corba: Corba{
		Consum: map[time.Time]float64{}, Excedents: map[time.Time]float64{}, Autoconsum: map[time.Time]float64{},
	}}
	var estimat, total int

	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("llegint fila: %w", err)
		}
		if len(rec) < 4 {
			continue
		}
		dt, ok := parseDateTime(rec[idx.fecha], rec[idx.hora], loc)
		if !ok {
			continue
		}
		consum, _ := parseSpanishFloat(rec[idx.ae])
		if consum > 0 {
			info.Corba.Consum[dt] += consum
			p := PeriodFor(dt, holidays)
			info.ConsumAnalisi.Anual += consum
			switch p {
			case PeriodPunta:
				info.ConsumAnalisi.P1 += consum
			case PeriodLlano:
				info.ConsumAnalisi.P2 += consum
			default:
				info.ConsumAnalisi.P3 += consum
			}
		}
		if idx.as >= 0 && len(rec) > idx.as {
			if v, ok := parseSpanishFloat(rec[idx.as]); ok && v > 0 {
				info.Corba.Excedents[dt] += v
			}
		}
		if idx.auto >= 0 && len(rec) > idx.auto {
			if v, ok := parseSpanishFloat(rec[idx.auto]); ok && v > 0 {
				info.Corba.Autoconsum[dt] += v
				info.ConsumAnalisi.AutoconsumKWh += v
			}
		}
		if info.Corba.First.IsZero() || dt.Before(info.Corba.First) {
			info.Corba.First = dt
		}
		if dt.After(info.Corba.Last) {
			info.Corba.Last = dt
		}
		info.Rows++
		total++
		if idx.est >= 0 && len(rec) > idx.est && strings.HasPrefix(strings.ToUpper(rec[idx.est]), "E") {
			estimat++
		}
	}
	if total > 0 {
		info.EstimatedPct = 100 * float64(estimat) / float64(total)
	}
	info.Holes = countHoles(info.Corba.First, info.Corba.Last, info.Corba.Consum, holidays)
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
	if err != nil || h < 1 || h > 24 {
		return time.Time{}, false
	}
	// Hora 1..24 -> hora-inici = Hora-1 (00..23)
	return time.Date(dt.Year(), dt.Month(), dt.Day(), h-1, 0, 0, 0, loc), true
}

// parseSpanishFloat converteix "0,168" -> 0.168. Retorna 0 ok=false si buit/no vàlid.
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

// countHoles estima hores mancants al rang (una corba completa té ~8760 hores/any).
func countHoles(first, last time.Time, consum map[time.Time]float64, holidays HolidayCalendar) int {
	if first.IsZero() || last.IsZero() {
		return 0
	}
	holes := 0
	for t := first; !t.After(last); t = t.Add(time.Hour) {
		if _, ok := consum[t]; !ok {
			holes++
		}
	}
	return holes
}
