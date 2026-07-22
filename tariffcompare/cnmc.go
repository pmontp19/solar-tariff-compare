package tariffcompare

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// cnmcBaseURL es el endpoint del API pública del comparador de la CNMC.
// No requiere autenticación ni cookies. Véase docs/CNMC-API.md.
const cnmcBaseURL = "https://comparador.cnmc.gob.es/api/publico/ofertas/electricidad"

// ConsumptionSummary es el resultado de agregar una curva horaria en los 6 períodos
// (P1..P6) del comparador. Para 2.0TD sólo se usan P1/P2/P3; P4..P6 quedan a 0.
type ConsumptionSummary struct {
	Annual float64 // kWh anuales totales
	P1     float64 // kWh P1 Punta
	P2     float64 // kWh P2 Llano
	P3     float64 // kWh P3 Valle
	// SelfConsumed: kWh producidos por la FV y consumidos en el mismo instante.
	SelfConsumedKWh float64
}

// Query define los parámetros significativos de la consulta al comparador.
// El resto de campos auxiliares (*Qr, *Orig, imp*, etc.) se rellenan internamente a 0.
type Query struct {
	PostalCode         string  // código postal; el 0 inicial se elimina automáticamente ("08001" → "8001")
	Power              float64 // kW contratados (2.0TD: potencia única para P1/P2/P3)
	Consumption        ConsumptionSummary
	SelfConsumption    bool    // true si hay autoconsumo FV
	SelfConsumedEnergy float64 // kWh autoconsumidos (normalmente ya en Consumption.SelfConsumedKWh)
	// SelfConsumptionPower: potencia FV INSTALADA en kWp (campo potenciaAutoconsumo
	// del comparador). Si es 0 se usa Power como aproximación (comportamiento antiguo),
	// pero la potencia contratada y la instalada no suelen coincidir: pásala si se conoce.
	SelfConsumptionPower float64
	Holidays             HolidayCalendar
	// Período de facturación (por defecto: últimos 365 días hasta hoy).
	Start time.Time
	End   time.Time
}

// Offer es una oferta del comparador. Los campos que pueden ser null en el API
// usan punteros. Sólo se declaran los campos útiles; el resto se ignora (Go
// descarta campos JSON desconocidos por defecto).
type Offer struct {
	ID                int64    `json:"id"`
	IDComercialtz     int64    `json:"idComercializadora"`
	Comercializadora  string   `json:"comercializadora"`
	Oferta            string   `json:"oferta"`
	Tipo              *string  `json:"tipo"` // puede ser null
	TipoElectricidad  string   `json:"tipoElectricidad"`
	TipoRevision      int      `json:"tipoRevision"` // entero
	Tarifa            int      `json:"tarifa"`
	ImportePrimerAnio float64  `json:"importePrimerAnio"`
	ImporteSegundo    *float64 `json:"importeSegundoAnio"` // puede ser null
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

// FetchOffers consulta el API y devuelve las ofertas.
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
		return nil, fmt.Errorf("decodifica respuesta CNMC: %w", err)
	}
	return cr.ResultadoComparador, nil
}

// suspectImporteFactor: una oferta cuyo importePrimerAnio cae por debajo de este
// múltiplo de la mediana se considera NO comparable. La CNMC incluye entradas
// informativas/de referencia (p.ej. "PVPC Histórico de referencia", tipoElectricidad
// "PVPC") cuyo importe se calcula sobre un perfil de referencia y NO escala con tu
// consumo: con consumo alto quedan como valores absurdamente bajos y contaminarían el
// "más barato". Las ofertas reales de 2.0TD están muy agrupadas (peajes y cargos son
// regulados e idénticos), así que un importe por debajo de la mitad de la mediana es un
// artefacto con casi total seguridad.
const suspectImporteFactor = 0.5

// PartitionSuspectOffers separa las ofertas comparables de las sospechosas de ser
// artefactos de la CNMC (importe <= 0, o un importePrimerAnio anómalamente bajo respecto
// a la mediana: no escala con el consumo). Conserva el orden de entrada en ambas listas.
//
// No filtra (devuelve todo como "clean") cuando hay pocas ofertas (<8, sin mediana
// fiable) o cuando demasiadas (>25%) caerían como sospechosas (señal de que la heurística
// no aplica a esta distribución). Así, con un consumo minúsculo —donde todas las ofertas
// quedan cerca del suelo de costes fijos— no se marca nada.
func PartitionSuspectOffers(offers []Offer) (clean, suspect []Offer) {
	const minOffers = 8
	if len(offers) < minOffers {
		return offers, nil
	}
	imps := make([]float64, 0, len(offers))
	for _, o := range offers {
		if o.ImportePrimerAnio > 0 {
			imps = append(imps, o.ImportePrimerAnio)
		}
	}
	if len(imps) < minOffers {
		return offers, nil
	}
	sort.Float64s(imps)
	median := imps[len(imps)/2]
	threshold := suspectImporteFactor * median
	for _, o := range offers {
		if o.ImportePrimerAnio <= 0 || o.ImportePrimerAnio < threshold {
			suspect = append(suspect, o)
		} else {
			clean = append(clean, o)
		}
	}
	// Si "demasiadas" resultan sospechosas, la heurística no aplica: no filtramos.
	if len(suspect)*4 > len(offers) {
		return offers, nil
	}
	return clean, suspect
}

