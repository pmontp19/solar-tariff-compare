package tariffcompare

import (
	"math"
	"testing"
	"time"
)

// Aquest fitxer prova la lògica de rànquing net d'excedents (ranking.go):
// LookupSurplusTerms, surplusRate, surplusCredit (sostre mensual i anual) i
// RankOffersWithSurplus. Reutilitza els helpers de excedents_test.go (mkHour,
// fill24, assertFloat) i datadisHour de datadis_test.go per a un segon mes.

// -------------------- LookupSurplusTerms --------------------

func TestLookupSurplusTerms(t *testing.T) {
	tests := []struct {
		name             string
		comercializadora string
		oferta           string
		wantName         string
		wantType         SchemeType
	}{
		// Els noms es refereixen al registre (no literals) perquè els termes reals
		// s'actualitzen; aquí es prova el mecanisme de match, no la cadena concreta.
		{"octopus per comercialitzadora", "Octopus Energy", "Tarifa Sol", RetailerRegistry["octopus"].Name, SchemeVirtualBattery},
		{"octopus en minúscules", "octopus energy", "tarifa solar", RetailerRegistry["octopus"].Name, SchemeVirtualBattery},
		{"insensible a majúscules", "OCTOPUS ENERGY", "SOLAR WALLET", RetailerRegistry["octopus"].Name, SchemeVirtualBattery},
		{"el text de l'oferta també compta", "Acme Corp", "Naturgy Solar Plan", RetailerRegistry["naturgy"].Name, SchemeVirtualBattery},
		// "EDP" no és al registre → cau en DefaultSurplusTerms (regulada).
		{"sense coincidència: sostre per defecte", "EDP", "One Luz", DefaultSurplusTerms.Name, SchemeRegulated},
		// La CNMC retorna noms amb accents; les claus del registre van sense.
		// Sense plegat de diacrítics, "Gana Energía" cauria al default en silenci.
		{"accents plegats (Energía)", "GANA ENERGÍA S.L.", "Monedero Solar", RetailerRegistry["gana energia"].Name, SchemeVirtualBattery},
		{"accents plegats (Som)", "SOM ENERGÍA, SCCL", "Tarifa 2.0TD", RetailerRegistry["som energia"].Name, SchemeRegulated},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := LookupSurplusTerms(tc.comercializadora, tc.oferta)
			if got.Name != tc.wantName {
				t.Errorf("Name: got %q, want %q", got.Name, tc.wantName)
			}
			if got.Type != tc.wantType {
				t.Errorf("Type: got %v, want %v", got.Type, tc.wantType)
			}
		})
	}
}

// Comprova que el cas sense coincidència retorna exactament DefaultSurplusTerms
// (mateix valor, no només mateix Name/Type).
func TestLookupSurplusTerms_SenseCoincidenciaRetornaDefault(t *testing.T) {
	got := LookupSurplusTerms("Comercialitzadora Inventada SA", "Tarifa X")
	if got != DefaultSurplusTerms {
		t.Errorf("esperava DefaultSurplusTerms, got %+v", got)
	}
}

// -------------------- surplusRate --------------------

