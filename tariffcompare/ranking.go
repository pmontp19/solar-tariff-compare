package tariffcompare

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
	// Octopus: VERIFICADO 2026-07-23 (octopusenergy.es/solar-wallet). Monedero en euros
	// sin caducidad que compensa la factura entera (incluida la potencia). Pago 0,035 €/kWh
	// (o 0,07 si Octopus instaló los paneles). Tope de 1.000 kWh/mes de ingreso al wallet
	// (sólo tras factura 0€; no afecta al año 1). Usamos 0,035.
	"octopus": {Name: "Octopus Solar Wallet (monedero €, sin caducidad, incl. potencia; 0,035 €/kWh)", Type: SchemeVirtualBattery, Price: 0.035, CeilingAnnual: true},

	// Holaluz: verificado 2026-07-23 (holaluz.com/placas-solares/tarifa-autoconsumo-con-
	// excedentes). La "Tarifa Clásica Cloud" compra los excedentes a 0,05 €/kWh fijos (tope
	// mensual). La "Tarifa Justa Cloud" (cuota mensual plana, batería anual) no publica
	// €/kWh y queda sin modelar; requiere presupuesto personalizado.
	"holaluz": {Name: "Holaluz Clásica Cloud (0,05 €/kWh fijos, tope mensual)", Type: SchemeIndexed, Price: 0.05, CeilingAnnual: false},

	// Naturgy: 0,06 €/kWh CONFIRMADO. Modela el add-on gratuito "Batería Virtual"
	// (compensa la factura entera); la tarifa por defecto sin optar es tope mensual sin
	// arrastre. El saldo de la batería caduca a los 5 años.
	"naturgy": {Name: "Naturgy Batería Virtual (opt-in gratis, caduca a 5 años)", Type: SchemeVirtualBattery, Price: 0.06, CeilingAnnual: true, ExpiryMonths: 60},

	// TotalEnergies: verificado 2026-07-23 (totalenergies.es/.../compensacion-excedentes).
	// 0,07 €/kWh fijos, compensación simplificada (RD 244/2019), tope mensual al término de
	// energía, sin batería, nunca compensa los términos fijos. La compensación aplica sobre
	// las tarifas vigentes (A Tu Aire, Plan Ahora, 4 Estaciones...).
	"totalenergies":  {Name: "TotalEnergies (compensación simplificada, 0,07 €/kWh fijos, tope mensual)", Type: SchemeRegulated, Price: 0.07, CeilingAnnual: false},
	"total energies": {Name: "TotalEnergies (compensación simplificada, 0,07 €/kWh fijos, tope mensual)", Type: SchemeRegulated, Price: 0.07, CeilingAnnual: false},

	// Repsol: verificado 2026-07-23 (repsol.es/.../tarifa-bateria-virtual + blog
	// 2026-06-16). Vivit Batería Virtual: 0,06 €/kWh, 1,99 €/mes, compensa luz+gas, sin
	// caducidad, sin tope documentado (el blog indica "no existe un límite de
	// almacenamiento").
	"repsol": {Name: "Repsol Vivit Batería Virtual (0,06 €/kWh, 1,99 €/mes, sin caducidad, sin tope documentado)", Type: SchemeVirtualBattery, Price: 0.06, CeilingAnnual: true, MonthlyFee: 1.99, ThrottleFraction: 0},

	// Nabalia: verificado 2026-07-23 sobre las Condiciones Generales oficiales (PDF
	// assets.nabaliaenergia.com/COND-GENERALES-NABALIA.pdf, 2025-11-19). La compensación
	// simplificada por defecto paga 0,08 €/kWh fijos (80 €/MWh, cláusula 10), tope mensual
	// sobre el término de energía, permanencia 12 meses. La "Batería Virtual / monedero
	// solar" es un opt-in a precio de mercado (OMIE − margen) con saldo sin caducidad y
	// cuota mensual; sin €/kWh publicado, queda sin modelar.
	"nabalia": {Name: "Nabalia (compensación simplificada, 0,08 €/kWh fijos, tope mensual)", Type: SchemeRegulated, Price: 0.08, CeilingAnnual: false},

	// --- Comercializadoras secundarias con evidencia razonable (julio 2026) ---
	"endesa":    {Name: "Endesa Solar Plus + Batería Virtual (2 €/mes)", Type: SchemeVirtualBattery, Price: 0.06, CeilingAnnual: true, MonthlyFee: 2.0},
	// Iberdrola: VERIFICADO 2026-07-23. Solar Cloud compensa a ~0,06 €/kWh FIXOS (3
	// comparadores convergen: tarifasgasluz act. 23/06/26, selectra, solarbalcon act.
	// 15/07/26). La web oficial dice "al precio asociado al plan", pero significa el precio
	// de excedente escrito en el plan, NO el precio de consumo (consum es 0,12-0,24 €/kWh).
	// Batería virtual gratis: sostre anual sobre toda la factura (energía+potencia+peajes,
	// no impuestos), saldo 24 meses, tope de acumulación 1.000 €/mes (no modelable con
	// ThrottleFraction; reflejado en el nombre).
	"iberdrola": {Name: "Iberdrola Solar Cloud (0,06 €/kWh, gratis, 24 meses, tope 1.000 €/mes)", Type: SchemeVirtualBattery, Price: 0.06, CeilingAnnual: true, ExpiryMonths: 24},
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

