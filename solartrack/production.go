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

// PVGIS es el API de la Comisión Europea para estimar producción fotovoltaica.
// Véase https://joint-research-centre.ec.europa.eu/pvgis-photovoltaic-geographical-information-system_en
const pvgisURL = "https://re.jrc.ec.europa.eu/api/seriescalc"

// PVGISParams define una instalación FV para estimar su producción horaria.
type PVGISParams struct {
	Lat       float64 // Latitud (p.ej. 41.38 para Barcelona)
	Lon       float64 // Longitud (p.ej. 2.17)
	PeakPower float64 // Potencia de pico en kW (p.ej. 3.5 → ~10 paneles de 350W)
	Angle     float64 // Inclinación en grados (0=plano, 35=típico residencial)
	Aspect    float64 // Orientación en grados (0=sur, -90=este, 90=oeste)
	Loss      float64 // Pérdidas del sistema en % (14 es un valor típico por defecto)
	Mounting  string  // "free" (suelo/techo) o "building" (integrado)
	Tech      string  // "crystSi" (silicio cristalino), "CIS", "CdTe"
}

// ProductionProfile es la producción horaria estimada (kWh) indexada por
// (mes, hora-del-día). Se deriva de un TMY (año meteorológico típico) de PVGIS.
type ProductionProfile struct {
	ByMonthHour [12][24]float64 // kWh medios por mes (0=ene) y hora (0-23)
	AnnualKWh   float64
}

// pvgisHour es una fila horaria de la respuesta de PVGIS. PVGIS seriescalc
// devuelve irradiancia G(i) en W/m² en el plano (no la potencia), por lo que la
// calculamos a partir de PeakPower y las pérdidas.
type pvgisHour struct {
	Time string  `json:"time"` // "YYYYMMDD:HHMM"
	GI   float64 `json:"G(i)"` // Irradiancia global en el plano [W/m²]
}

type pvgisResponse struct {
	Outputs struct {
		Hourly []pvgisHour `json:"hourly"`
	} `json:"outputs"`
}

// FetchPVGISProfile descarga el TMY horario de PVGIS y lo agrega en un perfil
// mes×hora (kWh medios para cada combinación de mes y hora del día).
func FetchPVGISProfile(p PVGISParams) (*ProductionProfile, error) {
	if p.PeakPower <= 0 {
		return nil, fmt.Errorf("peakpower debe ser > 0")
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
	return aggregateProfile(pr.Outputs.Hourly, p), nil
}

// aggregateProfile convierte las horas de la serie histórica de PVGIS en un perfil
// mes×hora medio (kWh). Como PVGIS puede devolver varios años (hasta ~19), hacer la
// media por mes×hora suaviza la variabilidad interanual.
//
// Energía horaria (kWh) = PeakPower_kW × G(i)[W/m²]/1000 × (1 − loss/100).
// (Aproximación lineal a STC; ignora la degradación por temperatura, suficientemente
// precisa para una estimación de perfil.)
func aggregateProfile(horas []pvgisHour, p PVGISParams) *ProductionProfile {
	perfil := &ProductionProfile{}
	loss := 0.86 // (1 − 14/100) por defecto
	if p.Loss > 0 {
		loss = 1 - p.Loss/100
	}
	var suma [12][24]float64
	var cuenta [12][24]int
	for _, h := range horas {
		mes, hora, ok := parsePVGISTime(h.Time)
		if !ok {
			continue
		}
		kwh := p.PeakPower * h.GI / 1000.0 * loss
		suma[mes][hora] += kwh
		cuenta[mes][hora]++
	}
	for m := 0; m < 12; m++ {
		for hr := 0; hr < 24; hr++ {
			if cuenta[m][hr] > 0 {
				perfil.ByMonthHour[m][hr] = suma[m][hr] / float64(cuenta[m][hr])
			}
		}
	}
	// Anual = perfil medio (8760 h) ponderado por días de cada mes.
	perfil.AnnualKWh = 0
	for m := 0; m < 12; m++ {
		days := daysInMonth(m + 1)
		for hr := 0; hr < 24; hr++ {
			perfil.AnnualKWh += perfil.ByMonthHour[m][hr] * float64(days)
		}
	}
	return perfil
}

// daysInMonth devuelve los días de cada mes (sin año bisiesto, suficientemente preciso).
func daysInMonth(m int) int {
	return []int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}[m-1]
}

