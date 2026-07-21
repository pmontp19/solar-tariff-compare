package solartrack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// datadisHour construeix la marca horària esperada (Europe/Madrid) per a
// comparacions amb .Equal() (First/Last). NO s'ha d'usar per indexar
// directament els mapes Curve.Consumption/Surplus/SelfConsumed: time.Time com
// a clau de mapa compara també el punter *Location, i time.LoadLocation pot
// tornar un punter diferent cada vegada que es crida encara que sigui la
// mateixa zona (vegeu el comentari a excedents_test.go). Per buscar dins dels
// mapes s'ha d'usar findHour, que compara per components (any/mes/dia/hora).
func datadisHour(year int, month time.Month, day, h int) time.Time {
	loc, _ := time.LoadLocation("Europe/Madrid")
	return time.Date(year, month, day, h, 0, 0, 0, loc)
}

// findHour cerca dins d'un mapa horari l'entrada que correspon a
// any/mes/dia/hora comparant per components (evita la trampa del punter
// *Location en la igualtat de time.Time com a clau de mapa).
func findHour(m map[time.Time]float64, year int, month time.Month, day, hour int) (float64, bool) {
	for k, v := range m {
		if k.Year() == year && k.Month() == month && k.Day() == day && k.Hour() == hour {
			return v, true
		}
	}
	return 0, false
}

// TestParseDatadisReader_DateTimeMapping comprova el mapeig "hora al final de
// l'interval": "01:00" -> hora 00 del mateix dia, "24:00" -> hora 23 del mateix dia.
func TestParseDatadisReader_DateTimeMapping(t *testing.T) {
	csv := "cups,date,time,consumptionKWh,obtainMethod,surplusEnergyKWh,generationEnergyKWh,selfConsumptionEnergyKWh\n" +
		"CUPS1,2025/07/01,01:00,0.317,Real,0.0,,\n" +
		"CUPS1,2025/07/01,24:00,0.500,Real,0.0,,\n"
	info, err := ParseDatadisReader(strings.NewReader(csv), nil)
	if err != nil {
		t.Fatalf("ParseDatadisReader: %v", err)
	}
	got01, ok := findHour(info.Curve.Consumption, 2025, time.July, 1, 0)
	if !ok {
		t.Fatalf("no s'ha trobat l'entrada per a 01:00 -> hora 00")
	}
	assertFloat(t, got01, 0.317, "01:00 -> hora 00 mateix dia")

	got24, ok := findHour(info.Curve.Consumption, 2025, time.July, 1, 23)
	if !ok {
		t.Fatalf("no s'ha trobat l'entrada per a 24:00 -> hora 23")
	}
	assertFloat(t, got24, 0.500, "24:00 -> hora 23 mateix dia")
}

// TestParseDatadisReader_DateFormats comprova que tant "YYYY/MM/DD" com
// "YYYY-MM-DD" (normalitzat internament substituint - per /) es parsegen igual.
func TestParseDatadisReader_DateFormats(t *testing.T) {
	csvSlash := "date,time,consumptionKWh\n2025/07/01,01:00,1.0\n"
	csvDash := "date,time,consumptionKWh\n2025-07-01,01:00,1.0\n"

	infoSlash, err := ParseDatadisReader(strings.NewReader(csvSlash), nil)
	if err != nil {
		t.Fatalf("ParseDatadisReader (slash): %v", err)
	}
	infoDash, err := ParseDatadisReader(strings.NewReader(csvDash), nil)
	if err != nil {
		t.Fatalf("ParseDatadisReader (dash): %v", err)
	}

	vSlash, ok := findHour(infoSlash.Curve.Consumption, 2025, time.July, 1, 0)
	if !ok {
		t.Fatalf("format YYYY/MM/DD: no s'ha trobat la clau esperada")
	}
	vDash, ok := findHour(infoDash.Curve.Consumption, 2025, time.July, 1, 0)
	if !ok {
		t.Fatalf("format YYYY-MM-DD: no s'ha trobat la clau esperada")
	}
	assertFloat(t, vSlash, vDash, "slash vs dash")
}

