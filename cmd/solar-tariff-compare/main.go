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
		cp         = flag.String("cp", "", "código postal (acepta 08001 o 8001) [obligatorio]")
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

	// 1. Carga la curva de consumo (auto-detecta formato Datadis o CCH e-distribución)
	info, err := st.ParseConsumption(*consumPath, nil)
	if err != nil {
		fatal("leyendo consumo: %v", err)
	}
	cs := info.ConsumptionSummary
	fmt.Fprintf(os.Stderr, "Consumo: %.0f kWh/año (P1=%.0f P2=%.0f P3=%.0f) | %d horas, %d huecos, %.0f%% estimado\n",
		cs.Annual, cs.P1, cs.P2, cs.P3, info.Rows, info.Holes, info.EstimatedPct)

	// 2. Producción FV: o bien CSV real (-prod), o bien estimación PVGIS (-kwp).
	// Si la curva de consumo YA trae excedentes reales (Datadis / CCH 7 columnas),
	// los datos reales mandan: se omite la estimación PVGIS y -kwp sólo se usa como
	// potencia instalada de autoconsumo en la consulta CNMC.
	hasRealSurplus := len(info.Curve.Surplus) > 0
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
	} else if *kwp > 0 && hasRealSurplus {
		fmt.Fprintln(os.Stderr, "aviso: la curva de consumo ya trae excedentes reales; se omite PVGIS y -kwp se usa sólo como potencia de autoconsumo para la CNMC.")
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

	// Excedentes: reales (Datadis / CCH de 7 columnas) o derivados de la producción
	// estimada por PVGIS. gridImport = consumo neto de red por hora.
	var surplusCurve map[time.Time]float64
	gridImport := info.Curve.Consumption
	if len(info.Curve.Surplus) > 0 {
		// Excedentes reales: el consumo ya es neto de red y ambos flujos son
		// mutuamente excluyentes por hora, así que sirven directamente.
		surplusCurve = info.Curve.Surplus
		if prodCurve == nil {
			// Para la simulación de esquemas basada en flujos reales, "producción" = excedentes.
			prodCurve = surplusCurve
		}
	} else if prodCurve != nil {
		// Deriva excedentes e importación hora a hora de la producción estimada.
		surplusCurve = map[time.Time]float64{}
		grid := map[time.Time]float64{}
		for t, c := range info.Curve.Consumption {
			p := prodCurve[t]
			if s := p - c; s > 0 {
				surplusCurve[t] = s
			}
			if b := c - p; b > 0 {
				grid[t] = b
			}
		}
		gridImport = grid
	}
	hasSurplus := len(surplusCurve) > 0

	// 3. Consulta CNMC
	// Para la consulta "con FV" reducimos el consumo según el autoconsumo FV (la parte
	// producida y consumida in situ no se paga a la red) y marcamos autoconsumo=true.
	// Con datos de Datadis el consumo YA es neto de red, así que energiaAutoconsumo=0.
	csFV := cs
	if *kwp > 0 || *prodCSV != "" {
		csFV.SelfConsumedKWh = autoResult.SelfConsumedKWh
	}
	hasFV := *kwp > 0 || *prodCSV != "" || hasSurplus
	offersFV, err := st.FetchOffers(st.Query{
		PostalCode:           *cp,
		Power:                *potencia,
		Consumption:          csFV,
		SelfConsumption:      hasFV,
		SelfConsumptionPower: *kwp, // kWp instalada; 0 → se aproxima con la contratada
	})
	if err != nil {
		fatal("CNMC: %v", err)
	}
	// Aparta las ofertas "artefacto" de la CNMC (importe que no escala con el consumo,
	// p.ej. la "PVPC Histórico de referencia") para que no contaminen el "más barato".
	offersFV, suspectFV := st.PartitionSuspectOffers(offersFV)
	reportSuspect(suspectFV)
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
		offersSinFV, _ = st.PartitionSuspectOffers(offersSinFV) // mismo filtro; aviso ya emitido
		sort.Slice(offersSinFV, func(i, j int) bool {
			return offersSinFV[i].ImportePrimerAnio < offersSinFV[j].ImportePrimerAnio
		})
	}

	// Precios horarios de e-sios: una única descarga compartida entre el ranking
	// neto y la simulación de esquemas (antes se descargaban hasta tres veces, y sin
	// token cada llamada podría devolver un "último día" distinto).
	var prices *st.HourlyPrices
	needPrices := (hasSurplus && len(offersFV) > 0) || (*sim && prodCurve != nil)
	if needPrices {
		prices, err = st.FetchHourlyPrices(info.Curve.First, info.Curve.Last)
		if err != nil {
			fmt.Fprintf(os.Stderr, "aviso: sin precios e-sios; no se calcula el ranking neto ni la simulación de excedentes: %v\n", err)
			prices = nil
		}
	}

	// Ranking integrado: atribuye a cada oferta la compensación de sus excedentes
	// reales según su comercializadora y ordena por coste NETO anual. Es lo que el
	// ranking crudo de la CNMC no hace (ignora excedentes).
	var ranked []st.RankedOffer
	if hasSurplus && len(offersFV) > 0 && prices != nil {
		ranked = st.RankOffersWithSurplus(offersFV, gridImport, surplusCurve, prices)
	}

	var schemes []st.SchemeResult
	if *sim && prodCurve != nil && prices != nil {
		schemes = st.CompareSchemes(info.Curve.Consumption, prodCurve, prices)
	}

	if *asJSON {
		printJSON(jsonInputs{
			info: info, offers: offersFV, suspect: suspectFV, auto: autoResult,
			top: *top, schemes: schemes, ranked: ranked, prices: prices,
		})
		return
	}
	printTaula(offersFV, *top, autoResult, hasFV)
	if len(ranked) > 0 {
		printRanking(ranked, *top)
	}
	if len(offersSinFV) > 0 {
		fmt.Printf("\n— Sin FV (referencia): la más barata es %.2f €/año —\n", offersSinFV[0].ImportePrimerAnio)
		if len(offersFV) > 0 {
			fmt.Printf("— Con FV: la más barata es %.2f €/año — ahorro %.2f €/año\n",
				offersFV[0].ImportePrimerAnio, offersSinFV[0].ImportePrimerAnio-offersFV[0].ImportePrimerAnio)
		}
	}
	if len(schemes) > 0 {
		printSim(schemes, prices)
	}
}

