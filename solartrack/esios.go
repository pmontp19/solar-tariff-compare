package solartrack

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"time"
)

// e-sios (Red Eléctrica) ofrece datos públicos del mercado eléctrico español.
// Indicadores relevantes:
//   - 1001: Término de facturación de energía activa del PVPC 2.0TD (precio consumo)
//   - 1739: Precio de la energía excedentaria del autoconsumo (precio compensación)
//
// Sin token sólo se pueden consultar los valores más recientes (sin rango de
// fechas). Con un token personal gratuito (ESIOS_TOKEN) se puede descargar histórico.
// Obtén el token en https://api.esios.ree.es (registro gratuito).
const (
	esiosBase        = "https://api.esios.ree.es/indicators"
	indicadorPVPC    = 1001
	indicadorExced   = 1739
	geoPeninsulaPVPC = 8741 // primera zona peninsular del PVPC 2.0TD
	geoPeninsulaExc  = 3    // península para excedentes
)

// HourlySeries es una serie de precios horarios en €/kWh (ya convertidos desde €/MWh).
type HourlySeries struct {
	ByDay  map[string][24]float64 // clave "YYYY-MM-DD" -> 24 precios (€/kWh)
	Source string                 // "token" (histórico) o "latest" (sólo último día, sin token)
}

// HourlyPrices contiene las dos series necesarias para simular la factura.
type HourlyPrices struct {
	PVPC    HourlySeries // precio de consumo (indicador 1001)
	Surplus HourlySeries // precio de compensación de excedentes (indicador 1739)
}

// FetchHourlyPrices descarga las series PVPC y de excedentes para el rango de fechas.
// Si no hay ESIOS_TOKEN, sólo se puede obtener el último día disponible (Source="latest").
func FetchHourlyPrices(start, end time.Time) (*HourlyPrices, error) {
	token := os.Getenv("ESIOS_TOKEN")
	if token == "" && (!start.IsZero() || !end.IsZero()) {
		fmt.Fprintln(os.Stderr, "aviso: sin ESIOS_TOKEN sólo se puede obtener el último día; la simulación usará este perfil horario como representativo de todo el año.")
	}
	pvpc, err := fetchIndicador(indicadorPVPC, geoPeninsulaPVPC, start, end, token)
	if err != nil {
		return nil, fmt.Errorf("PVPC: %w", err)
	}
	exc, err := fetchIndicador(indicadorExced, geoPeninsulaExc, start, end, token)
	if err != nil {
		return nil, fmt.Errorf("excedentes: %w", err)
	}
	return &HourlyPrices{PVPC: pvpc, Surplus: exc}, nil
}

func fetchIndicador(indicador, geo int, start, end time.Time, token string) (HourlySeries, error) {
	u, _ := url.Parse(fmt.Sprintf("%s/%d", esiosBase, indicador))
	q := u.Query()
	hist := token != "" && !start.IsZero() && !end.IsZero()
	if hist {
		q.Set("start_date", start.Format("2006-01-02T15:04"))
		q.Set("end_date", end.Format("2006-01-02T15:04"))
	}
	// NOTA: no añadimos geo_ids[] — e-sios devuelve 403 para algunos indicadores
	// (1739) al filtrar por geo sin token. Obtenemos todas las zonas y filtramos
	// client-side.
	u.RawQuery = q.Encode()

	// e-sios (detrás de un WAF) bloquea por fingerprint TLS el ClientHello de Go
	// (http.Get devuelve 403) pero acepta curl. Para evitar dependencias Go (uTLS),
	// delegamos la descarga a curl, presente en macOS/Linux/Windows 10+.
	args := []string{"-sS", "--max-time", "30", "-H", "Accept: application/json",
		"-A", "solar-tariff-compare/0.1"}
	if token != "" {
		args = append(args, "-H", "x-api-key: "+token)
	}
	args = append(args, u.String())
	cmd := exec.Command("curl", args...)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return HourlySeries{}, fmt.Errorf("curl e-sios (requiere curl instalado): %w", err)
	}
	var body struct {
		Status    int    `json:"Status"`
		Message   string `json:"message"`
		Indicator struct {
			Values []struct {
				Datetime string  `json:"datetime"`
				Value    float64 `json:"value"` // €/MWh
				GeoID    int     `json:"geo_id"`
			} `json:"values"`
		} `json:"indicator"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		return HourlySeries{}, fmt.Errorf("decodifica: %w (cuerpo: %.200s)", err, string(out))
	}
	if body.Status == 403 || body.Message == "Forbidden" {
		return HourlySeries{}, fmt.Errorf("e-sios 403 para indicador %d (prueba con ESIOS_TOKEN)", indicador)
	}

	s := HourlySeries{ByDay: map[string][24]float64{}}
	if hist {
		s.Source = "token"
	} else {
		s.Source = "latest"
	}
	loc, _ := time.LoadLocation("Europe/Madrid")
	for _, v := range body.Indicator.Values {
		if v.GeoID != geo {
			continue
		}
		t, err := time.Parse("2006-01-02T15:04:05.000-07:00", v.Datetime)
		if err != nil {
			// formato alternativo
			t, err = time.Parse(time.RFC3339, v.Datetime)
			if err != nil {
				continue
			}
		}
		t = t.In(loc)
		dia := t.Format("2006-01-02")
		hora := t.Hour()
		if hora < 0 || hora > 23 {
			continue
		}
		arr := s.ByDay[dia]
		arr[hora] = v.Value / 1000.0 // €/MWh -> €/kWh
		s.ByDay[dia] = arr
	}
	if len(s.ByDay) == 0 {
		return HourlySeries{}, fmt.Errorf("ningún valor para el indicador %d (geo %d)", indicador, geo)
	}
	return s, nil
}

// AverageHourlyProfile promedia una serie diaria en un perfil 24h medio (€/kWh por
// hora del día). Útil cuando sólo se dispone de un día representativo o para
// sintetizar un año.
func (s HourlySeries) AverageHourlyProfile() [24]float64 {
	var suma [24]float64
	var cuenta [24]int
	for _, arr := range s.ByDay {
		for h := 0; h < 24; h++ {
			if arr[h] > 0 {
				suma[h] += arr[h]
				cuenta[h]++
			}
		}
	}
	var perfil [24]float64
	for h := 0; h < 24; h++ {
		if cuenta[h] > 0 {
			perfil[h] = suma[h] / float64(cuenta[h])
		}
	}
	return perfil
}