// TestParseDatadisReader_DecimalParsing comprova el parsing de decimals amb punt
// (format habitual de Datadis) i que una cel·la buida no s'afegeix (el consum de
// la fila es descarta). La tolerància a la coma es prova a nivell unitari a
// TestParseDotFloat_CommaTolerance (a través del CSV la coma xocaria amb el
// separador de camp).
func TestParseDatadisReader_DecimalParsing(t *testing.T) {
	csv := "date,time,consumptionKWh\n" +
		"2025/07/01,01:00,0.317\n" + // punt decimal
		"2025/07/01,03:00,\n" // buida -> no s'afegeix
	info, err := ParseDatadisReader(strings.NewReader(csv), nil)
	if err != nil {
		t.Fatalf("ParseDatadisReader: %v", err)
	}
	v, ok := findHour(info.Curve.Consumption, 2025, time.July, 1, 0)
	if !ok {
		t.Fatalf("no s'ha trobat l'entrada per a 01:00")
	}
	assertFloat(t, v, 0.317, "punt decimal")
	// La fila 03:00 amb consum buit -> parseDotFloat retorna ok=false, consumo=0,
	// per tant NO s'afegeix (consumo > 0 és fals) i no queda cap entrada al mapa.
	if _, ok := findHour(info.Curve.Consumption, 2025, time.July, 1, 2); ok {
		t.Errorf("consum buit no hauria d'afegir cap entrada")
	}
}

// TestParseDotFloat_CommaTolerance verifica directament que parseDotFloat
// accepta tant "0.317" com "0,317" (sense passar per CSV, per evitar l'ambigüitat
// del separador de camp).
func TestParseDotFloat_CommaTolerance(t *testing.T) {
	v, ok := parseDotFloat("0.317")
	if !ok {
		t.Fatalf("parseDotFloat(\"0.317\") hauria de ser vàlid")
	}
	assertFloat(t, v, 0.317, "punt decimal")

	v, ok = parseDotFloat("0,317")
	if !ok {
		t.Fatalf("parseDotFloat(\"0,317\") hauria de ser vàlid")
	}
	assertFloat(t, v, 0.317, "coma decimal tolerada")

	if _, ok := parseDotFloat(""); ok {
		t.Errorf("cadena buida no hauria de ser vàlida")
	}
	if _, ok := parseDotFloat("   "); ok {
		t.Errorf("cadena en blanc no hauria de ser vàlida")
	}
}

// TestParseDatadisReader_Surplus comprova que Curve.Surplus només es popula quan
// surplusEnergyKWh > 0; "0.0" queda absent del mapa.
func TestParseDatadisReader_Surplus(t *testing.T) {
	csv := "date,time,consumptionKWh,surplusEnergyKWh\n" +
		"2025/07/01,01:00,0.317,0.0\n" +
		"2025/07/01,13:00,0.010,2.177\n"
	info, err := ParseDatadisReader(strings.NewReader(csv), nil)
	if err != nil {
		t.Fatalf("ParseDatadisReader: %v", err)
	}
	if _, ok := findHour(info.Curve.Surplus, 2025, time.July, 1, 0); ok {
		t.Errorf("surplus 0.0 no hauria d'afegir cap entrada")
	}
	v, ok := findHour(info.Curve.Surplus, 2025, time.July, 1, 12)
	if !ok {
		t.Fatalf("no s'ha trobat l'entrada de surplus per a les 13:00")
	}
	assertFloat(t, v, 2.177, "surplus > 0")
}

// TestParseDatadisReader_EmptyGenerationSelfConsumption cobreix el cas habitual de
// Datadis: generationEnergyKWh i selfConsumptionEnergyKWh venen buits -> Curve.SelfConsumed
// queda buit i ConsumptionSummary.SelfConsumedKWh és 0.
func TestParseDatadisReader_EmptyGenerationSelfConsumption(t *testing.T) {
	csv := "cups,date,time,consumptionKWh,obtainMethod,surplusEnergyKWh,generationEnergyKWh,selfConsumptionEnergyKWh\n" +
		"CUPS1,2025/07/01,01:00,0.317,Real,0.0,,\n" +
		"CUPS1,2025/07/01,13:00,0.010,Real,2.177,,\n"
	info, err := ParseDatadisReader(strings.NewReader(csv), nil)
	if err != nil {
		t.Fatalf("ParseDatadisReader: %v", err)
	}
	if len(info.Curve.SelfConsumed) != 0 {
		t.Errorf("SelfConsumed hauria de quedar buit, got %d entrades", len(info.Curve.SelfConsumed))
	}
	assertFloat(t, info.ConsumptionSummary.SelfConsumedKWh, 0, "SelfConsumedKWh")
}

