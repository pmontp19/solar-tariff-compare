package tariffcompare

import (
	"testing"
	"time"
)

func TestOverlayProduction(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Madrid")
	// Consumption: dues hores, una amb sol i una sense
	consum := map[time.Time]float64{
		time.Date(2025, 6, 15, 13, 0, 0, 0, loc): 0.5, // migdia estiu, poc consum
		time.Date(2025, 6, 15, 23, 0, 0, 0, loc): 1.0, // nit, sense sol
	}
	// Perfil: al juny hora 13 = 3 kW; hora 23 = 0
	perfil := &ProductionProfile{}
	perfil.ByMonthHour[5][13] = 3.0 // juny (índex 5), 13h
	perfil.ByMonthHour[5][23] = 0

	r := OverlayProduction(consum, perfil)
	// Producció = 3 kWh (només 1 hora amb sol); autoconsum = min(3, 0.5)=0.5; excedents=2.5
	if r.ProductionKWh != 3.0 {
		t.Errorf("producció esperada 3.0, got %.2f", r.ProductionKWh)
	}
	if r.SelfConsumedKWh != 0.5 {
		t.Errorf("autoconsum esperat 0.5, got %.2f", r.SelfConsumedKWh)
	}
	if r.SurplusKWh != 2.5 {
		t.Errorf("excedents esperats 2.5, got %.2f", r.SurplusKWh)
	}
	if r.SelfConsumRatio < 0.16 || r.SelfConsumRatio > 0.17 {
		t.Errorf("índex autoconsum esperat ~0.167, got %.3f", r.SelfConsumRatio)
	}
}

func TestParsePVGISTime(t *testing.T) {
	cases := []struct {
		in      string
		mes, hr int
		ok      bool
	}{
		{"20210615:1300", 5, 13, true}, // juny, 13h
		{"20211201:0000", 11, 0, true}, // desembre, 0h
		{"bad", 0, 0, false},
	}
	for _, c := range cases {
		m, h, ok := parsePVGISTime(c.in)
		if ok != c.ok || (ok && (m != c.mes || h != c.hr)) {
			t.Errorf("parsePVGISTime(%q): got (%d,%d,%v), want (%d,%d,%v)",
				c.in, m, h, ok, c.mes, c.hr, c.ok)
		}
	}
}

// parsePVGISTime no ha de fer panic amb cadenes curtes (dada remota): el format
// vàlid té 13 caràcters mínim ("YYYYMMDD:HHMM").
func TestParsePVGISTime_CadenesCurtes(t *testing.T) {
	for _, s := range []string{"", "20210101", "20210101:0", "20210101:00", "20210101:001"} {
		if _, _, ok := parsePVGISTime(s); ok {
			t.Errorf("parsePVGISTime(%q) hauria de ser invàlid", s)
		}
	}
	m, h, ok := parsePVGISTime("20210615:1310")
	if !ok || m != 5 || h != 13 {
		t.Errorf("parsePVGISTime vàlid: got (mes=%d, hora=%d, ok=%v), want (5, 13, true)", m, h, ok)
	}
}

// TestFetchPVGISProfile_Live: test d'integració contra PVGIS (Barcelona, 3.5 kWp).
func TestFetchPVGISProfile_Live(t *testing.T) {
	if v := testEnv("TARIFFCOMPARE_SKIP_LIVE"); v != "" {
		t.Skip("test d'integració saltat")
	}
	perfil, err := FetchPVGISProfile(PVGISParams{
		Lat: 41.38, Lon: 2.17, PeakPower: 3.5, Angle: 35, Aspect: 0,
	})
	if err != nil {
		t.Fatalf("FetchPVGISProfile: %v", err)
	}
	// 3.5 kWp a Barcelona ~5000-6000 kWh/any
	if perfil.AnnualKWh < 4000 || perfil.AnnualKWh > 8000 {
		t.Errorf("producció anual esperada 4000-8000, got %.0f", perfil.AnnualKWh)
	}
	t.Logf("OK PVGIS: %.0f kWh/any (3.5 kWp BCN)", perfil.AnnualKWh)
}
