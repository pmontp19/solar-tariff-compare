package solartrack

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
	if info.ConsumAnalisi.Anual < 3800 || info.ConsumAnalisi.Anual > 4800 {
		t.Errorf("consum anual esperat ~4313, got %.0f", info.ConsumAnalisi.Anual)
	}
	// P1+P2+P3 ha de sumar l'anual
	sum := info.ConsumAnalisi.P1 + info.ConsumAnalisi.P2 + info.ConsumAnalisi.P3
	if diff := sum - info.ConsumAnalisi.Anual; diff > 1 || diff < -1 {
		t.Errorf("P1+P2+P3 (%.1f) != anual (%.1f)", sum, info.ConsumAnalisi.Anual)
	}
	t.Logf("Files: %d | Anual: %.0f kWh | P1=%.0f P2=%.0f P3=%.0f | forats=%d (%.0f%% estimat)",
		info.Rows, info.ConsumAnalisi.Anual, info.ConsumAnalisi.P1, info.ConsumAnalisi.P2, info.ConsumAnalisi.P3,
		info.Holes, info.EstimatedPct)
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
	if info.ConsumAnalisi.P1 != 1.0 {
		t.Errorf("P1 esperat 1.0, got %.2f", info.ConsumAnalisi.P1)
	}
	if info.ConsumAnalisi.P2 != 0.5 {
		t.Errorf("P2 esperat 0.5, got %.2f", info.ConsumAnalisi.P2)
	}
	// Valle = 0.2 + 2.0 = 2.2
	if diff := info.ConsumAnalisi.P3 - 2.2; diff > 0.01 || diff < -0.01 {
		t.Errorf("P3 esperat 2.2, got %.2f", info.ConsumAnalisi.P3)
	}
}