func TestSurplusRate(t *testing.T) {
	tests := []struct {
		name  string
		terms SurplusTerms
		pvpcH float64
		excH  float64
		want  float64
	}{
		{
			"bateria virtual amb preu fix ignora pvpc/excedents",
			SurplusTerms{Type: SchemeVirtualBattery, Price: 0.03}, 0.20, 0.05, 0.03,
		},
		{
			"bateria virtual sense preu fix, a preu de consum",
			SurplusTerms{Type: SchemeVirtualBattery, AtConsumptionPrice: true}, 0.22, 0.05, 0.22,
		},
		{
			"bateria virtual sense preu fix, a preu d'excedents",
			SurplusTerms{Type: SchemeVirtualBattery, AtConsumptionPrice: false}, 0.22, 0.07, 0.07,
		},
		{
			"regulada amb preu fix",
			SurplusTerms{Type: SchemeRegulated, Price: 0.08}, 0.30, 0.40, 0.08,
		},
		{
			"indexada amb preu fix",
			SurplusTerms{Type: SchemeIndexed, Price: 0.08}, 0.30, 0.40, 0.08,
		},
		{
			"regulada sense preu fix: excedents*(1+coef)+prima",
			SurplusTerms{Type: SchemeRegulated, Coefficient: 0.2, Premium: 0.03}, 0.0, 0.10, 0.15,
		},
		{
			"indexada sense preu fix: excedents*(1+coef)+prima",
			SurplusTerms{Type: SchemeIndexed, Coefficient: 0.1, Premium: 0.02}, 0.0, 0.05, 0.075,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := surplusRate(tc.terms, tc.pvpcH, tc.excH)
			assertFloat(t, got, tc.want, tc.name)
		})
	}
}

// -------------------- surplusCredit: preu 0 i preu negatiu --------------------

// Un preu d'excedents 0 amb dada REAL (Seen marcat) s'ha de respectar, no
// substituir pel perfil mitjà: des del 2024 el 1739 val sovint 0 al migdia,
// exactament on hi ha l'excedent. Substituir-lo inflava la compensació.
func TestSurplusCredit_PreuZeroRealEsRespecta(t *testing.T) {
	seen := [24]bool{}
	for i := range seen {
		seen[i] = true
	}
	// Excedents: 0.10 tot el dia EXCEPTE les 12h, que val 0 (amb dada real).
	excArr := fill24(0.10)
	excArr[12] = 0.0
	prices := &HourlyPrices{
		PVPC: HourlySeries{
			ByDay: map[string][24]float64{"2025-06-01": fill24(0.20)},
			Seen:  map[string][24]bool{"2025-06-01": seen}, Source: "test",
		},
		Surplus: HourlySeries{
			ByDay: map[string][24]float64{"2025-06-01": excArr},
			Seen:  map[string][24]bool{"2025-06-01": seen}, Source: "test",
		},
	}
	gridImport := map[time.Time]float64{mkHour(1, 0): 100.0}            // sostre mensual alt (20€)
	surplus := map[time.Time]float64{mkHour(1, 12): 10.0}               // tot l'excedent a l'hora de preu 0
	terms := SurplusTerms{Name: "test-regulada", Type: SchemeRegulated} // rate = excH

	got := surplusCredit(gridImport, surplus, prices, terms, 0)
	assertFloat(t, got, 0.0, "preu 0 real -> compensació 0 (no perfil mitjà)")
}

// Un preu horari negatiu no pot generar compensació negativa (ningú cobra per
// abocar): la tarifa es retalla a 0.
func TestSurplusCredit_PreuNegatiuEsRetallaAZero(t *testing.T) {
	seen := [24]bool{}
	for i := range seen {
		seen[i] = true
	}
	excArr := fill24(-0.05) // preu negatiu tot el dia
	prices := &HourlyPrices{
		PVPC: HourlySeries{
			ByDay: map[string][24]float64{"2025-06-01": fill24(0.20)},
			Seen:  map[string][24]bool{"2025-06-01": seen}, Source: "test",
		},
		Surplus: HourlySeries{
			ByDay: map[string][24]float64{"2025-06-01": excArr},
			Seen:  map[string][24]bool{"2025-06-01": seen}, Source: "test",
		},
	}
	gridImport := map[time.Time]float64{mkHour(1, 0): 10.0}
	surplus := map[time.Time]float64{mkHour(1, 12): 10.0}
	terms := SurplusTerms{Name: "test-regulada", Type: SchemeRegulated}

	got := surplusCredit(gridImport, surplus, prices, terms, 0)
	if got < 0 {
		t.Fatalf("la compensació no pot ser negativa: %.4f", got)
	}
	assertFloat(t, got, 0.0, "preu negatiu -> compensació 0")
}

