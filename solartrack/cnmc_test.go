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
		CodigoPostal: "8001",
		Potencia:     3.45,
		Consum: ConsumAnalisi{
			Anual: 1400,
			P1:    400,
			P2:    400,
			P3:    600,
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
		CodigoPostal: "8001",
		Potencia:     3.45,
		Consum:       ConsumAnalisi{Anual: 1400, P1: 400, P2: 400, P3: 600},
		Autoconsum:   true,
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

func TestQuery_Validate(t *testing.T) {
	if err := (Query{}).validate(); err == nil {
		t.Error("esperava error sense codi postal")
	}
	if err := (Query{CodigoPostal: "8001"}).validate(); err == nil {
		t.Error("esperava error sense potència")
	}
	if err := (Query{CodigoPostal: "8001", Potencia: 3.45}).validate(); err != nil {
		t.Errorf("consulta vàlida va donar error: %v", err)
	}
}
