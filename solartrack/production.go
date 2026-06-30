package solartrack

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// PVGIS és l'API de la Comissió Europea per estimar producció fotovoltaica.
// Vegeu https://joint-research-centre.ec.europa.eu/pvgis-photovoltaic-geographical-information-system_en
const pvgisURL = "https://re.jrc.ec.europa.eu/api/seriescalc"

// PVGISParams defineix una instal·lació FV per estimar-ne la producció horària.
type PVGISParams struct {
	Lat       float64 // Latitud (p.ex. 41.38 per Barcelona)
	Lon       float64 // Longitud (p.ex. 2.17)
	PeakPower float64 // Potència de pic en kW (p.ex. 3.5 → ~10 plaques de 350W)
	Angle     float64 // Inclinació en graus (0=pla, 35=típic residencial)
	Aspect    float64 // Orientació en graus (0=sud, -90=est, 90=oest)
	Loss      float64 // Pèrdues del sistema en % (14 és un valor típic per defecte)
	Mounting  string  // "free" (terra/teulada) o "building" (integrat)
	Tech      string  // "crystSi" (silici cristal·lí), "CIS", "CdTe"
}

// PerfilProd és la producció horària estimada (kWh) clau per (mes, hora-del-dia).
// Es deriva d'un TMY (any meteorològic tipus) de PVGIS.
type PerfilProd struct {
	ByMonthHour [12][24]float64 // kWh mitjans per mes (0=gen) i hora (0-23)
	AnualKWh    float64
}

// pvgisHour és una fila horària de la resposta de PVGIS. PVGIS seriescalc
// retorna irradiància G(i) en W/m² al pla (no la potència), per la qual cosa la
// calculem a partir de PeakPower i les pèrdues.
type pvgisHour struct {
	Time string  `json:"time"` // "YYYYMMDD:HHMM"
	GI   float64 `json:"G(i)"` // Irradiància global al pla [W/m²]
}

type pvgisResponse struct {
	Outputs struct {
		Hourly []pvgisHour `json:"hourly"`
	} `json:"outputs"`
}

// FetchPerfilPVGIS descarrega el TMY horari de PVGIS i l'agrega en un perfil
// mes×hora (kWh mitjans per cada combinació de mes i hora del dia).
func FetchPerfilPVGIS(p PVGISParams) (*PerfilProd, error) {
	if p.PeakPower <= 0 {
		return nil, fmt.Errorf("peakpower ha de ser > 0")
	}
	if p.Loss == 0 {
		p.Loss = 14
	}
	if p.Mounting == "" {
		p.Mounting = "free"
	}
	if p.Tech == "" {
		p.Tech = "crystSi"
	}
	params := url.Values{}
	params.Set("lat", strconv.FormatFloat(p.Lat, 'f', 4, 64))
	params.Set("lon", strconv.FormatFloat(p.Lon, 'f', 4, 64))
	params.Set("peakpower", strconv.FormatFloat(p.PeakPower, 'f', 3, 64))
	params.Set("angle", strconv.FormatFloat(p.Angle, 'f', 1, 64))
	params.Set("aspect", strconv.FormatFloat(p.Aspect, 'f', 1, 64))
	params.Set("loss", strconv.FormatFloat(p.Loss, 'f', 1, 64))
	params.Set("mountingplace", p.Mounting)
	params.Set("pvtechchoice", p.Tech)
	params.Set("outputformat", "json")

	req, err := http.NewRequest(http.MethodGet, pvgisURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "solar-tariff-compare/0.1")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("consulta PVGIS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("PVGIS HTTP %d: %s", resp.StatusCode, string(body))
	}
	var pr pvgisResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, fmt.Errorf("decodifica PVGIS: %w", err)
	}
	return aggregaPerfil(pr.Outputs.Hourly, p), nil
}

// aggregaPerfil converteix les hores de la sèrie històrica de PVGIS en un perfil
// mes×hora mitjà (kWh). Com que PVGIS pot retornar múltiples anys (fins a ~19),
// fer la mitjana per mes×hora suavitza la variabilitat interanual.
//
// Energia horària (kWh) = PeakPower_kW × G(i)[W/m²]/1000 × (1 − loss/100).
// (Aproximació lineal a STC; ignora la degradació per temperatura, prou precisa
// per a una estimació de perfil.)
func aggregaPerfil(hores []pvgisHour, p PVGISParams) *PerfilProd {
	perfil := &PerfilProd{}
	loss := 0.86 // (1 − 14/100) per defecte
	if p.Loss > 0 {
		loss = 1 - p.Loss/100
	}
	var suma [12][24]float64
	var comte [12][24]int
	for _, h := range hores {
		mes, hora, ok := parsePVGISTime(h.Time)
		if !ok {
			continue
		}
		kwh := p.PeakPower * h.GI / 1000.0 * loss
		suma[mes][hora] += kwh
		comte[mes][hora]++
	}
	for m := 0; m < 12; m++ {
		for hr := 0; hr < 24; hr++ {
			if comte[m][hr] > 0 {
				perfil.ByMonthHour[m][hr] = suma[m][hr] / float64(comte[m][hr])
				perfil.AnualKWh += suma[m][hr] / float64(comte[m][hr]) * float64(comte[m][hr]) // = suma
			}
		}
	}
	// Anual = suma de totes les hores del perfil mitjà × (dies representatius)
	// Però és més net: Anual = suma directa del TMY promocionat. Recalculem com a
	// perfil mitjà anual (8760 h amb el perfil promig):
	perfil.AnualKWh = 0
	for m := 0; m < 12; m++ {
		dies := diesPerMes(m + 1)
		for hr := 0; hr < 24; hr++ {
			perfil.AnualKWh += perfil.ByMonthHour[m][hr] * float64(dies)
		}
	}
	return perfil
}

