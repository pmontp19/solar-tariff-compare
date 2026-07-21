package solartrack

import (
	"testing"
)

// TestFetchOffers_Live és un test d'integració que fa una crida real a la CNMC.
// Saltar-lo amb: SOLARTRACK_SKIP_LIVE=1 go test ./...
func TestFetchOffers_Live(t *testing.T) {
	if v := testEnv("SOLARTRACK_SKIP_LIVE"); v != "" {
		t.Skip("test d'integració saltat (SOLARTRACK_SKIP_LIVE)")
	}
	q := Query{
		PostalCode: "8001",
		Power:      3.45,
		Consumption: ConsumptionSummary{
			Annual: 1400,
			P1:     400,
			P2:     400,
			P3:     600,
		},
	}
	offers, err := FetchOffers(q)
	if err != nil {
		t.Fatalf("FetchOffers: %v", err)
	}
	if len(offers) < 50 {
		t.Fatalf("esperava >50 ofertes, got %d", len(offers))
	}
	// Comprova que tenim dades útils
	if offers[0].ImportePrimerAnio <= 0 {
		t.Errorf("import primer any no vàlid: %v", offers[0].ImportePrimerAnio)
	}
	t.Logf("OK: %d ofertes. Més barata: %s - %s (%.2f €/any)",
		len(offers), offers[0].Comercializadora, offers[0].Oferta, offers[0].ImportePrimerAnio)
}

func TestBuildParams_RequiredFields(t *testing.T) {
	q := Query{
		PostalCode:      "8001",
		Power:           3.45,
		Consumption:     ConsumptionSummary{Annual: 1400, P1: 400, P2: 400, P3: 600},
		SelfConsumption: true,
	}
	p := buildParams(q)
	// Camps crítics que l'API exigeix (omissió -> HTTP 500)
	required := []string{
		"tipoSuministro", "codigoPostal", "tarifa", "potencia",
		"consumoAnualE", "consumoAnualEOrig", "consumoPrimeraFranja",
		"autoconsumo", "energiaAutoconsumo", "perfilConsumo",
		"dateInicio", "dateFin",
	}
	for _, k := range required {
		if p.Get(k) == "" {
			t.Errorf("camp requerit absent: %s", k)
		}
	}
	if p.Get("autoconsumo") != "true" {
		t.Errorf("autoconsumo esperat true, got %s", p.Get("autoconsumo"))
	}
}

func TestPartitionSuspectOffers(t *testing.T) {
	// Distribució realista: 10 ofertes agrupades ~1000 € i dues artefacte ~250 €
	// (com la "PVPC Histórico de referencia" de la CNMC, que no escala amb el consum).
	mk := func(imp float64, name string) Offer {
		return Offer{ImportePrimerAnio: imp, Comercializadora: name, Oferta: "x"}
	}
	offers := []Offer{
		mk(252.76, "PVPC Histórico"), // artefacte
		mk(980, "A"), mk(1000, "B"), mk(1010, "C"), mk(1020, "D"), mk(1030, "E"),
		mk(1040, "F"), mk(1050, "G"), mk(1060, "H"), mk(1070, "I"), mk(1080, "J"),
		mk(260.49, "NOSA"), // artefacte
	}
	clean, suspect := PartitionSuspectOffers(offers)
	if len(suspect) != 2 {
		t.Fatalf("esperava 2 sospitoses, got %d (%v)", len(suspect), suspect)
	}
	if len(clean) != 10 {
		t.Fatalf("esperava 10 netes, got %d", len(clean))
	}
	// Ordre d'entrada conservat i cap neta per sota del llindar (0.5 × mediana).
	for _, o := range clean {
		if o.ImportePrimerAnio < 500 {
			t.Errorf("oferta neta massa baixa: %.2f", o.ImportePrimerAnio)
		}
	}

	// Amb poques ofertes (<8) no filtra res.
	few := offers[:5]
	c2, s2 := PartitionSuspectOffers(few)
	if len(s2) != 0 || len(c2) != len(few) {
		t.Errorf("amb <8 ofertes no s'hauria de filtrar: clean=%d suspect=%d", len(c2), len(s2))
	}

	// Si massa (>25%) cauen com a sospitoses, no filtra (heurística no aplicable).
	half := []Offer{
		mk(100, "a"), mk(110, "b"), mk(120, "c"), mk(130, "d"),
		mk(1000, "e"), mk(1010, "f"), mk(1020, "g"), mk(1030, "h"),
	}
	c3, s3 := PartitionSuspectOffers(half)
	if len(s3) != 0 || len(c3) != len(half) {
		t.Errorf("amb >25%% sospitoses no s'hauria de filtrar: clean=%d suspect=%d", len(c3), len(s3))
	}
}

func TestQuery_Validate(t *testing.T) {
	if err := (Query{}).validate(); err == nil {
		t.Error("esperava error sense codi postal")
	}
	if err := (Query{PostalCode: "8001"}).validate(); err == nil {
		t.Error("esperava error sense potència")
	}
	if err := (Query{PostalCode: "8001", Power: 3.45}).validate(); err != nil {
		t.Errorf("consulta vàlida va donar error: %v", err)
	}
}
