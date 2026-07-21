package solartrack

import (
	"sort"
	"strings"
	"time"
)

// SurplusTerms describe cómo una comercializadora concreta compensa los excedentes.
// Es lo que la API de la CNMC NO da (sólo devuelve importePrimerAnio), así que se
// mantiene en un registro editable y, cuando haga falta, se rellena con datos de las
// webs de cada comercializadora.
type SurplusTerms struct {
	Name          string     // etiqueta legible
	Type          SchemeType // regulada / indexada / batería virtual
	Price         float64    // €/kWh fijo de compensación (0 = usar precio horario de referencia)
	Coefficient   float64    // sobre el precio de excedentes horario (indexada)
	Premium       float64    // prima €/kWh adicional (indexada)
	CeilingAnnual bool       // true = techo anual y compensa el total (batería virtual/wallet);
	// false = techo mensual limitado al término de energía (compensación simplificada, RD 244/2019)
	AtConsumptionPrice bool // batería virtual valorada al precio de consumo (PVPC horario) si Price==0

	// MonthlyFee: cuota mensual en € que cobra la comercializadora desde el mes 1
	// por el servicio de batería virtual/monedero (0 = sin cuota). Se aplica en
	// RankOffersWithSurplus (aquí surplusCredit sólo calcula la compensación).
	MonthlyFee float64

	// ExpiryMonths: caducidad del saldo acumulado, en meses (0 = sin caducidad).
	// METADATA informativa únicamente: NO se aplica al número de PRIMER año
	// (ImportePrimerAnio/surplusCredit), porque una caducidad >= 12 meses no llega
	// a recortar nada dentro del propio primer año (el saldo generado en el año 1
	// todavía no ha caducado). Sirve para que la UI/documentación avise del riesgo
	// a partir del año 2 sin falsear el cálculo del año 1.
	ExpiryMonths int

	// ThrottleFraction: fracción (0-1) del consumo anual de red por encima de la
	// cual el excedente NO se compensa a tarifa plena (modelo conservador de topes
	// tipo Repsol, que compensa hasta el 40% del consumo anual). 0 = sin tope.
	ThrottleFraction float64
}