// diesPerMes retorna els dies de cada mes (sense any de traspàs, prou precís).
func diesPerMes(m int) int {
	return []int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}[m-1]
}

// parsePVGISTime interpreta "YYYYMMDD:HHMM" -> (mes 0-11, hora 0-23).
func parsePVGISTime(s string) (mes, hora int, ok bool) {
	// Format: 20210101:0010  (any,mes,dia : hora,min)
	if len(s) < 11 {
		return 0, 0, false
	}
	m, err1 := strconv.Atoi(s[4:6])
	d, _ := strconv.Atoi(s[6:8])
	hhmm := s[9:13]
	hh, err2 := strconv.Atoi(hhmm[:2])
	_ = d
	if err1 != nil || err2 != nil || m < 1 || m > 12 {
		return 0, 0, false
	}
	// PVGIS marca l'inici de l'interval; hours poden anar 0-23
	if hh < 0 || hh > 23 {
		return 0, 0, false
	}
	return m - 1, hh, true
}

// ResultatAutoconsum és el resultat d'overlay de producció FV sobre consum real.
type ResultatAutoconsum struct {
	ProduccioKWh  float64 // producció FV total estimada
	AutoconsumKWh float64 // producció consumida in situ
	ExcedentsKWh  float64 // producció bolcada a la xarxa
	IndexAutocons float64 // ratio autoconsum = autoconsum / producció (0..1)
	Cobertura     float64 // ratio de consum cobert per FV = autoconsum / consum
}

// OverlayProduccio aplica un perfil FV (mes×hora) sobre la corba de consum real
// i calcula l'autoconsum i els excedents hora a hora.
func OverlayProduccio(consum map[time.Time]float64, perfil *PerfilProd) ResultatAutoconsum {
	var r ResultatAutoconsum
	for t, c := range consum {
		prod := perfil.ByMonthHour[int(t.Month())-1][t.Hour()]
		if prod <= 0 {
			continue
		}
		r.ProduccioKWh += prod
		auto := prod
		if auto > c {
			auto = c // només es pot autoconsumir fins al consum de l'hora
		}
		r.AutoconsumKWh += auto
		r.ExcedentsKWh += prod - auto
	}
	if r.ProduccioKWh > 0 {
		r.IndexAutocons = r.AutoconsumKWh / r.ProduccioKWh
	}
	var consumTotal float64
	for _, c := range consum {
		consumTotal += c
	}
	if consumTotal > 0 {
		r.Cobertura = r.AutoconsumKWh / consumTotal
	}
	return r
}

// Aplicar expandeix un perfil mes×hora a una corba horària alineada amb les
// marques temporals de la corba de consum (per poder fer l'overlay hora a hora).
func (p *PerfilProd) Aplicar(consum map[time.Time]float64) map[time.Time]float64 {
	out := make(map[time.Time]float64, len(consum))
	for t := range consum {
		out[t] = p.ByMonthHour[int(t.Month())-1][t.Hour()]
	}
	return out
}

// OverlayCurves computa l'autoconsum i els excedents de dues corbes horàries
// (consum i producció reals). Útil quan es té un CSV de producció real.
func OverlayCurves(consum, prod map[time.Time]float64) ResultatAutoconsum {
	var r ResultatAutoconsum
	for t, c := range consum {
		p := prod[t]
		r.ProduccioKWh += p
		auto := p
		if auto > c {
			auto = c
		}
		r.AutoconsumKWh += auto
		r.ExcedentsKWh += p - auto
	}
	if r.ProduccioKWh > 0 {
		r.IndexAutocons = r.AutoconsumKWh / r.ProduccioKWh
	}
	var consumTotal float64
	for _, c := range consum {
		consumTotal += c
	}
	if consumTotal > 0 {
		r.Cobertura = r.AutoconsumKWh / consumTotal
	}
	return r
}

// ProduccioTotal de la corba = autoconsum + excedents per cada hora (dades reals
// de CCH amb les columnes AS_KWh i AE_AUTOCONS_kWh).
func (c Corba) ProduccioTotal() map[time.Time]float64 {
	out := make(map[time.Time]float64)
	for t, v := range c.Autoconsum {
		out[t] += v
	}
	for t, v := range c.Excedents {
		out[t] += v
	}
	return out
}
