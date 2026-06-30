// Command solar-tariff-compare compara ofertes d'electricitat de la CNMC usant
// la corba de consum real (CCH) i, opcionalment, una estimació de producció FV
// (PVGIS) per modelar l'autoconsum.
//
// Ús:
//
//	solar-tariff-compare -consum corba.csv -cp 08001 -potencia 3.45 \
//	    [-kwp 3.5 -lat 41.38 -lon 2.17 -angle 35 -aspect 0] [-top 20] [-json]
//
// Sense paràmetres FV (-kwp) compara només amb consum real. Amb FV, a més mostra
// la comparativa amb/sense autoconsum i l'estalvi.
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
		consumPath = flag.String("consum", "", "fitxer CSV de la corba horària (CCH e-distribución) [obligatori]")
		cp         = flag.String("cp", "", "codi postal sense el 0 inicial (p.ex. 08001 → 8001) [obligatori]")
		potencia   = flag.Float64("potencia", 3.45, "potència contractada en kW (2.0TD)")
		top        = flag.Int("top", 20, "nombre d'ofertes a mostrar")

		// Autoconsum FV (opcional). Si -kwp > 0, s'estima la producció amb PVGIS.
		kwp    = flag.Float64("kwp", 0, "potència FV de pic en kW (activa estimació PVGIS)")
		lat    = flag.Float64("lat", 41.38, "latitud per PVGIS")
		lon    = flag.Float64("lon", 2.17, "longitud per PVGIS")
		angle  = flag.Float64("angle", 35, "inclinació dels panells en graus")
		aspect = flag.Float64("aspect", 0, "orientació en graus (0=sud, -90=est, 90=oest)")
		loss   = flag.Float64("loss", 14, "pèrdues del sistema FV en %")

		asJSON   = flag.Bool("json", false, "mostra la sortida en JSON")
		sinSolar = flag.Bool("sense-solar", false, "amb FV: també mostra la comparativa sense FV")

		// Simulació d'excedents (agent-first). Amb -sim i FV, compara els esquemes
		// de compensació (regulada / indexada / bateria virtual) hora a hora.
		sim     = flag.Bool("sim", false, "amb FV: simula esquemes d'excedents (regulada/indexada/bateria virtual) amb preus e-sios")
		prodCSV = flag.String("prod", "", "CSV de producció real (variant 7 columnes amb AS_KWh/AE_AUTOCONS); substitueix PVGIS")
	)
	flag.Parse()

	if *consumPath == "" || *cp == "" {
		fmt.Fprintln(os.Stderr, "Ús: solar-tariff-compare -consum <corba.csv> -cp <codi_postal> [opcions]")
		flag.PrintDefaults()
		os.Exit(2)
	}

	// 1. Carrega la corba de consum
	info, err := st.ParseCCH(*consumPath, nil)
	if err != nil {
		fatal("llegint consum: %v", err)
	}
	ca := info.ConsumAnalisi
	fmt.Fprintf(os.Stderr, "Consum: %.0f kWh/any (P1=%.0f P2=%.0f P3=%.0f) | %d hores, %d forats, %.0f%% estimat\n",
		ca.Anual, ca.P1, ca.P2, ca.P3, info.Rows, info.Holes, info.EstimatedPct)

	// 2. Producció FV: o bé CSV real (-prod), o bé estimació PVGIS (-kwp).
	var perfil *st.PerfilProd
	var resultatAuto st.ResultatAutoconsum
	var prodCurve map[time.Time]float64 // corba de producció per a la simulació d'excedents
	if *prodCSV != "" {
		// CSV real amb columnes AS_KWh (excedents) i AE_AUTOCONS_kWh (autoconsum)
		pinfo, err := st.ParseCCH(*prodCSV, nil)
		if err != nil {
			fatal("llegint producció: %v", err)
		}
		prodCurve = pinfo.Corba.ProduccioTotal()
		resultatAuto = st.OverlayCurves(info.Corba.Consum, prodCurve)
		fmt.Fprintf(os.Stderr, "Producció real: %.0f kWh/any | autoconsum %.0f kWh (%.0f%%) | excedents %.0f kWh | cobertura %.0f%%\n",
			resultatAuto.ProduccioKWh, resultatAuto.AutoconsumKWh, resultatAuto.IndexAutocons*100,
			resultatAuto.ExcedentsKWh, resultatAuto.Cobertura*100)
	} else if *kwp > 0 {
		fmt.Fprintf(os.Stderr, "Estimant producció FV amb PVGIS (%.2f kWp, lat %.3f lon %.3f, angle %.0f aspect %.0f)...\n",
			*kwp, *lat, *lon, *angle, *aspect)
		perfil, err = st.FetchPerfilPVGIS(st.PVGISParams{
			Lat: *lat, Lon: *lon, PeakPower: *kwp, Angle: *angle, Aspect: *aspect, Loss: *loss,
		})
		if err != nil {
			fatal("PVGIS: %v", err)
		}
		prodCurve = perfil.Aplicar(info.Corba.Consum)
		resultatAuto = st.OverlayProduccio(info.Corba.Consum, perfil)
		fmt.Fprintf(os.Stderr, "Producció FV: %.0f kWh/any | autoconsum %.0f kWh (%.0f%%) | excedents %.0f kWh | cobertura del consum %.0f%%\n",
			resultatAuto.ProduccioKWh, resultatAuto.AutoconsumKWh, resultatAuto.IndexAutocons*100,
			resultatAuto.ExcedentsKWh, resultatAuto.Cobertura*100)
	}

	// 3. Consulta CNMC
	// Per la consulta "amb FV" reduïm el consum segons l'autoconsum FV (la part
	// produïda i consumida in situ no es paga a la xarxa) i marco autoconsumo=true.
	caFV := ca
	if *kwp > 0 {
		caFV.AutoconsumKWh = resultatAuto.AutoconsumKWh
	}
	offersFV, err := st.FetchOffers(st.Query{
		CodigoPostal: *cp,
		Potencia:     *potencia,
		Consum:       caFV,
		Autoconsum:   *kwp > 0,
	})
	if err != nil {
		fatal("CNMC: %v", err)
	}
	sort.Slice(offersFV, func(i, j int) bool {
		return offersFV[i].ImportePrimerAnio < offersFV[j].ImportePrimerAnio
	})

	var offersSense []st.Offer
	if *kwp > 0 && *sinSolar {
		offersSense, err = st.FetchOffers(st.Query{
			CodigoPostal: *cp,
			Potencia:     *potencia,
			Consum:       ca, // sense autoconsum
		})
		if err != nil {
			fatal("CNMC (sense FV): %v", err)
		}
		sort.Slice(offersSense, func(i, j int) bool {
			return offersSense[i].ImportePrimerAnio < offersSense[j].ImportePrimerAnio
		})
	}

	if *asJSON {
		printJSON(offersFV, resultatAuto, *top, esquemesIfSim(*sim, prodCurve, info))
		return
	}
	printTaula(offersFV, *top, resultatAuto, *kwp > 0 || *prodCSV != "")
	if len(offersSense) > 0 {
		fmt.Printf("\n— Sense FV (referència): la més barata és %.2f €/any —\n", offersSense[0].ImportePrimerAnio)
		if len(offersFV) > 0 {
			fmt.Printf("— Amb FV: la més barata és %.2f €/any — estalvi %.2f €/any\n",
				offersFV[0].ImportePrimerAnio, offersSense[0].ImportePrimerAnio-offersFV[0].ImportePrimerAnio)
		}
	}
	if *sim && prodCurve != nil {
		printSim(info, prodCurve)
	}
}