// RetailerRegistry mapea (subcadena en minúsculas del nombre de la comercializadora)
// -> términos de excedentes. Es un PUNTO DE PARTIDA aproximado y editable; los
// precios y condiciones cambian y deben verificarse en la web de cada compañía
// (julio 2026). Si una oferta no encaja con ninguna entrada, se aplica DefaultSurplusTerms
// (compensación regulada).
//
// Datos revisados en julio 2026 (webs oficiales + comparadores; ver detalle y fuentes
// en docs/REVIEW-autoconsum.md). Todavía no existe un SchemeType de "monedero en euros":
// los wallets tipo Octopus/Holaluz se aproximan con SchemeVirtualBattery + CeilingAnnual
// (el techo anual se aplica sobre ImportePrimerAnio de la CNMC, que ya incluye energía +
// potencia + impuestos, así que funciona como un tope a la factura completa). Desde el
// paso 5 del HANDOFF el modelo también captura: cuotas mensuales (MonthlyFee, sumadas en
// AnnualFeeEUR/NetAnnualEUR en RankOffersWithSurplus), caducidad del saldo (ExpiryMonths,
// metadata informativa que no recorta el número de primer año) y topes tipo el 40% del
// consumo anual de Repsol (ThrottleFraction, aplicado en surplusCredit).
var RetailerRegistry = map[string]SurplusTerms{
	// Octopus: CORREGIDO. No es una batería a 0,03 €/kWh con tope, sino un monedero en
	// euros sin caducidad que compensa la factura entera (incluida la potencia). El precio
	// depende de quién instaló los paneles: 0,07 €/kWh si instala Octopus, 0,04 €/kWh en
	// otro caso. Usamos 0,04 (conservador). Nota: el contrato ACTUAL del usuario es 0,03
	// €/kWh (tarifa antigua), infravalorado frente al mercado actual.
	"octopus": {Name: "Octopus Solar Wallet (monedero €, sin caducidad, incl. potencia)", Type: SchemeVirtualBattery, Price: 0.04, CeilingAnnual: true},

	// Holaluz: sin €/kWh oficial único; usa una cuota anual personalizada, ~equivalente a
	// valorar el excedente al precio de consumo. Compensa la factura entera.
	"holaluz": {Name: "Holaluz Cloud (batería virtual, cuota anual ~ precio de consumo)", Type: SchemeVirtualBattery, CeilingAnnual: true, AtConsumptionPrice: true},

	// Naturgy: 0,06 €/kWh CONFIRMADO. Modela el add-on gratuito "Batería Virtual"
	// (compensa la factura entera); la tarifa por defecto sin optar es tope mensual sin
	// arrastre. El saldo de la batería caduca a los 5 años.
	"naturgy": {Name: "Naturgy Batería Virtual (opt-in gratis, caduca a 5 años)", Type: SchemeVirtualBattery, Price: 0.06, CeilingAnnual: true, ExpiryMonths: 60},

	// TotalEnergies: CONFIRMADO sin cambios. 0,07 €/kWh fijo, tope mensual al término de
	// energía, sin batería, nunca compensa los términos fijos.
	"totalenergies":  {Name: "TotalEnergies Siempre Solar", Type: SchemeIndexed, Price: 0.07, CeilingAnnual: false},
	"total energies": {Name: "TotalEnergies Siempre Solar", Type: SchemeIndexed, Price: 0.07, CeilingAnnual: false},

	// Repsol: modela Vivit Batería Virtual (0,06 €/kWh, sin caducidad, compensa luz+gas).
	// Cuota de 1,99 €/mes y tope del 40% del consumo anual (penaliza justo a los perfiles
	// con muchos excedentes, el usuario objetivo de esta herramienta).
	"repsol": {Name: "Repsol Vivit (batería virtual, 1,99 €/mes, tope 40% consumo)", Type: SchemeVirtualBattery, Price: 0.06, CeilingAnnual: true, MonthlyFee: 1.99, ThrottleFraction: 0.40},

	// Nabalia: es Tarifa Solar Flex, precio FIJO 0,095 €/kWh a 12 meses (no indexado por
	// hora; SchemeIndexed es inocuo aquí porque Price>0 anula la fórmula del coeficiente).
	"nabalia": {Name: "Nabalia Tarifa Solar Flex (fijo 0,095 €/kWh 12 meses)", Type: SchemeIndexed, Price: 0.095, CeilingAnnual: false},

	// --- Comercializadoras secundarias con evidencia razonable (julio 2026) ---
	"endesa":    {Name: "Endesa Solar Plus + Batería Virtual (2 €/mes)", Type: SchemeVirtualBattery, Price: 0.06, CeilingAnnual: true, MonthlyFee: 2.0},
	"iberdrola": {Name: "Iberdrola Solar Cloud (batería virtual gratis, saldo 24 meses)", Type: SchemeVirtualBattery, Price: 0.06, CeilingAnnual: true, ExpiryMonths: 24},
	// Gana Energía: gratis el 1r año → para el número de PRIMER año (el que modela
	// ImportePrimerAnio/RankOffersWithSurplus) la cuota es 0; después pasa a ~2,1 €/mes,
	// pero eso ya no afecta al año 1 y por eso no se penaliza aquí.
	"gana energia": {Name: "Gana Energía Monedero Virtual (gratis 12 meses, luego ~2,1 €/mes)", Type: SchemeVirtualBattery, Price: 0.06, CeilingAnnual: true, MonthlyFee: 0},
	// Som Energia: confirmat 0,03 €/kWh fix a la tarifa periodes 2.0TD (vàlid des de
	// l'1 de maig de 2026, font: somenergia.coop). Compensació simplificada cooperativa
	// sense marge; sostre mensual regulat. També ofereix Generation kWh (esquema
	// diferent, no modelat aquí).
	"som energia": {Name: "Som Energia (compensación simplificada, 0,03 €/kWh fijos)", Type: SchemeRegulated, Price: 0.03, CeilingAnnual: false},

	// Plenitude y Eleia se OMITEN a propósito: sin cifras fiables por comercializadora a
	// fecha de julio 2026. Caen en DefaultSurplusTerms en lugar de inventar un número.
}

