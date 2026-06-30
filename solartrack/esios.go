package solartrack

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"time"
)

// e-sios (Red Eléctrica) ofereix dades públiques del mercat elèctric espanyol.
// Indicadors rellevants:
//   - 1001: Término de facturación de energía activa del PVPC 2.0TD (preu consum)
//   - 1739: Precio de la energía excedentaria del autoconsumo (preu compensació)
//
// Sense token només es poden consultar els valors més recents (sense rang de
// dates). Amb un token personal gratuït (ESIOS_TOKEN) es pot baixar històric.
// Obteniu el token a https://api.esios.ree.es (registre gratuït).
const (
	esiosBase        = "https://api.esios.ree.es/indicators"
	indicadorPVPC    = 1001
	indicadorExced   = 1739
	geoPeninsulaPVPC = 8741 // primera zona peninsular del PVPC 2.0TD
	geoPeninsulaExc  = 3    // península per a excedents
)

// SerieHoraria és una sèrie de preus horaris en €/kWh (ja convertits des d'euros/MWh).
type SerieHoraria struct {
	Dia    map[string][24]float64 // clau "YYYY-MM-DD" -> 24 preus (€/kWh)
	Fuente string                 // "token" (històric) o "latest" (només últim dia, sense token)
}

// PreusHoraris conté les dues sèries necessàries per simular la factura.
type PreusHoraris struct {
	PVPC      SerieHoraria // preu de consum (indicador 1001)
	Excedents SerieHoraria // preu de compensació d'excedents (indicador 1739)
}

// FetchPreusHoraris descarrega les sèries PVPC i d'excedents per al rang de dates.
// Si no hi ha ESIOS_TOKEN, només es pot obtenir l'últim dia disponible (Fuente="latest")
// i es retorna un error si es demana un rang concret sense token.
func FetchPreusHoraris(inici, fi time.Time) (*PreusHoraris, error) {
	token := os.Getenv("ESIOS_TOKEN")
	if token == "" && (!inici.IsZero() || !fi.IsZero()) {
		fmt.Fprintln(os.Stderr, "avís: sense ESIOS_TOKEN només es pot obtenir l'últim dia; la simulació usarà aquest perfil horari com a representatiu de tot l'any.")
	}
	pvpc, err := fetchIndicador(indicadorPVPC, geoPeninsulaPVPC, inici, fi, token)
	if err != nil {
		return nil, fmt.Errorf("PVPC: %w", err)
	}
	exc, err := fetchIndicador(indicadorExced, geoPeninsulaExc, inici, fi, token)
	if err != nil {
		return nil, fmt.Errorf("excedents: %w", err)
	}
	return &PreusHoraris{PVPC: pvpc, Excedents: exc}, nil
}

func fetchIndicador(indicador, geo int, inici, fi time.Time, token string) (SerieHoraria, error) {
	u, _ := url.Parse(fmt.Sprintf("%s/%d", esiosBase, indicador))
	q := u.Query()
	hist := token != "" && !inici.IsZero() && !fi.IsZero()
	if hist {
		q.Set("start_date", inici.Format("2006-01-02T15:04"))
		q.Set("end_date", fi.Format("2006-01-02T15:04"))
	}
	// NOTA: no afegim geo_ids[] — e-sios retorna 403 per a alguns indicadors (1739)
	// en filtrar per geo sense token. Obtenim totes les zones i filtrem client-side.
	u.RawQuery = q.Encode()

	// e-sios (darrere d'un WAF) bloqueja per fingerprint TLS el ClientHello de Go
	// (http.Get retorna 403) però accepta curl. Per evitar dependències Go (uTLS),
	// deleguem la descàrrega a curl, present a macOS/Linux/Windows 10+.
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
		return SerieHoraria{}, fmt.Errorf("curl e-sios (cal curl instal·lat): %w", err)
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
		return SerieHoraria{}, fmt.Errorf("decodifica: %w (cos: %.200s)", err, string(out))
	}
	if body.Status == 403 || body.Message == "Forbidden" {
		return SerieHoraria{}, fmt.Errorf("e-sios 403 per indicador %d (proveu amb ESIOS_TOKEN)", indicador)
	}

	s := SerieHoraria{Dia: map[string][24]float64{}}
	if hist {
		s.Fuente = "token"
	} else {
		s.Fuente = "latest"
	}
	loc, _ := time.LoadLocation("Europe/Madrid")
	for _, v := range body.Indicator.Values {
		if v.GeoID != geo {
			continue
		}
		t, err := time.Parse("2006-01-02T15:04:05.000-07:00", v.Datetime)
		if err != nil {
			// format alternatiu
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
		arr := s.Dia[dia]
		arr[hora] = v.Value / 1000.0 // €/MWh -> €/kWh
		s.Dia[dia] = arr
	}
	if len(s.Dia) == 0 {
		return SerieHoraria{}, fmt.Errorf("cap valor per a l'indicador %d (geo %d)", indicador, geo)
	}
	return s, nil
}

// PerfilMigHorari promitja una sèrie diària en un perfil 24h mitjà (€/kWh per hora
// del dia). Útil quan només es disposa d'un dia representatiu o per sintetitzar un any.
func (s SerieHoraria) PerfilMigHorari() [24]float64 {
	var suma [24]float64
	var comte [24]int
	for _, arr := range s.Dia {
		for h := 0; h < 24; h++ {
			if arr[h] > 0 {
				suma[h] += arr[h]
				comte[h]++
			}
		}
	}
	var perfil [24]float64
	for h := 0; h < 24; h++ {
		if comte[h] > 0 {
			perfil[h] = suma[h] / float64(comte[h])
		}
	}
	return perfil
}