// foldDiacritics normaliza vocales acentuadas, ñ y ç a ASCII. La CNMC devuelve
// nombres con acentos ("Gana Energía") y las claves del registro van sin ellos;
// sin plegado, el lookup fallaría en silencio y la oferta caería en el default.
func foldDiacritics(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case 'á', 'à', 'ä', 'â', 'ã':
			b.WriteRune('a')
		case 'é', 'è', 'ë', 'ê':
			b.WriteRune('e')
		case 'í', 'ì', 'ï', 'î':
			b.WriteRune('i')
		case 'ó', 'ò', 'ö', 'ô', 'õ':
			b.WriteRune('o')
		case 'ú', 'ù', 'ü', 'û':
			b.WriteRune('u')
		case 'ñ':
			b.WriteRune('n')
		case 'ç':
			b.WriteRune('c')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// LookupSurplusTerms busca los términos de excedentes de una oferta por el nombre de
// la comercializadora (subcadena, insensible a mayúsculas y acentos). Devuelve
// DefaultSurplusTerms si no hay coincidencia. Las claves se prueban en orden
// determinista (más larga primero) para que un nombre que casara con dos entradas
// no dependa del orden aleatorio de iteración del map.
func LookupSurplusTerms(comercializadora, oferta string) SurplusTerms {
	name := foldDiacritics(strings.ToLower(comercializadora + " " + oferta))
	keys := make([]string, 0, len(RetailerRegistry))
	for key := range RetailerRegistry {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) != len(keys[j]) {
			return len(keys[i]) > len(keys[j])
		}
		return keys[i] < keys[j]
	})
	for _, key := range keys {
		if strings.Contains(name, key) {
			return RetailerRegistry[key]
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
		// El perfil medio sólo sustituye horas SIN dato; un precio 0 es real
		// (frecuente en el indicador de excedentes al mediodía) y debe respetarse.
		pvpcH, ok := prices.PVPC.PriceAt(dia, hour)
		if !ok {
			pvpcH = pvpcProfile[hour]
		}
		excH, ok := prices.Surplus.PriceAt(dia, hour)
		if !ok {
			excH = excProfile[hour]
		}
		rate := surplusRate(terms, pvpcH, excH)
		if rate < 0 {
			// Ninguna comercializadora cobra por verter (la compensación se limita
			// a 0 aunque el precio horario de referencia sea negativo).
			rate = 0
		}

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