// parsePVGISTime interpreta "YYYYMMDD:HHMM" -> (mes 0-11, hora 0-23).
func parsePVGISTime(s string) (mes, hora int, ok bool) {
	// Formato: 20210101:0010  (año,mes,día : hora,min) — 13 caracteres mínimo;
	// el slice s[9:13] de más abajo entraría en pánico con cadenas más cortas.
	if len(s) < 13 {
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
	// PVGIS marca el inicio del intervalo; las horas van de 0 a 23.
	if hh < 0 || hh > 23 {
		return 0, 0, false
	}
	return m - 1, hh, true
}

// AutoconsumptionResult es el resultado de superponer producción FV sobre consumo real.
type AutoconsumptionResult struct {
	ProductionKWh   float64 // producción FV total estimada
	SelfConsumedKWh float64 // producción consumida in situ
	SurplusKWh      float64 // producción vertida a la red
	SelfConsumRatio float64 // ratio de autoconsumo = autoconsumo / producción (0..1)
	Coverage        float64 // ratio de consumo cubierto por FV = autoconsumo / consumo
}

// OverlayProduction aplica un perfil FV (mes×hora) sobre la curva de consumo real
// y calcula el autoconsumo y los excedentes hora a hora.
func OverlayProduction(consumption map[time.Time]float64, perfil *ProductionProfile) AutoconsumptionResult {
	var r AutoconsumptionResult
	for t, c := range consumption {
		prod := perfil.ByMonthHour[int(t.Month())-1][t.Hour()]
		if prod <= 0 {
			continue
		}
		r.ProductionKWh += prod
		auto := prod
		if auto > c {
			auto = c // sólo se puede autoconsumir hasta el consumo de la hora
		}
		r.SelfConsumedKWh += auto
		r.SurplusKWh += prod - auto
	}
	if r.ProductionKWh > 0 {
		r.SelfConsumRatio = r.SelfConsumedKWh / r.ProductionKWh
	}
	var totalConsumption float64
	for _, c := range consumption {
		totalConsumption += c
	}
	if totalConsumption > 0 {
		r.Coverage = r.SelfConsumedKWh / totalConsumption
	}
	return r
}

// Apply expande un perfil mes×hora a una curva horaria alineada con las marcas
// temporales de la curva de consumo (para poder hacer el overlay hora a hora).
func (p *ProductionProfile) Apply(consumption map[time.Time]float64) map[time.Time]float64 {
	out := make(map[time.Time]float64, len(consumption))
	for t := range consumption {
		out[t] = p.ByMonthHour[int(t.Month())-1][t.Hour()]
	}
	return out
}

// OverlayCurves calcula el autoconsumo y los excedentes de dos curvas horarias
// (consumo y producción reales). Útil cuando se dispone de un CSV de producción real.
func OverlayCurves(consumption, production map[time.Time]float64) AutoconsumptionResult {
	var r AutoconsumptionResult
	for t, c := range consumption {
		p := production[t]
		r.ProductionKWh += p
		auto := p
		if auto > c {
			auto = c
		}
		r.SelfConsumedKWh += auto
		r.SurplusKWh += p - auto
	}
	if r.ProductionKWh > 0 {
		r.SelfConsumRatio = r.SelfConsumedKWh / r.ProductionKWh
	}
	var totalConsumption float64
	for _, c := range consumption {
		totalConsumption += c
	}
	if totalConsumption > 0 {
		r.Coverage = r.SelfConsumedKWh / totalConsumption
	}
	return r
}

// TotalProduction de la curva = autoconsumo + excedentes para cada hora (datos
// reales de CCH con las columnas AS_KWh y AE_AUTOCONS_kWh).
func (c LoadCurve) TotalProduction() map[time.Time]float64 {
	out := make(map[time.Time]float64)
	for t, v := range c.SelfConsumed {
		out[t] += v
	}
	for t, v := range c.Surplus {
		out[t] += v
	}
	return out
}
