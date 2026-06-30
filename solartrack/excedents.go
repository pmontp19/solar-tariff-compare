package solartrack

import (
	"fmt"
	"time"
)

// Scheme descriu com es compensen els excedents d'una comercialitzadora.
type Scheme struct {
	Nom        string // nom comercial (p.ex. "Holaluz Sun")
	Tipus      TipusScheme
	PreuConsum float64 // per a TipusFixe/IndexadaFixe: €/kWh del consum (0 = usar PVPC horari)
	// Per a bateria virtual: els excedents es valoren al preu de consum (PVPC horari o PreuConsum).
	// Per a excedents regulats/indexats: es valoren al preu d'excedents horari (indicador 1739)
	// ± un coeficient/prima.
	Coeficient float64 // coeficient sobre el preu d'excedents (indexada: excedents × (1+coef))
	Prima      float64 // prima addicional en €/kWh sobre excedents
}

// TipusScheme enumera els esquemes de compensació d'excedents.
type TipusScheme int

const (
	// SchemeRegulada: compensació simplificada per defecte al preu d'excedents regulat (1739).
	SchemeRegulada TipusScheme = iota
	// SchemeIndexada: excedents al preu d'excedents horari × (1+coef) + prima (p.ex. Octopus, Som).
	SchemeIndexada
	// SchemeBateriaVirtual: excedents valorats al preu de CONSUM horari (p.ex. Holaluz Sun).
	// Més favorable amb perfil FV gran perquè valora els excedents al preu que pagues.
	SchemeBateriaVirtual
)

// SchemesRegistre és un conjunt d'esquemes coneguts de comercialitzadores espanyoles.
// Les condicions canvien; això és un punt de partida editable. El preu de consum
// real de cada comercialitzadora no es modela aquí (caldria el seu full tarifari);
// el simulador usa el PVPC horari com a referència comuna per aïllar l'efecte
// de l'esquema d'excedents.
var SchemesRegistre = []Scheme{
	{Nom: "PVPC regulada (compensació simplificada per defecte)", Tipus: SchemeRegulada},
	{Nom: "Octopus / Som (indexada: excedents al preu d'excedents)", Tipus: SchemeIndexada},
	{Nom: "Holaluz Sun (bateria virtual: excedents al preu de consum)", Tipus: SchemeBateriaVirtual},
	{Nom: "Repsol / Núcleo (bateria virtual variant)", Tipus: SchemeBateriaVirtual, Coeficient: -0.0, Prima: 0},
}

// ResultatScheme és la factura simulada per un esquema.
type ResultatScheme struct {
	Scheme             Scheme  `json:"scheme"`
	Tipus              string  `json:"tipus"`
	EnergiaBruta       float64 `json:"energia_bruta_eur"`       // cost consum abans de compensar
	CompensacioBruta   float64 `json:"compensacio_bruta_eur"`   // compensació acumulada abans del sostre
	CompensacioUsada   float64 `json:"compensacio_usada_eur"`   // aplicada (després del sostre mensual)
	CompensacioPerduda float64 `json:"compensacio_perduda_eur"` // no aprofitada (per la regla no-negatiu)
	EnergiaNeta        float64 `json:"energia_neta_eur"`        // energiaBruta - compensacioUsada
	ExcedentsKWh       float64 `json:"excedents_kwh"`
	ConsumXarxaKWh     float64 `json:"consum_xarxa_kwh"`
}