// -------------------- surplusCredit: sostre mensual --------------------

// Mirall de TestSimula_SostreMensual_Indexada (excedents_test.go): l'excedent
// supera de llarg el cost d'energia del mes, així que la compensació aplicada
// es limita al terme d'energia (proxy PVPC) d'aquell mes.
func TestSurplusCredit_SostreMensual(t *testing.T) {
	gridImport := map[time.Time]float64{mkHour(1, 0): 1.0} // 1 kWh a les 0h
	surplus := map[time.Time]float64{mkHour(1, 12): 100.0} // 100 kWh excedent a les 12h
	prices := &HourlyPrices{
		PVPC:    HourlySeries{ByDay: map[string][24]float64{"2025-06-01": fill24(0.15)}, Source: "test"},
		Surplus: HourlySeries{ByDay: map[string][24]float64{"2025-06-01": fill24(0.10)}, Source: "test"},
	}
	terms := SurplusTerms{Name: "test-indexada", Type: SchemeIndexed} // Price=0 -> rate = excH

	// energyCostPVPC = 1 kWh * 0.15 = 0.15 €; surplusValue = 100 kWh * 0.10 = 10.0 €
	// sostre mensual: min(10.0, 0.15) = 0.15
	got := surplusCredit(gridImport, surplus, prices, terms, 0)
	assertFloat(t, got, 0.15, "compensació amb sostre mensual")
}

// El sostre mensual s'aplica PER MES, no globalment: un mes amb excedent molt
// superior al consum (el sostre bindeja) i un altre amb excedent petit i molt
// de consum (el sostre no bindeja) han de sumar-se com a sostres independents.
func TestSurplusCredit_SostreMensual_EsPerMes(t *testing.T) {
	juny0 := mkHour(1, 0)
	juny12 := mkHour(1, 12)
	juliol0 := datadisHour(2025, time.July, 1, 0)
	juliol12 := datadisHour(2025, time.July, 1, 12)

	gridImport := map[time.Time]float64{juny0: 1.0, juliol0: 100.0}
	surplus := map[time.Time]float64{juny12: 50.0, juliol12: 2.0}
	prices := &HourlyPrices{
		PVPC: HourlySeries{ByDay: map[string][24]float64{
			"2025-06-01": fill24(0.20),
			"2025-07-01": fill24(0.20),
		}, Source: "test"},
		Surplus: HourlySeries{ByDay: map[string][24]float64{
			"2025-06-01": fill24(0.10),
			"2025-07-01": fill24(0.10),
		}, Source: "test"},
	}
	terms := SurplusTerms{Name: "test-indexada", Type: SchemeIndexed}

	// Juny: cost energia = 1*0.20 = 0.20; excedent brut = 50*0.10 = 5.0 -> sostre bindeja: min(5.0,0.20)=0.20
	// Juliol: cost energia = 100*0.20 = 20.0; excedent brut = 2*0.10 = 0.2 -> sostre NO bindeja: 0.2
	capJuny := 0.20
	valorJuliol := 0.2
	want := capJuny + valorJuliol // 0.40

	got := surplusCredit(gridImport, surplus, prices, terms, 0)
	assertFloat(t, got, want, "sostre mensual aplicat mes a mes")

	// Si el sostre s'apliqués GLOBALMENT (suma d'excedents vs suma de costos
	// d'energia de tots els mesos junts) el resultat seria min(5.2, 20.2) = 5.2,
	// molt diferent del 0.40 esperat. Ho deixem com a guarda de regressió.
	if math.Abs(got-5.2) < 0.01 {
		t.Fatalf("el sostre sembla aplicar-se globalment en comptes de mes a mes: got %.4f", got)
	}
}

// -------------------- surplusCredit: sostre anual --------------------

