// Package solartrack compares Spanish electricity tariffs from the CNMC public API
// against a real consumption curve, optionally with self-consumed solar production.
package solartrack

import "time"

// Period és un període de la tarifa 2.0TD espanyola (RD 1484/2021).
type Period int

const (
	PeriodPunta Period = 1 // P1 — dies feiners 10:00-14:00 i 18:00-22:00
	PeriodLlano Period = 2 // P2 — dies feiners 08:00-10:00, 14:00-18:00, 22:00-24:00
	PeriodValle Period = 3 // P3 — dies feiners 00:00-08:00, cap de setmana i festius
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

// horesPunta són les hores d'inici (rellotge local 0-23) en P1 un dia feiner.
var horesPunta = map[int]bool{10: true, 11: true, 12: true, 13: true, 18: true, 19: true, 20: true, 21: true}
var horesLlano = map[int]bool{8: true, 9: true, 14: true, 15: true, 16: true, 17: true, 22: true, 23: true}

// HolidayCalendar retorna el conjunt de dates festives per a un any.
// Per defecte usa FestiusEspanya; es pot sobreescriure per a festius autonòmics/locals.
type HolidayCalendar func(year int) map[time.Time]bool

// dateKey normalitza una data a migdia UTC per usar-la com a clau estable
// (time.Time com a map key no és igual si difereix la Location).
func dateKey(year int, m time.Month, day int) time.Time {
	return time.Date(year, m, day, 12, 0, 0, 0, time.UTC)
}

// PeriodFor retorna el període d'un instant en hora local peninsular.
// Si holidays és nil, usa FestiusEspanya.
func PeriodFor(t time.Time, holidays HolidayCalendar) Period {
	// Cap de setmana -> Valle
	if t.Weekday() == time.Saturday || t.Weekday() == time.Sunday {
		return PeriodValle
	}
	// Festius -> Valle (comparació per dia, ignorant la zona horària)
	hc := holidays
	if hc == nil {
		hc = FestiusEspanya
	}
	key := dateKey(t.Year(), t.Month(), t.Day())
	if hc(t.Year())[key] {
		return PeriodValle
	}
	h := t.Hour()
	if horesPunta[h] {
		return PeriodPunta
	}
	if horesLlano[h] {
		return PeriodLlano
	}
	return PeriodValle // 0-7h
}

// FestiusEspanya retorna els festius nacionals espanyols (península/Baleares)
// d'un any: fixos + Divendres Sant (calculat a partir de la Pasqua).
// Es poden afegir festius autonòmics/locals passant-los a la vostra pròpia HolidayCalendar.
func FestiusEspanya(year int) map[time.Time]bool {
	out := make(map[time.Time]bool)
	add := func(m time.Month, d int) {
		out[dateKey(year, m, d)] = true
	}
	// Festius fixos nacionals
	add(time.January, 1)   // Any Nou
	add(time.January, 6)   // Reis
	add(time.May, 1)       // Dia del Treballador
	add(time.October, 12)  // Festa Nacional d'Espanya
	add(time.November, 1)  // Tots Sants
	add(time.December, 6)  // Dia de la Constitució
	add(time.December, 8)  // Inmaculada Concepció
	add(time.December, 25) // Nadal
	// Divendres Sant: divendres anterior al diumenge de Pasqua
	easter := easterSunday(year)
	viernesSanto := easter.AddDate(0, 0, -2)
	out[dateKey(year, viernesSanto.Month(), viernesSanto.Day())] = true
	return out
}

// easterSunday calcula el diumenge de Pasqua (còmput gregorià) via l'algoritme
// de Meeus/Jones/Butcher. Vàlid per a anys del 1583 en endavant.
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