// TestParseDatadisReader_SelfConsumptionValue comprova que quan selfConsumptionEnergyKWh
// ve informat amb un valor, es suma tant a Curve.SelfConsumed com a SelfConsumedKWh.
func TestParseDatadisReader_SelfConsumptionValue(t *testing.T) {
	csv := "date,time,consumptionKWh,selfConsumptionEnergyKWh\n" +
		"2025/07/01,13:00,0.010,0.450\n" +
		"2025/07/01,14:00,0.020,0.300\n"
	info, err := ParseDatadisReader(strings.NewReader(csv), nil)
	if err != nil {
		t.Fatalf("ParseDatadisReader: %v", err)
	}
	v, ok := findHour(info.Curve.SelfConsumed, 2025, time.July, 1, 12)
	if !ok {
		t.Fatalf("no s'ha trobat l'entrada self-consumed per a les 13:00")
	}
	assertFloat(t, v, 0.450, "self consumed 13h")
	assertFloat(t, info.ConsumptionSummary.SelfConsumedKWh, 0.750, "suma total self consumed")
}

// TestParseDatadisReader_EstimatedPct comprova que "Estimada"/"Estimado" compten
// com a estimades i "Real" no, amb una barreja coneguda (2 de 4 -> 50%).
func TestParseDatadisReader_EstimatedPct(t *testing.T) {
	csv := "date,time,consumptionKWh,obtainMethod\n" +
		"2025/07/01,01:00,0.1,Real\n" +
		"2025/07/01,02:00,0.1,Estimada\n" +
		"2025/07/01,03:00,0.1,Estimado\n" +
		"2025/07/01,04:00,0.1,Real\n"
	info, err := ParseDatadisReader(strings.NewReader(csv), nil)
	if err != nil {
		t.Fatalf("ParseDatadisReader: %v", err)
	}
	assertFloat(t, info.EstimatedPct, 50.0, "percentatge estimat")
}

// TestParseDatadisReader_HeaderReorder comprova que la cabecera es localitza per
// nom, no per posició: reordenant columnes el resultat ha de ser idèntic.
func TestParseDatadisReader_HeaderReorder(t *testing.T) {
	csv := "obtainMethod,consumptionKWh,time,date,surplusEnergyKWh\n" +
		"Real,0.317,01:00,2025/07/01,0.0\n" +
		"Real,0.010,13:00,2025/07/01,2.177\n"
	info, err := ParseDatadisReader(strings.NewReader(csv), nil)
	if err != nil {
		t.Fatalf("ParseDatadisReader: %v", err)
	}
	vCons, ok := findHour(info.Curve.Consumption, 2025, time.July, 1, 0)
	if !ok {
		t.Fatalf("no s'ha trobat el consum de les 01:00 (cabecera reordenada)")
	}
	assertFloat(t, vCons, 0.317, "consum 01:00 (cabecera reordenada)")

	vSurplus, ok := findHour(info.Curve.Surplus, 2025, time.July, 1, 12)
	if !ok {
		t.Fatalf("no s'ha trobat el surplus de les 13:00 (cabecera reordenada)")
	}
	assertFloat(t, vSurplus, 2.177, "surplus 13:00 (cabecera reordenada)")

	if info.Rows != 2 {
		t.Errorf("Rows esperat 2, got %d", info.Rows)
	}
}

