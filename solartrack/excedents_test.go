package solartrack

import (
	"math"
	"testing"
	"time"
)

// Construeix una marca horària deterministicament.
func mkHour(day int, h int) time.Time {
	loc, _ := time.LoadLocation("Europe/Madrid")
	return time.Date(2025, 6, day, h, 0, 0, 0, loc)
}

func TestSimula_Excedents_Indexada(t *testing.T) {
	// Consumption: 1 kWh/hora durant 24h d'un sol dia (juny). Producció: 10 kWh a les 12h.
	consum := map[time.Time]float64{}
	for h := 0; h < 24; h++ {
		consum[mkHour(1, h)] = 1.0
	}
	// Producció amb claus derivades de consum (evita la trampa del time.Time com a
	// clau de map, que compara el punter de Location).
	prod := map[time.Time]float64{}
	for t := range consum {
		if t.Hour() == 12 {
			prod[t] = 10.0 // 1 kWh autoconsumit + 9 kWh excedents
		}
	}
	// Preus constants per simplificar: PVPC 0.15 €/kWh consum, excedents 0.05
	preus := &HourlyPrices{
		PVPC:    HourlySeries{ByDay: map[string][24]float64{"2025-06-01": fill24(0.15)}, Source: "test"},
		Surplus: HourlySeries{ByDay: map[string][24]float64{"2025-06-01": fill24(0.05)}, Source: "test"},
	}
	// Consum de xarxa = 24 - 1 (autoconsum a les 12) = 23 kWh × 0.15 = 3.45 €
	// Excedents = 9 kWh × 0.05 = 0.45 € compensats (dins del sostre mensual 3.45)
	// Energia neta = 3.45 - 0.45 = 3.00
	r := SimulateSurplus(consum, prod, preus, Scheme{Name: "indexada", Type: SchemeIndexed})
	assertFloat(t, r.GridConsumptionKWh, 23.0, "consum xarxa")
	assertFloat(t, r.SurplusKWh, 9.0, "excedents")
	assertFloat(t, r.GrossEnergy, 3.45, "energia bruta")
	assertFloat(t, r.UsedCompensation, 0.45, "compensat")
	assertFloat(t, r.NetEnergy, 3.00, "energia neta")
	if r.LostCompensation > 0.01 {
		t.Errorf("no hauria de perdre compensació: %.2f", r.LostCompensation)
	}
}

func TestSimula_SostreMensual_Indexada(t *testing.T) {
	// Cas on l'excedent supera de llarg el consum del mes (estiu): la part no
	// compensable es perd amb esquema indexat (sostre mensual).
	consum := map[time.Time]float64{mkHour(1, 0): 1.0}  // 1 kWh a les 0h
	prod := map[time.Time]float64{mkHour(1, 12): 100.0} // 100 kWh excedents a les 12h
	preus := &HourlyPrices{
		PVPC:    HourlySeries{ByDay: map[string][24]float64{"2025-06-01": fill24(0.15)}, Source: "test"},
		Surplus: HourlySeries{ByDay: map[string][24]float64{"2025-06-01": fill24(0.10)}, Source: "test"},
	}
	r := SimulateSurplus(consum, prod, preus, Scheme{Name: "indexada", Type: SchemeIndexed})
	// Energia bruta = 0 (l'hora 0 té consum però no producció → buy 1 × 0.15 = 0.15)
	assertFloat(t, r.GrossEnergy, 0.15, "energia bruta")
	// Excedents = 100 kWh (tot excedent, consum 0 a les 12) × 0.10 = 10 € bruts
	assertFloat(t, r.GrossCompensation, 10.0, "compensació bruta")
	// Sostre mensual: usable = min(10, 0.15) = 0.15; perdut = 9.85
	assertFloat(t, r.UsedCompensation, 0.15, "compensat (sostre mensual)")
	assertFloat(t, r.LostCompensation, 9.85, "perdut (sostre mensual)")
}

