// Package tariffcompare compara tarifas de electricidad españolas desde el API
// pública de la CNMC frente a una curva de consumo real, opcionalmente con
// autoconsumo fotovoltaico.
package tariffcompare

import "time"

// Period es un período de la tarifa española 2.0TD (RD 1484/2021).
type Period int

const (
	PeriodPunta Period = 1 // P1 — días laborables 10:00-14:00 y 18:00-22:00
	PeriodLlano Period = 2 // P2 — días laborables 08:00-10:00, 14:00-18:00, 22:00-24:00
	PeriodValle Period = 3 // P3 — días laborables 00:00-08:00, fines de semana y festivos
)

func (p Period) Label() string {
	switch p {
	case PeriodPunta:
		return "P1 (Punta)"
	case PeriodLlano:
		return "P2 (Llano)"
	default:
		return "P3 (Valle)"
	}
}

// puntaHours son las horas de inicio (reloj local 0-23) en P1 un día laborable.
var puntaHours = map[int]bool{10: true, 11: true, 12: true, 13: true, 18: true, 19: true, 20: true, 21: true}
var llanoHours = map[int]bool{8: true, 9: true, 14: true, 15: true, 16: true, 17: true, 22: true, 23: true}

// HolidayCalendar devuelve el conjunto de fechas festivas de un año.
// Por defecto usa SpanishHolidays; se puede sobrescribir para festivos
// autonómicos/locales.
type HolidayCalendar func(year int) map[time.Time]bool

// PeriodFor devuelve el período de un instante en hora local peninsular.
// Si holidays es nil, usa SpanishHolidays.
func PeriodFor(t time.Time, holidays HolidayCalendar) Period {
	// Fin de semana -> Valle
	if t.Weekday() == time.Saturday || t.Weekday() == time.Sunday {
		return PeriodValle
	}
	// Festivos -> Valle (comparación por día, ignorando la zona horaria)
	hc := holidays
	if hc == nil {
		hc = SpanishHolidays
	}
	key := dateKey(t.Year(), t.Month(), t.Day())
	if hc(t.Year())[key] {
		return PeriodValle
	}
	h := t.Hour()
	if puntaHours[h] {
		return PeriodPunta
	}
	if llanoHours[h] {
		return PeriodLlano
	}
	return PeriodValle // 0-7h
}

// dateKey normaliza una fecha a mediodía UTC para usarla como clave estable
// (time.Time como map key no es igual si difiere la Location).
func dateKey(year int, m time.Month, day int) time.Time {
	return time.Date(year, m, day, 12, 0, 0, 0, time.UTC)
}

// SpanishHolidays devuelve los festivos que cuentan para los períodos de la 2.0TD:
// los nacionales NO SUSTITUIBLES (Circular 3/2020 de la CNMC; la lista de no
// sustituibles viene del art. 45 del RD 2001/1983). Ojo: Reyes (6 de enero) es
// sustituible y por tanto NO cuenta como valle; la Asunción (15 de agosto) sí.
// Se pueden añadir festivos autonómicos/locales pasándolos en su propia
// HolidayCalendar, pero para la tarifa sólo aplican los nacionales.
func SpanishHolidays(year int) map[time.Time]bool {
	out := make(map[time.Time]bool)
	add := func(m time.Month, d int) {
		out[dateKey(year, m, d)] = true
	}
	// Festivos nacionales no sustituibles de fecha fija
	add(time.January, 1)   // Año Nuevo
	add(time.May, 1)       // Día del Trabajador
	add(time.August, 15)   // Asunción de la Virgen
	add(time.October, 12)  // Fiesta Nacional de España
	add(time.November, 1)  // Todos los Santos
	add(time.December, 6)  // Día de la Constitución
	add(time.December, 8)  // Inmaculada Concepción
	add(time.December, 25) // Navidad
	// Viernes Santo (no sustituible, fecha móvil): viernes anterior a Pascua
	easter := easterSunday(year)
	viernesSanto := easter.AddDate(0, 0, -2)
	out[dateKey(year, viernesSanto.Month(), viernesSanto.Day())] = true
	return out
}

// easterSunday calcula el domingo de Pascua (cómputo gregoriano) mediante el
// algoritmo de Meeus/Jones/Butcher. Válido para años a partir de 1583.
func easterSunday(year int) time.Time {
	a := year % 19
	b := year / 100
	c := year % 100
	d := b / 4
	e := b % 4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i := c / 4
	k := c % 4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	day := ((h + l - 7*m + 114) % 31) + 1
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}