// TestIndexDatadisHeader_SpanishAliases comprova que indexDatadisHeader reconeix
// els alies en castellà (fecha, hora, consumokwh, etc.) a més dels noms Datadis.
func TestIndexDatadisHeader_SpanishAliases(t *testing.T) {
	header := []string{"fecha", "hora", "consumokwh", "excedente", "autoconsumokwh", "metodo"}
	idx := indexDatadisHeader(header)
	if idx.date != 0 {
		t.Errorf("date esperat índex 0, got %d", idx.date)
	}
	if idx.time != 1 {
		t.Errorf("time esperat índex 1, got %d", idx.time)
	}
	if idx.cons != 2 {
		t.Errorf("cons esperat índex 2, got %d", idx.cons)
	}
	if idx.surplus != 3 {
		t.Errorf("surplus esperat índex 3, got %d", idx.surplus)
	}
	if idx.self != 4 {
		t.Errorf("self esperat índex 4, got %d", idx.self)
	}
	if idx.method != 5 {
		t.Errorf("method esperat índex 5, got %d", idx.method)
	}
}

// TestParseDatadisReader_AnnualSum comprova que ConsumptionSummary.Annual és la
// suma de tot el consum de files amb consumo > 0, i que P1+P2+P3 quadra amb
// l'anual (repartiment concret verificat a més amb hores conegudes de dilluns).
func TestParseDatadisReader_AnnualSum(t *testing.T) {
	// 2025/07/07 és dilluns (laborable).
	csv := "date,time,consumptionKWh\n" +
		"2025/07/07,01:00,0.2\n" + // hora 00 -> Valle
		"2025/07/07,11:00,1.0\n" + // hora 10 -> Punta
		"2025/07/07,15:00,0.5\n" // hora 14 -> Llano
	info, err := ParseDatadisReader(strings.NewReader(csv), nil)
	if err != nil {
		t.Fatalf("ParseDatadisReader: %v", err)
	}
	assertFloat(t, info.ConsumptionSummary.Annual, 1.7, "consum anual")
	assertFloat(t, info.ConsumptionSummary.P1, 1.0, "P1 (Punta)")
	assertFloat(t, info.ConsumptionSummary.P2, 0.5, "P2 (Llano)")
	assertFloat(t, info.ConsumptionSummary.P3, 0.2, "P3 (Valle)")
	sum := info.ConsumptionSummary.P1 + info.ConsumptionSummary.P2 + info.ConsumptionSummary.P3
	assertFloat(t, sum, info.ConsumptionSummary.Annual, "P1+P2+P3 == anual")
}

// TestParseDatadisReader_FirstLastRows comprova Curve.First, Curve.Last i Rows per
// a una entrada petita i coneguda. Time.Equal compara l'instant absolut, no el
// punter *Location, per tant aquí és segur usar directament datadisHour().
func TestParseDatadisReader_FirstLastRows(t *testing.T) {
	csv := "date,time,consumptionKWh\n" +
		"2025/07/01,01:00,0.1\n" +
		"2025/07/02,12:00,0.2\n" +
		"2025/07/03,24:00,0.3\n"
	info, err := ParseDatadisReader(strings.NewReader(csv), nil)
	if err != nil {
		t.Fatalf("ParseDatadisReader: %v", err)
	}
	wantFirst := datadisHour(2025, time.July, 1, 0)
	wantLast := datadisHour(2025, time.July, 3, 23)
	if !info.Curve.First.Equal(wantFirst) {
		t.Errorf("First esperat %v, got %v", wantFirst, info.Curve.First)
	}
	if !info.Curve.Last.Equal(wantLast) {
		t.Errorf("Last esperat %v, got %v", wantLast, info.Curve.Last)
	}
	if info.Rows != 3 {
		t.Errorf("Rows esperat 3, got %d", info.Rows)
	}
}

// TestParseDatadisReader_MalformedRowsSkipped comprova que files amb massa poques
// columnes o data/hora no parsejable es descarten sense error fatal.
func TestParseDatadisReader_MalformedRowsSkipped(t *testing.T) {
	csv := "date,time,consumptionKWh\n" +
		"2025/07/01,01:00,0.1\n" + // vàlida
		"2025/07/01\n" + // massa poques columnes -> descartada
		"no-es-una-data,01:00,0.5\n" + // data no parsejable -> descartada
		"2025/07/01,no-es-una-hora,0.5\n" + // hora no parsejable -> descartada
		"2025/07/02,02:00,0.2\n" // vàlida
	info, err := ParseDatadisReader(strings.NewReader(csv), nil)
	if err != nil {
		t.Fatalf("ParseDatadisReader no hauria de fallar amb files malmeses: %v", err)
	}
	if info.Rows != 2 {
		t.Errorf("Rows esperat 2 (files vàlides), got %d", info.Rows)
	}
	assertFloat(t, info.ConsumptionSummary.Annual, 0.3, "consum anual (només files vàlides)")
}