// DefaultSurplusTerms se aplica a las ofertas sin entrada en el registro:
// compensación simplificada regulada (precio de excedentes horario, techo mensual).
var DefaultSurplusTerms = SurplusTerms{
	Name: "Compensación simplificada (regulada)", Type: SchemeRegulated, CeilingAnnual: false,
}

// LookupSurplusTerms busca los términos de excedentes de una oferta por el nombre de
// la comercializadora (subcadena, insensible a mayúsculas). Devuelve DefaultSurplusTerms
// si no hay coincidencia.
func LookupSurplusTerms(comercializadora, oferta string) SurplusTerms {
	name := strings.ToLower(comercializadora + " " + oferta)
	for key, terms := range RetailerRegistry {
		if strings.Contains(name, key) {
			return terms
		}
	}
	return DefaultSurplusTerms
}

// RankedOffer es una oferta con la compensación de excedentes ya atribuida y el coste
// neto anual resultante.
type RankedOffer struct {
	Offer            Offer        `json:"offer"`
	SurplusTerms     string       `json:"surplus_terms"`
	SurplusCreditEUR float64      `json:"surplus_credit_eur"` // compensación anual aplicada
	AnnualFeeEUR     float64      `json:"annual_fee_eur"`     // cuota anual = 12 × MonthlyFee
	NetAnnualEUR     float64      `json:"net_annual_eur"`     // importePrimerAnio - compensación + cuota
	terms            SurplusTerms // interno, no serializado
}

// monthlyBreakdown agrega, por mes natural, el coste de la energía importada valorada
// a PVPC (proxy del término de energía, ya que la CNMC no expone el precio de cada
// oferta) y el valor bruto de los excedentes según unos términos dados.
type monthlyBreakdown struct {
	energyCostPVPC float64 // coste de la energía de red a PVPC (proxy del techo mensual)
	surplusValue   float64 // valor bruto de los excedentes con los términos de la oferta
}