func TestSurplusCredit_SostreAnual(t *testing.T) {
	gridImport := map[time.Time]float64{mkHour(1, 0): 1.0}
	prices := &HourlyPrices{
		PVPC:    HourlySeries{ByDay: map[string][24]float64{"2025-06-01": fill24(0.20)}, Source: "test"},
		Surplus: HourlySeries{ByDay: map[string][24]float64{"2025-06-01": fill24(0.05)}, Source: "test"},
	}
	terms := SurplusTerms{Name: "test-bateria", Type: SchemeVirtualBattery, Price: 0.10, CeilingAnnual: true}

	t.Run("es limita a l'import anual quan el supera", func(t *testing.T) {
		surplus := map[time.Time]float64{mkHour(1, 12): 1000.0} // 1000*0.10 = 100€ bruts
		got := surplusCredit(gridImport, surplus, prices, terms, 50.0)
		assertFloat(t, got, 50.0, "compensació limitada a l'import anual")
	})

	t.Run("no es limita si el valor brut no supera l'import anual", func(t *testing.T) {
		surplus := map[time.Time]float64{mkHour(1, 12): 10.0} // 10*0.10 = 1.0€ bruts
		got := surplusCredit(gridImport, surplus, prices, terms, 500.0)
		assertFloat(t, got, 1.0, "compensació sense limitar")
	})

	t.Run("el sostre anual suma els excedents de tots els mesos", func(t *testing.T) {
		multiPrices := &HourlyPrices{
			PVPC: HourlySeries{ByDay: map[string][24]float64{
				"2025-06-01": fill24(0.20),
				"2025-07-01": fill24(0.20),
			}, Source: "test"},
			Surplus: HourlySeries{ByDay: map[string][24]float64{
				"2025-06-01": fill24(0.05),
				"2025-07-01": fill24(0.05),
			}, Source: "test"},
		}
		gi := map[time.Time]float64{mkHour(1, 0): 1.0, datadisHour(2025, time.July, 1, 0): 1.0}
		su := map[time.Time]float64{
			mkHour(1, 12):                       200.0, // 200*0.10 = 20€
			datadisHour(2025, time.July, 1, 12): 200.0, // 200*0.10 = 20€
		}
		got := surplusCredit(gi, su, multiPrices, terms, 1000.0)
		assertFloat(t, got, 40.0, "sostre anual: suma entre mesos abans d'aplicar el sostre")
	})
}

// -------------------- RankOffersWithSurplus --------------------