// esquemesIfSim executa la simulació d'excedents si -sim està actiu; retorna nil altrament.
func esquemesIfSim(doSim bool, prodCurve map[time.Time]float64, info *st.CCHInfo) []st.ResultatScheme {
	if !doSim || prodCurve == nil {
		return nil
	}
	preus, err := st.FetchPreusHoraris(info.Corba.First, info.Corba.Last)
	if err != nil {
		fmt.Fprintf(os.Stderr, "avís (sim): no s'han pogut obtenir preus e-sios: %v\n", err)
		return nil
	}
	return st.ResumComparativa(info.Corba.Consum, prodCurve, preus)
}

func printSim(info *st.CCHInfo, prodCurve map[time.Time]float64) {
	preus, err := st.FetchPreusHoraris(info.Corba.First, info.Corba.Last)
	if err != nil {
		fmt.Fprintf(os.Stderr, "avís (sim): %v\n", err)
		return
	}
	res := st.ResumComparativa(info.Corba.Consum, prodCurve, preus)
	fmt.Println("\n— Simulació d'excedents (terme d'energia net, sense fixos) —")
	fmt.Printf("  font preus: PVPC=%s  excedents=%s\n", preus.PVPC.Fuente, preus.Excedents.Fuente)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\n", "€/any energia", "Compensat", "Perdut", "Exc kWh", "Scheme")
	for _, r := range res {
		fmt.Fprintf(w, "  %.2f\t%.2f\t%.2f\t%.0f\t%s\n",
			r.EnergiaNeta, r.CompensacioUsada, r.CompensacioPerduda, r.ExcedentsKWh, r.Scheme.Nom)
	}
	w.Flush()
	if len(res) >= 2 {
		fmt.Printf("\n  Millor: %s (%.2f €/any) | pitjor: %s (%.2f €/any) → diferència %.2f €/any\n",
			res[0].Scheme.Nom, res[0].EnergiaNeta, res[len(res)-1].Scheme.Nom, res[len(res)-1].EnergiaNeta,
			res[len(res)-1].EnergiaNeta-res[0].EnergiaNeta)
	}
	_ = res
}