func (q Query) validate() error {
	if q.PostalCode == "" {
		return fmt.Errorf("falta el código postal")
	}
	if q.Power <= 0 {
		return fmt.Errorf("la potencia debe ser > 0")
	}
	return nil
}

// buildRequest construye la petición HTTP GET a la CNMC.
func buildRequest(q Query, params url.Values) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodGet, cnmcBaseURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "solar-tariff-compare/0.1 (github.com/pmontp19/solar-tariff-compare)")
	return req, nil
}

// buildParams construye la query string completa con todos los campos requeridos
// por el API (los auxiliares a 0; el API devuelve 500 si falta alguno).
func buildParams(q Query) url.Values {
	p := url.Values{}
	// Fechas por defecto: último año
	start, end := q.Start, q.End
	if end.IsZero() {
		end = time.Now()
	}
	if start.IsZero() {
		start = end.AddDate(-1, 0, 0)
	}

	p.Set("tipoSuministro", "E")
	// El API espera el CP sin el 0 inicial ("08001" → "8001"); normalizamos aquí
	// para que el llamante pueda pasar el CP tal como lo escribe el usuario.
	p.Set("codigoPostal", strings.TrimLeft(q.PostalCode, "0"))
	p.Set("tarifa", "4") // 4 = peaje 2.0TD

	// Potencias (igual para P1/P2/P3 en 2.0TD)
	setPot := func(key string, v float64) { p.Set(key, strconv.FormatFloat(v, 'f', -1, 64)) }
	setPot("potencia", q.Power)
	setPot("potenciaPrimeraFranja", q.Power)
	setPot("potenciaSegundaFranja", q.Power)
	setPot("potenciaTerceraFranja", q.Power)
	setPot("potenciaCuartaFranja", 0)
	setPot("potenciaQuintaFranja", 0)
	setPot("potenciaSextaFranja", 0)

	// Consumo por período. La CNMC exigece kWh ENTEROS (decimales -> HTTP 400),
	// así que redondeamos. La potencia sí admite decimales.
	round := func(v float64) int { return int(v + 0.5) }
	setF := func(key string, v float64) { p.Set(key, strconv.Itoa(round(v))) }
	setF("consumoAnualE", q.Consumption.Annual)
	setF("consumoAnualEOrig", q.Consumption.Annual)
	setF("consumoPrimeraFranja", q.Consumption.P1)
	setF("consumoSegundaFranja", q.Consumption.P2)
	setF("consumoTerceraFranja", q.Consumption.P3)
	setF("consumoCuartaFranja", 0)
	setF("consumoQuintaFranja", 0)
	setF("consumoSextaFranja", 0)

	// Versiones QR (todas 0)
	for _, k := range []string{
		"consumoAnualEQr", "consumoPrimeraFranjaQr", "consumoSegundaFranjaQr", "consumoTerceraFranjaQr",
		"consumoCuartaFranjaQr", "consumoQuintaFranjaQr", "consumoSextaFranjaQr",
		"consumoAnualEPQr", "consumoPrimeraFranjaPQr", "consumoSegundaFranjaPQr", "consumoTerceraFranjaPQr",
		"consumoCuartaFranjaPQr", "consumoQuintaFranjaPQr", "consumoSextaFranjaPQr",
	} {
		p.Set(k, "0")
	}

	// Autoconsumo
	ea := q.SelfConsumedEnergy
	if ea == 0 {
		ea = q.Consumption.SelfConsumedKWh
	}
	setF("energiaAutoconsumo", ea)
	// potenciaAutoconsumo = potencia FV instalada (kWp), no la contratada. Si no se
	// conoce (p.ej. sólo Datadis, sin -kwp) se aproxima con la contratada.
	pa := q.SelfConsumptionPower
	if pa <= 0 {
		pa = q.Power
	}
	setPot("potenciaAutoconsumo", pa)
	p.Set("autoconsumo", strconv.FormatBool(q.SelfConsumption))

	// Filtros por defecto (sin filtro restrictivo): 2 = "Indiferente/No"
	p.Set("serviciosAdicionales", "2")
	p.Set("permanencia", "2")
	p.Set("revisionPrecios", "2")
	p.Set("perfilConsumo", "13") // 13 = Estándar 2.0TD
	p.Set("cups", "0000")
	p.Set("vivienda", "true")
	p.Set("factura", "true")
	p.Set("consumoAnualG", "0")
	p.Set("consumoAnualGOrig", "0")
	p.Set("idAuditoriaQR", "0")

	// Fechas (epoch ms)
	p.Set("dateInicio", strconv.FormatInt(start.UnixMilli(), 10))
	p.Set("dateFin", strconv.FormatInt(end.UnixMilli(), 10))
	p.Set("fFact", strconv.FormatInt(end.UnixMilli(), 10))

	// Campos auxiliares a 0 (el API los espera presentes)
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