func TestRankOffersWithSurplus(t *testing.T) {
	offers := []Offer{
		{Comercializadora: "Octopus Energy", Oferta: "Solar Wallet", ImportePrimerAnio: 800.0},
		{Comercializadora: "ACME Energía", Oferta: "Tarifa Plana", ImportePrimerAnio: 750.0},
		{Comercializadora: "Nabalia Energía", Oferta: "Excedentes Plus", ImportePrimerAnio: 900.0},
	}
	gridImport := map[time.Time]float64{mkHour(1, 0): 5.0} // 5 kWh a les 0h
	surplus := map[time.Time]float64{mkHour(1, 12): 20.0}  // 20 kWh excedent a les 12h
	prices := &HourlyPrices{
		PVPC:    HourlySeries{ByDay: map[string][24]float64{"2025-06-01": fill24(0.20)}, Source: "test"},
		Surplus: HourlySeries{ByDay: map[string][24]float64{"2025-06-01": fill24(0.05)}, Source: "test"},
	}

	ranked := RankOffersWithSurplus(offers, gridImport, surplus, prices)
	if len(ranked) != 3 {
		t.Fatalf("esperava 3 ofertes, got %d", len(ranked))
	}

	// Els preus fixos es prenen del registre (es corregeixen amb el temps); així el
	// test comprova el mecanisme (atribució, sostre, ordre) i no un número congelat.
	octoPrice := RetailerRegistry["octopus"].Price // bateria virtual, sostre ANUAL
	nabaPrice := RetailerRegistry["nabalia"].Price // indexada preu fix, sostre MENSUAL

	// energyCostPVPC (comú a totes, mateix mes) = 5*0.20 = 1.0 €
	//
	// Octopus (bateria virtual, sostre ANUAL):
	//   surplusValue = 20*octoPrice -> no supera l'importAnual (800) -> credit = surplusValue
	//   net = 800 - credit
	// ACME -> sense coincidència -> DefaultSurplusTerms (regulada, sostre MENSUAL):
	//   rate = excH = 0.05 -> surplusValue = 20*0.05 = 1.0 -> sostre mensual: min(1.0, 1.0) = 1.0
	//   net = 750 - 1.0 = 749.0
	// Nabalia (indexada, preu fix ALT, sostre MENSUAL):
	//   surplusValue = 20*nabaPrice (=1.9) -> sostre mensual: min(1.9, 1.0) = 1.0
	//   net = 900 - 1.0 = 899.0
	//
	// Ordre ascendent per NetAnnualEUR: ACME (749.0) < Octopus (~799.2) < Nabalia (899.0)
	octoCredit := 20 * octoPrice
	octoNet := 800.0 - octoCredit
	wantOrder := []string{"ACME Energía", "Octopus Energy", "Nabalia Energía"}
	wantNet := []float64{749.0, octoNet, 899.0}
	wantCredit := []float64{1.0, octoCredit, 1.0}
	wantTerms := []string{DefaultSurplusTerms.Name, RetailerRegistry["octopus"].Name, RetailerRegistry["nabalia"].Name}
	// Precondició: el valor brut de Nabalia ha de superar el sostre mensual (1.0) perquè
	// el seu crèdit quedi retallat a 1.0 (si no, aquest cas ja no prova el sostre).
	if 20*nabaPrice <= 1.0 {
		t.Fatalf("test assumeix 20*%.3f > sostre mensual 1.0; ajusta el fixture", nabaPrice)
	}

	for i, r := range ranked {
		if r.Offer.Comercializadora != wantOrder[i] {
			t.Errorf("posició %d: got comercialitzadora %q, want %q", i, r.Offer.Comercializadora, wantOrder[i])
		}
		if r.SurplusTerms != wantTerms[i] {
			t.Errorf("posició %d: got SurplusTerms %q, want %q", i, r.SurplusTerms, wantTerms[i])
		}
		assertFloat(t, r.SurplusCreditEUR, wantCredit[i], "SurplusCreditEUR "+wantOrder[i])
		assertFloat(t, r.NetAnnualEUR, wantNet[i], "NetAnnualEUR "+wantOrder[i])
		// Invariant general de RankedOffer, independent dels valors concrets.
		assertFloat(t, r.NetAnnualEUR, r.Offer.ImportePrimerAnio-r.SurplusCreditEUR, "invariant net = importe - credit")
	}

	for i := 1; i < len(ranked); i++ {
		if ranked[i-1].NetAnnualEUR > ranked[i].NetAnnualEUR {
			t.Errorf("no ordenat ascendentment per NetAnnualEUR a la posició %d", i)
		}
	}
}

// -------------------- Registre: cuotes, caducitat i tope (pas 5 del HANDOFF) --------------------

