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

// CNMCAPI és el client de l'API pública del comparador de la CNMC.
// No requereix autenticació ni cookies. Vegeu CNMC-API-INVESTIGACIO.md.
const cnmcBaseURL = "https://comparador.cnmc.gob.es/api/publico/ofertas/electricidad"

// ConsumAnalisi és el resultat d'agregar una corba horària en els 6 períodes
// (P1..P6) del comparador. Per a 2.0TD només s'usen P1/P2/P3; P4..P6 queden a 0.
type ConsumAnalisi struct {
	Anual float64 // kWh totals anuals
	P1    float64 // kWh P1 Punta
	P2    float64 // kWh P2 Llano
	P3    float64 // kWh P3 Valle
	// Autoconsum: kWh produïts per la FV i consumits en mateix instant.
	AutoconsumKWh float64
}

// Query defineix els paràmetres significatius de la consulta al comparador.
// La resta de camps auxiliars (*Qr, *Orig, imp*, etc.) s'omplen internament a 0.
type Query struct {
	CodigoPostal    string  // sense el 0 inicial (p.ex. "8001")
	Potencia        float64 // kW contractats (2.0TD: potència única per P1/P2/P3)
	Consum          ConsumAnalisi
	Autoconsum      bool    // true si hi ha autoconsum FV
	EnergiaAutocons float64 // kWh autoconsumits (ja inclòs a Consum.AutoconsumKWh normalment)
	Festius         HolidayCalendar
	// Periode de facturació (per defecte: últims 365 dies fins avui).
	Inici time.Time
	Fi    time.Time
}

// Offer és una oferta del comparador. Els camps que poden ser null a l'API usen
// punters. Només es declaren els camps útils; la resta s'ignora (Go descarta camps
// JSON desconeguts per defecte).
type Offer struct {
	ID                int64    `json:"id"`
	IDComercialtz     int64    `json:"idComercializadora"`
	Comercializadora  string   `json:"comercializadora"`
	Oferta            string   `json:"oferta"`
	Tipo              *string  `json:"tipo"` // pot ser null
	TipoElectricidad  string   `json:"tipoElectricidad"`
	TipoRevision      int      `json:"tipoRevision"` // enter
	Tarifa            int      `json:"tarifa"`
	ImportePrimerAnio float64  `json:"importePrimerAnio"`
	ImporteSegundo    *float64 `json:"importeSegundoAnio"` // pot ser null
	Validez           string   `json:"validez"`
	Verde             bool     `json:"verde"`
	Penalizacion      bool     `json:"penalizacion"`
	ServiciosAdic     bool     `json:"serviciosAdicionales"`
	Peaje             string   `json:"peaje"`
	Autoconsum        bool     `json:"autoconsumo"`
}

type cnmcResponse struct {
	ResultadoComparador []Offer `json:"resultadoComparador"`
	ErrorGestor         any     `json:"errorGestor"`
	Consum1             float64 `json:"consumo1"`
	Consum2             float64 `json:"consumo2"`
	Consum3             float64 `json:"consumo3"`
}

// FetchOffers consulta l'API i retorna les ofertes ordenades per import del primer
// any (de més barata a més cara).
func FetchOffers(q Query) ([]Offer, error) {
	if err := q.validate(); err != nil {
		return nil, err
	}
	params := buildParams(q)
	req, err := buildRequest(q, params)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("consulta a la CNMC: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("CNMC HTTP %d: %s", resp.StatusCode, string(body))
	}
	var cr cnmcResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return nil, fmt.Errorf("decodifica resposta CNMC: %w", err)
	}
	// Ordena per import del primer any (NaN/0 al final)
	return cr.ResultadoComparador, nil
}

func (q Query) validate() error {
	if q.CodigoPostal == "" {
		return fmt.Errorf("falta el codi postal")
	}
	if q.Potencia <= 0 {
		return fmt.Errorf("la potència ha de ser > 0")
	}
	return nil
}

// buildRequest construeix la petició HTTP GET a la CNMC.
func buildRequest(q Query, params url.Values) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodGet, cnmcBaseURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "solar-tariff-compare/0.1 (github.com/pmontp19/solar-tariff-compare)")
	return req, nil
}

