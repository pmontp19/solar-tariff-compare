package solartrack

import (
	"fmt"
	"time"
)

// Scheme describe cómo se compensan los excedentes de una comercializadora.
type Scheme struct {
	Name             string // nombre comercial (p.ej. "Holaluz Sun")
	Type             SchemeType
	ConsumptionPrice float64 // para bateria virtual fija: €/kWh del consumo (0 = usar PVPC horario)
	// Para bateria virtual: los excedentes se valoran al precio de consumo (PVPC
	// horario o ConsumptionPrice).
	// Para excedentes regulados/indexados: se valoran al precio de excedentes horario
	// (indicador 1739) ± un coeficiente/prima.
	Coefficient float64 // coeficiente sobre el precio de excedentes (indexada: excedentes × (1+coef))
	Premium     float64 // prima adicional en €/kWh sobre excedentes
}

// SchemeType enumera los esquemas de compensación de excedentes.
type SchemeType int

const (
	// SchemeRegulated: compensación simplificada por defecto al precio de excedentes
	// regulado (1739).
	SchemeRegulated SchemeType = iota
	// SchemeIndexed: excedentes al precio de excedentes horario × (1+coef) + prima
	// (p.ej. Octopus, Som).
	SchemeIndexed
	// SchemeVirtualBattery: excedentes valorados al precio de CONSUMO horario (p.ej.
	// Holaluz Sun). Más favorable con perfil FV grande porque valora los excedentes al
	// precio que pagas.
	SchemeVirtualBattery
)

// SchemesRegistry es un conjunto de esquemas conocidos de comercializadoras españolas.
// Las condiciones cambian; esto es un punto de partida editable. El precio de
// consumo real de cada comercializadora no se modela aquí (haría falta su tarifa);
// el simulador usa el PVPC horario como referencia común para aislar el efecto del
// esquema de excedentes.
var SchemesRegistry = []Scheme{
	{Name: "PVPC regulada (compensación simplificada por defecto)", Type: SchemeRegulated},
	{Name: "Octopus / Som (indexada: excedentes al precio de excedentes)", Type: SchemeIndexed},
	{Name: "Holaluz Sun (batería virtual: excedentes al precio de consumo)", Type: SchemeVirtualBattery},
	{Name: "Repsol / Núcleo (batería virtual variante)", Type: SchemeVirtualBattery, Coefficient: -0.0, Premium: 0},
}

// SchemeResult es la factura simulada para un esquema.
type SchemeResult struct {
	Scheme             Scheme  `json:"scheme"`
	Type               string  `json:"type"`
	GrossEnergy        float64 `json:"gross_energy_eur"`       // coste de consumo antes de compensar
	GrossCompensation  float64 `json:"gross_compensation_eur"` // compensación acumulada antes del techo
	UsedCompensation   float64 `json:"used_compensation_eur"`  // aplicada (tras el techo)
	LostCompensation   float64 `json:"lost_compensation_eur"`  // no aprovechada (por la regla no-negativo)
	NetEnergy          float64 `json:"net_energy_eur"`         // GrossEnergy - UsedCompensation
	SurplusKWh         float64 `json:"surplus_kwh"`
	GridConsumptionKWh float64 `json:"grid_consumption_kwh"`
}