// Comprova els valors exactes que HANDOFF pas 5 demana al registre: cuota mensual
// (Repsol, Endesa), caducitat del saldo (Naturgy, Iberdrola) i tope tipus Repsol
// (ThrottleFraction). La resta de comercialitzadores no han de portar cap d'aquests
// tres camps.
func TestRetailerRegistry_FeeExpiryThrottle(t *testing.T) {
	tests := []struct {
		key              string
		wantMonthlyFee   float64
		wantExpiryMonths int
		wantThrottle     float64
	}{
		{"repsol", 1.99, 0, 0.40},
		{"endesa", 2.0, 0, 0},
		{"naturgy", 0, 60, 0},
		{"iberdrola", 0, 24, 0},
		{"gana energia", 0, 0, 0}, // gratis el 1r any: MonthlyFee=0 pel número de PRIMER any
		{"octopus", 0, 0, 0},
		{"holaluz", 0, 0, 0},
		{"totalenergies", 0, 0, 0},
		{"total energies", 0, 0, 0},
		{"nabalia", 0, 0, 0},
		{"som energia", 0, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.key, func(t *testing.T) {
			got, ok := RetailerRegistry[tc.key]
			if !ok {
				t.Fatalf("clau %q no existeix al registre", tc.key)
			}
			assertFloat(t, got.MonthlyFee, tc.wantMonthlyFee, tc.key+".MonthlyFee")
			if got.ExpiryMonths != tc.wantExpiryMonths {
				t.Errorf("%s.ExpiryMonths: got %d, want %d", tc.key, got.ExpiryMonths, tc.wantExpiryMonths)
			}
			assertFloat(t, got.ThrottleFraction, tc.wantThrottle, tc.key+".ThrottleFraction")
		})
	}
}

// -------------------- surplusCredit: tope tipus Repsol (ThrottleFraction) --------------------

// El tope recorta el crèdit ABANS del sostre habitual: escala el valor de cada mes
// pel factor fullRateKWh/totalSurplusKWh. Es construeix un fixture d'alta exportació
// (molt més excedent que el fullRateKWh permès) amb preu fix (Price=0.10, bateria
// virtual) i un cost d'energia PVPC prou alt (pvpcH=2.0€/kWh sobre 100 kWh = 200€)
// perquè el sostre mensual NO bindegi en cap dels dos casos comparats: així s'aïlla
// l'efecte pur del throttle.
func TestSurplusCredit_Throttle(t *testing.T) {
	gridImport := map[time.Time]float64{mkHour(1, 0): 100.0} // 100 kWh de xarxa (consum anual de referència)
	surplus := map[time.Time]float64{mkHour(1, 12): 1000.0}  // 1000 kWh d'excedent: perfil de molta exportació
	prices := &HourlyPrices{
		PVPC:    HourlySeries{ByDay: map[string][24]float64{"2025-06-01": fill24(2.0)}, Source: "test"},
		Surplus: HourlySeries{ByDay: map[string][24]float64{"2025-06-01": fill24(0.05)}, Source: "test"},
	}
	base := SurplusTerms{Name: "test-throttle", Type: SchemeVirtualBattery, Price: 0.10, CeilingAnnual: false}
	withThrottle := base
	withThrottle.ThrottleFraction = 0.40 // com Repsol: 40% del consum anual de xarxa

	// Precondició del fixture: cal que hi hagi més excedent que el que permet el
	// tope, si no aquest test no prova res.
	totalGrid := 100.0
	totalSurplusKWh := 1000.0
	fullRateKWh := withThrottle.ThrottleFraction * totalGrid
	if totalSurplusKWh <= fullRateKWh {
		t.Fatalf("el fixture ha de tenir totalSurplusKWh (%.1f) > fullRateKWh (%.1f)", totalSurplusKWh, fullRateKWh)
	}

	gotNoThrottle := surplusCredit(gridImport, surplus, prices, base, 0)
	gotThrottle := surplusCredit(gridImport, surplus, prices, withThrottle, 0)

	// Sense throttle: surplusValue = 1000*0.10 = 100€; sostre mensual min(100, 200) = 100.
	assertFloat(t, gotNoThrottle, 100.0, "compensació sense throttle")

	factor := fullRateKWh / totalSurplusKWh // 40/1000 = 0.04
	want := factor * gotNoThrottle
	assertFloat(t, gotThrottle, want, "compensació amb throttle = factor * compensació sense throttle")
	assertFloat(t, gotThrottle, 4.0, "valor concret esperat amb throttle 40%")

	if gotThrottle >= gotNoThrottle {
		t.Fatalf("el throttle hauria de reduir la compensació: throttle=%.4f, sense=%.4f", gotThrottle, gotNoThrottle)
	}
}