// --- Tests que requereixen fitxer real (path), no io.Reader ---

// writeTempCSV escriu contingut a un fitxer temporal amb el nom donat dins de
// t.TempDir() i en retorna el path.
func writeTempCSV(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("escrivint fitxer temporal: %v", err)
	}
	return path
}

func TestIsDatadisCSV(t *testing.T) {
	datadisContent := "cups,date,time,consumptionKWh,obtainMethod,surplusEnergyKWh,generationEnergyKWh,selfConsumptionEnergyKWh\n" +
		"CUPS1,2025/07/01,01:00,0.317,Real,0.0,,\n"
	datadisPath := writeTempCSV(t, "datadis.csv", datadisContent)
	if !IsDatadisCSV(datadisPath) {
		t.Errorf("fitxer Datadis (coma + consumptionKWh) hauria de detectar-se com a Datadis")
	}

	cchContent := "CUPS;Fecha;Hora;AE_kWh;REAL/ESTIMADO\n" +
		"ES0031400000000000TF0F;15/01/2025;1;0,2;R\n"
	cchPath := writeTempCSV(t, "cch.csv", cchContent)
	if IsDatadisCSV(cchPath) {
		t.Errorf("fitxer CCH (separador ;) NO hauria de detectar-se com a Datadis")
	}
}

func TestParseConsumption_AutoRoutesDatadis(t *testing.T) {
	datadisContent := "cups,date,time,consumptionKWh,obtainMethod,surplusEnergyKWh,generationEnergyKWh,selfConsumptionEnergyKWh\n" +
		"CUPS1,2025/07/01,01:00,0.317,Real,0.0,,\n" +
		"CUPS1,2025/07/01,13:00,0.010,Real,2.177,,\n"
	path := writeTempCSV(t, "datadis.csv", datadisContent)
	info, err := ParseConsumption(path, nil)
	if err != nil {
		t.Fatalf("ParseConsumption: %v", err)
	}
	if info.Rows != 2 {
		t.Errorf("Rows esperat 2, got %d", info.Rows)
	}
	v, ok := findHour(info.Curve.Surplus, 2025, time.July, 1, 12)
	if !ok {
		t.Fatalf("no s'ha trobat l'entrada de surplus")
	}
	assertFloat(t, v, 2.177, "surplus via ParseConsumption (Datadis)")
}

func TestParseConsumption_AutoRoutesCCH(t *testing.T) {
	cchContent := "CUPS;Fecha;Hora;AE_kWh;REAL/ESTIMADO\n" +
		"ES0031400000000000TF0F;15/01/2025;1;0,2;R\n" +
		"ES0031400000000000TF0F;15/01/2025;11;1,0;R\n"
	path := writeTempCSV(t, "cch.csv", cchContent)
	info, err := ParseConsumption(path, nil)
	if err != nil {
		t.Fatalf("ParseConsumption: %v", err)
	}
	if info.Rows != 2 {
		t.Errorf("Rows esperat 2, got %d", info.Rows)
	}
	// Hora 11 (10:00, dimecres laborable) -> Punta
	assertFloat(t, info.ConsumptionSummary.P1, 1.0, "P1 via ParseConsumption (CCH)")
}

func TestParseDatadis_RealSampleFile(t *testing.T) {
	// Reutilitza examples/sample-datadis.csv com a fitxer real per exercitar
	// ParseDatadis (path complet, no reader).
	path := "../examples/sample-datadis.csv"
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skip("examples/sample-datadis.csv no trobat")
	}
	info, err := ParseDatadis(path, nil)
	if err != nil {
		t.Fatalf("ParseDatadis: %v", err)
	}
	if info.Rows == 0 {
		t.Errorf("esperava almenys una fila")
	}
	if !IsDatadisCSV(path) {
		t.Errorf("sample-datadis.csv hauria de detectar-se com a Datadis")
	}
}