// SimulateSurplus calcula la factura de energía hora a hora para un esquema,
// aplicando la regla no-negativo por mes natural (RD 244/2019: la compensación no
// puede exceder el término de energía del mismo período de facturación).
//
// Los términos fijos (potencia, alquiler, impuestos) NO se incluyen: son comunes a
// todas las ofertas y hay que añadirlos aparte para el total. Comparar esquemas por
// la energía neta es válido porque los términos fijos se cancelan.
func SimulateSurplus(consumption, production map[time.Time]float64, prices *HourlyPrices, scheme Scheme) SchemeResult {
	r := SchemeResult{Scheme: scheme}
	r.Type = typeString(scheme.Type)

	// Normaliza las claves a UTC (instante canónico, loc=UTC singleton) para hacer el
	// lookup robusto entre maps construidos independientemente. La búsqueda de precio
	// se hace con la hora local de Madrid (tu.In(madrid)).
	madrid, _ := time.LoadLocation("Europe/Madrid")
	consumNorm := make(map[time.Time]float64, len(consumption))
	for t, v := range consumption {
		consumNorm[t.UTC()] = v
	}
	prodNorm := make(map[time.Time]float64, len(production))
	for t, v := range production {
		prodNorm[t.UTC()] = v
	}
	keys := make(map[time.Time]bool, len(consumNorm)+len(prodNorm))
	for t := range consumNorm {
		keys[t] = true
	}
	for t := range prodNorm {
		keys[t] = true
	}

	// Acumulamos por mes (clave "YYYY-MM") para aplicar el techo mensual.
	type monthAgg struct {
		grossEnergy, grossComp float64
		surplus, gridConsump   float64
	}
	months := map[string]*monthAgg{}
	getMonth := func(k string) *monthAgg {
		m, ok := months[k]
		if !ok {
			m = &monthAgg{}
			months[k] = m
		}
		return m
	}

	// Para esquemas que usan perfil horario, hace falta un perfil de precio por hora.
	// Cuando hay histórico, usamos el precio de cada día; si no, el perfil medio.
	consumProfile := prices.PVPC.AverageHourlyProfile()
	excProfile := prices.Surplus.AverageHourlyProfile()

	for tu := range keys {
		tm := tu.In(madrid) // hora local de Madrid para la búsqueda de precio
		c := consumNorm[tu]
		p := prodNorm[tu]
		buy := c - p
		if buy < 0 {
			buy = 0
		}
		surplus := p - c
		if surplus < 0 {
			surplus = 0
		}
		dia := tm.Format("2006-01-02")
		pvpcH := prices.PVPC.ByDay[dia][tm.Hour()]
		if pvpcH == 0 {
			pvpcH = consumProfile[tm.Hour()]
		}
		excH := prices.Surplus.ByDay[dia][tm.Hour()]
		if excH == 0 {
			excH = excProfile[tm.Hour()]
		}

		// Precio de compensación según esquema.
		compRate := 0.0
		switch scheme.Type {
		case SchemeRegulated, SchemeIndexed:
			compRate = excH*(1+scheme.Coefficient) + scheme.Premium
		case SchemeVirtualBattery:
			// valorado al precio de consumo (PVPC o fijo)
			if scheme.ConsumptionPrice > 0 {
				compRate = scheme.ConsumptionPrice
			} else {
				compRate = pvpcH
			}
		}

		mk := tm.Format("2006-01")
		m := getMonth(mk)
		m.grossEnergy += buy * pvpcH
		m.grossComp += surplus * compRate
		m.surplus += surplus
		m.gridConsump += buy
	}

	// Aplica el techo según el esquema:
	//  - Regulada/Indexada: techo mensual (RD 244/2019: la compensación no puede
	//    exceder el término de energía del mismo período de facturación mensual).
	//  - Batería virtual: techo ANUAL (lleva saldo entre meses; la compensación sólo
	//    se limita al término de energía total del año, no por mes).
	for _, m := range months {
		r.GrossEnergy += m.grossEnergy
		r.GrossCompensation += m.grossComp
		r.SurplusKWh += m.surplus
		r.GridConsumptionKWh += m.gridConsump
	}
	if scheme.Type == SchemeVirtualBattery {
		// Techo anual: la compensación no puede exceder el término de energía anual.
		usable := r.GrossCompensation
		if usable > r.GrossEnergy {
			usable = r.GrossEnergy
		}
		r.UsedCompensation = usable
		r.LostCompensation = r.GrossCompensation - usable
	} else {
		// Techo mensual
		for _, m := range months {
			usable := m.grossComp
			if usable > m.grossEnergy {
				usable = m.grossEnergy
			}
			r.UsedCompensation += usable
			r.LostCompensation += m.grossComp - usable
		}
	}
	r.NetEnergy = r.GrossEnergy - r.UsedCompensation
	return r
}

func typeString(t SchemeType) string {
	switch t {
	case SchemeRegulated:
		return "regulated"
	case SchemeIndexed:
		return "indexed"
	case SchemeVirtualBattery:
		return "virtual_battery"
	}
	return "?"
}

// CompareSchemes simula todos los esquemas del registro y devuelve los resultados
// ordenados de menor a mayor coste de energía neta (mejor primero).
func CompareSchemes(consumption, production map[time.Time]float64, prices *HourlyPrices) []SchemeResult {
	out := make([]SchemeResult, 0, len(SchemesRegistry))
	for _, s := range SchemesRegistry {
		out = append(out, SimulateSurplus(consumption, production, prices, s))
	}
	// ordena por energía neta (menor coste primero)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].NetEnergy < out[j-1].NetEnergy; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// String da una línea resumen legible de un resultado (para la tabla del CLI).
func (r SchemeResult) String() string {
	return fmt.Sprintf("%-52s energía neta %.2f € | compensado %.2f € (perdido %.2f €) | exc %.0f kWh",
		r.Scheme.Name, r.NetEnergy, r.UsedCompensation, r.LostCompensation, r.SurplusKWh)
}