func printSim(res []st.SchemeResult, prices *st.HourlyPrices) {
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

// jsonConsumption resume la curva cargada: lo que antes sólo salía por stderr y
// un agente en modo -json perdía (calidad de los datos: huecos, % estimado, rango).
type jsonConsumption struct {
	AnnualKWh       float64 `json:"annual_kwh"`
	P1KWh           float64 `json:"p1_kwh"`
	P2KWh           float64 `json:"p2_kwh"`
	P3KWh           float64 `json:"p3_kwh"`
	SelfConsumedKWh float64 `json:"self_consumed_kwh"`
	SurplusKWh      float64 `json:"surplus_kwh"` // excedentes reales de la curva (0 si no hay)
	Rows            int     `json:"rows"`
	Holes           int     `json:"holes"`
	EstimatedPct    float64 `json:"estimated_pct"`
	FirstDate       string  `json:"first_date"`
	LastDate        string  `json:"last_date"`
}

type jsonPriceSource struct {
	PVPC    string `json:"pvpc"`    // "token" (histórico) o "latest" (un día representativo)
	Surplus string `json:"surplus"` // ídem para el indicador de excedentes
}

type jsonOut struct {
	SchemaVersion   int                      `json:"schema_version"`
	Consumption     jsonConsumption          `json:"consumption_summary"`
	Autoconsumption st.AutoconsumptionResult `json:"autoconsumption,omitempty"`
	NumOfertas      int                      `json:"num_offers"`
	TopOfertas      []st.Offer               `json:"top_offers"`
	SuspectOffers   []st.Offer               `json:"suspect_offers,omitempty"` // apartadas por importe no comparable
	PriceSource     *jsonPriceSource         `json:"price_source,omitempty"`
	Schemes         []st.SchemeResult        `json:"surplus_schemes,omitempty"`
	RankingNet      []st.RankedOffer         `json:"ranking_net,omitempty"`
}

type jsonInputs struct {
	info    *st.CCHInfo
	offers  []st.Offer
	suspect []st.Offer
	auto    st.AutoconsumptionResult
	top     int
	schemes []st.SchemeResult
	ranked  []st.RankedOffer
	prices  *st.HourlyPrices
}

func printJSON(in jsonInputs) {
	top := in.top
	if top > len(in.offers) {
		top = len(in.offers)
	}
	ranked := in.ranked
	if len(ranked) > top {
		ranked = ranked[:top]
	}
	surplusKWh := 0.0
	for _, v := range in.info.Curve.Surplus {
		surplusKWh += v
	}
	cs := in.info.ConsumptionSummary
	out := jsonOut{
		SchemaVersion: 2,
		Consumption: jsonConsumption{
			AnnualKWh: cs.Annual, P1KWh: cs.P1, P2KWh: cs.P2, P3KWh: cs.P3,
			SelfConsumedKWh: cs.SelfConsumedKWh, SurplusKWh: surplusKWh,
			Rows: in.info.Rows, Holes: in.info.Holes, EstimatedPct: in.info.EstimatedPct,
			FirstDate: in.info.Curve.First.Format("2006-01-02"),
			LastDate:  in.info.Curve.Last.Format("2006-01-02"),
		},
		Autoconsumption: in.auto,
		NumOfertas:      len(in.offers),
		TopOfertas:      in.offers[:top],
		SuspectOffers:   in.suspect,
		Schemes:         in.schemes,
		RankingNet:      ranked,
	}
	if in.prices != nil {
		out.PriceSource = &jsonPriceSource{PVPC: in.prices.PVPC.Source, Surplus: in.prices.Surplus.Source}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(out)
}

// printRanking muestra el ranking neto integrando la compensación de excedentes.
func printRanking(ranked []st.RankedOffer, top int) {
	if top > len(ranked) {
		top = len(ranked)
	}
	fmt.Println("\n— Ranking NETO con excedentes reales (importe CNMC − compensación + cuota) —")
	fmt.Println("  (compensación y cuota atribuidas por comercializadora; verifica precios en su web)")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\t%s\n", "€/año NETO", "Importe CNMC", "Compensado", "Cuota/año", "Comercializadora", "Excedentes")
	for _, r := range ranked[:top] {
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\t%s\n",
			formatEUR(r.NetAnnualEUR), formatEUR(r.Offer.ImportePrimerAnio),
			formatEUR(r.SurplusCreditEUR), formatEUR(r.AnnualFeeEUR),
			truncar(r.Offer.Comercializadora, 26), truncar(r.SurplusTerms, 30))
	}
	w.Flush()
}

// reportSuspect avisa (por stderr) de las ofertas apartadas por no ser comparables:
// su importePrimerAnio no escala con el consumo (p.ej. la "PVPC Histórico de referencia"
// de la CNMC, calculada sobre un perfil de referencia). Se apartan para que no aparezcan
// como la opción "más barata".
func reportSuspect(suspect []st.Offer) {
	if len(suspect) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "aviso: %d oferta(s) apartada(s) por importe no comparable (no escala con el consumo):\n", len(suspect))
	for _, o := range suspect {
		fmt.Fprintf(os.Stderr, "  · %.2f € — %s / %s\n", o.ImportePrimerAnio, o.Comercializadora, o.Oferta)
	}
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
