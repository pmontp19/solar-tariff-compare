// Command solar-tariff-compare compara ofertas de electricidad de la CNMC usando
// la curva de consumo real (CCH) y, opcionalmente, una estimación de producción FV
// (PVGIS) para modelar el autoconsumo y los excedentes.
//
// Uso:
//
//	solar-tariff-compare -consum curva.csv -cp 8001 -potencia 3.45 \
//	    [-kwp 4.1 -lat 41.38 -lon 2.17 -angle 35 -aspect 0] [-sim] [-top 20] [-json]
//
// Sin parámetros FV (-kwp) compara sólo con consumo real. Con FV muestra además la
// comparativa con/sin autoconsumo; con -sim simula esquemas de excedentes.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"text/tabwriter"
	"time"

	st "github.com/pmontp19/solar-tariff-compare/solartrack"
)

func main() {
	var (
		consumPath = flag.String("consum", "", "fichero CSV de la curva horaria (CCH e-distribución) [obligatorio]")
		cp         = flag.String("cp", "", "código postal sin el 0 inicial (p.ej. 08001 → 8001) [obligatorio]")
		potencia   = flag.Float64("potencia", 3.45, "potencia contratada en kW (2.0TD)")
		top        = flag.Int("top", 20, "número de ofertas a mostrar")

		// Autoconsumo FV (opcional). Si -kwp > 0, se estima la producción con PVGIS.
		kwp    = flag.Float64("kwp", 0, "potencia FV de pico en kW (activa estimación PVGIS)")
		lat    = flag.Float64("lat", 41.38, "latitud para PVGIS")
		lon    = flag.Float64("lon", 2.17, "longitud para PVGIS")
		angle  = flag.Float64("angle", 35, "inclinación de los paneles en grados")
		aspect = flag.Float64("aspect", 0, "orientación en grados (0=sur, -90=este, 90=oeste)")
		loss   = flag.Float64("loss", 14, "pérdidas del sistema FV en %")

		asJSON   = flag.Bool("json", false, "muestra la salida en JSON")
		sinSolar = flag.Bool("sin-solar", false, "con FV: muestra también la comparativa sin FV")

		// Simulación de excedentes (agent-first). Con -sim y FV, compara los esquemas
		// de compensación (regulada / indexada / batería virtual) hora a hora.
		sim     = flag.Bool("sim", false, "con FV: simula esquemas de excedentes (regulada/indexada/batería virtual) con precios e-sios")
		prodCSV = flag.String("prod", "", "CSV de producción real (variante de 7 columnas con AS_KWh/AE_AUTOCONS); sustituye a PVGIS")
	)
	flag.Parse()

	if *consumPath == "" || *cp == "" {
		fmt.Fprintln(os.Stderr, "Uso: solar-tariff-compare -consum <curva.csv> -cp <código_postal> [opciones]")
		flag.PrintDefaults()
		os.Exit(2)
	}

	// 1. Carga la curva de consumo
	info, err := st.ParseCCH(*consumPath, nil)
	if err != nil {
		fatal("leyendo consumo: %v", err)
	}
	cs := info.ConsumptionSummary
	fmt.Fprintf(os.Stderr, "Consumo: %.0f kWh/año (P1=%.0f P2=%.0f P3=%.0f) | %d horas, %d huecos, %.0f%% estimado\n",
		cs.Annual, cs.P1, cs.P2, cs.P3, info.Rows, info.Holes, info.EstimatedPct)

	// 2. Producción FV: o bien CSV real (-prod), o bien estimación PVGIS (-kwp).
	var perfil *st.ProductionProfile
	var autoResult st.AutoconsumptionResult
	var prodCurve map[time.Time]float64 // curva de producción para la simulación de excedentes
	if *prodCSV != "" {
		// CSV real con columnas AS_KWh (excedentes) y AE_AUTOCONS_kWh (autoconsumo)
		pinfo, err := st.ParseCCH(*prodCSV, nil)
		if err != nil {
			fatal("leyendo producción: %v", err)
		}
		prodCurve = pinfo.Curve.TotalProduction()
		autoResult = st.OverlayCurves(info.Curve.Consumption, prodCurve)
		fmt.Fprintf(os.Stderr, "Producción real: %.0f kWh/año | autoconsumo %.0f kWh (%.0f%%) | excedentes %.0f kWh | cobertura %.0f%%\n",
			autoResult.ProductionKWh, autoResult.SelfConsumedKWh, autoResult.SelfConsumRatio*100,
			autoResult.SurplusKWh, autoResult.Coverage*100)
	} else if *kwp > 0 {
		fmt.Fprintf(os.Stderr, "Estimando producción FV con PVGIS (%.2f kWp, lat %.3f lon %.3f, ángulo %.0f aspect %.0f)...\n",
			*kwp, *lat, *lon, *angle, *aspect)
		perfil, err = st.FetchPVGISProfile(st.PVGISParams{
			Lat: *lat, Lon: *lon, PeakPower: *kwp, Angle: *angle, Aspect: *aspect, Loss: *loss,
		})
		if err != nil {
			fatal("PVGIS: %v", err)
		}
		prodCurve = perfil.Apply(info.Curve.Consumption)
		autoResult = st.OverlayProduction(info.Curve.Consumption, perfil)
		fmt.Fprintf(os.Stderr, "Producción FV: %.0f kWh/año | autoconsumo %.0f kWh (%.0f%%) | excedentes %.0f kWh | cobertura del consumo %.0f%%\n",
			autoResult.ProductionKWh, autoResult.SelfConsumedKWh, autoResult.SelfConsumRatio*100,
			autoResult.SurplusKWh, autoResult.Coverage*100)
	}

	// 3. Consulta CNMC
	// Para la consulta "con FV" reducimos el consumo según el autoconsumo FV (la parte
	// producida y consumida in situ no se paga a la red) y marcamos autoconsumo=true.
	csFV := cs
	if *kwp > 0 || *prodCSV != "" {
		csFV.SelfConsumedKWh = autoResult.SelfConsumedKWh
	}
	hasFV := *kwp > 0 || *prodCSV != ""
	offersFV, err := st.FetchOffers(st.Query{
		PostalCode:      *cp,
		Power:           *potencia,
		Consumption:     csFV,
		SelfConsumption: hasFV,
	})
	if err != nil {
		fatal("CNMC: %v", err)
	}
	sort.Slice(offersFV, func(i, j int) bool {
		return offersFV[i].ImportePrimerAnio < offersFV[j].ImportePrimerAnio
	})

	var offersSinFV []st.Offer
	if hasFV && *sinSolar {
		offersSinFV, err = st.FetchOffers(st.Query{
			PostalCode:  *cp,
			Power:       *potencia,
			Consumption: cs, // sin autoconsumo
		})
		if err != nil {
			fatal("CNMC (sin FV): %v", err)
		}
		sort.Slice(offersSinFV, func(i, j int) bool {
			return offersSinFV[i].ImportePrimerAnio < offersSinFV[j].ImportePrimerAnio
		})
	}

	if *asJSON {
		printJSON(offersFV, autoResult, *top, schemesIfSim(*sim, prodCurve, info))
		return
	}
	printTaula(offersFV, *top, autoResult, hasFV)
	if len(offersSinFV) > 0 {
		fmt.Printf("\n— Sin FV (referencia): la más barata es %.2f €/año —\n", offersSinFV[0].ImportePrimerAnio)
		if len(offersFV) > 0 {
			fmt.Printf("— Con FV: la más barata es %.2f €/año — ahorro %.2f €/año\n",
				offersFV[0].ImportePrimerAnio, offersSinFV[0].ImportePrimerAnio-offersFV[0].ImportePrimerAnio)
		}
	}
	if *sim && prodCurve != nil {
		printSim(info, prodCurve)
	}
}

