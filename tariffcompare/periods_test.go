package tariffcompare

import (
	"testing"
	"time"
)

func TestPeriodFor_Weekday(t *testing.T) {
	// Dimecres 15 de gener de 2025 (cap festiu)
	loc, _ := time.LoadLocation("Europe/Madrid")
	cases := []struct {
		hour int
		want Period
	}{
		{0, PeriodValle}, {3, PeriodValle}, {7, PeriodValle}, // 0-8h Valle
		{8, PeriodLlano}, {9, PeriodLlano}, // 8-10h Llano
		{10, PeriodPunta}, {11, PeriodPunta}, {13, PeriodPunta}, // 10-14h Punta
		{14, PeriodLlano}, {17, PeriodLlano}, // 14-18h Llano
		{18, PeriodPunta}, {21, PeriodPunta}, // 18-22h Punta
		{22, PeriodLlano}, {23, PeriodLlano}, // 22-24h Llano
	}
	for _, c := range cases {
		dt := time.Date(2025, 1, 15, c.hour, 0, 0, 0, loc)
		got := PeriodFor(dt, nil)
		if got != c.want {
			t.Errorf("hour %02d: got %s, want %s", c.hour, got.Label(), c.want.Label())
		}
	}
}

func TestPeriodFor_Weekend(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Madrid")
	// Dissabte a les 11:00 (seria Punta un dia feiner) -> Valle
	sat := time.Date(2025, 1, 18, 11, 0, 0, 0, loc) // dissabte
	if got := PeriodFor(sat, nil); got != PeriodValle {
		t.Errorf("dissabte 11h: got %s, want Valle", got.Label())
	}
	// Diumenge a les 19:00 (seria Punta) -> Valle
	sun := time.Date(2025, 1, 19, 19, 0, 0, 0, loc) // diumenge
	if got := PeriodFor(sun, nil); got != PeriodValle {
		t.Errorf("diumenge 19h: got %s, want Valle", got.Label())
	}
}

func TestPeriodFor_NationalHoliday(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Madrid")
	// 1 de gener de 2025 (Any Nou, dimecres) a les 11:00 -> Valle
	nadal := time.Date(2025, 1, 1, 11, 0, 0, 0, loc)
	if got := PeriodFor(nadal, nil); got != PeriodValle {
		t.Errorf("Any Nou 11h: got %s, want Valle", got.Label())
	}
	// 1 de maig (Treballador, dijous) a les 19:00 -> Valle
	treball := time.Date(2025, 5, 1, 19, 0, 0, 0, loc)
	if got := PeriodFor(treball, nil); got != PeriodValle {
		t.Errorf("1 de maig 19h: got %s, want Valle", got.Label())
	}
	// 15 d'agost de 2025 (Assumpció, divendres): festiu nacional NO substituïble -> Valle
	assumpcio := time.Date(2025, 8, 15, 11, 0, 0, 0, loc)
	if got := PeriodFor(assumpcio, nil); got != PeriodValle {
		t.Errorf("15 d'agost 11h: got %s, want Valle (festiu no substituïble)", got.Label())
	}
}

// Reis (6 de gener) és festiu SUBSTITUÏBLE: segons la Circular 3/2020 CNMC no
// compta com a valle per a la 2.0TD. El 6/1/2025 és dilluns laborable -> 11h Punta.
func TestPeriodFor_ReyesNoEsValle(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Madrid")
	reyes := time.Date(2025, 1, 6, 11, 0, 0, 0, loc)
	if got := PeriodFor(reyes, nil); got != PeriodPunta {
		t.Errorf("Reis 11h (substituïble, laborable): got %s, want Punta", got.Label())
	}
}

func TestPeriodFor_ViernesSanto(t *testing.T) {
	// Divendres Sant 2025 = 18 d'abril (calculat)
	easter := easterSunday(2025)
	viernes := easter.AddDate(0, 0, -2)
	if got := viernes.Format("2006-01-02"); got != "2025-04-18" {
		t.Fatalf("Divendres Sant 2025 esperat 2025-04-18, got %s", got)
	}
	loc, _ := time.LoadLocation("Europe/Madrid")
	// Divendres Sant a les 11:00 (Punta normal) -> Valle per festiu
	dt := time.Date(2025, 4, 18, 11, 0, 0, 0, loc)
	if p := PeriodFor(dt, nil); p != PeriodValle {
		t.Errorf("Divendres Sant 11h: got %s, want Valle", p.Label())
	}
	// El dijous sant (17/4) NO és festiu nacional -> hora normal (11h = Punta)
	dijous := time.Date(2025, 4, 17, 11, 0, 0, 0, loc)
	if p := PeriodFor(dijous, nil); p != PeriodPunta {
		t.Errorf("Dijous Sant 11h: got %s, want Punta (no és festiu nacional)", p.Label())
	}
}

func TestSpanishHolidays_Count(t *testing.T) {
	// 8 fixos no substituïbles + Divendres Sant = 9 festius (sense Reis, amb Assumpció)
	f := SpanishHolidays(2025)
	if len(f) != 9 {
		t.Errorf("2025: esperaba 9 festivos nacionales, got %d", len(f))
	}
	if f[dateKey(2025, time.January, 6)] {
		t.Errorf("Reis (6 de gener) és substituïble i no hauria de ser al calendari de la tarifa")
	}
	if !f[dateKey(2025, time.August, 15)] {
		t.Errorf("l'Assumpció (15 d'agost) és no substituïble i hauria de ser al calendari")
	}
}

func TestEasterSunday_KnownDates(t *testing.T) {
	cases := map[int]string{
		2024: "2024-03-31",
		2025: "2025-04-20",
		2026: "2026-04-05",
		2027: "2027-03-28",
	}
	for year, want := range cases {
		got := easterSunday(year).Format("2006-01-02")
		if got != want {
			t.Errorf("Pasqua %d: esperava %s, got %s", year, want, got)
		}
	}
}