func TestSimula_SostreAnual_BateriaVirtual(t *testing.T) {
	// Mateix cas però amb bateria virtual: sostre ANUAL, i com que només hi ha un
	// mes el resultat numèric coincideix, però la lògica és diferent. Aquí verifiquem
	// que bateria virtual compense a PREU DE CONSUM (0.15) no d'excedents (0.10).
	consum := map[time.Time]float64{mkHour(1, 0): 1.0}
	prod := map[time.Time]float64{mkHour(1, 12): 100.0}
	preus := &HourlyPrices{
		PVPC:    HourlySeries{ByDay: map[string][24]float64{"2025-06-01": fill24(0.15)}, Source: "test"},
		Surplus: HourlySeries{ByDay: map[string][24]float64{"2025-06-01": fill24(0.10)}, Source: "test"},
	}
	r := SimulateSurplus(consum, prod, preus, Scheme{Name: "bateria", Type: SchemeVirtualBattery})
	// Bateria: compensa a preu consum 0.15 → bruta = 100 × 0.15 = 15 €; sostre anual 0.15
	assertFloat(t, r.GrossCompensation, 15.0, "bruta bateria (a preu consum)")
	assertFloat(t, r.UsedCompensation, 0.15, "usat (sostre)")
}

func TestSimula_BateriaVirtual_PotNegatiuLimitZero(t *testing.T) {
	// Si la compensació supera el consum anual, energia neta ≥ 0 (no es paga negatiu).
	consum := map[time.Time]float64{mkHour(1, 0): 1.0} // 1 kWh × 0.15 = 0.15 € consum
	prod := map[time.Time]float64{mkHour(1, 12): 100.0}
	preus := &HourlyPrices{
		PVPC:    HourlySeries{ByDay: map[string][24]float64{"2025-06-01": fill24(0.15)}, Source: "test"},
		Surplus: HourlySeries{ByDay: map[string][24]float64{"2025-06-01": fill24(0.10)}, Source: "test"},
	}
	r := SimulateSurplus(consum, prod, preus, Scheme{Name: "bateria", Type: SchemeVirtualBattery})
	if r.NetEnergy < -0.001 {
		t.Errorf("energia neta no pot ser negativa: %.4f", r.NetEnergy)
	}
}

// El mateix invariant de preu 0 real, però a la simulació d'esquemes: l'hora amb
// preu d'excedents 0 (marcada com a vista) no s'ha de substituir pel perfil mitjà.
func TestSimula_PreuZeroRealEsRespecta(t *testing.T) {
	seen := [24]bool{}
	for i := range seen {
		seen[i] = true
	}
	excArr := fill24(0.10)
	excArr[12] = 0.0 // migdia amb preu 0 real (habitual des del 2024)
	preus := &HourlyPrices{
		PVPC: HourlySeries{
			ByDay: map[string][24]float64{"2025-06-01": fill24(0.15)},
			Seen:  map[string][24]bool{"2025-06-01": seen}, Source: "test",
		},
		Surplus: HourlySeries{
			ByDay: map[string][24]float64{"2025-06-01": excArr},
			Seen:  map[string][24]bool{"2025-06-01": seen}, Source: "test",
		},
	}
	consum := map[time.Time]float64{mkHour(1, 0): 1.0}
	prod := map[time.Time]float64{mkHour(1, 12): 10.0} // tot l'excedent a l'hora de preu 0
	r := SimulateSurplus(consum, prod, preus, Scheme{Name: "regulada", Type: SchemeRegulated})
	assertFloat(t, r.GrossCompensation, 0.0, "compensació bruta amb preu 0 real")
	assertFloat(t, r.UsedCompensation, 0.0, "compensació usada amb preu 0 real")
}

func fill24(v float64) [24]float64 {
	var a [24]float64
	for i := range a {
		a[i] = v
	}
	return a
}

func assertFloat(t *testing.T, got, want float64, label string) {
	t.Helper()
	if math.Abs(got-want) > 0.01 {
		t.Errorf("%s: got %.4f, want %.4f", label, got, want)
	}
}