// -------------------- surplusCredit / RankOffersWithSurplus: ExpiryMonths és metadata --------------------

// ExpiryMonths (caducitat del saldo) NO ha d'alterar el crèdit ni el net del PRIMER
// any: és informació de metadata perquè ImportePrimerAnio és el primer any, i una
// caducitat >= 12 mesos no arriba a retallar res dins d'aquest primer any.
func TestSurplusCredit_ExpiryMonthsEsMetadataNoAlteraElCredit(t *testing.T) {
	gridImport := map[time.Time]float64{mkHour(1, 0): 5.0}
	surplus := map[time.Time]float64{mkHour(1, 12): 20.0}
	prices := &HourlyPrices{
		PVPC:    HourlySeries{ByDay: map[string][24]float64{"2025-06-01": fill24(0.20)}, Source: "test"},
		Surplus: HourlySeries{ByDay: map[string][24]float64{"2025-06-01": fill24(0.05)}, Source: "test"},
	}
	base := SurplusTerms{Name: "test-expiry", Type: SchemeVirtualBattery, Price: 0.06, CeilingAnnual: true}
	withExpiry := base
	withExpiry.ExpiryMonths = 60 // com Naturgy

	gotBase := surplusCredit(gridImport, surplus, prices, base, 500.0)
	gotExpiry := surplusCredit(gridImport, surplus, prices, withExpiry, 500.0)
	assertFloat(t, gotExpiry, gotBase, "ExpiryMonths no ha d'alterar la compensació del primer any")

	// El camp ha de quedar emmagatzemat als termes (metadata informativa), encara
	// que surplusCredit no el faci servir per calcular.
	if withExpiry.ExpiryMonths != 60 {
		t.Fatalf("ExpiryMonths hauria de quedar emmagatzemat: got %d", withExpiry.ExpiryMonths)
	}
}

// -------------------- RankOffersWithSurplus: cuota mensual (AnnualFeeEUR) --------------------

// Una oferta d'una comercialitzadora amb MonthlyFee>0 (Endesa: 2€/mes) ha de reflectir
// AnnualFeeEUR = 12*MonthlyFee, i el net ha d'incloure la cuota SUMANT-LA (augmenta el
// cost, no el redueix): NetAnnualEUR = ImportePrimerAnio - SurplusCreditEUR + AnnualFeeEUR.
func TestRankOffersWithSurplus_Fee(t *testing.T) {
	offers := []Offer{
		{Comercializadora: "Endesa Energía", Oferta: "Solar Plus", ImportePrimerAnio: 700.0},
	}
	gridImport := map[time.Time]float64{mkHour(1, 0): 5.0}
	surplus := map[time.Time]float64{mkHour(1, 12): 10.0}
	prices := &HourlyPrices{
		PVPC:    HourlySeries{ByDay: map[string][24]float64{"2025-06-01": fill24(0.20)}, Source: "test"},
		Surplus: HourlySeries{ByDay: map[string][24]float64{"2025-06-01": fill24(0.05)}, Source: "test"},
	}

	ranked := RankOffersWithSurplus(offers, gridImport, surplus, prices)
	if len(ranked) != 1 {
		t.Fatalf("esperava 1 oferta, got %d", len(ranked))
	}
	r := ranked[0]

	endesaFee := RetailerRegistry["endesa"].MonthlyFee
	if endesaFee <= 0 {
		t.Fatalf("test assumeix RetailerRegistry[\"endesa\"].MonthlyFee > 0; ajusta el fixture")
	}
	wantFee := 12 * endesaFee
	assertFloat(t, r.AnnualFeeEUR, wantFee, "AnnualFeeEUR")
	assertFloat(t, r.NetAnnualEUR, r.Offer.ImportePrimerAnio-r.SurplusCreditEUR+r.AnnualFeeEUR, "NetAnnualEUR = importe - credit + cuota anual")
}