// SimulaExcedents computa la factura d'energia hora a hora per a un esquema, aplicant
// la regla no-negatiu per mes natural (RD 244/2019: la compensació no pot excedir el
// terme d'energia del mateix període de facturació).
//
// Els termes fixos (potència, lloguer, impostos) NO s'inclouen: són comuns a totes
// les ofertes i cal afegir-los a part per al total. Comparar esquemes per l'energia
// neta és vàlid perquè els termes fixos es cancel·len.
//
// Parameters:
//   - consum, produccio: corbes horàries (instant inici -> kWh)
//   - preus: sèries horàries PVPC i d'excedents
//   - scheme: esquema de compensació
func SimulaExcedents(consum, produccio map[time.Time]float64, preus *PreusHoraris, scheme Scheme) ResultatScheme {
	r := ResultatScheme{Scheme: scheme}
	r.Tipus = tipusString(scheme.Tipus)

	// Normalitza les claus a UTC (instant canònic, loc=UTC singleton) per fer el
	// lookup robust entre maps construïts independentment. La cerca de preus es fa
	// amb l'hora local de Madrid (tu.In(madrid)).
	madrid, _ := time.LoadLocation("Europe/Madrid")
	consumNorm := make(map[time.Time]float64, len(consum))
	for t, v := range consum {
		consumNorm[t.UTC()] = v
	}
	prodNorm := make(map[time.Time]float64, len(produccio))
	for t, v := range produccio {
		prodNorm[t.UTC()] = v
	}
	upload := make(map[time.Time]bool, len(consumNorm)+len(prodNorm))
	for t := range consumNorm {
		upload[t] = true
	}
	for t := range prodNorm {
		upload[t] = true
	}

	// Acumulem per mes (clau "YYYY-MM") per aplicar el sostre mensual
	type mes struct {
		energiaBruta, compBruta float64
		excedents, consumXarxa  float64
	}
	mesos := map[string]*mes{}
	getMes := func(k string) *mes {
		m, ok := mesos[k]
		if !ok {
			m = &mes{}
			mesos[k] = m
		}
		return m
	}

	// Per a esquemes que usen perfil horari, cal un perfil de preu per hora.
	// Quan hi ha històric, usem el preu de cada dia; si no, el perfil mig.
	perfilConsum := preus.PVPC.PerfilMigHorari()
	perfilExc := preus.Excedents.PerfilMigHorari()

	for tu := range upload {
		tm := tu.In(madrid) // hora local de Madrid per a la cerca de preus
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
		pvpcH := preus.PVPC.Dia[dia][tm.Hour()]
		if pvpcH == 0 {
			pvpcH = perfilConsum[tm.Hour()]
		}
		excH := preus.Excedents.Dia[dia][tm.Hour()]
		if excH == 0 {
			excH = perfilExc[tm.Hour()]
		}

		// Preu de compensació segons esquema
		compRate := 0.0
		switch scheme.Tipus {
		case SchemeRegulada, SchemeIndexada:
			compRate = excH*(1+scheme.Coeficient) + scheme.Prima
		case SchemeBateriaVirtual:
			// valorat al preu de consum (PVPC o fixe)
			if scheme.PreuConsum > 0 {
				compRate = scheme.PreuConsum
			} else {
				compRate = pvpcH
			}
		}

		mk := tm.Format("2006-01")
		m := getMes(mk)
		m.energiaBruta += buy * pvpcH
		m.compBruta += surplus * compRate
		m.excedents += surplus
		m.consumXarxa += buy
	}

	// Aplica el sostre segons l'esqueme:
	//  - Regulada/Indexada: sostre mensual (RD 244/2019: la compensació no pot excedir
	//    el terme d'energia del mateix període de facturació mensual).
	//  - Bateria virtual: sostre ANUAL (porta saldo entre mesos; la compensació només
	//    es limita al terme d'energia total de l'any, no per mes).
	for _, m := range mesos {
		r.EnergiaBruta += m.energiaBruta
		r.CompensacioBruta += m.compBruta
		r.ExcedentsKWh += m.excedents
		r.ConsumXarxaKWh += m.consumXarxa
	}
	if scheme.Tipus == SchemeBateriaVirtual {
		// Sostre anual: la compensació no pot excedir el terme d'energia anual.
		usable := r.CompensacioBruta
		if usable > r.EnergiaBruta {
			usable = r.EnergiaBruta
		}
		r.CompensacioUsada = usable
		r.CompensacioPerduda = r.CompensacioBruta - usable
	} else {
		// Sostre mensual
		for _, m := range mesos {
			usable := m.compBruta
			if usable > m.energiaBruta {
				usable = m.energiaBruta
			}
			r.CompensacioUsada += usable
			r.CompensacioPerduda += m.compBruta - usable
		}
	}
	r.EnergiaNeta = r.EnergiaBruta - r.CompensacioUsada
	return r
}

func tipusString(t TipusScheme) string {
	switch t {
	case SchemeRegulada:
		return "regulada"
	case SchemeIndexada:
		return "indexada"
	case SchemeBateriaVirtual:
		return "bateria_virtual"
	}
	return "?"
}

// ResumComparativa simula tots els esquemes del registre i retorna els resultats
// ordenats de menys a més cost d'energia neta (millor primer).
func ResumComparativa(consum, produccio map[time.Time]float64, preus *PreusHoraris) []ResultatScheme {
	out := make([]ResultatScheme, 0, len(SchemesRegistre))
	for _, s := range SchemesRegistre {
		out = append(out, SimulaExcedents(consum, produccio, preus, s))
	}
	// ordena per energia neta (menys cost primer)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].EnergiaNeta < out[j-1].EnergiaNeta; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// String dona una línia resum llegible d'un resultat (per a la taula del CLI).
func (r ResultatScheme) String() string {
	return fmt.Sprintf("%-52s energia neta %.2f € | compensat %.2f € (perdut %.2f €) | exc %.0f kWh",
		r.Scheme.Nom, r.EnergiaNeta, r.CompensacioUsada, r.CompensacioPerduda, r.ExcedentsKWh)
}