// schemesIfSim ejecuta la simulación de excedentes si -sim está activo; devuelve nil en caso contrario.
func schemesIfSim(doSim bool, prodCurve map[time.Time]float64, info *st.CCHInfo) []st.SchemeResult {
	if !doSim || prodCurve == nil {
		return nil
	}
	prices, err := st.FetchHourlyPrices(info.Curve.First, info.Curve.Last)
	if err != nil {
		fmt.Fprintf(os.Stderr, "aviso (sim): no se han podido obtener precios e-sios: %v\n", err)
		return nil
	}
	return st.CompareSchemes(info.Curve.Consumption, prodCurve, prices)
}

func printSim(info *st.CCHInfo, prodCurve map[time.Time]float64) {
	prices, err := st.FetchHourlyPrices(info.Curve.First, info.Curve.Last)
	if err != nil {
		fmt.Fprintf(os.Stderr, "aviso (sim): %v\n", err)
		return
	}
	res := st.CompareSchemes(info.Curve.Consumption, prodCurve, prices)
	fmt.Println("\n— Simulación de excedentes (término de energía neto, sin fijos) —")
	fmt.Printf("  fuente precios: PVPC=%s  excedentes=%s\n", prices.PVPC.Source, prices.Surplus.Source)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\n", "€/año energía", "Compensado", "Perdido", "Exc kWh", "Esquema")
	for _, r := range res {
		fmt.Fprintf(w, "  %.2f\t%.2f\t%.2f\t%.0f\t%s\n",
			r.NetEnergy, r.UsedCompensation, r.LostCompensation, r.SurplusKWh, r.Scheme.Name)
	}
	w.Flush()
	if len(res) >= 2 {
		fmt.Printf("\n  Mejor: %s (%.2f €/año) | peor: %s (%.2f €/año) → diferencia %.2f €/año\n",
			res[0].Scheme.Name, res[0].NetEnergy, res[len(res)-1].Scheme.Name, res[len(res)-1].NetEnergy,
			res[len(res)-1].NetEnergy-res[0].NetEnergy)
	}
}