// buildParams construeix la query string completa amb tots els camps requerits
// per l'API (els auxiliars a 0; l'API retorna 500 si en manquen).
func buildParams(q Query) url.Values {
	p := url.Values{}
	// Dates per defecte: últim any
	inici, fi := q.Inici, q.Fi
	if fi.IsZero() {
		fi = time.Now()
	}
	if inici.IsZero() {
		inici = fi.AddDate(-1, 0, 0)
	}

	p.Set("tipoSuministro", "E")
	p.Set("codigoPostal", q.CodigoPostal)
	p.Set("tarifa", "4") // 4 = peatge 2.0TD

	// Potències (igual per P1/P2/P3 en 2.0TD)
	setPot := func(key string, v float64) { p.Set(key, strconv.FormatFloat(v, 'f', -1, 64)) }
	setPot("potencia", q.Potencia)
	setPot("potenciaPrimeraFranja", q.Potencia)
	setPot("potenciaSegundaFranja", q.Potencia)
	setPot("potenciaTerceraFranja", q.Potencia)
	setPot("potenciaCuartaFranja", 0)
	setPot("potenciaQuintaFranja", 0)
	setPot("potenciaSextaFranja", 0)

	// Consum per període. La CNMC exigeix kWh ENTERS (decimals → HTTP 400),
	// així que arrodonim. La potència sí admet decimals.
	round := func(v float64) int { return int(v + 0.5) }
	setF := func(key string, v float64) { p.Set(key, strconv.Itoa(round(v))) }
	setF("consumoAnualE", q.Consum.Anual)
	setF("consumoAnualEOrig", q.Consum.Anual)
	setF("consumoPrimeraFranja", q.Consum.P1)
	setF("consumoSegundaFranja", q.Consum.P2)
	setF("consumoTerceraFranja", q.Consum.P3)
	setF("consumoCuartaFranja", 0)
	setF("consumoQuintaFranja", 0)
	setF("consumoSextaFranja", 0)

	// Versions QR (totes 0)
	for _, k := range []string{
		"consumoAnualEQr", "consumoPrimeraFranjaQr", "consumoSegundaFranjaQr", "consumoTerceraFranjaQr",
		"consumoCuartaFranjaQr", "consumoQuintaFranjaQr", "consumoSextaFranjaQr",
		"consumoAnualEPQr", "consumoPrimeraFranjaPQr", "consumoSegundaFranjaPQr", "consumoTerceraFranjaPQr",
		"consumoCuartaFranjaPQr", "consumoQuintaFranjaPQr", "consumoSextaFranjaPQr",
	} {
		p.Set(k, "0")
	}

	// Autoconsum
	ea := q.EnergiaAutocons
	if ea == 0 {
		ea = q.Consum.AutoconsumKWh
	}
	setF("energiaAutoconsumo", ea)
	setPot("potenciaAutoconsumo", q.Potencia)
	p.Set("autoconsumo", strconv.FormatBool(q.Autoconsum))

	// Filtres per defecte (cap filtre restrictiu): 2 = "Indiferent/No"
	p.Set("serviciosAdicionales", "2")
	p.Set("permanencia", "2")
	p.Set("revisionPrecios", "2")
	p.Set("perfilConsumo", "13") // 13 = Estàndard 2.0TD
	p.Set("cups", "0000")
	p.Set("vivienda", "true")
	p.Set("factura", "true")
	p.Set("consumoAnualG", "0")
	p.Set("consumoAnualGOrig", "0")
	p.Set("idAuditoriaQR", "0")

	// Dates (epoch ms)
	p.Set("dateInicio", strconv.FormatInt(inici.UnixMilli(), 10))
	p.Set("dateFin", strconv.FormatInt(fi.UnixMilli(), 10))
	p.Set("fFact", strconv.FormatInt(fi.UnixMilli(), 10))

	// Camps auxiliars a 0 (l'API els espera presents)
	for _, k := range []string{
		"importe", "tc", "bs", "impSA", "impOtros", "exc", "reg", "mecanismoAjuste",
		"importeMecanismoAjustePunta", "importeMecanismoAjusteLlano", "importeMecanismoAjusteValle",
		"precioConsumoMecanismoAjustePunta", "precioConsumoMecanismoAjusteLlano", "precioConsumoMecanismoAjusteValle",
		"precioConsumoMecanismoAjusteTotal", "mecanismoAjusteIVA", "impOtrosConIE", "impOtrosSinIE",
		"pmaxP1", "pmaxP2", "dtoBS", "finBS", "ajuste", "impPot", "impEner", "dto",
		"prP1", "prP2", "prE1", "prE2", "prE3", "cfP1flex", "cfP2flex", "cambio", "promo",
		"verde", "rev", "trampeo",
	} {
		p.Set(k, "0")
	}
	return p
}