func printTaula(offers []st.Offer, top int, auto st.ResultatAutoconsum, ambFV bool) {
	if len(offers) == 0 {
		fmt.Println("(cap oferta)")
		return
	}
	if top > len(offers) {
		top = len(offers)
	}
	tipus := func(o st.Offer) string {
		if o.Tipo != nil {
			return *o.Tipo
		}
		if o.TipoElectricidad == "TI" {
			return "Indexat"
		}
		return "Fixe"
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if ambFV {
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\t%s\n", "€/any", "Comercialitzadora", "Oferta", "Tipus", "Verd", "Autocons")
	} else {
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\n", "€/any", "Comercialitzadora", "Oferta", "Tipus", "Verd")
	}
	for _, o := range offers[:top] {
		verd := "—"
		if o.Verde {
			verd = "✓"
		}
		autoCol := "—"
		if o.Autoconsum {
			autoCol = "✓"
		}
		nom := truncar(o.Comercializadora, 26)
		oferta := truncar(o.Oferta, 34)
		if ambFV {
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\t%s\n",
				formatEUR(o.ImportePrimerAnio), nom, oferta, truncar(tipus(o), 12), verd, autoCol)
		} else {
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\n",
				formatEUR(o.ImportePrimerAnio), nom, oferta, truncar(tipus(o), 12), verd)
		}
	}
	w.Flush()
	fmt.Printf("\nMostrant %d de %d ofertes.\n", top, len(offers))
}

type jsonOut struct {
	Autoconsum st.ResultatAutoconsum `json:"autoconsum,omitempty"`
	NumOfertes int                   `json:"num_ofertes"`
	TopOfertes []st.Offer            `json:"top_ofertes"`
	Esquemes   []st.ResultatScheme   `json:"esquemes_excedents,omitempty"`
}

func printJSON(offers []st.Offer, auto st.ResultatAutoconsum, top int, esquemes []st.ResultatScheme) {
	if top > len(offers) {
		top = len(offers)
	}
	out := jsonOut{Autoconsum: auto, NumOfertes: len(offers), TopOfertes: offers[:top], Esquemes: esquemes}
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