func printTaula(offers []st.Offer, top int, auto st.AutoconsumptionResult, conFV bool) {
	if len(offers) == 0 {
		fmt.Println("(ninguna oferta)")
		return
	}
	if top > len(offers) {
		top = len(offers)
	}
	tipo := func(o st.Offer) string {
		if o.Tipo != nil {
			return *o.Tipo
		}
		if o.TipoElectricidad == "TI" {
			return "Indexado"
		}
		return "Fijo"
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if conFV {
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\t%s\n", "€/año", "Comercializadora", "Oferta", "Tipo", "Verde", "Autocons")
	} else {
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\n", "€/año", "Comercializadora", "Oferta", "Tipo", "Verde")
	}
	for _, o := range offers[:top] {
		verde := "—"
		if o.Verde {
			verde = "✓"
		}
		autoCol := "—"
		if o.Autoconsum {
			autoCol = "✓"
		}
		nombre := truncar(o.Comercializadora, 26)
		oferta := truncar(o.Oferta, 34)
		if conFV {
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\t%s\n",
				formatEUR(o.ImportePrimerAnio), nombre, oferta, truncar(tipo(o), 12), verde, autoCol)
		} else {
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\n",
				formatEUR(o.ImportePrimerAnio), nombre, oferta, truncar(tipo(o), 12), verde)
		}
	}
	w.Flush()
	fmt.Printf("\nMostrando %d de %d ofertas.\n", top, len(offers))
}

type jsonOut struct {
	Autoconsumption st.AutoconsumptionResult `json:"autoconsumption,omitempty"`
	NumOfertas      int                      `json:"num_offers"`
	TopOfertas      []st.Offer               `json:"top_offers"`
	Schemes         []st.SchemeResult        `json:"surplus_schemes,omitempty"`
}

func printJSON(offers []st.Offer, auto st.AutoconsumptionResult, top int, schemes []st.SchemeResult) {
	if top > len(offers) {
		top = len(offers)
	}
	out := jsonOut{Autoconsumption: auto, NumOfertas: len(offers), TopOfertas: offers[:top], Schemes: schemes}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(out)
}

func formatEUR(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64) + " €"
}

func truncar(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", a...)
	os.Exit(1)
}