// surplusCredit calcula la compensación anual APLICADA de los excedentes reales para
// unos términos concretos, aplicando el techo correspondiente:
//   - techo mensual (regulada/indexada): la compensación de cada mes no puede exceder
//     el término de energía de ese mes (aproximado con PVPC).
//   - techo anual (batería virtual/wallet): la compensación total no puede exceder el
//     importe anual de la oferta (compensa el total y arrastra saldo entre meses).
//
// gridImport = consumo neto de red por hora; surplus = excedentes por hora (ambos
// reales, p.ej. de Datadis). prices aporta PVPC y precio de excedentes horarios.
func surplusCredit(gridImport, surplus map[time.Time]float64, prices *HourlyPrices, terms SurplusTerms, importeAnual float64) float64 {
	madrid, _ := time.LoadLocation("Europe/Madrid")
	pvpcProfile := prices.PVPC.AverageHourlyProfile()
	excProfile := prices.Surplus.AverageHourlyProfile()

	months := map[string]*monthlyBreakdown{}
	get := func(k string) *monthlyBreakdown {
		m, ok := months[k]
		if !ok {
			m = &monthlyBreakdown{}
			months[k] = m
		}
		return m
	}

	keys := map[time.Time]bool{}
	for t := range gridImport {
		keys[t] = true
	}
	for t := range surplus {
		keys[t] = true
	}

	totalGrid := 0.0       // Σ gridImport, para el tope tipo Repsol (40% del consumo anual)
	totalSurplusKWh := 0.0 // Σ surplus, ídem
	for t := range keys {
		tm := t.In(madrid)
		dia := tm.Format("2006-01-02")
		hour := tm.Hour()
		pvpcH := prices.PVPC.ByDay[dia][hour]
		if pvpcH == 0 {
			pvpcH = pvpcProfile[hour]
		}
		excH := prices.Surplus.ByDay[dia][hour]
		if excH == 0 {
			excH = excProfile[hour]
		}
		rate := surplusRate(terms, pvpcH, excH)

		mk := tm.Format("2006-01")
		m := get(mk)
		m.energyCostPVPC += gridImport[t] * pvpcH
		m.surplusValue += surplus[t] * rate
		totalGrid += gridImport[t]
		totalSurplusKWh += surplus[t]
	}

	// TOPE tipo Repsol: sólo se compensa a tarifa plena hasta ThrottleFraction del
	// consumo anual de red; el resto de excedente se recorta ANTES del techo habitual.
	// Escalamos el valor acumulado de CADA mes por el mismo factor. Es EXACTO para
	// tarifas de precio fijo (Price>0/AtConsumptionPrice con precio único), porque
	// escalar €=Σ(kWh×precio) por un factor uniforme equivale a escalar los kWh
	// compensados a tarifa plena. Para precio variable (PVPC/excedentes horario) es
	// una APROXIMACIÓN documentada: asume que los kWh recortados por el tope llevan
	// la tarifa media del período, no necesariamente las horas más caras/baratas.
	if terms.ThrottleFraction > 0 && totalSurplusKWh > 0 {
		fullRateKWh := terms.ThrottleFraction * totalGrid
		if totalSurplusKWh > fullRateKWh {
			factor := fullRateKWh / totalSurplusKWh
			for _, m := range months {
				m.surplusValue *= factor
			}
		}
	}

	if terms.CeilingAnnual {
		total := 0.0
		for _, m := range months {
			total += m.surplusValue
		}
		if total > importeAnual {
			total = importeAnual // no puede compensar más que la factura anual completa
		}
		return total
	}
	// techo mensual al término de energía
	used := 0.0
	for _, m := range months {
		u := m.surplusValue
		if u > m.energyCostPVPC {
			u = m.energyCostPVPC
		}
		used += u
	}
	return used
}

// surplusRate devuelve el precio de compensación €/kWh para una hora según los términos.
func surplusRate(terms SurplusTerms, pvpcH, excH float64) float64 {
	switch terms.Type {
	case SchemeVirtualBattery:
		if terms.Price > 0 {
			return terms.Price
		}
		if terms.AtConsumptionPrice {
			return pvpcH
		}
		return excH
	default: // regulada / indexada
		if terms.Price > 0 {
			return terms.Price
		}
		return excH*(1+terms.Coefficient) + terms.Premium
	}
}

// RankOffersWithSurplus atribuye a cada oferta la compensación de sus excedentes
// (según el registro por comercializadora) y devuelve las ofertas ordenadas por coste
// NETO anual (importePrimerAnio − compensación), de más barata a más cara.
//
// A diferencia del ranking crudo de la CNMC (que ignora los excedentes), este ranking
// integra el autoconsumo con vertido: para un perfil con muchos excedentes cambia
// sustancialmente el orden. Los excedentes se toman reales (p.ej. de Datadis), no
// estimados.
func RankOffersWithSurplus(offers []Offer, gridImport, surplus map[time.Time]float64, prices *HourlyPrices) []RankedOffer {
	out := make([]RankedOffer, 0, len(offers))
	for _, o := range offers {
		terms := LookupSurplusTerms(o.Comercializadora, o.Oferta)
		credit := surplusCredit(gridImport, surplus, prices, terms, o.ImportePrimerAnio)
		annualFee := 12 * terms.MonthlyFee // cuota anual; incrementa el coste neto
		out = append(out, RankedOffer{
			Offer:            o,
			SurplusTerms:     terms.Name,
			SurplusCreditEUR: credit,
			AnnualFeeEUR:     annualFee,
			NetAnnualEUR:     o.ImportePrimerAnio - credit + annualFee,
			terms:            terms,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NetAnnualEUR < out[j].NetAnnualEUR })
	return out
}
