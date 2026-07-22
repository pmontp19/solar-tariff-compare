package tariffcompare

import (
	"os"
	"strings"
	"testing"
)

func TestParseCCH_RealFile(t *testing.T) {
	// Fitxer real del directori pare (si existeix). Saltar si no hi és.
	path := "../../ES0031408050694001TF0F_20250501_20260526_Horario_CCH_CONS.csv"
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skip("fitxer CCH real no trobat")
	}
	info, err := ParseCCH(path, nil)
	if err != nil {
		t.Fatalf("ParseCCH: %v", err)
	}
	if info.Rows < 8000 {
		t.Errorf("esperava >8000 files, got %d", info.Rows)
	}
	// Consum anual esperat ~4313 kWh (corba completa d'un any)
	if info.ConsumptionSummary.Annual < 3800 || info.ConsumptionSummary.Annual > 4800 {
		t.Errorf("consum anual esperat ~4313, got %.0f", info.ConsumptionSummary.Annual)
	}
	// P1+P2+P3 ha de sumar l'anual
	sum := info.ConsumptionSummary.P1 + info.ConsumptionSummary.P2 + info.ConsumptionSummary.P3
	if diff := sum - info.ConsumptionSummary.Annual; diff > 1 || diff < -1 {
		t.Errorf("P1+P2+P3 (%.1f) != anual (%.1f)", sum, info.ConsumptionSummary.Annual)
	}
	t.Logf("Files: %d | Annual: %.0f kWh | P1=%.0f P2=%.0f P3=%.0f | forats=%d (%.0f%% estimat)",
		info.Rows, info.ConsumptionSummary.Annual, info.ConsumptionSummary.P1, info.ConsumptionSummary.P2, info.ConsumptionSummary.P3,
		info.Holes, info.EstimatedPct)
}

// El dia del canvi horari d'octubre té 25 hores i la CCH les etiqueta 1..25.
// Cal acceptar l'hora 25 (abans es descartava, perdent 1 kWh/any) i rebutjar-la
// en dies normals. El 26/10/2025 és el canvi d'hora (diumenge, 25 hores).
func TestParseCCH_CanviHoraOctubre(t *testing.T) {
	csv := "CUPS;Fecha;Hora;AE_kWh;REAL/ESTIMADO\n" +
		"CUPS1;26/10/2025;1;0,1;R\n" +
		"CUPS1;26/10/2025;24;0,2;R\n" +
		"CUPS1;26/10/2025;25;0,4;R\n" + // hora extra del dia de 25 hores
		"CUPS1;27/10/2025;25;9,9;R\n" // dia normal: hora 25 invàlida -> descartada
	info, err := ParseCCHReader(strings.NewReader(csv), nil)
	if err != nil {
		t.Fatalf("ParseCCHReader: %v", err)
	}
	if info.Rows != 3 {
		t.Errorf("Rows esperat 3 (l'hora 25 d'un dia normal es descarta), got %d", info.Rows)
	}
	// L'etiqueta 25 del dia de 25 hores comença a les 23:00 locals.
	v, ok := findHour(info.Curve.Consumption, 2025, 10, 26, 23)
	if !ok {
		t.Fatalf("no s'ha trobat l'hora 25 del dia del canvi (hauria de ser les 23:00)")
	}
	assertFloat(t, v, 0.4, "hora 25 del dia de 25 hores")
	// Les etiquetes 24 i 25 han de ser instants DIFERENTS (24 -> 22:00, 25 -> 23:00).
	if v24, ok := findHour(info.Curve.Consumption, 2025, 10, 26, 22); !ok || v24 != 0.2 {
		t.Errorf("hora 24 del dia de 25 hores esperada a les 22:00 amb 0.2, got %.2f (ok=%v)", v24, ok)
	}
}

// El dia del canvi de març té 23 hores: l'etiqueta 24 no existeix i s'ha de
// descartar. El 30/03/2025 és el canvi d'hora de primavera.
func TestParseCCH_CanviHoraMarc(t *testing.T) {
	csv := "CUPS;Fecha;Hora;AE_kWh;REAL/ESTIMADO\n" +
		"CUPS1;30/03/2025;23;0,3;R\n" + // última hora vàlida del dia de 23 hores
		"CUPS1;30/03/2025;24;9,9;R\n" // etiqueta inexistent -> descartada
	info, err := ParseCCHReader(strings.NewReader(csv), nil)
	if err != nil {
		t.Fatalf("ParseCCHReader: %v", err)
	}
	if info.Rows != 1 {
		t.Errorf("Rows esperat 1 (l'etiqueta 24 d'un dia de 23 hores es descarta), got %d", info.Rows)
	}
}

func TestParseCCH_FakeData(t *testing.T) {
	// Dades sintètiques: 1 dia feiner amb hores conegudes
	csv := "CUPS;Fecha;Hora;AE_kWh;REAL/ESTIMADO\n" +
		"ES0031400000000000TF0F;15/01/2025;1;0,2;R\n" + // 00:00 Valle
		"ES0031400000000000TF0F;15/01/2025;11;1,0;R\n" + // 10:00 Punta
		"ES0031400000000000TF0F;15/01/2025;15;0,5;R\n" + // 14:00 Llano
		"ES0031400000000000TF0F;19/01/2025;11;2,0;R\n" // diumenge 10:00 -> Valle
	info, err := ParseCCHReader(strings.NewReader(csv), nil)
	if err != nil {
		t.Fatalf("ParseCCHReader: %v", err)
	}
	if info.ConsumptionSummary.P1 != 1.0 {
		t.Errorf("P1 esperat 1.0, got %.2f", info.ConsumptionSummary.P1)
	}
	if info.ConsumptionSummary.P2 != 0.5 {
		t.Errorf("P2 esperat 0.5, got %.2f", info.ConsumptionSummary.P2)
	}
	// Valle = 0.2 + 2.0 = 2.2
	if diff := info.ConsumptionSummary.P3 - 2.2; diff > 0.01 || diff < -0.01 {
		t.Errorf("P3 esperat 2.2, got %.2f", info.ConsumptionSummary.P3)
	}
}
